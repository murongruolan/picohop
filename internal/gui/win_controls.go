//go:build windows

package gui

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// ctrl 是窗口上一个子控件的句柄。
type ctrl uintptr

// id 是控件 ID。
//
// ⚠️ 硬性约束：**控件 ID 必须 <= 0xFFFF（65535）**。
// 原因：按钮点击产生的 WM_COMMAND 里，wParam = MAKEWPARAM(控件ID, 通知码)，
// 而 MAKEWPARAM 只能携带 **低 16 位**的控件 ID。
// 早先这里用 "组<<16 | 序号" 生成 ID（例如 0x1000A），
// GetDlgItem 找得到控件，但通知到达时 cid 会退化成 0x0A，
// 于是每个按钮都"点了没反应"——这是个非常隐蔽的坑。
// 现在用 checkIDOK 在创建控件时就把越界的 ID 直接 panic 出来。
type id int32

// maxControlID 是 Win32 通知机制允许的最大控件 ID。
const maxControlID = 0xFFFF

// groupID 按"组 × 100 + 序号"分配 ID，保证落在 16 位内且互不冲突。
func groupID(g int32) id { return id(g) * 100 }

// groupIDOf 返回组编号。
func groupIDOf(v id) int32 { return int32(v) / 100 }

func lowPart(v id) int32 { return int32(v) & 0xFFFF }

// checkIDOK 在创建控件前校验 ID 是否落在 16 位范围内。
func checkIDOK(cid id) id {
	if cid <= 0 || cid > maxControlID {
		panic(fmt.Sprintf(
			"控件 ID %d(0x%X) 超出 16 位范围：WM_COMMAND 的 wParam 只能携带控件 ID 的低 16 位，"+
				"越界会导致按钮永远收不到通知（表现为点击无反应）。", int32(cid), uint32(cid)))
	}
	return cid
}

// ---------------------------------------------------------------------------
// 控件创建
// ---------------------------------------------------------------------------

type control struct {
	Class string
	Text  string
	Style uint32
	Ex    uint32
	ID    id
}

type layout struct {
	parent   uintptr
	inst     uintptr
	dpi      int32
	fRegular uintptr
	fBold    uintptr
	fMono    uintptr
}

// s 按当前 DPI 换算设计尺寸（96 DPI 为基准）。
func (l layout) s(v int32) int32 { return scaleDPI(v, l.dpi) }

func (l layout) font() uintptr { return l.fRegular }

func (l layout) make(c control, x, y, w, h int32) ctrl {
	hwnd := createWindowExW(c.Ex, c.Class, c.Text, c.Style, x, y, w, h, l.parent, uintptr(checkIDOK(c.ID)), l.inst)
	if hwnd == 0 {
		return 0
	}
	if l.fRegular != 0 {
		sendMessage(hwnd, 0x0030 /* WM_SETFONT */, l.fRegular, 1 /* TRUE */)
	}
	return ctrl(hwnd)
}

func (l layout) label(text string, cid id, x, y, w, h int32) ctrl {
	return l.make(control{Class: "STATIC", Text: text, Style: wsChild | wsVisible | ssLeft, ID: cid}, x, y, w, h)
}

func (l layout) labelRight(text string, cid id, x, y, w, h int32) ctrl {
	return l.make(control{Class: "STATIC", Text: text, Style: wsChild | wsVisible | ssRight, ID: cid}, x, y, w, h)
}

func (l layout) bold(text string, cid id, x, y, w, h int32) ctrl {
	hwnd := l.make(control{Class: "STATIC", Text: text, Style: wsChild | wsVisible | ssLeft, ID: cid}, x, y, w, h)
	sendMessage(uintptr(hwnd), 0x0030, l.fBold, 1)
	return hwnd
}

func (l layout) button(text string, cid id, x, y, w, h int32) ctrl {
	return l.make(control{Class: "BUTTON", Text: text, Style: wsChild | wsVisible | wsTabStop | bsPushButton, ID: cid}, x, y, w, h)
}

func (l layout) defButton(text string, cid id, x, y, w, h int32) ctrl {
	return l.make(control{Class: "BUTTON", Text: text, Style: wsChild | wsVisible | wsTabStop | bsDefPushButton, ID: cid}, x, y, w, h)
}

func (l layout) checkbox(text string, cid id, x, y, w, h int32) ctrl {
	return l.make(control{Class: "BUTTON", Text: text, Style: wsChild | wsVisible | wsTabStop | bsAutoCheckBox, ID: cid}, x, y, w, h)
}

func (l layout) group(text string, cid id, x, y, w, h int32) ctrl {
	return l.make(control{Class: "BUTTON", Text: text, Style: wsChild | wsVisible | bsGroupBox, ID: cid}, x, y, w, h)
}

func (l layout) edit(cid id, x, y, w, h int32, extra uint32) ctrl {
	return l.make(control{
		Class: "EDIT",
		Style: wsChild | wsVisible | wsTabStop | wsBorder | wsExClientEdge | esLeft | extra,
		Ex:    wsExClientEdge,
		ID:    cid,
	}, x, y, w, h)
}

func (l layout) multiline(cid id, x, y, w, h int32, extra uint32) ctrl {
	return l.make(control{
		Class: "EDIT",
		Style: wsChild | wsVisible | wsBorder | wsVScroll | esLeft | esMultiline | esAutoVScroll | extra,
		Ex:    wsExClientEdge,
		ID:    cid,
	}, x, y, w, h)
}

