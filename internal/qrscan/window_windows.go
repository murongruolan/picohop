//go:build windows

package qrscan

import (
	"errors"
	"fmt"
	"image"
	"unsafe"
)

// PrintWindow 的 PW_RENDERFULLCONTENT：让窗口（包括 DirectComposition 渲染的
// 内容，例如 WebView2）把完整画面画进我们给的 DC。
const pwRenderFullContent = 0x00000002

// winRect 是 Win32 的 RECT。
type winRect struct {
	Left, Top, Right, Bottom int32
}

var (
	pGetWindowRect = user32.NewProc("GetWindowRect")
	pPrintWindow   = user32.NewProc("PrintWindow")
)

// CaptureWindow 截取**指定窗口**的内容。
//
// 为什么需要它，而不是用 CaptureRect 截屏幕上的那块区域：
// CaptureRect 读的是屏幕像素，因此
//   - 窗口被别的程序挡住时，截到的是挡住它的那个程序；
//   - 想让窗口露出来就得抢前台焦点（SetForegroundWindow），
//     那会打断正在用电脑的人。
//
// PrintWindow 则是让**窗口自己**把画面渲染到我们提供的 DC，
// 不依赖它是否可见、是否在最前面，也不需要抢焦点。
//
// 注意：返回的是**整个窗口**（含标题栏），不是客户区。
func CaptureWindow(hwnd uintptr) (*image.RGBA, error) {
	ensureDPIAware()
	if hwnd == 0 {
		return nil, errors.New("qrscan: 窗口句柄为空")
	}

	var r winRect
	ok, _, callErr := pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ok == 0 {
		return nil, fmt.Errorf("qrscan: GetWindowRect 失败: %s", winErrText(callErr))
	}
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("qrscan: 窗口尺寸非法 %dx%d（可能已最小化）", w, h)
	}

	// 目标是"与窗口 DC 兼容"的位图，用窗口自己的 DC 做参考最稳妥
	wndDC, _, _ := pGetDC.Call(hwnd)
	if wndDC == 0 {
		return nil, errors.New("qrscan: GetDC(hwnd) 失败")
	}
	defer pReleaseDC.Call(hwnd, wndDC)

	memDC, _, memErr := pCreateCompatibleDC.Call(wndDC)
	if memDC == 0 {
		return nil, fmt.Errorf("qrscan: CreateCompatibleDC 失败: %s", winErrText(memErr))
	}
	defer pDeleteDC.Call(memDC)

	bitmap, _, bmpErr := pCreateCompatibleBitmap.Call(wndDC, uintptr(w), uintptr(h))
	if bitmap == 0 {
		return nil, fmt.Errorf("qrscan: CreateCompatibleBitmap(%d,%d) 失败: %s", w, h, winErrText(bmpErr))
	}
	defer pDeleteObject.Call(bitmap)

	oldBitmap, _, selErr := pSelectObject.Call(memDC, bitmap)
	if oldBitmap == 0 {
		return nil, fmt.Errorf("qrscan: SelectObject 失败: %s", winErrText(selErr))
	}

	// 由窗口自己渲染；不需要 BitBlt。
	pw, _, pwErr := pPrintWindow.Call(hwnd, memDC, uintptr(pwRenderFullContent))
	// 无论成败都换回旧位图：GetDIBits 要求 hbmp 不在任何 DC 里
	pSelectObject.Call(memDC, oldBitmap)
	if pw == 0 {
		return nil, fmt.Errorf("qrscan: PrintWindow 失败: %s", winErrText(pwErr))
	}

	bmi := bitmapInfo{
		Header: bitmapInfoHeader{
			BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			BiWidth:       int32(w),
			BiHeight:      -int32(h), // 负高度 = top-down
			BiPlanes:      1,
			BiBitCount:    32,
			BiCompression: biRGB,
		},
	}
	buf := make([]byte, w*h*4)
	lines, _, dibErr := pGetDIBits.Call(memDC, bitmap, 0, uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bmi)), dibRGBColors)
	if lines == 0 {
		return nil, fmt.Errorf("qrscan: GetDIBits 失败: %s", winErrText(dibErr))
	}
	return bgraToRGBA(buf, w, h), nil
}

// LooksBlank 判断一张图是不是"基本全黑/全同色"。
//
// 用途：PrintWindow 对某些使用硬件叠加层的窗口会返回一片黑。
// 调用方可以据此决定是否退回"截屏幕"的方式。
func LooksBlank(img *image.RGBA) bool {
	if img == nil {
		return true
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return true
	}
	// 均匀抽样，避免大图上跑满像素
	stepX := maxInt(1, b.Dx()/32)
	stepY := maxInt(1, b.Dy()/32)
	var first [4]byte
	firstSet := false
	differing := 0
	total := 0
	for y := b.Min.Y; y < b.Max.Y; y += stepY {
		for x := b.Min.X; x < b.Max.X; x += stepX {
			i := img.PixOffset(x, y)
			px := [4]byte{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}
			total++
			if !firstSet {
				first = px
				firstSet = true
				continue
			}
			// 允许极小噪声：每个通道差 <= 2 视为同色
			if absInt(int(px[0])-int(first[0])) > 2 ||
				absInt(int(px[1])-int(first[1])) > 2 ||
				absInt(int(px[2])-int(first[2])) > 2 {
				differing++
			}
		}
	}
	if total == 0 {
		return true
	}
	// 差异像素占比不足 2% 就认为基本是同一片颜色
	return differing*100 < total*2
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
