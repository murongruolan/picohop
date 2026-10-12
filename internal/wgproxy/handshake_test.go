package wgproxy

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/murongruolan/picohop/internal/config"
	"github.com/murongruolan/picohop/internal/proxy"
)

// ---------------------------------------------------------------------------
// 审查 #27：等握手不许占着锁，失败/停机路径必须能停掉轮询协程。
//
// 这一组用例刻意**不依赖真实 WireGuard 服务端**：
//   - 纯逻辑部分（识别握手、超时、停机、轮询协程数量）用注入的假 IpcGet 直接测；
//   - 涉及真设备的两个用例只建"永远握不上手"的隧道（服务端地址指向本机没人应答的
//     UDP 端口），验证 Stop 不会被 12 秒的握手等待堵住、以及 Stop 之后资源真的关干净。
// ---------------------------------------------------------------------------

// TestHandshakeSeen 锁住"什么算握上了"这份定义。
func TestHandshakeSeen(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"空状态", "", false},
		{"从未握手", "public_key=abc\nlast_handshake_time_sec=0\nlast_handshake_time_nsec=0\n", false},
		{"握过手", "public_key=abc\nlast_handshake_time_sec=1700000000\nlast_handshake_time_nsec=1\n", true},
		{"只有 nsec 不算", "last_handshake_time_nsec=12345\n", false},
		{"值是垃圾", "last_handshake_time_sec=abc\n", false},
		{"负值不算", "last_handshake_time_sec=-1\n", false},
		{"前后有空白", "  last_handshake_time_sec=42  \n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := handshakeSeen(c.out); got != c.want {
				t.Fatalf("handshakeSeen(%q) = %v，期望 %v", c.out, got, c.want)
			}
		})
	}
}

// stuckGet 造一个"进去就卡住、直到 release 关闭才返回"的假 IpcGet
// （模拟 wireguard-go 的 ipcMutex 被长时间占住），并统计被调用了几次。
func stuckGet(release <-chan struct{}, calls *atomic.Int64, entered chan<- struct{}) func() (string, error) {
	return func() (string, error) {
		calls.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return "", nil
	}
}

// TestWaitHandshakeTimeoutDoesNotWaitForStuckIpcGet
// 卡住的 IpcGet 不许拖住调用方，也不许让轮询协程堆积。
func TestWaitHandshakeTimeoutDoesNotWaitForStuckIpcGet(t *testing.T) {
	u := &Upstream{}
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseNow)

	var calls atomic.Int64
	entered := make(chan struct{}, 1)
	get := stuckGet(release, &calls, entered)

	const timeout = 400 * time.Millisecond
	result := make(chan error, 1)
	begin := time.Now()
	go func() {
		result <- u.waitHandshake(context.Background(), make(chan struct{}), get, timeout)
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("轮询协程没有起来")
	}

	var err error
	select {
	case err = <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("waitHandshake 被卡住的 IpcGet 拖住了，没有按时超时返回")
	}
	elapsed := time.Since(begin)
	if err == nil || !strings.Contains(err.Error(), "握手超时") {
		t.Fatalf("期望握手超时错误，实际 %v", err)
	}
	// 【超时一到就得返回】旧实现把 IpcGet 放在"每轮 2 秒看门狗"里，卡住时
	// 调用方要等看门狗超时才会往下走（400ms 的等待会被拖到 ~2.3 秒）；
	// 现在调用方只等自己的定时器，跟卡住的那次 IpcGet 无关。
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("超时 %v 后过了 %v 才返回：调用方在等卡住的 IpcGet", timeout, elapsed)
	}
	// 【不许堆积】旧实现每轮（300ms）都新开一个协程去调 IpcGet，收不到就 continue：
	// 轮询协程会一轮一轮堆起来。这里固定一个轮询协程，所以永远只有 1 轮在途。
	if n := calls.Load(); n != 1 {
		t.Fatalf("等待期间发起了 %d 轮轮询（应当只有 1 个轮询协程、1 轮在途）", n)
	}

	// 调用方已经返回：放行卡住的那次 IpcGet 之后，轮询协程必须立刻退出、不再轮询。
	releaseNow()
	time.Sleep(2*handshakePollInterval + 200*time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("waitHandshake 返回后轮询协程还在轮询：已发起 %d 轮", n)
	}
}

