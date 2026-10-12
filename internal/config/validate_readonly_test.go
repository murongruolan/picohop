package config

import "testing"

// TestValidateDoesNotMutateConfig 锁死"校验是只读的"这条契约（审查 #13）。
//
// 【为什么值得一条专门的测试】App.Validate 持的是 RLock，而 RLock 允许**多个读者
// 同时进入**：只要 Validate 里存在任何一处回写（旧实现会补内部 ID、把空协议补成
// socks5、把空监听地址补成全局默认值），"校验"就会和并发的读者（节点列表、每秒统计、
// Save 时的 Encode）对同一片内存一读一写，是真正的 data race。
//
// Windows 上 CGO_ENABLED=0、-race 不可用（见 docs），所以这里改用**确定性**断言：
// 调用前后把配置序列化出来逐字节比对，只要有一处回写就必然不等。
func TestValidateDoesNotMutateConfig(t *testing.T) {
	cfg := Default()
	// 故意留空 ID / 空协议 / 空监听地址 —— 正是旧实现会被就地补掉的三处
	cfg.Nodes = []Node{
		{Name: "缺 ID 与协议", ListenPort: 10810, Server: "h", Port: 1},
		{ID: "b", Name: "B", Protocol: ProtocolVLESS, ListenPort: 10811,
			Server: "h", Port: 1, Front: "nope"},
	}

	before, err := cfg.Encode()
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}

	issues := cfg.Validate()

	// 缺内部 ID 必须被**报出来**，而不是被偷偷补上（偷偷补就是写配置）
	foundID := false
	for _, i := range issues {
		if i.Field == "id" {
			foundID = true
		}
	}
	if !foundID {
		t.Errorf("缺失的内部 ID 应当被报出来，实际 issues=%v", issues)
	}

	after, err := cfg.Encode()
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("校验（读路径）不能修改配置：\n调用前：%s\n调用后：%s", before, after)
	}
}

// TestNormalizeNodeFillsDefaults 锁死写入路径的归一化规则。
//
// 这三件事原本写在 Validate 里（读路径顺手写配置），现已搬到 NormalizeNode，
// 由 config.Load 的 normalize() 与 App.AddNode / App.UpdateNode 显式调用。
// 搬走之后必须仍然生效，否则手工编辑过的配置与新建节点就再也补不齐默认值了。
func TestNormalizeNodeFillsDefaults(t *testing.T) {
	cfg := Default()
	cfg.Settings.DefaultListenHost = "127.0.0.9"

	n := Node{Name: "n", Server: "h", Port: 1}
	cfg.NormalizeNode(&n)
	if n.Protocol != ProtocolSOCKS5 {
		t.Errorf("空协议应补成 socks5，实际 %q", n.Protocol)
	}
	if n.ListenHost != "127.0.0.9" {
		t.Errorf("空监听地址应取全局默认值，实际 %q", n.ListenHost)
	}
	if n.ID == "" {
		t.Error("缺失的内部 ID 应被补上")
	}

	// 已经设好的值一个都不能被覆盖
	m := Node{ID: "keep", Protocol: ProtocolVLESS, ListenHost: "10.0.0.1"}
	cfg.NormalizeNode(&m)
	if m.ID != "keep" || m.Protocol != ProtocolVLESS || m.ListenHost != "10.0.0.1" {
		t.Errorf("已有值不应被改写：%+v", m)
	}

	// 传 nil 不能 panic（调用方可能拿到一个不存在的节点）
	cfg.NormalizeNode(nil)
}
