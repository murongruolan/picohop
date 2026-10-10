//go:build windows

package gui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"chainproxy/internal/wgproxy"
)

// ---------------------------------------------------------------------------
// UI 自检（uitest）
//
// 设置环境变量 CHAINPROXY_UI_DUMP=<文件路径> 后启动程序，界面完成布局后会把
// 所有控件的句柄、类名、坐标、尺寸、文本、以及列表行数完整导出，然后退出。
//
// 这样"窗口到底有没有正确创建、字段有没有填上、列表有没有行"就能被
// 自动化脚本断言，而不是靠人眼看截图。
// ---------------------------------------------------------------------------

var (
	pEnumChildWindows          = user32.NewProc("EnumChildWindows")
	pIsWindowVisible           = user32.NewProc("IsWindowVisible")
	pIsWindowEnabled           = user32.NewProc("IsWindowEnabled")
	pGetClassNameW             = user32.NewProc("GetClassNameW")
	pGetDlgCtrlIDProc          = user32.NewProc("GetDlgCtrlID")
	pSendMessageTimeoutW       = user32.NewProc("SendMessageTimeoutW")
	pMsgWaitForMultipleObjects = user32.NewProc("MsgWaitForMultipleObjects")
)

type uiDumpEntry struct {
	hwnd    uintptr
	class   string
	id      int32
	x, y    int32
	w, h    int32
	vis     bool
	enabled bool
	text    string
}

const lvifTextD = 0x0001

type lvItemD struct {
	Mask        uint32
	IItem       int32
	ISubItem    int32
	State       uint32
	StateMask   uint32
	PszText     *uint16
	CCchTextMax int32
	IImage      int32
	LParam      uintptr
	IIndent     int32
	IGroupId    int32
	CColumns    uint32
}

// collectChildWindows 枚举主窗口的所有子孙控件。
func collectChildWindows(root uintptr) []uiDumpEntry {
	var out []uiDumpEntry
	cb := syscall.NewCallback(func(h uintptr, l uintptr) uintptr {
		var cls [256]uint16
		pGetClassNameW.Call(h, uintptr(unsafe.Pointer(&cls[0])), 256)
		id, _, _ := pGetDlgCtrlIDProc.Call(h)
		var r RECT
		pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
		vis, _, _ := pIsWindowVisible.Call(h)
		eb, _, _ := pIsWindowEnabled.Call(h)
		out = append(out, uiDumpEntry{
			hwnd:    h,
			class:   utf16String(cls[:]),
			id:      int32(id),
			x:       r.Left,
			y:       r.Top,
			w:       r.Right - r.Left,
			h:       r.Bottom - r.Top,
			vis:     vis != 0,
			enabled: eb != 0,
			text:    crossGetText(h),
		})
		return 1
	})
	pEnumChildWindows.Call(root, cb, 0)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].y != out[j].y {
			return out[i].y < out[j].y
		}
		return out[i].x < out[j].x
	})
	return out
}

// crossGetText 用 SendMessageTimeout 读取文本，避免界面卡死时把自检本身挂住。
func crossGetText(h uintptr) string {
	const smtoAbortOnHung = 0x0002
	buf := make([]uint16, 4096)
	pSendMessageTimeoutW.Call(h, 0x000D, uintptr(len(buf)),
		uintptr(unsafe.Pointer(&buf[0])), smtoAbortOnHung, 2000, 0)
	return utf16String(buf)
}

// listViewText 读取 ListView 第 row 行的内容（用 LVM_GETITEMTEXTW）。
func listViewText(h uintptr, row int32, maxCols int) []string {
	const (
		lvmGetItemTextW = lvmFirst + 76
	)
	res := make([]string, maxCols)
	for col := 0; col < maxCols; col++ {
		buf := make([]uint16, 1024)
		it := lvItemD{
			Mask:        lvifTextD,
			IItem:       row,
			ISubItem:    int32(col),
			PszText:     &buf[0],
			CCchTextMax: 1024,
		}
		pSendMessageTimeoutW.Call(h, lvmGetItemTextW, uintptr(row),
			uintptr(unsafe.Pointer(&it)), 0x0002, 2000, 0)
		res[col] = utf16String(buf)
	}
	return res
}

