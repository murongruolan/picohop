//go:build windows

package gui

import (
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// ctrl 是窗口上一个子控件的句柄。
type ctrl uintptr

// id 为控件 ID（高 16 位为组编号，低 16 位为序号，便于区分不同区域的同名控件）。
type id int32

func groupID(g int32) id   { return id(g) << 16 }
func groupIDOf(v id) int32 { return int32(v) >> 16 }

func lowPart(v id) int32 { return int32(v) & 0xFFFF }

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
	fRegular uintptr
	fBold    uintptr
	fMono    uintptr
}

func (l layout) font() uintptr { return l.fRegular }

func (l layout) make(c control, x, y, w, h int32) ctrl {
	hwnd := createWindowExW(c.Ex, c.Class, c.Text, c.Style, x, y, w, h, l.parent, uintptr(c.ID), l.inst)
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

func (c ctrl) comboText() string {
	if c == 0 {
		return ""
	}
	buf := make([]uint16, 512)
	n := sendMessage(uintptr(c), 0x0148 /* CB_GETLBTEXT */, 0, uintptr(unsafe.Pointer(&buf[0])))
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
	sendMessage(uintptr(c), 0x0177 /* EM_REPLACESEL */, 1, uintptr(unsafe.Pointer(&buf[0])))
}

func (c ctrl) selectAll() {
	if c == 0 {
		return
	}
	sendMessage(uintptr(c), 0x00B1 /* EM_SETSEL */, 0, uintptr(^uintptr(0)))
}

func (c ctrl) lineCount() int32 {
	if c == 0 {
		return 0
	}
	return int32(sendMessage(uintptr(c), 0x00BA /* EM_GETLINECOUNT */, 0, 0))
}

func (c ctrl) scrollToEnd() {
	if c == 0 {
		return
	}
	sendMessage(uintptr(c), 0x0115 /* EM_SCROLLCARET */, 0, 0)
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
