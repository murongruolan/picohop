package engine

// mihomo 的 UDP 通道包装（按目标建通道 —— 第 10 轮实测否掉"一次建一个"之后的正确设计）。
//
// 【为什么要按目标建】第 10 轮的实测结论：mihomo 的 pc.ResolveUDP(ctx, metadata)
// 需要一个具体目标才能解析出 IP，而我们的接口 DialUDP(ctx, front) **不带目标**
// （目标按每个数据报给：SendToUDP(payload, host, port)）。
// 传空 metadata 会直接报 "can't resolve ip: couldn't find ip"。
// 所以这里维护 map[host:port] → 通道，第一次发往某目标时才建。
//
// 【收发设计：每个目标一个常驻读协程，汇入一条 fan-in 队列】
//
// 这里换过一次实现，原因是实测到的代价，不是"看着更优雅"：
//
//	旧做法＝单个收包协程**顺序轮询**所有目标，每个目标给 100ms 读超时。
//	后果（都是结构性的，不是调参能救的）：
//	  1. 延迟 ∝ 目标序号：第 i 个目标的包要等前面 i-1 个各自超时，最坏 100ms×(i-1)；
//	  2. 每轮都从 keys[0] 重开，于是"第一个目标一直有包"时，后面的目标会被**饿死**；
//	  3. 每个目标的 100ms 读超时在 mihomo 里会被包一层 pipe 式 deadline
//	     （common/net/deadline/packet.go），于是**每个目标**在内核里留一个常驻
//	     读协程 + 64KB 缓冲；目标一多就是几十上百个常驻协程在空转；
//	     而且每次设超时都会新分配一个 timer；
//	  4. quit 每轮才查一次，关停最坏要等 100ms×N。
//
// 新做法＝每个目标一个常驻读协程 + 一条容量固定的 fan-in 队列，ReceiveFromUDP 从队列
// 阻塞取。这也是 mihomo 自己在 tunnel 里用的形状（一个会话一个读循环 + 非阻塞 chan），
// 以及本项目 frontPacketConn / proxy 前端已经在用的形状。
//
// 【接口上的硬事实，改之前必须知道】C.PacketConn 的读**没有 ctx、没有 quit**，
// 只有两件事能叫醒一个阻塞中的读：**SetReadDeadline** 与 **Close**。所以：
//   - 取消/关停只能靠 Close()（发个信号"希望它醒"是没用的）；
//   - **只有读协程自己可以调 SetReadDeadline**：mihomo 的 deadline 包装层在
//     "已有阻塞读在飞"时被别的协程调 SetReadDeadline，会把 pipe 机制**永久关掉**
//     （common/net/deadline/packet.go 的 disablePipe 分支），之后超时就不再可靠。

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	C "github.com/metacubex/mihomo/constant"

	"github.com/murongruolan/picohop/internal/proxy"
)

const (
	// udpTargetIdle 是"某个目标多久没有数据报进出就回收它的通道"。
	//
	// 【为什么必须有】一条长命会话可能发往成百上千个目标（扫端口、P2P、DNS 洪泛），
	// 而目标表只增不减时，每个目标都会留下一个常驻读协程 + 一个 64KB 缓冲。
	// 60 秒与 mihomo 自己的 udpTimeout 一致，用户不需要理解第二个旋钮。
	udpTargetIdle = 60 * time.Second
	// udpFanInQueue 是汇入队列的容量。满了就丢包（见 readLoop）：UDP 本来就可能丢，
	// 而读协程一旦阻塞在队列上，底层 socket 会反压，一条慢会话能拖住所有目标。
	udpFanInQueue = 128
	// udpReadBufSize 是每个目标读缓冲的大小（单颗 UDP 数据报的理论上限）。
	udpReadBufSize = 64 * 1024
)

// udpDatagram 是读协程交给收包方的一颗数据报。
type udpDatagram struct {
	payload []byte
	host    string
	port    int
}

