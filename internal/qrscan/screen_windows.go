//go:build windows

package qrscan

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"syscall"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Win32 API 绑定
//
// 走 syscall + LazyDLL，与 internal/gui 的写法保持一致：
// 不引 cgo，不引第三方截图库，保证 ChainProxy 始终是「一个 exe、零外部依赖」。
// ---------------------------------------------------------------------------

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")
)

var (
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pGetDC                         = user32.NewProc("GetDC")
	pReleaseDC                     = user32.NewProc("ReleaseDC")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
)

var (
	// 注意：CreateCompatibleDC / CreateCompatibleBitmap / GetDIBits / BitBlt
	// 都在 **gdi32.dll**，user32 里没有这些符号，放错会在 Call 时 panic。
	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pGetDIBits              = gdi32.NewProc("GetDIBits")
)

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

const (
	// GetSystemMetrics 的索引：虚拟桌面（所有显示器拼起来的 bounding box）。
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCxVirtualScreen = 78
	smCyVirtualScreen = 79

	// BitBlt 光栅操作码。CAPTUREBLT 必须带：
	// 少了它，带 WS_EX_LAYERED 的窗口（很多现代 UI 都是）会被整块漏掉，截出黑块。
	srcCopy    = 0x00CC0020
	captureBlt = 0x40000000

	// GetDIBits 的 usage 参数
	dibRGBColors = 0

	// biCompression：不压缩的 RGB
	biRGB = 0
)

// ---------------------------------------------------------------------------
// 结构体
// ---------------------------------------------------------------------------

// bitmapInfoHeader 对应 Win32 的 BITMAPINFOHEADER，一共 40 字节。
//
// 对齐必须逐字段抠：biPlanes / biBitCount 是 **uint16**，
// 如果偷懒把它们写成 int32（哪怕只写错一个），BiSizeImage 的位置就会整体后移，
// GetDIBits 会把 biSizeImage 当成 biXPelsPerMeter 之类的字段读，
// 结果要么返回 0 行、要么给出一块错位的像素数据 —— 而且是静默错，不报错。
type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

// bitmapInfo 对应 BITMAPINFO。
//
// 32bpp + BI_RGB（biClrUsed=0）其实不需要调色板，但 Win32 的 lpbmi 指向的是
// BITMAPINFO 而不是裸的 header；带上一个元素的颜色表既能保证结构体足够大，
// 又不会改变 header 的地址（Colors 在 Header 之后，指针仍指向 Header）。
type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

// ---------------------------------------------------------------------------
// DPI
// ---------------------------------------------------------------------------

var dpiOnce sync.Once

// ensureDPIAware 尝试把本进程设成 per-monitor DPI aware v2。
//
// Wails 主程序本身也是这个模式，GetSystemMetrics 因此返回物理像素；
// 但本包可能被命令行/测试单独调用，这时进程默认是 DPI unaware 的，
// 拿到的会是「虚拟化」后的逻辑像素，圈选的坐标就会和真实屏幕错位。
// 设置只在进程还没建过窗口时生效，失败（已被设置过）直接忽略，不 panic。
func ensureDPIAware() {
	dpiOnce.Do(func() {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 是个句柄值 -4。
		const perMonitorAwareV2 = ^uintptr(3)
		pSetProcessDpiAwarenessContext.Call(perMonitorAwareV2)
	})
}

// ---------------------------------------------------------------------------
// 截图
// ---------------------------------------------------------------------------

// VirtualScreen 返回整个虚拟桌面（所有显示器拼起来）的矩形，坐标是物理像素。
// 左上角通常是负的（副屏在主屏左边时），调用方不需要关心。
func VirtualScreen() (image.Rectangle, error) {
	ensureDPIAware()
	r := virtualScreenRect()
	if r.Dx() <= 0 || r.Dy() <= 0 {
		return image.Rectangle{}, fmt.Errorf("qrscan: 取虚拟桌面尺寸失败（%dx%d），当前会话可能没有桌面", r.Dx(), r.Dy())
	}
	return r, nil
}

// virtualScreenRect 不做任何校验地读出虚拟桌面矩形。
func virtualScreenRect() image.Rectangle {
	x := int(getSystemMetrics(smXVirtualScreen))
	y := int(getSystemMetrics(smYVirtualScreen))
	w := int(getSystemMetrics(smCxVirtualScreen))
	h := int(getSystemMetrics(smCyVirtualScreen))
	return image.Rect(x, y, x+w, y+h)
}

func getSystemMetrics(index int32) int32 {
	r, _, _ := pGetSystemMetrics.Call(uintptr(index))
	return int32(r)
}

// CaptureScreen 截取整个虚拟桌面。
func CaptureScreen() (*image.RGBA, error) {
	r, err := VirtualScreen()
	if err != nil {
		return nil, err
	}
	return capture(r)
}

