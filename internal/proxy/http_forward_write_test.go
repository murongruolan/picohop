package proxy

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"
)

// ------------------------------------------------------------------
// 审查 #17：httpForward 把上游应答"写回客户端"时没有写超时
// ------------------------------------------------------------------

// httpForwardStubUpstream 是本用例的假出口：不联网，Dial 直接把 net.Pipe 的一端
// 交出去当"上游连接"，于是"上游 -> 我们"的字节完全由用例控制。
//
// 【为什么用 net.Pipe 而不是真上游】net.Pipe 没有内核缓冲、写是**同步**的：
// 用例往 peer 写多少字节，handler 就必须原样往客户端塞多少字节。
// 这样"客户端不读 => 写阻塞"一定会发生，不必去猜 socket 缓冲到底有多大。
type httpForwardStubUpstream struct {
	conn   net.Conn
	dialed chan string
}

func (u *httpForwardStubUpstream) Start(context.Context, Front) error { return nil }
func (u *httpForwardStubUpstream) Stop() error                        { return nil }
func (u *httpForwardStubUpstream) SupportsUDP() bool                  { return false }
func (u *httpForwardStubUpstream) Describe() string                   { return "stub-http-forward" }
func (u *httpForwardStubUpstream) DialTimeout() time.Duration         { return time.Second }

func (u *httpForwardStubUpstream) DialUDP(context.Context, Front) (UDPChannel, error) {
	return nil, ErrUDPUnsupported
}

func (u *httpForwardStubUpstream) Dial(_ context.Context, _ Front, target string) (net.Conn, error) {
	select {
	case u.dialed <- target:
	default:
	}
	return u.conn, nil
}

// TestHTTPForwardWriteBackTimesOutWhenClientStopsReading 锁死审查 #17：
// 客户端"发完请求就再也不读"，httpForward 的写回必须自己超时退出，
// 不能把 goroutine、上游连接和成对的句柄永久钉住。
//
// 【缺陷原样】这条路径对客户端写应答头和响应体时**一次 SetWriteDeadline 都没设**
// （同文件的 CONNECT 路径走 Relay，那里两侧每次写之前都会设，见 relay.go）。
// 客户端不读 => 内核发送缓冲 + 客户端接收缓冲填满 => client.Write 永久阻塞，
// 上游连接也跟着挂着；反复来几次就能把进程句柄耗光（AllowLan 打开时局域网内谁都能触发）。
//
// 【为什么把超时压小】默认超时是 60s（= Relay 的 writeTimeout），真等满 60 秒的用例
// 没人会跑。这里临时把 httpForwardWriteTimeout 压到 300ms，其余链路（真 socket、
// 真阻塞）都保持原样。客户端侧刻意**绝不读**，并把接收缓冲调小，逼出真实的写阻塞。
//
// 【断言什么】
//  1. ServeClient 必须在写超时之后、且在 10s 上限之内返回 —— 返回不了就是又退化成永久阻塞；
//  2. 耗时不能短于写超时，否则说明它是"因为别的原因提前失败"，本用例就白跑了；
//  3. 上游连接确实被关掉了（句柄回收，而不是丢下连接不管）。
func TestHTTPForwardWriteBackTimesOutWhenClientStopsReading(t *testing.T) {
	const testWriteTimeout = 300 * time.Millisecond
	origTimeout := httpForwardWriteTimeout
	httpForwardWriteTimeout = testWriteTimeout
	t.Cleanup(func() { httpForwardWriteTimeout = origTimeout })

	pipeConn, peer := net.Pipe()
	t.Cleanup(func() { _ = pipeConn.Close(); _ = peer.Close() })

	up := &httpForwardStubUpstream{conn: pipeConn, dialed: make(chan string, 1)}
	h := &Handler{Name: "n1", Upstream: up, Stats: &Stats{}}

	// 真 socket：客户端连上之后只发请求、绝不读；接收缓冲压小让写阻塞尽早出现。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败：%v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("连本地代理失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if tc, ok := client.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(4096)
	}
	serverConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("接受连接失败：%v", err)
	}
	t.Cleanup(func() { _ = serverConn.Close() })

	served := make(chan struct{})
	go func() {
		h.ServeClient(serverConn)
		close(served)
	}()

	if _, err := client.Write([]byte("GET http://example.com/ HTTP/1.1\r\n" +
		"Host: example.com\r\nProxy-Connection: keep-alive\r\n\r\n")); err != nil {
		t.Fatalf("发代理请求失败：%v", err)
	}

	select {
	case target := <-up.dialed:
		if target != "example.com:80" {
			t.Errorf("转发目标应为 example.com:80，实际 %q", target)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("没有拨号到上游：用例没走到 httpForward 这条路径")
	}

	// 上游侧：先读掉转发的请求头（net.Pipe 是同步的，不读就会把 handler 卡在写请求头上），
	// 再灌一份很大的响应体，一直写到 handler 把上游连接关掉为止。
	upstreamDone := make(chan struct{})
	go func() {
		defer close(upstreamDone)
		if _, err := readHTTPHead(bufio.NewReader(peer)); err != nil {
			return
		}
		if _, err := peer.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 104857600\r\n" +
			"Connection: close\r\n\r\n")); err != nil {
			return
		}
		chunk := make([]byte, 64*1024)
		for {
			if _, err := peer.Write(chunk); err != nil {
				return // handler 收工关掉了上游连接 —— 期望路径
			}
		}
	}()

	start := time.Now()
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatalf("客户端不读响应时写回没有超时退出：10s 内 ServeClient 仍未返回" +
			"（写回 goroutine 与上游连接被永久钉住 = 审查 #17 回归）")
	}
	elapsed := time.Since(start)
	if elapsed < testWriteTimeout {
		t.Errorf("ServeClient 只用 %v 就返回了（短于写超时 %v）：它是别的原因提前失败的，本用例没测到写超时",
			elapsed.Round(time.Millisecond), testWriteTimeout)
	}

	select {
	case <-upstreamDone:
	case <-time.After(3 * time.Second):
		t.Error("handler 退出后上游连接没有被关闭（defer remote.Close() 没生效？）")
	}
}