// udpTarget 是一个目标的通道：底下的 PacketConn + 它自己的读协程要用的状态。
type udpTarget struct {
	key  string // "host:port"，摘表时要用
	pc   C.PacketConn
	dest *net.UDPAddr // 解析后的目标地址：复用 ResolveUDP 的结果，别再每包解析一次

	// lastAct 是最近一次"发出或收到"的时间（Unix 毫秒），空闲回收用它判断。
	lastAct atomic.Int64
	// dead 标记通道已经关掉：入口据此拒绝复用（否则会拿到一个已关闭的 conn 去写，
	// 表现为静默丢包）。
	dead atomic.Bool

	once sync.Once
}

// kill 关掉这条通道（幂等）。Close 是唯一能叫醒阻塞读的手段，见文件头。
func (t *udpTarget) kill() error {
	var err error
	t.once.Do(func() {
		t.dead.Store(true)
		err = t.pc.Close()
	})
	return err
}

type mihomoUDPChannel struct {
	up *MihomoUpstream
	// ctx 是**会话生命周期**的 context，不是那次 DialUDP 的短超时（见 newMihomoUDPChannel）。
	ctx context.Context

	mu      sync.Mutex
	targets map[string]*udpTarget
	closed  bool // 受 mu 保护：建通道时用它做最后一次确认，避免关停后还建出通道

	pkts      chan udpDatagram
	done      chan struct{}
	closeOnce sync.Once
}

func newMihomoUDPChannel(up *MihomoUpstream, ctx context.Context) *mihomoUDPChannel {
	// 【为什么必须 WithoutCancel】调用方是这样用的（internal/proxy/handler.go）：
	//
	//	ctx, cancel := context.WithTimeout(context.Background(), h.Upstream.DialTimeout())
	//	up, err := h.Upstream.DialUDP(ctx, h.Front)
	//	cancel()   // ← 返回后立刻取消
	//
	// 而我们的通道是**懒建**的（第一次发往某目标时才 ListenPacketContext/ResolveUDP）。
	// 直接拿这个 ctx 用，第一次发往域名目标就会拿到 context canceled ——
	// 症状是"UDP 全都不通"，而字面量 IP 目标会侥幸绕过（metadata 已解析、ResolveUDP 不用 ctx）。
	// 保留 ctx 上的值、去掉它的取消与超时：通道的真实寿命由 Close() 决定，
	// 单次建连的超时由 targetFor 另包一层。
	return &mihomoUDPChannel{
		up:      up,
		ctx:     context.WithoutCancel(ctx),
		targets: map[string]*udpTarget{},
		pkts:    make(chan udpDatagram, udpFanInQueue),
		done:    make(chan struct{}),
	}
}

// targetFor 取（必要时建）某个目标的通道。
//
// 【为什么建连不能持锁】旧实现整段（建连 + DNS 解析）都在 c.mu 里做，而收包路径也要拿
// 同一把锁 —— 一个新目标建连慢（DNS、QUIC 握手）就会把整条收包路径卡住。
// 现在只在"查表 / 入表"两个瞬间持锁，网络 I/O 全在锁外。
//
// 【并发重复建连怎么办】用"锁外建连 + 双检入表"，输的那条当场关掉。
// 多发一颗包不会建出第二条通道，也不会泄漏。
func (c *mihomoUDPChannel) targetFor(host string, port int) (*udpTarget, error) {
	key := net.JoinHostPort(host, strconv.Itoa(port))

	c.mu.Lock()
	if t, ok := c.targets[key]; ok {
		c.mu.Unlock()
		if t.dead.Load() {
			return nil, fmt.Errorf("UDP 目标 %s 的通道已关闭", key)
		}
		return t, nil
	}
	c.mu.Unlock()

	out, err := c.up.outbound()
	if err != nil {
		return nil, err
	}
	meta := &C.Metadata{NetWork: C.UDP, Host: host, DstPort: uint16(port)}
	if addr, perr := netip.ParseAddr(host); perr == nil {
		meta.DstIP = addr
	}

	// 建连超时只包住"建连 + 解析"这一步；通道寿命不绑在它上面。
	ectx, cancel := context.WithTimeout(c.ctx, c.up.DialTimeout())
	defer cancel()

	pc, err := out.ListenPacketContext(ectx, meta)
	if err != nil {
		// 【不包装成自己的话术】不支持 UDP 的出站由 mihomo 的 Base 返回 C.ErrNotSupport，
		// 原样带上才看得出"是内核不支持"，而不是我们的 bug。
		return nil, fmt.Errorf("mihomo UDP 通道建立失败（目标 %s）: %w", key, err)
	}
	if err := pc.ResolveUDP(ectx, meta); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("mihomo UDP 目标解析失败（目标 %s）: %w", key, err)
	}
	t := &udpTarget{key: key, pc: pc, dest: targetAddr(meta, host, port)}
	t.lastAct.Store(time.Now().UnixMilli())

	// 双检入表：并发时只留第一条。
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = t.kill()
		return nil, net.ErrClosed
	}
	if cur, ok := c.targets[key]; ok {
		c.mu.Unlock()
		_ = t.kill()
		return cur, nil
	}
	c.targets[key] = t
	c.mu.Unlock()

	go c.readLoop(t)
	return t, nil
}