func (l layout) combo(cid id, items []string, x, y, w, h int32) ctrl {
	hwnd := l.make(control{
		Class: "COMBOBOX",
		Style: wsChild | wsVisible | wsTabStop | cbsDropdownList | wsVScroll,
		ID:    cid,
	}, x, y, w, h)
	for _, it := range items {
		buf := utf16Slice(it)
		sendMessage(uintptr(hwnd), 0x0143 /* CB_ADDSTRING */, 0, uintptr(unsafe.Pointer(&buf[0])))
	}
	return hwnd
}

// ---------------------------------------------------------------------------
// 控件读写
// ---------------------------------------------------------------------------

func (c ctrl) text() string {
	if c == 0 {
		return ""
	}
	return getWindowText(uintptr(c))
}

func (c ctrl) setText(s string) {
	if c != 0 {
		setWindowText(uintptr(c), s)
	}
}

func (c ctrl) checked() bool {
	if c == 0 {
		return false
	}
	return sendMessage(uintptr(c), 0x00F0 /* BM_GETCHECK */, 0, 0) == 1
}

func (c ctrl) setChecked(v bool) {
	if c == 0 {
		return
	}
	var b uintptr
	if v {
		b = 1
	}
	sendMessage(uintptr(c), 0x00F1 /* BM_SETCHECK */, b, 0)
}

func (c ctrl) enabled() bool {
	if c == 0 {
		return false
	}
	return sendMessage(uintptr(c), 0x000A /* WM_ENABLE */, 0, 0) != 0
}

func (c ctrl) setEnabled(v bool) {
	if c != 0 {
		enableWindow(uintptr(c), v)
	}
}

func (c ctrl) setVisible(v bool) {
	if c != 0 {
		showControl(uintptr(c), v)
	}
}

func (c ctrl) visible() bool {
	if c == 0 {
		return false
	}
	r, _, _ := pIsWindowVisible.Call(uintptr(c))
	return r != 0
}

func (c ctrl) focus() {
	if c != 0 {
		setFocus(uintptr(c))
	}
}

func (c ctrl) textPtr(maxLen int) *uint16 {
	buf := make([]uint16, maxLen)
	sendMessage(uintptr(c), 0x000D /* WM_GETTEXT */, uintptr(maxLen), uintptr(unsafe.Pointer(&buf[0])))
	return &buf[0]
}

// comboIndex 读当前选项序号，-1 表示无选择。
func (c ctrl) comboIndex() int32 {
	if c == 0 {
		return -1
	}
	return int32(sendMessage(uintptr(c), 0x0147 /* CB_GETCURSEL */, 0, 0))
}

func (c ctrl) comboSelect(i int32) {
	if c == 0 {
		return
	}
	sendMessage(uintptr(c), 0x014E /* CB_SETCURSEL */, uintptr(i), 0)
}

// comboText 返回下拉框**当前选中项**的文字。
//
// ⚠️ CB_GETLBTEXT 的 wParam 是"项下标"，不是"从哪个位置开始读"。
// 早先这里硬编码了下标 0，于是无论用户选了什么，取回来的永远是第一项 ——
// 后果非常严重：前置类型永远被读成 socks5（选"直连"不生效），
// 住宅代理类型也永远被存成 socks5（选 http 或 wireguard 全都会变成 socks5）。
func (c ctrl) comboText() string {
	if c == 0 {
		return ""
	}
	idx := sendMessage(uintptr(c), cbGetCurSel, 0, 0)
	if int32(idx) < 0 {
		return "" // 没有选中任何一项
	}
	buf := make([]uint16, 512)
	n := sendMessage(uintptr(c), cbGetLbText, idx, uintptr(unsafe.Pointer(&buf[0])))
	if n == 0 {
		return ""
	}
	return utf16String(buf)
}

func (c ctrl) addText(s string) {
	if c == 0 {
		return
	}
	buf := utf16Slice(s)
	sendMessage(uintptr(c), emReplaceSel, 1, uintptr(unsafe.Pointer(&buf[0])))
}

func (c ctrl) selectAll() {
	if c == 0 {
		return
	}
	sendMessage(uintptr(c), emSetSel, 0, uintptr(^uintptr(0)))
}

func (c ctrl) lineCount() int32 {
	if c == 0 {
		return 0
	}
	return int32(sendMessage(uintptr(c), emGetLineCount, 0, 0))
}

func (c ctrl) scrollToEnd() {
	if c == 0 {
		return
	}
	sendMessage(uintptr(c), emScrollCaret, 0, 0)
}

// ---------------------------------------------------------------------------
// 数值便捷读写
// ---------------------------------------------------------------------------

func (c ctrl) intValue(def int) int {
	s := strings.TrimSpace(c.text())
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func (c ctrl) setIntValue(v int) {
	c.setText(strconv.Itoa(v))
}

func (c ctrl) font(f uintptr) {
	if c != 0 && f != 0 {
		sendMessage(uintptr(c), 0x0030, f, 1)
	}
}

// errno 是最后一次调用的错误码。
func errno() uintptr {
	if e, ok := syscall.GetLastError().(syscall.Errno); ok {
		return uintptr(e)
	}
	return 0
}

// getCtrlByID 在父窗口下按控件 ID 找子控件（自检与布局体检用）。
func getCtrlByID(parent uintptr, cid id) ctrl {
	return ctrl(getDlgItem(parent, int32(cid)))
}
