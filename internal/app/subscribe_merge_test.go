package app

import (
	"testing"

	"github.com/murongruolan/picohop/internal/config"
)

// subNode 造一个"来自订阅"的节点（远端字段齐全，本地状态留空）。
func subNode(uid, name, server string, port int) config.Node {
	return config.Node{
		ID:       uid,
		Name:     name,
		Protocol: config.ProtocolVLESS,
		Server:   server,
		Port:     port,
		UUID:     "11111111-2222-3333-4444-555555555555",
	}
}

// TestMergeSubNodesAddsPendingNodes 新导入的订阅节点必须是"未分配端口 + 未激活"。
//
// 这是本次功能最核心的一条取舍：真实机场动辄几百上千个节点，导入即占端口/即启动
// 等于把本机打爆。
func TestMergeSubNodesAddsPendingNodes(t *testing.T) {
	cfg := config.Default()
	st := mergeSubNodes(cfg, "sub1", []config.Node{
		subNode("", "🇺🇸A", "a.example.com", 443),
		subNode("", "🇭🇰B", "b.example.com", 443),
	})
	if st.Added != 2 || st.Updated != 0 || st.Missing != 0 {
		t.Fatalf("统计错：%+v", st)
	}
	for i, n := range cfg.Nodes {
		if n.SubID != "sub1" || n.SubOrder != i {
			t.Fatalf("订阅归属/顺序错：%+v", n)
		}
		if !n.PortPending() || n.Active {
			t.Fatalf("新增的订阅节点必须未分配端口且未激活：%+v", n)
		}
		if n.NameFromSub != n.Name {
			t.Fatalf("NameFromSub 应记下机场给的名字：%+v", n)
		}
		if n.ID == "" {
			t.Fatal("ID 必须被补上")
		}
	}
}

// TestMergeSubNodesKeepsLocalStateOnUpdate 更新订阅时，远端字段换新、本机状态一律保留。
//
// 对比：v2ray 系客户端是"整组删了重建"，用户改的名字/排序/测速全丢。我们不做那种事。
func TestMergeSubNodesKeepsLocalStateOnUpdate(t *testing.T) {
	cfg := config.Default()
	cfg.Nodes = []config.Node{{
		ID: "n1", Name: "改名后的美国节点", NameFromSub: "🇺🇸A",
		Protocol: config.ProtocolVLESS, Server: "old.example.com", Port: 1111,
		UUID:  "11111111-2222-3333-4444-555555555555",
		SubID: "sub1", SubOrder: 0,
		// 本机状态：
		ListenHost: "127.0.0.1", ListenPort: 10811, Front: "residential", Active: true,
		Remark: "备注别丢", UpBytes: 123, DownBytes: 456,
		LatencyMs: 42, TestedAddr: "old.example.com:1111",
	}}

	// 机场把地址/凭据都换了（同一个远端入口的凭据不变，所以这里改地址 → 会变成"新增 + 下架" ✗）
	// 所以本用例只改"非身份字段"：传输层与 TLS，地址与凭据保持身份不变。
	in := subNode("", "🇺🇸A 新名字", "old.example.com", 1111)
	in.Transport = config.TransportOptions{Network: "ws", Path: "/new"}
	in.TLS = config.TLSOptions{Enabled: true, ServerName: "sni.example.com"}

	st := mergeSubNodes(cfg, "sub1", []config.Node{in})
	if st.Updated != 1 || st.Added != 0 || st.Missing != 0 {
		t.Fatalf("统计错：%+v", st)
	}
	n := cfg.Nodes[0]
	// 远端字段更新了
	if n.Transport.Network != "ws" || n.Transport.Path != "/new" || !n.TLS.Enabled {
		t.Fatalf("远端字段没更新：%+v", n)
	}
	// 本机状态全在
	if n.ListenPort != 10811 || n.Front != "residential" || !n.Active || n.Remark != "备注别丢" ||
		n.UpBytes != 123 || n.DownBytes != 456 || n.LatencyMs != 42 {
		t.Fatalf("本机状态被覆盖了：%+v", n)
	}
	// 用户改过名 → 保留用户的
	if n.Name != "改名后的美国节点" {
		t.Fatalf("用户改过的名字被机场覆盖了：%q", n.Name)
	}
	if n.NameFromSub != "🇺🇸A 新名字" {
		t.Fatalf("NameFromSub 应更新成机场这次给的名字：%q", n.NameFromSub)
	}
}

