package engine

// mihomo UDP 通道（mihomo_udp.go）的回归测试。
//
// 【为什么必须为它单写测试】这块是"每个目标一个常驻读协程 + 一条 fan-in 队列"的并发结构，
// 靠读代码看不出错；而它替换掉的旧实现有**实测代价**：单协程顺序轮询 + 每目标 100ms 读超时，
// 于是延迟 ∝ 目标序号、前面的目标会饿死后面的、quit 最坏滞后 100ms×N。下面几条就是钉住新实现
// 必须守住的性质：
//
//	1. 后面的目标不许被前面的静默目标拖住；
//	2. Close / quit 必须能立刻叫醒阻塞中的收包（醒不过来就是关停死锁，本项目在 WG 侧吃过这个亏）；
//	3. 空闲目标必须被回收（否则长命会话按目标数泄漏协程与缓冲）；
//	4. 关停后不许再建通道；
//	5. 构造时拿到的 ctx 已经是被取消的，也不许影响建连（真实调用方就是"用完立刻 cancel"）；
//	6. 队列满时读协程绝不许阻塞（否则一条慢会话拖住所有目标）。
//
// 用假 PacketConn 而不是真网络：这些性质与出站协议无关，而假连接能让
// "某个目标什么时候有回包"完全可控 —— 真网络做不到这一点。

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"

	"github.com/murongruolan/picohop/internal/proxy"
)

// ---------------------------------------------------------------- 假 PacketConn

type fakePkt struct {
	payload []byte
	addr    net.Addr
}

// fakePacketConn 是 C.PacketConn 的最小实现，但**忠实复刻两条硬性质**：
// SetReadDeadline 到点让阻塞中的读返回 os.ErrDeadlineExceeded；
// Close 让阻塞中的读返回 net.ErrClosed（这是唯一能叫醒它的手段，新实现依赖这一点）。
type fakePacketConn struct {
	inbox chan fakePkt

	mu      sync.Mutex
	rdl     time.Time
	wdl     time.Time
	written []net.Addr

	closeOnce sync.Once
	closed    chan struct{}
}

var _ C.PacketConn = (*fakePacketConn)(nil)

func newFakePacketConn() *fakePacketConn {
	return &fakePacketConn{inbox: make(chan fakePkt, 8192), closed: make(chan struct{})}
}

// feed 往这个连接"投喂"一颗回包（模拟对端发回来）。
func (c *fakePacketConn) feed(payload string, from *net.UDPAddr) {
	c.inbox <- fakePkt{payload: []byte(payload), addr: from}
}

func (c *fakePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.mu.Lock()
	rdl := c.rdl
	c.mu.Unlock()

	var timeout <-chan time.Time
	if !rdl.IsZero() {
		left := time.Until(rdl)
		if left <= 0 {
			return 0, nil, os.ErrDeadlineExceeded
		}
		timer := time.NewTimer(left)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case pk := <-c.inbox:
		return copy(p, pk.payload), pk.addr, nil
	case <-timeout:
		return 0, nil, os.ErrDeadlineExceeded
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *fakePacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	c.mu.Lock()
	c.written = append(c.written, addr)
	c.mu.Unlock()
	return len(p), nil
}

func (c *fakePacketConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *fakePacketConn) LocalAddr() net.Addr { return &net.UDPAddr{} }

func (c *fakePacketConn) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}

func (c *fakePacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rdl = t
	c.mu.Unlock()
	return nil
}

func (c *fakePacketConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.wdl = t
	c.mu.Unlock()
	return nil
}

func (c *fakePacketConn) WaitReadFrom() ([]byte, func(), net.Addr, error) {
	buf := make([]byte, udpReadBufSize)
	n, addr, err := c.ReadFrom(buf)
	if err != nil {
		return nil, nil, nil, err
	}
	return buf[:n], func() {}, addr, nil
}

func (c *fakePacketConn) writtenAddrs() []net.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]net.Addr(nil), c.written...)
}

