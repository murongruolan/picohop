package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadV2ConfigSurvivesV3 守住"版本门槛"那个坑。
//
// 【为什么这条测试最要紧】migrate() 只认 v1 的外形（configV1）。如果 Load 里的门槛
// 写成 CurrentVersion，v2 配置就会被当成 v1 去解析 —— 字段全对不上，
// 结果是"升级程序之后所有节点凭空消失"。所以这里直接喂一份 v2 配置，
// 断言节点、前置关系、设置与版本号都还在。
func TestLoadV2ConfigSurvivesV3(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{
  "version": 2,
  "front": {"type": "socks5", "host": "127.0.0.1", "port": 10808, "timeoutMs": 10000},
  "settings": {
    "defaultListenHost": "127.0.0.1",
    "portStart": 10810,
    "globalNodeId": "n1",
    "systemProxy": false,
    "bottomPanelView": "traffic",
    "bottomPanelHeight": 240,
    "bottomPanelOpen": true
  },
  "nodes": [
    {"id": "n1", "name": "住宅前置", "protocol": "socks5", "listenHost": "127.0.0.1", "listenPort": 10808,
     "front": "", "server": "1.2.3.4", "port": 1080, "username": "u", "password": "p", "active": false},
    {"id": "n2", "name": "机场节点", "protocol": "vless", "listenHost": "127.0.0.1", "listenPort": 10811,
     "front": "n1", "server": "hk.example.com", "port": 443,
     "uuid": "11111111-2222-3333-4444-555555555555", "active": true}
  ]
}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载 v2 配置失败：%v", err)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("节点数 %d，期望 2 —— v2 是不是被当成 v1 迁移了？", len(cfg.Nodes))
	}
	n := cfg.Nodes[1]
	if n.Name != "机场节点" || n.Front != "n1" || n.UUID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("节点字段没保住：%+v", n)
	}
	if !n.Active {
		t.Fatal("激活状态没保住")
	}
	if cfg.Settings.GlobalNodeID != "n1" || cfg.Settings.BottomPanelView != "traffic" || cfg.Settings.BottomPanelHeight != 240 {
		t.Fatalf("设置没保住：%+v", cfg.Settings)
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("版本号 %d，期望抬到 %d", cfg.Version, CurrentVersion)
	}
	// normalize 应当把订阅 UA 填成内置默认值，界面才显示得出"现在实际用哪个"
	if cfg.Settings.SubscriptionUserAgent != DefaultSubscriptionUA {
		t.Fatalf("订阅 UA 默认值没填上：%q", cfg.Settings.SubscriptionUserAgent)
	}
}

// TestLoadV1ConfigStillMigrates v1 那条老路不能被改坏（门槛从 CurrentVersion 收紧到 2 之后）。
func TestLoadV1ConfigStillMigrates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{
  "version": 1,
  "front": {"type": "socks5", "host": "127.0.0.1", "port": 10808, "timeoutMs": 10000},
  "upstreams": [
    {"id": "u1", "name": "住宅", "type": "socks5", "enabled": true, "listenHost": "127.0.0.1", "listenPort": 10810,
     "serverHost": "1.2.3.4", "serverPort": 1080, "username": "u", "password": "p"},
    {"id": "u2", "name": "直连口", "type": "socks5", "enabled": false, "listenHost": "127.0.0.1", "listenPort": 10811,
     "serverHost": "5.6.7.8", "serverPort": 1080, "frontOverride": {"type": "direct"}}
  ],
  "defaultListenHost": "127.0.0.1",
  "startOnLaunch": true
}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载 v1 配置失败：%v", err)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("迁移后节点数 %d，期望 2", len(cfg.Nodes))
	}
	// v1 里"没有 frontOverride"= 走全局前置（迁移必须保持这个语义，否则用户的住宅代理会突然直连）
	if cfg.Nodes[0].Front != FrontGlobal {
		t.Fatalf("无 frontOverride 的链路应迁成全局前置，实际 %q", cfg.Nodes[0].Front)
	}
	if cfg.Nodes[1].Front != FrontDirect {
		t.Fatalf("frontOverride=direct 的链路应迁成直连，实际 %q", cfg.Nodes[1].Front)
	}
	if !cfg.Nodes[0].Active || cfg.Nodes[1].Active {
		t.Fatal("v1 的 enabled 没有映射成 Active")
	}
	if !cfg.Settings.AutoActivateOnLaunch {
		t.Fatal("v1 的 startOnLaunch 没有映射成 AutoActivateOnLaunch")
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("版本号 %d，期望 %d", cfg.Version, CurrentVersion)
	}
}

