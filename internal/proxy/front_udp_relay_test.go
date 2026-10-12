package proxy

// 本文件锁死审查 #18：SOCKS5 UDP 中继的 relay 地址**必须先解析成 IP**，
// 解析不出来必须明确报错 —— 绝不允许像旧实现那样把域名静默换成 0.0.0.0。
//
// 【旧实现为什么危险】两处调用点都写的是：
//
//	udpAddr := &net.UDPAddr{IP: net.ParseIP(relayHost), Port: relayPort}
//	if udpAddr.IP == nil { udpAddr.IP = net.IPv4zero }   // ← 域名在这里被静默吃掉
//
// net.ParseIP 对域名一律返回 nil，而 relay 主机名**经常**是域名：
//   - RFC 1928 允许服务端在 BND.ADDR 里回域名（ATYP=0x03）；
//   - 服务端回 0.0.0.0 / :: 时会退回配置里的前置（或出口）主机名，
//     用户填的通常就是域名（本仓库 data/config.json 里的出口节点就是域名）。
//
// 0.0.0.0 在 Windows 上连**不会报错**：DLL 会把它改写成 127.0.0.1（本机回环），
// 于是所有 SOCKS5 UDP 帧都发给了本机一个没人监听的端口，日志里一行错误都没有，
// 外部只看到"WireGuard 一直发握手包、永远连不上"。所以回归用例必须断言
// "要么解析成真实地址、要么明确报错"，而不是"没崩就算过"。

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/murongruolan/picohop/internal/config"
)

// fakeRelayReply 是假 SOCKS5 服务端要回给客户端的 BND.ADDR / BND.PORT。
// host 既可以是 IP 字面量，也可以是域名（走 ATYP=0x03）。
type fakeRelayReply struct {
	host string
	port int
}

// startFakeUDPAssociateServer 起一个最小 SOCKS5 服务端：只处理一次 UDP ASSOCIATE，
// 按 reply 回一个成功应答，之后保持控制连接不动。
//
// 返回值 ctrlClosed 会在**客户端主动关掉控制连接**时被关闭 —— 用来验证
// "relay 拨号失败时 fail-closed，不把半死会话留给上游"。
func startFakeUDPAssociateServer(t *testing.T, reply fakeRelayReply) (addr string, ctrlClosed <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	closed := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// greeting: VER NMETHODS METHOD，一律回"无需认证"
		head := make([]byte, 3)
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		if _, err := conn.Write([]byte{socks5Version, authNone}); err != nil {
			return
		}

		// 请求: VER CMD RSV ATYP + ADDR + PORT（本客户端固定发 IPv4 通配 0.0.0.0:0）
		cmd := make([]byte, 4)
		if _, err := io.ReadFull(conn, cmd); err != nil {
			return
		}
		if cmd[1] != cmdUDPAssociate {
			return
		}
		addrLen := 0
		switch cmd[3] {
		case atypIPv4:
			addrLen = 4
		case atypIPv6:
			addrLen = 16
		case atypDomain:
			l := make([]byte, 1)
			if _, err := io.ReadFull(conn, l); err != nil {
				return
			}
			addrLen = int(l[0])
		default:
			return
		}
		if _, err := io.ReadFull(conn, make([]byte, addrLen+2)); err != nil {
			return
		}

		resp, err := appendSocks5Addr([]byte{socks5Version, repSuccess, 0x00}, reply.host, reply.port)
		if err != nil {
			return
		}
		if _, err := conn.Write(resp); err != nil {
			return
		}

		// 之后什么都不做：客户端若主动关掉控制连接，这里的读会立刻返回错误。
		if _, err := conn.Read(make([]byte, 1)); err != nil {
			close(closed)
		}
	}()
	return ln.Addr().String(), closed
}

// listenLoopbackPair 在**同一个端口**上同时绑 127.0.0.1 与 ::1（IPv6 环回绑不上时为 nil），
// 这样无论被测代码把域名解析成哪一族，数据报都能被观察到。
func listenLoopbackPair(t *testing.T) (v4, v6 *net.UDPConn, port int) {
	t.Helper()
	v4, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v4.Close() })
	port = v4.LocalAddr().(*net.UDPAddr).Port

	if s, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback, Port: port}); err == nil {
		v6 = s
		t.Cleanup(func() { _ = s.Close() })
	}
	return v4, v6, port
}