// TestWaitHandshakeStopAbortsAndStopsPoller 停机信号要能立刻收手并停掉轮询协程。
func TestWaitHandshakeStopAbortsAndStopsPoller(t *testing.T) {
	u := &Upstream{}
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseNow)

	var calls atomic.Int64
	entered := make(chan struct{}, 1)
	get := stuckGet(release, &calls, entered)

	stop := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		// 超时给得很长：唯一能让它返回的就该是停机信号。
		result <- u.waitHandshake(context.Background(), stop, get, 60*time.Second)
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("轮询协程没有起来")
	}

	close(stop)
	select {
	case err := <-result:
		if !errors.Is(err, errStoppedDuringHandshake) {
			t.Fatalf("期望 errStoppedDuringHandshake（握手完成前被停止），实际 %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("停机信号没能结束握手等待（Stop 会一直卡着）")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("停机路径留下了 %d 轮轮询，期望 1", n)
	}

	releaseNow()
	time.Sleep(2*handshakePollInterval + 200*time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("waitHandshake 返回后轮询协程还在轮询：已发起 %d 轮", n)
	}
}

// TestWaitHandshakeDetectsHandshake 真的握上手就返回 nil。
func TestWaitHandshakeDetectsHandshake(t *testing.T) {
	u := &Upstream{}
	var calls atomic.Int64
	get := func() (string, error) {
		if calls.Add(1) < 2 {
			return "public_key=abc\nlast_handshake_time_sec=0\n", nil
		}
		return "public_key=abc\nlast_handshake_time_sec=1700000000\n", nil
	}
	if err := u.waitHandshake(context.Background(), make(chan struct{}), get, 5*time.Second); err != nil {
		t.Fatalf("应当识别出握手完成，实际 %v", err)
	}
	if calls.Load() < 2 {
		t.Fatalf("轮询次数异常：%d", calls.Load())
	}
}

// TestWaitHandshakeCtxCancelStopsPoller ctx 取消也要收手。
func TestWaitHandshakeCtxCancelStopsPoller(t *testing.T) {
	u := &Upstream{}
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseNow)

	var calls atomic.Int64
	entered := make(chan struct{}, 1)
	get := stuckGet(release, &calls, entered)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- u.waitHandshake(ctx, make(chan struct{}), get, 60*time.Second)
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("轮询协程没有起来")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("期望 context.Canceled，实际 %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消没能结束握手等待")
	}
	releaseNow()
	time.Sleep(2*handshakePollInterval + 200*time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("ctx 取消后轮询协程还在轮询：已发起 %d 轮", n)
	}
}

// ---------------------------------------------------------------------------
// 下面两个用例要真的建出 WireGuard 设备（用户态，无需管理员权限）。
// ---------------------------------------------------------------------------

// newTestUpstream 造一条"设备建得起来、但永远握不上手"的出口：
// 服务端地址指向本机一个没人应答的 UDP 端口，前置走直连。
func newTestUpstream(t *testing.T) (*Upstream, proxy.Front) {
	t.Helper()

	local, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("生成本端密钥失败：%v", err)
	}
	peer, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("生成对端密钥失败：%v", err)
	}

	// 借一个空闲 UDP 端口号再立刻放掉：这个地址上不会有 WireGuard 服务端应答。
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("申请空闲 UDP 端口失败：%v", err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	_ = probe.Close()

	node := config.Node{
		Name:     "测试WG",
		Protocol: config.ProtocolWireGuard,
		Server:   "127.0.0.1",
		Port:     port,
		WireGuard: &config.WireGuardConfig{
			PrivateKey:    local.PrivateKey,
			Address:       "10.99.0.2/32",
			PeerPublicKey: peer.PublicKey,
			AllowedIPs:    "10.99.0.0/24",
			MTU:           1420,
		},
	}
	front := proxy.NewFront(config.Front{Type: config.FrontTypeDirect, TimeoutMs: 5000})
	up, err := New(node, front)
	if err != nil {
		front.Close()
		t.Fatalf("构造 WireGuard 出口失败：%v", err)
	}
	u := up.(*Upstream)
	t.Cleanup(func() {
		_ = u.Stop()
		front.Close()
	})
	return u, front
}

// waitCond 轮询等待条件成立（Windows 上定时器精度粗，一律轮询而不是睡固定时长）。
func waitCond(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// handshakingNow 报告"设备已建好、正在等握手"。
func (u *Upstream) handshakingNow() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.dev != nil && u.handshaking
}

