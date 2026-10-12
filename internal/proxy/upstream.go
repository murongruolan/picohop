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
//	vmess/vless/...   由内嵌的 sing-box 引擎负责（internal/engine）
type Upstream interface {
	// Start 建立底层通道。socks5/http 出口这里是空操作；
	// wireguard 在这里完成握手；sing-box 出口在这里拉起实例。
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
//   - wgFactory   由 internal/wgproxy 注册
//   - singFactory 由 internal/engine 注册
var (
	wgFactory   func(config.Node, Front) (Upstream, error)
	singFactory func(config.Node, config.Front, func(string, ...any)) (Upstream, error)
	// mihomoFactory 由 internal/engine 在 init 时注册（换装期与 sing-box 并存）。
	mihomoFactory func(config.Node, config.Front, func(string, ...any)) (Upstream, error)

	// engineMode 决定优先用哪个内核：空串 / "singbox" = sing-box；"mihomo" = 优先 mihomo。
	// 【为什么默认 sing-box】换装分步走：mihomo 只在明确打开时启用，而且接不了的节点
	// 会自动回退 —— 任何时刻都不会因为换装而少一个可用节点。
	engineMode string
)

// RegisterWireGuardFactory 让 internal/wgproxy 在 init 时注册构造器。
func RegisterWireGuardFactory(f func(config.Node, Front) (Upstream, error)) {
	wgFactory = f
}

// RegisterMihomoFactory 让 internal/engine 在 init 时注册 mihomo 构造器（与 sing-box 并存）。
func RegisterMihomoFactory(f func(config.Node, config.Front, func(string, ...any)) (Upstream, error)) {
	mihomoFactory = f
}

// SetEngineMode 选择优先使用的内核（空串 / "singbox" / "mihomo"）。
func SetEngineMode(mode string) { engineMode = mode }

// EngineMode 返回当前引擎模式（测试与日志用）。
func EngineMode() string { return engineMode }

// RegisterSingBoxFactory 让 internal/engine 在 init 时注册构造器。
func RegisterSingBoxFactory(f func(config.Node, config.Front, func(string, ...any)) (Upstream, error)) {
	singFactory = f
}

// NewUpstream 按节点配置创建出口实现。
//
// frontCfg 是已经解析好的前置（直连 / 全局代理 / 另一个节点的本地端口）。
// 原生实现（socks5/http/wireguard）在 Dial 时使用运行期的 Front 实例；
// sing-box 实现则需要在构造时就知道前置是什么，因为前置是写进它的配置里的。
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
		// 【换装开关】mode=mihomo 时优先用它；它接不了这个节点就回退 sing-box，
		// 并写一行日志说明原因 —— 宁可少一次用上新内核，也不能让节点不可用。
		if engineMode == "mihomo" && mihomoFactory != nil {
			if up, err := mihomoFactory(n, frontCfg, logf); err == nil {
				return up, nil
			} else if logf != nil {
				logf("mihomo 接不了「%s」，回退 sing-box：%v", n.Name, err)
			}
		}
		if singFactory == nil {
			return nil, fmt.Errorf("当前构建未包含 %s 支持", config.ProtocolLabel(n.Protocol))
		}
		return singFactory(n, frontCfg, logf)

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
