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
	h.connLog("socks5", target)
	h.logf("SOCKS5 隧道建立 → %s", target)
	Relay(&bufConn{Conn: client, r: br}, remote, h.Stats, h.Idle)
}

func (h *Handler) socksUDPAssociate(tcp net.Conn, br *bufio.Reader) {
	if !h.AllowUDP || !h.Upstream.SupportsUDP() {
		writeSocksReply(tcp, repCommandNotSupported)
		h.fail("该链路不支持 UDP 转发")
		return
	}

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
	if err := writeSocksUDPReply(tcp, relayIP.String(), pc.LocalAddr().(*net.UDPAddr).Port); err != nil {
		return
	}
	h.connLog("udp", describeAddrUDP(relayIP.String(), pc.LocalAddr().(*net.UDPAddr).Port))
	h.logf("UDP ASSOCIATE 已建立，中继 %s", describeAddrUDP(relayIP.String(), pc.LocalAddr().(*net.UDPAddr).Port))

	done := make(chan struct{})
	var closeOnce sync.Once
	shutdown := func() { closeOnce.Do(func() { close(done) }) }

	// TCP 控制连接断开 => 结束整个 UDP 会话
	go func() {
		_, _ = io.Copy(io.Discard, &bufConn{Conn: tcp, r: br})
		shutdown()
	}()

	var clientUDP *net.UDPAddr
	var clientOnce sync.Once

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
			clientOnce.Do(func() { clientUDP = from })
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
			dst := clientUDP
			if dst == nil {
				continue // 还没收到过客户端数据报，无法确定回程地址
			}
			frame, ferr := appendUDPHeader(make([]byte, 0, n+300), sh, sp, buf[:n])
			if ferr != nil {
				continue
			}
			_, _ = pc.WriteToUDP(frame, dst)
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
	h.connLog("http", target)
	h.logf("HTTP CONNECT 隧道建立 → %s", target)
	Relay(&bufConn{Conn: client, r: br}, remote, h.Stats, h.Idle)
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

	// 把上游应答原样回给客户端。
	rbr := bufio.NewReaderSize(remote, 16*1024)
	respHead, err := readHTTPHead(rbr)
	if err != nil {
		h.fail("读取上游应答失败: %v", err)
		return
	}
	if _, err := client.Write(respHead.raw); err != nil {
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
			if _, werr := client.Write(buf[:n]); werr != nil {
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
