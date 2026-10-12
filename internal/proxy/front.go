package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/murongruolan/picohop/internal/config"
)

// UDPChannel 是一条"已经能收发 UDP 数据报"的逻辑通道。
// 实现方负责把数据报封装成各自的传输格式（SOCKS5 UDP 头等）。
// ErrUDPChannelClosed 表示本次会话被上层主动结束（不是通道坏了）。
// 收包协程看到它应当安静退出。
var ErrUDPChannelClosed = errors.New("UDP 通道会话已结束")

type UDPChannel interface {
	// SendToUDP 发送一个数据报到指定目标。
	SendToUDP(payload []byte, host string, port int) error
	// ReceiveFromUDP 阻塞等待下一个数据报，把来源地址填入 srcHost/srcPort。
	//
	// quit 被关闭时立刻返回 ErrUDPChannelClosed。
	//
	// 【为什么必须有 quit】
	// 调用方（WireGuard 的收包协程）需要一个"能被叫醒"的阻塞读：
	// wireguard-go 在 BindUpdate / Close 时会先调 bind.Close()、再
	// **等待收包协程退出**。如果这里既不看 quit 又关不掉底层通道，
	// 收包协程就永远醒不过来，device.Close() 也永远等不到 —— 直接死锁
	// （现象是 fatal error: all goroutines are asleep - deadlock!，
	//   以及界面上"点停止卡死"）。
	ReceiveFromUDP(quit <-chan struct{}, buf []byte) (n int, srcHost string, srcPort int, err error)
	// Close 释放通道（控制连接 + UDP socket）。
	Close() error
}

// Front 是"第一跳"：负责把一条连接经由前置代理送到某个地址。
//
// DialTCP 返回的连接，其对端就是 addr 对应的主机 —— 但这条 TCP 连接
// 实际是与前置代理建立的，之后的所有字节都在前置隧道内部传输。
type Front interface {
	DialTCP(ctx context.Context, addr string) (net.Conn, error)
	// DialUDP 与 DialTCP 正交：给 WireGuard 上游提供外层 UDP 通道。
	DialUDP(ctx context.Context) (UDPChannel, error)
	Describe() string
	Close() error
}

// NewFront 按配置创建前置代理实现。
func NewFront(fc config.Front) Front {
	switch fc.Type {
	case config.FrontTypeHTTP:
		return &httpFront{fc: fc}
	case config.FrontTypeDirect:
		return &directFront{fc: fc}
	default:
		return &socks5Front{fc: fc}
	}
}

// ------------------------------------------------------------------
// socks5 前置
// ------------------------------------------------------------------

type socks5Front struct {
	fc config.Front
}

func (f *socks5Front) Describe() string {
	s := fmt.Sprintf("SOCKS5 %s", f.fc.Address())
	if f.fc.Username != "" {
		s += fmt.Sprintf(" [%s]", f.fc.Username)
	}
	return s
}

func (f *socks5Front) Close() error { return nil }