// maybeDumpUI 在进入消息循环前导出界面状态；未设置环境变量时什么都不做。
func maybeDumpUI(w *winMain) bool {
	path := strings.TrimSpace(os.Getenv("CHAINPROXY_UI_DUMP"))
	if path == "" {
		return false
	}

	// 给界面一次机会完成"载入配置 → 刷新列表 → 自动启动"。
	// 这里不能真正 sleep：睡眠期间没人处理消息，控件状态不会前进。
	// 因此采用"手动泵消息"的方式等待。
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		pumpMessagesFor(50 * time.Millisecond)
	}

	var sb strings.Builder
	sb.WriteString("ChainProxy UI 自检报告\n")
	sb.WriteString(fmt.Sprintf("生成时间: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("主窗口 hwnd=%d  标题=%q\n", w.hwnd, crossGetText(w.hwnd)))
	sb.WriteString(fmt.Sprintf("DPI=%d  链路运行中=%v  WM_TIMER触发次数=%d  PostMessage刷新次数=%d\n\n", w.dpi, w.app.IsRunning(), w.timerTicks, w.refreshTicks))

	entries := collectChildWindows(w.hwnd)
	sb.WriteString(fmt.Sprintf("控件总数: %d\n\n", len(entries)))
	sb.WriteString(fmt.Sprintf("%-4s %-16s %-8s %6s %6s %6s %6s %-4s %-4s %s\n", "#", "类名", "ID", "X", "Y", "W", "H", "可见", "可用", "文本"))
	for i, e := range entries {
		t := e.text
		if len(t) > 70 {
			t = t[:70] + "…"
		}
		t = strings.ReplaceAll(t, "\r", "")
		t = strings.ReplaceAll(t, "\n", " ⏎ ")
		sb.WriteString(fmt.Sprintf("%-4d %-16s %-8d %6d %6d %6d %6d %-4s %-4s %s\n",
			i, e.class, e.id, e.x, e.y, e.w, e.h, yesNo(e.vis), yesNo(e.enabled), t))
	}

	sb.WriteString("\n=== 布局体检 ===\n")
	sb.WriteString(checkLayout(w.hwnd).render())

	sb.WriteString("\n=== 点击模拟（验证按钮真的会被响应）===\n")
	sb.WriteString(simulateClicks(w))

	sb.WriteString("\n住宅代理列表（表格文本）：\n")
	tbl := w.list.text()
	for _, line := range strings.Split(strings.ReplaceAll(tbl, "\r", ""), "\n") {
		sb.WriteString("  | " + line + "\n")
	}
	sb.WriteString("\n链路状态：\n")
	for _, row := range w.app.Status() {
		sb.WriteString(fmt.Sprintf("  %-12s %-6s %-10s %d  %s\n",
			row.Name, row.Type, row.State, row.ListenPort, row.Detail))
	}

	// 追加写：对话框自检报告会先写入同一个文件，不能被截断。
	fMain, errMain := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if errMain == nil {
		_, _ = fMain.WriteString(sb.String())
		_ = fMain.Close()
	}
	return true
}

func crossMsgLong(h uintptr, msg uint32, wparam uintptr) int64 {
	var res uintptr
	pSendMessageTimeoutW.Call(h, uintptr(msg), wparam, 0, 0x0002, 2000, uintptr(unsafe.Pointer(&res)))
	return int64(res)
}

func stageDump(path, marker string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString("=== " + marker + " @ " + time.Now().Format("15:04:05.000") + "\n")
}

func yesNo(v bool) string {
	if v {
		return "是"
	}
	return "否"
}

// maybeDumpDialog 在编辑对话框显示后导出它的控件布局，然后自动关闭。
// 由环境变量 CHAINPROXY_UI_DUMP 指定输出文件。
func maybeDumpDialog(d *dlgState) {
	path := strings.TrimSpace(os.Getenv("CHAINPROXY_UI_DUMP"))
	if path == "" {
		return
	}
	pumpMessagesFor(400 * time.Millisecond)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("ChainProxy 编辑对话框自检报告（类型=%s）\n", d.cType.comboText()))
	sb.WriteString(fmt.Sprintf("生成时间: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("对话框 hwnd=%d  标题=%q  类型=%q  名称=%q\n\n",
		d.hwnd, crossGetText(d.hwnd), d.cType.comboText(), d.cName.text()))

	entries := collectChildWindows(d.hwnd)
	sb.WriteString(fmt.Sprintf("%-4s %-14s %-8s %6s %6s %6s %6s %-4s %s\n", "#", "类名", "ID", "X", "Y", "W", "H", "可见", "文本"))
	for i, e := range entries {
		t := strings.ReplaceAll(strings.ReplaceAll(e.text, "\r", ""), "\n", " ")
		if len(t) > 60 {
			t = t[:60] + "…"
		}
		sb.WriteString(fmt.Sprintf("%-4d %-14s %-8d %6d %6d %6d %6d %-4s %s\n",
			i, e.class, e.id, e.x, e.y, e.w, e.h, yesNo(e.vis), t))
	}
	sb.WriteString("\n=== 对话框布局体检 ===\n")
	sb.WriteString(checkLayout(d.hwnd).render())
	sb.WriteString(fmt.Sprintf("\n住宅服务器组可见=%v  WireGuard 组可见=%v\n",
		d.cServerGroup.enabled() && d.cServerGroup.visible(), d.cWGGroup.visible()))

	// 记下"对话框打开时的类型"：下面的 WG 按钮/密钥体检只对 wireguard 那次有意义
	//（socks5 那次虽然会被类型切换体检切成 wireguard，但字段本来是空的，
	//  拿空值去断言复制结果只会得到假失败）。
	isWGAtOpen := d.cType.comboText() == "wireguard"

	// ---- 类型切换体检 ----
	//
	// 【为什么必须单独测这一步】
	// 之前只测"socks5 对话框"和"wireguard 对话框"，而这两次都是 build() 里
	// 调 load()+typeOnChange() 直接把类型摆好的，**根本没有经过下拉框的通知路径**。
	// 结果"用户在界面上切换类型"这条最常用的路径从来没被测到，
	// 真实 bug（切换后下面那组输入框不跟着变）就这么溜过去了。
	//
	// 这里手工模拟下拉框的行为：先 CB_SETCURSEL 改选中项，再发一条
	// WM_COMMAND(CBN_SELCHANGE) —— 这正是 ComboBox 自己会发的那条消息。
	sb.WriteString("\n=== 类型切换体检 ===\n")
	{
		typeSwitch := func(want string) (wgVis, srvVis bool, gotType string) {
			idx := int32(indexOf(dlgTypes, want))
			sendMessage(uintptr(getCtrlByID(d.hwnd, dgType)), cbSetCurSel, uintptr(idx), 0)
			// 模拟 ComboBox 选中变化后发给父窗口的通知
			sendMessage(d.hwnd, wmCommand,
				uintptr(uint32(uint16(int32(dgType)))|uint32(cbnSelChange)<<16),
				uintptr(getCtrlByID(d.hwnd, dgType)))
			pumpMessagesFor(150 * time.Millisecond)
			return d.cWGGroup.visible(), d.cServerGroup.visible(), d.cType.comboText()
		}

		wgVis, srvVis, got := typeSwitch("wireguard")
		okWG := got == "wireguard" && wgVis && !srvVis
		sb.WriteString(fmt.Sprintf("  %s 切到 wireguard：类型读回=%q WireGuard组可见=%v 服务器组可见=%v\n",
			yesNoWord(okWG), got, wgVis, srvVis))

		wgVis2, srvVis2, got2 := typeSwitch("socks5")
		okSocks := got2 == "socks5" && !wgVis2 && srvVis2
		sb.WriteString(fmt.Sprintf("  %s 切回 socks5：类型读回=%q WireGuard组可见=%v 服务器组可见=%v\n",
			yesNoWord(okSocks), got2, wgVis2, srvVis2))

		// 测完恢复成 wireguard，后面的按钮体检还要用
		typeSwitch("wireguard")
	}

	// ---- 对话框按钮点击体检 ----
	//
	// 这一段专门盯住一个曾经真实发生过的坑：
	// Win32 的窗口类**每个进程只能注册一次**，类里保存的是第一次注册的窗口过程。
	// 早先每次打开对话框都重新注册，于是**从第二次打开开始**，
	// 所有按钮消息都派发到第一次那个已被销毁的对象上，点什么都"没反应"。
	// 而本函数在自检里恰好是**第二次**打开对话框（第一次是 socks5），
	// 所以在这里点按钮，正好覆盖那个场景。
	if isWGAtOpen {
		sb.WriteString("\n=== 对话框按钮点击体检 ===\n")

		privBefore := d.cWGPriv.text()
		sendMessage(uintptr(getCtrlByID(d.hwnd, dgWGGen)), bmClick, 0, 0)
		pumpMessagesFor(300 * time.Millisecond)
		privAfter := d.cWGPriv.text()
		pubAfter := d.cWGPubShow.text()

		genOK := privAfter != "" && privAfter != privBefore && pubAfter != ""
		sb.WriteString(fmt.Sprintf("  %s 「生成密钥」: 私钥 %s → %s；本端公钥=%s\n",
			yesNoWord(genOK), shortKey(privBefore), shortKey(privAfter), shortKey(pubAfter)))
		if !genOK {
			sb.WriteString("      ❌ 按钮没有生效（窗口类注册或消息路由有问题）\n")
		}

		// 由私钥反推的公钥必须和显示的一致
		matchOK := false
		if derived, derr := wgproxy.PublicKeyFromPrivate(privAfter); derr == nil {
			matchOK = derived == pubAfter
		}
		sb.WriteString(fmt.Sprintf("  %s 「本端公钥」与私钥一致：%s\n",
			yesNoWord(matchOK), shortKey(pubAfter)))

		// 复制公钥：点一下，再把剪贴板拿回来比对
		sendMessage(uintptr(getCtrlByID(d.hwnd, dgWGCopyPub)), bmClick, 0, 0)
		pumpMessagesFor(200 * time.Millisecond)
		clip := getClipboardText()
		clipOK := clip != "" && clip == pubAfter
		sb.WriteString(fmt.Sprintf("  %s 「复制公钥」: 剪贴板内容=%s\n",
			yesNoWord(clipOK), shortKey(clip)))

		// ---- 密钥框宽度体检 ----
		//
		// 密钥固定 44 个字符，框太窄就看不全；而手动框选又极易选不全
		// （用户就因此把私钥抄成了 37 个字符，导致握手一直失败还查不出原因）。
		// 所以这里把"每个密钥框都装得下 44 个字符"钉成断言。
		sb.WriteString("\n=== 密钥框宽度体检（44 字符必须完整显示）===\n")
		sampleKey := "YPRXVXeYap1NQGmTWwf746EzPZF7nGYO0X+GQTtbRPA="
		needW := d.l.measure(d.l.fRegular, sampleKey) + 12
		for _, f := range []struct {
			name string
			c    ctrl
		}{
			{"私钥", d.cWGPriv},
			{"对端公钥", d.cWGPub},
			{"预共享密钥", d.cWGPSK},
			{"本端公钥", d.cWGPubShow},
		} {
			w := ctrlClientWidth(f.c)
			sb.WriteString(fmt.Sprintf("  %s %-6s 宽度 %4d px（44 字符需 %4d px）\n",
				yesNoWord(w >= needW), f.name, w, needW))
		}

		// ---- 每个密钥字段的一键复制 ----
		sb.WriteString("\n=== 密钥一键复制体检 ===\n")
		for _, cc := range []struct {
			name string
			btn  id
			src  ctrl
		}{
			{"私钥", dgWGCopyPriv, d.cWGPriv},
			{"对端公钥", dgWGCopyPeer, d.cWGPub},
			{"预共享密钥", dgWGCopyPSK, d.cWGPSK},
		} {
			sendMessage(uintptr(getCtrlByID(d.hwnd, cc.btn)), bmClick, 0, 0)
			pumpMessagesFor(200 * time.Millisecond)
			got := getClipboardText()
			want := cc.src.text()
			sb.WriteString(fmt.Sprintf("  %s 「复制%s」: 期待 %s，剪贴板 %s\n",
				yesNoWord(want != "" && got == want), cc.name, shortKey(want), shortKey(got)))
		}
	}

	// 追加到已有报告（可能与主窗口报告写在同一个文件里）
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(sb.String())

	// 自动关闭
	postMessage(d.hwnd, wmClose, 0, 0)
}

