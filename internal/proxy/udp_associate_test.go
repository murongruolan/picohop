package proxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ------------------------------------------------------------------
// 测试替身：假出口 + 内存 UDP 通道 + 事件收集器
// ------------------------------------------------------------------

// fakeDatagram 是一个被转发（或准备投喂）的 UDP 数据报。
type fakeDatagram struct {
	payload []byte
	host    string
	port    int
}

// fakeUDPChannel 是一条纯内存的 UDPChannel：
// SendToUDP 的产出进 sent 供测试观察，测试往 recv 里投喂就等价于"上游回包"。
//
// 【为什么不用真的 UDP 套接字】测的是 handler 的会话生命周期（登记 / close 事件 /
// 上界 / 空闲回收），不是网络栈；内存通道没有丢包与超时抖动，断言才是确定的。
type fakeUDPChannel struct {
	sent   chan fakeDatagram
	recv   chan fakeDatagram
	closed chan struct{}
	once   sync.Once
}

func newFakeUDPChannel() *fakeUDPChannel {
	return &fakeUDPChannel{
		sent:   make(chan fakeDatagram, 16),
		recv:   make(chan fakeDatagram, 16),
		closed: make(chan struct{}),
	}
}

func (c *fakeUDPChannel) SendToUDP(payload []byte, host string, port int) error {
	d := fakeDatagram{payload: append([]byte(nil), payload...), host: host, port: port}
	select {
	case c.sent <- d:
		return nil
	case <-c.closed:
		return ErrUDPChannelClosed
	}
}

func (c *fakeUDPChannel) ReceiveFromUDP(quit <-chan struct{}, buf []byte) (int, string, int, error) {
	select {
	case <-quit:
		return 0, "", 0, ErrUDPChannelClosed
	case <-c.closed:
		return 0, "", 0, ErrUDPChannelClosed
	case d := <-c.recv:
		n := copy(buf, d.payload)
		return n, d.host, d.port, nil
	}
}

func (c *fakeUDPChannel) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *fakeUDPChannel) inject(host string, port int, payload string) {
	c.recv <- fakeDatagram{payload: []byte(payload), host: host, port: port}
}

func (c *fakeUDPChannel) waitSent(t *testing.T, timeout time.Duration) fakeDatagram {
	t.Helper()
	select {
	case d := <-c.sent:
		return d
	case <-time.After(timeout):
		t.Fatalf("等上游收到数据报超时（%v）", timeout)
	}
	return fakeDatagram{}
}

func (c *fakeUDPChannel) waitClosed(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-c.closed:
	case <-time.After(timeout):
		t.Fatalf("等上游 UDP 通道被关闭超时（%v）", timeout)
	}
}

func (c *fakeUDPChannel) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// udpAssocUpstream 是只服务 UDP ASSOCIATE 的假出口，并记下 DialUDP 被调了几次。
type udpAssocUpstream struct {
	ch    *fakeUDPChannel
	dials atomic.Int64
}

func (u *udpAssocUpstream) Start(context.Context, Front) error { return nil }
func (u *udpAssocUpstream) Stop() error                        { return nil }
func (u *udpAssocUpstream) Dial(context.Context, Front, string) (net.Conn, error) {
	return nil, errors.New("本假出口只支持 UDP")
}
func (u *udpAssocUpstream) DialUDP(context.Context, Front) (UDPChannel, error) {
	u.dials.Add(1)
	return u.ch, nil
}
func (u *udpAssocUpstream) SupportsUDP() bool          { return true }
func (u *udpAssocUpstream) Describe() string           { return "fake-udp" }
func (u *udpAssocUpstream) DialTimeout() time.Duration { return time.Second }

// connEventRecorder 收集连接事件。
//
// 【为什么要加锁】事件由转发的 goroutine 发出、断言在主 goroutine 上做；
// 不加锁就是测试自己制造的 data race（而 Windows 上 CGO_ENABLED=0，-race 用不了，
// 不会有任何提示）。
type connEventRecorder struct {
	mu sync.Mutex
	ev []ConnEvent
}

