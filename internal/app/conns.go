package app

import (
	"sort"

	"github.com/murongruolan/picohop/internal/proxy"
)

// ConnRecord 是界面上"连接"视图的一行。
//
// 字段刻意对齐常见代理工具的连接表：主机 / 协议 / 节点 / 源端口 / 上传 / 下载 / 时长。
// 没有"规则"这一列 —— 我们不做规则分流（没有规则引擎），与其放一个永远空的列，
// 不如不放。
type ConnRecord struct {
	ID       uint64 `json:"id"`
	Node     string `json:"node"`
	Protocol string `json:"protocol"`
	Target   string `json:"target"`
	SrcPort  int    `json:"srcPort"`
	StartAt  int64  `json:"startAt"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	Duration int64  `json:"duration"`
	State    string `json:"state"` // active / closed
}

// closedConnCap 是"已关闭"列表保留的条数。
const closedConnCap = 200

// trackConnEvent 由 proxy 的连接事件驱动，维护活动表与最近关闭表。
//
// 单独一把锁：连接事件由转发的 goroutine 触发（每条连接两次），
// 不该和主锁互相等。
func (a *App) trackConnEvent(ev proxy.ConnEvent) {
	a.connMu.Lock()
	defer a.connMu.Unlock()

	switch ev.Phase {
	case proxy.ConnPhaseOpen:
		a.conns[ev.ID] = ConnRecord{
			ID: ev.ID, Node: ev.Node, Protocol: ev.Protocol, Target: ev.Target,
			SrcPort: ev.SrcPort, StartAt: ev.At, State: "active",
		}
		// 顺带进网络日志（连接流水）：只有建立时才写，结束时不再重复一行
		a.appendNetworkLogLocked(ev.Node, ev.Protocol, ev.Protocol+" → "+ev.Target)
	case proxy.ConnPhaseClose:
		rec, ok := a.conns[ev.ID]
		if !ok {
			return
		}
		delete(a.conns, ev.ID)
		rec.State = "closed"
		rec.Up = ev.Up
		rec.Down = ev.Down
		rec.Duration = ev.Duration
		a.closedConns = append(a.closedConns, rec)
		if n := len(a.closedConns); n > closedConnCap {
			copy(a.closedConns, a.closedConns[n-closedConnCap:])
			a.closedConns = a.closedConns[:closedConnCap]
		}
	}
}

// Connections 返回连接表：活动在前（按建立时间倒序），随后是最近关闭的。
func (a *App) Connections() []ConnRecord {
	a.connMu.Lock()
	defer a.connMu.Unlock()

	// 活动连接的字节数从"实时计数"里补：它们还在传输，
	// 不到结束那一刻是不会回传最终值的（否则界面上一直显示 0，看不出在不在动）。
	live := proxy.LiveConnStats()

	out := make([]ConnRecord, 0, len(a.conns)+len(a.closedConns))
	for _, r := range a.conns {
		// 活动连接的时长按"到现在"算，界面不用自己算时钟
		r.Duration = nowMillis() - r.StartAt
		if v, ok := live[r.ID]; ok {
			r.Up, r.Down = v[0], v[1]
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartAt > out[j].StartAt })

	closed := make([]ConnRecord, len(a.closedConns))
	copy(closed, a.closedConns)
	// 最近关闭的排前面
	for i, j := 0, len(closed)-1; i < j; i, j = i+1, j-1 {
		closed[i], closed[j] = closed[j], closed[i]
	}
	return append(out, closed...)
}

// CloseAllConnections 关闭全部活动连接，返回请求关闭的条数。
func (a *App) CloseAllConnections() int {
	return proxy.CloseConnections(nil)
}
