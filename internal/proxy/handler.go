package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Auth 是我们自己的 mixed 端口上的可选鉴权。
// 默认监听 127.0.0.1 且不鉴权，保持"省事"。
type Auth struct {
	Username string
	Password string
}

// Handler 负责一条"链路"的全部协议处理：
// 客户端协议（SOCKS5 或 HTTP）→ 前置代理 → 住宅上游 → 目标。
type Handler struct {
	Name     string
	Front    Front
	Upstream Upstream
	Listen   *Auth

	Idle     time.Duration
	AllowUDP bool
	Stats    *Stats
	Logf     func(format string, args ...any)

	closeOnce sync.Once
	done      chan struct{}
}

func (h *Handler) logf(format string, args ...any) {
	if h.Logf != nil {
		h.Logf("[%s] "+format, append([]any{h.Name}, args...)...)
	}
}

func (h *Handler) fail(format string, args ...any) {
	h.Stats.Errors.Add(1)
	h.logf("失败: "+format, args...)
}

// bufConn 把已经预读了字节的 bufio.Reader 与底层连接缝在一起。
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// WriteTo 保留 TCP 的 splice/sendfile 优化：先把缓冲区排空，再交还给底层 TCPConn。
func (c *bufConn) WriteTo(w io.Writer) (int64, error) {
	n, err := io.Copy(w, c.r)
	if err != nil {
		return n, err
	}
	if wt, ok := c.Conn.(io.WriterTo); ok {
		m, err2 := wt.WriteTo(w)
		return n + m, err2
	}
	m, err2 := io.Copy(w, c.Conn)
	return n + m, err2
}

// ServeClient 处理一条已建立的客户端连接。
// 通过首字节判断客户端说的是 SOCKS5（0x05）还是 HTTP（ASCII 文本）。
func (h *Handler) ServeClient(client net.Conn) {
	defer client.Close()

	br := bufio.NewReaderSize(client, 16*1024)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	if first[0] == socks5Version {
		h.serveSOCKS5(client, br)
		return
	}
	h.serveHTTP(client, br)
}

// ------------------------------------------------------------------
// SOCKS5 服务端
// ------------------------------------------------------------------

func (h *Handler) serveSOCKS5(client net.Conn, br *bufio.Reader) {
	// --- 方法协商 ---
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil || head[0] != socks5Version {
		h.fail("SOCKS5 握手首包异常")
		return
	}
	nMethods := int(head[1])
	if nMethods == 0 {
		h.fail("SOCKS5 greeting 未给出可用方法")
		return
	}
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(br, methods); err != nil {
		h.fail("SOCKS5 方法列表读取失败: %v", err)
		return
	}

	want := byte(authNone)
	if h.Listen != nil {
		want = authUserPass
	}
	supported := false
	for _, m := range methods {
		if m == want {
			supported = true
			break
		}
	}
	if !supported {
		_, _ = client.Write([]byte{socks5Version, authNoAcceptable})
		h.fail("客户端不支持我们要求的 SOCKS5 认证方式 0x%02x", want)
		return
	}
	if _, err := client.Write([]byte{socks5Version, want}); err != nil {
		return
	}

	// --- 用户名密码认证（RFC 1929）---
	if want == authUserPass {
		ok := func() bool {
			hdr := make([]byte, 2)
			if _, err := io.ReadFull(br, hdr); err != nil {
				return false
			}
			u := make([]byte, hdr[1])
			if _, err := io.ReadFull(br, u); err != nil {
				return false
			}
			plen := make([]byte, 1)
			if _, err := io.ReadFull(br, plen); err != nil {
				return false
			}
			p := make([]byte, plen[0])
			if _, err := io.ReadFull(br, p); err != nil {
				return false
			}
			okUser := constantTimeEq(string(u), h.Listen.Username)
			okPass := constantTimeEq(string(p), h.Listen.Password)
			if okUser && okPass {
				_, _ = client.Write([]byte{0x01, 0x00})
				return true
			}
			_, _ = client.Write([]byte{0x01, 0x01})
			return false
		}()
		if !ok {
			h.fail("客户端鉴权失败")
			return
		}
	}

	// --- 请求 ---
	// 请求头共 3 字节：VER CMD RSV；ATYP+ADDR+PORT 交给 readSocks5Addr。
	reqHead := make([]byte, 3)
	if _, err := io.ReadFull(br, reqHead); err != nil || reqHead[0] != socks5Version {
		h.fail("SOCKS5 请求头异常")
		return
	}
	cmd := reqHead[1]
	targetHost, targetPort, err := readSocks5Addr(br)
	if err != nil {
		writeSocksReply(client, repAddressNotSupported)
		h.fail("SOCKS5 请求地址解析失败: %v", err)
		return
	}

	switch cmd {
	case cmdConnect:
		h.socksConnect(client, br, targetHost, targetPort)
	case cmdUDPAssociate:
		h.socksUDPAssociate(client, br)
	default:
		writeSocksReply(client, repCommandNotSupported)
		h.fail("SOCKS5 不支持的命令 0x%02x", cmd)
	}
}