func (f *socks5Front) DialTCP(ctx context.Context, addr string) (net.Conn, error) {
	host, port, err := SplitHostPort(addr, 1080)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(f.fc.Timeout()) * time.Millisecond

	d := &net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", f.fc.Address())
	if err != nil {
		return nil, fmt.Errorf("连接前置代理 %s 失败: %w", f.fc.Address(), err)
	}

	deadline := time.Now().Add(timeout)
	_ = conn.SetDeadline(deadline)
	if err := socks5Connect(conn, f.fc.Username, f.fc.Password, host, port); err != nil {
		conn.Close()
		return nil, fmt.Errorf("经前置代理请求连接 %s 失败: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func (f *socks5Front) DialUDP(ctx context.Context) (UDPChannel, error) {
	timeout := time.Duration(f.fc.Timeout()) * time.Millisecond
	d := &net.Dialer{Timeout: timeout}
	ctrl, err := d.DialContext(ctx, "tcp", f.fc.Address())
	if err != nil {
		return nil, fmt.Errorf("连接前置代理 %s 失败: %w", f.fc.Address(), err)
	}
	_ = ctrl.SetDeadline(time.Now().Add(timeout))
	relayHost, relayPort, err := socks5UDPAssociate(ctrl, f.fc.Username, f.fc.Password)
	if err != nil {
		ctrl.Close()
		return nil, fmt.Errorf("前置代理不支持 UDP 转发（UDP ASSOCIATE 失败）: %w", err)
	}
	_ = ctrl.SetDeadline(time.Time{})

	// relay 主机名可能是域名（服务端回了 ATYP=0x03，或它回 0.0.0.0 时退回
	// 配置里的前置主机名）。这里必须真的解析成 IP；解析不了就硬报错，
	// 绝不许像以前那样把域名静默换成 0.0.0.0（详见 dialSocks5UDPRelay 的注释）。
	relay, err := dialSocks5UDPRelay(ctx, relayHost, relayPort, f.fc.Host, timeout)
	if err != nil {
		ctrl.Close()
		return nil, err
	}
	return newSocks5UDPChannel(ctrl, relay), nil
}

// ------------------------------------------------------------------
// http 前置
// ------------------------------------------------------------------

type httpFront struct {
	fc config.Front
}

func (f *httpFront) Describe() string {
	s := fmt.Sprintf("HTTP %s", f.fc.Address())
	if f.fc.Username != "" {
		s += fmt.Sprintf(" [%s]", f.fc.Username)
	}
	return s
}

func (f *httpFront) Close() error { return nil }

func (f *httpFront) DialTCP(ctx context.Context, addr string) (net.Conn, error) {
	host, port, err := SplitHostPort(addr, 80)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(f.fc.Timeout()) * time.Millisecond

	d := &net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", f.fc.Address())
	if err != nil {
		return nil, fmt.Errorf("连接前置代理 %s 失败: %w", f.fc.Address(), err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := httpConnect(conn, host, port, f.fc.Username, f.fc.Password); err != nil {
		conn.Close()
		return nil, fmt.Errorf("经前置代理请求连接 %s 失败: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func (f *httpFront) DialUDP(context.Context) (UDPChannel, error) {
	return nil, fmt.Errorf("HTTP 前置代理不支持 UDP（WireGuard 上游需要 SOCKS5 前置）: %w", ErrUDPUnsupported)
}

// ------------------------------------------------------------------
// 直连（不使用前置）
// ------------------------------------------------------------------

type directFront struct {
	fc config.Front
}

func (f *directFront) Describe() string { return "直连（不使用前置代理）" }

func (f *directFront) Close() error { return nil }

func (f *directFront) DialTCP(ctx context.Context, addr string) (net.Conn, error) {
	timeout := time.Duration(f.fc.Timeout()) * time.Millisecond
	d := &net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("直连 %s 失败: %w", addr, err)
	}
	return conn, nil
}

func (f *directFront) DialUDP(context.Context) (UDPChannel, error) {
	return &plainUDPChannel{pkts: make(chan udpPacket, 256), done: make(chan struct{})}, nil
}

// plainUDPChannel 是不经前置代理、直接收发 UDP 的通道。
// 主要给"WireGuard 直连"与自动化测试使用。
//
// 【为什么按地址族分开建 socket】
// 早先这里只建一个 `net.ListenUDP("udp", 0.0.0.0:0)`。实测在 Windows 上
// Go 会把它建成**双栈的 [::] 套接字**，而从这个套接字往 127.0.0.1 发 IPv4 包
// **不会报错、也不会送达**（err=nil，对端收不到），
// 表现为 WireGuard "一直在发握手包、永远收不到回包"。
//
// 现在改成按目标地址族惰性创建 udp4 / udp6 套接字：
// IPv4 目标用纯粹的 AF_INET 套接字，IPv6 目标用 AF_INET6，
// 不再依赖双栈映射行为。
type plainUDPChannel struct {
	mu       sync.Mutex
	pc4      *net.UDPConn
	pc6      *net.UDPConn
	pkts     chan udpPacket
	done     chan struct{}
	closed   bool
	doneOnce sync.Once
}

// signalDone 关闭"结束信号"，用 once 保护。
// 收协程和 Close() 都会走到这里，各自的 select/default 判断是有竞态的，
// 直接 close 会 panic（"close of closed channel"）。
func (c *plainUDPChannel) signalDone() { c.doneOnce.Do(func() { close(c.done) }) }

// sockFor 返回（必要时创建）适合目标地址的套接字，并保证它有读协程在跑。
func (c *plainUDPChannel) sockFor(ip net.IP) (*net.UDPConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, net.ErrClosed
	}
	if ip.To4() != nil {
		if c.pc4 == nil {
			pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
			if err != nil {
				return nil, err
			}
			c.pc4 = pc
			go c.readLoop(pc)
		}
		return c.pc4, nil
	}
	if c.pc6 == nil {
		pc, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6unspecified, Port: 0})
		if err != nil {
			return nil, err
		}
		c.pc6 = pc
		go c.readLoop(pc)
	}
	return c.pc6, nil
}

func (c *plainUDPChannel) readLoop(pc *net.UDPConn) {
	buf := make([]byte, 64*1024)
	for {
		n, from, err := pc.ReadFromUDP(buf)
		if err != nil {
			c.signalDone()
			return
		}
		cp := make([]byte, n)
		copy(cp, buf[:n])
		p := udpPacket{payload: cp, host: from.IP.String(), port: from.Port}
		select {
		case c.pkts <- p:
		case <-c.done:
			return
		default:
			// 队列满：丢最旧的，保证实时性
			select {
			case <-c.pkts:
			default:
			}
			select {
			case c.pkts <- p:
			case <-c.done:
				return
			}
		}
	}
}

func (c *plainUDPChannel) SendToUDP(payload []byte, host string, port int) error {
	ip := net.ParseIP(host)
	if ip == nil {
		ip = net.IPv4zero
	}
	pc, err := c.sockFor(ip)
	if err != nil {
		return err
	}
	addr := &net.UDPAddr{IP: ip, Port: port}
	n, werr := pc.WriteToUDP(payload, addr)
	if os.Getenv("PICOHOP_UDP_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[udp-send] local=%v -> %v  %d 字节 err=%v\n",
			pc.LocalAddr(), addr, n, werr)
	}
	return werr
}

func (c *plainUDPChannel) ReceiveFromUDP(quit <-chan struct{}, buf []byte) (int, string, int, error) {
	select {
	case p := <-c.pkts:
		n := copy(buf, p.payload)
		return n, p.host, p.port, nil
	case <-quit:
		return 0, "", 0, ErrUDPChannelClosed
	case <-c.done:
		return 0, "", 0, net.ErrClosed
	}
}

func (c *plainUDPChannel) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	if c.pc4 != nil {
		_ = c.pc4.Close()
	}
	if c.pc6 != nil {
		_ = c.pc6.Close()
	}
	c.mu.Unlock()
	c.signalDone()
	return nil
}

