package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"chainproxy/internal/config"
)

// Upstream 是"出口"：负责把客户端要访问的目标地址，变成一条真正连到目标的连接。
//
// 实现方式分两类：
//
//	socks5 / http     经典"套娃"：先在前置隧道里连上出口服务器，再做第二次握手
//	wireguard         隧道在 Start() 时就已建立，Dial() 直接从用户态网络栈出栈
//	vmess/vless/...   由内嵌的 mihomo 引擎负责（internal/engine）
type Upstream interface {
	// Start 建立底层通道。socks5/http 出口这里是空操作；
	// wireguard 在这里完成握手；mihomo 出口在这里把构造好的出站置为激活态。
	Start(ctx context.Context, front Front) error
	// Stop 释放资源。
	Stop() error
	// Dial 建立到 target（"host:port"）的连接，连接已就绪，可直接读写应用数据。
	Dial(ctx context.Context, front Front, target string) (net.Conn, error)
	// DialUDP 建立 UDP 通道。http 出口返回 ErrUDPUnsupported。
	DialUDP(ctx context.Context, front Front) (UDPChannel, error)
	// SupportsUDP 说明该出口能否转发 UDP。
	SupportsUDP() bool
	// Describe 返回一行可读描述。
	Describe() string
	// DialTimeout 返回单条链路建立的超时。
	DialTimeout() time.Duration
}

// 两个工厂函数由外部包在 init 时注入，避免 proxy 包反向依赖它们
// （也方便在不需要某个出口类型的构建里完全不引入相关依赖）。
//
//   - wgFactory     由 internal/wgproxy 注册
//   - mihomoFactory 由 internal/engine 注册（vmess/vless/ss/trojan/hysteria2）
var (
	wgFactory func(config.Node, Front) (Upstream, error)
	// mihomoFactory 是**唯一**的"重协议"内核工厂。这里没有第二个内核了：
	// 回退（mihomo 接不了就换 sing-box）已经随 sing-box 一起删掉 —— 见 NewUpstream。
	mihomoFactory func(config.Node, config.Front, func(string, ...any)) (Upstream, error)
)

// RegisterWireGuardFactory 让 internal/wgproxy 在 init 时注册构造器。
func RegisterWireGuardFactory(f func(config.Node, Front) (Upstream, error)) {
	wgFactory = f
}

// RegisterMihomoFactory 让 internal/engine 在 init 时注册 mihomo 构造器。
//
// 【为什么用注册而不是直接 import】保持依赖单向：proxy 不依赖 engine，
// 于是 proxy 自己的单测不必编译整个 mihomo。
func RegisterMihomoFactory(f func(config.Node, config.Front, func(string, ...any)) (Upstream, error)) {
	mihomoFactory = f
}

// NewUpstream 按节点配置创建出口实现。
//
// frontCfg 是已经解析好的前置（直连 / 全局代理 / 另一个节点的本地端口）。
// 原生实现（socks5/http/wireguard）在 Dial 时使用运行期的 Front 实例；
// mihomo 实现则需要在**构造时**就拿到前置：它会把前置装进出站自己的拨号器里
// （见 internal/engine/mihomo_dialer.go），这也是"配了前置的节点真的经前置出去"的机制。
func NewUpstream(n config.Node, frontCfg config.Front, logf func(string, ...any)) (Upstream, error) {
	switch n.Protocol {
	case config.ProtocolHTTP:
		return &httpUpstream{n: n}, nil

	case config.ProtocolSOCKS5:
		return &socks5Upstream{n: n}, nil

	case config.ProtocolWireGuard:
		if wgFactory == nil {
			return nil, errors.New("当前构建未包含 WireGuard 支持")
		}
		// WireGuard 需要一个 Front 来建立外层 UDP 通道，
		// 这里按前置配置造一个（Front 本身无状态，会话状态在返回的连接里）。
		return &lazyWireGuardUpstream{n: n, front: NewFront(frontCfg)}, nil

	case config.ProtocolVMess, config.ProtocolVLESS,
		config.ProtocolShadowsocks, config.ProtocolTrojan, config.ProtocolHysteria2:
		// 【没有回退，也不许"假装能跑"】mihomo 是唯一的内核（sing-box 已摘除）。
		// 它接不了这个节点就直接把错误交出去，让上层把这个节点标成失败 ——
		// 绝不允许退化成"直连"：那会绕开前置把本机 IP 暴露出去，
		// 而"配了前置却从本机 IP 出去"正是本项目最不能接受的失败方式。
		if mihomoFactory == nil {
			return nil, fmt.Errorf("当前构建未包含 %s 支持", config.ProtocolLabel(n.Protocol))
		}
		return mihomoFactory(n, frontCfg, logf)

	default:
		return nil, fmt.Errorf("不支持的协议 %q", n.Protocol)
	}
}

