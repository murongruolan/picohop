// Command uishot 是开发期用的"界面截图"小工具。
//
// 为什么需要它：这是个桌面程序，界面好不好看、对话框有没有错位，
// 靠读代码是判断不出来的，必须真的截一张图看一眼。
//
// 用法：
//
//	go run ./cmd/uishot -list                              # 列出可见窗口标题
//	go run ./cmd/uishot -title ChainProxy -out shot.png     # 截指定窗口的客户区
//
// 【为什么没有"模拟点击"】【重要】
// 早期版本里做过真实鼠标注入（SetCursorPos + mouse_event）来点开对话框，
// 结果是：一旦被测窗口失去前台焦点，这些点击就会落到**用户当时正在用的
// 别的程序**上 —— 实测确实发生过。这是不可接受的副作用，所以整套输入注入
// 已经删掉。
//
// 现在要截某个对话框，改用程序自己的启动参数（见 desktop/main.go 的 -open），
// 让界面自己把对话框打开，属于纯只读操作，不会去碰用户任何其它窗口。
//
// 截图能力直接复用 internal/qrscan（它已经有一套验证过的 GDI 截图实现）。
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"chainproxy/internal/qrscan"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")

	procEnumWindows      = user32.NewProc("EnumWindows")
	procGetWindowTextW   = user32.NewProc("GetWindowTextW")
	procGetWindowRect    = user32.NewProc("GetWindowRect")
	procGetClientRect    = user32.NewProc("GetClientRect")
	procClientToScreen   = user32.NewProc("ClientToScreen")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")
	procSetForegroundWin = user32.NewProc("SetForegroundWindow")
	procShowWindow       = user32.NewProc("ShowWindow")
	procPostMessageW     = user32.NewProc("PostMessageW")
)

const swRestore = 9

type rect struct {
	Left, Top, Right, Bottom int32
}

type point struct {
	X, Y int32
}

type windowInfo struct {
	hwnd  uintptr
	title string
	rect  rect
}

func windowTitle(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

func windowRect(hwnd uintptr) (rect, bool) {
	var r rect
	ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r, ok != 0
}

// clientRectScreen 返回窗口**客户区**的屏幕坐标。
//
// 为什么要专门算这个：GetWindowRect 包含 Windows 的隐形缩放边框
// （每个方向约 7-8 像素），直接按它截图会把窗口后面的其它程序也截进来，
// 看起来就像界面上多了几条奇怪的滚动条 —— 排查半天才发现是这个原因。
func clientRectScreen(hwnd uintptr) (rect, bool) {
	var cr rect
	ok, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&cr)))
	if ok == 0 {
		return rect{}, false
	}
	origin := point{0, 0}
	ok2, _, _ := procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&origin)))
	if ok2 == 0 {
		return rect{}, false
	}
	return rect{
		Left:   origin.X,
		Top:    origin.Y,
		Right:  origin.X + cr.Right,
		Bottom: origin.Y + cr.Bottom,
	}, true
}

func visible(hwnd uintptr) bool {
	ok, _, _ := procIsWindowVisible.Call(hwnd)
	return ok != 0
}

func enumWindows() []windowInfo {
	var out []windowInfo
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if !visible(hwnd) {
			return 1
		}
		title := windowTitle(hwnd)
		if strings.TrimSpace(title) == "" {
			return 1
		}
		r, ok := windowRect(hwnd)
		if !ok || r.Right <= r.Left || r.Bottom <= r.Top {
			return 1
		}
		out = append(out, windowInfo{hwnd: hwnd, title: title, rect: r})
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return out
}