// TestStopIsNotBlockedByHandshakeWait 是审查 #27 的回归用例。
//
// 旧实现里 Start 全程持着 u.mu 去等 12 秒握手，任何并发的 Stop() 都得干等满
// handshakeTimeout（用户看到的就是"点了停止没反应"）。这里把 Start 挂在后台，
// 等它进入等握手状态，然后要求 Stop 立刻返回。
func TestStopIsNotBlockedByHandshakeWait(t *testing.T) {
	up, front := newTestUpstream(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startErr := make(chan error, 1)
	go func() { startErr <- up.Start(ctx, front) }()

	if !waitCond(5*time.Second, up.handshakingNow) {
		t.Fatal("设备没有进入等握手状态（若这里超时，多半是 Start 把 u.mu 一直攥在手里）")
	}

	begin := time.Now()
	if err := up.Stop(); err != nil {
		t.Logf("Stop 返回：%v（本用例只关心它多久返回）", err)
	}
	elapsed := time.Since(begin)
	if elapsed > 3*time.Second {
		t.Fatalf("Stop 等了 %v 才返回：等握手还在持锁", elapsed)
	}

	select {
	case err := <-startErr:
		if !errors.Is(err, errStoppedDuringHandshake) {
			t.Fatalf("Start 应当以 errStoppedDuringHandshake 收手，实际 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("设备被停掉后 Start 没有收手")
	}
}

// TestDialRefusedWhileHandshakePending 握手没确认之前，拨号必须明确报错。
//
// 旧实现靠"Dial 和 Start 抢同一把锁"顺带挡住这种调用；Start 改成不持锁等握手之后，
// 这个保证必须由 Dial 自己给出（fail-closed），否则会在没通的隧道上悄悄拨号，
// 表现为一堆查不出原因的连接超时。
func TestDialRefusedWhileHandshakePending(t *testing.T) {
	up, front := newTestUpstream(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startErr := make(chan error, 1)
	go func() { startErr <- up.Start(ctx, front) }()

	if !waitCond(5*time.Second, up.handshakingNow) {
		t.Fatal("设备没有进入等握手状态（若这里超时，多半是 Start 把 u.mu 一直攥在手里）")
	}

	_, err := up.Dial(ctx, front, "10.99.0.1:443")
	if err == nil {
		t.Fatal("握手还没完成就不该允许拨号")
	}
	if !strings.Contains(err.Error(), "正在建立") {
		t.Fatalf("期望提示隧道正在建立（尚未完成握手），实际 %v", err)
	}

	_ = up.Stop()
	select {
	case <-startErr:
	case <-time.After(5 * time.Second):
		t.Fatal("设备被停掉后 Start 没有收手")
	}
}

// TestRestartAfterStopClosesDevice Stop 之后再次建隧道，新一轮的资源也得能被关掉。
//
// 【为什么需要它】Start 原来不复位 u.closed，于是"Stop 过再 Start"之后，
// stopLocked 会因为 closed==true 直接返回：新建设备的收包协程与 UDP 套接字
// 永远没人关。这条路径在"停止后重启同一条出口"时是常态。
func TestRestartAfterStopClosesDevice(t *testing.T) {
	up, front := newTestUpstream(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 直接调 setupDevice：这里只关心设备的生灭，不需要真等满 12 秒握手。
	if _, _, err := up.setupDevice(ctx, front); err != nil {
		t.Fatalf("第一轮建隧道失败：%v", err)
	}
	if err := up.Stop(); err != nil {
		t.Logf("第一轮 Stop 返回：%v", err)
	}

	if _, _, err := up.setupDevice(ctx, front); err != nil {
		t.Fatalf("第二轮建隧道失败：%v", err)
	}
	if err := up.Stop(); err != nil {
		t.Logf("第二轮 Stop 返回：%v", err)
	}

	up.mu.Lock()
	dev, bind, tnet := up.dev, up.bind, up.tnet
	up.mu.Unlock()
	if dev != nil || bind != nil || tnet != nil {
		t.Fatalf("Stop 之后仍有资源挂着（dev=%v bind=%v tnet=%v）：closed 没有复位，设备/套接字/收包协程泄漏",
			dev != nil, bind != nil, tnet != nil)
	}
}
