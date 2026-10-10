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

// Server 是一条链路的本地监听服务：绑定一个端口，处理所有客户端连接。
type Server struct {
	mu       sync.Mutex
	cfg      config.Upstream
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
func NewServer(cfg config.Upstream, front Front, logf func(string, ...any)) (*Server, error) {
	up, err := NewUpstream(cfg)
	if err != nil {
		return nil, err
	}
	var auth *Auth
	if cfg.ListenUsername != "" || cfg.ListenPassword != "" {
		auth = &Auth{Username: cfg.ListenUsername, Password: cfg.ListenPassword}
	}
	idle := 300 * time.Second
	if cfg.TimeoutMs > 0 {
		idle = time.Duration(cfg.TimeoutMs) * time.Millisecond
	}
	s := &Server{
		cfg:      cfg,
		front:    front,
		upstream: up,
		stats:    &Stats{},
		conns:    make(map[net.Conn]struct{}),
	}
	s.handler = &Handler{
		Name:     cfg.Name,
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

// Start 绑定端口、启动上游、开始接受连接。
func (s *Server) Start() error {
	if s.started.Load() {
		return nil
	}
	host := s.cfg.ListenHost
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, fmt.Sprint(s.cfg.ListenPort))

	// 先启动上游（WireGuard 需要在这里握手），再绑定端口。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.upstream.Start(ctx, s.front); err != nil {
		s.setErr(err)
		return fmt.Errorf("上游启动失败: %w", err)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.setErr(err)
		return fmt.Errorf("端口 %d 绑定失败: %w", s.cfg.ListenPort, err)
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
	_ = s.upstream.Stop()
	return nil
}

// Addr 返回实际监听地址（Start 之后有效）。
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return net.JoinHostPort(s.cfg.ListenHost, fmt.Sprint(s.cfg.ListenPort))
	}
	return s.ln.Addr().String()
}

// Name 返回链路名称（住宅代理名）。
func (s *Server) Name() string { return s.cfg.Name }

// ListenPort 返回配置中的端口号。
func (s *Server) ListenPort() int { return s.cfg.ListenPort }

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
