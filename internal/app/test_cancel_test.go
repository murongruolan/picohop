package app

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"chainproxy/internal/config"
)

// 审查 #14 的回归测试：取消测试后不得再派发剩余节点、也不得再写回结果。
//
// 缺陷原貌（internal/app/test.go 的派发循环）：
//
//	select {
//	case <-ctx.Done():
//	    break   // 只跳出 select，for 继续把剩下的节点全部派发出去
//	default:
//	}
//
// ctx 已经取消，这些测试必然失败，却仍会被 updateTest 一条条写成
// "连不上 + 延迟清零"：用户点了"停止"，结果反而把节点全判成了无效项
// （「移除测试无效项」随后就会删掉它们）。

// TestCancelStopsDispatchingRemaining 钉死"取消后一个节点都不再派发"。
//
// 【怎么做到不靠网络、也不靠运气地复现】用一个事件订阅者在测试任务协程的
// **第一条日志**（"开始测试…"，它就在派发循环之前）上设卡：等这条日志把
// 测试任务协程卡住 → 此刻它还没派发任何节点 → 从测试协程里点"停止"
// （取消一定早于第一个节点的测试开始）→ 再放行。
//
// 这样"取消"与"节点开始测试"的先后是确定的，不受 Windows 上 connect 失败
// 快慢的影响（真实环境里锁不住这个顺序，正是这个缺陷难复现的原因）：
//   - 修好之后：循环第一次检查 ctx 就跳出整轮，一个节点都不派发，配置一个字都不改；
//   - 没修之前：30 个节点全被派发，全部因为 ctx 取消而失败，全部写成 TestFailed=true。
func TestCancelStopsDispatchingRemaining(t *testing.T) {
	const n = 30
	nodes := make([]config.Node, 0, n)
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		node := config.Node{
			ID: fmt.Sprintf("n%d", i), Name: fmt.Sprintf("节点%d", i),
			Protocol: config.ProtocolSOCKS5,
			// 地址本身无所谓：取消后连拨号都不会发生
			Server: "127.0.0.1", Port: 1,
		}
		nodes = append(nodes, node)
		ids = append(ids, node.ID)
	}
	a := newTestApp(t, nodes, config.Front{})

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	// 订阅回调正常要求"快速返回"；这里**故意**卡住它，把"测试任务协程已经起跑、
	// 但还没进入派发循环"这个瞬间变成一个可控的同步点（见上面的注释）。
	unsub := a.Subscribe(func(e Event) {
		if e.Kind != EventLog || !strings.Contains(e.Text, "开始测试") {
			return
		}
		once.Do(func() {
			close(started)
			<-release
		})
	})
	defer unsub()

	if err := a.TestNodes(ids, TestTCP); err != nil {
		t.Fatalf("启动测试失败：%v", err)
	}

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("测试任务没有在 5 秒内开始（设卡的日志没等到）")
	}

	// 用户点"停止"：此刻一个节点的测试都还没开始
	a.CancelTests()
	close(release)

	deadline := time.Now().Add(10 * time.Second)
	for a.Testing() {
		if time.Now().After(deadline) {
			t.Fatal("取消之后测试任务迟迟没有结束")
		}
		time.Sleep(5 * time.Millisecond)
	}

	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, node := range a.cfg.Nodes {
		if node.TestFailed || !node.TestedAt.IsZero() {
			t.Fatalf("取消后不该再写回任何结论：「%s」却 testFailed=%v testedAt=%v",
				node.Name, node.TestFailed, node.TestedAt)
		}
	}
}

// TestCanceledTestWritesNothing 钉死"取消导致的失败不是节点的结论"。
//
// 这是同一缺陷的另一半：派发循环之外的、已经在跑的测试也会因为 ctx 取消而
// 立刻失败（TCP 握手拿到的是 context canceled）。那种失败写回去有两个坏结果：
// 节点被标成"连不上"（随后会被「移除测试无效项」删掉），以及已经测出来的
// 延迟/速度被 0 覆盖。这里直接用一个已取消的 ctx 跑一次 TCP 测试。
func TestCanceledTestWritesNothing(t *testing.T) {
	a := newTestApp(t, []config.Node{{
		ID: "n1", Name: "n1", Protocol: config.ProtocolSOCKS5,
		Server: "127.0.0.1", Port: 1,
	}}, config.Front{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 模拟用户点了"停止"

	node, ok := a.GetNode("n1")
	if !ok {
		t.Fatal("测试节点不存在")
	}
	a.runOneTest(ctx, node, TestTCP)

	got := a.NodeConfigs()[0]
	if got.TestFailed || !got.TestedAt.IsZero() || got.LatencyMs != 0 {
		t.Fatalf("取消后的失败不能写回：testFailed=%v testedAt=%v latencyMs=%d",
			got.TestFailed, got.TestedAt, got.LatencyMs)
	}
}

// TestUncanceledTestStillWrites 反向钉死：没取消时结果必须照常写回。
//
// 为什么要有这一条：抑制写回的判据一旦写歪（写成"失败就不写"、或者条件反了），
// 节点就永远测不出结论 —— 那是另一种坏法。这里用本机回环端口做一次真 TCP 握手。
func TestUncanceledTestStillWrites(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("起本地监听失败：%v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	a := newTestApp(t, []config.Node{{
		ID: "n1", Name: "本机", Protocol: config.ProtocolSOCKS5,
		Server: "127.0.0.1", Port: port,
	}}, config.Front{})

	node, ok := a.GetNode("n1")
	if !ok {
		t.Fatal("测试节点不存在")
	}
	a.runOneTest(context.Background(), node, TestTCP)

	got := a.NodeConfigs()[0]
	if got.TestFailed || got.TestedAt.IsZero() {
		t.Fatalf("没取消就必须写回成功结论：testFailed=%v testedAt=%v",
			got.TestFailed, got.TestedAt)
	}
}
