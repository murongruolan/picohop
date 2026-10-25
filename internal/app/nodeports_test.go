package app

import (
	"testing"

	"github.com/murongruolan/picohop/internal/config"
)

// TestUnassignNodePorts 「端口置于未分配」：先停服务、清端口，且不碰没选中的节点。
func TestUnassignNodePorts(t *testing.T) {
	a := newTestApp(t, []config.Node{
		{ID: "n1", Name: "A", Protocol: config.ProtocolSOCKS5, ListenHost: "127.0.0.1", Server: "127.0.0.1", Port: 9},
		{ID: "n2", Name: "B", Protocol: config.ProtocolSOCKS5, ListenHost: "127.0.0.1", Server: "127.0.0.1", Port: 9},
	}, config.Front{})

	// 显式激活会给它们各分配一个端口并启动
	if err := a.Activate([]string{"n1", "n2"}); err != nil {
		t.Fatalf("激活失败：%v", err)
	}
	if n, _ := a.GetNode("n1"); n.ListenPort == 0 || !n.Active {
		t.Fatalf("激活后应当已分配端口：%+v", n)
	}

	count, err := a.UnassignNodePorts([]string{"n1"})
	if err != nil {
		t.Fatalf("置未分配失败：%v", err)
	}
	if count != 1 {
		t.Fatalf("应当改动 1 个节点，实际 %d", count)
	}
	n1, _ := a.GetNode("n1")
	if n1.ListenPort != 0 || n1.Active {
		t.Fatalf("应当回到未分配且不再激活：%+v", n1)
	}
	// 没选中的节点不该被动
	if n2, _ := a.GetNode("n2"); n2.ListenPort == 0 || !n2.Active {
		t.Fatalf("没选中的节点不该受影响：%+v", n2)
	}

	// 重复操作不计数（本来就是未分配）
	if c, _ := a.UnassignNodePorts([]string{"n1"}); c != 0 {
		t.Fatalf("重复置未分配不该计数，实际 %d", c)
	}

	// 【关键】未分配的**手动**节点必须能通过配置校验（否则一键置未分配会让配置立刻报错）
	if issues := a.Validate(); len(issues) != 0 {
		t.Fatalf("存在未分配的手动节点时校验不该报错：%+v", issues)
	}

	// 但"未分配却激活"仍然是错误
	a.mu.Lock()
	a.cfg.Nodes[0].Active = true
	a.mu.Unlock()
	bad := false
	for _, is := range a.Validate() {
		if is.Field == "listenPort" {
			bad = true
		}
	}
	if !bad {
		t.Fatal("未分配却激活必须报错")
	}
	a.mu.Lock()
	a.cfg.Nodes[0].Active = false
	a.mu.Unlock()

	_ = a.Deactivate([]string{"n2"})
}