// ------------------------------------------------------------------
// SOCKS5 UDP 通道
// ------------------------------------------------------------------

type udpPacket struct {
	payload []byte
	host    string
	port    int
}

type socks5UDPChannel struct {
	ctrl  net.Conn
	relay *net.UDPConn

	pkts     chan udpPacket
	done     chan struct{}
	once     sync.Once
	doneOnce sync.Once
}

// signalDone 关闭"结束信号"（once 保护，避免收协程与 Close 抢着关）。
func (c *socks5UDPChannel) signalDone() { c.doneOnce.Do(func() { close(c.done) }) }

func newSocks5UDPChannel(ctrl net.Conn, relay *net.UDPConn) *socks5UDPChannel {
	c := &socks5UDPChannel{
		ctrl:  ctrl,
		relay: relay,
		pkts:  make(chan udpPacket, 128),
		done:  make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *socks5UDPChannel) readLoop() {
	buf := make([]byte, 64*1024)
	for {
		n, err := c.relay.Read(buf)
		if err != nil {
			c.signalDone()
			return
		}
		host, port, payload, perr := parseUDPHeader(buf[:n])
		if perr != nil || len(payload) == 0 {
			continue // 丢弃异常/空数据报
		}
		cp := make([]byte, len(payload))
		copy(cp, payload)
		select {
		case c.pkts <- udpPacket{payload: cp, host: host, port: port}:
		case <-c.done:
			return
		default:
			// 队列满：丢弃最旧的一条，保证实时性（DNS/QUIC 对陈旧包不敏感）
			select {
			case <-c.pkts:
			default:
			}
			select {
			case c.pkts <- udpPacket{payload: cp, host: host, port: port}:
			case <-c.done:
				return
			}
		}
	}
}

func (c *socks5UDPChannel) SendToUDP(payload []byte, host string, port int) error {
	frame, err := appendUDPHeader(make([]byte, 0, len(payload)+262), host, port, payload)
	if err != nil {
		return err
	}
	_, err = c.relay.Write(frame)
	return err
}

func (c *socks5UDPChannel) ReceiveFromUDP(quit <-chan struct{}, buf []byte) (int, string, int, error) {
	select {
	case p := <-c.pkts:
		n := copy(buf, p.payload)
		return n, p.host, p.port, nil
	case <-quit:
		return 0, "", 0, ErrUDPChannelClosed
	case <-c.done:
		return 0, "", 0, net.ErrClosed
	}
}

func (c *socks5UDPChannel) Close() error {
	c.once.Do(func() {
		c.signalDone()
		_ = c.relay.Close()
		_ = c.ctrl.Close()
	})
	return nil
}

// describeAddrUDP 是给日志用的小工具。
func describeAddrUDP(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