func main() {
	title := flag.String("title", "", "窗口标题（子串匹配，不区分大小写）")
	out := flag.String("out", "", "输出 PNG 路径")
	list := flag.Bool("list", false, "只列出可见窗口标题")
	wait := flag.Int("wait", 0, "等待窗口出现的秒数")
	pad := flag.Int("pad", 0, "截图时四周留白像素")
	rect := flag.String("rect", "", "截取屏幕区域，格式 x,y,w,h（用于拍摄任务栏托盘等不属于任何窗口的东西）")
	postdrag := flag.String("postdrag", "", "向该标题的窗口投递一组鼠标拖拽消息（自测用，完全不碰真实鼠标）")
	flag.Parse()

	// 【为什么用 PostMessage 而不是真的动鼠标】
	// 圈选遮罩那条交互路径必须被真实驱动一次才能确认它没问题，但真实鼠标注入
	// 会在焦点丢失时点到用户的其它程序上（本项目已彻底禁用）。
	// PostMessage 走的是同一条窗口消息路径，只是不经过物理鼠标。
	if *postdrag != "" {
		if *rect == "" {
			fmt.Fprintln(os.Stderr, "-postdrag 需要 -rect 给出屏幕坐标 x,y,w,h")
			os.Exit(2)
		}
		r, err := parseRect(*rect)
		if err != nil {
			fmt.Fprintf(os.Stderr, "解析 -rect 失败：%v\n", err)
			os.Exit(2)
		}
		if err := postDrag(*postdrag, r); err != nil {
			fmt.Fprintf(os.Stderr, "投递拖拽消息失败：%v\n", err)
			os.Exit(1)
		}
		fmt.Printf("已向 %q 投递拖拽：屏幕 (%d,%d) → (%d,%d)\n",
			*postdrag, r.Min.X, r.Min.Y, r.Max.X, r.Max.Y)
		return
	}

	if *rect != "" {
		r, err := parseRect(*rect)
		if err != nil {
			fmt.Fprintf(os.Stderr, "解析 -rect 失败：%v\n", err)
			os.Exit(2)
		}
		if *out == "" {
			fmt.Fprintln(os.Stderr, "需要 -out 指定输出路径")
			os.Exit(2)
		}
		img, err := qrscan.CaptureRect(r)
		if err != nil {
			fmt.Fprintf(os.Stderr, "截屏失败：%v\n", err)
			os.Exit(1)
		}
		if err := savePNG(*out, img); err != nil {
			fmt.Fprintf(os.Stderr, "写文件失败：%v\n", err)
			os.Exit(1)
		}
		fmt.Printf("已截取屏幕区域 %v → %s（%dx%d）\n", r, *out, img.Bounds().Dx(), img.Bounds().Dy())
		return
	}

	if *list {
		for _, w := range enumWindows() {
			fmt.Printf("%-10x  %4dx%-4d  @(%d,%d)  %s\n", w.hwnd,
				w.rect.Right-w.rect.Left, w.rect.Bottom-w.rect.Top,
				w.rect.Left, w.rect.Top, w.title)
		}
		return
	}
	if *title == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "需要 -title 与 -out（或用 -list 列出窗口）")
		os.Exit(2)
	}

	needle := strings.ToLower(*title)
	deadline := time.Now().Add(time.Duration(*wait) * time.Second)

	var target *windowInfo
	for {
		for _, w := range enumWindows() {
			if strings.Contains(strings.ToLower(w.title), needle) {
				ww := w
				target = &ww
				break
			}
		}
		if target != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if target == nil {
		fmt.Fprintf(os.Stderr, "没有找到标题包含 %q 的可见窗口\n", *title)
		os.Exit(1)
	}

	// 【不抢焦点】用 PrintWindow 让窗口自己渲染，因此不需要 SetForegroundWindow。
	// 抢焦点会打断正在用电脑的人，而且一旦抢不到，截到的就是压在它上面的别的程序
	// （实测踩过：截出来是用户正在看的浏览器页面）。
	img, err := qrscan.CaptureWindow(target.hwnd)
	if err != nil || qrscan.LooksBlank(img) {
		if err == nil {
			err = fmt.Errorf("PrintWindow 返回的画面基本是同一片颜色（多半是硬件叠加层导致的空白）")
		}
		fmt.Fprintf(os.Stderr, "  窗口截图不可用（%v），退回截屏幕区域\n", err)
		procShowWindow.Call(target.hwnd, swRestore)
		procSetForegroundWin.Call(target.hwnd)
		time.Sleep(700 * time.Millisecond)
		r, ok := clientRectScreen(target.hwnd)
		if !ok {
			fmt.Fprintln(os.Stderr, "取客户区失败")
			os.Exit(1)
		}
		area := image.Rect(
			int(r.Left)-*pad, int(r.Top)-*pad,
			int(r.Right)+*pad, int(r.Bottom)+*pad,
		)
		img, err = qrscan.CaptureRect(area)
		if err != nil {
			fmt.Fprintf(os.Stderr, "截图失败: %v\n", err)
			os.Exit(1)
		}
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建输出目录失败: %v\n", err)
		os.Exit(1)
	}
	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建文件失败: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fmt.Fprintf(os.Stderr, "写 PNG 失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("已截图：%s\n  窗口：%s\n  尺寸：%dx%d\n", *out, target.title, img.Bounds().Dx(), img.Bounds().Dy())
}

// postDrag 向标题匹配的窗口投递"按下 → 移动若干步 → 松开"的消息序列。
//
// 坐标用**屏幕坐标**传入，这里换算成客户区坐标（遮罩窗口覆盖整个虚拟桌面，
// 原点是 (-1920,0) 这种负值，不换算就会选到屏幕外面去）。
func postDrag(title string, screenArea image.Rectangle) error {
	needle := strings.ToLower(title)
	var target *windowInfo
	for _, w := range enumWindows() {
		if strings.Contains(strings.ToLower(w.title), needle) {
			ww := w
			target = &ww
			break
		}
	}
	if target == nil {
		return fmt.Errorf("没有找到标题包含 %q 的可见窗口", title)
	}
	// 客户区原点（这个窗口无边框，客户区 == 窗口矩形）
	var cr rect
	if ok, _, _ := procGetClientRect.Call(target.hwnd, uintptr(unsafe.Pointer(&cr))); ok == 0 {
		return fmt.Errorf("GetClientRect 失败")
	}
	var origin point
	origin.X, origin.Y = cr.Left, cr.Top
	procClientToScreen.Call(target.hwnd, uintptr(unsafe.Pointer(&origin)))
	toClient := func(p image.Point) (int, int) {
		return p.X - int(origin.X), p.Y - int(origin.Y)
	}
	lparam := func(x, y int) uintptr {
		return uintptr(uint32(x)&0xffff | uint32(y)&0xffff<<16)
	}
	post := func(msg uint32, wparam uintptr, x, y int) error {
		r, _, err := procPostMessageW.Call(target.hwnd, uintptr(msg), wparam, lparam(x, y))
		if r == 0 {
			return fmt.Errorf("PostMessage(%#x) 失败：%v", msg, err)
		}
		return nil
	}
	const (
		wmLButtonDown = 0x0201
		wmLButtonUp   = 0x0202
		wmMouseMove   = 0x0200
		mkLButton     = 1
	)
	x1, y1 := toClient(screenArea.Min)
	x2, y2 := toClient(screenArea.Max)
	if err := post(wmLButtonDown, mkLButton, x1, y1); err != nil {
		return err
	}
	const steps = 6
	for i := 1; i <= steps; i++ {
		x := x1 + (x2-x1)*i/steps
		y := y1 + (y2-y1)*i/steps
		if err := post(wmMouseMove, mkLButton, x, y); err != nil {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return post(wmLButtonUp, 0, x2, y2)
}

// parseRect 解析 "x,y,w,h"。给 -rect 用。
func parseRect(s string) (image.Rectangle, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return image.Rectangle{}, fmt.Errorf("需要 4 个数字 x,y,w,h，收到 %q", s)
	}
	var v [4]int
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return image.Rectangle{}, fmt.Errorf("第 %d 个不是整数：%q", i+1, p)
		}
		v[i] = n
	}
	return image.Rect(v[0], v[1], v[0]+v[2], v[1]+v[3]), nil
}

// savePNG 写 PNG，并自动建好上级目录。
func savePNG(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
