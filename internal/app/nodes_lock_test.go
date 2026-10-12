package app

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"chainproxy/internal/config"
	"chainproxy/internal/engine"
	"chainproxy/internal/proxy"
)

// blockingStopUpstream 是"Stop 会一直卡住直到测试放行"的假出口。
//
// 【为什么需要它】缺陷 #4 是 startNode 持着全局写锁去调阻塞式 Stop()。
// 本地 SOCKS5/HTTP 出口的 Stop 是空实现，真实的 sing-box 出口 Stop 又快到
// 没法稳定观测，所以这里把"Stop 很慢"这件事做成可控的：Stop 一进入就通知
// 测试，然后一直等。测试就能在"Stop 正在执行"的这一刻去 TryLock 全局锁，
// 从而**确定性地**区分两种写法：
//
//	老代码：锁被带进 Stop，TryLock 一次都拿不到（界面读取全卡住）
//	修好后：Stop 在锁外，TryLock 立刻成功
type blockingStopUpstream struct {
	entered chan struct{}
	release chan struct{}
}

func (u *blockingStopUpstream) Start(context.Context, proxy.Front) error { return nil }

func (u *blockingStopUpstream) Stop() error {
	select {
	case u.entered <- struct{}{}:
	default:
	}
	<-u.release
	return nil
}

func (u *blockingStopUpstream) Dial(context.Context, proxy.Front, string) (net.Conn, error) {
	return nil, errors.New("假出口不转发流量")
}

func (u *blockingStopUpstream) DialUDP(context.Context, proxy.Front) (proxy.UDPChannel, error) {
	return nil, errors.New("假出口不转发流量")
}

func (u *blockingStopUpstream) SupportsUDP() bool          { return false }
func (u *blockingStopUpstream) Describe() string           { return "假出口（仅测试用）" }
func (u *blockingStopUpstream) DialTimeout() time.Duration { return time.Second }

// TestStartNodeStopsOldServerOutsideLock 是审查 #4 的回归测试：
// startNode 替换掉一个**仍在运行**的旧服务时，不许把全局写锁带进 Stop()。
//
// 【状态是怎么来的】复核时确认的可达交错：startNode 从读配置到发布服务之间
// 没有锁保护，期间并发的设置/节点改动会换掉配置里的监听地址，另一次激活也能
// 抢先把一个跑着的服务写进 a.servers。于是走到替换那一步时，a.servers[id]
// 会是一个"监听地址和当前配置已经不同、但仍在运行"的实例。
//
// 测试直接构造这个状态（改配置里的端口，不碰 a.servers），而不是靠多协程
// 赛跑去碰运气 —— 状态一样，但断言可以确定性复现，不会变成偶发红的测试。
//
// 【为什么必须用真实的全局锁去 TryLock，而不是量时间】Stop 的耗时在不同
// 内核/平台差很多（Windows 时间戳精度也粗），量时间必然要设阈值、必然偶发。
// 这里让假出口把 Stop 硬卡住，于是"锁有没有被带进 Stop"变成一个二值事实。
func TestStartNodeStopsOldServerOutsideLock(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(release) }) }

	// 只替换默认内核（sing-box）的出口工厂：Shadowsocks 节点走这条路。
	// 跑完立刻还原成真工厂（engine.init 里注册的就是 engine.New），
	// 免得影响同包其它测试。
	proxy.RegisterSingBoxFactory(func(config.Node, config.Front, func(string, ...any)) (proxy.Upstream, error) {
		return &blockingStopUpstream{entered: entered, release: release}, nil
	})
	defer proxy.RegisterSingBoxFactory(engine.New)
	defer releaseNow()

	base := freeBase(t, 15000, 2)
	a := newTestApp(t, []config.Node{{
		ID: "n1", Name: "旧服务", Protocol: config.ProtocolShadowsocks,
		ListenHost: "127.0.0.1", ListenPort: base,
		Server: "127.0.0.1", Port: 1, Method: "aes-128-gcm", Password: "pw",
		Front: config.FrontDirect,
	}}, config.Front{Type: config.FrontTypeDirect})

	if err := a.Activate([]string{"n1"}); err != nil {
		t.Fatalf("激活节点失败：%v", err)
	}
	oldSrv := a.serverOf("n1")
	if oldSrv == nil || !oldSrv.Running() {
		t.Fatal("旧服务没起来，测试前提不成立")
	}

	// 只改配置里的端口，不动 a.servers：旧服务仍在 base 上跑着，
	// 而配置已经指向 base+1 —— 这就是并发窗口里那个"地址与配置不一致"的状态。
	a.mu.Lock()
	a.cfg.Nodes[a.indexOfLocked("n1")].ListenPort = base + 1
	a.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- a.startNode("n1") }()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("旧服务的 Stop 一直没开始：startNode 没走到替换那一步")
	}

	// 此刻旧服务的 Stop 正卡在假出口里。锁必须是空的 —— 界面上的读取
	// （节点列表、统计、端口自检）全靠它。
	held := true
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a.mu.TryLock() {
			a.mu.Unlock()
			held = false
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	releaseNow()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("startNode 失败：%v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("startNode 没有返回")
	}

	if held {
		t.Fatal("旧服务的 Stop 是持着全局写锁执行的：界面会一直卡到 Stop 结束（缺陷 #4）")
	}
	if s := a.serverOf("n1"); s == nil || s == oldSrv {
		t.Error("锁内应当已经发布了新服务")
	}
	if oldSrv.Running() {
		t.Error("旧服务应当已经被停掉")
	}
}
