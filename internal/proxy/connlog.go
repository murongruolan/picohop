package proxy

import (
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ConnEvent 是一条连接的生命周期事件（建立 / 结束）。
//
// 【为什么不用内核自带的那个 RESTful API】我们只把内核当出口用、**没有 inbound**，
// 而且是直接调 outbound 的 Dial、**绕过了它的路由器** —— 它的连接列表里永远不会有东西
// （第 3 步在日志上也踩过同一个坑）。真正知道"谁连了哪儿、传了多少"的是我们自己的 handler。
type ConnEvent struct {
	ID       uint64 `json:"id"`
	Phase    string `json:"phase"` // open / close
	Node     string `json:"node"`
	Protocol string `json:"protocol"` // socks5 / http
	Target   string `json:"target"`
	Src      string `json:"src"`      // 客户端地址（127.0.0.1:xxxxx）
	SrcPort  int    `json:"srcPort"`  // 界面上的"源端口"列
	At       int64  `json:"at"`       // 建立时间（Unix 毫秒）
	Up       int64  `json:"up"`       // 仅 close 有值：客户端 → 目标
	Down     int64  `json:"down"`     // 仅 close 有值：目标 → 客户端
	Duration int64  `json:"duration"` // 仅 close 有值：毫秒
	Err      string `json:"err,omitempty"`
}

const (
	ConnPhaseOpen  = "open"
	ConnPhaseClose = "close"
)

// connSink 是连接事件的接收器（进程内唯一）。
var (
	connSinkMu sync.Mutex
	connSink   func(ConnEvent)
)

// SetConnSink 注册连接事件接收器；传 nil 取消。
//
// 用一次注册而不是顺着 NewServer/Handler 层层透传：接收器最终落到 app 的一张连接表里，
// 跟"哪个节点"无关，一次注册更贴切、改动也小。
func SetConnSink(fn func(ConnEvent)) {
	connSinkMu.Lock()
	connSink = fn
	connSinkMu.Unlock()
}

func emitConn(ev ConnEvent) {
	connSinkMu.Lock()
	fn := connSink
	connSinkMu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

// ------------------------------------------------------------------
// 活动连接登记（"关闭全部"要能真的把它们关掉）
// ------------------------------------------------------------------

// ConnCounter 是一条连接的实时字节计数。
//
// 【为什么要它】字节数原来只在连接结束时回传给界面 —— 于是**正在传输**的连接
// 上下行一直显示 0，看不出它是不是在动（自测截图里就抓到了这一点）。
// 这里让中继循环直接累加到它身上，界面每秒轮询时读一次即可：
// 不用发事件（每秒几十次的进度事件毫无意义），也不会有锁竞争。
type ConnCounter struct {
	// ID 是这条连接的编号，与事件里的 ID 一致。
	// 中继需要计数、界面需要按编号定向关闭，两条需求合在一个对象上最省事。
	ID   uint64
	up   atomic.Int64
	down atomic.Int64
}

func (c *ConnCounter) AddUp(n int64)   { c.up.Add(n) }
func (c *ConnCounter) AddDown(n int64) { c.down.Add(n) }
func (c *ConnCounter) Up() int64       { return c.up.Load() }
func (c *ConnCounter) Down() int64     { return c.down.Load() }

type activeConn struct {
	closers []io.Closer
	counter *ConnCounter
}

var (
	activeMu    sync.Mutex
	activeConns = map[uint64]*activeConn{}
	connSeq     atomic.Uint64
)

// LiveConnStats 返回每条活动连接的实时字节数：id → (上行, 下行)。
//
// 界面按秒轮询连接表时用它补上"正在动"的数字。
func LiveConnStats() map[uint64][2]int64 {
	activeMu.Lock()
	defer activeMu.Unlock()
	out := make(map[uint64][2]int64, len(activeConns))
	for id, c := range activeConns {
		out[id] = [2]int64{c.counter.Up(), c.counter.Down()}
	}
	return out
}

// trackConn 登记一条已建立的连接，返回它的 id 与"结束"回调。
//
// closers 是这条连接的参与者（客户端连接 + 远端连接）—— 记下来，
// 界面上点"关闭全部"时才有东西可关；不记的话这个按钮就只能是个摆设。
func (h *Handler) trackConn(protocol, target string, client net.Conn, closers ...io.Closer) (*ConnCounter, func(up, down int64, err error)) {
	id := connSeq.Add(1)
	counter := &ConnCounter{ID: id}
	srcPort := 0
	src := ""
	if client != nil {
		src = client.RemoteAddr().String()
		if _, portStr, err := net.SplitHostPort(src); err == nil {
			srcPort, _ = strconv.Atoi(portStr)
		}
	}
	at := time.Now()

	activeMu.Lock()
	activeConns[id] = &activeConn{closers: closers, counter: counter}
	activeMu.Unlock()

	emitConn(ConnEvent{
		ID: id, Phase: ConnPhaseOpen, Node: h.Name,
		Protocol: protocol, Target: target, Src: src, SrcPort: srcPort, At: at.UnixMilli(),
	})

	return counter, func(up, down int64, err error) {
		activeMu.Lock()
		delete(activeConns, id)
		activeMu.Unlock()

		ev := ConnEvent{
			ID: id, Phase: ConnPhaseClose, Node: h.Name,
			Protocol: protocol, Target: target, Src: src, SrcPort: srcPort,
			At: at.UnixMilli(), Up: up, Down: down,
			Duration: time.Since(at).Milliseconds(),
		}
		if err != nil {
			ev.Err = err.Error()
		}
		emitConn(ev)
	}
}

// CloseConnections 关闭指定的活动连接；ids 为空表示**全部**。返回请求关闭的条数。
//
// 关闭的同时**立刻把登记项摘掉**：只关底层连接、不摘登记的话，
// "关闭全部"之后活动列表还是满的，用户会以为没关掉
// （自测就抓到了这个 —— 摘与不摘，界面感受完全不同）。
// 真正的收尾（读写 goroutine 退出）随后发生，届时 finish 回调会补发 close 事件；
// 那时登记项已经不在，delete 一个不存在的键是无害的。
func CloseConnections(ids []uint64) int {
	activeMu.Lock()
	var targets [][]io.Closer
	if len(ids) == 0 {
		for id, c := range activeConns {
			targets = append(targets, c.closers)
			delete(activeConns, id)
		}
	} else {
		for _, id := range ids {
			if c, ok := activeConns[id]; ok {
				targets = append(targets, c.closers)
				delete(activeConns, id)
			}
		}
	}
	activeMu.Unlock()

	n := 0
	for _, cs := range targets {
		for _, c := range cs {
			if c != nil {
				_ = c.Close()
			}
		}
		n++
	}
	return n
}

// ActiveConnCount 返回当前登记的活动连接数（自测与状态区用）。
func ActiveConnCount() int {
	activeMu.Lock()
	defer activeMu.Unlock()
	return len(activeConns)
}
