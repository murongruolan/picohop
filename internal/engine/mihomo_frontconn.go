package engine

// 把 proxy.UDPChannel 桥接成 net.PacketConn。
//
// 【为什么需要这一层】mihomo 的 UDP 出口只认 net.PacketConn（Shadowsocks 的
// `dialer.ListenPacket`、Hysteria2 的 QUIC 都是），而我们的前置给出的是
// proxy.UDPChannel（SendToUDP / ReceiveFromUDP + quit 语义）。这里就是两者的桥。
//
// 【为什么用收包协程 + 队列，而不是在 ReadFrom 里直接阻塞 ReceiveFromUDP】
// net.PacketConn 的 SetReadDeadline 要求"到点就让阻塞中的读返回"，而
// ReceiveFromUDP 只认 quit 通道、没法按任意时刻唤醒。所以这里用一个协程把数据报
// 搬进队列，ReadFrom 用 select 同时等队列、截止时间与唤醒信号 —— 这样截止时间是
// 真的生效的（QUIC 那类调用方就靠它推进自己的计时器）。

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"chainproxy/internal/proxy"
)

// frontDatagram 是队列里的一颗数据报（从前置收到的）。
type frontDatagram struct {
	payload []byte
	addr    net.Addr
}

// frontPacketConn 是 net.PacketConn 的实现，底下是一条经前置的 UDP 通道。
type frontPacketConn struct {
	ch   proxy.UDPChannel
	pkts chan frontDatagram
	quit chan struct{} // Close 时关闭，用来叫醒收包协程与读方
	done chan struct{} // 收包协程退出时关闭（通道坏了或已被关）

	closeOnce sync.Once
	closeErr  error

	mu  sync.Mutex
	rdl time.Time
	wdl time.Time
	// wake 每次改读截止时间都换一个新的并关掉旧的：正在等的 ReadFrom 立刻醒来重算，
	// 否则"把截止时间提前"要等旧的那次到点才生效。
	wake chan struct{}
}

var _ net.PacketConn = (*frontPacketConn)(nil)

func newFrontPacketConn(ch proxy.UDPChannel) *frontPacketConn {
	c := &frontPacketConn{
		ch:   ch,
		pkts: make(chan frontDatagram, 128),
		quit: make(chan struct{}),
		done: make(chan struct{}),
		wake: make(chan struct{}),
	}
	go c.pump()
	return c
}

// pump 把前置通道里的数据报搬进队列，直到通道结束或本端关闭。
func (c *frontPacketConn) pump() {
	defer close(c.done)
	buf := make([]byte, 64*1024)
	for {
		n, host, port, err := c.ch.ReceiveFromUDP(c.quit, buf)
		if err != nil {
			return
		}
		if n <= 0 {
			continue
		}
		dg := frontDatagram{payload: append([]byte(nil), buf[:n]...), addr: udpNetAddr(host, port)}
		select {
		case c.pkts <- dg:
		case <-c.quit:
			return
		}
	}
}

func (c *frontPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		c.mu.Lock()
		rdl, wake := c.rdl, c.wake
		c.mu.Unlock()

		var timer *time.Timer
		var timeout <-chan time.Time
		if !rdl.IsZero() {
			left := time.Until(rdl)
			if left <= 0 {
				return 0, nil, os.ErrDeadlineExceeded
			}
			timer = time.NewTimer(left)
			timeout = timer.C
		}

		select {
		case dg := <-c.pkts:
			stopTimer(timer)
			return copy(p, dg.payload), dg.addr, nil
		case <-timeout:
			return 0, nil, os.ErrDeadlineExceeded
		case <-wake:
			// 截止时间被改了，重算一次再等
			stopTimer(timer)
		case <-c.done:
			stopTimer(timer)
			return 0, nil, net.ErrClosed
		case <-c.quit:
			stopTimer(timer)
			return 0, nil, net.ErrClosed
		}
	}
}

func (c *frontPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	wdl := c.wdl
	c.mu.Unlock()
	if !wdl.IsZero() && time.Now().After(wdl) {
		return 0, os.ErrDeadlineExceeded
	}
	select {
	case <-c.quit:
		return 0, net.ErrClosed
	default:
	}
	host, port, err := splitUDPAddr(addr)
	if err != nil {
		return 0, err
	}
	if err := c.ch.SendToUDP(p, host, port); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *frontPacketConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.quit)
		c.closeErr = c.ch.Close()
	})
	return c.closeErr
}

// LocalAddr 如实说明"本端没有可报的地址"。
//
// 【为什么不猜一个真实地址】经前置的 UDP 通道，本端看到的是前置那边的 relay 地址，
// 而真正决定走向的是每次 WriteTo 的目标。返回零值 UDPAddr 而不是 nil，是因为
// net.PacketConn 的调用方（quic-go 之类）会直接读它。
func (c *frontPacketConn) LocalAddr() net.Addr { return &net.UDPAddr{} }

func (c *frontPacketConn) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}

func (c *frontPacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rdl = t
	old := c.wake
	c.wake = make(chan struct{})
	c.mu.Unlock()
	close(old)
	return nil
}

func (c *frontPacketConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.wdl = t
	c.mu.Unlock()
	return nil
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}

// splitUDPAddr 把 net.Addr 拆成 host/port。
func splitUDPAddr(addr net.Addr) (string, int, error) {
	if addr == nil {
		return "", 0, errors.New("UDP 目标地址为空")
	}
	if ua, ok := addr.(*net.UDPAddr); ok {
		return ua.IP.String(), ua.Port, nil
	}
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "", 0, fmt.Errorf("UDP 目标地址 %q 无法解析: %w", addr.String(), err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("UDP 目标端口 %q 非法: %w", portStr, err)
	}
	return host, port, nil
}

// udpNetAddr 把前置给回的来源地址变成 net.Addr。
//
// 【为什么优先解析成 IP】quic-go 一类调用方会拿 addr 的字符串再解析一次
// （netip.ParseAddrPort），域名会当场失败；所以能解析就解析，解析不了才如实返回
// 一个带域名的自定义地址（SOCKS5 允许 relay 回域名，虽然很少见）。
func udpNetAddr(host string, port int) net.Addr {
	if ip := net.ParseIP(host); ip != nil {
		return &net.UDPAddr{IP: ip, Port: port}
	}
	if ua, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port))); err == nil {
		return ua
	}
	return frontUDPAddr{host: host, port: port}
}

// frontUDPAddr 是最后兜底的地址类型（域名解析不出来时）。
type frontUDPAddr struct {
	host string
	port int
}

func (a frontUDPAddr) Network() string { return "udp" }
func (a frontUDPAddr) String() string  { return net.JoinHostPort(a.host, strconv.Itoa(a.port)) }
