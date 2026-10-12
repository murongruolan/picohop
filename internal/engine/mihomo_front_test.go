package engine

// 经前置拨号的**行为**验证（A′ 的核心证据）。
//
// 【为什么要自己搭一个前置】"真的绕经前置"这件事，只有**前置侧观察到的请求**能证明 ——
// 光看"能连上"证明不了（直连也能连上，而直连正是我们要防的那个故障）。
// 所以这里的被测对象是一个最小 SOCKS5 服务端：它记录每一次 CONNECT / UDP ASSOCIATE
// 的目标，并如实转发。不依赖用户机器上的 前置代理是否在跑。
//
// 每个用例都带一条反向断言：把 DialerForAPI 摘掉（传直连）时，前置必须**一个请求都收不到**
// —— 否则说明这些用例根本没钉住"经前置"这件事。

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"

	"github.com/murongruolan/picohop/internal/config"
	"github.com/murongruolan/picohop/internal/proxy"
)

// ------------------------------------------------------------------
// 最小 SOCKS5 前置（测试替身）
// ------------------------------------------------------------------

type fakeSocks5Front struct {
	ln net.Listener

	mu         sync.Mutex
	tcpTargets []string
	udpAssocs  int
	udpTargets []string
	udpPayload []string
}

func newFakeSocks5Front(t *testing.T) *fakeSocks5Front {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("起假前置失败：%v", err)
	}
	f := &fakeSocks5Front{ln: ln}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeSocks5Front) Port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSocks5Front) Front() config.Front {
	return config.Front{
		Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: f.Port(), TimeoutMs: 3000,
	}
}

func (f *fakeSocks5Front) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *fakeSocks5Front) handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)

	// greeting：只支持"无需认证"，与我们 socks5Front 在没填账号时的行为一致。
	ver, err := br.ReadByte()
	if err != nil || ver != 5 {
		return
	}
	nmethods, err := br.ReadByte()
	if err != nil {
		return
	}
	if _, err := io.CopyN(io.Discard, br, int64(nmethods)); err != nil {
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return
	}

	// 请求头：VER CMD RSV
	head := make([]byte, 3)
	if _, err := io.ReadFull(br, head); err != nil {
		return
	}
	host, port, err := readSocksAddr(br)
	if err != nil {
		return
	}
	target := net.JoinHostPort(host, strconv.Itoa(port))

	switch head[1] {
	case 1: // CONNECT
		f.mu.Lock()
		f.tcpTargets = append(f.tcpTargets, target)
		f.mu.Unlock()

		dst, derr := net.Dial("tcp", target)
		if derr != nil {
			_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
			return
		}
		defer dst.Close()
		if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
			return
		}
		go func() {
			_, _ = io.Copy(dst, br)
			_ = dst.Close()
		}()
		_, _ = io.Copy(c, dst)
	case 3: // UDP ASSOCIATE
		f.serveUDPAssociate(c, br)
	}
}

// serveUDPAssociate 起一个 UDP relay，记录收到哪些"经前置"的数据报，并回一颗 pong。
func (f *fakeSocks5Front) serveUDPAssociate(c net.Conn, br *bufio.Reader) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return
	}
	defer pc.Close()

	f.mu.Lock()
	f.udpAssocs++
	f.mu.Unlock()

	relayPort := pc.LocalAddr().(*net.UDPAddr).Port
	reply := []byte{5, 0, 0, 1, 127, 0, 0, 1, byte(relayPort >> 8), byte(relayPort)}
	if _, err := c.Write(reply); err != nil {
		return
	}

	// 控制连接被客户端关掉 = 这次 ASSOCIATE 结束。
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, br)
		close(done)
	}()

	buf := make([]byte, 64*1024)
	for {
		_ = pc.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, from, rerr := pc.ReadFromUDP(buf)
		if rerr != nil {
			select {
			case <-done:
				return
			default:
			}
			if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		host, port, payload, perr := parseSocks5UDP(buf[:n])
		if perr != nil {
			continue
		}
		f.mu.Lock()
		f.udpTargets = append(f.udpTargets, net.JoinHostPort(host, strconv.Itoa(port)))
		f.udpPayload = append(f.udpPayload, string(payload))
		f.mu.Unlock()

		resp := wrapSocks5UDP(host, port, []byte("pong:"+string(payload)))
		_, _ = pc.WriteToUDP(resp, from)
	}
}

