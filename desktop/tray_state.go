package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// trayMode 是托盘图标要表达的状态。三种底色由使用者指定（取值与生成方式见
// scripts\gen_icons.ps1 的头部说明）：
//
//	trayModeNormal  白底            —— 平时
//	trayModeGlobal  深棕 #3E1F08    —— 全局代理模式开着（系统代理指向全局节点）
//	trayModeTun     深紫 #240046    —— 预留：以后做 TUN 模式时用
//
// 【为什么用换底色表达状态，而不是换形状 / 加角标】
// 托盘只有 16~24 px，而这个标记（弧线 + 两个点 + 三条输出线）在那个尺寸下本来就看不清
// 细节 —— 再叠角标等于加噪点。换底色是"扫一眼就知道现在是不是全局代理"最省力的做法，
// 也正是使用者的要求。
//
// 【trayModeTun 现在没有任何生产代码会切到它】TUN 模式还没开发，这一档先备齐：
// 图标资产、映射、单测都在位。将来 TUN 开关落地时，在那个开关里调一次
// a.setTrayMode(trayModeTun) 即可（关掉时切回 trayModeNormal）。
type trayMode int

const (
	trayModeNormal trayMode = iota
	trayModeGlobal
	trayModeTun
)

// trayModeName 给日志用：排查"图标怎么没变"时，日志里得看得出当时是哪个状态。
func trayModeName(m trayMode) string {
	switch m {
	case trayModeGlobal:
		return "全局代理"
	case trayModeTun:
		return "TUN（预留）"
	default:
		return "普通"
	}
}

// trayIconFor 返回该状态对应的图标字节。
//
// 做成纯函数有两个理由：
//  1. 可单测（见 tray_state_test.go）：三种状态必须各有一份非空、格式正确的图标 ——
//     //go:embed 路径写错或生成脚本没跑，编译**不会**报错，只有用户会看到空白托盘；
//  2. "状态 → 图标"这条映射只有这一处，将来加第四种状态不会漏改。
func trayIconFor(m trayMode) []byte {
	switch m {
	case trayModeGlobal:
		return trayIconGlobal
	case trayModeTun:
		return trayIconTun
	default:
		return trayIconNormal
	}
}

// setTray 登记托盘句柄并设置初始图标，由 setupTray 在 app.Run() 之前调用。
//
// 【为什么要把句柄存下来】全局代理模式会开/关，托盘图标得跟着变，而 setupTray
// 是启动早期的一次性动作 —— 不存句柄，后面就没法再改图标了。
func (a *API) setTray(tray *application.SystemTray, mode trayMode) {
	a.trayMu.Lock()
	a.tray = tray
	a.trayIconSize = len(trayIconFor(mode))
	a.trayMu.Unlock()
	a.setTrayMode(mode)
}

// setTrayMode 切换托盘图标。同一个状态重复调用不会重复设置。
//
// 【为什么要判重】设置托盘图标是进程外的 Shell 调用；而系统代理那条路径每次开/关
// 都会走到这里，重复设置没有意义。同时它也保证日志里只在真的变了的时候留痕。
func (a *API) setTrayMode(mode trayMode) {
	a.trayMu.Lock()
	tray, cur := a.tray, a.trayMode
	changed := mode != cur
	if changed {
		a.trayMode = mode
	}
	a.trayMu.Unlock()

	if tray == nil || !changed {
		return
	}
	icon := trayIconFor(mode)
	tray.SetIcon(icon)
	a.logf("外壳：托盘图标已切到「%s」（%d 字节）", trayModeName(mode), len(icon))
}

// refreshTrayMode 按"全局代理模式是否开着"决定托盘图标。
//
// 所有会改变该状态的路径都要调它：现在是 SetSystemProxyMode 的开/关两处，
// 以及启动时把开关对齐成关闭那一处。
func (a *API) refreshTrayMode() {
	on := a.app != nil && a.app.Settings().SystemProxy
	if on {
		a.setTrayMode(trayModeGlobal)
		return
	}
	a.setTrayMode(trayModeNormal)
}
