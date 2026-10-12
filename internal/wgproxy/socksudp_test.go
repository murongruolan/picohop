package wgproxy

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/murongruolan/picohop/internal/config"
	"github.com/murongruolan/picohop/internal/proxy"
)

// dialFrontIfAvailable 返回一个连到本地主流客户端的前置代理；没有就跳过测试。
func dialFrontIfAvailable(t *testing.T) proxy.Front {
	t.Helper()
	fc := config.Front{Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10808, TimeoutMs: 8000}
	front := proxy.NewFront(fc)

	// 先用 TCP 连一下，确认前置在
	d := net.Dialer{Timeout: 2 * time.Second}
	c, err := d.Dial("tcp", fc.Address())
	if err != nil {
		t.Skipf("前置代理 %s 不可用（未启动前置代理？）：%v", fc.Address(), err)
	}
	_ = c.Close()
	return front
}

// TestSocksUDPAssociateThroughFront 验证整条 WireGuard 方案的地基：
// 前置代理（前置代理）必须支持 SOCKS5 UDP ASSOCIATE，
// 否则 WireGuard 的握手包根本发不出去。
//
// 这里直接通过 UDP 通道向公网 DNS 发一个查询，能收到应答即证明 UDP 往返正常。
func TestSocksUDPAssociateThroughFront(t *testing.T) {
	front := dialFrontIfAvailable(t)
	defer front.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, err := front.DialUDP(ctx)
	if err != nil {
		t.Fatalf("前置代理不支持 UDP（UDP ASSOCIATE 失败）：%v", err)
	}
	defer ch.Close()

	// 组一个最小 DNS A 查询：example.com
	var q []byte
	q = append(q, 0xAB, 0xCD) // id
	q = append(q, 0x01, 0x00) // 标准查询，递归
	q = append(q, 0x00, 0x01) // qdcount
	q = append(q, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00)
	q = append(q, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e')
	q = append(q, 3, 'c', 'o', 'm')
	q = append(q, 0x00)       // root label
	q = append(q, 0x00, 0x01) // A
	q = append(q, 0x00, 0x01) // IN

	// 目标可用环境变量覆盖，用来区分"公网 UDP"与"私有地址 UDP"：
	//主流客户端之类的前置通常对 TCP 私有地址走直连，但 UDP 未必同样处理。
	dstHost := envOr("PICOHOP_UDP_TEST_HOST", "1.1.1.1")
	dstPort := 53
	if pv := os.Getenv("PICOHOP_UDP_TEST_PORT"); pv != "" {
		if n, e := strconv.Atoi(pv); e == nil {
			dstPort = n
		}
	}
	t.Logf("UDP 测试目标 %s:%d", dstHost, dstPort)
	if err := ch.SendToUDP(q, dstHost, dstPort); err != nil {
		t.Fatalf("发送 DNS 查询失败：%v", err)
	}

	buf := make([]byte, 1500)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		n, host, port, err := ch.ReceiveFromUDP(nil, buf)
		if err != nil {
			t.Fatalf("接收失败：%v", err)
		}
		if n < 12 {
			continue
		}
		if binary.BigEndian.Uint16(buf[0:2]) != 0xABCD {
			continue
		}
		if !isDNSAnswer(buf[:n]) {
			continue
		}
		t.Logf("✅ 前置代理 UDP 往返正常：来自 %s:%d 的 DNS 应答 %d 字节", host, port, n)
		return
	}
	t.Fatalf("8 秒内没有收到可用的 DNS 应答——前置代理的 UDP 通道可能被墙/被限")
}

func isDNSAnswer(pkt []byte) bool {
	flags := binary.BigEndian.Uint16(pkt[2:4])
	return flags&0x8000 != 0 && flags&0x000F == 0
}

var errNotImplemented = errors.New("not implemented")
