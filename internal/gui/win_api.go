//go:build windows

package gui

import (
	"unsafe"
)

// ---------------------------------------------------------------------------
// 窗口 / 消息 API 封装
// ---------------------------------------------------------------------------

func registerClassExW(wc *WNDCLASSEX) uint16 {
	wc.CbSize = uint32(unsafe.Sizeof(*wc))
	r, _, _ := pRegisterClassExW.Call(uintptr(unsafe.Pointer(wc)))
	return uint16(r)
}

func createWindowExW(exStyle uint32, class, title string, style uint32,
	x, y, w, h int32, parent uintptr, menu uintptr, hInst uintptr) uintptr {

	pClass, _ := utf16Ptr(class)
	pTitle, _ := utf16Ptr(title)
	r, _, _ := pCreateWindowExW.Call(
		uintptr(exStyle),
		uintptr(unsafe.Pointer(pClass)),
		uintptr(unsafe.Pointer(pTitle)),
		uintptr(style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, menu, hInst, 0)
	return r
}

func showWindow(hwnd uintptr, cmd int32) {
	pShowWindow.Call(hwnd, uintptr(cmd))
}

func updateWindow(hwnd uintptr) {
	pUpdateWindow.Call(hwnd)
}

func destroyWindow(hwnd uintptr) {
	pDestroyWindow.Call(hwnd)
}

func setForegroundWindow(hwnd uintptr) {
	pSetForegroundWindow.Call(hwnd)
}

func postQuitMessage(code int32) {
	pPostQuitMessage.Call(uintptr(code))
}

func postMessage(hwnd uintptr, msg uint32, wParam, lParam uintptr) {
	pPostMessageW.Call(hwnd, uintptr(msg), wParam, lParam)
}

func sendMessage(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	r, _, _ := pSendMessageW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func getDlgItem(hwnd uintptr, id int32) uintptr {
	r, _, _ := pGetDlgItem.Call(hwnd, uintptr(id))
	return r
}

func enableWindow(hwnd uintptr, enabled bool) {
	var v uintptr
	if enabled {
		v = 1
	}
	pEnableWindow.Call(hwnd, v)
}

func showControl(hwnd uintptr, visible bool) {
	cmd := int32(swHide)
	if visible {
		cmd = swShow
	}
	pShowWindow.Call(hwnd, uintptr(cmd))
}

func setFocus(hwnd uintptr) {
	pSetFocus.Call(hwnd)
}

func setForeground(hwnd uintptr) { pSetForegroundWindow.Call(hwnd) }

func messageBox(hwnd uintptr, text, caption string, flags uint32) int32 {
	pText, _ := utf16Ptr(text)
	pCap, _ := utf16Ptr(caption)
	r, _, _ := pMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(pText)), uintptr(unsafe.Pointer(pCap)), uintptr(flags))
	return int32(r)
}

func loadCursor(name *uint16) uintptr {
	r, _, _ := pLoadCursorW.Call(0, uintptr(unsafe.Pointer(name)))
	return r
}

func setTimer(hwnd uintptr, id uintptr, interval uint32) {
	pSetTimer.Call(hwnd, id, uintptr(interval), 0)
}

func killTimer(hwnd uintptr, id uintptr) {
	pKillTimer.Call(hwnd, id)
}

func invalidateRect(hwnd uintptr) {
	pInvalidateRect.Call(hwnd, 0, 0)
}

func getClientRect(hwnd uintptr) RECT {
	var r RECT
	pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

func moveWindow(hwnd uintptr, x, y, w, h int32) {
	pMoveWindow.Call(hwnd, uintptr(x), uintptr(y), uintptr(w), uintptr(h))
}

func setWindowPos(hwnd uintptr, x, y, w, h int32) {
	pSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), uintptr(swpNoZOrder|swpNoActivate))
}

func getWindowRect(hwnd uintptr) RECT {
	var r RECT
	pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

func adjustWindowRectEx(r *RECT, style uint32, menu bool, exStyle uint32) {
	b := uintptr(0)
	if menu {
		b = 1
	}
	pAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(r)), uintptr(style), b, uintptr(exStyle))
}

func setWindowText(hwnd uintptr, s string) {
	p, _ := utf16Ptr(s)
	pSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(p)))
}

