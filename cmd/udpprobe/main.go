// Command udpprobe 是 UDP 端到端探针：经 SOCKS5 UDP ASSOCIATE 发 DNS 查询，看回包能不能回来。
//
// 用法：
//
//	go run ./cmd/udpprobe <socks主机:端口> <DNS服务器IP:端口[,更多目标...]> [超时秒]
//
// 退出码：0 = 所有目标都拿到了应答；1 = 失败（原因打到 stdout）；2 = 用法错误。
//
// 【为什么要有这个工具】这是本项目 UDP 通道唯一的"真机验证手段"：
// 它走的就是客户端那条路（本地端口 → SOCKS5 UDP ASSOCIATE → 出口 → 目标），
// 能一次覆盖"多目标分派、回程地址、会话回收"。命令行传多个目标（逗号分隔）时，
// 它们会在**同一个会话**里并发发出去 —— 这正是旧轮询实现最容易露馅的地方。
// 可先 `picohop-cli.exe run -node <节点>` 起一个节点，再对它打这个探针。
//
// 只用标准库：本项目 go.mod 里没有 SOCKS5 UDP 客户端库（x/net/proxy 也不导出 UDP 那部分）。
//
// 用法：go run udp_probe.go <socks主机:端口> <DNS服务器IP:端口> [超时秒]
//
// 【为什么要自己手写 SOCKS5】标准库里没有 SOCKS5 UDP 客户端（x/net/proxy 也不导出
// UDP 那个部分），而这件事必须走真正的 UDP ASSOCIATE —— 正是本项目 UDP 通道的入口。
package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("用法: udpprobe <socks主机:端口> <DNS IP:端口[,更多目标...]> [超时秒]")
		fmt.Println("例:   go run ./cmd/udpprobe 127.0.0.1:10811 1.1.1.1:53,8.8.8.8:53 15")
		os.Exit(2)
	}
	socksAddr, dnsAddr := os.Args[1], os.Args[2]
	timeout := 10 * time.Second
	if len(os.Args) > 3 {
		if s, err := strconv.Atoi(os.Args[3]); err == nil {
			timeout = time.Duration(s) * time.Second
		}
	}

	if err := run(socksAddr, dnsAddr, timeout); err != nil {
		fmt.Printf("UDP FAIL: %v\n", err)
		os.Exit(1)
	}
}

