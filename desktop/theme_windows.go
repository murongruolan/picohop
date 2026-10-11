//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// Wails v3 **没有运行期切换标题栏明暗的 API**：明暗只能在创建窗口时用
// WindowsWindow{Theme: ...} 指定（内部实现方法叫 setTheme，没有导出），
// v2 的 runtime.WindowSetDarkTheme 在 v3 里没有对应物。
//
// 但"内容切成深色、标题栏还是白的"这种割裂感必须避免，所以这里直接调
// DwmSetWindowAttribute(DWMWA_USE_IMMERSIVE_DARK_MODE) —— 这是微软文档里的
// 正式做法，不属于 hack。
//
// 找窗口的办法：只枚举**本进程**的顶层窗口，再比对标题。
// 限本进程是有意的，免得误改别的程序。

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procDwmSetWindowAttribute    = dwmapi.NewProc("DwmSetWindowAttribute")
	procRegGetValueW             = advapi32.NewProc("RegGetValueW")
)

// DWMWA_USE_IMMERSIVE_DARK_MODE。20 是正式文档值（更早的预览版是 19）。
const dwmwaUseImmersiveDarkMode = 20

// HKEY_CURRENT_USER
const hkeyCurrentUser = 0x80000001

// RRF_RT_REG_DWORD（RegGetValueW 的取值类型限制）
const rrfRTRegDword = 0x00000010

// applyImmersiveDarkMode 把本进程里标题匹配的顶层窗口设成亮/暗标题栏。
func applyImmersiveDarkMode(title string, dark bool) bool {
	pid := uint32(os.Getpid())
	found := false

	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var wpid uint32
		procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&wpid)))
		if wpid != pid {
			return 1 // 不是我们的窗口，继续枚举
		}
		buf := make([]uint16, 512)
		n, _, _ := procGetWindowTextW.Call(hwnd,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n == 0 {
			return 1
		}
		if syscall.UTF16ToString(buf[:n]) != title {
			return 1
		}
		v := uint32(0)
		if dark {
			v = 1
		}
		procDwmSetWindowAttribute.Call(hwnd,
			uintptr(dwmwaUseImmersiveDarkMode),
			uintptr(unsafe.Pointer(&v)),
			unsafe.Sizeof(v))
		found = true
		return 0 // 找到了，停止枚举
	})
	procEnumWindows.Call(cb, 0)
	return found
}

// systemIsDark 读注册表判断系统是否处于深色模式。
//
// AppsUseLightTheme = 1 是浅色、0 是深色。用 syscall 直接问 advapi32，
// 免得为了一个布尔值去引 golang.org/x/sys。
func systemIsDark() bool {
	key, err := syscall.UTF16PtrFromString(
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`)
	if err != nil {
		return false
	}
	name, err := syscall.UTF16PtrFromString("AppsUseLightTheme")
	if err != nil {
		return false
	}
	var val uint32
	size := uint32(unsafe.Sizeof(val))
	r, _, _ := procRegGetValueW.Call(
		uintptr(hkeyCurrentUser),
		uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(name)),
		uintptr(rrfRTRegDword),
		0,
		uintptr(unsafe.Pointer(&val)),
		uintptr(unsafe.Pointer(&size)),
	)
	if r != 0 {
		return false
	}
	return val == 0
}
