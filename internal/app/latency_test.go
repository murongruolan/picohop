package app

import (
	"testing"

	"chainproxy/internal/config"
)

// TestUpdateTestLatestWins 锁死「延迟」列的取值规则：谁最后测谁覆盖。
//
// 背景（用户实报）："测试完成后并没有实时更新延迟列的数值"，日志里已经有
// 「真连接延迟 759 ms」，列表却没变化。查明是结果写进了默认不显示的
// 「真连接延迟」列。用户选择方案 C：**一个延迟列，显示最后一次测试的结果** ——
// 测真连接就显示真连接、测 TCP 就显示 TCP。
func TestUpdateTestLatestWins(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})

	a.mu.Lock()
	a.cfg.Nodes = []config.Node{{
		ID: "n1", Name: "n1", Protocol: config.ProtocolVLESS,
		Server: "1.1.1.1", Port: 443,
	}}
	a.mu.Unlock()

	a.updateTest("n1", TestReal, false, 759, 0, "")
	if got := a.cfg.Nodes[0]; got.LatencyKind != "real" || got.RealMs != 759 {
		t.Fatalf("真连接测试后应当 kind=real / realMs=759，实际 kind=%q realMs=%d", got.LatencyKind, got.RealMs)
	}
	a.updateTest("n1", TestTCP, false, 312, 0, "")
	if got := a.cfg.Nodes[0]; got.LatencyKind != "tcp" || got.LatencyMs != 312 {
		t.Fatalf("TCP 测试后应当 kind=tcp / latencyMs=312，实际 kind=%q latencyMs=%d", got.LatencyKind, got.LatencyMs)
	}
	// 视图必须把 kind 带出去，否则界面无从判断该显示哪个值
	view := a.Nodes()
	if len(view) != 1 || view[0].LatencyKind != "tcp" {
		t.Fatalf("NodeView 没带出 latencyKind：%+v", view)
	}
}