func getWindowText(hwnd uintptr) string {
	const wmGetText = 0x000D
	buf := make([]uint16, 4096)
	pSendMessageW.Call(hwnd, uintptr(wmGetText), uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	return utf16String(buf)
}

func enableDarkTitleBar(hwnd uintptr) {
	enabled := int32(1)
	pDwmSetWindowAttribute.Call(hwnd, uintptr(dwmwaUseImmersiveDarkMode), uintptr(unsafe.Pointer(&enabled)), unsafe.Sizeof(enabled))
}

// setProcessDPIAware 让界面在高分屏上不发虚。
func setProcessDPIAware() {
	// PER_MONITOR_AWARE_V2 = -4
	if r, _, _ := pSetProcessDpiAwarenessContext.Call(^uintptr(3)); r != 0 {
		return
	}
	// Windows 8.1 回退：PER_MONITOR_DPI_AWARE = 2
	if r, _, _ := shcore.NewProc("SetProcessDpiAwareness").Call(2); r != 0 {
		return
	}
	// 更老系统回退到 system DPI aware
	user32.NewProc("SetProcessDPIAware").Call()
}

// ---------------------------------------------------------------------------
// GDI / 字体
// ---------------------------------------------------------------------------

// createFont 是 CreateFontW 的薄封装。
//
// CreateFontW 一共 14 个参数（最后一个是字体名），
// 之前这里多传了一个 family，导致**真正的字体名参数被错位**——
// 界面实际用的是回退字体，中文宽度比预期大得多，于是所有按钮/标签都超宽。
func createFont(height, weight int32, face string) uintptr {
	pFace, _ := utf16Ptr(face)
	const (
		defaultCharSet = 0
		outDefaultPrec = 0
		clipDefault    = 0
		cleartypeQual  = 5
		defaultPitch   = 0
	)
	r, _, _ := pCreateFontW.Call(
		uintptr(height), 0, 0, 0, uintptr(weight),
		0, 0, 0, // bItalic / bUnderline / bStrikeOut
		uintptr(defaultCharSet), uintptr(outDefaultPrec), uintptr(clipDefault),
		uintptr(cleartypeQual), uintptr(defaultPitch),
		uintptr(unsafe.Pointer(pFace)))
	return r
}

func deleteObject(obj uintptr) {
	pDeleteObject.Call(obj)
}

func getStockObject(index uint32) uintptr {
	r, _, _ := pGetStockObject.Call(uintptr(index))
	return r
}

func initCommonControls() {
	type initCommonControlsEx struct {
		DwSize uint32
		DwICC  uint32
	}
	icc := initCommonControlsEx{
		DwSize: uint32(unsafe.Sizeof(initCommonControlsEx{})),
		DwICC:  iccListViewClasses,
	}
	pInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))
}

// ---------------------------------------------------------------------------
// 颜色
// ---------------------------------------------------------------------------

type colorRef uint32

func rgb(r, g, b uint8) colorRef {
	return colorRef(uint32(r) | uint32(g)<<8 | uint32(b)<<16)
}

func (c colorRef) low() uintptr  { return uintptr(c & 0xFF) }
func (c colorRef) high() uintptr { return uintptr((c >> 16) & 0xFF) }

func setTextColor(hdc uintptr, c colorRef) {
	pSetTextColor.Call(hdc, uintptr(c))
}

func setBkMode(hdc uintptr, mode int32) {
	pSetBkMode.Call(hdc, uintptr(mode))
}

func createSolidBrush(c colorRef) uintptr {
	r, _, _ := pCreateSolidBrush.Call(uintptr(c))
	return r
}

func getSysColorBrush(idx uint32) uintptr {
	r, _, _ := pGetSysColorBrush.Call(uintptr(idx))
	return r
}

// getMessageW 取一条消息；返回 0 表示 WM_QUIT。
func getMessage(msg *MSG) int32 {
	r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(msg)), 0, 0, 0)
	return int32(r)
}

func translateMessage(msg *MSG) {
	pTranslateMessage.Call(uintptr(unsafe.Pointer(msg)))
}

func dispatchMessage(msg *MSG) {
	pDispatchMessageW.Call(uintptr(unsafe.Pointer(msg)))
}

func getModuleHandle() uintptr {
	h, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	return h
}

// ---------------------------------------------------------------------------
// ListView 封装
// ---------------------------------------------------------------------------

