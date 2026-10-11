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

// ---------------------------------------------------------------------------
// 全局快捷键
// ---------------------------------------------------------------------------

// ShortcutStatus 是一条全局快捷键的注册结果（给界面显示用）。
type ShortcutStatus struct {
	Key     string `json:"key"`     // 配置里的字段名：toggleWindow / toggleAll
	Label   string `json:"label"`   // 中文说明
	Accel   string `json:"accel"`   // 实际使用的组合键；空表示未启用
	OK      bool   `json:"ok"`      // 是否注册成功
	Message string `json:"message"` // 失败原因（成功时为空）
}

// disabledShortcut 是"显式关闭某条快捷键"的约定值。
//
// 不拿空字符串表示"关闭"：空在配置迁移与老配置里天然等同于"没设过"，
// 那样用户就没法主动关掉一条快捷键了。
const disabledShortcut = "none"

// shortcutDefs 返回当前配置下的快捷键定义（读的是后端里的最新设置）。
func (a *API) shortcutDefs() []ShortcutStatus {
	s := config.Default().Settings
	if a.app != nil {
		s = a.app.Settings()
	} else if c, err := config.Load(config.ConfigPath()); err == nil && c != nil {
		// 应用还没起来（首次注册发生在 ServiceStartup 之前），直接读配置文件
		s = c.Settings
	}
	norm := func(v string) string {
		if strings.EqualFold(strings.TrimSpace(v), disabledShortcut) {
			return ""
		}
		return normalizeAccel(v)
	}
	return []ShortcutStatus{
		{Key: "toggleWindow", Label: "显示 / 隐藏主窗口", Accel: norm(s.ShortcutToggleWindow)},
		{Key: "toggleAll", Label: "激活全部节点 / 停止全部", Accel: norm(s.ShortcutToggleAll)},
	}
}

// normalizeAccel 修正常见的"键名写法问题"，让老配置也能自愈。
//
// 真实踩过：空格键在浏览器里 e.key === ' '，于是存成了 "Ctrl+Alt+ "。
// 而 Wails 的加速键解析器是**按 "+" 切分**的，切出来的最后一段是个空格 ——
// 它报 "' ' is not a valid key"，表现就是"设置看着保存了、按下去毫无反应"。
// 它认的名字是 "Space"（见 global_shortcut_windows.go 的 winKeyCodes）。
//
// 这里只做"裸空格 → Space"这一处归一化：其它错误键名交给注册去报错，
// 免得自作聪明把用户写的组合改成别的东西。
func normalizeAccel(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	parts := strings.Split(v, "+")
	last := parts[len(parts)-1]
	if strings.TrimSpace(last) == "" {
		parts[len(parts)-1] = "Space"
	} else {
		parts[len(parts)-1] = strings.TrimSpace(last)
	}
	return strings.Join(parts, "+")
}

// ApplyShortcuts 按当前设置**重新**注册全局快捷键，返回每一条的结果。
//
// 先全部注销再注册：用户改绑定时旧的那条必须让出来，否则会同时占着两个组合键。
// 失败（组合键被别的程序占用是最常见的）不返回 error，而是逐条报告 ——
// 一条冲突不该让另一条也失效。
func (a *API) ApplyShortcuts() []ShortcutStatus {
	if a.wapp == nil {
		return nil
	}

	// 注销上一次注册的
	a.shortcutMu.Lock()
	old := a.registeredShortcuts
	a.registeredShortcuts = nil
	a.shortcutMu.Unlock()
	for _, accel := range old {
		_ = a.wapp.GlobalShortcut.Unregister(accel)
	}

	handlers := map[string]func(){
		"toggleWindow": a.ToggleWindow,
		"toggleAll":    a.ToggleActivateAll,
	}

	out := a.shortcutDefs()
	for i := range out {
		st := &out[i]
		if st.Accel == "" {
			st.Message = "未启用"
			continue
		}
		if err := a.wapp.GlobalShortcut.Register(st.Accel, handlers[st.Key]); err != nil {
			st.Message = err.Error()
			continue
		}
		st.OK = true
		a.shortcutMu.Lock()
		a.registeredShortcuts = append(a.registeredShortcuts, st.Accel)
		a.shortcutMu.Unlock()
	}
	return out
}

// reportShortcuts 把快捷键注册结果写进日志（GUI 版没有控制台，不记就等于查不到），
// 并在有失败时攒一条界面告警。
func (a *API) reportShortcuts(list []ShortcutStatus) {
	if a.app == nil {
		return
	}
	var okList, failList []string
	for _, st := range list {
		if st.Accel == "" {
			continue
		}
		if st.OK {
			okList = append(okList, fmt.Sprintf("%s（%s）", st.Accel, st.Label))
		} else {
			failList = append(failList, fmt.Sprintf("%s（%s）可能被别的程序占用了：%s", st.Accel, st.Label, st.Message))
		}
	}
	if len(okList) > 0 {
		a.app.Logf("外壳：已注册全局快捷键 %s", strings.Join(okList, "、"))
	}
	for _, f := range failList {
		a.app.Logf("外壳：⚠️ 全局快捷键注册失败：%s", f)
	}
	if len(failList) > 0 {
		a.warnShell(strings.Join(failList, "；"))
	}
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
