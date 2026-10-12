package wgproxy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/murongruolan/picohop/internal/config"
	"github.com/murongruolan/picohop/internal/proxy"
)

// TestPlainUDPTransport 把 UDP 传输层单独拎出来验证：
// 一个最朴素的 net.ListenUDP 接收方，能不能收到 plainUDPChannel 发出去的包。
//
// 这一层如果都不通，WireGuard 必然握不上手，而且现象极具误导性
// （wireguard-go 只会说"一直在发握手包"，看不出是发送方自己的问题）。
func TestPlainUDPTransport(t *testing.T) {
	// 接收方：绑在 127.0.0.1 上（纯 IPv4）
	recv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("接收端绑定失败：%v", err)
	}
	defer recv.Close()
	recvAddr := recv.LocalAddr().(*net.UDPAddr)
	t.Logf("接收端 %v", recvAddr)

	// 发送方：PicoHop 的直连 UDP 通道
	front := proxy.NewFront(config.Front{Type: config.FrontTypeDirect, TimeoutMs: 5000})
	defer front.Close()
	ch, err := front.DialUDP(context.Background())
	if err != nil {
		t.Fatalf("建立 UDP 通道失败：%v", err)
	}
	defer ch.Close()

	want := []byte("picohop-udp-transport-probe")
	if err := ch.SendToUDP(want, recvAddr.IP.String(), recvAddr.Port); err != nil {
		t.Fatalf("发送失败：%v", err)
	}

	_ = recv.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 512)
	n, from, err := recv.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("❌ 接收端没收到任何数据（传输层有问题）：%v", err)
	}
	if string(buf[:n]) != string(want) {
		t.Fatalf("收到内容不一致：%q", buf[:n])
	}
	t.Logf("✅ 传输层正常：收到 %d 字节，来自 %v", n, from)

	// 反向：接收端回一个包，通道要能收到
	if _, err := recv.WriteToUDP([]byte("pong"), from); err != nil {
		t.Fatalf("回包失败：%v", err)
	}
	got := make(chan string, 1)
	go func() {
		b := make([]byte, 512)
		n, _, _, rerr := ch.ReceiveFromUDP(nil, b)
		if rerr == nil {
			got <- string(b[:n])
		}
	}()
	select {
	case s := <-got:
		if s != "pong" {
			t.Fatalf("回程内容不一致：%q", s)
		}
		t.Log("✅ 回程也正常")
	case <-time.After(3 * time.Second):
		t.Fatal("❌ 回程收不到数据")
	}
}
