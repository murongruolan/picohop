package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"chainproxy/internal/config"
)

// Upstream 是"第二跳/出口"：负责把客户端要访问的目标地址，变成一条真正连到目标的连接。
//
// 对 socks5 / http 上游，实现方式是经典的"套娃"：
//
//	conn := front.DialTCP(upstream.ServerAddr())   // ← 已经在前置隧道内部
//	upstream.握手(conn, target)                     // ← 第二次握手
//
// 对 wireguard 上游，隧道在 Start() 时就已建立，front 仅用于外层 UDP；
// Dial() 直接从用户态网络栈出栈，不存在第二次握手。
type Upstream interface {
	// Start 建立底层通道。socks5/http 上游这里是空操作；wireguard 上游在此完成握手。
	Start(ctx context.Context, front Front) error
	// Stop 释放资源。
	Stop() error
	// Dial 建立到 target（"host:port"）的连接，连接已就绪，可直接读写应用数据。
	Dial(ctx context.Context, front Front, target string) (net.Conn, error)
	// DialUDP 建立 UDP 通道。socks5 上游可用；http 上游返回 ErrUDPUnsupported。
	DialUDP(ctx context.Context, front Front) (UDPChannel, error)
	// SupportsUDP 说明该上游能否转发 UDP。
	SupportsUDP() bool
	// Describe 返回一行可读描述。
	Describe() string
	// DialTimeout 返回单条链路建立的超时。
	DialTimeout() time.Duration
}

// NewUpstream 按配置创建住宅上游实现。
//
// wgFactory 由 internal/wg 包在启动时注入，避免 proxy 包反向依赖 wg 包
// （也方便在未启用 WireGuard 的构建里完全不引入相关依赖）。
var wgFactory func(config.Upstream, Front) (Upstream, error)

// RegisterWireGuardFactory 让 internal/wg 在 init 时注册构造器。
func RegisterWireGuardFactory(f func(config.Upstream, Front) (Upstream, error)) {
	wgFactory = f
}

// NewUpstream 创建指定类型的上游。
func NewUpstream(u config.Upstream) (Upstream, error) {
	switch u.Type {
	case config.UpstreamTypeHTTP:
		return &httpUpstream{u: u}, nil
	case config.UpstreamTypeWireGuard:
		if wgFactory == nil {
			return nil, errors.New("当前构建未包含 WireGuard 支持")
		}
		// 构造器需要 Front，延迟到 Start 时注入；这里只做配置校验。
		return &lazyWireGuardUpstream{u: u}, nil
	case config.UpstreamTypeSocks5:
		return &socks5Upstream{u: u}, nil
	default:
		return nil, fmt.Errorf("不支持的上游类型 %q", u.Type)
	}
}

// ------------------------------------------------------------------
// socks5 上游
// ------------------------------------------------------------------

type socks5Upstream struct {
	u config.Upstream
}

func (s *socks5Upstream) Describe() string    { return s.u.Describe() }
func (s *socks5Upstream) Start(context.Context, Front) error { return nil }
func (s *socks5Upstream) Stop() error                        { return nil }
func (s *socks5Upstream) SupportsUDP() bool                  { return true }

func (s *socks5Upstream) DialTimeout() time.Duration {
	return time.Duration(s.u.Timeout()) * time.Millisecond
}