// lvSetText 写列表某行某列的文字。
func lvSetText(hwnd uintptr, item, sub int32, text string) {
	buf := utf16Slice(text)
	itemStruct := LVITEM{
		Mask:        lvifText,
		IItem:       item,
		ISubItem:    sub,
		PszText:     &buf[0],
		CCchTextMax: int32(len(buf)),
	}
	sendMessage(hwnd, lvmSetItemText, 0, uintptr(unsafe.Pointer(&itemStruct)))
}

// lvInsertRowAt 插入一个空行。
func lvInsertRowAt(hwnd uintptr, item int32) {
	empty := utf16Slice("")
	itemStruct := LVITEM{Mask: lvifText, IItem: item, PszText: &empty[0], CCchTextMax: 1}
	sendMessage(hwnd, lvmInsertItem, 0, uintptr(unsafe.Pointer(&itemStruct)))
}

const lvmInsertColumn = lvmFirst + 39

func lvSetColumn(hwnd uintptr, index int32, title string, width int32) {
	buf := utf16Slice(title)
	col := LVCOLUMN{
		Mask:// LVCF_FMT|LVCF_WIDTH|LVCF_TEXT|LVCF_SUBITEM
		// 注意 LVCF_SUBITEM = 0x0008；0x0010 是 LVCF_IMAGE，本程序没有 ImageList，不能开。
		0x0001 | 0x0002 | 0x0004 | 0x0008,
		Fmt:         lvcFmtLeft,
		Cx:          width,
		PszText:     &buf[0],
		CCchTextMax: int32(len(buf)),
		ISubItem:    index,
	}
	sendMessage(hwnd, lvmInsertColumn, uintptr(index), uintptr(unsafe.Pointer(&col)))
}

func lvSetColumnWidth(hwnd uintptr, index int32, width int32) {
	sendMessage(hwnd, lvmSetColumnWidth, uintptr(index), uintptr(width))
}

func lvDeleteAll(hwnd uintptr) {
	sendMessage(hwnd, lvmDeleteAllItems, 0, 0)
}

func lvGetItemCount(hwnd uintptr) int32 {
	return int32(sendMessage(hwnd, lvmGetItemCount, 0, 0))
}

func lvGetNextItem(hwnd uintptr, item int32, flags uint32) int32 {
	return int32(sendMessage(hwnd, lvmGetNextItem, uintptr(item), uintptr(flags)))
}

func lvEnsureVisible(hwnd uintptr, item int32) {
	sendMessage(hwnd, lvmEnsureVisible, uintptr(item), 0)
}

func lvGetExtendedStyle(hwnd uintptr) uintptr {
	return sendMessage(hwnd, 0x1017 /* LVM_GETEXTENDEDLISTVIEWSTYLE */, 0, 0)
}

func lvSetExtendedStyle(hwnd uintptr, style uintptr) {
	sendMessage(hwnd, lvmSetExtendedStyle, 0, style)
}

func lvSelectedItem(hwnd uintptr) int32 {
	return lvGetNextItem(hwnd, -1, 0x0002 /* LVNI_SELECTED */)
}

// ---------------------------------------------------------------------------
// 文字测量（布局体检用）
// ---------------------------------------------------------------------------

var (
	pGetDCZero    = user32.NewProc("GetDC")
	pReleaseDCAll = user32.NewProc("ReleaseDC")
)

type size struct{ CX, CY int32 }

func getDCZero(hwnd uintptr) uintptr {
	r, _, _ := pGetDCZero.Call(hwnd)
	return r
}

func releaseDCZero(hwnd uintptr, hdc uintptr) {
	if hdc != 0 {
		pReleaseDCAll.Call(hwnd, hdc)
	}
}

func selectObject(hdc, obj uintptr) uintptr {
	r, _, _ := pSelectObject.Call(hdc, obj)
	return r
}

// getTextExtentPoint32 量出字符串在指定 DC 上占的像素宽度。
func getTextExtentPoint32(hdc uintptr, s string) int {
	if hdc == 0 {
		return 0
	}
	p, _ := utf16Ptr(s)
	var sz size
	r, _, _ := pGetTextExtentPoint32W.Call(hdc, uintptr(unsafe.Pointer(p)), uintptr(len(s)), uintptr(unsafe.Pointer(&sz)))
	if r == 0 {
		return 0
	}
	return int(sz.CX)
}
