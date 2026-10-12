// Package proxy 实现 PicoHop 的协议核心：
//   - Front   ：把一条 TCP/UDP 连接"经由前置代理"送到某个地址（第一跳）
//   - Upstream：在一���已经连通前置"的流里，再建立到最终目标的连接（第二跳）
//   - Handler ：对外的 mixed 监听器，同时服务 SOCKS5 与 HTTP 客户端
//
// 设计要点见 docs/ARCHITECTURE.md。
package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// ------------------------------------------------------------------
// RFC 1928 / RFC 1929 常量
// ------------------------------------------------------------------

const (
	socks5Version = 0x05

	authNone         = 0x00
	authUserPass     = 0x02
	authNoAcceptable = 0xFF

	cmdConnect      = 0x01
	cmdBind         = 0x02
	cmdUDPAssociate = 0x03

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04

	repSuccess             = 0x00
	repGeneralFailure      = 0x01
	repNotAllowed          = 0x02
	repNetworkUnreachable  = 0x03
	repHostUnreachable     = 0x04
	repConnectionRefused   = 0x05
	repTTLExpired          = 0x06
	repCommandNotSupported = 0x07
	repAddressNotSupported = 0x08
)

// ErrUDPUnsupported 表示该上游不支持 UDP 转发（HTTP 上游天然不支持）。
var ErrUDPUnsupported = errors.New("该上游类型不支持 UDP 转发")

// ------------------------------------------------------------------
// 地址编解码
// ------------------------------------------------------------------

// appendSocks5Addr 把 host:port 编码成 SOCKS5 的 ADDR 形式追加到 b。
func appendSocks5Addr(b []byte, host string, port int) ([]byte, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("端口 %d 非法", port)
	}
	// 已经是 IP 就直接用 IP 类型，避免不必要的域名解析。
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			b = append(b, atypIPv4)
			b = append(b, v4...)
		} else {
			b = append(b, atypIPv6)
			b = append(b, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, fmt.Errorf("域名过长")
		}
		if len(host) == 0 {
			return nil, errors.New("域名为空")
		}
		b = append(b, atypDomain, byte(len(host)))
		b = append(b, host...)
	}
	return binary.BigEndian.AppendUint16(b, uint16(port)), nil
}

// readSocks5Addr 从 r 读取一个 SOCKS5 ADDR，返回 host 与 port。
func readSocks5Addr(r io.Reader) (host string, port int, err error) {
	var atyp [1]byte
	if _, err = io.ReadFull(r, atyp[:]); err != nil {
		return
	}
	var raw []byte
	switch atyp[0] {
	case atypIPv4:
		raw = make([]byte, 4)
	case atypIPv6:
		raw = make([]byte, 16)
	case atypDomain:
		var n [1]byte
		if _, err = io.ReadFull(r, n[:]); err != nil {
			return
		}
		if n[0] == 0 {
			return "", 0, errors.New("域名为空")
		}
		raw = make([]byte, n[0])
	default:
		return "", 0, fmt.Errorf("未知地址类型 0x%02x", atyp[0])
	}
	if _, err = io.ReadFull(r, raw); err != nil {
		return
	}
	var p [2]byte
	if _, err = io.ReadFull(r, p[:]); err != nil {
		return
	}
	port = int(binary.BigEndian.Uint16(p[:]))

	switch atyp[0] {
	case atypIPv4:
		host = net.IP(raw).String()
	case atypIPv6:
		host = net.IP(raw).String()
	case atypDomain:
		host = string(raw)
	}
	return
}

// ------------------------------------------------------------------
// 客户端侧握手
// ------------------------------------------------------------------

