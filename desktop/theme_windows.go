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
	procSetWindowDisplayAffinity = user32.NewProc("SetWindowDisplayAffinity")
	procDwmFlush                 = dwmapi.NewProc("DwmFlush")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procRegGetValueW             = advapi32.NewProc("RegGetValueW")
	procSetWindowLongPtrW        = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
)

// SetWindowDisplayAffinity 的取值。
//
// WDA_EXCLUDEFROMCAPTURE（0x11，Windows 10 2004+）：窗口在屏幕捕获里直接不可见。
// 圈选前把主窗口排除掉，就不用隐藏它、也不用等它消失 ——
// 那 280ms 的等待是用户能明显感觉到的卡顿（实测从 ~340ms 降到 ~60ms）。
const (
	wdaNone               = 0x00000000
	wdaExcludeFromCapture = 0x00000011
)

// 把浮动窗口设为主窗口的附属窗口要用到的常量。
const (
	// GWLP_HWNDPARENT：设置"所有者窗口"（注意名字叫 parent，语义是 owner）
	gwlpHWNDParent = ^uintptr(7) // -8
	// HWND_NOTOPMOST
	hwndNotTopmost = ^uintptr(1) // -2
	// SetWindowPos 的 flags：不动位置尺寸、不激活
	swpNoSize     = 0x0001
	swpNoZOrder   = 0x0004
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

// setWindowCaptureExclusion 把窗口从**屏幕捕获**里排除/恢复。
//
// 用 WDA_EXCLUDEFROMCAPTURE：被排除的窗口在 BitBlt 之类的屏幕捕获里直接看不见。
// 圈选前把主窗口排除掉，就**不用把它隐藏起来**，于是也没有"等窗口真的消失"
// 那 280ms —— 用户感觉是"点了就有反应"。
//
// 返回 false 表示系统不支持，调用方应退回"隐藏窗口"的老路。
func setWindowCaptureExclusion(hwnd uintptr, exclude bool) bool {
	if hwnd == 0 {
		return false
	}
	v := uintptr(wdaNone)
	if exclude {
		v = wdaExcludeFromCapture
	}
	r, _, _ := procSetWindowDisplayAffinity.Call(hwnd, v)
	return r != 0
}

// raiseMainWindowOnce 把主窗口**临时**提到最前，但**不做置顶**。
//
// 用户的原话："将主窗口临时提到所有窗口上面，就提一下，不固定"。
// 所以用 SetWindowPos(HWND_TOP) 而不是 HWND_TOPMOST ——
// 前者只是这一次把它挪到 z 序最前，后者会留下 WS_EX_TOPMOST 永久压住别的程序。
func raiseMainWindowOnce() bool {
	hwnd := findOwnWindow(mainWindowTitle)
	if hwnd == 0 {
		return false
	}
	procSetWindowPos.Call(hwnd, 0 /* HWND_TOP */, 0, 0, 0, 0,
		uintptr(swpNoMove|swpNoSize))
	// 只提到最前还不够：当前前台若是别的程序，用户可能仍看不到它。
	// SetForegroundWindow 受前台锁限制（后台进程不一定抢得到），
	// 所以失败不当错误 —— 上一步已经让它排到最前了。
	procSetForegroundWindow.Call(hwnd)
	return true
}

// dwmFlush 等桌面合成器把当前这一帧真正画完。
//
// 用途：隐藏主窗口之后，屏幕内容不会立刻更新（Hide 只是投递消息），
// 早先的做法是 `time.Sleep(280ms)` 硬等 —— 那是整条圈选链路里最大的一块延迟。
// DwmFlush 是等到"这一帧合成完成"，比瞎等精确也快得多。
//
// 它失败（比如 DWM 被关掉）不算错误，调用方照常往下走。
func dwmFlush() {
	procDwmFlush.Call()
}

// parkMainWindowOffscreen 把主窗口临时挪到虚拟桌面之外，返回原来的位置。
//
// 【为什么不直接 Hide()】DWM 会给"隐藏窗口"做一个约 200ms 的**淡出动画**，
// 而截屏是在动画中间拍的 —— 实测拍到的是一块半透明的黑板（窗口标题栏还在）。
// 早先那句 `time.Sleep(280ms)` 实际上就是在等这个动画演完。
//
// 挪窗口没有动画：位置一变，下一帧就是最终状态。所以既快（几十毫秒）又干净。
// -32000 远超任何真实显示器，屏幕捕获（只覆盖虚拟桌面矩形）拍不到它。
func parkMainWindowOffscreen() (int, int, bool) {
	hwnd := findOwnWindow(mainWindowTitle)
	if hwnd == 0 {
		return 0, 0, false
	}
	var rc rectT
	if r, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc))); r == 0 {
		return 0, 0, false
	}
	x, y := int(rc.Left), int(rc.Top)
	// 用变量而不是常量：常量负数转 uintptr 会被编译器判为溢出，
	// 而 Win32 的坐标本来就是有符号的，运行时按位传过去正是我们要的。
	offX, offY := int64(-32000), int64(-32000)
	r, _, _ := procSetWindowPos.Call(hwnd, 0,
		uintptr(offX), uintptr(offY), 0, 0,
		uintptr(swpNoSize|swpNoZOrder|swpNoActivate))
	if r == 0 {
		return 0, 0, false
	}
	return x, y, true
}

// restoreWindowPos 把窗口挪回原来的位置。
func restoreWindowPos(x, y int) {
	hwnd := findOwnWindow(mainWindowTitle)
	if hwnd == 0 {
		return
	}
	procSetWindowPos.Call(hwnd, 0,
		uintptr(int32(x)), uintptr(int32(y)), 0, 0,
		uintptr(swpNoSize|swpNoZOrder|swpNoActivate))
}

// rectT 是 Win32 的 RECT（这里只用到左上角）。
type rectT struct {
	Left, Top, Right, Bottom int32
}
