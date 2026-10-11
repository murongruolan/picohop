//go:build windows

package qrscan

import (
	"errors"
	"syscall"
	"testing"
)

// TestAllWin32ProcsResolve 把所有绑定的 Win32 过程名挨个 Find() 一遍。
//
// 【为什么必须有这个测试】这是一个真实外伤换来的：
// `pTextOutW` 曾经绑在 user32 上，而 TextOutW 实际在 gdi32 里。
// 这种错误**编译期完全看不出来**，只有真正调用到时，Go 的 LazyProc 才会在
// mustFind 里 panic，而 panic 发生在窗口过程里 —— 结果是**整个程序当场退出**。
//
// 当时的覆盖层测试全绿：它们用 hideOverlayForTest 让窗口保持隐藏来驱动消息，
// 隐藏窗口永远收不到 WM_PAINT，画尺寸标签那条路径从没被执行过。
// 也就是说"测试全绿"和"这条路径没坏"是两件事。
//
// 这个测试不碰屏幕、不建窗口、瞬时完成：它只是问一句
// "你声明的这个过程名在这个 DLL 里真的存在吗"。绑错 DLL 当场就暴露。
func TestAllWin32ProcsResolve(t *testing.T) {
	procs := []struct {
		name string
		proc *syscall.LazyProc
	}{
		{"RegisterClassExW", pRegisterClassExW},
		{"CreateWindowExW", pCreateWindowExW},
		{"DefWindowProcW", pDefWindowProcW},
		{"DestroyWindow", pDestroyWindow},
		{"ShowWindow", pShowWindow},
		{"UpdateWindow", pUpdateWindow},
		{"GetMessageW", pGetMessageW},
		{"TranslateMessage", pTranslateMessage},
		{"DispatchMessageW", pDispatchMessageW},
		{"PostQuitMessage", pPostQuitMessage},
		{"InvalidateRect", pInvalidateRect},
		{"BeginPaint", pBeginPaint},
		{"EndPaint", pEndPaint},
		{"GetClientRect", pGetClientRect},
		{"FillRect", pFillRect},
		{"SetCapture", pSetCapture},
		{"ReleaseCapture", pReleaseCapture},
		{"ShowCursor", pShowCursor},
		{"SetWindowLongPtrW", pSetWindowLongPtrW},
		{"GetWindowLongPtrW", pGetWindowLongPtrW},
		{"SystemParametersInfoW", pSystemParametersInfoW},
		{"StretchDIBits", pStretchDIBits},
		{"CreateFontW", pCreateFontW},
		{"GetDeviceCaps", pGetDeviceCaps},
		{"SetTextColor", pSetTextColor},
		{"SetBkMode", pSetBkMode},
		{"CreateSolidBrush", pCreateSolidBrush},
		{"TextOutW", pTextOutW},
		{"AlphaBlend", pAlphaBlend},
		{"SetLayeredWindowAttributes", pSetLayeredWindowAttributes},
		{"SetWindowRgn", pSetWindowRgn},
		{"CreateRectRgn", pCreateRectRgn},
		{"CombineRgn", pCombineRgn},
		// 截屏那边（screen_windows.go）
		{"GetSystemMetrics", pGetSystemMetrics},
		{"GetDC", pGetDC},
		{"ReleaseDC", pReleaseDC},
		{"SetProcessDpiAwarenessContext", pSetProcessDpiAwarenessContext},
		{"CreateCompatibleDC", pCreateCompatibleDC},
		{"CreateCompatibleBitmap", pCreateCompatibleBitmap},
		{"SelectObject", pSelectObject},
		{"DeleteObject", pDeleteObject},
		{"DeleteDC", pDeleteDC},
		{"BitBlt", pBitBlt},
		{"GetDIBits", pGetDIBits},
		// 窗口截图那边（window_windows.go）
		{"GetWindowRect", pGetWindowRect},
		{"PrintWindow", pPrintWindow},
	}
	for _, p := range procs {
		if err := p.proc.Find(); err != nil {
			var dllErr *syscall.DLLError
			where := "未知 DLL"
			if errors.As(err, &dllErr) {
				where = dllErr.ObjName
			}
			t.Errorf("过程 %s 在 %s 里找不到：%v\n"+
				"（多半是绑错了 DLL —— 比如 TextOutW/CreateSolidBrush 属于 gdi32 而不是 user32。\n"+
				" 这类错误编译期看不出来，只会在真正调用时 panic 掉整个程序。）",
				p.name, where, err)
		}
	}
}