func run(socksAddr, dnsAddr string, timeout time.Duration) error {
	// ① TCP 控制连接 + 无认证握手 + UDP ASSOCIATE
	tcp, err := net.DialTimeout("tcp", socksAddr, timeout)
	if err != nil {
		return fmt.Errorf("连 SOCKS5 控制连接失败: %w", err)
	}
	defer tcp.Close()
	_ = tcp.SetDeadline(time.Now().Add(timeout))

	if _, err := tcp.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	greet := make([]byte, 2)
	if _, err := readFull(tcp, greet); err != nil {
		return fmt.Errorf("读握手应答失败: %w", err)
	}
	if greet[0] != 0x05 || greet[1] != 0x00 {
		return fmt.Errorf("SOCKS5 握手被拒: % x", greet)
	}
	// cmd=0x03(UDP ASSOCIATE) atyp=0x01(IPv4) 0.0.0.0:0
	if _, err := tcp.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	rep := make([]byte, 10)
	if _, err := readFull(tcp, rep); err != nil {
		return fmt.Errorf("读 ASSOCIATE 应答失败: %w", err)
	}
	if rep[1] != 0x00 {
		return fmt.Errorf("UDP ASSOCIATE 被拒: REP=0x%02x", rep[1])
	}
	relayIP := net.IPv4(rep[4], rep[5], rep[6], rep[7])
	relayPort := int(binary.BigEndian.Uint16(rep[8:10]))
	if relayIP.IsUnspecified() {
		// 有的实现回 0.0.0.0:0，意思是"用控制连接那条 TCP 的地址"
		host, _, _ := net.SplitHostPort(tcp.RemoteAddr().String())
		relayIP = net.ParseIP(host)
	}
	relay := &net.UDPAddr{IP: relayIP, Port: relayPort}
	fmt.Printf("UDP ASSOCIATE 已建立，relay=%s\n", relay)

	udp, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		return fmt.Errorf("连 relay 失败: %w", err)
	}
	defer udp.Close()
	_ = udp.SetDeadline(time.Now().Add(timeout))

	// ② 一次会话里同时发往**多个**目标（这条路径对应"目标表 + 分派"，
	// 也是旧实现最容易露馅的地方：串行轮询会让第二个目标等第一个的超时）
	targets := splitComma(dnsAddr)
	want := map[string]bool{}
	start := time.Now()
	for i, dst := range targets {
		ip := net.ParseIP(hostOf(dst))
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("本探针只支持 IPv4 目标: %q", dst)
		}
		packet := []byte{0, 0, 0, 0x01}
		packet = append(packet, ip.To4()...)
		packet = binary.BigEndian.AppendUint16(packet, uint16(portOf(dst)))
		packet = append(packet, dnsQuery(uint16(0x1200+i), "example.com")...)
		if _, err := udp.Write(packet); err != nil {
			return fmt.Errorf("发往 %s 失败: %w", dst, err)
		}
		want[dst] = true
	}

	buf := make([]byte, 2048)
	got := map[string]string{}
	for len(got) < len(targets) {
		read, from, err := udp.ReadFrom(buf)
		if err != nil {
			return fmt.Errorf("等回包失败（已收到 %d/%d 个目标）: %w", len(got), len(targets), err)
		}
		if read < 10 || buf[3] != 0x01 {
			continue
		}
		ips := dnsAnswers(buf[10:read])
		if len(ips) == 0 {
			continue
		}
		name := net.IPv4(buf[4], buf[5], buf[6], buf[7]).String() + ":" + strconv.Itoa(int(binary.BigEndian.Uint16(buf[8:10])))
		got[name] = ips[0]
		fmt.Printf("  回包来自 %s -> %s（%s）\n", name, ips[0], from)
	}
	fmt.Printf("UDP OK: 会话内 %d 个目标全部拿到应答，总耗时 %v\n", len(got), time.Since(start).Round(time.Millisecond))
	return nil
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// dnsQuery 组一条最小的 A 查询。
func dnsQuery(id uint16, name string) []byte {
	q := make([]byte, 0, 64)
	q = binary.BigEndian.AppendUint16(q, id)
	q = binary.BigEndian.AppendUint16(q, 0x0100) // 标准查询 + 递归期望
	q = binary.BigEndian.AppendUint16(q, 1)      // qdcount
	q = binary.BigEndian.AppendUint16(q, 0)      // ancount
	q = binary.BigEndian.AppendUint16(q, 0)      // nscount
	q = binary.BigEndian.AppendUint16(q, 0)      // arcount
	for _, part := range splitDots(name) {
		q = append(q, byte(len(part)))
		q = append(q, part...)
	}
	q = append(q, 0)
	q = binary.BigEndian.AppendUint16(q, 1) // type A
	q = binary.BigEndian.AppendUint16(q, 1) // class IN
	return q
}

// dnsAnswers 从应答里挑出 A 记录（只处理不被压缩指针绕晕的最简情形）。
func dnsAnswers(msg []byte) []string {
	if len(msg) < 12 {
		return nil
	}
	ancount := int(binary.BigEndian.Uint16(msg[6:8]))
	qdcount := int(binary.BigEndian.Uint16(msg[4:6]))
	off := 12
	for i := 0; i < qdcount; i++ {
		var ok bool
		off, ok = skipName(msg, off)
		if !ok || off+4 > len(msg) {
			return nil
		}
		off += 4
	}
	var out []string
	for i := 0; i < ancount; i++ {
		var ok bool
		off, ok = skipName(msg, off)
		if !ok || off+10 > len(msg) {
			return out
		}
		typ := binary.BigEndian.Uint16(msg[off : off+2])
		rdlen := int(binary.BigEndian.Uint16(msg[off+8 : off+10]))
		off += 10
		if off+rdlen > len(msg) {
			return out
		}
		if typ == 1 && rdlen == 4 {
			out = append(out, net.IPv4(msg[off], msg[off+1], msg[off+2], msg[off+3]).String())
		}
		off += rdlen
	}
	return out
}

func skipName(msg []byte, off int) (int, bool) {
	for {
		if off >= len(msg) {
			return off, false
		}
		b := msg[off]
		if b == 0 {
			return off + 1, true
		}
		if b&0xC0 == 0xC0 { // 压缩指针
			return off + 2, true
		}
		off += int(b) + 1
	}
}

func splitDots(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '.' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func hostOf(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}

func portOf(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 53
	}
	n, _ := strconv.Atoi(p)
	return n
}

func readFull(c net.Conn, buf []byte) (int, error) {
	got := 0
	for got < len(buf) {
		n, err := c.Read(buf[got:])
		got += n
		if err != nil {
			return got, err
		}
	}
	return got, nil
}
