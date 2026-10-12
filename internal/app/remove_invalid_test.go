package app

import (
	"testing"
	"time"

	"github.com/murongruolan/picohop/internal/config"
)

// 审查 #8 的回归测试。
//
// 缺陷原貌：RemoveInvalidNodes 用 `n.LatencyMs == 0` 当"测试无效"的判据，
// 而用「真连接延迟」测出来的结果写进 RealMs（LatencyMs 恒为 0），
// 于是「移除测试无效项」会把**所有**节点都删掉 —— 界面上还零确认。
//
// 这里钉死三件事：
//  1. 判据必须是"测过且明确连不上"：没测过的、真连接延迟测通过的、0ms 通过的都不算；
//  2. 过期的结论（> TestResultTTL）与"改了服务器地址之后"的旧结论都不算；
//  3. 删除只删预览名单里、且此刻仍然满足判据的节点。

// TestRemoveInvalidIgnoresUntestedAndRealOK 是本缺陷的核心：老判据会把
// "没测过"和"用真连接延迟测通了"的节点全部误判为无效项。
func TestRemoveInvalidIgnoresUntestedAndRealOK(t *testing.T) {
	// 三个节点都带着 LatencyMs == 0 —— 这正是老判据眼里的"无效项"。
	// untested 从没测过；realok 用真连接延迟测通了（结果在 RealMs）；
	// fast 用 TCP 测通了但耗时不足 1ms（成功也是 0）。
	a := newTestApp(t, []config.Node{
		{ID: "untested", Name: "没测过", Protocol: config.ProtocolSOCKS5, Server: "a.example.com", Port: 1},
		{ID: "realok", Name: "真连接通", Protocol: config.ProtocolSOCKS5, Server: "b.example.com", Port: 1},
		{ID: "fast", Name: "极快", Protocol: config.ProtocolSOCKS5, Server: "c.example.com", Port: 1},
	}, config.Front{})

	// 真连接延迟测试成功：LatencyMs 仍然是 0，成功与否只能看 updateTest 的显式标记
	a.updateTest("realok", TestReal, false, measured{ok: true})
	// TCP 测试成功但耗时 0ms（本机/局域网节点很常见）
	a.updateTest("fast", TestTCP, false, measured{ok: true})

	p := a.PreviewInvalidNodes()
	if len(p.IDs) != 0 {
		t.Fatalf("没测过 / 测通了的节点都不该算无效项，实际选中 %v", p.Names)
	}
	if p.Untested != 1 {
		t.Errorf("应当有 1 个「从没测过」，实际 %d", p.Untested)
	}
	if p.Stale != 0 {
		t.Errorf("不应当有「结论过期」，实际 %d", p.Stale)
	}
	// 判据本身也不认这些节点
	for _, n := range a.NodeConfigs() {
		if n.KnownUnreachable(time.Now()) {
			t.Errorf("「%s」不该被判定为无效项：%+v", n.Name, n)
		}
	}

	// 一次全选删除请求也不能删掉它们
	n, err := a.RemoveInvalidNodes([]string{"untested", "realok", "fast"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || a.CountNodes() != 3 {
		t.Fatalf("不该删掉任何节点，实际删了 %d 个、剩 %d 个", n, a.CountNodes())
	}
}

// TestRemoveInvalidDeletesOnlyFailed 确认真正的"连不上"还是会被删掉，
// 并且只删它一个。
func TestRemoveInvalidDeletesOnlyFailed(t *testing.T) {
	a := newTestApp(t, []config.Node{
		{ID: "dead", Name: "连不上", Protocol: config.ProtocolSOCKS5, Server: "dead.example.com", Port: 1},
		{ID: "alive", Name: "能连上", Protocol: config.ProtocolSOCKS5, Server: "ok.example.com", Port: 1},
		{ID: "never", Name: "没测过", Protocol: config.ProtocolSOCKS5, Server: "n.example.com", Port: 1},
	}, config.Front{})

	// 先失败、后成功：谁最后测谁覆盖，alive 不能被删
	a.updateTest("alive", TestTCP, true, measured{})
	a.updateTest("alive", TestTCP, false, measured{ok: true, ms: 88})
	// TCP 握手失败 = 明确连不上
	a.updateTest("dead", TestTCP, true, measured{})

	p := a.PreviewInvalidNodes()
	if len(p.IDs) != 1 || p.IDs[0] != "dead" {
		t.Fatalf("只有「连不上」该被选中，实际 %v", p.Names)
	}
	if p.Untested != 1 {
		t.Errorf("应当有 1 个从未测过，实际 %d", p.Untested)
	}

	n, err := a.RemoveInvalidNodes(p.IDs)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || a.CountNodes() != 2 {
		t.Fatalf("应当只删 1 个，实际删了 %d 个、剩 %d 个", n, a.CountNodes())
	}
	if _, ok := a.GetNode("dead"); ok {
		t.Error("「连不上」应当已被删除")
	}
	if _, ok := a.GetNode("alive"); !ok {
		t.Error("「能连上」不该被删除")
	}
}

// TestRemoveInvalidIgnoresStaleAndChangedAddr 覆盖两条"证据不再算数"的路径：
// 结论过期、以及结论对应的是另一个服务器地址。
//
// 为什么这两条必须算"不算无效"：删除不可撤销，而失败结论只描述"测的那一刻的
// 那个地址"。过期/改过地址之后重测一次的成本远低于误删一个可用节点。
func TestRemoveInvalidIgnoresStaleAndChangedAddr(t *testing.T) {
	a := newTestApp(t, []config.Node{
		{ID: "stale", Name: "结论过期", Protocol: config.ProtocolSOCKS5, Server: "s.example.com", Port: 1},
		{ID: "moved", Name: "改过地址", Protocol: config.ProtocolSOCKS5, Server: "new.example.com", Port: 1},
		{ID: "fresh", Name: "刚测失败", Protocol: config.ProtocolSOCKS5, Server: "f.example.com", Port: 1},
	}, config.Front{})

	a.updateTest("stale", TestTCP, true, measured{})
	a.updateTest("moved", TestTCP, true, measured{})
	a.updateTest("fresh", TestTCP, true, measured{})

	// 把两条结论弄成"不再算数"：一个时间太久，一个地址对不上
	a.mu.Lock()
	for i := range a.cfg.Nodes {
		switch a.cfg.Nodes[i].ID {
		case "stale":
			a.cfg.Nodes[i].TestedAt = time.Now().Add(-config.TestResultTTL - time.Minute)
		case "moved":
			// 用户把服务器地址改成了另一个（旧结论说的是 old.example.com）
			a.cfg.Nodes[i].TestedAddr = "old.example.com:1"
		}
	}
	a.mu.Unlock()

	p := a.PreviewInvalidNodes()
	if len(p.IDs) != 1 || p.IDs[0] != "fresh" {
		t.Fatalf("只有「刚测失败」该被选中，实际 %v", p.Names)
	}
	// stale 与 moved 都归入 Stale：结论已经不算数（过期 / 地址对不上），
	// 界面据此解释"为什么这两个没删"。
	if p.Stale != 2 {
		t.Errorf("应当有 2 个结论已经不算数，实际 %d", p.Stale)
	}

	// 界面把三个都送来（比如确认框开着的时候配置被改过）也只会删掉有效的那一个
	n, err := a.RemoveInvalidNodes([]string{"stale", "moved", "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || a.CountNodes() != 2 {
		t.Fatalf("应当只删 1 个，实际删了 %d 个、剩 %d 个", n, a.CountNodes())
	}
	if _, ok := a.GetNode("fresh"); ok {
		t.Error("「刚测失败」应当已被删除")
	}
}

// TestRemoveInvalidRechecksBeforeDelete 锁死"确认框开着的时候测试跑通了"这条竞态：
// 名单是旧的，但删除前必须按当前配置再核一遍。
func TestRemoveInvalidRechecksBeforeDelete(t *testing.T) {
	a := newTestApp(t, []config.Node{
		{ID: "n1", Name: "先失败后跑通", Protocol: config.ProtocolSOCKS5, Server: "x.example.com", Port: 1},
	}, config.Front{})

	a.updateTest("n1", TestTCP, true, measured{})
	preview := a.PreviewInvalidNodes()
	if len(preview.IDs) != 1 {
		t.Fatalf("前提不成立：应当先判定为无效项，实际 %v", preview.Names)
	}

	// 用户还没点确认，后台测试刚好跑通了
	a.updateTest("n1", TestTCP, false, measured{ok: true, ms: 120})

	// 界面仍然把预览时的那份 ids 送来 —— 后端必须自己再核一遍
	n, err := a.RemoveInvalidNodes(preview.IDs)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || a.CountNodes() != 1 {
		t.Fatalf("跑通之后不该被删，实际删了 %d 个、剩 %d 个", n, a.CountNodes())
	}
}

// TestRemoveInvalidEmptyIDs 确认空名单是"什么都不删"，而不是"删全部"。
func TestRemoveInvalidEmptyIDs(t *testing.T) {
	a := newTestApp(t, []config.Node{
		{ID: "dead", Name: "连不上", Protocol: config.ProtocolSOCKS5, Server: "d.example.com", Port: 1},
	}, config.Front{})
	a.updateTest("dead", TestTCP, true, measured{})

	n, err := a.RemoveInvalidNodes(nil)
	if err != nil || n != 0 || a.CountNodes() != 1 {
		t.Fatalf("空名单必须什么都不删：n=%d err=%v 剩 %d 个", n, err, a.CountNodes())
	}
}

// TestKnownUnreachableWindows 把判据的边界写死（含 TTL 边界与地址比较规则）。
func TestKnownUnreachableWindows(t *testing.T) {
	now := time.Now()
	base := config.Node{Server: "H.example.com", Port: 443}
	base.TestedAddr = base.TestKey()

	sample := func(failed bool, at time.Time, addr string) config.Node {
		n := base
		n.TestFailed = failed
		n.TestedAt = at
		n.TestedAddr = addr
		return n
	}

	cases := []struct {
		name string
		node config.Node
		want bool
	}{
		{"没测过", config.Node{Server: "h", Port: 1}, false},
		{"测通了", sample(false, now, base.TestKey()), false},
		{"刚测失败", sample(true, now, base.TestKey()), true},
		{"结论过期", sample(true, now.Add(-config.TestResultTTL-time.Second), base.TestKey()), false},
		{"地址对不上", sample(true, now, "other.example.com:443"), false},
		{"没有地址指纹的老配置", sample(true, now, ""), false},
		// 大小写不同不算改地址（TestKey 会把 server 归一化）
		{"只变大小写", sample(true, now, config.Node{Server: "h.example.com", Port: 443}.TestKey()), true},
	}
	for _, c := range cases {
		if got := c.node.KnownUnreachable(now); got != c.want {
			t.Errorf("%s：KnownUnreachable = %v，期望 %v", c.name, got, c.want)
		}
	}
}
