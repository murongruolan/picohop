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
