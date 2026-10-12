package wgproxy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// TestVanillaLoopback 是"对照组"：两端都用 wireguard-go 自带的 conn.NewDefaultBind()，
// 完全不经过 PicoHop 的 socksBind。
//
// 目的：把问题切开。
//   - 如果这个测试能握手成功 → 说明"服务端配置 + API 用法 + 本机回环 UDP"都没问题，
//     那么之前 e2e 失败一定是 socksBind/plainUDPChannel 这一层的问题；
//   - 如果这个也失败 → 说明是我的测试脚手架（回环 + 双栈 UDP 等）有问题，
//     而不是 PicoHop 的实现。
//
// 这种"对照组"排查方式比反复猜有用得多。
func TestVanillaLoopback(t *testing.T) {
	serverKeys := genKeys(t)
	clientKeys := genKeys(t)

	const (
		clientIP = "10.99.0.2"
		serverIP = "10.99.0.1"
	)

	// ---- 服务端 ----
	serverTun, _, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr(serverIP)}, nil, 1420)
	if err != nil {
		t.Fatalf("服务端网络栈创建失败：%v", err)
	}
	serverDev := device.NewDevice(serverTun, conn.NewDefaultBind(),
		device.NewLogger(device.LogLevelVerbose, "vanilla-server "))
	defer serverDev.Close()

	err = serverDev.IpcSet(fmt.Sprintf(
		"private_key=%s\nlisten_port=0\npublic_key=%s\nallowed_ip=%s/32\n",
		serverKeys.privHex, clientKeys.pubHex, clientIP))
	if err != nil {
		t.Fatalf("服务端配置失败：%v", err)
	}
	if err := serverDev.Up(); err != nil {
		t.Fatalf("服务端启动失败：%v", err)
	}
	port := 0
	{
		out, _ := serverDev.IpcGet()
		fmt.Sscanf(extractKV(out, "listen_port"), "%d", &port)
	}
	if port == 0 {
		t.Fatal("读不到服务端监听端口")
	}
	t.Logf("服务端监听 127.0.0.1:%d", port)

	// ---- 客户端（标准 bind，不用我们的 socksBind） ----
	clientTun, _, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr(clientIP)}, nil, 1420)
	if err != nil {
		t.Fatalf("客户端网络栈创建失败：%v", err)
	}
	clientDev := device.NewDevice(clientTun, conn.NewDefaultBind(),
		device.NewLogger(device.LogLevelVerbose, "vanilla-client "))
	defer clientDev.Close()

	err = clientDev.IpcSet(fmt.Sprintf(
		"private_key=%s\nlisten_port=0\npublic_key=%s\nallowed_ip=%s/32\nendpoint=127.0.0.1:%d\npersistent_keepalive_interval=25\n",
		clientKeys.privHex, serverKeys.pubHex, serverIP, port))
	if err != nil {
		t.Fatalf("客户端配置失败：%v", err)
	}
	if err := clientDev.Up(); err != nil {
		t.Fatalf("客户端启动失败：%v", err)
	}

	// ---- 等握手 ----
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := clientDev.IpcGet()
		if extractKV(out, "last_handshake_time_sec") != "0" {
			t.Logf("✅ 标准 bind 两端握手成功（对照组通过）")
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	out, _ := clientDev.IpcGet()
	t.Fatalf("❌ 连标准 bind 都握不上手，说明问题在测试脚手架而非 socksBind：\n%s", out)
}

// extractKV 从 UAPI 文本里取一个键的值。
func extractKV(uapi, key string) string {
	for _, line := range splitLines(uapi) {
		if v, ok := cutPrefix(line, key+"="); ok {
			return v
		}
	}
	return ""
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

var _ = rand.Read
var _ = hex.EncodeToString