func (h *Handler) socksConnect(client net.Conn, br *bufio.Reader, host string, port int) {
	target := net.JoinHostPort(host, strconv.Itoa(port))
	ctx, cancel := context.WithTimeout(context.Background(), h.Upstream.DialTimeout())
	defer cancel()

	remote, err := h.Upstream.Dial(ctx, h.Front, target)
	if err != nil {
		writeSocksReply(client, repHostUnreachable)
		h.fail("建立到 %s 的链路失败: %v", target, err)
		return
	}
	if err := writeSocksReply(client, repSuccess); err != nil {
		remote.Close()
		return
	}
	h.logf("SOCKS5 隧道建立 → %s", target)
	counter, finish := h.trackConn("socks5", target, client, client, remote)
	up, down := Relay(&bufConn{Conn: client, r: br}, remote, h.Stats, h.Idle, counter)
	finish(up, down, nil)
}

// maxUDPSessions 是**进程内**同时存活的 UDP ASSOCIATE 会话数上限。
//
// 【为什么必须有上界】每个 UDP ASSOCIATE 会话都要额外占：1 个本地 UDP 中继口、
// 1 条上游 UDP 通道（socks5 出口是"TCP 控制连接 + UDP socket"两条）、外加 3 个常驻
// goroutine —— 而这条路径原来**一个上界都没有**：客户端（或一段脚本）可以反复
// UDP ASSOCIATE 之后把控制连接挂着不放，几秒内就能把进程的句柄/内存吃光，
// 之后连 TCP 代理都建不起来。所以这里按进程封顶，超了**直接拒绝**（fail-closed），
// 而不是"先接下来再说"。
//
// 【为什么是常量而不是设置项】上界只是为了兜住失控的客户端：256 个会话
// （≈ 800 个句柄）远超单机代理的正常用量。真要调，改这一处即可，
// 不必让用户多背一个旋钮。
const maxUDPSessions = 256

// udpSessionCount 是当前存活的 UDP 会话数（进程级，所有节点共用）。
var udpSessionCount atomic.Int64

// acquireUDPSession 认领一个 UDP 会话名额；超出上限返回 false 且不占名额。
func acquireUDPSession() bool {
	if udpSessionCount.Add(1) > maxUDPSessions {
		udpSessionCount.Add(-1)
		return false
	}
	return true
}

// releaseUDPSession 归还名额，必须与成功的 acquireUDPSession 一一配对。
func releaseUDPSession() { udpSessionCount.Add(-1) }