func (f *fakeSocks5Front) snapshot() (tcp []string, assocs int, udpTargets, udpPayload []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tcpTargets...), f.udpAssocs,
		append([]string(nil), f.udpTargets...), append([]string(nil), f.udpPayload...)
}

func readSocksAddr(r io.Reader) (string, int, error) {
	atyp, err := readByte(r)
	if err != nil {
		return "", 0, err
	}
	var host string
	switch atyp {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case 3:
		n, err := readByte(r)
		if err != nil {
			return "", 0, err
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	default:
		return "", 0, fmt.Errorf("未知的 ATYP 0x%02x", atyp)
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(r, pb); err != nil {
		return "", 0, err
	}
	return host, int(binary.BigEndian.Uint16(pb)), nil
}

func readByte(r io.Reader) (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

// parseSocks5UDP 解开 SOCKS5 UDP 请求头（RSV RSV FRAG ATYP ADDR PORT DATA）。
func parseSocks5UDP(b []byte) (string, int, []byte, error) {
	if len(b) < 4 || b[0] != 0 || b[1] != 0 {
		return "", 0, nil, errors.New("UDP 头非法")
	}
	if b[2] != 0 {
		return "", 0, nil, errors.New("不支持分片")
	}
	rest := b[3:]
	var host string
	switch rest[0] {
	case 1:
		if len(rest) < 1+4+2 {
			return "", 0, nil, errors.New("UDP 头太短")
		}
		host, rest = net.IP(rest[1:5]).String(), rest[5:]
	case 4:
		if len(rest) < 1+16+2 {
			return "", 0, nil, errors.New("UDP 头太短")
		}
		host, rest = net.IP(rest[1:17]).String(), rest[17:]
	case 3:
		if len(rest) < 2 {
			return "", 0, nil, errors.New("UDP 头太短")
		}
		n := int(rest[1])
		if len(rest) < 2+n+2 {
			return "", 0, nil, errors.New("UDP 头太短")
		}
		host, rest = string(rest[2:2+n]), rest[2+n:]
	default:
		return "", 0, nil, errors.New("未知的 ATYP")
	}
	port := int(binary.BigEndian.Uint16(rest[:2]))
	return host, port, rest[2:], nil
}

func wrapSocks5UDP(host string, port int, payload []byte) []byte {
	b := []byte{0, 0, 0}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		b = append(b, 1)
		b = append(b, ip.To4()...)
	} else {
		b = append(b, 3, byte(len(host)))
		b = append(b, host...)
	}
	b = binary.BigEndian.AppendUint16(b, uint16(port))
	return append(b, payload...)
}

// ------------------------------------------------------------------
// 假"节点服务器"：只观察"有没有人直接来连它"
// ------------------------------------------------------------------

type countingTCPServer struct {
	ln net.Listener

	mu sync.Mutex
	n  int
}

func newCountingTCPServer(t *testing.T) *countingTCPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("起假节点服务器失败：%v", err)
	}
	s := &countingTCPServer{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.n++
			s.mu.Unlock()
			// 只读不写：我们不实现任何协议，连接保持到对端关掉为止。
			go func() { _, _ = io.Copy(io.Discard, c); _ = c.Close() }()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *countingTCPServer) Addr() (string, int) {
	a := s.ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", a.Port
}

func (s *countingTCPServer) Conns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// vlessNodeOn 造一个指向假节点服务器的 VLESS 节点（不启用 TLS：只关心"往哪拨"）。
func vlessNodeOn(t *testing.T, host string, port int) config.Node {
	t.Helper()
	return config.Node{
		ID: "n1", Name: "经前置的节点", Protocol: config.ProtocolVLESS,
		Server: host, Port: port, UUID: "11111111-2222-3333-4444-555555555555",
	}
}

// ------------------------------------------------------------------
// 用例
// ------------------------------------------------------------------

// TestMihomoVLESSDialsThroughFront 是 A′ 的主证据：mihomo 出站连**节点服务器**
// 那一跳必须经过前置。
func TestMihomoVLESSDialsThroughFront(t *testing.T) {
	front := newFakeSocks5Front(t)
	nodeSrv := newCountingTCPServer(t)
	host, port := nodeSrv.Addr()
	node := vlessNodeOn(t, host, port)
	want := net.JoinHostPort(host, strconv.Itoa(port))

	// ---- 经前置 ----
	up, err := NewMihomo(node, front.Front(), nil)
	if err != nil {
		t.Fatalf("配了前置的节点应当被接受（A′ 之后它经前置拨号）：%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := up.Start(ctx, nil); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	// 假节点服务器不会实现 VLESS 握手，所以这里不关心成败 —— 只关心**谁被连了**。
	if conn, derr := up.Dial(ctx, nil, "example.com:443"); conn != nil {
		_ = conn.Close()
	} else if derr != nil && !strings.Contains(derr.Error(), "mihomo") && !strings.Contains(derr.Error(), "EOF") {
		t.Logf("（预期内的拨号失败）%v", derr)
	}
	_ = up.Stop()

	tcpTargets, _, _, _ := front.snapshot()
	found := false
	for _, got := range tcpTargets {
		if got == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("前置必须收到「到节点服务器 %s 的 CONNECT」—— 这才是经前置的证据，实际收到 %v", want, tcpTargets)
	}

	// ---- 反向验证：直连时前置一个请求都不该收到，而假节点服务器会被直接连上 ----
	before := nodeSrv.Conns()
	up2, err := NewMihomo(node, config.Front{Type: config.FrontTypeDirect}, nil)
	if err != nil {
		t.Fatalf("直连节点应当被接受：%v", err)
	}
	if err := up2.Start(ctx, nil); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	if conn, _ := up2.Dial(ctx, nil, "example.com:443"); conn != nil {
		_ = conn.Close()
	}
	_ = up2.Stop()

	tcpTargets, _, _, _ = front.snapshot()
	if len(tcpTargets) != 1 {
		t.Fatalf("直连时前置不该再收到任何请求（这条界线就是静默直连），实际 %v", tcpTargets)
	}
	// 直连那一跳是**真的连到了**假节点服务器（accept 是异步的，等一下）。
	deadline := time.Now().Add(3 * time.Second)
	for nodeSrv.Conns() <= before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if nodeSrv.Conns() <= before {
		t.Fatalf("直连时假节点服务器应当被直接连上，实际连接数没变（%d）", nodeSrv.Conns())
	}
}

// TestMihomoDialerUDPGoesThroughFront 锁死 UDP 那条路也经前置。
//
// 【为什么单独测拨号器而不是整个出站】Shadowsocks 的原生 UDP 与 Hysteria2 的 QUIC
// 最终都落在 `C.Dialer.ListenPacket` 上；这里直接验这一层，不需要真的架一个 QUIC 服务端。
func TestMihomoDialerUDPGoesThroughFront(t *testing.T) {
	front := newFakeSocks5Front(t)
	d := newMihomoDialer(front.Front())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pc, err := d.ListenPacket(ctx, "udp", "", netip.AddrPort{})
	if err != nil {
		t.Fatalf("经前置建 UDP 通道失败：%v", err)
	}
	defer pc.Close()

	target := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353}
	if _, err := pc.WriteTo([]byte("ping"), target); err != nil {
		t.Fatalf("写数据报失败：%v", err)
	}

	// 前置侧必须看到：一次 UDP ASSOCIATE + 一颗发往目标的数据报。
	// UDP 是"发了就不管"的，所以这里等一下（TCP 那条路径要等 CONNECT 应答，天然同步）。
	_, assocs, _, _ := front.snapshot()
	if assocs == 0 {
		t.Fatal("前置没有收到 UDP ASSOCIATE：说明 UDP 没走前置")
	}
	wantTarget := target.String()
	deadline := time.Now().Add(3 * time.Second)
	var udpTargets, udpPayload []string
	foundTarget, foundPayload := false, false
	for time.Now().Before(deadline) && !foundPayload {
		_, _, udpTargets, udpPayload = front.snapshot()
		for i, tp := range udpTargets {
			if tp == wantTarget {
				foundTarget = true
				if i < len(udpPayload) && udpPayload[i] == "ping" {
					foundPayload = true
				}
			}
		}
		if foundPayload {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !foundTarget || !foundPayload {
		t.Fatalf("前置应当收到发往 %s 的 payload=ping，实际 targets=%v payload=%v",
			wantTarget, udpTargets, udpPayload)
	}

	// 回包也要能读回来（走的是前置那条 UDP 通道）。
	if err := pc.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, from, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("从前置读回包失败：%v", err)
	}
	if got := string(buf[:n]); got != "pong:ping" {
		t.Fatalf("回包内容应当是我们那颗数据报的应答，实际 %q", got)
	}
	if from == nil || from.String() != wantTarget {
		t.Fatalf("回包来源应当是目标 %s，实际 %v", wantTarget, from)
	}
}

// TestMihomoDialerNeverSilentlyDirectsUDP 锁死"前置做不了 UDP 时明确报错"。
func TestMihomoDialerNeverSilentlyDirectsUDP(t *testing.T) {
	// HTTP 前置没有 UDP 转发（proxy.httpFront.DialUDP 明确返回 ErrUDPUnsupported）。
	d := newMihomoDialer(config.Front{Type: config.FrontTypeHTTP, Host: "127.0.0.1", Port: 1, TimeoutMs: 1000})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if pc, err := d.ListenPacket(ctx, "udp", "", netip.AddrPort{}); err == nil {
		_ = pc.Close()
		t.Fatal("HTTP 前置做不了 UDP，必须明确报错而不能回落到本机直连")
	}

	// 经前置的 "udp" 直拨同样必须拒绝（mihomo 只在非常规路径上会这么问）。
	if _, err := d.DialContext(ctx, "udp", "1.1.1.1:53"); err == nil {
		t.Fatal(`DialContext("udp", ...) 必须明确报错，不能偷偷直连`)
	}
}

// TestFrontPacketConnBasics 锁死桥接层自身的语义（截止时间 / 读写 / 关闭）。
func TestFrontPacketConnBasics(t *testing.T) {
	ch := newFakeUDPChannel()
	pc := newFrontPacketConn(ch)
	defer pc.Close()

	// 已经过期的读截止时间：立刻返回超时，而不是阻塞。
	if err := pc.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := pc.ReadFrom(make([]byte, 8)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("过期的读截止时间应当返回 ErrDeadlineExceeded，实际 %v", err)
	}

	// 写：应当原样落到通道上。
	if _, err := pc.WriteTo([]byte("hello"), &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 9}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ch.sent:
		if got.host != "10.0.0.1" || got.port != 9 || string(got.payload) != "hello" {
			t.Fatalf("写下去的数据报不对：%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("通道没收到写入的数据报")
	}

	// 读：喂一颗进来，应当带来源地址返回。
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	ch.in <- fakeDatagram{payload: []byte("world"), host: "10.0.0.2", port: 10}
	buf := make([]byte, 16)
	n, from, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "world" || from.String() != "10.0.0.2:10" {
		t.Fatalf("读回的数据报不对：%q from=%v", buf[:n], from)
	}

	// 关闭：读与写都要立刻返回"已关闭"，而不是永久阻塞。
	if err := pc.Close(); err != nil {
		t.Fatalf("关闭不该报错：%v", err)
	}
	if _, _, err := pc.ReadFrom(buf); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("关闭后读应当返回 ErrClosed，实际 %v", err)
	}
	if _, err := pc.WriteTo([]byte("x"), &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 9}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("关闭后写应当返回 ErrClosed，实际 %v", err)
	}
	if !ch.isClosed() {
		t.Fatal("关闭 PacketConn 必须把底层通道也关掉，否则会漏资源")
	}
}

// ------------------------------------------------------------------
// 五种协议的接线
// ------------------------------------------------------------------

// sentinelDialer 一被调用就计数并返回可辨认的错误：用来证明出站真的用了我们的拨号器。
type sentinelDialer struct {
	calls atomic.Int32
}

func (d *sentinelDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls.Add(1)
	return nil, errors.New("sentinel: 经前置拨号")
}

func (d *sentinelDialer) ListenPacket(context.Context, string, string, netip.AddrPort) (net.PacketConn, error) {
	d.calls.Add(1)
	return nil, errors.New("sentinel: 经前置 UDP")
}

// TestEveryProtocolGetsTheFrontDialer 表驱动锁死"五种协议都装上了经前置的拨号器"。
//
// 【为什么要逐个协议验】"装进 BasicOption.DialerForAPI"这件事是**每个协议各写一遍**的
// （Go 没有"取匿名字段"的泛型写法），漏掉一个协议 = 那个协议的节点在配了前置时
// 会静默从本机 IP 出去。这里用一个哨兵拨号器：出站只要真的用了它，计数器就一定会动。
func TestEveryProtocolGetsTheFrontDialer(t *testing.T) {
	withSNI := func(n config.Node) config.Node {
		// hysteria2 是协议自带 TLS 的，SNI 不能留空（否则客户端根本建不起来）。
		n.TLS.Enabled = true
		n.TLS.ServerName = "sni.example.com"
		return n
	}
	cases := []struct {
		name string
		node config.Node
	}{
		{"vless", config.Node{Protocol: config.ProtocolVLESS, Server: "1.2.3.4", Port: 443, UUID: "u-1"}},
		{"vmess", config.Node{Protocol: config.ProtocolVMess, Server: "1.2.3.4", Port: 443, UUID: "u-1", Security: "auto"}},
		{"trojan", config.Node{Protocol: config.ProtocolTrojan, Server: "1.2.3.4", Port: 443, Password: "pw"}},
		{"shadowsocks", config.Node{Protocol: config.ProtocolShadowsocks, Server: "1.2.3.4", Port: 8388,
			Password: "pw", Method: "aes-128-gcm"}},
		{"hysteria2", withSNI(config.Node{Protocol: config.ProtocolHysteria2, Server: "1.2.3.4", Port: 443, Password: "pw"})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := c.node
			n.Name = c.name
			d := &sentinelDialer{}
			out, err := buildMihomoOutbound(n, d)
			if err != nil {
				t.Fatalf("构造出站失败：%v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// 拨号一定会失败（哨兵立刻返回错误），这里只看出站**有没有问过**我们的拨号器。
			if conn, _ := out.DialContext(ctx, &C.Metadata{Host: "example.com", DstPort: 443, NetWork: C.TCP}); conn != nil {
				_ = conn.Close()
			}
			if d.calls.Load() == 0 {
				t.Fatalf("%s 的出站没有走我们的拨号器：配了前置时它会静默直连", c.name)
			}
		})
	}
}

// ------------------------------------------------------------------
// 替身：假的 proxy.UDPChannel
// ------------------------------------------------------------------

type fakeDatagram struct {
	payload []byte
	host    string
	port    int
}

type fakeUDPChannel struct {
	in   chan fakeDatagram
	sent chan fakeDatagram

	mu     sync.Mutex
	done   chan struct{}
	closed bool
}

func newFakeUDPChannel() *fakeUDPChannel {
	return &fakeUDPChannel{
		in:   make(chan fakeDatagram, 8),
		sent: make(chan fakeDatagram, 8),
	}
}

func (c *fakeUDPChannel) SendToUDP(payload []byte, host string, port int) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return net.ErrClosed
	}
	c.sent <- fakeDatagram{payload: append([]byte(nil), payload...), host: host, port: port}
	return nil
}

func (c *fakeUDPChannel) ReceiveFromUDP(quit <-chan struct{}, buf []byte) (int, string, int, error) {
	select {
	case dg := <-c.in:
		return copy(buf, dg.payload), dg.host, dg.port, nil
	case <-quit:
		return 0, "", 0, proxy.ErrUDPChannelClosed
	case <-c.closedCh():
		return 0, "", 0, net.ErrClosed
	}
}

func (c *fakeUDPChannel) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.done != nil {
		close(c.done)
	}
	return nil
}

func (c *fakeUDPChannel) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeUDPChannel) closedCh() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done == nil {
		c.done = make(chan struct{})
	}
	return c.done
}