func (r *connEventRecorder) sink(ev ConnEvent) {
	r.mu.Lock()
	r.ev = append(r.ev, ev)
	r.mu.Unlock()
}

func (r *connEventRecorder) snapshot() []ConnEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ConnEvent(nil), r.ev...)
}

// waitOpenAny 等到第一条指定协议、指定节点的 open 事件（ID 由被测代码分配，
// 测试事先不知道，所以按协议认）。
func (r *connEventRecorder) waitOpenAny(t *testing.T, protocol string, timeout time.Duration) ConnEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, ev := range r.snapshot() {
			if ev.Phase == ConnPhaseOpen && ev.Protocol == protocol {
				return ev
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("等 %q 的 open 事件超时（%v），已收到：%+v", protocol, timeout, r.snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *connEventRecorder) waitClose(t *testing.T, id uint64, timeout time.Duration) ConnEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, ev := range r.snapshot() {
			if ev.Phase == ConnPhaseClose && ev.ID == id {
				return ev
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("等 %d 的 close 事件超时（%v），已收到：%+v", id, timeout, r.snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *connEventRecorder) countClose(id uint64) int {
	n := 0
	for _, ev := range r.snapshot() {
		if ev.Phase == ConnPhaseClose && ev.ID == id {
			n++
		}
	}
	return n
}

// ------------------------------------------------------------------
// 测试脚手架
// ------------------------------------------------------------------

// holdUDPSessionQuotaForTest 记住测试开始时的名额账本，并在测试收尾时把它拉回原值。
//
// 测试内部会显式轮询"账本归位"（waitUDPSessionQuota）来做断言；这里的兜底只是防止
// 中途 Fatalf 退出时，漏掉的名额污染后面的测试（名额是进程级的）。
func holdUDPSessionQuotaForTest(t *testing.T) int64 {
	t.Helper()
	base := udpSessionCount.Load()
	t.Cleanup(func() { udpSessionCount.Store(base) })
	return base
}

// waitUDPSessionQuota 轮询等名额账本回到期望值。
//
// 【为什么不用一次性的相等断言】会话的 release 在 handler goroutine 的 defer 里，
// 与"活动连接表已清空"之间有几微秒的先后差；直接断言会变成偶发红。
func waitUDPSessionQuota(t *testing.T, want int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if got := udpSessionCount.Load(); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("UDP 会话名额没归位：期望 %d，实际 %d（acquire / release 没配对）", want, udpSessionCount.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitActiveConns(t *testing.T, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if got := ActiveConnCount(); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("活动连接数没到期望值：期望 %d，实际 %d", want, ActiveConnCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// newUDPAssocHandler 造一个只服务 UDP 的 handler。
//
// 刻意不设 Logf：空闲回收 / 会话结束的日志是在 handler 自己的 goroutine 里打的，
// 若接到 t.Logf 上，测试结束后才落下的日志会 panic（"Log in goroutine after test
// has completed"）。这里的断言全部落在事件与计数上，不需要日志。
func newUDPAssocHandler(up Upstream, idle time.Duration) *Handler {
	return &Handler{Name: "n1", Upstream: up, Stats: &Stats{}, AllowUDP: true, Idle: idle}
}

// serveUDPTestClient 起一个真实 TCP 监听并用 handler 服务它（走完整 ServeClient 路径）。
func serveUDPTestClient(t *testing.T, h *Handler) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败：%v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go h.ServeClient(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// udpClient 是测试侧的"客户端"：一条 SOCKS5 控制连接 + 一个冒充客户端的 UDP 套接字。
type udpClient struct {
	tcp   net.Conn
	cr    *bufio.Reader
	sock  *net.UDPConn
	relay *net.UDPAddr
}

// associateUDP 完整走一遍 greeting + UDP ASSOCIATE。
// 成功时返回 (repSuccess, 客户端句柄)；失败时返回应答码与 nil。
func associateUDP(t *testing.T, addr string) (byte, *udpClient) {
	t.Helper()
	tcp, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("连本地代理失败：%v", err)
	}
	_ = tcp.SetDeadline(time.Now().Add(5 * time.Second))
	cr := bufio.NewReader(tcp)

	if _, err := tcp.Write([]byte{socks5Version, 0x01, authNone}); err != nil {
		t.Fatalf("发 greeting 失败：%v", err)
	}
	greet := make([]byte, 2)
	if _, err := io.ReadFull(cr, greet); err != nil {
		t.Fatalf("读 greeting 应答失败：%v", err)
	}
	if greet[0] != socks5Version || greet[1] != authNone {
		t.Fatalf("greeting 应答不对：% x", greet)
	}

	// UDP ASSOCIATE，DST 按 RFC 1928 填 0.0.0.0:0
	req := []byte{socks5Version, cmdUDPAssociate, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}
	if _, err := tcp.Write(req); err != nil {
		t.Fatalf("发 UDP ASSOCIATE 失败：%v", err)
	}
	head := make([]byte, 3)
	if _, err := io.ReadFull(cr, head); err != nil {
		t.Fatalf("读 UDP ASSOCIATE 应答失败：%v", err)
	}
	if head[0] != socks5Version {
		t.Fatalf("应答版本不对：% x", head)
	}
	if head[1] != repSuccess {
		// 失败应答的 BND 是 0.0.0.0:0 占位（共 7 字节），读掉免得脏了后续读取
		_, _ = io.ReadFull(cr, make([]byte, 7))
		t.Cleanup(func() { _ = tcp.Close() })
		return head[1], nil
	}
	host, port, err := readSocks5Addr(cr)
	if err != nil {
		t.Fatalf("读中继地址失败：%v", err)
	}
	relayIP := net.ParseIP(host)
	if relayIP == nil {
		t.Fatalf("中继地址应当是 IP，实际 %q", host)
	}
	_ = tcp.SetDeadline(time.Time{})

	sock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("开客户端 UDP 套接字失败：%v", err)
	}
	uc := &udpClient{tcp: tcp, cr: cr, sock: sock, relay: &net.UDPAddr{IP: relayIP, Port: port}}
	t.Cleanup(func() {
		_ = uc.tcp.Close()
		_ = uc.sock.Close()
	})
	return repSuccess, uc
}

// send 把一个 SOCKS5 UDP 帧发给中继口。
func (c *udpClient) send(t *testing.T, host string, port int, payload string) {
	t.Helper()
	frame, err := appendUDPHeader(nil, host, port, []byte(payload))
	if err != nil {
		t.Fatalf("封装 UDP 帧失败：%v", err)
	}
	if _, err := c.sock.WriteToUDP(frame, c.relay); err != nil {
		t.Fatalf("发数据报失败：%v", err)
	}
}

// recv 从中继口收一个回程帧并拆包。
func (c *udpClient) recv(t *testing.T, timeout time.Duration) (string, int, string) {
	t.Helper()
	_ = c.sock.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 64*1024)
	n, _, err := c.sock.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("收回程数据报失败：%v", err)
	}
	host, port, payload, err := parseUDPHeader(buf[:n])
	if err != nil {
		t.Fatalf("拆回程 UDP 帧失败：%v", err)
	}
	return host, port, string(payload)
}

// waitLiveStat 轮询等某条活动连接的实时统计达到期望值。
//
// 【为什么要轮询】字节计数紧接着转发成功之后累加，而客户端可能已经先收到包了
// （写成功与对面读到之间没有先后保证），直接断言会偶发红。
func waitLiveStat(t *testing.T, id uint64, up bool, want int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if v, ok := LiveConnStats()[id]; ok {
			got := v[1]
			if up {
				got = v[0]
			}
			if got >= want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("活动连接的实时字节数没到期望值：id=%d up=%v 期望 >= %d，实际 %v", id, up, want, LiveConnStats()[id])
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ------------------------------------------------------------------
// 用例
// ------------------------------------------------------------------

// TestUDPAssociateSessionIsRegisteredAndReportsClose 锁死审查 #15 的主诉求：
// UDP ASSOCIATE 会话必须登记进活动连接表，并在结束时补一条 close 事件。
//
// 【原来的样子】handler.go 里只手工发了一条 open 事件（ID 还是自己 new 的）：
// 既不登记 closers、也没有 close 事件。App 侧的连接表只在收到 close 时才把记录
// 从活动表移到已关闭表，于是那一行永远是 active —— 控制连接早断了、UDP 口也关了，
// 连接表里却一直挂着这条幽灵记录；点「关闭全部」也清不掉它。
func TestUDPAssociateSessionIsRegisteredAndReportsClose(t *testing.T) {
	base := holdUDPSessionQuotaForTest(t)
	CloseConnections(nil)

	rec := &connEventRecorder{}
	SetConnSink(rec.sink)
	t.Cleanup(func() { SetConnSink(nil); CloseConnections(nil) })

	ch := newFakeUDPChannel()
	up := &udpAssocUpstream{ch: ch}
	h := newUDPAssocHandler(up, time.Minute)

	rep, uc := associateUDP(t, serveUDPTestClient(t, h))
	if rep != repSuccess {
		t.Fatalf("UDP ASSOCIATE 应当成功，实际应答码 0x%02x（%s）", rep, ReplyText(rep))
	}

	open := rec.waitOpenAny(t, "udp", 2*time.Second)
	if open.Target != uc.relay.String() {
		t.Fatalf("open 事件的目标应当是本地中继口 %s，实际 %q", uc.relay, open.Target)
	}
	if open.Node != "n1" {
		t.Fatalf("open 事件应当带上节点名，实际 %q", open.Node)
	}
	if n := ActiveConnCount(); n != 1 {
		t.Fatalf("UDP 会话必须登记进活动连接表（否则「关闭全部」根本看不见它），实际登记 %d 条", n)
	}

	// 上行：真的转发出去，且实时统计是真实 payload 字节（不是假的 0）
	uc.send(t, "8.8.8.8", 53, "ping")
	dg := ch.waitSent(t, 2*time.Second)
	if dg.host != "8.8.8.8" || dg.port != 53 || string(dg.payload) != "ping" {
		t.Fatalf("上游收到的数据报不对：%s:%d %q", dg.host, dg.port, dg.payload)
	}
	waitLiveStat(t, open.ID, true, int64(len("ping")), 2*time.Second)

	// 下行：上游回包要按 SOCKS5 UDP 帧写回客户端
	ch.inject("8.8.8.8", 53, "pong")
	host, port, payload := uc.recv(t, 3*time.Second)
	if host != "8.8.8.8" || port != 53 || payload != "pong" {
		t.Fatalf("客户端收到的回程数据不对：%s:%d %q", host, port, payload)
	}
	waitLiveStat(t, open.ID, false, int64(len("pong")), 2*time.Second)

	// 控制连接断开 => 会话结束，必须补一条带字节与时长的 close 事件
	_ = uc.tcp.Close()
	closed := rec.waitClose(t, open.ID, 3*time.Second)
	if closed.Protocol != "udp" || closed.Target != uc.relay.String() {
		t.Fatalf("close 事件应当带上协议与目标：%+v", closed)
	}
	if closed.Up != int64(len("ping")) || closed.Down != int64(len("pong")) {
		t.Fatalf("close 事件应当带上真实字节数（up=%d down=%d）：%+v", len("ping"), len("pong"), closed)
	}
	if closed.Duration < 0 {
		t.Fatalf("close 事件的时长不该是负数：%+v", closed)
	}
	if n := rec.countClose(open.ID); n != 1 {
		t.Fatalf("close 事件只该发一次，实际 %d 次", n)
	}
	waitActiveConns(t, 0, 2*time.Second)
	waitUDPSessionQuota(t, base, 2*time.Second)
}

// TestUDPAssociateSessionIsClosableByCloseAll 锁死「关闭全部」真能关掉 UDP 会话：
// 登记了却不给 closers，等于按钮看得见、关不掉，那行幽灵记录仍然清不掉。
func TestUDPAssociateSessionIsClosableByCloseAll(t *testing.T) {
	base := holdUDPSessionQuotaForTest(t)
	CloseConnections(nil)

	rec := &connEventRecorder{}
	SetConnSink(rec.sink)
	t.Cleanup(func() { SetConnSink(nil); CloseConnections(nil) })

	ch := newFakeUDPChannel()
	h := newUDPAssocHandler(&udpAssocUpstream{ch: ch}, time.Minute)

	rep, uc := associateUDP(t, serveUDPTestClient(t, h))
	if rep != repSuccess {
		t.Fatalf("UDP ASSOCIATE 应当成功，实际应答码 0x%02x（%s）", rep, ReplyText(rep))
	}
	open := rec.waitOpenAny(t, "udp", 2*time.Second)

	if n := CloseConnections(nil); n != 1 {
		t.Fatalf("「关闭全部」应当关掉这 1 条 UDP 会话，实际报告 %d 条", n)
	}
	// 底层参与者真的被关：上游通道 + 控制连接
	ch.waitClosed(t, 2*time.Second)
	_ = uc.tcp.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := uc.cr.ReadByte(); err == nil {
		t.Fatal("控制连接应当被关掉（读应当报错/EOF），实际还能读到字节")
	}
	// 并且补上了 close 事件（界面上那一行才会从 active 归档掉）
	if closed := rec.waitClose(t, open.ID, 3*time.Second); closed.Phase != ConnPhaseClose {
		t.Fatalf("close 事件不对：%+v", closed)
	}
	waitActiveConns(t, 0, 2*time.Second)
	waitUDPSessionQuota(t, base, 2*time.Second)
}

// TestUDPSessionCapRejectsNewSessions 锁死"会话有上界"：占满之后必须拒绝，
// 而且被拒绝的尝试不许占名额（否则一次拒绝就把上限永久拉低一格）。
func TestUDPSessionCapRejectsNewSessions(t *testing.T) {
	base := holdUDPSessionQuotaForTest(t) // 收尾会把账本拉回 base

	for i := int64(0); i < maxUDPSessions; i++ {
		if !acquireUDPSession() {
			t.Fatalf("第 %d 个名额就被拒了（上限 %d）", i+1, maxUDPSessions)
		}
	}
	if acquireUDPSession() {
		releaseUDPSession()
		t.Fatal("超出上限还发得出名额 —— 这条路径的上界形同虚设")
	}
	if got := udpSessionCount.Load(); got != base+maxUDPSessions {
		t.Fatalf("被拒绝的 acquire 不该占名额：期望 %d，实际 %d", base+maxUDPSessions, got)
	}

	// 归还一个名额后必须重新认领得到（否则上界会被慢慢"漏"低）
	releaseUDPSession()
	if !acquireUDPSession() {
		t.Fatal("归还名额后应当能重新认领")
	}
}

// TestUDPAssociateOverCapIsRefusedFailClosed 锁死超限时的**协议层**行为：
// 客户端收到明确的失败应答，且一个上游通道都不拨（fail-closed，
// 不能"先接下来再说"）；归还名额后又能正常建会话。
func TestUDPAssociateOverCapIsRefusedFailClosed(t *testing.T) {
	base := holdUDPSessionQuotaForTest(t)
	CloseConnections(nil)

	// 先把名额占满（只动计数器、不开真套接字 —— 测的是"满了就拒绝"这条逻辑，
	// 真开 256 条会话属于慢且脆的写法）
	held := int64(0)
	for i := int64(0); i < maxUDPSessions; i++ {
		if !acquireUDPSession() {
			t.Fatalf("第 %d 个名额就被拒了（上限 %d）", i+1, maxUDPSessions)
		}
		held++
	}

	ch := newFakeUDPChannel()
	up := &udpAssocUpstream{ch: ch}
	h := newUDPAssocHandler(up, time.Minute)
	addr := serveUDPTestClient(t, h)

	rep, uc := associateUDP(t, addr)
	if rep == repSuccess {
		t.Fatal("名额已满时不该再接受新的 UDP ASSOCIATE")
	}
	if uc != nil {
		t.Fatal("被拒绝时不该给出中继地址")
	}
	if got := up.dials.Load(); got != 0 {
		t.Fatalf("被拒绝时不该去拨上游 UDP 通道，实际拨了 %d 次", got)
	}

	// 归还一个名额：新的会话必须能建起来（上界不是死的）
	releaseUDPSession()
	held--
	rep2, uc2 := associateUDP(t, addr)
	if rep2 != repSuccess {
		t.Fatalf("归还名额后应当能建会话，实际应答码 0x%02x（%s）", rep2, ReplyText(rep2))
	}
	_ = uc2.tcp.Close()
	waitActiveConns(t, 0, 3*time.Second)
	// 账本 = base + 还手工占着的名额（新会话自己的名额已经归还）
	waitUDPSessionQuota(t, base+held, 3*time.Second)
}

// TestUDPAssociateIdleSessionIsRecycled 锁死空闲回收：控制连接挂着不动、
// 一条数据报都没有的会话必须被回收 —— 这正是"幽灵会话"的另一半（没有上界时
// 它会一直占着本地 UDP 口与上游通道）。同时反向确认"有流量就不会被误杀"。
//
// 【为什么阈值取 600ms 这种小值】Windows 定时器精度约 15ms；这里只需验证
// "到点会被回收 / 没到点不会被回收"，不必等生产默认的 300s。空闲回收用的就是
// 与 TCP 中继同一个 h.Idle（来源 Settings.IdleTimeoutSec），所以这里给出小值即可。
func TestUDPAssociateIdleSessionIsRecycled(t *testing.T) {
	base := holdUDPSessionQuotaForTest(t)
	CloseConnections(nil)

	const idle = 600 * time.Millisecond

	rec := &connEventRecorder{}
	SetConnSink(rec.sink)
	t.Cleanup(func() { SetConnSink(nil); CloseConnections(nil) })

	ch := newFakeUDPChannel()
	h := newUDPAssocHandler(&udpAssocUpstream{ch: ch}, idle)

	rep, uc := associateUDP(t, serveUDPTestClient(t, h))
	if rep != repSuccess {
		t.Fatalf("UDP ASSOCIATE 应当成功，实际应答码 0x%02x（%s）", rep, ReplyText(rep))
	}
	open := rec.waitOpenAny(t, "udp", 2*time.Second)

	// 反向：持续有数据报的会话，跨过 3 个空闲阈值也不许被回收
	// （每次都等上游真的收到，顺便证明转发在持续进行）
	deadline := time.Now().Add(3 * idle)
	for time.Now().Before(deadline) {
		uc.send(t, "1.1.1.1", 53, "keepalive")
		_ = ch.waitSent(t, 2*time.Second)
		time.Sleep(idle / 3)
	}
	if n := rec.countClose(open.ID); n != 0 {
		t.Fatalf("一直有数据报的 UDP 会话不该被回收，却收到了 close 事件：%+v", rec.snapshot())
	}
	if n := ActiveConnCount(); n != 1 {
		t.Fatalf("有流量的会话应当还在活动连接表里，实际 %d 条", n)
	}
	if ch.isClosed() {
		t.Fatal("有流量的会话不该把上游通道关掉")
	}

	// 停手：空闲超过阈值后必须被回收，并补上 close 事件
	closed := rec.waitClose(t, open.ID, 3*time.Second)
	if closed.Protocol != "udp" {
		t.Fatalf("close 事件应当带上协议：%+v", closed)
	}
	ch.waitClosed(t, 2*time.Second)
	waitActiveConns(t, 0, 2*time.Second)
	waitUDPSessionQuota(t, base, 2*time.Second)
}