func (h *Handler) socksUDPAssociate(tcp net.Conn, br *bufio.Reader) {
	if !h.AllowUDP || !h.Upstream.SupportsUDP() {
		writeSocksReply(tcp, repCommandNotSupported)
		h.fail("该链路不支持 UDP 转发")
		return
	}

	// 先占名额再动手：名额不够时连本地 UDP 口都不申请、上游也不拨号，直接拒绝。
	if !acquireUDPSession() {
		writeSocksReply(tcp, repGeneralFailure)
		h.fail("UDP 会话数已达上限 %d，拒绝新的 UDP ASSOCIATE", maxUDPSessions)
		return
	}
	defer releaseUDPSession()

	// 在客户端来源 IP 上开一个 UDP 中继口，端口由系统分配。
	clientIP := hostOf(tcp.RemoteAddr())
	var bindIP net.IP
	if ip := net.ParseIP(clientIP); ip != nil {
		bindIP = ip
	}
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: bindIP, Port: 0})
	if err != nil {
		writeSocksReply(tcp, repGeneralFailure)
		h.fail("UDP 中继端口申请失败: %v", err)
		return
	}
	defer pc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), h.Upstream.DialTimeout())
	up, err := h.Upstream.DialUDP(ctx, h.Front)
	cancel()
	if err != nil {
		writeSocksReply(tcp, repGeneralFailure)
		h.fail("上游 UDP 通道建立失败: %v", err)
		return
	}
	defer up.Close()

	relayIP := pc.LocalAddr().(*net.UDPAddr).IP
	if relayIP == nil || relayIP.IsUnspecified() {
		relayIP = net.ParseIP(hostOf(tcp.LocalAddr()))
	}
	relayAddr := describeAddrUDP(relayIP.String(), pc.LocalAddr().(*net.UDPAddr).Port)
	if err := writeSocksUDPReply(tcp, relayIP.String(), pc.LocalAddr().(*net.UDPAddr).Port); err != nil {
		return
	}
	h.logf("UDP ASSOCIATE 已建立，中继 %s", relayAddr)

	// 登记进"活动连接表"，并在会话结束时补一条 close 事件。
	//
	// 【原来错在哪】这里曾经是手工发一条 open 事件（ID 还是自己 new 出来的）：
	// 既没有登记 closers，会话结束时也没有 close 事件。后果有两层：
	//  1. 界面上那一行永远是 active —— 控制连接早断了、UDP 口也关了，连接表里
	//     却一直挂着一条幽灵记录，越攒越多；
	//  2. 「关闭全部」只能遍历 activeConns，而这个 ID 从来没进过那张表，
	//     所以按钮既关不掉这条会话、也清不掉那行幽灵记录。
	// 现在统一交给 trackConn 登记：tcp 控制连接 / 本地中继口 / 上游 UDP 通道都是
	// 这条会话的参与者（任一被关都会把会话带走），结束时 finish 上报 close。
	//
	// 【为什么现在可以登记字节数】当初"不登记"的理由是 UDP 没有 Relay 那样的
	// 双向字节统计，登记进去会显示假的上下行。那个理由现在不成立：下面两个中继
	// 循环按数据报 payload 长度累加 ConnCounter，界面看到的是真实字节数。
	// 别再改回"只发 open、不登记"。
	counter, finish := h.trackConn("udp", relayAddr, tcp, tcp, pc, up)
	defer func() { finish(counter.Up(), counter.Down(), nil) }()

	done := make(chan struct{})
	var closeOnce sync.Once
	shutdown := func() { closeOnce.Do(func() { close(done) }) }

	// lastAct 是"最近一次真正转发出去 / 收回来的数据报"的时间（Unix 毫秒），
	// 空闲回收用它判断。刻意按"成功转发的数据报"算，而不是"收到过任何字节"：
	// 一直灌垃圾包的客户端不该被算成活跃。
	var lastAct atomic.Int64
	lastAct.Store(time.Now().UnixMilli())

	// TCP 控制连接断开 => 结束整个 UDP 会话
	go func() {
		_, _ = io.Copy(io.Discard, &bufConn{Conn: tcp, r: br})
		shutdown()
	}()

	// 空闲回收：控制连接一直挂着不动的会话（客户端崩了 / NAT 映射没了 / 脚本忘了关）
	// 原来会永远占着本地 UDP 口和上游通道。这里按 TCP 中继**同一套**「空闲自动断开」
	// 设置（h.Idle，来源是 Settings.IdleTimeoutSec）回收 —— 用户只需要理解一个旋钮，
	// 也不必为 UDP 再发明一套默认值。h.Idle <= 0 表示不设空闲超时，与 Relay 一致。
	if h.Idle > 0 {
		go func() {
			// 检查间隔取阈值的 1/4（下限 100ms）：既不会在超时后拖很久才发现，
			// 也不至于为一条几乎不动的会话每秒醒来几十次。
			interval := h.Idle / 4
			if interval < 100*time.Millisecond {
				interval = 100 * time.Millisecond
			}
			tick := time.NewTicker(interval)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					idleFor := time.Duration(time.Now().UnixMilli()-lastAct.Load()) * time.Millisecond
					if idleFor >= h.Idle {
						h.logf("UDP 会话空闲 %v 无数据报（阈值 %v），自动回收",
							idleFor.Round(time.Millisecond), h.Idle)
						shutdown()
						return
					}
				}
			}
		}()
	}

	// clientUDP 是"回程往哪儿写"：SOCKS5 UDP ASSOCIATE 靠客户端的**第一份**数据报
	// 确定客户端是从哪个地址发来的，之后所有回包都写回那里。写入方是下面
	// 「客户端 -> 上游」的 goroutine，读取方是「上游 -> 客户端」的 goroutine。
	//
	// 【为什么必须是 atomic.Pointer，而不是裸变量 + sync.Once】sync.Once 只在**调用过
	// Do 的** goroutine 之间建立先后关系（它保证的是"只执行一次"，不是"对谁都可见"）。
	// 读取方从来没调用过 Do，于是这一读一写之间**没有任何 happens-before 边**：
	// 这既是 data race（Go 内存模型下属 UB），也让读取方可以一直读到 nil、或者拿到一个
	// 还没被完整发布的地址对象 —— 回程包会被静静丢掉，界面上表现为"客户端发得出去、
	// 永远收不到回包"，而且只在弱内存序机器或激进优化下才现形。
	// （-race 抓得到：WARNING: DATA RACE，读在「上游 -> 客户端」的循环里，
	// 写侧原来在 sync.Once 里；连它指向的 IP/Port 字段一起报。）
	// 改成 atomic.Pointer 后，Load 与 CompareAndSwap 之间自带 happens-before：
	// 要么读到 nil（客户端还没发过数据报，此时丢掉回包本来就是对的），
	// 要么读到一个**完整发布**的地址。
	//
	// 【为什么写侧用 CompareAndSwap(nil, ...) 而不是 Store】要保持旧实现"只认第一份
	// 数据报"的语义：换成 Store 就成了"最后一份说了算"，任何能往本地中继口发包的人
	// 都能把这条会话的回程劫走。别改回去。
	var clientUDP atomic.Pointer[net.UDPAddr]

	// 客户端 -> 上游
	go func() {
		buf := make([]byte, 64*1024+512)
		for {
			select {
			case <-done:
				return
			default:
			}
			_ = pc.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, from, err := pc.ReadFromUDP(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				shutdown()
				return
			}
			// 只认第一份：CAS 失败说明回程地址已经定过了（见上面 clientUDP 的注释）。
			clientUDP.CompareAndSwap(nil, from)
			dh, dp, payload, perr := parseUDPHeader(buf[:n])
			if perr != nil {
				continue
			}
			if err := up.SendToUDP(payload, dh, dp); err != nil {
				select {
				case <-done:
					return
				default:
				}
			} else {
				// 只记**真正转发出去**的 payload 字节（不含 SOCKS5 UDP 头），
				// 界面上的"上传"才是诚实的。
				counter.AddUp(int64(len(payload)))
				lastAct.Store(time.Now().UnixMilli())
			}
		}
	}()

	// 上游 -> 客户端
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, sh, sp, err := up.ReceiveFromUDP(done, buf)
			if err != nil {
				shutdown()
				return
			}
			// 原子读：与写入方之间的 happens-before 由原子操作本身建立
			// （见上面 clientUDP 的注释，别再改回裸变量）。
			dst := clientUDP.Load()
			if dst == nil {
				continue // 还没收到过客户端数据报，无法确定回程地址
			}
			frame, ferr := appendUDPHeader(make([]byte, 0, n+300), sh, sp, buf[:n])
			if ferr != nil {
				continue
			}
			if _, werr := pc.WriteToUDP(frame, dst); werr == nil {
				// 与上行对称：只记真正写回客户端的 payload 字节。
				counter.AddDown(int64(n))
				lastAct.Store(time.Now().UnixMilli())
			}
		}
	}()

	<-done
	h.logf("UDP 会话结束")
}

