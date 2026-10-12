package app

import (
	"sync"
	"testing"

	"chainproxy/internal/config"
)

// TestValidateConcurrentDoesNotWrite 是审查 #13 的回归测试。
//
// App.Validate 持的是 RLock，RLock 允许**多个读者同时进入** —— 所以它调用的
// config.Validate 里一行都不能写配置。旧实现会就地把空 ID 补成 NewID()：
// 那既是"读的时候改配置"，又会与并发进入的读者（另一个 Validate、Nodes()、
// 每秒统计）对同一片内存一读一写。
//
// Windows 上 CGO_ENABLED=0、-race 不可用，所以用两条确定性断言来判：
//  1. 缺 ID 的节点必须一路被报出来（不再被偷偷补掉）；
//  2. 跑完之后节点 ID 必须仍然是空的 —— 只要发生了一次回写，这里就必然失败。
func TestValidateConcurrentDoesNotWrite(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})

	// 注意：必须在 New 之后直接塞进内存，走 Load -> normalize() 的话 ID 会被补齐
	a.mu.Lock()
	a.cfg.Nodes = []config.Node{{
		Name: "缺 ID", Protocol: config.ProtocolSOCKS5,
		ListenPort: 10810, Server: "h", Port: 1,
	}}
	a.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if issues := a.Validate(); len(issues) == 0 {
					t.Errorf("缺内部 ID 的节点应当被报出来，实际没有任何问题")
					return
				}
			}
		}()
	}
	wg.Wait()

	a.mu.RLock()
	id := a.cfg.Nodes[0].ID
	a.mu.RUnlock()
	if id != "" {
		t.Fatalf("校验不该给节点补 ID（那是写配置，会在 RLock 下与并发读者竞争）；实际 ID=%q", id)
	}
}