// socks5Greeting 完成 SOCKS5 的方法协商与（可选的）用户名密码认证。
//
// 调用前请先给 conn 设置好 deadline。
func socks5Greeting(conn net.Conn, username, password string) error {
	if username != "" {
		// 仍然宣告 NO AUTHENTICATION REQUIRED 是允许的（服务端任选其一），
		// 但更稳妥的做法是只宣告需要用户名密码。
		if _, err := conn.Write([]byte{socks5Version, 0x01, authUserPass}); err != nil {
			return fmt.Errorf("SOCKS5 greeting 写入失败: %w", err)
		}
	} else {
		if _, err := conn.Write([]byte{socks5Version, 0x01, authNone}); err != nil {
			return fmt.Errorf("SOCKS5 greeting 写入失败: %w", err)
		}
	}

	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return fmt.Errorf("SOCKS5 greeting 读取失败: %w", err)
	}
	if head[0] != socks5Version {
		return fmt.Errorf("不是 SOCKS5 应答（版本 0x%02x）", head[0])
	}

	switch head[1] {
	case authNone:
		return nil
	case authUserPass:
		if username == "" {
			return errors.New("服务端要求用户名密码认证，但未配置凭据")
		}
		buf := make([]byte, 0, 3+len(username)+len(password))
		buf = append(buf, 0x01, byte(len(username)))
		buf = append(buf, username...)
		buf = append(buf, byte(len(password)))
		buf = append(buf, password...)
		if _, err := conn.Write(buf); err != nil {
			return fmt.Errorf("SOCKS5 认证请求写入失败: %w", err)
		}
		rep := make([]byte, 2)
		if _, err := io.ReadFull(conn, rep); err != nil {
			return fmt.Errorf("SOCKS5 认证应答读取失败: %w", err)
		}
		if rep[0] != 0x01 {
			return fmt.Errorf("SOCKS5 认证应答版本错误 0x%02x", rep[0])
		}
		if rep[1] != 0x00 {
			return errors.New("SOCKS5 认证被拒绝：用户名或密码错误")
		}
		return nil
	case authNoAcceptable:
		return errors.New("SOCKS5 服务端不接受任何我们支持的认证方式")
	default:
		return fmt.Errorf("SOCKS5 服务端选择了不支持的认证方式 0x%02x", head[1])
	}
}

// socks5Command 在已经完成 greeting 的连接上发送一个请求（CONNECT / UDP ASSOCIATE），
// 并返回应答里的绑定地址。
func socks5Command(conn net.Conn, cmd byte, host string, port int) (string, int, error) {
	req := []byte{socks5Version, cmd, 0x00}
	req, err := appendSocks5Addr(req, host, port)
	if err != nil {
		return "", 0, err
	}
	if _, err := conn.Write(req); err != nil {
		return "", 0, fmt.Errorf("SOCKS5 请求写入失败: %w", err)
	}

	// 应答头共 3 字节：VER REP RSV；后面的 ATYP+ADDR+PORT 交给 readSocks5Addr 读取。
	head := make([]byte, 3)
	if _, err := io.ReadFull(conn, head); err != nil {
		return "", 0, fmt.Errorf("SOCKS5 应答读取失败: %w", err)
	}
	if head[0] != socks5Version {
		return "", 0, fmt.Errorf("SOCKS5 应答版本错误 0x%02x", head[0])
	}
	if head[1] != repSuccess {
		return "", 0, fmt.Errorf("SOCKS5 请求被拒绝（应答码 0x%02x：%s）", head[1], ReplyText(head[1]))
	}
	bindHost, bindPort, err := readSocks5Addr(conn)
	if err != nil {
		return "", 0, fmt.Errorf("SOCKS5 绑定地址读取失败: %w", err)
	}
	return bindHost, bindPort, nil
}

// socks5Connect 完成 greeting + CONNECT，返回就绪的连接（目标仍是远端，尚未发过任何应用数据）。
func socks5Connect(conn net.Conn, username, password, targetHost string, targetPort int) error {
	if err := socks5Greeting(conn, username, password); err != nil {
		return err
	}
	_, _, err := socks5Command(conn, cmdConnect, targetHost, targetPort)
	return err
}