func (c *fakePacketConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

func (c *fakePacketConn) Chains() C.Chain                               { return nil }
func (c *fakePacketConn) ProviderChains() C.Chain                       { return nil }
func (c *fakePacketConn) AppendToChains(C.ProxyAdapter)                 {}
func (c *fakePacketConn) RemoteDestination() string                     { return "" }
func (c *fakePacketConn) ResolveUDP(context.Context, *C.Metadata) error { return nil }

// ---------------------------------------------------------------- 通道骨架

// newTestUDPChannel 直接搭出通道骨架（不碰 mihomo 出站）：被测的是分派 / 回收 / 关停，
// 与"某个出站怎么把包送出去"无关。
func newTestUDPChannel(ctx context.Context) *mihomoUDPChannel {
	if ctx == nil {
		ctx = context.Background()
	}
	return &mihomoUDPChannel{
		ctx:     ctx,
		targets: map[string]*udpTarget{},
		pkts:    make(chan udpDatagram, udpFanInQueue),
		done:    make(chan struct{}),
	}
}

// addTestTarget 登记一个目标并起它的读协程。idleAge > 0 表示"它已经空闲了这么久"
// （用来测空闲回收；必须在起读协程**之前**设好，否则读协程已经按"刚活跃"算出了截止时间）。
func addTestTarget(c *mihomoUDPChannel, host string, port int, pc *fakePacketConn, idleAge time.Duration) *udpTarget {
	key := net.JoinHostPort(host, strconv.Itoa(port))
	t := &udpTarget{key: key, pc: pc, dest: &net.UDPAddr{IP: net.ParseIP(host), Port: port}}
	t.lastAct.Store(time.Now().Add(-idleAge).UnixMilli())
	c.mu.Lock()
	c.targets[key] = t
	c.mu.Unlock()
	go c.readLoop(t)
	return t
}

func (c *mihomoUDPChannel) targetCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.targets)
}

// ---------------------------------------------------------------- 1. 不许饿死后面的目标

func TestUDPChannelDoesNotStarveLaterTargets(t *testing.T) {
	c := newTestUDPChannel(nil)
	defer c.Close()

	// 前面挂三个**永远没有回包**的目标：旧实现每个都要点满 100ms 读超时，
	// 于是第四个目标的回包最坏要等 300ms 才回得去（而且第一个目标一直有包时，
	// 后面的会被一直饿着）。
	for i := 0; i < 3; i++ {
		addTestTarget(c, "10.0.0."+strconv.Itoa(i+1), 53, newFakePacketConn(), 0)
	}
	live := newFakePacketConn()
	addTestTarget(c, "10.0.0.9", 53, live, 0)

	// 先等四个读协程都进了阻塞读，再投喂 —— 否则测到的只是"队列里本来就有包"。
	time.Sleep(50 * time.Millisecond)
	live.feed("hi", &net.UDPAddr{IP: net.ParseIP("10.0.0.9"), Port: 5300})

	// 【阈值 200ms 是怎么来的】旧实现是**单协程顺序轮询**：每个静默目标各占满 100ms
	// 读超时，三个就是 300ms 起步（第一个目标一直有包时后面的还会被彻底饿死）。
	// 新实现是一个目标一个读协程，回包互相不排队，实测在 1ms 量级 —— 200ms 给了
	// 两个数量级的余量，既不误报也不至于把"串行"放过去。
	// 这条阈值是用**变异验证**校准过的：把读串行化 + 给静默目标加 100ms 占用之后，
	// 它确实会变红（见 docs/COMMIT_HISTORY.md 里这一节的记录）。

	buf := make([]byte, 2048)
	type result struct {
		n    int
		host string
		port int
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		n, host, port, err := c.ReceiveFromUDP(nil, buf)
		ch <- result{n, host, port, err}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("收包失败: %v", r.err)
		}
		if string(buf[:r.n]) != "hi" || r.host != "10.0.0.9" || r.port != 5300 {
			t.Fatalf("收到的数据报不对：payload=%q 来源=%s:%d", buf[:r.n], r.host, r.port)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("活跃目标的回包被前面的静默目标拖住了：旧实现单协程顺序轮询，三个静默目标就会把它拖到 300ms 以上")
	}
}

// ---------------------------------------------------------------- 2. Close / quit 必须能叫醒

