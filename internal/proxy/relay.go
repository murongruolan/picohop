package proxy

import (
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Stats 是单条链路 / 单个上游的流量统计。
type Stats struct {
	Up        atomic.Int64 // 客户端 -> 目标（上传）
	Down      atomic.Int64 // 目标 -> 客户端（下载）
	Conns     atomic.Int64 // 当前活动连接数
	TotalConn atomic.Int64 // 累计连接数
	Errors    atomic.Int64 // 累计失败次数
}

// Add 把另一个统计累加进来。
func (s *Stats) Add(o *Stats) {
	s.Up.Add(o.Up.Load())
	s.Down.Add(o.Down.Load())
	s.TotalConn.Add(o.TotalConn.Load())
	s.Errors.Add(o.Errors.Load())
}

func (s *Stats) Reset() {
	s.Up.Store(0)
	s.Down.Store(0)
	s.TotalConn.Store(0)
	s.Errors.Store(0)
}

// SetReadDeadlineIfPossible 给实现了接口的连接设置读超时。
func setReadDeadline(c net.Conn, t time.Time) {
	if t.IsZero() {
		_ = c.SetReadDeadline(time.Time{})
		return
	}
	_ = c.SetReadDeadline(t)
}

// relayBuffers 双向中继使用的缓冲区大小。
const relayBuffers = 32 * 1024

// Relay 在 client 与 remote 之间做双向搬运。
//
// 关键细节：
//   - 每一方向独立 goroutine，任意一方向 EOF 时只对另一方向 CloseWrite()（半关闭），
//     这样"客户端发完了但还想继续收"（典型：HTTP 请求结束、响应还在来）不会被误杀；
//   - 两侧都结束后才真正关闭连接；
//   - idle > 0 时对读操作加空闲超时，避免死连接长期占用。
func Relay(client, remote net.Conn, stats *Stats, idle time.Duration) {
	if stats != nil {
		stats.Conns.Add(1)
		stats.TotalConn.Add(1)
		defer stats.Conns.Add(-1)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// 上行：客户端 -> 目标
	go func() {
		defer wg.Done()
		buf := make([]byte, relayBuffers)
		for {
			setReadDeadline(client, idleDeadline(idle))
			n, err := client.Read(buf)
			if n > 0 {
				_ = remote.SetWriteDeadline(time.Now().Add(writeTimeout))
				if _, werr := remote.Write(buf[:n]); werr != nil {
					break
				}
				if stats != nil {
					stats.Up.Add(int64(n))
				}
			}
			if err != nil {
				break
			}
		}
		halfClose(remote)
	}()

	// 下行：目标 -> 客户端
	go func() {
		defer wg.Done()
		buf := make([]byte, relayBuffers)
		for {
			setReadDeadline(remote, idleDeadline(idle))
			n, err := remote.Read(buf)
			if n > 0 {
				_ = client.SetWriteDeadline(time.Now().Add(writeTimeout))
				if _, werr := client.Write(buf[:n]); werr != nil {
					break
				}
				if stats != nil {
					stats.Down.Add(int64(n))
				}
			}
			if err != nil {
				break
			}
		}
		halfClose(client)
	}()

	wg.Wait()
	_ = client.Close()
	_ = remote.Close()
}

// writeTimeout 是单次写操作的兜底超时（正常情况下写不会阻塞）。
const writeTimeout = 60 * time.Second

func idleDeadline(idle time.Duration) time.Time {
	if idle <= 0 {
		return time.Time{}
	}
	return time.Now().Add(idle)
}

// halfClose 对 TCP 连接做半关闭；若不是 TCPConn 则忽略。
func halfClose(c net.Conn) {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := c.(closeWriter); ok {
		_ = cw.CloseWrite()
		return
	}
	// UDP 通道等非 TCP 实现无法半关闭，直接忽略（它们的生命周期由外层管理）。
}

// isClosedErr 判断错误是否只是"通道被正常关闭"。
func isClosedErr(err error) bool {
	if err == nil {
		return true
	}
	if err == io.EOF {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "closed") || strings.Contains(s, "EOF") || strings.Contains(s, "reset")
}