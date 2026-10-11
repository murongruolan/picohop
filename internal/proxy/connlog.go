package proxy

import "sync"

// ConnEvent 是一条"已建立的连接"，是网络日志的数据来源。
//
// 【为什么不由内嵌的 sing-box 提供】试过：给 sing-box 挂 log.PlatformWriter 后它一条
// 连接流水都不产生 —— 因为我们的 sing-box 只当**出口**用（没有 inbound），
// 而且是直接调 outbound 的 Dial、**绕过了它的路由器**，而那条"outbound connection to"
// 是路由器在路由时才打的。真正知道"谁连了哪儿"的是我们自己的 handler。
type ConnEvent struct {
	Node     string
	Protocol string // socks5 / http / udp
	Target   string
}

// connSink 是网络日志接收器（进程内唯一，与具体节点无关）。
var (
	connSinkMu sync.Mutex
	connSink   func(ConnEvent)
)

// SetConnLogSink 注册连接事件接收器；传 nil 取消。
//
// 用一次注册而不是顺着 NewServer/Handler 层层透传：接收器最终落到 app 的一个
// 环形缓冲里，跟"哪个节点"无关，一次注册更贴切、改动也小。
func SetConnLogSink(fn func(ConnEvent)) {
	connSinkMu.Lock()
	connSink = fn
	connSinkMu.Unlock()
}

func connLogSink() func(ConnEvent) {
	connSinkMu.Lock()
	defer connSinkMu.Unlock()
	return connSink
}

// connLog 在连接建立时上报一条事件。没有注册接收器时是空操作。
func (h *Handler) connLog(protocol, target string) {
	if fn := connLogSink(); fn != nil {
		fn(ConnEvent{Node: h.Name, Protocol: protocol, Target: target})
	}
}