// ------------------------------------------------------------------
// socks5 出口
// ------------------------------------------------------------------

type socks5Upstream struct {
	n config.Node
}

func (s *socks5Upstream) Describe() string                   { return s.n.Describe() }
func (s *socks5Upstream) Start(context.Context, Front) error { return nil }
func (s *socks5Upstream) Stop() error                        { return nil }
func (s *socks5Upstream) SupportsUDP() bool                  { return true }

func (s *socks5Upstream) DialTimeout() time.Duration {
	return time.Duration(s.n.Timeout()) * time.Millisecond
}

func (s *socks5Upstream) Dial(ctx context.Context, front Front, target string) (net.Conn, error) {
	conn, err := front.DialTCP(ctx, s.n.Address())
	if err != nil {
		return nil, err
	}
	host, port, err := SplitHostPort(target, 443)
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(s.DialTimeout()))
	if err := socks5Connect(conn, s.n.Username, s.n.Password, host, port); err != nil {
		conn.Close()
		return nil, fmt.Errorf("SOCKS5 出口握手失败（连 %s）: %w", target, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func (s *socks5Upstream) DialUDP(ctx context.Context, front Front) (UDPChannel, error) {
	ctrl, err := front.DialTCP(ctx, s.n.Address())
	if err != nil {
		return nil, err
	}
	_ = ctrl.SetDeadline(time.Now().Add(s.DialTimeout()))
	host, port, err := socks5UDPAssociate(ctrl, s.n.Username, s.n.Password)
	if err != nil {
		ctrl.Close()
		return nil, fmt.Errorf("SOCKS5 出口不支持 UDP ASSOCIATE: %w", err)
	}
	_ = ctrl.SetDeadline(time.Time{})

	// 同一处缺陷的第二处实例（审查 #18）：出口地址在真实配置里常常就是域名，
	// 旧写法把域名喂给 net.ParseIP（返回 nil）后静默改成 0.0.0.0，
	// 等于把 UDP 中继指向本机。详见 dialSocks5UDPRelay 的注释。
	relay, err := dialSocks5UDPRelay(ctx, host, port, s.n.Server, s.DialTimeout())
	if err != nil {
		ctrl.Close()
		return nil, err
	}
	return newSocks5UDPChannel(ctrl, relay), nil
}

// ------------------------------------------------------------------
// http 出口
// ------------------------------------------------------------------

type httpUpstream struct {
	n config.Node
}

func (h *httpUpstream) Describe() string                   { return h.n.Describe() }
func (h *httpUpstream) Start(context.Context, Front) error { return nil }
func (h *httpUpstream) Stop() error                        { return nil }
func (h *httpUpstream) SupportsUDP() bool                  { return false }

func (h *httpUpstream) DialTimeout() time.Duration {
	return time.Duration(h.n.Timeout()) * time.Millisecond
}

func (h *httpUpstream) Dial(ctx context.Context, front Front, target string) (net.Conn, error) {
	conn, err := front.DialTCP(ctx, h.n.Address())
	if err != nil {
		return nil, err
	}
	host, port, err := SplitHostPort(target, 443)
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(h.DialTimeout()))
	if err := httpConnect(conn, host, port, h.n.Username, h.n.Password); err != nil {
		conn.Close()
		return nil, fmt.Errorf("HTTP 出口 CONNECT 握手失败（连 %s）: %w", target, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func (h *httpUpstream) DialUDP(context.Context, Front) (UDPChannel, error) {
	return nil, fmt.Errorf("HTTP 出口不支持 UDP: %w", ErrUDPUnsupported)
}

// ------------------------------------------------------------------
// WireGuard 出口的延迟构造包装
// ------------------------------------------------------------------

// lazyWireGuardUpstream 在 Start() 时才创建底层 wireguard 出口。
//
// 这样"点激活"之前的构造阶段不会去碰网络，失败也能在激活时明确报出来。
type lazyWireGuardUpstream struct {
	n     config.Node
	front Front
	inner Upstream
}

func (l *lazyWireGuardUpstream) Describe() string { return l.n.Describe() }

func (l *lazyWireGuardUpstream) Start(ctx context.Context, front Front) error {
	if l.inner == nil {
		if wgFactory == nil {
			return errors.New("当前构建未包含 WireGuard 支持")
		}
		// 优先用调用方传进来的前置；没传就用构造时根据配置生成的。
		f := front
		if f == nil {
			f = l.front
		}
		inner, err := wgFactory(l.n, f)
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

func (l *lazyWireGuardUpstream) SupportsUDP() bool {
	return l.inner != nil && l.inner.SupportsUDP()
}

func (l *lazyWireGuardUpstream) DialTimeout() time.Duration {
	return time.Duration(l.n.Timeout()) * time.Millisecond
}