// socks5UDPAssociate 完成 greeting + UDP ASSOCIATE，返回 relay 地址。
// 返回的控制连接必须保持打开，直到 UDP 会话结束。
//
// 注意：请求里的 DST.ADDR/DST.PORT 按 RFC 1928 是"客户端期望使用的源地址"，
// 通常填 0.0.0.0:0。而 appendSocks5Addr 会拒绝端口 0，所以这里手工拼这一段。
func socks5UDPAssociate(conn net.Conn, username, password string) (string, int, error) {
	if err := socks5Greeting(conn, username, password); err != nil {
		return "", 0, err
	}
	req := []byte{socks5Version, cmdUDPAssociate, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}
	if _, err := conn.Write(req); err != nil {
		return "", 0, fmt.Errorf("SOCKS5 UDP ASSOCIATE 请求写入失败: %w", err)
	}

	head := make([]byte, 3)
	if _, err := io.ReadFull(conn, head); err != nil {
		return "", 0, fmt.Errorf("SOCKS5 UDP ASSOCIATE 应答读取失败: %w", err)
	}
	if head[0] != socks5Version {
		return "", 0, fmt.Errorf("SOCKS5 应答版本错误 0x%02x", head[0])
	}
	if head[1] != repSuccess {
		return "", 0, fmt.Errorf("SOCKS5 拒绝了 UDP ASSOCIATE（应答码 0x%02x：%s）", head[1], ReplyText(head[1]))
	}
	return readSocks5Addr(conn)
}

// dialSocks5UDPRelay 按 UDP ASSOCIATE 应答里的 relay 地址拨一条 UDP 套接字。
//
// 【这个坑是什么（审查 #18）】relay 主机名**经常**是域名，但原来的写法是：
//
//	udpAddr := &net.UDPAddr{IP: net.ParseIP(relayHost), Port: relayPort}
//	if udpAddr.IP == nil { udpAddr.IP = net.IPv4zero }   // ← 域名在这里被静默吃掉
//
// net.ParseIP 对域名一律返回 nil，于是 UDP 中继被拨到 0.0.0.0:relayPort：
// **不报错**，Windows 还会把它改写成 127.0.0.1（本机回环），
// 所有 SOCKS5 UDP 帧于是发给了本机一个没人监听的端口 —— 日志里一行错误都没有，
// 外面只看到"WireGuard 一直发握手包、永远连不上"。域名从哪来：
//   - RFC 1928 允许服务端在 BND.ADDR 里回域名（ATYP=0x03）；
//   - 服务端回 0.0.0.0 / :: / 空时我们退回 fallbackHost（配置里的前置/出口主机名），
//     用户填的通常就是域名（本仓库 data/config.json 的出口节点就是域名）。
//
// 【为什么这么修】域名交给 net.Dialer 解析（受 timeout 与 ctx 约束），
// 解析或连接失败一律返回错误 —— 宁可明确失败，也**绝不允许**把地址降级成 0.0.0.0。
// 这里刻意不写成"先 ResolveUDPAddr 拿 IP、再 DialUDP"：DialContext 会按解析结果
// 逐个地址尝试，并按远端地址族选 AF_INET / AF_INET6（与原来的 DialUDP 语义一致）；
// 自己取解析结果的第一条，会在 IPv6 优先的双栈机器上挑到不通的那一族。
func dialSocks5UDPRelay(ctx context.Context, relayHost string, relayPort int, fallbackHost string, timeout time.Duration) (*net.UDPConn, error) {
	// 服务端回 0.0.0.0 / :: / 空：按 RFC 1928 的含义就是"用你连我的那个地址"，
	// 退回配置里的主机名（可能仍是域名，下面统一解析）。
	if relayHost == "" || relayHost == "0.0.0.0" || relayHost == "::" {
		relayHost = fallbackHost
	}
	// 兜底之后还是通配/空，说明配置里也没有可用主机名：直接失败，
	// 不许"退化成 0.0.0.0"（那等于把中继指向本机）。
	if relayHost == "" || relayHost == "0.0.0.0" || relayHost == "::" {
		return nil, fmt.Errorf("SOCKS5 服务端没有给出可用的 UDP relay 地址（%q），配置里也没有可用主机名", relayHost)
	}
	if relayPort <= 0 || relayPort > 65535 {
		return nil, fmt.Errorf("SOCKS5 服务端给出的 UDP relay 端口 %d 非法", relayPort)
	}

	addr := net.JoinHostPort(relayHost, strconv.Itoa(relayPort))
	d := &net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		// 域名解析不了、地址不可用都会走到这里；错误里带上原始主机名便于用户定位。
		return nil, fmt.Errorf("连接 UDP relay %s 失败: %w", addr, err)
	}
	uc, ok := conn.(*net.UDPConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("UDP relay %s 返回了非 UDP 连接（%T）", addr, conn)
	}
	return uc, nil
}

