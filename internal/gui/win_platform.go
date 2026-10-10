//go:build windows

package gui

import (
	"fmt"
	"os/exec"

	"chainproxy/internal/netutil"
)

// checkPortFree 是 netutil.PortAvailable 的 GUI 侧包装。
func checkPortFree(host string, port int) error { return netutil.PortAvailable(host, port) }

// openExplorer 用系统文件管理器打开目录。
func openExplorer(dir string) error {
	return exec.Command("explorer.exe", dir).Start()
}

// ---------------------------------------------------------------------------
// 剪贴板（纯 Win32 API，避免拉 golang.design/x/clipboard 依赖）
// ---------------------------------------------------------------------------

var (
	pOpenClipboard    = user32.NewProc("OpenClipboard")
	pCloseClipboard   = user32.NewProc("CloseClipboard")
	pEmptyClipboard   = user32.NewProc("EmptyClipboard")
	pSetClipboardData = user32.NewProc("SetClipboardData")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
	gmemZeroInit  = 0x0040
)

var (
	pGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	pGlobalLock   = kernel32.NewProc("GlobalLock")
	pGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	pGlobalFree   = kernel32.NewProc("GlobalFree")
)

// putClipboard 把文本写入系统剪贴板。
func putClipboard(text string) error {
	if len(text) == 0 {
		return fmt.Errorf("内容为空")
	}
	// Win32 要求带终止符
	runes := utf16Slice(text)
	byteLen := uintptr(len(runes) * 2)

	hmem, _, _ := pGlobalAlloc.Call(gmemMoveable|gmemZeroInit, byteLen)
	if hmem == 0 {
		return fmt.Errorf("分配剪贴板内存失败")
	}
	ptr, _, _ := pGlobalLock.Call(hmem)
	if ptr == 0 {
		pGlobalFree.Call(hmem)
		return fmt.Errorf("锁定剪贴板内存失败")
	}
	copyWString(ptr, runes)
	pGlobalUnlock.Call(hmem)

	ok, _, _ := pOpenClipboard.Call(0)
	if ok == 0 {
		pGlobalFree.Call(hmem)
		return fmt.Errorf("无法打开剪贴板")
	}
	pEmptyClipboard.Call()
	set, _, _ := pSetClipboardData.Call(cfUnicodeText, hmem)
	pCloseClipboard.Call()
	if set == 0 {
		pGlobalFree.Call(hmem)
		return fmt.Errorf("写入剪贴板失败")
	}
	// 成功时系统接管这块内存，不能再 Free
	return nil
}

// copyWString 把 []uint16 写到裸内存地址。
func copyWString(dst uintptr, src []uint16) {
	for i, v := range src {
		*(*uint16)(unsafePtrAt(dst, uintptr(i*2))) = v
	}
}