// readLoop 常驻读一个目标通道，把数据报塞进 fan-in 队列。
//
// 三条出口：① done 被关（Close）；② 读返回非超时错误（被 Close 时是 net.ErrClosed）；
// ③ 空闲超过 udpTargetIdle 自己摘表。绝不空转：没有包时它会一直阻塞在带上截止时间的读上。
func (c *mihomoUDPChannel) readLoop(t *udpTarget) {
	// 自己的读缓冲：不能借用调用方 ReceiveFromUDP 的 buf —— 两者根本不同步，
	// 借用会串包（这正是"每 conn 一个读协程"必须有的代价）。
	buf := make([]byte, udpReadBufSize)
	for {
		select {
		case <-c.done:
			return
		default:
		}
		if t.dead.Load() {
			return
		}

		// 截止时间取"距离空闲回收还剩多久"：既不用为关停单独设短超时（关停靠 Close 叫醒），
		// 又能保证到点回到循环顶部自重判。**只能由本协程设置**，理由见文件头。
		left := udpTargetIdle - time.Duration(time.Now().UnixMilli()-t.lastAct.Load())*time.Millisecond
		if left <= 0 {
			c.dropTarget(t)
			return
		}
		_ = t.pc.SetReadDeadline(time.Now().Add(left))

		n, addr, err := t.pc.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue // 只是到点，回顶部自查空闲/关停
			}
			if errors.Is(err, net.ErrClosed) {
				return // 本端在关（或已被回收）
			}
			// 其余错误：**只摘这个目标**。旧实现把任一目标的读错误当成整条会话失败，
			// 于是一个坏目标能带走同会话里所有正常目标。
			c.dropTarget(t)
			return
		}
		if n <= 0 {
			continue
		}
		t.lastAct.Store(time.Now().UnixMilli())

		host, port := peerHostPort(addr, t)
		// 按实际长度分配，不做池化：单颗 UDP 数据报的大小就是它的真实开销，
		// 而"池 + 归还回调"会把缓冲区所有权分散到两个协程里，得不偿失。
		payload := make([]byte, n)
		copy(payload, buf[:n])
		select {
		case c.pkts <- udpDatagram{payload: payload, host: host, port: port}:
		case <-c.done:
			return
		default:
			// 队列满：丢掉这一颗（与 mihomo 的 `default: packet.Drop()` 同策略）。
			// 这里**绝不能阻塞**：读协程一堵，底层 socket 就反压，一条慢会话会拖住所有目标。
		}
	}
}

// dropTarget 把一个目标从表里摘掉并关掉它的通道（幂等）。
func (c *mihomoUDPChannel) dropTarget(t *udpTarget) {
	c.mu.Lock()
	if cur, ok := c.targets[t.key]; ok && cur == t {
		delete(c.targets, t.key)
	}
	c.mu.Unlock()
	_ = t.kill()
}

