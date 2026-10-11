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
	procSetWindowLongPtrW        = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
)

// 把浮动窗口设为主窗口的附属窗口要用到的常量。
const (
	// GWLP_HWNDPARENT：设置"所有者窗口"（注意名字叫 parent，语义是 owner）
	gwlpHWNDParent = ^uintptr(7) // -8
	// HWND_NOTOPMOST
	hwndNotTopmost = ^uintptr(1) // -2
	// SetWindowPos 的 flags：不动位置尺寸、不激活
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010
)

// DWMWA_USE_IMMERSIVE_DARK_MODE。20 是正式文档值（更早的预览版是 19）。
const dwmwaUseImmersiveDarkMode = 20

// HKEY_CURRENT_USER
const hkeyCurrentUser = 0x80000001

// RRF_RT_REG_DWORD（RegGetValueW 的取值类型限制）
const rrfRTRegDword = 0x00000010

// applyImmersiveDarkMode 把本进程里标题匹配的顶层窗口设成亮/暗标题栏。
func applyImmersiveDarkMode(title string, dark bool) bool {
	hwnd := findOwnWindow(title)
	if hwnd == 0 {
		return false
	}
	v := uint32(0)
	if dark {
		v = 1
	}
	procDwmSetWindowAttribute.Call(hwnd,
		uintptr(dwmwaUseImmersiveDarkMode),
		uintptr(unsafe.Pointer(&v)),
		unsafe.Sizeof(v))
	return true
}

// findOwnWindow 在本进程的顶层窗口里按标题找一个，返回句柄（找不到返回 0）。
//
// 为什么要自己找：v3 没有"按名字取 HWND"的公开 API，而有两件事非拿到句柄不可 ——
// 运行期切标题栏明暗（v3 没有 setter）、以及把浮动窗口设为**主窗口的附属窗口**。
// 只枚举本进程的窗口，不会误伤别的程序。
func findOwnWindow(title string) uintptr {
	pid := uint32(os.Getpid())
	found := uintptr(0)
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
		found = hwnd
		return 0 // 找到了，停止枚举
	})
	procEnumWindows.Call(cb, 0)
	return found
}

// setWindowOwner 把 child 设为 owner 的**附属窗口**（owned window）。
//
// 这是"浮动弹窗只比主窗口高一级"的正解：
//   - 附属窗口永远在它的所有者之上（不会被主窗口盖住）
//   - 但**不会**盖住别的程序的窗口（那才是 AlwaysOnTop 的行为，用户明确说太高了）
//   - 所有者最小化时它跟着消失，所有者关闭时它一起销毁 —— 都符合直觉
//
// 只传 SetWindowPos 的 SWP_NOACTIVATE|SWP_NOMOVE|SWP_NOSIZE 是为了让 z 序立即生效，
// 不然要等下一次窗口层级变化才看得到效果。
func setWindowOwner(child, owner uintptr) bool {
	if child == 0 || owner == 0 {
		return false
	}
	prev, _, _ := procSetWindowLongPtrW.Call(child, uintptr(gwlpHWNDParent), owner)
	// 返回 0 可能是"原来没有所有者"（正常），也可能是失败，所以再看一次 LastError 没必要 ——
	// 用一次 SetWindowPos 让新层级生效，成功与否以它为准。
	r, _, _ := procSetWindowPos.Call(child, hwndNotTopmost,
		0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoActivate)
	_ = prev
	return r != 0
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
