package wgproxy

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"chainproxy/internal/config"
	"chainproxy/internal/proxy"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// TestWireGuardThroughFrontOnLAN 是判断"到底是 前置代理的问题，还是那台 WG 服务器的问题"的关键实验。
//
// 做法：在本机的**局域网 IP** 上跑一个真正的 WireGuard 服务端，
// 然后让 ChainProxy 的 WireGuard 客户端**经前置代理（前置代理）**去连它。
//
//   - 如果握手成功 → 说明 前置代理能正常转发 WireGuard 的 UDP，
//     那 192.168.31.94 连不上就是**那台服务器/它的防火墙**的问题；
//   - 如果握手失败 → 说明问题在前置这一侧。
//
// 启用：CHAINPROXY_WG_LAN_FRONT_TEST=1
func TestWireGuardThroughFrontOnLAN(t *testing.T) {
	if os.Getenv("CHAINPROXY_WG_LAN_FRONT_TEST") == "" {
		t.Skip("未设置 CHAINPROXY_WG_LAN_FRONT_TEST，跳过")
	}

	lanIP := os.Getenv("CHAINPROXY_UDP_LAN_IP")
	if lanIP == "" {
		lanIP = detectLANIP()
	}
	if lanIP == "" {
		t.Fatal("无法确定本机局域网 IP")
	}

	// ---- 服务端：绑在局域网 IP 上 ----
	serverKeys := genKeys(t)
	clientKeys := genKeys(t)
	const (
		clientIP = "10.88.0.2"
		serverIP = "10.88.0.1"
	)

	serverTun, serverNet, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr(serverIP)}, nil, 1420)
	if err != nil {
		t.Fatalf("服务端网络栈失败：%v", err)
	}
	serverDev := device.NewDevice(serverTun, conn.NewDefaultBind(),
		device.NewLogger(device.LogLevelVerbose, "lan-srv "))
	defer serverDev.Close()

	if err := serverDev.IpcSet(fmt.Sprintf(
		"private_key=%s\nlisten_port=0\npublic_key=%s\nallowed_ip=%s/32\n",
		serverKeys.privHex, clientKeys.pubHex, clientIP)); err != nil {
		t.Fatalf("服务端配置失败：%v", err)
	}
	if err := serverDev.Up(); err != nil {
		t.Fatalf("服务端启动失败：%v", err)
	}
	port := 0
	if out, gerr := serverDev.IpcGet(); gerr == nil {
		fmt.Sscanf(extractKV(out, "listen_port"), "%d", &port)
	}
	if port == 0 {
		t.Fatal("读不到服务端端口")
	}

	// 服务端上加一个 TCP 回声，用于验证隧道真的能承载流量
	ln, lerr := serverNet.ListenTCP(&net.TCPAddr{IP: net.ParseIP(serverIP), Port: 9000})
	if lerr != nil {
		t.Fatalf("服务端监听 9000 失败：%v", lerr)
	}
	defer ln.Close()
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(cc net.Conn) {
				defer cc.Close()
				b := make([]byte, 256)
				n, _ := cc.Read(b)
				_, _ = cc.Write(b[:n])
			}(c)
		}
	}()

	t.Logf("本机局域网 WG 服务端 = %s:%d", lanIP, port)

	// ---- 客户端：经前置代理 前置 ----
	front := proxy.NewFront(config.Front{
		Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10808, TimeoutMs: 8000,
	})
	defer front.Close()

	cfg := config.Upstream{
		Name: "局域网前置测试", Type: config.UpstreamTypeWireGuard,
		ListenHost: "127.0.0.1", ListenPort: 10830, TimeoutMs: 20000,
		WireGuard: &config.WireGuardConfig{
			PrivateKey:    hexToB64(clientKeys.privHex),
			Address:       clientIP + "/32",
			PeerPublicKey: hexToB64(serverKeys.pubHex),
			EndpointHost:  lanIP,
			EndpointPort:  port,
			AllowedIPs:    serverIP + "/32",
			// 【必须有】keepalive=0 且没有数据要发时，WireGuard 不会主动发起握手，
			// 于是表现为 tx_bytes=0、一直没有握手包。测试里漏掉这项会得出错误结论。
			PersistentKeepalive: 25, MTU: 1420,
		},
	}
	up, err := New(cfg, front)
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	real := up.(*Upstream)
	real.logf = func(format string, args ...any) { t.Logf(format, args...) }

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	start := time.Now()
	if err := real.Start(ctx, front); err != nil {
		t.Fatalf("❌ 经前置代理 连本机局域网 WG 服务端失败（%d ms）：%v\n"+
			"   → 说明问题在前置代理这一侧，不是那台 192.168.31.94 的问题",
			time.Since(start).Milliseconds(), err)
	}
	defer real.Stop()
	t.Logf("✅ 经前置代理 连本机局域网 WG 服务端握手成功，耗时 %d ms", time.Since(start).Milliseconds())
	t.Logf("   → 说明 前置代理能正常转发 WireGuard 的 UDP；192.168.31.94 连不上是那台服务器那一侧的问题")

	c, derr := real.Dial(ctx, front, serverIP+":9000")
	if derr != nil {
		t.Fatalf("隧道内连接回声服务失败：%v", derr)
	}
	defer c.Close()
	if _, werr := c.Write([]byte("lan-front-echo")); werr != nil {
		t.Fatalf("隧道内写失败：%v", werr)
	}
	buf := make([]byte, 256)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, rerr := c.Read(buf)
	if rerr != nil {
		t.Fatalf("隧道内读失败：%v", rerr)
	}
	t.Logf("✅ 隧道内 TCP 往返正常：%q", string(buf[:n]))
}

// hexToB64 把 UAPI 用的 hex 密钥转成配置用的 base64。
func hexToB64(h string) string {
	b, err := hex.DecodeString(h)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}
