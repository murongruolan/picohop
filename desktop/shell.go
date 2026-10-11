package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"chainproxy/internal/config"
)

// ---------------------------------------------------------------------------
// Wails v3 外壳胶水层
//
// 把"和 Wails 打交道"的代码集中在这里，api.go 尽量只写业务。
// v2 → v3 的差别在这一个文件里就能看全：
//   - 事件：runtime.EventsEmit(ctx, ...)        → app.Event.Emit(...)
//   - 剪贴板：runtime.ClipboardGetText(ctx)      → app.Clipboard.Text()
//   - 对话框：runtime.MessageDialog(ctx, ...)    → app.Dialog.Error()...
//   - 退出：runtime.Quit(ctx)                    → app.Quit()
//   - 窗口：runtime.WindowHide(ctx)              → window.Hide()
//   - 主题：runtime.WindowSetDarkTheme(ctx)      → 无对应物，自己调 DwmSetWindowAttribute
// ---------------------------------------------------------------------------

// 主窗口标题。托盘图标定位、标题栏明暗同步都要按它找窗口，所以集中在这里。
const mainWindowTitle = "ChainProxy · 链式代理"

// setTitleBarTheme 按 system/light/dark 同步主窗口标题栏明暗。
//
// 返回是否真的改到了窗口（失败只影响观感，不影响功能，所以调用方不必处理错误）。
func setTitleBarTheme(theme string) bool {
	dark := false
	switch theme {
	case "dark":
		dark = true
	case "light":
		dark = false
	default:
		// system：跟随系统。Wails 只在创建窗口时认这个值，
		// 运行期切换得我们自己读注册表。
		dark = systemIsDark()
	}
	return applyImmersiveDarkMode(mainWindowTitle, dark)
}

// attach 把 v3 的应用对象与通知服务交给 API。
//
// 必须在 application.New 之后、app.Run 之前调用 —— ServiceStartup 里要用到它们。
func (a *API) attach(wapp *application.App, notif *notifications.NotificationService) {
	a.wapp = wapp
	a.notif = notif
}

// setMainWindow 记住主窗口，托盘/快捷键/导入都要用它。
func (a *API) setMainWindow(w application.Window) { a.mainWin = w }

// initialWindowTheme 单独读一份配置，只为拿初始主题。
//
// 为什么不能等 ServiceStartup：窗口是在 app.Run() **之前**创建的，
// 而加载后端的 ServiceStartup 发生在 Run 里面 —— 那时标题栏已经画出来了。
// 读失败就按"跟随系统"处理，不值得为它挡住启动。
func (a *API) initialWindowTheme() application.Theme {
	cfg, err := config.Load(config.ConfigPath())
	if err != nil || cfg == nil {
		return application.SystemDefault
	}
	switch cfg.Settings.Theme {
	case "dark":
		return application.Dark
	case "light":
		return application.Light
	default:
		return application.SystemDefault
	}
}

// Emit 把事件推给前端（对应 v2 的 runtime.EventsEmit）。
func (a *API) Emit(name string, data ...any) {
	if a.wapp == nil {
		return
	}
	a.wapp.Event.Emit(name, data...)
}

// Errorf 记一条错误日志（对应 v2 的 runtime.LogErrorf）。
func (a *API) Errorf(format string, args ...any) { log.Printf(format, args...) }

// dialogError 弹一个错误对话框（对应 v2 的 runtime.MessageDialog + ErrorDialog）。
func (a *API) dialogError(title, message string) {
	if a.wapp == nil {
		return
	}
	a.wapp.Dialog.Error().SetTitle(title).SetMessage(message).Show()
}

// notify 发一条 Windows 系统通知。
//
// 失败只记日志、不上抛：用户完全可能在系统设置里关掉通知，
// 那是他的选择，不该让程序把它当成错误。
func (a *API) notify(title, body string) {
	if a.notif == nil {
		return
	}
	if err := a.notif.SendNotification(notifications.NotificationOptions{
		// 同一个 ID 会替换掉上一条，正是这里想要的（状态类提示不需要堆积）
		ID:    "chainproxy-" + strings.ReplaceAll(title, " ", "-"),
		Title: title,
		Body:  body,
	}); err != nil {
		log.Printf("发送系统通知失败：%v", err)
	}
}

// warnShell 记录一条"外壳初始化"相关的告警。
//
// 后端就绪前先攒着，等 ServiceStartup 里 reportShellStatus 补投 ——
// 否则这类提示在 GUI 版（没有控制台）里会被静默丢掉。
func (a *API) warnShell(msg string) {
	a.mu.Lock()
	a.pendingWarnings = append(a.pendingWarnings, msg)
	ready := a.app != nil
	a.mu.Unlock()
	if ready {
		a.flushWarnings()
	}
}