// TestMergeSubNodesFollowsAirportRename 用户没改过名时，机场改名要跟着改。
func TestMergeSubNodesFollowsAirportRename(t *testing.T) {
	cfg := config.Default()
	n := subNode("n1", "🇺🇸美国1号", "a.example.com", 443)
	n.SubID = "sub1"
	n.NameFromSub = "🇺🇸美国1号"
	cfg.Nodes = []config.Node{n}

	in := subNode("", "🇺🇸美国1号 倍率1.0", "a.example.com", 443)
	mergeSubNodes(cfg, "sub1", []config.Node{in})
	if cfg.Nodes[0].Name != "🇺🇸美国1号 倍率1.0" {
		t.Fatalf("没跟着机场改名：%q", cfg.Nodes[0].Name)
	}
}

// TestMergeSubNodesMarksMissingThenRestores 下架只打标记不删；回来了要清标记并计数。
func TestMergeSubNodesMarksMissingThenRestores(t *testing.T) {
	cfg := config.Default()
	a := subNode("n1", "🇺🇸A", "a.example.com", 443)
	a.SubID = "sub1"
	a.NameFromSub = a.Name
	b := subNode("n2", "🇭🇰B", "b.example.com", 443)
	b.SubID = "sub1"
	b.NameFromSub = b.Name
	cfg.Nodes = []config.Node{a, b}

	// 第二次只有 A → B 被标下架，但**不能删**
	st := mergeSubNodes(cfg, "sub1", []config.Node{subNode("", "🇺🇸A", "a.example.com", 443)})
	if st.Missing != 1 || len(cfg.Nodes) != 2 {
		t.Fatalf("下架处理错：stats=%+v 节点数=%d", st, len(cfg.Nodes))
	}
	if !cfg.Nodes[1].SubMissing {
		t.Fatal("B 应被标记为机场已下架")
	}

	// 再一次 A+B 都回来了 → B 的清标记 + Restored
	st = mergeSubNodes(cfg, "sub1", []config.Node{
		subNode("", "🇺🇸A", "a.example.com", 443),
		subNode("", "🇭🇰B", "b.example.com", 443),
	})
	if st.Restored != 1 || st.Missing != 0 {
		t.Fatalf("恢复处理错：%+v", st)
	}
	if cfg.Nodes[1].SubMissing {
		t.Fatal("B 回来了，标记应被清掉")
	}
}

// TestMergeSubNodesSkipsConflicts 与手动节点（或另一条订阅）撞同一个远端入口时跳过，
// 不"接管"它 —— 那可能是用户精心配过前置与端口的节点。
func TestMergeSubNodesSkipsConflicts(t *testing.T) {
	cfg := config.Default()
	manual := subNode("m1", "我手动加的", "dup.example.com", 443)
	manual.ListenPort = 10999
	manual.Front = "residential"
	cfg.Nodes = []config.Node{manual}

	st := mergeSubNodes(cfg, "sub1", []config.Node{subNode("", "机场也给了这个", "dup.example.com", 443)})
	if st.Conflicts != 1 || st.Added != 0 {
		t.Fatalf("冲突处理错：%+v", st)
	}
	if len(cfg.Nodes) != 1 || cfg.Nodes[0].SubID != "" || cfg.Nodes[0].Front != "residential" {
		t.Fatalf("手动节点被动过了：%+v", cfg.Nodes[0])
	}
}

// TestMergeSubNodesIsIdempotent 同一份订阅连合并两次，第二次不该再"新增"。
func TestMergeSubNodesIsIdempotent(t *testing.T) {
	cfg := config.Default()
	in := []config.Node{subNode("", "🇺🇸A", "a.example.com", 443), subNode("", "🇭🇰B", "b.example.com", 443)}
	mergeSubNodes(cfg, "sub1", in)
	st := mergeSubNodes(cfg, "sub1", in)
	if st.Added != 0 || st.Updated != 2 || len(cfg.Nodes) != 2 {
		t.Fatalf("第二次合并不该新增：%+v（节点数 %d）", st, len(cfg.Nodes))
	}
}
