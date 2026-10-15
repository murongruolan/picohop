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

// trayModeUnknown 表示"还没往系统里推过任何图标"。
//
// 【它不是可有可无的】托盘图标是"只在变化时推给系统"的，而结构体的零值恰好是
// trayModeNormal —— 登记托盘时若把当前状态当成"已经是普通"，那么"设成普通"这一步
// 会被判成"没变化"而**从不调用 SetIcon**，托盘就一直是系统默认图标。
// 这个 bug 真发生过（见 trayPlan 的注释）。
//
// 【为什么单独一个 const 块】它必须与下面三个同处一个 iota 序列里的话，iota 会把
// trayModeNormal 算成 1（iota 数的是本块内的行号）—— 而"零值 = 普通"这条约定不能破。
const trayModeUnknown trayMode = -1

const (
	trayModeNormal trayMode = iota
	trayModeGlobal
	trayModeTun
)

// trayModeName 给日志用：排查"图标怎么没变"时，日志里得看得出当时是哪个状态。
func trayModeName(m trayMode) string {
	switch m {
	case trayModeUnknown:
		return "未设置"
	case trayModeGlobal:
		return "全局代理"
	case trayModeTun:
		return "TUN（预留）"
	default:
		return "普通"
	}
}

// trayPlan 决定"这次要不要真的把图标推给系统"，返回（新的当前状态, 要不要推）。
//
// 【为什么要单独抽成纯函数】这里踩过一个真 bug：登记托盘时用"结构体零值"当当前状态，
// 而零值就是 trayModeNormal，于是"设成普通"被判成没变化 → 从没 SetIcon → 托盘一直
// 是系统默认图标。当时的单测只检查了状态字段、没检查"到底推没推"，所以没抓到。
// 现在这条规则本身可单测（见 tray_state_test.go 的 TestTrayPlanFirstPushAlwaysPushes）。
func trayPlan(cur, want trayMode) (trayMode, bool) {
	if cur == want {
		return cur, false
	}
	return want, true
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
	// 【必须先把当前状态标成"未设置"】否则下面这次调用会被 trayPlan 判成"没变化"，
	// 于是第一次 SetIcon 永远不会发生 —— 托盘就一直是系统默认图标（踩过）。
	a.trayMode = trayModeUnknown
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
	next, push := trayPlan(cur, mode)
	a.trayMode = next
	a.trayMu.Unlock()

	if tray == nil || !push {
		return
	}
	icon := trayIconFor(mode)
	tray.SetIcon(icon)
	a.trayMu.Lock()
	a.trayPushed = true
	a.trayMu.Unlock()
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