// flushWarnings 把攒下的告警投给界面（后端已就绪时才会真的显示出来）。
func (a *API) flushWarnings() {
	a.mu.Lock()
	list := a.pendingWarnings
	a.pendingWarnings = nil
	app := a.app
	a.mu.Unlock()
	if app == nil || len(list) == 0 {
		return
	}
	for _, msg := range list {
		app.Notify("warning", "外壳初始化提示", msg)
	}
}

// minimiseToTray 把主窗口收进托盘、但不退出进程。
//
// 这是"点 × 不退出"的实际动作。注意它**不是**关窗：窗口只是隐藏，
// 代理与所有监听端口都不受影响；真正的退出走托盘菜单或界面上的退出按钮。
func (a *API) minimiseToTray() {
	if a.mainWin != nil {
		a.mainWin.Hide()
	}
	if a.app != nil {
		a.app.Logf("窗口关闭被拦下：已收进托盘，进程与代理继续运行（退出请用托盘菜单）")
	}
	a.notifyMinimisedToTray()
}

// notifyMinimisedToTray 把窗口收进托盘时提示一次。
//
// 不提示的话用户会以为程序被关掉了 —— 代理还在跑，但界面上找不到入口，
// 这是最容易被误解的状态。只提示一次，之后他应该已经知道了。
func (a *API) notifyMinimisedToTray() {
	a.trayHintOnce.Do(func() {
		a.notify("ChainProxy 仍在运行",
			"窗口已收进托盘，代理不受影响。右键托盘图标可激活/停止节点或退出。")
	})
}

// warnShortcutFailed 快捷键注册失败时记一条告警（后端就绪后会在界面上显示）。
func (a *API) warnShortcutFailed(accel, desc string, err error) {
	a.shortcutWarned.Do(func() {
		a.warnShell(fmt.Sprintf("%s（%s）可能被其它程序占用了：%v", accel, desc, err))
	})
}

// clipboardText 读系统剪贴板（对应 v2 的 runtime.ClipboardGetText）。
//
// 签名与 v2 不同：v3 返回 (string, bool)，没有 error —— 拿不到就是 false。
func (a *API) clipboardText() (string, bool) {
	if a.wapp == nil {
		return "", false
	}
	return a.wapp.Clipboard.Text()
}

// setClipboard 写系统剪贴板（对应 v2 的 runtime.ClipboardSetText）。
func (a *API) setClipboard(text string) bool {
	if a.wapp == nil {
		return false
	}
	return a.wapp.Clipboard.SetText(text)
}

// windowHide 隐藏主窗口（圈选二维码前要先把窗口收起来，否则会挡住屏幕）。
func (a *API) windowHide() {
	if a.mainWin != nil {
		a.mainWin.Hide()
	}
}

// windowRestore 把主窗口显示回来并取消最小化。
//
// 单独抽出来是因为它被 defer 调用：圈选流程中途无论出什么问题，
// 都必须把界面恢复出来，否则用户会面对"程序还在跑但窗口没了"的状态。
func (a *API) windowRestore() {
	if a.mainWin == nil {
		return
	}
	a.mainWin.Show()
	a.mainWin.Restore()
}

// ShowWindow 显示主窗口并聚焦（托盘单击、快捷键都走它）。
func (a *API) ShowWindow() {
	if a.mainWin == nil {
		return
	}
	a.mainWin.Show()
	a.mainWin.Restore()
	a.mainWin.Focus()
}

// ToggleWindow 显示/隐藏主窗口（全局快捷键用）。
func (a *API) ToggleWindow() {
	if a.mainWin == nil {
		return
	}
	if a.mainWin.IsVisible() {
		a.mainWin.Hide()
		return
	}
	a.ShowWindow()
}

// ToggleActivateAll 全局快捷键用：有在跑的节点就全停，否则全开。
func (a *API) ToggleActivateAll() {
	if a.app == nil {
		return
	}
	if a.app.Summary().Running > 0 {
		if err := a.DeactivateAll(); err != nil {
			a.Errorf("停止全部失败：%v", err)
		}
		return
	}
	if err := a.ActivateAll(); err != nil {
		a.Errorf("激活全部失败：%v", err)
	}
}

// CopyFirstLocalAddress 把第一个节点的本地代理地址复制到剪贴板（托盘菜单用）。
//
// 之所以要这个：托盘常驻时用户多半是为了拿地址去配浏览器/其它工具，
// 让他先打开窗口再找那一行、再复制，属于多余步骤。
func (a *API) CopyFirstLocalAddress() {
	if a.app == nil || a.wapp == nil {
		return
	}
	list := a.app.Nodes()
	if len(list) == 0 {
		a.notify("还没有节点", "先在界面里添加一个节点，再回来复制本地地址。")
		return
	}
	n := list[0]
	scheme := n.Protocol
	if scheme == "" {
		scheme = "socks5"
	}
	addr := fmt.Sprintf("%s://%s:%d", scheme, n.ListenHost, n.ListenPort)
	if !a.wapp.Clipboard.SetText(addr) {
		a.notify("复制失败", "系统剪贴板暂时不可用。")
		return
	}
	a.notify("已复制本地代理地址", addr)
}