func writeSocksReply(w io.Writer, code byte) error {
	// BND.ADDR/BND.PORT 用 0.0.0.0:0 占位（客户端只关心 code）。
	_, err := w.Write([]byte{socks5Version, code, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0})
	return err
}

func writeSocksUDPReply(w io.Writer, host string, port int) error {
	buf := []byte{socks5Version, repSuccess, 0x00}
	buf, err := appendSocks5Addr(buf, host, port)
	if err != nil {
		return err
	}
	_, err = w.Write(buf)
	return err
}

// ------------------------------------------------------------------
// HTTP 代理服务端
// ------------------------------------------------------------------

func (h *Handler) serveHTTP(client net.Conn, br *bufio.Reader) {
	head, err := readHTTPHead(br)
	if err != nil {
		h.fail("HTTP 请求头解析失败: %v", err)
		return
	}
	if h.Listen != nil && !h.checkProxyAuth(head) {
		h.fail("HTTP 客户端鉴权失败")
		writeHTTPStatus(client, 407, "Proxy Authentication Required")
		return
	}

	if strings.EqualFold(head.method, "CONNECT") {
		h.httpConnect(client, br, head)
		return
	}
	h.httpForward(client, br, head)
}

func (h *Handler) checkProxyAuth(head *httpHead) bool {
	raw := head.Get("Proxy-Authorization")
	const prefix = "Basic "
	if len(raw) <= len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return false
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw[len(prefix):]))
	if err != nil {
		return false
	}
	user, pass, _ := strings.Cut(string(dec), ":")
	return constantTimeEq(user, h.Listen.Username) && constantTimeEq(pass, h.Listen.Password)
}

