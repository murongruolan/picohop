package proxy

import (
	"bytes"
	"net"
	"testing"
)

// TestAppendReadSocks5AddrRoundTrip 覆盖 RFC 1928 的三种地址类型编解码。
//
// 这组用例曾经救过一个真实 bug：socks5Command 里把 4 字节应答头
// （VER REP RSV ATYP）整个读走之后，又让 readSocks5Addr 再读一次 ATYP，
// 导致 ATYP 被读了两次、地址整体错位一位。
func TestAppendReadSocks5AddrRoundTrip(t *testing.T) {
	cases := []struct {
		host string
		port int
	}{
		{"1.2.3.4", 8080},
		{"2001:db8::1", 443},
		{"api.ipify.org", 443},
		{"node-a.example.com", 10000},
	}
	for _, c := range cases {
		buf, err := appendSocks5Addr([]byte{0x05, 0x01, 0x00}, c.host, c.port)
		if err != nil {
			t.Fatalf("appendSocks5Addr(%s): %v", c.host, err)
		}
		// 前 3 字节是 VER/CMD/RSV，剩余部分必须是完整可读的 ADDR
		gotHost, gotPort, err := readSocks5Addr(bytes.NewReader(buf[3:]))
		if err != nil {
			t.Fatalf("readSocks5Addr(%s): %v", c.host, err)
		}
		if gotHost != c.host {
			t.Errorf("host = %q, want %q", gotHost, c.host)
		}
		if gotPort != c.port {
			t.Errorf("port = %d, want %d", gotPort, c.port)
		}
	}
}

// TestSocks5ConnectWireFormat 用一个假服务端校验客户端发出的报文与读取的分隔。
func TestSocks5ConnectWireFormat(t *testing.T) {
	client, server := net.Pipe()

	done := make(chan error, 1)
	go func() {
		// greeting
		g := make([]byte, 3)
		if _, err := readFull(server, g); err != nil {
			done <- err
			return
		}
		if g[0] != 0x05 || g[1] != 0x01 || g[2] != 0x00 {
			done <- errf("greeting 错误: % x", g)
			return
		}
		if _, err := server.Write([]byte{0x05, 0x00}); err != nil {
			done <- err
			return
		}
		// request：VER CMD RSV ATYP(len) host port
		head := make([]byte, 4)
		if _, err := readFull(server, head); err != nil {
			done <- err
			return
		}
		if head[0] != 0x05 || head[1] != 0x01 || head[2] != 0x00 || head[3] != 0x03 {
			done <- errf("request 头错误: % x", head)
			return
		}
		n := int(head[3+1-1])
		_ = n
		hostLen := make([]byte, 1)
		if _, err := readFull(server, hostLen); err != nil {
			done <- err
			return
		}
		host := make([]byte, hostLen[0])
		if _, err := readFull(server, host); err != nil {
			done <- err
			return
		}
		portBytes := make([]byte, 2)
		if _, err := readFull(server, portBytes); err != nil {
			done <- err
			return
		}
		if string(host) != "example.com" || portBytes[0] != 0x01 || portBytes[1] != 0xBB {
			done <- errf("目标错误: %s:%d", host, int(portBytes[0])<<8|int(portBytes[1]))
			return
		}
		// reply: VER REP RSV ATYP BND.ADDR(4) BND.PORT(2) —— 共 10 字节
		_, err := server.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0x2a, 0x38})
		done <- err
	}()

	if err := socks5Connect(client, "", "", "example.com", 443); err != nil {
		t.Fatalf("socks5Connect: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("服务端校验失败: %v", err)
	}
	_ = client.Close()
	_ = server.Close()
}

// TestParseUDPHeader 覆盖 SOCKS5 UDP 封帧的解析。
func TestParseUDPHeader(t *testing.T) {
	frame, err := appendUDPHeader(nil, "1.1.1.1", 53, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	host, port, payload, err := parseUDPHeader(frame)
	if err != nil {
		t.Fatalf("parseUDPHeader: %v", err)
	}
	if host != "1.1.1.1" || port != 53 || string(payload) != "hello" {
		t.Fatalf("got %s:%d %q", host, port, payload)
	}

	// 分片包必须被拒绝
	bad := append([]byte{}, frame...)
	bad[2] = 0x01
	if _, _, _, err := parseUDPHeader(bad); err == nil {
		t.Fatal("应当拒绝 FRAG != 0 的数据报")
	}
}

// TestParseProxySpec 在 config 包之外做一次冒烟检查，防止格式解析回归。
func TestSplitHostPort(t *testing.T) {
	h, p, err := SplitHostPort("node-a.example.com:10000", 1080)
	if err != nil || h != "node-a.example.com" || p != 10000 {
		t.Fatalf("got %s:%d err=%v", h, p, err)
	}
	h, p, err = SplitHostPort("example.com", 1080)
	if err != nil || h != "example.com" || p != 1080 {
		t.Fatalf("got %s:%d err=%v", h, p, err)
	}
	h, p, err = SplitHostPort("[::1]:1080", 0)
	if err != nil || h != "::1" || p != 1080 {
		t.Fatalf("got %s:%d err=%v", h, p, err)
	}
}
