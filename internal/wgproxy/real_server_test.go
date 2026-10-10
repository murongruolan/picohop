package wgproxy

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"chainproxy/internal/config"
	"chainproxy/internal/proxy"
)

// ---------------------------------------------------------------------------
// 对**真实 WireGuard 服务端**的连接测试。
//
// 默认跳过（避免日常 go test 依赖外部环境）。启用方式：
//
//	$env:CHAINPROXY_WG_REAL=1
//	$env:CHAINPROXY_WG_ENDPOINT=203.0.113.10:51820
//	$env:CHAINPROXY_WG_PRIVKEY=<base64 私钥>
//	$env:CHAINPROXY_WG_PUBKEY=<base64 对端公钥>
//	$env:CHAINPROXY_WG_PSK=<base64 预共享密钥，可留空>
//	$env:CHAINPROXY_WG_ADDR=10.8.0.2/32
//	$env:CHAINPROXY_WG_ALLOWED=192.168.31.0/24, 10.8.0.0/24
//	$env:CHAINPROXY_WG_FRONT=direct|socks5
//	go test ./internal/wgproxy -run TestWireGuardRealServer -v
//
// 这个测试的价值在于：它把"握手失败"拆成可判定的几步
// （UDP 能否发出去、对端有没有回包、回包能否解密），
// 而不是笼统地报一句"超时"。
// ---------------------------------------------------------------------------

func TestWireGuardRealServer(t *testing.T) {
	if os.Getenv("CHAINPROXY_WG_REAL") == "" {
		t.Skip("未设置 CHAINPROXY_WG_REAL，跳过真实服务端测试")
	}

	endpoint := envOr("CHAINPROXY_WG_ENDPOINT", "203.0.113.10:51820")
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		t.Fatalf("端点格式错误 %q：%v", endpoint, err)
	}
	port, _ := strconv.Atoi(portStr)

	cfg := config.Upstream{
		Name:       "真实WG",
		Type:       config.UpstreamTypeWireGuard,
		ListenHost: "127.0.0.1",
		ListenPort: 10813,
		TimeoutMs:  20000,
		WireGuard: &config.WireGuardConfig{
			PrivateKey:          os.Getenv("CHAINPROXY_WG_PRIVKEY"),
			Address:             envOr("CHAINPROXY_WG_ADDR", "10.8.0.2/32"),
			PeerPublicKey:       os.Getenv("CHAINPROXY_WG_PUBKEY"),
			PresharedKey:        os.Getenv("CHAINPROXY_WG_PSK"),
			EndpointHost:        host,
			EndpointPort:        port,
			AllowedIPs:          envOr("CHAINPROXY_WG_ALLOWED", "10.8.0.0/24"),
			PersistentKeepalive: 25,
			MTU:                 1420,
		},
	}
	if cfg.WireGuard.PrivateKey == "" || cfg.WireGuard.PeerPublicKey == "" {
		t.Fatal("必须设置 CHAINPROXY_WG_PRIVKEY 与 CHAINPROXY_WG_PUBKEY")
	}

	// ---- 第 0 步：先确认 UDP 到端点根本没被拦 ----
	t.Logf("端点 %s，本机前置模式 = %s", endpoint, envOr("CHAINPROXY_WG_FRONT", "direct"))

	// 先把"本端公钥"算出来打印。服务端的 peer 配置里必须就是这一个，
	// 否则 WireGuard 只会静默丢包，看不出任何错误。
	if myPub, kerr := PublicKeyFromPrivate(cfg.WireGuard.PrivateKey); kerr == nil {
		t.Logf("本端公钥（服务端 peer 必须配置这一个）= %s", myPub)
	} else {
		t.Logf("私钥解析失败：%v", kerr)
	}
	t.Logf("对端公钥（我们用来连接的服务端）= %s", cfg.WireGuard.PeerPublicKey)

	frontCfg := config.Front{Type: config.FrontTypeDirect, TimeoutMs: 8000}
	if envOr("CHAINPROXY_WG_FRONT", "direct") == "socks5" {
		frontCfg = config.Front{Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10808, TimeoutMs: 8000}
	}
	front := proxy.NewFront(frontCfg)
	defer front.Close()

	udpCh, err := front.DialUDP(context.Background())
	if err != nil {
		t.Fatalf("第 0 步失败：无法建立 UDP 通道：%v", err)
	}
	defer udpCh.Close()

	// 发一个 32 字节的探针，看对端是否有任何回应。
	// WireGuard 服务端对非法包通常是静默丢弃，但如果连"发了"都做不到，
	// 就会在这里暴露出来（比如 前置代理不支持 UDP ASSOCIATE）。
	probe := make([]byte, 32)
	probe[0] = 0x01 // 伪造一个 handshake initiation 类型
	if err := udpCh.SendToUDP(probe, host, port); err != nil {
		t.Fatalf("第 0 步失败：UDP 发不出去：%v", err)
	}
	t.Log("第 0 步通过：UDP 探针已发出（对端对非法包静默丢弃属正常）")

	// ---- 第 1 步：起隧道 ----
	up, err := New(cfg, front)
	if err != nil {
		t.Fatalf("构造上游失败：%v", err)
	}
	real := up.(*Upstream)
	real.logf = func(format string, args ...any) { t.Logf(format, args...) }

	dctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	start := time.Now()
	if err := real.Start(dctx, front); err != nil {
		// 失败时把 wireguard-go 的 UAPI 状态打出来，能看到 last_handshake / tx / rx
		if real.dev != nil {
			if out, gerr := real.dev.IpcGet(); gerr == nil {
				t.Logf("失败时的设备状态：\n%s", out)
			}
		}
		t.Fatalf("第 1 步失败：隧道未建立（耗时 %d ms）：%v", time.Since(start).Milliseconds(), err)
	}
	defer real.Stop()
	t.Logf("第 1 步通过：隧道握手成功，耗时 %d ms", time.Since(start).Milliseconds())

	// ---- 第 2 步：隧道内实际通信 ----
	// 判定标准要说清楚，否则很容易把"通了"误判成"没通"：
	//   - 连上了             → 隧道通了，且目标端口有服务
	//   - connection refused → 隧道通了（SYN 进隧道、对端回了 RST），只是该端口没服务
	//   - timeout            → 才是真的没通
	target := envOr("CHAINPROXY_WG_TARGET", "10.8.0.1:80")
	c, err := real.Dial(dctx, front, target)
	switch {
	case err == nil:
		_ = c.Close()
		t.Logf("✅ 第 2 步通过：隧道内 TCP 连接 %s 成功（目标端口有服务）", target)
	case isConnectionRefused(err):
		t.Logf("✅ 第 2 步通过：%s 返回 connection refused —— "+
			"说明 SYN 已穿过隧道且对端回了 RST，隧道双向可用（该端口只是没有服务）", target)
	default:
		t.Fatalf("❌ 第 2 步失败：经隧道连 %s 超时/出错，隧道没能承载流量：%v", target, err)
	}
}

// isConnectionRefused 判断错误是不是"对端明确拒绝"。
// 这是"隧道通了"的正面证据，绝不能当成失败。
func isConnectionRefused(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "refused")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var _ = fmt.Sprint
