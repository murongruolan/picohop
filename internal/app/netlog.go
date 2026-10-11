package app

import (
	"time"
)

// NetLogEntry 是一条"网络日志"（来自内嵌 sing-box 的日志流水）。
//
// 与"用户日志"分开是刻意的：用户日志是**操作与错误**（启动/停止/导入/失败），
// 网络日志是**连接流水**（谁连了谁、走的哪个出口）。混在一起两边都不好用。
type NetLogEntry struct {
	// Seq 是自增序号，界面靠它做增量拉取（只取"我还没见过的"），
	// 避免每秒把整段日志重新搬一遍。
	Seq   int64  `json:"seq"`
	At    int64  `json:"at"` // Unix 毫秒
	Node  string `json:"node"`
	Level string `json:"level"`
	Text  string `json:"text"`
}

// netLogCap 是网络日志保留的条数。
// 2000 条足够回溯一次排查；再多的价值不如省下的内存。
const netLogCap = 2000

// appendNetworkLog 追加一条网络日志。由 sing-box 的日志回调（内核 goroutine）调用，
// 所以它用**自己那把锁**，不和主锁抢。
func (a *App) appendNetworkLog(node, level, text string) {
	a.netLogMu.Lock()
	defer a.netLogMu.Unlock()
	a.netLogSeq++
	a.netLog = append(a.netLog, NetLogEntry{
		Seq:   a.netLogSeq,
		At:    nowMillis(),
		Node:  node,
		Level: level,
		Text:  text,
	})
	if n := len(a.netLog); n > netLogCap {
		// 一次挪掉一批，别每来一条就拷一次
		copy(a.netLog, a.netLog[n-netLogCap:])
		a.netLog = a.netLog[:netLogCap]
	}
}

// NetworkLogs 返回序号大于 afterSeq 的网络日志，以及当前最大序号。
//
// 游标式而不是"每次返回全部"：网络日志每秒可能进来十几条，
// 每次都把 2000 条重新传一遍纯属浪费。
func (a *App) NetworkLogs(afterSeq int64) ([]NetLogEntry, int64) {
	a.netLogMu.Lock()
	defer a.netLogMu.Unlock()
	latest := a.netLogSeq
	// 缓冲是顺序的，二分找到第一条 Seq > afterSeq 的即可
	lo, hi := 0, len(a.netLog)
	for lo < hi {
		mid := (lo + hi) / 2
		if a.netLog[mid].Seq <= afterSeq {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	out := make([]NetLogEntry, len(a.netLog)-lo)
	copy(out, a.netLog[lo:])
	return out, latest
}

// ClearNetworkLogs 清空网络日志（界面上的"清除本项"）。
func (a *App) ClearNetworkLogs() {
	a.netLogMu.Lock()
	defer a.netLogMu.Unlock()
	a.netLog = nil
}

// nowMillis 是"当前 Unix 毫秒"。单独抽出来是为了在测试里能一眼看出时间戳的来源。
func nowMillis() int64 { return timeNow().UnixMilli() }

var timeNow = time.Now
