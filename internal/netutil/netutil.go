// Package netutil 提供端口探测与地址处理的小工具。
package netutil

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// PortAvailable 试绑定 host:port 判断端口是否可用。
// 注意：这是"独占"式探测 —— 只要能绑上就认为可用（哪怕端口处于 TIME_WAIT）。
func PortAvailable(host string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口 %d 非法", port)
	}
	if strings.TrimSpace(host) == "" {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("端口 %d 不可用：%w", port, err)
	}
	return ln.Close()
}

// FirstFreePort 从 start 开始向上寻找第一个可用端口，最多尝试 200 个。
func FirstFreePort(host string, start int) (int, error) {
	for i := 0; i < 200; i++ {
		p := start + i
		if p > 65535 {
			break
		}
		if err := PortAvailable(host, p); err == nil {
			return p, nil
		}
	}
	return 0, fmt.Errorf("从 %d 开始找不到可用端口", start)
}

// HostPortString 拼接 host:port，host 为空时用 127.0.0.1。
func HostPortString(host string, port int) string {
	if strings.TrimSpace(host) == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// LocalIP 尽力探测本机在默认出口上的 IPv4 地址，用于 UDP ASSOCIATE 返回地址。
func LocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err == nil {
		defer conn.Close()
		if ua, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			return ua.IP.String()
		}
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if v4 := ipnet.IP.To4(); v4 != nil {
					return v4.String()
				}
			}
		}
	}
	return "127.0.0.1"
}
