package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// DialSOCKS5 通过一个本地 SOCKS5 监听器建立到 target 的连接。
//
// 【为什么自己写这几十行】站点测试要"像浏览器一样"走一遍完整链路
// （本地监听口 → 前置 → 出口），所以需要一个 SOCKS5 **客户端**；
// 而本项目已有的 SOCKS5 代码都是服务端。为这点功能引入 x/net（现在是间接依赖）
// 也能做，但自己写的好处是超时/关闭时机完全可控，且不牵动 go.mod。
//
// 只实现 no-auth + CONNECT（域名 / IPv4 / IPv6 三种目标），这也是我们自己的
// 监听器支持的全部形态（见 handler.go）。
func DialSOCKS5(ctx context.Context, proxyAddr, target string, timeout time.Duration) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, fmt.Errorf("目标地址不合法 %q: %w", target, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("目标端口不合法 %q", portStr)
	}

	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("连不上本地代理 %s: %w", proxyAddr, err)
	}
	// 握手期间必须有截止时间，否则对端不回包就会一直挂住
	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	if err := socks5Handshake(conn, host, port); err != nil {
		_ = conn.Close()
		return nil, err
	}

	// 握手完成，清掉截止时间 —— 之后的读写由调用方负责
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func socks5Handshake(conn net.Conn, host string, port int) error {
	// 1) 只提供 no-auth（0x00）
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	var greet [2]byte
	if _, err := io.ReadFull(conn, greet[:]); err != nil {
		return err
	}
	if greet[0] != 0x05 {
		return fmt.Errorf("SOCKS5 版本不对: %d", greet[0])
	}
	if greet[1] != 0x00 {
		return fmt.Errorf("SOCKS5 需要认证（方法 %d），本程序只支持无认证", greet[1])
	}

	// 2) CONNECT 请求
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("域名过长: %s", host)
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		return err
	}

	// 3) 应答：VER REP RSV ATYP BND.ADDR BND.PORT
	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return err
	}
	if head[0] != 0x05 {
		return fmt.Errorf("SOCKS5 应答版本不对: %d", head[0])
	}
	if head[1] != 0x00 {
		return fmt.Errorf("SOCKS5 连接失败: %s", socks5ReplyText(head[1]))
	}
	// 把绑定地址读掉，否则残留字节会污染后续读取
	var skip int
	switch head[3] {
	case 0x01:
		skip = 4
	case 0x04:
		skip = 16
	case 0x03:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			return err
		}
		skip = int(l[0])
	default:
		return fmt.Errorf("SOCKS5 应答里的地址类型不认识: %d", head[3])
	}
	buf := make([]byte, skip+2)
	_, err := io.ReadFull(conn, buf)
	return err
}

func socks5ReplyText(rep byte) string {
	switch rep {
	case 0x01:
		return "通用失败"
	case 0x02:
		return "规则不允许"
	case 0x03:
		return "网络不可达"
	case 0x04:
		return "主机不可达"
	case 0x05:
		return "连接被拒绝"
	case 0x06:
		return "TTL 过期"
	case 0x07:
		return "命令不支持"
	case 0x08:
		return "地址类型不支持"
	}
	return fmt.Sprintf("未知错误码 %d", rep)
}