// pumpMessagesFor 手动处理一小段时间的消息（用于自检与初始化等待）。
//
// 必须用 **PeekMessage** 而不是 GetMessage：
// GetMessage 会阻塞到"下一条消息到达"为止，而 WM_TIMER 是 1 秒一次，
// 于是每次 pump 都要等将近一秒，自检会慢到不可用，
// 而且"点击后立刻观察效果"的时序全被打乱。
// PeekMessage 是非阻塞的：取空就退出循环，配合外层 deadline 才可控。
func pumpMessagesFor(d time.Duration) {
	var msg MSG
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for {
			r, _, _ := pPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, pmRemove)
			if r == 0 {
				break
			}
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var pPeekMessageW = user32.NewProc("PeekMessageW")

const pmRemove = 0x0001

func yesNoWord(ok bool) string {
	if ok {
		return "✅"
	}
	return "❌"
}

// shortKey 把密钥截短显示，报告里只关心"有没有变/一不一致"。
func shortKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(空)"
	}
	r := []rune(s)
	if len(r) <= 16 {
		return s
	}
	return string(r[:8]) + "…" + string(r[len(r)-4:])
}

// getClipboardText 读取剪贴板文本（用于验证"复制公钥"真的写进去了）。
func getClipboardText() string {
	ok, _, _ := pOpenClipboard.Call(0)
	if ok == 0 {
		return ""
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}
	ptr, _, _ := pGlobalLock.Call(h)
	if ptr == 0 {
		return ""
	}
	defer pGlobalUnlock.Call(h)
	return utf16String(unsafeSliceU16(ptr))
}

// unsafeSliceU16 把一个 NUL 结尾的 UTF-16 裸指针读成切片（长度扫到 NUL 为止）。
func unsafeSliceU16(ptr uintptr) []uint16 {
	var out []uint16
	for i := 0; i < 1<<16; i++ {
		c := *(*uint16)(unsafePtrAt(ptr, uintptr(i*2)))
		if c == 0 {
			break
		}
		out = append(out, c)
	}
	return out
}

// ctrlClientWidth 返回控件的客户区宽度（像素）。
// 用来断言"密钥框装得下 44 个字符"这类尺寸要求。
func ctrlClientWidth(c ctrl) int32 {
	if c == 0 {
		return 0
	}
	var r RECT
	pGetClientRect.Call(uintptr(c), uintptr(unsafe.Pointer(&r)))
	return r.Right - r.Left
}
