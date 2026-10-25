package app

import (
	"testing"

	"github.com/murongruolan/picohop/internal/config"
)

// TestSubscriptionProxyPicksFrontFirst "经代理更新"取哪个代理：前置优先，其次第一个在跑的节点。
func TestSubscriptionProxyPicksFrontFirst(t *testing.T) {
	// 1) 前置可用（且带认证）→ 用前置
	a := newTestApp(t, nil, config.Front{
		Type: config.FrontTypeSocks5, Host: "1.2.3.4", Port: 1080, Username: "u", Password: "p",
	})
	if got := a.subscriptionProxy(); got != "socks5://u:p@1.2.3.4:1080" {
		t.Fatalf("前置可用时应当用前置（含凭据），实际 %q", got)
	}

	// 2) 前置直连、也没有节点在跑 → 空串（调用方会记一行日志说明本次改走直连）
	b := newTestApp(t, []config.Node{{
		ID: "n1", Name: "测试节点", Protocol: config.ProtocolSOCKS5,
		ListenHost: "127.0.0.1", Server: "127.0.0.1", Port: 9,
	}}, config.Front{Type: config.FrontTypeDirect})
	if got := b.subscriptionProxy(); got != "" {
		t.Fatalf("前置直连且没有节点在跑时应当返回空串，实际 %q", got)
	}

	// 3) 把一个节点跑起来 → 回落到它的本机端口
	if err := b.Activate([]string{"n1"}); err != nil {
		t.Fatalf("启动节点失败：%v", err)
	}
	defer func() { _ = b.Deactivate([]string{"n1"}) }()
	n, _ := b.GetNode("n1")
	if n.ListenPort <= 0 {
		t.Fatalf("节点启动后应当有本机端口：%+v", n)
	}
	want := nodeSocksURL(config.Node{ListenHost: "127.0.0.1", ListenPort: n.ListenPort})
	if got := b.subscriptionProxy(); got != want {
		t.Fatalf("前置直连时应回落到运行中的节点端口：想要 %q，实际 %q", want, got)
	}
}

// TestNodeSocksURL 本机节点端口：开了局域网鉴权时要把凭据带上，否则会被自己的端口拒绝。
func TestNodeSocksURL(t *testing.T) {
	if got := nodeSocksURL(config.Node{ListenHost: "127.0.0.1", ListenPort: 10810}); got != "socks5://127.0.0.1:10810" {
		t.Fatalf("无鉴权时 %q", got)
	}
	got := nodeSocksURL(config.Node{
		ListenHost: "0.0.0.0", ListenPort: 10810,
		ListenUsername: "u", ListenPassword: "p@ss",
	})
	if got != "socks5://u:p%40ss@0.0.0.0:10810" {
		t.Fatalf("带鉴权时 %q（凭据要转义）", got)
	}
	if got := nodeSocksURL(config.Node{ListenPort: 10811}); got != "socks5://127.0.0.1:10811" {
		t.Fatalf("监听地址为空时应兜底 127.0.0.1，实际 %q", got)
	}
}