// waitRelayFrame 在给定的 relay 套接字上等一帧 SOCKS5 UDP 数据报。
func waitRelayFrame(t *testing.T, conns ...*net.UDPConn) (host string, port int, payload []byte) {
	t.Helper()
	type result struct {
		host    string
		port    int
		payload []byte
		err     error
	}
	ch := make(chan result, len(conns))
	for _, c := range conns {
		if c == nil {
			continue
		}
		go func(c *net.UDPConn) {
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			buf := make([]byte, 2048)
			n, _, err := c.ReadFromUDP(buf)
			if err != nil {
				ch <- result{err: err}
				return
			}
			h, p, body, perr := parseUDPHeader(buf[:n])
			ch <- result{host: h, port: p, payload: body, err: perr}
		}(c)
	}
	deadline := time.After(5 * time.Second)
	for i := 0; i < len(conns); i++ {
		select {
		case r := <-ch:
			if r.err == nil {
				return r.host, r.port, r.payload
			}
		case <-deadline:
			t.Fatalf("relay 套接字一直没收到数据报：UDP 中继没有落在我们监听的地址上（旧实现会静默退化成 0.0.0.0）")
		}
	}
	t.Fatalf("relay 套接字一直没收到数据报：UDP 中继没有落在我们监听的地址上（旧实现会静默退化成 0.0.0.0）")
	return "", 0, nil
}

