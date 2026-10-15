//go:build windows

package main

import (
	"time"

	"github.com/wailsapp/wails/v3/pkg/w32"
)

// applyWindowIcon 自己给主窗口设图标 —— Windows 上**只能**这么做。
//
// 【为什么不靠 Wails v3】三条路都堵着（对着 v3.0.0-beta.28 的源码读的）：
//   - app 级 setIcon 是空实现（application_windows.go:125）；
//   - 注册窗口类时图标被**硬编码**成 w32.IDI_APPLICATION（application_windows.go:210）
//     —— 也就是系统那个"通用应用"图标，标题栏与任务栏显示的正是它；
//   - WebviewWindowOptions 上没有 Icon 字段（只有 LinuxWindow 有）。
//
// 所以只能拿窗口句柄自己发 WM_SETICON。句柄的取法与主题那套一样：findOwnWindow。
//
// 【为什么 ICON_BIG 与 ICON_SMALL 都要设】ICON_BIG 管任务栏与 Alt+Tab，
// ICON_SMALL 管标题栏左上角那个；只设其中一个，另一处还是通用图标。
//
// 【为什么把旧句柄写进日志】SendMessage 返回被替换掉的旧图标句柄 ——
// 若它正好是 IDI_APPLICATION 的句柄，就证明这次替换确实生效了。GUI 版没有控制台，
// 这类"到底成没成"的信息只有落进 data\picohop.log 才查得到（同 reportShellStatus 的理由）。
func (a *API) applyWindowIcon() bool {
	// 【为什么要重试】窗口的 HWND 是在 app.Run() 里真正创建的，ServiceStartup 阶段
	// findOwnWindow 还找不到主窗口（实测日志写着"没找到主窗口"）。所以给它一个 5 秒的
	// 重试窗口 —— 调用方用 goroutine 起它，免得把启动流程堵在这里。
	for i := 0; i < 25; i++ {
		if a.tryApplyWindowIcon() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	a.logf("外壳：⚠️ 5 秒内没找到主窗口，窗口图标未设置（标题栏会是系统通用图标）")
	return false
}

// tryApplyWindowIcon 试一次；成功返回 true。
func (a *API) tryApplyWindowIcon() bool {
	hwnd := findOwnWindow(mainWindowTitle)
	if hwnd == 0 {
		return false
	}
	small, err := w32.CreateSmallHIconFromImage(windowIcon)
	if err != nil {
		a.logf("外壳：⚠️ 窗口小图标创建失败：%v", err)
		return false
	}
	big, err := w32.CreateLargeHIconFromImage(windowIcon)
	if err != nil {
		a.logf("外壳：⚠️ 窗口大图标创建失败：%v", err)
		return false
	}
	prevSmall := w32.SendMessage(w32.HWND(hwnd), w32.WM_SETICON, w32.ICON_SMALL, uintptr(small))
	prevBig := w32.SendMessage(w32.HWND(hwnd), w32.WM_SETICON, w32.ICON_BIG, uintptr(big))
	a.logf("外壳：窗口图标已设置（被替换掉的旧句柄：ICON_SMALL=%#x ICON_BIG=%#x）", prevSmall, prevBig)
	return true
}