// ------------------------------------------------------------------
// SOCKS5 UDP 数据报封帧（RFC 1928 §7）
// ------------------------------------------------------------------

// appendUDPHeader 把 SOCKS5 UDP 请求头拼到 payload 前面。
func appendUDPHeader(dst []byte, host string, port int, payload []byte) ([]byte, error) {
	dst = append(dst, 0x00, 0x00, 0x00) // RSV(2) + FRAG(0)
	var err error
	if dst, err = appendSocks5Addr(dst, host, port); err != nil {
		return nil, err
	}
	return append(dst, payload...), nil
}

// parseUDPHeader 拆出 SOCKS5 UDP 头，返回剩余的 payload。
func parseUDPHeader(pkt []byte) (host string, port int, payload []byte, err error) {
	if len(pkt) < 5 {
		return "", 0, nil, errors.New("UDP 数据报过短")
	}
	if pkt[0] != 0 || pkt[1] != 0 {
		return "", 0, nil, errors.New("UDP 数据报 RSV 字段非法")
	}
	if pkt[2] != 0 {
		return "", 0, nil, errors.New("暂不支持分片的 UDP 数据报（FRAG != 0）")
	}
	rest := pkt[3:]
	switch rest[0] {
	case atypIPv4:
		if len(rest) < 1+4+2 {
			return "", 0, nil, errors.New("UDP 数据报过短")
		}
		host = net.IP(rest[1:5]).String()
		port = int(binary.BigEndian.Uint16(rest[5:7]))
		payload = rest[7:]
	case atypIPv6:
		if len(rest) < 1+16+2 {
			return "", 0, nil, errors.New("UDP 数据报过短")
		}
		host = net.IP(rest[1:17]).String()
		port = int(binary.BigEndian.Uint16(rest[17:19]))
		payload = rest[19:]
	case atypDomain:
		n := int(rest[1])
		if len(rest) < 1+1+n+2 {
			return "", 0, nil, errors.New("UDP 数据报过短")
		}
		host = string(rest[2 : 2+n])
		port = int(binary.BigEndian.Uint16(rest[2+n : 4+n]))
		payload = rest[4+n:]
	default:
		return "", 0, nil, fmt.Errorf("未知地址类型 0x%02x", rest[0])
	}
	return host, port, payload, nil
}

// ReplyText 把 SOCKS5 应答码翻译成中文，便于 GUI 直接显示。
func ReplyText(code byte) string {
	switch code {
	case repSuccess:
		return "成功"
	case repGeneralFailure:
		return "一般性失败"
	case repNotAllowed:
		return "规则不允许建立连接"
	case repNetworkUnreachable:
		return "网络不可达"
	case repHostUnreachable:
		return "主机不可达"
	case repConnectionRefused:
		return "连接被拒绝"
	case repTTLExpired:
		return "TTL 超时"
	case repCommandNotSupported:
		return "命令不支持"
	case repAddressNotSupported:
		return "地址类型不支持"
	default:
		return "未知错误"
	}
}

// ------------------------------------------------------------------
// 小工具
// ------------------------------------------------------------------

// SplitHostPort 拆 "host:port"，容忍缺端口（返回默认端口）。
func SplitHostPort(addr string, defPort int) (string, int, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", 0, errors.New("地址为空")
	}
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.Trim(addr, "[]"), defPort, nil
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, fmt.Errorf("端口 %q 不是数字", p)
	}
	return h, n, nil
}