// TestSubscriptionHelpers 订阅自身的取值辅助。
func TestSubscriptionHelpers(t *testing.T) {
	// UA 优先级：订阅自己的 → 设置的 → 内置默认值
	s := Subscription{}
	if got := s.EffectiveUA(""); got != DefaultSubscriptionUA {
		t.Fatalf("空 UA 应回落到内置默认值，实际 %q", got)
	}
	if got := s.EffectiveUA("clash-verge/v1.6.0"); got != "clash-verge/v1.6.0" {
		t.Fatalf("应回落到设置里的 UA，实际 %q", got)
	}
	s.UserAgent = "v2rayN/6.8"
	if got := s.EffectiveUA("clash-verge/v1.6.0"); got != "v2rayN/6.8" {
		t.Fatalf("订阅自己的 UA 优先，实际 %q", got)
	}

	cfg := Default()
	cfg.Subscriptions = []Subscription{{ID: "s1", Name: "机场A"}, {ID: "s2", Name: "机场B"}}
	if got := cfg.FindSubscription("s2"); got == nil || got.Name != "机场B" {
		t.Fatalf("FindSubscription 没找到：%+v", got)
	}
	if got := cfg.FindSubscription("nope"); got != nil {
		t.Fatal("找不到时应返回 nil")
	}
}

// TestNodeSubscriptionFlags 订阅节点与"未分配端口"的判定。
func TestNodeSubscriptionFlags(t *testing.T) {
	pending := Node{Protocol: ProtocolVLESS, SubID: "s1"}
	if !pending.IsSubNode() || !pending.PortPending() {
		t.Fatal("订阅节点且无端口时，两个判定都应为真")
	}
	manual := Node{Protocol: ProtocolSOCKS5, ListenPort: 10810}
	if manual.IsSubNode() {
		t.Fatal("手动节点不该被判成订阅节点")
	}
	if manual.PortPending() {
		t.Fatal("有端口就不算待分配")
	}
}

// TestValidateAllowsPendingSubscriptionNodes 校验必须容忍"未分配端口的订阅节点"。
//
// 【为什么这条重要】真实机场一次导入可能上千个节点，如果校验把它们全判成错误，
// 界面会被上千条报错淹没、用户以为整份配置坏了。但"未分配却又激活"必须报错 ——
// 那种节点一定起不来（startNode 会拒绝）。
func TestValidateAllowsPendingSubscriptionNodes(t *testing.T) {
	cfg := Default()
	cfg.Nodes = []Node{{
		ID: "n1", Name: "机场节点", Protocol: ProtocolVLESS, SubID: "s1",
		ListenHost: "127.0.0.1", ListenPort: 0, Active: false,
		Server: "a.example.com", Port: 443, UUID: "11111111-2222-3333-4444-555555555555",
	}}
	if issues := cfg.Validate(); len(issues) != 0 {
		t.Fatalf("未分配端口的订阅节点不该报错：%+v", issues)
	}

	// 未分配却激活 → 明确报错
	cfg.Nodes[0].Active = true
	issues := cfg.Validate()
	if len(issues) == 0 {
		t.Fatal("未分配端口却处于激活状态时必须报错")
	}
	found := false
	for _, is := range issues {
		if is.Field == "listenPort" {
			found = true
		}
	}
	if !found {
		t.Fatalf("报错应指向 listenPort，实际：%+v", issues)
	}

	// 手动节点没有端口仍然要报错（别把这条例外开成万能口子）
	cfg2 := Default()
	cfg2.Nodes = []Node{{ID: "m1", Name: "手动节点", Protocol: ProtocolVLESS,
		ListenHost: "127.0.0.1", Server: "b.example.com", Port: 443,
		UUID: "11111111-2222-3333-4444-555555555555"}}
	if issues := cfg2.Validate(); len(issues) == 0 {
		t.Fatal("手动节点没有本地端口必须报错")
	}
}
