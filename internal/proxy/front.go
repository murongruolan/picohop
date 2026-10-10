package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"chainproxy/internal/config"
)

// UDPChannel 是一条"已经能收发 UDP 数据报"的逻辑通道。
// 实现方负责把数据报封装成各自的传输格式（SOCKS5 UDP 头等）。
type UDPChannel interface {
	// SendToUDP 发送一个数据报到指定目标。
	SendToUDP(payload []byte, host string, port int) error
	// ReceiveFromUDP 阻塞等待下一个数据报，把来源地址填入 srcHost/srcPort。
	ReceiveFromUDP(buf []byte) (n int, srcHost string, srcPort int, err error)
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
	// 有些服务端返回 0.0.0.0 作为 relay 地址，此时应使用前置代理所在主机。
	if relayHost == "" || relayHost == "0.0.0.0" || relayHost == "::" {
		relayHost = f.fc.Host
	}
	_ = ctrl.SetDeadline(time.Time{})

	udpAddr := &net.UDPAddr{IP: net.ParseIP(relayHost), Port: relayPort}
	if udpAddr.IP == nil {
		udpAddr.IP = net.IPv4zero
	}
	relay, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		ctrl.Close()
		return nil, fmt.Errorf("连接前置 UDP relay %s 失败: %w", udpAddr, err)
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
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("建立本地 UDP 通道失败: %w", err)
	}
	return &plainUDPChannel{pc: pc}, nil
}

// plainUDPChannel 是不经前置代理、直接收发 UDP 的通道。
// 主要给"WireGuard 直连"和自动化测试使用。
type plainUDPChannel struct {
	pc *net.UDPConn
}

func (c *plainUDPChannel) SendToUDP(payload []byte, host string, port int) error {
	addr := &net.UDPAddr{IP: net.ParseIP(host), Port: port}
	if addr.IP == nil {
		addr.IP = net.IPv4zero
	}
	_, err := c.pc.WriteToUDP(payload, addr)
	return err
}

func (c *plainUDPChannel) ReceiveFromUDP(buf []byte) (int, string, int, error) {
	_ = c.pc.SetReadDeadline(time.Now().Add(20 * time.Second))
	n, from, err := c.pc.ReadFromUDP(buf)
	if err != nil {
		return 0, "", 0, err
	}
	return n, from.IP.String(), from.Port, nil
}

func (c *plainUDPChannel) Close() error { return c.pc.Close() }

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

	pkts chan udpPacket
	done chan struct{}
	once sync.Once
}

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
			select {
			case <-c.done:
			default:
				close(c.done)
			}
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

func (c *socks5UDPChannel) ReceiveFromUDP(buf []byte) (int, string, int, error) {
	select {
	case p, ok := <-c.pkts:
		if !ok {
			return 0, "", 0, errors.New("UDP 通道已关闭")
		}
		n := copy(buf, p.payload)
		return n, p.host, p.port, nil
	case <-c.done:
		return 0, "", 0, errors.New("UDP 通道已关闭")
	}
}

func (c *socks5UDPChannel) Close() error {
	c.once.Do(func() {
		close(c.done)
		_ = c.relay.Close()
		_ = c.ctrl.Close()
	})
	return nil
}

// describeAddrUDP 是给日志用的小工具。
func describeAddrUDP(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