// SendToUDP 发送一个数据报到指定目标（首次发往该目标时按需建通道）。
func (c *mihomoUDPChannel) SendToUDP(payload []byte, host string, port int) error {
	select {
	case <-c.done:
		return net.ErrClosed
	default:
	}
	t, err := c.targetFor(host, port)
	if err != nil {
		return err
	}
	if _, err := t.pc.WriteTo(payload, t.dest); err != nil {
		// 写失败只摘这一个目标，让下一个包重建；不再让整条会话跟着失败。
		c.dropTarget(t)
		return err
	}
	t.lastAct.Store(time.Now().UnixMilli())
	return nil
}

// ReceiveFromUDP 阻塞等待下一个数据报，把来源地址填入 srcHost/srcPort。
//
// 与旧实现的区别：不再逐个目标轮询，而是等人喂队列 —— 谁的包先到就先回谁，
// 不需要先等前面的目标各超时 100ms。
func (c *mihomoUDPChannel) ReceiveFromUDP(quit <-chan struct{}, buf []byte) (int, string, int, error) {
	select {
	case <-quit:
		return 0, "", 0, proxy.ErrUDPChannelClosed
	case <-c.done:
		// 本端 Close：对调用方而言与"会话结束"同义（契约里只有 ErrUDPChannelClosed
		// 这一个安静退出的信号，调用方两种都按会话结束处理）。
		return 0, "", 0, proxy.ErrUDPChannelClosed
	case dg := <-c.pkts:
		n := copy(buf, dg.payload)
		return n, dg.host, dg.port, nil
	}
}

// Close 关掉所有通道并唤醒所有读协程。
//
// 【为什么不用等读协程退出】读协程只可能在两个地方停住：阻塞读（被下面的 pc.Close()
// 叫醒）与往队列里塞（带 done 分支，不会卡死）。两者都会自行结束，所以这里不必等待 ——
// 等待反而把"某个出站 Close 没能唤回读"变成一次死锁（本项目在 WireGuard 侧吃过这个亏）。
func (c *mihomoUDPChannel) Close() error {
	var first error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		victims := make([]*udpTarget, 0, len(c.targets))
		for k, t := range c.targets {
			victims = append(victims, t)
			delete(c.targets, k)
		}
		c.mu.Unlock()

		close(c.done)
		for _, t := range victims {
			if err := t.kill(); err != nil && first == nil {
				first = err
			}
		}
	})
	return first
}

// targetAddr 取"真正要写进去的地址"：优先用 ResolveUDP 解析出来的 IP。
//
// 【为什么不再自己解析一遍】旧实现每个数据报都调一次 net.LookupIP：既慢，又在 DNS 抖动时
// 让同一目标时通时不通（同一目标的每个包可能落到不同 IP）。mihomo 自己也是把解析结果
// 缓存成 DNAT 映射（tunnel/connection.go），而不是每包查一次。
func targetAddr(meta *C.Metadata, host string, port int) *net.UDPAddr {
	if meta.DstIP.IsValid() {
		return &net.UDPAddr{IP: meta.DstIP.AsSlice(), Port: port}
	}
	if ip := net.ParseIP(host); ip != nil {
		return &net.UDPAddr{IP: ip, Port: port}
	}
	return &net.UDPAddr{Port: port}
}

// peerHostPort 把"回包的来源地址"拆成 host/port。
//
// 拿不到可用地址时退回目标地址（回包本来就来自它），而不是像旧实现那样返回端口 0 ——
// 端口 0 会让 SOCKS5 的回程头变成一个客户端无法理解的地址。
func peerHostPort(addr net.Addr, t *udpTarget) (string, int) {
	if ua, ok := addr.(*net.UDPAddr); ok && ua != nil {
		return ua.IP.String(), ua.Port
	}
	if addr != nil {
		if h, ps, err := net.SplitHostPort(addr.String()); err == nil {
			if p, perr := strconv.Atoi(ps); perr == nil {
				return h, p
			}
		}
	}
	if t != nil && t.dest != nil {
		return t.dest.IP.String(), t.dest.Port
	}
	return "", 0
}