func (h *Handler) httpConnect(client net.Conn, br *bufio.Reader, head *httpHead) {
	targetHost, targetPort, err := SplitHostPort(head.target, 443)
	if err != nil {
		writeHTTPStatus(client, 400, "Bad Request")
		h.fail("CONNECT 目标地址非法: %q", head.target)
		return
	}
	target := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))

	ctx, cancel := context.WithTimeout(context.Background(), h.Upstream.DialTimeout())
	defer cancel()
	remote, err := h.Upstream.Dial(ctx, h.Front, target)
	if err != nil {
		writeHTTPStatus(client, 502, "Bad Gateway")
		h.fail("建立到 %s 的链路失败: %v", target, err)
		return
	}
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\nProxy-Agent: ChainProxy\r\n\r\n"); err != nil {
		remote.Close()
		return
	}
	h.logf("HTTP CONNECT 隧道建立 → %s", target)
	counter, finish := h.trackConn("http", target, client, client, remote)
	up, down := Relay(&bufConn{Conn: client, r: br}, remote, h.Stats, h.Idle, counter)
	finish(up, down, nil)
}

// httpForwardWriteTimeout 是 httpForward 每次"写回客户端"的兜底写超时。
//
// 【为什么必须是 var 而不是 const】回归测试要复现的场景是"客户端发了请求就再也不读，
// 写回被内核缓冲堵死"，而真等满 60 秒的用例没人会跑 —— 所以留一个测试接缝，
// 让用例把它临时压到几百毫秒（与 idle_test.go 里替换 mihomoFactory 是同一类做法）。
// 生产代码不许改它。
var httpForwardWriteTimeout = writeTimeout

// writeBackToClient 给一次"写回客户端"套上与 Relay 同款的兜底写超时。
//
// 【为什么不直接调 client.Write】SetWriteDeadline 设的是**一次性绝对时刻**，
// 写一次就得重设一次；包一层既不会漏设，也免得把下面那段理由抄两遍。
//
// 【这个坑是什么】httpForward 原来对客户端只写不设任何超时（同文件的 CONNECT 路径走
// Relay，那里每次写之前都会 SetWriteDeadline，这条路径漏了）。客户端只要"发了请求就
// 挂着不读"（脚本连上就睡、下载中途停住、故意不读走人），内核发送缓冲 + 客户端接收
// 缓冲填满后 client.Write 就会**永久阻塞**：写回的 goroutine、已建立的上游连接、
// 以及成对的 socket 句柄全部钉死，反复来几次就能把进程的句柄耗光 —— 而这不是理论
// 风险，任何能连上代理端口的人（AllowLan 打开时包括整个局域网）都能触发。
// 加上超时后，写不动就报错返回，defer 会把上游连接关掉，句柄立刻回收。
func writeBackToClient(c net.Conn, p []byte) error {
	_ = c.SetWriteDeadline(time.Now().Add(httpForwardWriteTimeout))
	_, err := c.Write(p)
	return err
}

