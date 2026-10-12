package app

import (
	"sync"
	"testing"
	"time"

	"chainproxy/internal/config"
)

// 本文件是审查 #28 的回归测试：端口占用探测（net.Listen）不许在全局锁里做。
//
// 【缺陷】AddNode / CloneNode / startNode 都在持着 a.mu（全局写锁）的时候探测
// 端口，而探测就是一次真正的 net.Listen —— Next() 那条路最坏要探测 512 次。
// a.mu 是界面上每一次读取（节点列表、统计、端口自检）的必经之路，
// 持着它做几十到几百次系统调用，整个窗口就跟着卡住。
//
// 【怎么做到确定性】探测的耗时在不同机器上差很多，量时间必然偶发。
// 这里把探测换成"一进锁就通知测试、然后一直等放行"的可控实现
// （App 侧的 portProbe 钩子，与 netlog.go 里的 timeNow 同一个做法），
// 于是"探测正在进行"变成一个确定的事实；此刻拿 TryLock 去抢全局锁：
//
//	老代码：锁被带进了探测，TryLock 一次都拿不到
//	修好后：锁是空的，TryLock 立刻成功
//
// 与 TestStartNodeStopsOldServerOutsideLock（审查 #4）同一套路数。

// blockingProbe 返回一个"被调用就通知测试、然后一直等放行"的探测实现。
//
// 它一律返回 false（=端口空闲）：测试只关心探测发生在哪把锁的外面，
// 不关心端口本身忙不忙。
func blockingProbe(entered chan<- struct{}, release <-chan struct{}) func(string, int) bool {
	return func(string, int) bool {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return false
	}
}

// lockHeldFor 在 wait 这段时间里反复抢全局锁：一直抢不到才认为"锁被人占着"。
//
// 之所以要重试而不是 TryLock 一次：App 里的每秒统计等后台协程也会短暂拿锁，
// 单次 TryLock 失败说明不了问题。
func lockHeldFor(a *App, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if a.mu.TryLock() {
			a.mu.Unlock()
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return true
}

// TestAddNodePortProbeRunsOutsideGlobalLock 锁死"自动分配端口的探测在锁外"。
func TestAddNodePortProbeRunsOutsideGlobalLock(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseNow()

	portProbe = blockingProbe(entered, release)
	defer func() { portProbe = nil }()

	a := newTestApp(t, nil, config.Front{})

	done := make(chan error, 1)
	go func() {
		_, err := a.AddNode(config.Node{
			Name: "新增", Protocol: config.ProtocolSOCKS5, Server: "h", Port: 1,
		})
		done <- err
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("端口探测一直没开始：AddNode 没走到分配端口那一步")
	}

	// 此刻探测正卡在可控实现里。锁必须是空的 —— 界面上的读取全靠它。
	held := lockHeldFor(a, 2*time.Second)
	releaseNow()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("新增节点失败：%v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("AddNode 没有返回")
	}

	if held {
		t.Fatal("端口探测是持着全局写锁执行的：探测期间界面上的读取全被挡住（缺陷 #28）")
	}
	nodes := a.NodeConfigs()
	if len(nodes) != 1 || nodes[0].ListenPort == 0 {
		t.Fatalf("新增的节点没有拿到合法端口：%+v", nodes)
	}
}

// TestStartNodePortProbeRunsOutsideGlobalLock 锁死"启停路径的端口探测在锁外"。
//
// startNode 只探测一次，但它在激活/重启这条最热的路径上：
// 每次点启动都要先把界面卡住一次系统调用的时间。
func TestStartNodePortProbeRunsOutsideGlobalLock(t *testing.T) {
	base := freeBase(t, 15000, 2)

	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseNow()

	portProbe = blockingProbe(entered, release)
	defer func() { portProbe = nil }()

	a := newTestApp(t, []config.Node{{
		ID: "n1", Name: "节点", Protocol: config.ProtocolSOCKS5,
		ListenHost: "127.0.0.1", ListenPort: base, Server: "h", Port: 1,
		Front: config.FrontDirect,
	}}, config.Front{})

	done := make(chan error, 1)
	go func() { done <- a.Activate([]string{"n1"}) }()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("端口探测一直没开始：startNode 没走到端口检查那一步")
	}

	held := lockHeldFor(a, 2*time.Second)
	releaseNow()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("激活节点失败：%v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Activate 没有返回")
	}

	if held {
		t.Fatal("端口探测是持着全局写锁执行的：激活期间界面上的读取全被挡住（缺陷 #28）")
	}
	if s := a.serverOf("n1"); s == nil || !s.Running() {
		t.Fatal("节点没起来，测试前提不成立")
	}
}

// TestAllocPortRechecksCandidateBeforeCommit 锁死"放锁探测之后、落盘之前"的那次复查。
//
// 探测挪到锁外意味着候选端口不再受锁保护：放锁的窗口里，另一个新增/克隆完全
// 可能挑走同一个端口。所以 allocPort 必须在锁内用**当前**占用表复查一次。
//
// 这里就制造这个交错：探测一被调用（此刻锁必须是空的），另一个 goroutine 抢先
// 把候选端口登记给一个节点。断言的是"AddNode 换了一个端口"，而不是耗时。
//
// 抢占者用 TryLock + 截止时间而不是 Lock()：旧代码里锁被探测占着，
// 直接 Lock() 会把测试挂死而不是给出清晰的失败 —— 拿不到锁就放弃，
// 于是旧代码的失败表现为"同一个端口被两个节点占用"。
func TestAllocPortRechecksCandidateBeforeCommit(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})
	base := freeBase(t, 16000, 4)
	s := a.Settings()
	s.PortStart = base
	if err := a.SetSettings(s); err != nil {
		t.Fatal(err)
	}

	probed := make(chan int, 1)
	sniped := make(chan struct{})
	sniperDone := make(chan struct{})
	defer func() { <-sniperDone }()

	portProbe = func(_ string, port int) bool {
		if port == base {
			probed <- port
			select {
			case <-sniped:
			case <-time.After(5 * time.Second):
			}
		}
		return false // 端口"忙不忙"由抢占者说了算，这里一律报空闲
	}
	defer func() { portProbe = nil }()

	go func() {
		defer close(sniperDone)
		select {
		case p := <-probed:
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if a.mu.TryLock() {
					a.cfg.Nodes = append(a.cfg.Nodes, config.Node{
						ID: "sniper", Name: "抢占者", Protocol: config.ProtocolSOCKS5,
						ListenHost: "127.0.0.1", ListenPort: p, Server: "h", Port: 1,
						Front: config.FrontDirect,
					})
					a.mu.Unlock()
					break
				}
				time.Sleep(time.Millisecond)
			}
		case <-time.After(10 * time.Second):
		}
		close(sniped)
	}()

	got, err := a.AddNode(config.Node{
		Name: "新增", Protocol: config.ProtocolSOCKS5, Server: "h", Port: 1,
	})
	if err != nil {
		t.Fatalf("新增节点失败：%v", err)
	}
	if got.ListenPort == base {
		t.Fatalf("候选端口 %d 在探测期间已被抢占，AddNode 仍然把它写进了配置", base)
	}

	seen := map[int]string{}
	for _, n := range a.NodeConfigs() {
		if prev, dup := seen[n.ListenPort]; dup {
			t.Fatalf("端口 %d 被「%s」和「%s」同时占用", n.ListenPort, prev, n.Name)
		}
		seen[n.ListenPort] = n.Name
	}
	if _, ok := seen[base]; !ok {
		t.Fatalf("抢占者应当占着 %d，实际 %v", base, seen)
	}
}
