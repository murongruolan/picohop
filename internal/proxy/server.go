package proxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"chainproxy/internal/config"
)

// Server 是一个节点的本地监听服务：绑定一个端口，处理所有客户端连接。
//
// 它同时暴露 mixed 协议（SOCKS5 + HTTP），并把流量经"前置 → 出口"转发出去。
type Server struct {
	mu       sync.Mutex
	node     config.Node
	frontCfg config.Front
	front    Front
	upstream Upstream
	handler  *Handler
	ln       net.Listener
	stats    *Stats
	lastErr  atomic.Value // string
	conns    map[net.Conn]struct{}
	closing  atomic.Bool
	started  atomic.Bool
}

// NewServer 构造监听服务（此时尚未绑定端口）。
//
// idle 是"链路空闲多久自动断开"，由调用方从全局设置
// Settings.IdleTimeoutSec 换算后传进来（见 config.Settings.IdleTimeout）。
//
// 【为什么空闲超时必须由外面传进来，而不是在这里用 node.Timeout()】
// node.Timeout() 是**拨号**超时（目前固定 15s，语义是"连上游最多等多久"），
// 和"链路空闲多久断开"是两件毫不相干的事。这里曾经直接拿它顶替空闲超时，
// 后果有两层：Settings.IdleTimeoutSec（默认 300s，界面「网络设置 → 空闲自动断开」）
// 全仓没人读，用户改成多少都不生效；而所有链路只要 15 秒没有字节流动就被掐断 ——
// 长轮询 / SSE / 大文件下载中途最容易中招，表现为"用着用着就断"。
// 修法：空闲超时成了显式参数，来源只有一个（全局设置）。别再改回 node.Timeout()。
//
// node.ListenHost 应当是**已经解析好的**实际监听地址
// （见 config.Config.ListenHostEffective）——这里不再做 AllowLan 判断，
// 免得同一套规则散落在两个包里。
func NewServer(node config.Node, frontCfg config.Front, idle time.Duration, logf func(string, ...any)) (*Server, error) {
	front := NewFront(frontCfg)
	up, err := NewUpstream(node, frontCfg, logf)
	if err != nil {
		return nil, err
	}

	var auth *Auth
	if node.ListenUsername != "" || node.ListenPassword != "" {
		auth = &Auth{Username: node.ListenUsername, Password: node.ListenPassword}
	}

	idle = idleTimeoutOrDefault(idle)

	s := &Server{
		node:     node,
		frontCfg: frontCfg,
		front:    front,
		upstream: up,
		stats:    &Stats{},
		conns:    make(map[net.Conn]struct{}),
	}
	s.handler = &Handler{
		Name:     node.Name,
		Front:    front,
		Upstream: up,
		Listen:   auth,
		Idle:     idle,
		AllowUDP: true,
		Stats:    s.stats,
		Logf:     logf,
	}
	return s, nil
}

// idleTimeoutOrDefault 把非正的空闲超时拉回默认值。
//
// 【为什么不能把 <=0 原样交给中继】Relay 里 idle <= 0 表示"不设空闲超时"
// （永不超时）。参数漏传或传 0 时如果原样透传，就从"15 秒就断"变成"永远不断"，
// 死连接再也回收不了。这里的默认值统一取 config 里的定义，避免两处 300 分叉。
func idleTimeoutOrDefault(idle time.Duration) time.Duration {
	if idle <= 0 {
		return time.Duration(config.DefaultIdleTimeoutSec) * time.Second
	}
	return idle
}

// Start 绑定端口、启动出口、开始接受连接。
func (s *Server) Start() error {
	if s.started.Load() {
		return nil
	}
	host := s.node.ListenHost
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, fmt.Sprint(s.node.ListenPort))

	// 先启动出口（WireGuard 需要在这里握手、sing-box 要在这里拉实例），再绑定端口。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.upstream.Start(ctx, s.front); err != nil {
		s.setErr(err)
		return fmt.Errorf("出口启动失败: %w", err)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.setErr(err)
		// 端口没绑上就把已经起来的出口关掉，避免留下后台实例
		_ = s.upstream.Stop()
		return fmt.Errorf("端口 %d 绑定失败: %w", s.node.ListenPort, err)
	}

	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	s.started.Store(true)
	s.lastErr.Store("")

	go s.acceptLoop(ln)
	return nil
}

func (s *Server) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.closing.Load() {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			s.setErr(err)
			return
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetKeepAlive(true)
			_ = tcp.SetKeepAlivePeriod(30 * time.Second)
			_ = tcp.SetNoDelay(true)
		}

		s.mu.Lock()
		if s.closing.Load() {
			s.mu.Unlock()
			conn.Close()
			return
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()

		go func(c net.Conn) {
			defer func() {
				// 任何客户端异常都不允许拖垮服务
				if r := recover(); r != nil {
					s.handler.fail("客户端处理 panic: %v", r)
				}
				s.mu.Lock()
				delete(s.conns, c)
				s.mu.Unlock()
				_ = c.Close()
			}()
			s.handler.ServeClient(c)
		}(conn)
	}
}

// Stop 停止监听并断开所有活动连接，从而**释放端口**。
func (s *Server) Stop() error {
	if !s.started.Swap(false) {
		return nil
	}
	s.closing.Store(true)

	s.mu.Lock()
	ln := s.ln
	s.ln = nil
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.conns = make(map[net.Conn]struct{})
	s.mu.Unlock()

	if ln != nil {
		_ = ln.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
	return s.upstream.Stop()
}

// Addr 返回实际监听地址（Start 之后有效）。
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return net.JoinHostPort(s.node.ListenHost, fmt.Sprint(s.node.ListenPort))
	}
	return s.ln.Addr().String()
}

// Name 返回节点别名。
func (s *Server) Name() string { return s.node.Name }

// ListenPort 返回配置中的端口号。
func (s *Server) ListenPort() int { return s.node.ListenPort }

// Stats 返回流量统计。
func (s *Server) Stats() *Stats { return s.stats }

// ActiveConns 返回当前活动连接数。
func (s *Server) ActiveConns() int64 { return s.stats.Conns.Load() }

// Running 返回服务是否正在运行。
func (s *Server) Running() bool { return s.started.Load() }

// LastError 返回最近一次错误描述。
func (s *Server) LastError() string {
	if v, ok := s.lastErr.Load().(string); ok {
		return v
	}
	return ""
}

func (s *Server) setErr(err error) {
	if err == nil {
		s.lastErr.Store("")
		return
	}
	s.lastErr.Store(err.Error())
}
