package wgproxy

import (
	"context"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/murongruolan/picohop/internal/config"
	"github.com/murongruolan/picohop/internal/proxy"
)

// TestSocksUDPRelayToLAN 专门验证一件事：前置代理（主流客户端）
// 到底有没有把 UDP 转发到**局域网地址**。
//
// 做法：在本机起一个 UDP 回声服务，绑在 0.0.0.0 上，
// 然后经前置代理把包发到「本机局域网 IP:该端口」。
// 收到回声 = 前置确实转发了局域网 UDP；收不到 = 前置把这条 UDP 吞了。
//
// 用回声服务而不是 DNS，是为了避免"前置自己答了 DNS"这种假阳性。
//
// 启用：PICOHOP_UDP_LAN_TEST=1（可选 PICOHOP_UDP_LAN_IP 指定本机局域网 IP）
func TestSocksUDPRelayToLAN(t *testing.T) {
	if os.Getenv("PICOHOP_UDP_LAN_TEST") == "" {
		t.Skip("未设置 PICOHOP_UDP_LAN_TEST，跳过")
	}

	// 本机局域网 IP
	lanIP := os.Getenv("PICOHOP_UDP_LAN_IP")
	if lanIP == "" {
		lanIP = detectLANIP()
	}
	if lanIP == "" {
		t.Fatal("无法确定本机局域网 IP，请设置 PICOHOP_UDP_LAN_IP")
	}

	// UDP 回声服务
	echo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("起回声服务失败：%v", err)
	}
	defer echo.Close()
	echoPort := echo.LocalAddr().(*net.UDPAddr).Port
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, rerr := echo.ReadFromUDP(buf)
			if rerr != nil {
				return
			}
			_, _ = echo.WriteToUDP(append([]byte("echo:"), buf[:n]...), from)
		}
	}()
	t.Logf("回声服务 0.0.0.0:%d，目标将用 %s:%d", echoPort, lanIP, echoPort)

	front := proxy.NewFront(config.Front{
		Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10808, TimeoutMs: 8000,
	})
	defer front.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	ch, err := front.DialUDP(ctx)
	if err != nil {
		t.Fatalf("前置不支持 UDP ASSOCIATE：%v", err)
	}
	defer ch.Close()

	// 先确认这个回声服务本身是好的（直连打一发）
	direct, derr := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP(lanIP), Port: echoPort})
	if derr == nil {
		_, _ = direct.Write([]byte("direct"))
		_ = direct.SetReadDeadline(time.Now().Add(2 * time.Second))
		b := make([]byte, 128)
		if n, _, rerr := direct.ReadFromUDP(b); rerr == nil {
			t.Logf("对照组：直连回声正常（%s）", string(b[:n]))
		} else {
			t.Logf("对照组：直连回声失败 %v（回声服务可能被防火墙挡了，结果不可信）", rerr)
		}
		_ = direct.Close()
	}

	// 发送长度可调：用来验证"前置是不是按包大小/内容丢包"
	sendLen := 9
	if v := os.Getenv("PICOHOP_UDP_LAN_TEST_LEN"); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 {
			sendLen = n
		}
	}
	payload := make([]byte, sendLen)
	for i := range payload {
		payload[i] = byte(i)
	}
	if os.Getenv("PICOHOP_UDP_LAN_TEST_WG") != "" {
		payload[0] = 0x01 // 伪装成 WireGuard 握手类型，排查按协议嗅探丢弃
	}
	t.Logf("发送 %d 字节（首字节=0x%02x）", sendLen, payload[0])

	if err := ch.SendToUDP(payload, lanIP, echoPort); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	t.Logf("已经由前置发出：%s:%d", lanIP, echoPort)

	buf := make([]byte, 2048)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		n, host, port, rerr := ch.ReceiveFromUDP(nil, buf)
		if rerr != nil {
			t.Logf("接收出错：%v", rerr)
			break
		}
		t.Logf("✅ 前置确实转发了局域网 UDP：收到回声 %q 来自 %s:%d", string(buf[:n]), host, port)
		return
	}
	t.Fatalf("❌ 前置没有把 UDP 转发到局域网 %s:%d（只发出去了，收不到任何回包）", lanIP, echoPort)
}

// detectLANIP 找一个本机的局域网 IPv4 地址。
func detectLANIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, aerr := ifc.Addrs()
		if aerr != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			// 只挑 192.168.x / 10.x 这类私有地址
			if ip4[0] == 192 && ip4[1] == 168 {
				return ip4.String()
			}
		}
	}
	return ""
}