// CaptureRect 截取屏幕上一块矩形区域（物理像素坐标）。
// 越界时自动裁剪到屏幕范围内。
func CaptureRect(r image.Rectangle) (*image.RGBA, error) {
	vs, err := VirtualScreen()
	if err != nil {
		return nil, err
	}
	// 越界不报错而是裁剪：用户在屏幕边缘松手时，选区很容易多出几个像素，
	// 那属于正常操作，不该让整次识别失败。
	area := r.Intersect(vs)
	if area.Dx() <= 0 || area.Dy() <= 0 {
		return nil, fmt.Errorf("qrscan: 选区 %v 与屏幕 %v 没有交集", r, vs)
	}
	return capture(area)
}

// capture 截取 area（虚拟桌面坐标，调用方保证已在屏幕内且非空）。
//
// 返回的图以 (0,0) 为原点：识别只需要像素内容，绝对坐标没有意义，
// 这样调用方也不用处理副屏带来的负坐标。
func capture(area image.Rectangle) (*image.RGBA, error) {
	ensureDPIAware()
	w, h := area.Dx(), area.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("qrscan: 截图区域非法 %v", area)
	}

	// GetDC(0) 拿整个屏幕的 DC。刻意不用 CreateDC("DISPLAY", ...)：
	// 那个在多屏（尤其是副屏在主屏左侧/上方）时经常只给出主屏范围。
	screenDC, _, _ := pGetDC.Call(0)
	if screenDC == 0 {
		return nil, errors.New("qrscan: GetDC 失败，当前会话可能没有桌面（服务/无头环境）")
	}
	// 逐层 defer 释放：注册顺序的逆序正好是 DeleteObject → DeleteDC → ReleaseDC。
	defer pReleaseDC.Call(0, screenDC)

	memDC, _, memErr := pCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, fmt.Errorf("qrscan: CreateCompatibleDC 失败: %s", winErrText(memErr))
	}
	defer pDeleteDC.Call(memDC)

	bitmap, _, bmpErr := pCreateCompatibleBitmap.Call(screenDC, uintptr(w), uintptr(h))
	if bitmap == 0 {
		return nil, fmt.Errorf("qrscan: CreateCompatibleBitmap(%d, %d) 失败: %s", w, h, winErrText(bmpErr))
	}
	defer pDeleteObject.Call(bitmap)

	// 位图必须先选进内存 DC 才能作为 BitBlt 的目标。
	// 记下旧位图，取像素前要换回来 —— MSDN 明确要求 GetDIBits 的 hbmp
	// 不能处于「已选入某个 DC」的状态。
	oldBitmap, _, selErr := pSelectObject.Call(memDC, bitmap)
	if oldBitmap == 0 {
		return nil, fmt.Errorf("qrscan: SelectObject 失败: %s", winErrText(selErr))
	}

	// 源坐标直接用 area.Min：虚拟桌面坐标与屏幕 DC 的原点（主屏左上角）是同一套，
	// 副屏在主屏左侧时 area.Min.X 为负，正好由 Win32 的有符号参数处理。
	ok, _, bltErr := pBitBlt.Call(memDC, 0, 0, uintptr(w), uintptr(h),
		screenDC, uintptr(int32(area.Min.X)), uintptr(int32(area.Min.Y)), uintptr(srcCopy|captureBlt))
	// 无论成败都要把位图从 DC 里换出来，否则后面的 GetDIBits 会失败。
	pSelectObject.Call(memDC, oldBitmap)
	if ok == 0 {
		return nil, fmt.Errorf("qrscan: BitBlt 失败: %s", winErrText(bltErr))
	}

	// biHeight 取负 = top-down DIB：像素按「从左上角开始、每行自左向右」排列，
	// 省掉一次上下翻转；同时也避开了「正高度时每行按 4 字节向上对齐」的坑
	// （32bpp 恰好天然对齐，但换成 24bpp 就会错位，这里直接把这条路堵死）。
	bmi := bitmapInfo{
		Header: bitmapInfoHeader{
			BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			BiWidth:       int32(w),
			BiHeight:      -int32(h),
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

// bgraToRGBA 把 top-down 的 BGRA 像素缓冲转成 image.RGBA。
func bgraToRGBA(buf []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i+3 < len(buf); i += 4 {
		img.Pix[i] = buf[i+2]   // R ← B
		img.Pix[i+1] = buf[i+1] // G ← G
		img.Pix[i+2] = buf[i]   // B ← R
		// alpha 一律写死 255：GDI 不保证填这个字节，实测不少驱动给的是 0。
		// 原样抄过去会得到一张「全透明黑图」，而 gozxing 取色时按 alpha 预乘处理，
		// 透明像素会被当成白色 —— 识别率直接归零，且完全不报错。
		img.Pix[i+3] = 0xFF
	}
	return img
}

// winErrText 把 LazyProc.Call 返回的 errno 转成可读文本。
//
// 注意：Call 的 error 在成功时也可能是上一次遗留的错误码，所以只在失败路径上用它。
func winErrText(err error) string {
	if err == nil || err == syscall.Errno(0) {
		return "未知错误"
	}
	return err.Error()
}