// httpForward 处理普通（非隧道）HTTP 代理请求：GET http://host/path HTTP/1.1
//
// 刻意取舍：本路径**每条客户端连接只处理一个请求**，转发时对上游声明 Connection: close。
// 原因是浏览器绝大多数流量都是 HTTPS→CONNECT（已走全双工隧道），
// 明文 HTTP 代理场景对 keep-alive 的收益很小，却会显著增加状态机复杂度与出错面。
func (h *Handler) httpForward(client net.Conn, br *bufio.Reader, head *httpHead) {
	host, port, path, err := parseAbsoluteTarget(head.target, 80)
	if err != nil {
		writeHTTPStatus(client, 400, "Bad Request")
		h.fail("HTTP 目标解析失败: %v", err)
		return
	}
	if head.Has("transfer-encoding") {
		writeHTTPStatus(client, 501, "Not Implemented")
		h.fail("暂不支持 chunked 请求体")
		return
	}

	bodyLen := 0
	if cl := head.Get("content-length"); cl != "" {
		bodyLen, _ = strconv.Atoi(cl)
		if bodyLen < 0 || bodyLen > 32*1024*1024 {
			writeHTTPStatus(client, 413, "Payload Too Large")
			return
		}
	}
	var body []byte
	if bodyLen > 0 {
		body = make([]byte, bodyLen)
		if _, err := io.ReadFull(br, body); err != nil {
			writeHTTPStatus(client, 400, "Bad Request")
			return
		}
	}

	// 组装转发给上游的请求头：删掉逐跳头与代理鉴权头。
	fwd := map[string][]string{}
	for k, v := range head.header {
		switch k {
		case "proxy-authorization", "proxy-connection", "connection", "keep-alive", "upgrade":
			continue
		}
		fwd[k] = append([]string(nil), v...)
	}
	fwd["host"] = []string{net.JoinHostPort(host, strconv.Itoa(port))}
	fwd["connection"] = []string{"close"}
	if bodyLen > 0 {
		fwd["content-length"] = []string{strconv.Itoa(bodyLen)}
	}
	order := []string{"host", "user-agent", "accept", "accept-encoding", "accept-language",
		"cache-control", "pragma", "content-type", "content-length", "cookie", "referer",
		"origin", "connection"}

	target := net.JoinHostPort(host, strconv.Itoa(port))
	ctx, cancel := context.WithTimeout(context.Background(), h.Upstream.DialTimeout())
	defer cancel()
	remote, err := h.Upstream.Dial(ctx, h.Front, target)
	if err != nil {
		writeHTTPStatus(client, 502, "Bad Gateway")
		h.fail("建立到 %s 的链路失败: %v", target, err)
		return
	}
	defer remote.Close()

	if err := writeRequestHead(remote, head.method, path, "HTTP/1.1", fwd, order); err != nil {
		writeHTTPStatus(client, 502, "Bad Gateway")
		h.fail("转发请求头失败: %v", err)
		return
	}
	if len(body) > 0 {
		if _, err := remote.Write(body); err != nil {
			writeHTTPStatus(client, 502, "Bad Gateway")
			h.fail("转发请求体失败: %v", err)
			return
		}
	}
	if h.Stats != nil {
		h.Stats.Up.Add(int64(len(body)))
	}

	// 把上游应答原样回给客户端（前端写超时的理由见 writeBackToClient）。
	rbr := bufio.NewReaderSize(remote, 16*1024)
	respHead, err := readHTTPHead(rbr)
	if err != nil {
		h.fail("读取上游应答失败: %v", err)
		return
	}
	if err := writeBackToClient(client, respHead.raw); err != nil {
		return
	}

	if respHead.Get("connection") != "close" && !hasCloseToken(respHead.Get("connection")) {
		// 上游没关连接时，我们主动把剩余字节读到 EOF 后关闭。
		// 用 30s 的整体截止，避免挂死。
		_ = remote.SetReadDeadline(time.Now().Add(30 * time.Second))
	}
	buf := make([]byte, relayBuffers)
	for {
		n, rerr := rbr.Read(buf)
		if n > 0 {
			if h.Stats != nil {
				h.Stats.Down.Add(int64(n))
			}
			if err := writeBackToClient(client, buf[:n]); err != nil {
				return
			}
		}
		if rerr != nil {
			return
		}
	}
}

func hasCloseToken(v string) bool {
	for _, tok := range strings.Split(v, ",") {
		if strings.EqualFold(strings.TrimSpace(tok), "close") {
			return true
		}
	}
	return false
}

func writeHTTPStatus(w io.Writer, code int, reason string) {
	if reason == "" {
		reason = statusText(code)
	}
	_, _ = fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", code, reason)
}

func statusText(code int) string {
	switch code {
	case 400:
		return "Bad Request"
	case 407:
		return "Proxy Authentication Required"
	case 413:
		return "Payload Too Large"
	case 501:
		return "Not Implemented"
	case 502:
		return "Bad Gateway"
	default:
		return "Error"
	}
}

// ------------------------------------------------------------------
// 小工具
// ------------------------------------------------------------------

func hostOf(a net.Addr) string {
	switch v := a.(type) {
	case *net.TCPAddr:
		return v.IP.String()
	case *net.UDPAddr:
		return v.IP.String()
	case nil:
		return ""
	default:
		s := v.String()
		if h, _, err := net.SplitHostPort(s); err == nil {
			return h
		}
		return s
	}
}

// constantTimeEq 做定长比较，避免用户名密码比较的时序侧信道。
func constantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