// TestSocks5FrontUDPRelayDomainMustResolveFailClosed 是本缺陷的**主回归用例**：
// 服务端在 BND.ADDR 里回域名（RFC 1928 允许），且用 RFC 6761 保留的 .invalid
// 保证解析一定失败。此时必须明确报错。
//
// 旧实现这里**不会报错**：ParseIP 返回 nil → 0.0.0.0 → Windows 上被改写成
// 127.0.0.1 → DialUDP 成功 → 返回一条"看起来正常、其实发不出包"的通道。
func TestSocks5FrontUDPRelayDomainMustResolveFailClosed(t *testing.T) {
	srv, ctrlClosed := startFakeUDPAssociateServer(t, fakeRelayReply{
		host: "no-such-host-18.invalid",
		port: 40001,
	})
	host, port, err := SplitHostPort(srv, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := &socks5Front{fc: config.Front{
		Type: config.FrontTypeSocks5, Host: host, Port: port, TimeoutMs: 3000,
	}}

	ch, err := f.DialUDP(context.Background())
	if err == nil {
		_ = ch.Close()
		t.Fatal("relay 主机名解析不了时必须明确报错；旧实现把域名喂给 ParseIP（返回 nil）后静默改成 0.0.0.0，" +
			"在 Windows 上等于把中继指向本机 127.0.0.1")
	}
	if ch != nil {
		t.Fatalf("失败时必须返回 nil 通道，实际 %T", ch)
	}
	if !strings.Contains(err.Error(), "no-such-host-18.invalid") {
		t.Fatalf("报错必须带上解析不了的 relay 主机名，便于用户定位，实际: %v", err)
	}
	select {
	case <-ctrlClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("relay 拨号失败后必须关掉控制连接（fail-closed），不能把半死会话留给上游")
	}
}

// TestSocks5FrontUDPRelayWildcardFallsBackToDomainFrontHost 复现用户报告的场景：
// 服务端回 0.0.0.0（很多 SOCKS5 服务端都这么回）→ 退回配置里的前置主机名，
// 而前置主机名是**域名**。修好之后必须解析成真实地址、数据报真的发得出去。
func TestSocks5FrontUDPRelayWildcardFallsBackToDomainFrontHost(t *testing.T) {
	relay4, relay6, relayPort := listenLoopbackPair(t)
	srv, _ := startFakeUDPAssociateServer(t, fakeRelayReply{host: "0.0.0.0", port: relayPort})
	_, port, err := SplitHostPort(srv, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 前置主机名写成域名 localhost：这是本机唯一能保证解析成功的域名，
	// 但它同样会走"域名必须解析"的那条路径。
	f := &socks5Front{fc: config.Front{
		Type: config.FrontTypeSocks5, Host: "localhost", Port: port, TimeoutMs: 3000,
	}}

	ch, err := f.DialUDP(context.Background())
	if err != nil {
		t.Fatalf("域名前置 + 服务端回 0.0.0.0 时，relay 必须解析成真实地址，实际报错: %v", err)
	}
	defer ch.Close()

	uc, ok := ch.(*socks5UDPChannel)
	if !ok {
		t.Fatalf("期望 *socks5UDPChannel，实际 %T", ch)
	}
	if ip := uc.relay.RemoteAddr().(*net.UDPAddr).IP; ip == nil || ip.IsUnspecified() {
		t.Fatalf("UDP 中继不能落在未指定地址上，实际 remote=%v", uc.relay.RemoteAddr())
	}

	if err := ch.SendToUDP([]byte("ping-18"), "198.51.100.7", 53); err != nil {
		t.Fatalf("SendToUDP 失败: %v", err)
	}
	gotHost, gotPort, gotPayload := waitRelayFrame(t, relay4, relay6)
	if gotHost != "198.51.100.7" || gotPort != 53 || string(gotPayload) != "ping-18" {
		t.Fatalf("relay 收到的帧不对: %s:%d %q", gotHost, gotPort, gotPayload)
	}
}

// TestSocks5FrontUDPRelayDomainInReplyIsResolved 锁死另一条入口：
// 服务端直接在 BND.ADDR 里回域名（ATYP=0x03，RFC 1928 明确允许）。
func TestSocks5FrontUDPRelayDomainInReplyIsResolved(t *testing.T) {
	relay4, relay6, relayPort := listenLoopbackPair(t)
	srv, _ := startFakeUDPAssociateServer(t, fakeRelayReply{host: "localhost", port: relayPort})
	host, port, err := SplitHostPort(srv, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := &socks5Front{fc: config.Front{
		Type: config.FrontTypeSocks5, Host: host, Port: port, TimeoutMs: 3000,
	}}

	ch, err := f.DialUDP(context.Background())
	if err != nil {
		t.Fatalf("BND.ADDR 是域名时必须解析成真实地址，实际报错: %v", err)
	}
	defer ch.Close()

	uc := ch.(*socks5UDPChannel)
	if ip := uc.relay.RemoteAddr().(*net.UDPAddr).IP; ip == nil || ip.IsUnspecified() {
		t.Fatalf("UDP 中继不能落在未指定地址上，实际 remote=%v", uc.relay.RemoteAddr())
	}
	if err := ch.SendToUDP([]byte("hello-18"), "203.0.113.9", 853); err != nil {
		t.Fatalf("SendToUDP 失败: %v", err)
	}
	gotHost, gotPort, gotPayload := waitRelayFrame(t, relay4, relay6)
	if gotHost != "203.0.113.9" || gotPort != 853 || string(gotPayload) != "hello-18" {
		t.Fatalf("relay 收到的帧不对: %s:%d %q", gotHost, gotPort, gotPayload)
	}
}

// TestSocks5FrontUDPRelayIPLiteralIsUnchanged 保证 IP 字面量那条路一个字都没变：
// 不做 DNS、直接就是这个地址。
func TestSocks5FrontUDPRelayIPLiteralIsUnchanged(t *testing.T) {
	relay4, _, relayPort := listenLoopbackPair(t)
	srv, _ := startFakeUDPAssociateServer(t, fakeRelayReply{host: "127.0.0.1", port: relayPort})
	host, port, err := SplitHostPort(srv, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := &socks5Front{fc: config.Front{
		Type: config.FrontTypeSocks5, Host: host, Port: port, TimeoutMs: 3000,
	}}

	ch, err := f.DialUDP(context.Background())
	if err != nil {
		t.Fatalf("IP 字面量 relay 不该失败: %v", err)
	}
	defer ch.Close()

	uc := ch.(*socks5UDPChannel)
	want := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: relayPort}
	if got := uc.relay.RemoteAddr().(*net.UDPAddr); !got.IP.Equal(want.IP) || got.Port != want.Port {
		t.Fatalf("IP 字面量必须原样使用: 期望 %v，实际 %v", want, got)
	}
	if err := ch.SendToUDP([]byte("ip-18"), "192.0.2.1", 123); err != nil {
		t.Fatalf("SendToUDP 失败: %v", err)
	}
	if gotHost, gotPort, gotPayload := waitRelayFrame(t, relay4); gotHost != "192.0.2.1" || gotPort != 123 || string(gotPayload) != "ip-18" {
		t.Fatalf("relay 收到的帧不对: %s:%d %q", gotHost, gotPort, gotPayload)
	}
}

// TestSocks5UpstreamUDPRelayDomainMustResolveFailClosed 是同一个缺陷的**第二处实例**：
// internal/proxy/upstream.go 的 SOCKS5 出口 DialUDP 里有一模一样的
// "ParseIP nil → IPv4zero" 代码，而出口节点的地址在真实配置里就是域名
// （data/config.json 的出口节点填的是域名），所以这处更常被走到。
func TestSocks5UpstreamUDPRelayDomainMustResolveFailClosed(t *testing.T) {
	srv, ctrlClosed := startFakeUDPAssociateServer(t, fakeRelayReply{
		host: "no-such-host-18.invalid",
		port: 40002,
	})
	host, port, err := SplitHostPort(srv, 0)
	if err != nil {
		t.Fatal(err)
	}
	up := &socks5Upstream{n: config.Node{Server: host, Port: port}}
	front := NewFront(config.Front{Type: config.FrontTypeDirect, TimeoutMs: 3000})

	ch, err := up.DialUDP(context.Background(), front)
	if err == nil {
		_ = ch.Close()
		t.Fatal("出口 relay 主机名解析不了时必须明确报错，不能静默改成 0.0.0.0")
	}
	if ch != nil {
		t.Fatalf("失败时必须返回 nil 通道，实际 %T", ch)
	}
	if !strings.Contains(err.Error(), "no-such-host-18.invalid") {
		t.Fatalf("报错必须带上解析不了的 relay 主机名，实际: %v", err)
	}
	select {
	case <-ctrlClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("relay 拨号失败后必须关掉控制连接（fail-closed）")
	}
}

// TestDialSocks5UDPRelayRejectsUnusableAddr 锁死最后一道闸：
// 服务端回了通配地址、配置里也没有可用主机名（或者端口非法）时，
// 必须明确报错，而不是"反正是 0.0.0.0，发出去算了"。
func TestDialSocks5UDPRelayRejectsUnusableAddr(t *testing.T) {
	cases := []struct {
		name          string
		host          string
		port          int
		fallbackHost  string
		wantErrSubstr string
	}{
		{"服务端回 0.0.0.0 且没有兜底主机名", "0.0.0.0", 51820, "", "没有给出可用的 UDP relay 地址"},
		{"服务端回 :: 且兜底也是通配", "::", 51820, "0.0.0.0", "没有给出可用的 UDP relay 地址"},
		{"服务端回空且没有兜底主机名", "", 51820, "", "没有给出可用的 UDP relay 地址"},
		{"服务端给的端口为 0", "127.0.0.1", 0, "", "端口 0 非法"},
		{"服务端给的端口越界", "127.0.0.1", 70000, "", "端口 70000 非法"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc, err := dialSocks5UDPRelay(context.Background(), tc.host, tc.port, tc.fallbackHost, time.Second)
			if err == nil {
				remote := uc.RemoteAddr()
				_ = uc.Close()
				t.Fatalf("必须报错，实际连上了 %v", remote)
			}
			if uc != nil {
				t.Fatalf("失败时必须返回 nil 连接，实际 %v", uc)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Fatalf("报错内容不对，期望包含 %q，实际: %v", tc.wantErrSubstr, err)
			}
		})
	}
}
