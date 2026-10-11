package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSocks5 起一个最小的 SOCKS5 服务端，用来验证客户端**实际发出去的字节**。
//
// 为什么要验字节：SOCKS5 是二进制协议，版本号、地址类型、端口字节序写错都会
// "连不上但看不出原因" —— 这类错误在真实使用中只会表现为"测试一直失败"。
func fakeSocks5(t *testing.T, replyCode byte, splitReply bool) (addr string, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	got = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// 问候：VER NMETHODS METHOD
		buf := make([]byte, 3)
		if _, err := io.ReadFull(conn, buf); err != nil {
			got <- "读问候失败: " + err.Error()
			return
		}
		if buf[0] != 0x05 || buf[1] != 0x01 || buf[2] != 0x00 {
			got <- fmt.Sprintf("问候不对: % x", buf)
			return
		}
		if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
			return
		}

		// 请求：VER CMD RSV ATYP ...
		head := make([]byte, 4)
		if _, err := io.ReadFull(conn, head); err != nil {
			got <- "读请求头失败: " + err.Error()
			return
		}
		desc := fmt.Sprintf("ver=%d cmd=%d atyp=%d", head[0], head[1], head[3])
		var host string
		switch head[3] {
		case 0x03:
			l := make([]byte, 1)
			if _, err := io.ReadFull(conn, l); err != nil {
				got <- "读域名长度失败"
				return
			}
			h := make([]byte, int(l[0]))
			if _, err := io.ReadFull(conn, h); err != nil {
				got <- "读域名失败"
				return
			}
			host = string(h)
		case 0x01:
			h := make([]byte, 4)
			if _, err := io.ReadFull(conn, h); err != nil {
				got <- "读 IPv4 失败"
				return
			}
			host = net.IP(h).String()
		default:
			got <- "不认识的地址类型: " + desc
			return
		}
		pb := make([]byte, 2)
		if _, err := io.ReadFull(conn, pb); err != nil {
			got <- "读端口失败"
			return
		}
		got <- fmt.Sprintf("%s host=%s port=%d", desc, host, binary.BigEndian.Uint16(pb))

		// 应答：VER REP RSV ATYP BND.ADDR BND.PORT
		reply := []byte{0x05, replyCode, 0x00, 0x01, 127, 0, 0, 1, 0x1f, 0x90}
		if splitReply {
			// 分两次写：专门验证客户端能处理"一次读不全"的情况
			if _, err := conn.Write(reply[:5]); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
			if _, err := conn.Write(reply[5:]); err != nil {
				return
			}
		} else {
			if _, err := conn.Write(reply); err != nil {
				return
			}
		}
		// 保持连接一会儿，免得客户端刚建好就被关掉
		time.Sleep(80 * time.Millisecond)
		_ = conn.Close()
	}()
	return ln.Addr().String(), got
}

func TestDialSOCKS5SendsCorrectRequest(t *testing.T) {
	addr, got := fakeSocks5(t, 0x00, false)

	conn, err := DialSOCKS5(context.Background(), addr, "example.com:443", 2*time.Second)
	if err != nil {
		t.Fatalf("握手应当成功，实际: %v", err)
	}
	defer conn.Close()

	select {
	case desc := <-got:
		// 域名走 atyp=3、cmd=1、端口按大端编码
		if !strings.Contains(desc, "ver=5 cmd=1 atyp=3") {
			t.Fatalf("请求头不对：%s", desc)
		}
		if !strings.Contains(desc, "host=example.com") {
			t.Fatalf("域名没传对：%s", desc)
		}
		if !strings.Contains(desc, "port=443") {
			t.Fatalf("端口没传对（大端序）：%s", desc)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("假服务端没收到请求")
	}
}

func TestDialSOCKS5HandlesSplitReply(t *testing.T) {
	// 应答被拆成两段：客户端必须读满，不能只看第一次 Read 的结果
	addr, _ := fakeSocks5(t, 0x00, true)
	conn, err := DialSOCKS5(context.Background(), addr, "example.com:443", 2*time.Second)
	if err != nil {
		t.Fatalf("应答分片时也应当成功，实际: %v", err)
	}
	_ = conn.Close()
}

func TestDialSOCKS5ReportsRefusal(t *testing.T) {
	addr, _ := fakeSocks5(t, 0x05, false)
	_, err := DialSOCKS5(context.Background(), addr, "example.com:443", 2*time.Second)
	if err == nil {
		t.Fatal("服务端拒绝时应当报错")
	}
	if !strings.Contains(err.Error(), "连接被拒绝") {
		t.Fatalf("错误信息应当说明原因，实际: %v", err)
	}
}

func TestDialSOCKS5RejectsBadTarget(t *testing.T) {
	if _, err := DialSOCKS5(context.Background(), "127.0.0.1:1", "example.com", time.Second); err == nil {
		t.Fatal("目标没有端口时应当报错")
	}
	if _, err := DialSOCKS5(context.Background(), "127.0.0.1:1", "example.com:99999", time.Second); err == nil {
		t.Fatal("端口越界时应当报错")
	}
}

func TestDialSOCKS5IPv4Target(t *testing.T) {
	addr, got := fakeSocks5(t, 0x00, false)
	conn, err := DialSOCKS5(context.Background(), addr, "1.2.3.4:8080", 2*time.Second)
	if err != nil {
		t.Fatalf("IPv4 目标应当成功，实际: %v", err)
	}
	defer conn.Close()
	select {
	case desc := <-got:
		if !strings.Contains(desc, "atyp=1") || !strings.Contains(desc, "host=1.2.3.4") {
			t.Fatalf("IPv4 目标应当用 atyp=1，实际：%s", desc)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("假服务端没收到请求")
	}
}