func (s *socks5Upstream) Dial(ctx context.Context, front Front, target string) (net.Conn, error) {
	conn, err := front.DialTCP(ctx, s.u.ServerAddress())
	if err != nil {
		return nil, err
	}
	host, port, err := SplitHostPort(target, 443)
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(s.DialTimeout()))
	if err := socks5Connect(conn, s.u.Username, s.u.Password, host, port); err != nil {
		conn.Close()
		return nil, fmt.Errorf("住宅 SOCKS5 握手失败（连 %s）: %w", target, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func (s *socks5Upstream) DialUDP(ctx context.Context, front Front) (UDPChannel, error) {
	ctrl, err := front.DialTCP(ctx, s.u.ServerAddress())
	if err != nil {
		return nil, err
	}
	_ = ctrl.SetDeadline(time.Now().Add(s.DialTimeout()))
	host, port, err := socks5UDPAssociate(ctrl, s.u.Username, s.u.Password)
	if err != nil {
		ctrl.Close()
		return nil, fmt.Errorf("住宅 SOCKS5 UDP ASSOCIATE 失败: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = s.u.ServerHost
	}
	_ = ctrl.SetDeadline(time.Time{})

	udpAddr := &net.UDPAddr{IP: net.ParseIP(host), Port: port}
	if udpAddr.IP == nil {
		udpAddr.IP = net.IPv4zero
	}
	relay, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		ctrl.Close()
		return nil, fmt.Errorf("连接住宅 UDP relay %s 失败: %w", udpAddr, err)
	}
	return newSocks5UDPChannel(ctrl, relay), nil
}

// ------------------------------------------------------------------
// http 上游
// ------------------------------------------------------------------

type httpUpstream struct {
	u config.Upstream
}

func (h *httpUpstream) Describe() string    { return h.u.Describe() }
func (h *httpUpstream) Start(context.Context, Front) error { return nil }
func (h *httpUpstream) Stop() error                        { return nil }
func (h *httpUpstream) SupportsUDP() bool                  { return false }

func (h *httpUpstream) DialTimeout() time.Duration {
	return time.Duration(h.u.Timeout()) * time.Millisecond
}

func (h *httpUpstream) Dial(ctx context.Context, front Front, target string) (net.Conn, error) {
	conn, err := front.DialTCP(ctx, h.u.ServerAddress())
	if err != nil {
		return nil, err
	}
	host, port, err := SplitHostPort(target, 443)
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(h.DialTimeout()))
	if err := httpConnect(conn, host, port, h.u.Username, h.u.Password); err != nil {
		conn.Close()
		return nil, fmt.Errorf("住宅 HTTP CONNECT 握手失败（连 %s）: %w", target, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func (h *httpUpstream) DialUDP(context.Context, Front) (UDPChannel, error) {
	return nil, fmt.Errorf("HTTP 住宅代理不支持 UDP: %w", ErrUDPUnsupported)
}

// ------------------------------------------------------------------
// WireGuard 上游的延迟构造包装
// ------------------------------------------------------------------

// lazyWireGuardUpstream 在 Start() 时用真实的 Front 创建底层 wireguard 上游，
// 这样 Front 实例（可能因配置变化被重建）始终是最新的。
type lazyWireGuardUpstream struct {
	u     config.Upstream
	inner Upstream
}

func (l *lazyWireGuardUpstream) Describe() string { return l.u.Describe() }

func (l *lazyWireGuardUpstream) Start(ctx context.Context, front Front) error {
	if l.inner == nil {
		if wgFactory == nil {
			return errors.New("当前构建未包含 WireGuard 支持")
		}
		inner, err := wgFactory(l.u, front)
		if err != nil {
			return err
		}
		l.inner = inner
	}
	return l.inner.Start(ctx, front)
}

func (l *lazyWireGuardUpstream) Stop() error {
	if l.inner != nil {
		return l.inner.Stop()
	}
	return nil
}

func (l *lazyWireGuardUpstream) Dial(ctx context.Context, front Front, target string) (net.Conn, error) {
	if l.inner == nil {
		return nil, errors.New("WireGuard 隧道尚未启动")
	}
	return l.inner.Dial(ctx, front, target)
}

func (l *lazyWireGuardUpstream) DialUDP(ctx context.Context, front Front) (UDPChannel, error) {
	if l.inner == nil {
		return nil, errors.New("WireGuard 隧道尚未启动")
	}
	return l.inner.DialUDP(ctx, front)
}

func (l *lazyWireGuardUpstream) SupportsUDP() bool { return l.inner != nil && l.inner.SupportsUDP() }

func (l *lazyWireGuardUpstream) DialTimeout() time.Duration {
	return time.Duration(l.u.Timeout()) * time.Millisecond
}