func TestUDPChannelCloseWakesBlockedReceive(t *testing.T) {
	c := newTestUDPChannel(nil)
	res := make(chan error, 1)
	go func() {
		_, _, _, err := c.ReceiveFromUDP(nil, make([]byte, 64))
		res <- err
	}()
	time.Sleep(50 * time.Millisecond) // 让它真的阻塞在收包上

	if err := c.Close(); err != nil {
		t.Fatalf("Close 不该报错: %v", err)
	}
	select {
	case err := <-res:
		if !errors.Is(err, proxy.ErrUDPChannelClosed) {
			t.Fatalf("Close 后收包应当返回 ErrUDPChannelClosed（调用方据此安静退出），实际 %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close 没能叫醒阻塞中的收包 —— 这正是本项目在 WireGuard 侧遇到过的关停死锁")
	}
	// 幂等：会话结束路径可能被多个人触发（quit、控制连接断开、空闲回收）。
	if err := c.Close(); err != nil {
		t.Fatalf("重复 Close 应当无害: %v", err)
	}
}

func TestUDPChannelQuitWakesBlockedReceive(t *testing.T) {
	c := newTestUDPChannel(nil)
	defer c.Close()

	quit := make(chan struct{})
	res := make(chan error, 1)
	go func() {
		_, _, _, err := c.ReceiveFromUDP(quit, make([]byte, 64))
		res <- err
	}()
	time.Sleep(50 * time.Millisecond)
	close(quit)

	select {
	case err := <-res:
		if !errors.Is(err, proxy.ErrUDPChannelClosed) {
			t.Fatalf("quit 关闭后应当返回 ErrUDPChannelClosed，实际 %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("quit 关闭没能叫醒阻塞中的收包")
	}
}

// ---------------------------------------------------------------- 3. 空闲目标必须被回收

func TestUDPChannelEvictsIdleTarget(t *testing.T) {
	c := newTestUDPChannel(nil)
	defer c.Close()

	pc := newFakePacketConn()
	addTestTarget(c, "10.0.0.1", 53, pc, udpTargetIdle+time.Second)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && c.targetCount() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if left := c.targetCount(); left != 0 {
		t.Fatalf("空闲超过 %v 的目标应当被回收，实际还留着 %d 个", udpTargetIdle, left)
	}
	if !pc.isClosed() {
		t.Fatal("回收目标时必须连底层通道一起关掉，否则读协程与 socket 都留着")
	}
}

// ---------------------------------------------------------------- 4. 关停后不许再建通道

func TestUDPChannelRejectsSendAfterClose(t *testing.T) {
	c := newTestUDPChannel(nil)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.SendToUDP([]byte("x"), "10.0.0.1", 53); err == nil {
		t.Fatal("关停之后发送应当报错，而不是默默建一条新通道")
	}
	if n := c.targetCount(); n != 0 {
		t.Fatalf("关停后不该再建出通道，实际 %d 个", n)
	}
}

// ---------------------------------------------------------------- 5. ctx 被取消也不影响

func TestNewMihomoUDPChannelSurvivesCancelledCtx(t *testing.T) {
	// 【这条钉的是一个真 bug】真实调用方（internal/proxy/handler.go）是这么用的：
	//   ctx, cancel := context.WithTimeout(...); up, _ := DialUDP(ctx, front); cancel()
	// 返回后立刻取消，而我们的通道是**懒建**的。旧实现直接把那个 ctx 存下来用在
	// ListenPacketContext/ResolveUDP 上，于是第一个**域名**目标的建连会拿到
	// context canceled（字面量 IP 目标侥幸绕过）—— 症状是"UDP 全都不通"。
	type ctxKey string
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey("k"), "v"))
	cancel()

	c := newMihomoUDPChannel(nil, ctx)
	defer c.Close()
	if err := c.ctx.Err(); err != nil {
		t.Fatalf("通道的 ctx 不该继承传入 ctx 的取消（否则懒建连会当场失败）: %v", err)
	}
	if got := c.ctx.Value(ctxKey("k")); got != "v" {
		t.Fatalf("应当只去掉取消/超时、保留 ctx 上的值，实际 %v", got)
	}
}

// ---------------------------------------------------------------- 6. 队列满不许阻塞

func TestUDPChannelDropsInsteadOfBlockingWhenQueueFull(t *testing.T) {
	c := newTestUDPChannel(nil)
	defer c.Close()

	pc := newFakePacketConn()
	addTestTarget(c, "10.0.0.1", 53, pc, 0)

	// 灌进远超队列容量的回包，并且**先不消费**：读协程此时只能丢包，绝不能卡住。
	for i := 0; i < udpFanInQueue*3; i++ {
		pc.feed("x", &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 53})
	}
	time.Sleep(100 * time.Millisecond)

	got := make(chan struct{})
	go func() {
		if _, _, _, err := c.ReceiveFromUDP(nil, make([]byte, 64)); err != nil {
			t.Errorf("队列满之后收包仍应正常: %v", err)
		}
		close(got)
	}()
	select {
	case <-got:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("队列满时读协程阻塞了：一条慢会话会把同会话的所有目标一起拖住")
	}
}

// ---------------------------------------------------------------- 7. 目标地址用解析结果

func TestTargetAddrPrefersResolvedIP(t *testing.T) {
	// 旧实现每个数据报都自己 net.LookupIP 一次（同一目标的每个包可能落到不同 IP），
	// 而解析结果在 ResolveUDP 那一步就已经拿到、存在 metadata.DstIP 里了。
	meta := &C.Metadata{Host: "example.invalid", DstPort: 53}
	meta.DstIP = netip.MustParseAddr("203.0.113.7")

	got := targetAddr(meta, "example.invalid", 53)
	if got == nil || got.IP == nil || got.IP.String() != "203.0.113.7" || got.Port != 53 {
		t.Fatalf("应当用 ResolveUDP 解析出的 IP（且不再解析域名），实际 %v", got)
	}
}
