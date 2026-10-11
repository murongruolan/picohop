//go:build windows

package qrscan

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// ---------------------------------------------------------------------------
// 屏幕圈选遮罩（overlay）
//
// 流程：先截整屏 → 再建一个覆盖整个虚拟桌面的置顶窗口当背景 → 用户拖拽圈选
// → 返回虚拟桌面坐标系里的矩形。
//
// 为什么必须先截图再建窗口：如果先建窗口，那张"点按钮那一刻的屏幕"就已经
// 被遮罩自己盖住了，BitBlt 拍到的是一张盖着遮罩的图，圈选时屏幕上会出现
// 无限套娃。截图和窗口创建之间只有毫秒级间隔，可接受。
//
// 线程模型（这是本文件最容易踩的坑，最终选型写在 SelectRegion 的注释里）：
// 内部开一个 goroutine，在里面 LockOSThread 后再建窗口 + 跑消息循环，
// 全程不切线程，结束后 UnlockOSThread。调用方在自己的 goroutine 里同步
// 等结果即可，不会被 Win32 的线程亲和要求牵连。
// ---------------------------------------------------------------------------

// ErrCancelled 表示用户取消了圈选（按 Esc、点右键、或选了空区域）。
var ErrCancelled = errors.New("已取消圈选")

// RegisterClassExW 在类名已存在时返回的错误码（WinError.h: ERROR_CLASS_ALREADY_EXISTS）。
const errorClassAlreadyExists = 1410

// ---------------------------------------------------------------------------
// Win32 API 绑定
//
// user32 / gdi32 这两个 LazyDLL 以及 winErrText / ensureDPIAware 都来自
// screen_windows.go，这里直接复用，不重复声明（同一个包，重复声明会编译不过）。
// ---------------------------------------------------------------------------

var (
	pRegisterClassExW           = user32.NewProc("RegisterClassExW")
	pCreateWindowExW            = user32.NewProc("CreateWindowExW")
	pDefWindowProcW             = user32.NewProc("DefWindowProcW")
	pDestroyWindow              = user32.NewProc("DestroyWindow")
	pShowWindow                 = user32.NewProc("ShowWindow")
	pUpdateWindow               = user32.NewProc("UpdateWindow")
	pGetMessageW                = user32.NewProc("GetMessageW")
	pTranslateMessage           = user32.NewProc("TranslateMessage")
	pDispatchMessageW           = user32.NewProc("DispatchMessageW")
	pPostQuitMessage            = user32.NewProc("PostQuitMessage")
	pInvalidateRect             = user32.NewProc("InvalidateRect")
	pBeginPaint                 = user32.NewProc("BeginPaint")
	pEndPaint                   = user32.NewProc("EndPaint")
	pGetClientRect              = user32.NewProc("GetClientRect")
	pFillRect                   = user32.NewProc("FillRect")
	pSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	pSetWindowRgn               = user32.NewProc("SetWindowRgn")
	pCreateRectRgn              = gdi32.NewProc("CreateRectRgn")
	pCombineRgn                 = gdi32.NewProc("CombineRgn")
	pSetCapture                 = user32.NewProc("SetCapture")
	pReleaseCapture             = user32.NewProc("ReleaseCapture")
	pShowCursor                 = user32.NewProc("ShowCursor")
	pSetWindowLongPtrW          = user32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtrW          = user32.NewProc("GetWindowLongPtrW")
)

var (
	// TextOutW 也在 gdi32 里，**不在 user32**。
	//
	// 这一行曾经写成 user32.NewProc("TextOutW")，代价是：拖动产生选区之后，
	// WM_PAINT 要画"尺寸标签"时 Go 的 LazyProc 找不到这个过程名，直接 panic，
	// **整个程序当场退出**（用户报的现象是"圈选点完什么都没了，软件也不见了"）。
	//
	// 为什么那 6 个覆盖层测试没抓到：它们用 `hideOverlayForTest` 让窗口保持**隐藏**，
	// 而隐藏窗口永远不会收到 WM_PAINT —— paintSelection 这条路径从来没被执行过。
	// 现在有 TestAllWin32ProcsResolve 兜底：它把每个绑定的过程名都 Find() 一遍，
	// "绑错 DLL"这一类再也不会躲过测试。
	pTextOutW      = gdi32.NewProc("TextOutW")
	pCreateFontW   = gdi32.NewProc("CreateFontW")
	pGetDeviceCaps = gdi32.NewProc("GetDeviceCaps")
	pSetTextColor  = gdi32.NewProc("SetTextColor")
	pSetBkMode     = gdi32.NewProc("SetBkMode")
	// CreateSolidBrush 在 gdi32 里，不在 user32 —— 放错不会编译报错，
	// 而是运行时在 mustFind 里直接 panic（Failed to find ... procedure）。
	pCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
)

// LOGPIXELSY：GetDeviceCaps 的索引，用来把字号从 96 DPI 缩放到实际 DPI。
const logPixelsY = 90

// ---------------------------------------------------------------------------
// 会话注册表
//
// 窗口过程（WNDPROC）拿不到 Go 闭包，必须有一条"从 hwnd 找回 *overlay"的通道。
// 常见做法是 SetWindowLongPtrW(hwnd, GWLP_USERDATA, uintptr(unsafe.Pointer(ov)))，
// 但 go vet 会（正确地）报 "possible misuse of unsafe.Pointer"：把 Go 指针存成
// uintptr 之后 GC 就看不到这个引用了，一旦对象被回收，窗口收到消息就会崩。
//
// 所以这里改用一张显式注册表：窗口属性里只存一个递增的会话 id，
// 指针始终以 map 的 value 形式存在，GC 能正常追踪，vet 也干净。
// 全局同时只有一个遮罩窗口，map 实际上最多一项；
// 加 sync.Mutex 是为了让"写注册表"（调用方 goroutine）和
// "读注册表"（锁定线程上的窗口过程）之间不存在数据竞争。
// ---------------------------------------------------------------------------

var (
	overlayRegistryMu sync.Mutex
	overlayRegistry   = make(map[uintptr]*overlay)
	overlayNextID     uintptr
)

// registerOverlay 分配会话 id 并登记，返回该 id（后面写进窗口属性）。
func registerOverlay(ov *overlay) uintptr {
	overlayRegistryMu.Lock()
	defer overlayRegistryMu.Unlock()
	overlayNextID++
	id := overlayNextID
	overlayRegistry[id] = ov
	return id
}

func unregisterOverlay(id uintptr) {
	overlayRegistryMu.Lock()
	defer overlayRegistryMu.Unlock()
	delete(overlayRegistry, id)
}

func lookupOverlay(id uintptr) *overlay {
	overlayRegistryMu.Lock()
	defer overlayRegistryMu.Unlock()
	return overlayRegistry[id]
}

// gwlpUserData 是 SetWindowLongPtr/GetWindowLongPtr 的索引 GWLP_USERDATA（-21）。
//
// 这里绕一圈纯粹是为了对付 Go 的常量算术：Go 的常量是任意精度整数，
// 直接写 uintptr(-21) 会被判定为溢出（constant -21 overflows uintptr），
// 而 Win32 要的恰恰是 0xFFFFFFEB 这个"负的索引值"。
// 让 -21 先离开常量身份落到 int32 变量上（编译期常量转换会被判定溢出），
// 再零扩展到 uintptr 得到 0xFFFFFFEB；Win32 按有符号 int 解释仍是 -21。
var userDataIndex = int32(-21)

var gwlpUserData = uintptr(uint32(userDataIndex))

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

// Tracef 是可选的时间线日志钩子，由 desktop 层注入（写进应用日志）。
//
// 为什么要有它：圈选这条链路"哪一步慢"只能靠测量，不能靠猜。
// 已经吃过两次教训：一次是 1.8 秒花在一个系统设置广播上；一次是度量点选错，
// 导致"改完了读数却没变"。默认什么都不做。
var Tracef = func(format string, args ...any) {}

// sinceMS 返回从 t 到现在经过的毫秒数（本文件内的计时用）。
func sinceMS(t time.Time) int64 { return time.Since(t).Milliseconds() }

const (
	// 窗口类 / 窗口
	csHRedraw = 0x0002
	csVRedraw = 0x0001
	// 注意 CsDblClks（0x0008）刻意不设：双击对圈选没有意义，
	// 而且它会让我们收到 WM_LBUTTONDBLCLK 这种额外消息，白增分支。

	wsPopup        = 0x80000000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080 // 不出现在 Alt+Tab 里
	wsExLayered    = 0x00080000 // 分层窗口：配合 SetLayeredWindowAttributes 做半透明

	lwaAlpha = 0x00000002 // SetLayeredWindowAttributes：整窗统一 alpha
	rgnDiff  = 4          // CombineRgn：前者减去后者

	swShowNa = 8 // 显示但不激活：圈选期间不该抢走前台窗口

	// 消息
	wmDestroy = 0x0002
	// minSelectSide 是"算得上一次圈选"的最小边长（像素）：小于它的选区按取消处理。
	// 一个 0x0 的矩形既截不到东西也识别不出二维码，不如当成"他点了一下想退出"。
	minSelectSide = 12

	wmPaint       = 0x000F
	wmClose       = 0x0010
	wmEraseBkgnd  = 0x0014
	wmSetCursor   = 0x0020
	wmKeyDown     = 0x0100
	wmRButtonDown = 0x0204
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmNCCreate    = 0x0081

	vkEscape = 0x1B

	// SetWindowLongPtr/GetWindowLongPtr 的索引 GWLP_USERDATA = -21。
	// 刻意做成下面那个 int32 变量而不是这里的常量：常量 -21 转 uintptr
	// 会直接编译报错（constant -21 overflows uintptr）。

	// SystemParametersInfo：设置/清除"拖拽时显示窗口内容"

	// EndPaint 的返回值 / DefWindowProc 的返回值
	defWindowProcOK = 0

	// 松开左键时宽或高小于这个值就当作"误点"，按取消处理
	minSelectionPx = 8

	// 选区边框：2 像素红色实线
	selectionBorderPx = 2

	// 十字准星：1 像素，贯穿整个客户区
	crosshairPx = 1

	// 尺寸文字的字号（96 DPI 下的逻辑像素），实际会按 DPI 缩放
	labelFontAt96DPI = 20

	// 尺寸文字相对选区左上角的偏移，避免压在边框上
	labelOffsetX = 4
	labelOffsetY = 4

	// 红色 COLORREF：0x00BBGGRR，所以纯红是 0x000000FF
	colorrefRed = 0x000000FF

	// SetBkMode 的 TRANSPARENT：文字不涂背景，直接压在截图上
	bkModeTransparent = 1

	// 压暗整屏用：AC_SRC_OVER 是唯一可用的混合方式（AlphaBlend 的 BlendOp）。
	// dimAlpha 是压暗程度（0-255），交给 SetLayeredWindowAttributes 的 LWA_ALPHA。
	// 110/255 ≈ 43%，和微信/QQ 截图的观感接近：能看清底下是什么，注意力却在选区上。
	dimAlpha = 110
)

// ---------------------------------------------------------------------------
// 光标可见性
//
// ShowCursor 是"计数器"而不是"布尔量"：每一次 ShowCursor(FALSE) 都必须配一次
// ShowCursor(TRUE)，否则用户的鼠标会永久消失。这里把它收敛成 hide/restore
// 一对函数，并在会话的 defer 里无条件调用 restore —— 即使中途报错或 panic，
// 光标也一定会恢复。
// ---------------------------------------------------------------------------

// hideSystemCursor 把光标计数按到负数（隐藏）。
func hideSystemCursor() {
	// 循环到计数 <= -1，抵消掉别处（比如 Wails 前端）可能已经额外 Hide 过的次数。
	for i := 0; i < 16; i++ {
		if r, _, _ := pShowCursor.Call(0); int32(r) < 0 {
			return
		}
	}
}

// restoreSystemCursor 把光标计数推回 >= 0，保证系统光标一定恢复显示。
func restoreSystemCursor() {
	for i := 0; i < 16; i++ {
		if r, _, _ := pShowCursor.Call(1); int32(r) >= 0 {
			return
		}
	}
}

// ---------------------------------------------------------------------------
// 结构体
// ---------------------------------------------------------------------------

// wndClassExW 对应 Win32 的 WNDCLASSEXW。
//
// 对齐必须逐字段抠：cbSize 是 uint32 后紧跟 style(uint32)，再往后才是
// lpfnWndProc 这个 8 字节函数指针；lpfnWndProc 与 hInstance 之间是
// "指针 + 4 字节图标句柄"的组合，编译器会自动插 4 字节 padding。
// 只要字段类型写错一个（比如把 cbSize 写成 uintptr），整个结构体就会错位，
// RegisterClassExW 会返回 "参数错误"，而且不告诉你哪个字段错了。
type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

// msgT 对应 Win32 的 MSG（x64 布局）。
//
// 坑：message 是 UINT(4 字节) 而 wParam 是 WPARAM(8 字节)，
// 两者之间有 4 字节 padding。把 message 写成 uintptr 会让 wParam/lParam
// 整体错位 —— GetMessageW 会往错误偏移写 wParam。
type msgT struct {
	Hwnd    uintptr
	Message uint32
	_       uint32 // 对齐填充，别删
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PtX     int32
	PtY     int32
}

// rectT 对应 Win32 的 RECT。
type rectT struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

// paintStructT 对应 Win32 的 PAINTSTRUCT，共 72 字节（x64）。
//
// 必须完整声明：BeginPaint 会往里面写 64 字节，结构体给小了就是栈溢出。
type paintStructT struct {
	Hdc         uintptr
	FErase      int32
	RcPaint     rectT
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

// overlay 是一次圈选会话的全部状态。
//
// 为什么做成结构体而不是纯模块级变量：SelectRegion 可能被连续调用，
// 每个会话一份状态，天然不会串味；窗口通过注册表里的会话 id 找回来。
type overlay struct {
	hwnd uintptr

	// id 是会话在 overlayRegistry 里的键，写进窗口属性供窗口过程回查。
	id uintptr

	// vs 是虚拟桌面矩形（物理像素，原点可能是负的）。窗口位置=vs.Min，
	// 尺寸=vs.Dx()xvs.Dy()，客户区坐标 = 绝对坐标 - vs.Min。
	vs image.Rectangle

	// 双缓冲：直接画到窗口 DC 上会闪烁（每帧先擦白再贴图）。
	memDC     uintptr
	memBmp    uintptr
	oldBmp    uintptr
	brush     uintptr // 红色实心画刷
	font      uintptr // 尺寸文字字体
	labelSize int32   // 实际字号像素

	loggedSizes bool

	// 压暗用的纯黑画刷（背景就是黑 + 半透明窗口属性）
	blackBrush uintptr

	// regionSel 是"窗口区域最后一次按哪个选区设过"，用来避免每帧重设区域
	regionSel image.Rectangle

	// 鼠标（客户区坐标）
	mouse image.Point

	// 选区（客户区坐标）；dragging 表示左键还按着。
	// anchor 是按下时那个角，整个拖拽过程保持不变。
	dragging bool
	anchor   image.Point
	sel      image.Rectangle
	haveSel  bool

	// 消息循环退出后带回的结果
	cancelled bool

	// SPI_SETDRAGFULLWINDOWS 的原值，退出时恢复（属于全局用户设置，不能改完就跑）
}

// ---------------------------------------------------------------------------
// 包级状态
// ---------------------------------------------------------------------------

var (
	overlayClassOnce sync.Once
	overlayClassName *uint16

	// overlayWndProcCallback 必须存在包级变量里。
	//
	// 这是本文件最经典的坑：syscall.NewCallback 返回的只是一个"回调编号"，
	// 真正的 C 蹦床（trampoline）由 Go 运行时按编号生成。如果返回值只被局部
	// 变量持有，GC 会认为它没人用 —— 蹦床被回收后，窗口一收到消息就崩在
	// 野指针上（而且是随机时刻崩，非常难查）。放包级变量 = 与进程同寿命。
	overlayWndProcCallback = syscall.NewCallback(overlayWndProc)

	// overlaySessions 只保护 hiddenOverlayForTest 这类"会话级开关"。
	// 单个会话的可变状态（选区、鼠标位置）全部挂在 overlay 结构体上，
	// 窗口过程、绘制、结果读取都发生在同一个锁定线程里，不需要加锁。
	overlaySessions sync.Mutex

	// overlayHiddenForTest 为 true 时窗口只创建不显示。
	//
	// 存在的唯一理由是自动化测试：测试进程里 ShowWindow 一个覆盖整个桌面、
	// WS_EX_TOPMOST 的窗口会盖住开发者的屏幕（甚至盖住本会话的 GUI）。
	// 窗口照样创建、照样收 PostMessage 的消息，只是不可见。
	// 生产路径恒为 false，且必须在窗口创建前设置。
	overlayHiddenForTest bool
)

// ---------------------------------------------------------------------------
// 对外 API
// ---------------------------------------------------------------------------

// SelectRegion 在屏幕上显示遮罩，让用户按住左键拖拽圈选一块矩形，
// 返回选中区域在**虚拟桌面坐标系**里的矩形（物理像素）。
//
// 取消（Esc / 右键 / 松开时区域太小）返回 ErrCancelled。
//
// 线程模型说明（最终选型：内部自建锁定线程）：
//
//	Win32 要求"创建窗口的线程 = 跑消息循环的线程 = 窗口过程被调用的线程"，
//	而且窗口过程必须始终跑在同一个 OS 线程上，否则 Go 调度器一旦把这个
//	goroutine 搬到别的线程，消息就会投递到没有消息队列的线程上，窗口直接僵死。
//	所以这里在函数内部起一个 goroutine，LockOSThread → 截图 → 建窗口 →
//	GetMessageW 循环 → 收尾 → UnlockOSThread，全程一条线程跑完。
//
//	调用方在**任何** goroutine 上调用都可以：本函数会阻塞到用户完成圈选，
//	但阻塞的是调用方自己的 goroutine，不影响它的主线程，也不需要调用方
//	自己懂 LockOSThread。这是"调用方零心智负担"的选型。
func SelectRegion() (image.Rectangle, error) {
	return SelectRegionHooked(nil)
}

// SelectRegionHooked 与 SelectRegion 相同，但会在遮罩窗口创建完成后回调 onReady。
//
// 存在的唯一目的是让自动化测试能拿到窗口句柄，用 PostMessage 模拟鼠标拖拽 ——
// 这样"圈选"这条交互路径也能被真实地测到，而不是只能靠人手动点。
func SelectRegionHooked(onReady func(hwnd uintptr)) (image.Rectangle, error) {
	ready := make(chan overlayReady, 1)
	result := make(chan overlayResult, 1)

	go func() {
		// defer 顺序：先解锁线程，再闭包发结果（闭包最简单，永远不会漏发，
		// 调用方也不会因为某条 error 分支忘了写 channel 而永久阻塞）。
		defer close(result)
		defer runtime.UnlockOSThread()

		runtime.LockOSThread()
		runOverlaySession(ready, result, onReady)
	}()

	// 等窗口建出来（或建失败）。窗口创建本身是毫秒级，这里只是同步点。
	r := <-ready
	if r.err != nil {
		// 无论有没有 onReady 回调，错误都会从这个 channel 回来，
		// 所以 onReady == nil 的情况不会漏掉失败信息。
		return image.Rectangle{}, r.err
	}
	if onReady != nil {
		onReady(r.hwnd)
	}

	res, ok := <-result
	if !ok {
		// 理论上不可达（闭包保证至少发一次），留作兜底而不是 panic。
		return image.Rectangle{}, errors.New("qrscan: 圈选会话异常结束")
	}
	return res.rect, res.err
}

type overlayReady struct {
	hwnd uintptr
	err  error
}

type overlayResult struct {
	rect image.Rectangle
	err  error
}

// ---------------------------------------------------------------------------
// 会话主体（跑在锁定线程上）
// ---------------------------------------------------------------------------

// runOverlaySession 是真正的工作函数：调用方必须已经 LockOSThread。
//
// 结果发送全部走 defer 闭包，保证 result 只被 send 一次且一定会 send。
func runOverlaySession(ready chan overlayReady, result chan overlayResult, onReady func(uintptr)) {
	var (
		ov     *overlay
		res    image.Rectangle
		resErr error
	)
	readySent := false
	sendReady := func(r overlayReady) {
		readySent = true
		ready <- r
	}

	defer func() {
		// 恢复光标：无论正常结束、报错还是 panic，都必须执行。
		// 漏掉这一句用户就再也看不到鼠标指针了。
		restoreSystemCursor()

		// 【就绪前 panic 也必须叫醒调用方】
		//
		// 真实踩过：newOverlay 因为空指针 panic，而 panic 发生在
		// ready 信号之前，于是 SelectRegionHooked 永远卡在 <-ready 上，
		// 表现为"点了圈选没反应、日志也停在准备创建遮罩"。
		// 这里补一次：只要还没发过 ready，就把错误送过去。
		if !readySent {
			readySent = true
			ready <- overlayReady{err: errors.New("qrscan: 圈选会话在就绪前异常结束")}
		}

		// 【把 panic 挡在这里】这是"圈选出问题不该拖死整个程序"的最后一道防线。
		//
		// 有真实外伤：pTextOutW 曾经绑错 DLL，拖动产生选区后 WM_PAINT 里
		// LazyProc 直接 panic —— panic 发生在窗口过程里，整个进程当场退出，
		// 用户看到的是"点完圈选，软件整个不见了"。有了这个 recover，
		// 同类问题最多让这一次圈选失败（主窗口照常恢复），而不是让程序消失。
		if r := recover(); r != nil {
			resErr = fmt.Errorf("qrscan: 圈选内部错误（已拦下，未影响程序运行）: %v", r)
		}

		// 用闭包发送结果：无论从哪条分支 return，都保证 result 恰好被 send 一次，
		// 调用方不会因为某个错误分支忘了写 channel 而永久阻塞。
		result <- overlayResult{rect: res, err: resErr}
	}()

	// 1) 只需要虚拟桌面矩形。
	//
	// 【这里以前会先截一整屏当背景】现在不需要了：压暗是"半透明黑窗口"这个窗口属性
	// 给的效果，选区是"窗口区域挖洞"漏出的真实桌面 —— 显示环节完全不碰像素，
	// 于是少了约 37ms 的整屏截图、也少了 16MB 的缓冲区。
	// 截图只在最后识别二维码时截一次（CaptureRect）。
	tMark := time.Now()
	msCapture := int64(0)

	vs, err := VirtualScreen()
	if err != nil {
		sendReady(overlayReady{err: err})
		resErr = err
		return
	}

	// 2) 隐藏系统光标，改由 WM_PAINT 自己画红色十字准星
	//    （不做 CreateCursor 自定义光标位图：那套 API 在 DPI 缩放 / 多屏下很脆）。
	hideSystemCursor()

	// 3) 建窗口。这一步失败（无桌面会话）必须回报有意义的错误，不能 panic。
	tMark = time.Now()
	ov = newOverlay(vs)
	msConvert := sinceMS(tMark)

	tMark = time.Now()
	if err := ov.create(hideOverlayForTest()); err != nil {
		sendReady(overlayReady{err: err})
		resErr = err
		return
	}
	msCreate := sinceMS(tMark)
	Tracef("qrscan 时间线：截屏=%dms 转格式=%dms 建窗+首帧=%dms（合计 %dms）",
		msCapture, msConvert, msCreate, msCapture+msConvert+msCreate)
	// 保证窗口一定会被销毁、GDI 资源一定会被释放，即使中途 return 或 panic。
	defer func() {
		pDestroyWindow.Call(ov.hwnd)
		ov.destroy()
	}()

	// 4) 通知调用方"窗口好了"。注意：onReady 由 SelectRegionHooked 那边统一调用，
	//    这里只管通过 ready 通道报告，避免回调被触发两次。
	//
	//    这一步必须在窗口**真的显示出来之后、任何昂贵操作之前**发出 ——
	//    调用方拿它当"用户已经能看到遮罩"的时刻来计时。
	sendReady(overlayReady{hwnd: ov.hwnd})

	// 【这里刻意什么都不做】
	//
	// 早先这里调 disableDragFullWindows() —— 它用 SPIF_SENDCHANGE 把
	// WM_SETTINGCHANGE 广播给**所有**顶层窗口并等它们响应，**实测要 1.8 秒**。
	// 而它跑在覆盖层自己那条消息循环线程上，于是窗口刚显示就被冻住 1.8 秒：
	// 用户看到的是"只出来半条线 → 卡住 → 过一会儿才恢复响应"。
	// 它想防的"拖拽重绘与选区重绘打架"并没有实测证据，而代价是一个冻结的窗口
	// 外加**改动用户的全局系统偏好**（SPIF_UPDATEINIFILE 会写进注册表）。
	// 权衡下来直接不要它：不冻结、也不动用户的系统设置。

	// 5) 消息循环（阻塞到窗口销毁）
	ov.messageLoop()

	// 6) 结果：取消 → ErrCancelled；否则返回归一化后的绝对矩形。
	if ov.cancelled {
		resErr = ErrCancelled
		return
	}
	res = ov.selectionAbsolute()
	// "点一下没拖"（或拖得极小）当成取消，而不是丢一个 0x0 的矩形给调用方 ——
	// 那种矩形既截不到东西，也识别不出二维码，只会让用户看到一句莫名其妙的报错。
	// 用户在圈选界面上点一下的意图本来就是"算了，不选了"。
	if res.Dx() < minSelectSide || res.Dy() < minSelectSide {
		resErr = ErrCancelled
		return
	}
}

// hideOverlayForTest 读会话级开关（带锁，避免 -race 下的数据竞争告警）。
func hideOverlayForTest() bool {
	overlaySessions.Lock()
	defer overlaySessions.Unlock()
	return overlayHiddenForTest
}

// ---------------------------------------------------------------------------
// 窗口生命周期
// ---------------------------------------------------------------------------

// registerOverlayClass 注册窗口类。
//
// RegisterClassExW 对同一个类名重复注册会返回 ERROR_CLASS_ALREADY_EXISTS，
// 所以我们用 sync.Once 保证进程内只注册一次。注意：窗口类是按"进程 + 类名"
// 记的，不是按线程，所以在哪个线程注册都行，只要注册成功一次。
func registerOverlayClass() (uint16, error) {
	var atom uint16
	var regErr error
	overlayClassOnce.Do(func() {
		name, err := syscall.UTF16PtrFromString("ChainProxyQRScanOverlay")
		if err != nil {
			regErr = fmt.Errorf("qrscan: 窗口类名转换失败: %w", err)
			return
		}
		wc := wndClassExW{
			CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			Style:         csHRedraw | csVRedraw,
			LpfnWndProc:   overlayWndProcCallback,
			LpszClassName: name,
			// HCursor 留空 + 全程 ShowCursor(FALSE)：光标由我们自己画。
			// HbrBackground 留空：WM_ERASEBKGND 里直接返回 1，背景由 WM_PAINT
			// 一次性贴图完成，不给系统任何"先擦成白色"的机会（那就是闪烁的来源）。
		}
		atomRaw, _, callErr := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		if atomRaw == 0 {
			// 只有"已存在"才当作成功；其它错误如实上报。
			if errno, ok := callErr.(syscall.Errno); ok && uintptr(errno) == errorClassAlreadyExists {
				// 类已存在，直接用。
			} else {
				regErr = fmt.Errorf("qrscan: RegisterClassExW 失败: %s", winErrText(callErr))
				return
			}
		}
		atom = uint16(atomRaw)
		overlayClassName = name
	})
	if regErr != nil {
		return 0, regErr
	}
	return atom, nil
}

// newOverlay 构造圈选会话。
//
// 这里曾经要把整屏 RGBA 转成 StretchDIBits 用的 top-down BGRA ——
// 显示改成"半透明黑窗口（WS_EX_LAYERED）+ 窗口区域挖洞"之后，背景不再需要截图，
// 于是那段 400 万像素的逐像素转换、16MB 的缓冲区、以及 bgra/bmi 两个字段一起删掉了。
// 截图现在只在最后识别二维码时截**选区那一小块**。
func newOverlay(vs image.Rectangle) *overlay {
	w, h := vs.Dx(), vs.Dy()
	return &overlay{
		vs:    vs,
		mouse: image.Pt(w/2, h/2), // 还没收到 WM_MOUSEMOVE 时，准星先待在屏幕中央
	}
}

// create 建窗口并显示。
func (ov *overlay) create(hidden bool) error {
	if _, err := registerOverlayClass(); err != nil {
		return err
	}
	if overlayClassName == nil {
		return errors.New("qrscan: 圈选窗口类未注册成功")
	}

	// 窗口位置尺寸 = 虚拟桌面矩形，可能是 (-1920,0)-(1920,1080) 这种负原点，
	// CreateWindowExW 的 x/y 是有符号 int，直接原样传。
	hwnd, _, callErr := pCreateWindowExW.Call(
		// WS_EX_LAYERED：让"黑底 + 半透明"自己构成压暗层，
		// 于是**不需要把整屏截图当背景**（截图只留给最后的二维码识别用）。
		uintptr(wsExTopmost|wsExToolWindow|wsExLayered),
		uintptr(unsafe.Pointer(overlayClassName)),
		uintptr(unsafe.Pointer(overlayClassName)), // 标题用不到（无 WS_CAPTION），给个非空串即可
		uintptr(wsPopup), // 无边框、无标题栏
		uintptr(int32(ov.vs.Min.X)), uintptr(int32(ov.vs.Min.Y)),
		uintptr(ov.vs.Dx()), uintptr(ov.vs.Dy()),
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("qrscan: CreateWindowExW 失败（%v）: %s", ov.vs, winErrText(callErr))
	}
	ov.hwnd = hwnd

	// 登记会话，并把会话 id 写进窗口属性。
	//
	// 为什么存 id 而不是存指针：见 overlayRegistry 上面那段说明 ——
	// 把 Go 指针转成 uintptr 存进窗口会让 GC 丢引用，go vet 也会报
	// "possible misuse of unsafe.Pointer"。
	ov.id = registerOverlay(ov)
	pSetWindowLongPtrW.Call(hwnd, gwlpUserData, ov.id)

	// 半透明黑 = 压暗。整屏压暗是**窗口属性**给的，不经过任何像素处理，
	// 也就不存在"截图与显示对不上"的可能。
	pSetLayeredWindowAttributes.Call(hwnd, 0, uintptr(dimAlpha), uintptr(lwaAlpha))

	ov.recreateBackBuffer()

	// 先 UpdateWindow 预热一次，让第一帧在"还没人操作"的时候就画好，
	// 避免用户按下左键时背景还是黑的。
	if !hidden {
		pShowWindow.Call(hwnd, uintptr(swShowNa))
	}
	pUpdateWindow.Call(hwnd)

	// 【注意】这里**不做** disableDragFullWindows —— 它要 1.8 秒（见下面那个函数），
	// 而"窗口已经出现在屏幕上"这件事不该等它。由 runOverlaySession 在发出
	// "遮罩已就绪"信号之后再调用它，这样调用方拿到的耗时如实等于用户看到的延迟。
	return nil
}

// recreateBackBuffer 建双缓冲：一个尺寸等于客户区的内存 DC + 兼容位图。
//
// 为什么要双缓冲：WM_PAINT 里要"贴整屏原图 + 画边框 + 画准星"三步，
// 如果直接画到窗口 DC 上，用户会看到每一步的中间态（尤其拖拽时每秒几十帧），
// 表现为边框和准星闪烁。全部画在内存位图上再一次性 BitBlt 过去就干净了。
func (ov *overlay) recreateBackBuffer() {
	var rc rectT
	pGetClientRect.Call(ov.hwnd, uintptr(unsafe.Pointer(&rc)))
	w, h := rc.Right-rc.Left, rc.Bottom-rc.Top
	if w <= 0 || h <= 0 {
		w, h = int32(ov.vs.Dx()), int32(ov.vs.Dy())
	}

	winDC, _, _ := pGetDC.Call(ov.hwnd)
	if winDC == 0 {
		return
	}
	defer pReleaseDC.Call(ov.hwnd, winDC)

	memDC, _, _ := pCreateCompatibleDC.Call(winDC)
	if memDC == 0 {
		return
	}
	bmp, _, _ := pCreateCompatibleBitmap.Call(winDC, uintptr(w), uintptr(h))
	if bmp == 0 {
		pDeleteDC.Call(memDC)
		return
	}
	// 旧资源先换出来再删，否则会把位图"留在 DC 里"直接 DeleteObject，泄漏 GDI 对象。
	if ov.memBmp != 0 {
		pSelectObject.Call(ov.memDC, ov.oldBmp)
		pDeleteObject.Call(ov.memBmp)
	}
	if ov.memDC != 0 {
		pDeleteDC.Call(ov.memDC)
	}
	old, _, _ := pSelectObject.Call(memDC, bmp)
	ov.memDC, ov.memBmp, ov.oldBmp = memDC, bmp, old

	// 压暗用的 1x1 黑源 DC 已经不需要了：压暗改成窗口属性（WS_EX_LAYERED）之后，
	// 背景只铺纯黑，既没有 AlphaBlend、也没有那张 1x1 位图。
	// 画刷 / 字体只建一次，destroy 里统一释放。
	if ov.brush == 0 {
		ov.brush, _, _ = pCreateSolidBrush.Call(uintptr(colorrefRed))
	}
	if ov.font == 0 {
		ov.labelSize = overlayFontSize(winDC)
		// CreateFontW 的 lfHeight 是"字符单元格高度"，取负 = 按字符高度而非
		// 单元格高度计算，这样文字不会上下被裁。
		ov.font, _, _ = pCreateFontW.Call(
			uintptr(int32(-ov.labelSize)), 0, 0, 0,
			400, // FW_NORMAL
			0, 0, 0,
			1, // DEFAULT_CHARSET，中文注释/尺寸文字用 "150x120" 是 ASCII，够用
			0, 0,
			5, // CLEARTYPE_QUALITY
			0, // FF_DONTCARE | DEFAULT_PITCH
			0,
		)
	}
}

// overlayFontSize 按当前屏幕 DPI 把逻辑字号换算成像素。
// 不做这一步的话，200% 缩放的高分屏上"150x120"会小到看不清。
func overlayFontSize(dc uintptr) int32 {
	dpi, _, _ := pGetDeviceCaps.Call(dc, uintptr(logPixelsY))
	if dpi == 0 {
		dpi = 96
	}
	return int32(int(dpi) * labelFontAt96DPI / 96)
}

// destroy 逆序释放全部资源。必须可重复调用（会话正常结束时走这里，
// 消息循环里的 WM_DESTROY 也会兜一次）。
func (ov *overlay) destroy() {
	if ov.memDC != 0 {
		if ov.memBmp != 0 {
			pSelectObject.Call(ov.memDC, ov.oldBmp)
		}
		pDeleteDC.Call(ov.memDC)
		ov.memDC = 0
	}
	if ov.memBmp != 0 {
		pDeleteObject.Call(ov.memBmp)
		ov.memBmp = 0
	}
	if ov.brush != 0 {
		pDeleteObject.Call(ov.brush)
		ov.brush = 0
	}
	if ov.blackBrush != 0 {
		pDeleteObject.Call(ov.blackBrush)
		ov.blackBrush = 0
	}
	if ov.font != 0 {
		pDeleteObject.Call(ov.font)
		ov.font = 0
	}
}

// messageLoop 是标准的 GetMessage/Translate/Dispatch 循环（跑在锁定线程上）。
func (ov *overlay) messageLoop() {
	var m msgT
	for {
		// GetMessageW 在窗口被销毁（PostQuitMessage）后返回 0；
		// 返回 -1 才是出错，此时必须跳出，否则就是死循环。
		r, _, callErr := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(r) {
		case 0:
			// 收尾：窗口可能还在（比如消息循环被异常打断），兜底销毁。
			pDestroyWindow.Call(ov.hwnd)
			return
		case -1:
			_ = callErr
			pDestroyWindow.Call(ov.hwnd)
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// ---------------------------------------------------------------------------
// 窗口过程
// ---------------------------------------------------------------------------

// overlayWndProc 是 WNDPROC。它必须是一个普通 Go 函数（由 NewCallback 包装），
// 且调用线程一定是创建窗口那个锁定线程。
func overlayWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	// id 是窗口属性里存的会话 id；取出来备用（WM_DESTROY 时要按它摘注册表）。
	id, _, _ := pGetWindowLongPtrW.Call(hwnd, gwlpUserData)
	var ov *overlay
	if id != 0 {
		ov = lookupOverlay(id)
	}

	switch msg {
	case wmNCCreate:
		// CreateWindowExW 的第一条消息。这里不做任何事：
		// 会话指针是在 create() 里用 SetWindowLongPtrW(GWLP_USERDATA) 挂上去的，
		// 刻意不依赖 CREATESTRUCT.lpCreateParams —— 那个结构体在 x64 上有
		// 11 个字段、顺序还和 MSDN 文档列表不完全一致，靠它取指针等于给自己埋雷。
		return defWindowProc(hwnd, msg, wParam, lParam)

	case wmSetCursor:
		// 返回 TRUE 且不调 SetCursor：光标已经 ShowCursor(FALSE) 隐藏，
		// 这里再把系统光标设回来就等于前功尽弃。
		return 1

	case wmEraseBkgnd:
		// 返回 1 = "我擦过了"。真正的擦除由 WM_PAINT 贴整屏原图完成，
		// 让系统先刷一遍背景色只会造成闪烁。
		return 1

	case wmPaint:
		if ov != nil {
			ov.onPaint()
		} else {
			// 没有会话状态的窗口（理论上不该出现）：走系统默认处理，
			// 但必须配一个 BeginPaint/EndPaint，否则 WM_PAINT 会无限重发。
			var ps paintStructT
			pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
			pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		}
		return defWindowProcOK

	case wmLButtonDown:
		if ov != nil {
			ov.onLButtonDown(getXLParam(lParam), getYLParam(lParam))
		}
		return defWindowProcOK

	case wmMouseMove:
		if ov != nil {
			ov.onMouseMove(getXLParam(lParam), getYLParam(lParam), wParam)
		}
		return defWindowProcOK

	case wmLButtonUp:
		if ov != nil {
			ov.onLButtonUp(getXLParam(lParam), getYLParam(lParam))
		}
		pDestroyWindow.Call(hwnd)
		return defWindowProcOK

	case wmRButtonDown:
		// 右键 = 取消
		if ov != nil {
			ov.cancelled = true
		}
		pDestroyWindow.Call(hwnd)
		return defWindowProcOK

	case wmKeyDown:
		if wParam == vkEscape {
			if ov != nil {
				ov.cancelled = true
			}
			pDestroyWindow.Call(hwnd)
		}
		return defWindowProcOK

	case wmClose:
		// 测试兜底路径：万一测试挂了，发 WM_CLOSE 也能把遮罩收掉，
		// 不至于留一个全屏窗口挡住用户。
		pDestroyWindow.Call(hwnd)
		return defWindowProcOK

	case wmDestroy:
		if ov != nil {
			ov.destroy()
		}
		// 会话从注册表里摘掉（与窗口状态无关，所以放在 if 外面）。
		// 及时删掉，避免连续多次圈选把 map 撑大。
		if id != 0 {
			unregisterOverlay(id)
		}
		if ov != nil {
			ov.hwnd = 0
		}
		pPostQuitMessage.Call(0)
		return defWindowProcOK
	}

	return defWindowProc(hwnd, msg, wParam, lParam)
}

// overlayFromWindow 通过窗口属性里的会话 id 找回会话状态。
//
// 返回 nil 表示这个窗口不是我们的遮罩窗口（或者会话已经收尾），
// 所有调用点都必须能接受 nil。
func overlayFromWindow(hwnd uintptr) *overlay {
	id, _, _ := pGetWindowLongPtrW.Call(hwnd, gwlpUserData)
	if id == 0 {
		return nil
	}
	return lookupOverlay(id)
}

func defWindowProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

// ---------------------------------------------------------------------------
// 消息处理
// ---------------------------------------------------------------------------

func (ov *overlay) onLButtonDown(x, y int) {
	ov.mouse = image.Pt(x, y)
	ov.dragging = true
	ov.haveSel = true
	// anchor 记录"按下时那个角"。拖拽过程中它必须保持不变 ——
	// 如果每次都从 ov.sel.Min 取锚点，反向拖拽（右下→左上）时 Min 会
	// 被上一次移动改写，锚点就跟着往左上漂，选区的 Max 会被越写越小，
	// 最终返回一个 10x10 的碎片矩形。这是本函数最容易写错的地方。
	ov.anchor = image.Pt(x, y)
	ov.sel = image.Rect(x, y, x, y)
	// SetCapture：鼠标拖到窗口外（多屏边缘）也能继续收到 WM_MOUSEMOVE，
	// 否则用户一拖出屏幕范围选区的终点就冻住了。
	pSetCapture.Call(ov.hwnd)
	ov.invalidate()
}

func (ov *overlay) onMouseMove(x, y int, wParam uintptr) {
	ov.mouse = image.Pt(x, y)
	if ov.dragging {
		// 用固定的 anchor 和当前鼠标位置构造矩形：image.Rect 会自动把两个角
		// 归一化成 (min, max)，所以从右下往左上拖也能得到 Dx()>0、Min=左上
		// 的矩形，上层拿到的永远是"左上角 + 尺寸"。
		ov.sel = image.Rect(ov.anchor.X, ov.anchor.Y, x, y)
	}
	// 即使没拖拽也要重绘：十字准星要跟着鼠标走。
	ov.invalidate()
}

func (ov *overlay) onLButtonUp(x, y int) {
	ov.mouse = image.Pt(x, y)
	pReleaseCapture.Call()
	if ov.dragging {
		ov.sel = image.Rect(ov.anchor.X, ov.anchor.Y, x, y)
	}
	ov.dragging = false

	// 太小 = 误点（用户可能只想取消，顺手点一下）。返回一个 1x1 的矩形
	// 对上层的解码毫无意义，还会掩盖"其实没选中"这件事，所以按取消处理。
	if ov.sel.Dx() < minSelectionPx || ov.sel.Dy() < minSelectionPx {
		ov.cancelled = true
	}
}

// invalidate 请求重绘整块客户区。传 nil RECT = 整个客户区。
func (ov *overlay) invalidate() {
	if ov.hwnd == 0 {
		return
	}
	pInvalidateRect.Call(ov.hwnd, 0, 0)
}

// selectionAbsolute 把客户区坐标的选区换算回虚拟桌面绝对坐标。
func (ov *overlay) selectionAbsolute() image.Rectangle {
	return image.Rect(
		ov.vs.Min.X+ov.sel.Min.X,
		ov.vs.Min.Y+ov.sel.Min.Y,
		ov.vs.Min.X+ov.sel.Max.X,
		ov.vs.Min.Y+ov.sel.Max.Y,
	)
}

// ---------------------------------------------------------------------------
// 绘制
// ---------------------------------------------------------------------------

func (ov *overlay) onPaint() {
	var ps paintStructT
	hdc, _, _ := pBeginPaint.Call(ov.hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		// BeginPaint 失败也要 EndPaint，否则 WM_PAINT 会一直排队。
		pEndPaint.Call(ov.hwnd, uintptr(unsafe.Pointer(&ps)))
		return
	}
	// 成对：BeginPaint/EndPaint 必须在同一次 WM_PAINT 里闭合，
	// 少了 EndPaint 系统会认为这块区域永远"还在脏"，WM_PAINT 会无限重发，
	// 表现为 CPU 100% 且界面疯狂闪烁。
	defer pEndPaint.Call(ov.hwnd, uintptr(unsafe.Pointer(&ps)))

	var rc rectT
	pGetClientRect.Call(ov.hwnd, uintptr(unsafe.Pointer(&rc)))
	w, h := int(rc.Right-rc.Left), int(rc.Bottom-rc.Top)

	// 每次 WM_PAINT 都从"整屏原图"重画一遍，而不是只在脏区上叠加：
	// 拖拽时选区每帧都在变，增量重绘必然留下上一帧的边框残影。
	if ov.memDC == 0 {
		ov.recreateBackBuffer()
	}
	// 只诊断一次：客户区尺寸和虚拟桌面尺寸是否一致。
	// 不一致就说明窗口的物理尺寸没对齐虚拟桌面（DPI 缩放 / 窗口管理器调整），
	// 那会让"挖洞"的坐标与真实屏幕对不上 —— 记下来，别靠猜。
	if !ov.loggedSizes {
		ov.loggedSizes = true
		Tracef("qrscan 尺寸核对：客户区=%dx%d 虚拟桌面=%dx%d", w, h, ov.vs.Dx(), ov.vs.Dy())
	}
	// 【不再贴截图】背景只铺一层纯黑：窗口本身是 LWA_ALPHA 半透明的，
	// "黑 + 半透明"就是整屏压暗。
	if ov.blackBrush == 0 {
		ov.blackBrush, _, _ = pCreateSolidBrush.Call(0)
	}
	fillRect(ov.memDC, ov.blackBrush, image.Rect(0, 0, w, h))

	// 选区那块不靠"挖像素"来透亮，而是把窗口区域挖个洞漏出**真实桌面**
	// （对齐由窗口管理器保证）。区域只在选区变化时才重设 —— 否则
	// SetWindowRgn(TRUE) 会在 WM_PAINT 里触发下一次重绘，形成死循环。
	if ov.sel != ov.regionSel {
		ov.regionSel = ov.sel
		ov.applySelectionRegion()
	}

	ov.paintSelection()
	ov.paintCrosshair(w, h)

	if ov.memDC != 0 {
		pBitBlt.Call(hdc, 0, 0, uintptr(w), uintptr(h), ov.memDC, 0, 0, uintptr(srcCopy))
	}
}

// paintSelection 画选区：2 像素红色实线边框 + 左上角的尺寸文字。
// ---------------------------------------------------------------------------
// 遮罩（压暗 / 挖空）
// ---------------------------------------------------------------------------

func (ov *overlay) paintSelection() {
	if ov.memDC == 0 || !ov.haveSel || ov.sel.Empty() {
		return
	}
	r := ov.sel
	// 用 4 条 FillRect 拼出边框，而不是 FrameRect：FrameRect 只能画 1 像素，
	// 要做 2 像素就得连画两次并反复调整坐标，反而更容易算错。
	drawBorderRect(ov.memDC, ov.brush, r, selectionBorderPx)

	if ov.font == 0 {
		return
	}
	// 文字位置夹一下：选区贴着屏幕右下角、或者选得太矮时，
	// 文字会被窗口裁掉或者压在边框上看不清，就挪到选区下方。
	textY := r.Min.Y + labelOffsetY
	if int(ov.labelSize)+8 > r.Dy() {
		textY = r.Max.Y + labelOffsetY
	}
	textX := r.Min.X + labelOffsetX
	if textX < 0 {
		textX = 0
	}
	if textY < 0 {
		textY = 0
	}
	oldFont, _, _ := pSelectObject.Call(ov.memDC, ov.font)
	pSetTextColor.Call(ov.memDC, uintptr(colorrefRed))
	pSetBkMode.Call(ov.memDC, uintptr(bkModeTransparent))
	text := fmt.Sprintf("%dx%d", r.Dx(), r.Dy())
	u16, err := syscall.UTF16FromString(text)
	if err == nil && len(u16) > 1 {
		pTextOutW.Call(ov.memDC, uintptr(int32(textX)), uintptr(int32(textY)),
			uintptr(unsafe.Pointer(&u16[0])), uintptr(len(u16)-1))
	}
	pSelectObject.Call(ov.memDC, oldFont)
}

// applySelectionRegion 在窗口区域上"挖"出选区。
//
// 【为什么这样做】早先是"把整屏截图贴到画布上、再自己算坐标把选区那块重新贴一遍"——
// 坐标只要有一点点不一致，选区内外就会错位（用户实报过，而且我连测几轮都没定位准）。
// 现在改成：压暗层是一个半透明黑窗口，把它的**窗口区域**设成"整屏减去选区"，
// 选区那块就不再被窗口覆盖 —— 用户看到的是**真实的、未压暗的桌面**。
// 对齐由窗口管理器保证，没有任何坐标换算，所以这一类错位从结构上消失。
func (ov *overlay) applySelectionRegion() {
	if ov.hwnd == 0 {
		return
	}
	full, _, _ := pCreateRectRgn.Call(0, 0, uintptr(ov.vs.Dx()), uintptr(ov.vs.Dy()))
	if full == 0 {
		return
	}
	if ov.sel.Empty() {
		// 还没有选区：整块都要压暗
		pSetWindowRgn.Call(ov.hwnd, full, 1)
		return // 成功后区域归系统所有，不能再 DeleteObject
	}
	// 洞比选区**内缩**一个边框宽度：让那 2px 边框仍然画在窗口上，
	// 否则边框正好压在洞口边缘会被区域一起裁掉。
	hole := ov.sel.Inset(selectionBorderPx)
	if hole.Dx() <= 0 || hole.Dy() <= 0 {
		hole = ov.sel
	}
	h, _, _ := pCreateRectRgn.Call(
		uintptr(int64(hole.Min.X)), uintptr(int64(hole.Min.Y)),
		uintptr(int64(hole.Max.X)), uintptr(int64(hole.Max.Y)))
	if h != 0 {
		pCombineRgn.Call(full, full, h, uintptr(rgnDiff))
		pDeleteObject.Call(h)
	}
	pSetWindowRgn.Call(ov.hwnd, full, 1)
}

// drawBorderRect 用画刷在 dc 上画一条 thickness 像素宽、画在 r 内侧的实线边框。
func drawBorderRect(dc, brush uintptr, r image.Rectangle, thickness int) {
	if brush == 0 || r.Empty() {
		return
	}
	t := thickness
	// 上
	fillRect(dc, brush, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+t))
	// 下
	fillRect(dc, brush, image.Rect(r.Min.X, r.Max.Y-t, r.Max.X, r.Max.Y))
	// 左（去掉上下已被覆盖的部分，避免重复填充）
	fillRect(dc, brush, image.Rect(r.Min.X, r.Min.Y+t, r.Min.X+t, r.Max.Y-t))
	// 右
	fillRect(dc, brush, image.Rect(r.Max.X-t, r.Min.Y+t, r.Max.X, r.Max.Y-t))
}

// fillRect 把 image.Rectangle 转成 Win32 RECT 后填充。
// 坐标类型转换只在这一处发生，其它绘制代码都用 int，省掉满屏 int32() 转换。
func fillRect(dc, brush uintptr, r image.Rectangle) {
	if dc == 0 || brush == 0 || r.Empty() {
		return
	}
	rc := rectT{
		Left:   int32(r.Min.X),
		Top:    int32(r.Min.Y),
		Right:  int32(r.Max.X),
		Bottom: int32(r.Max.Y),
	}
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&rc)), brush)
}

// paintCrosshair 在鼠标位置画"贯穿整个客户区"的红色十字准星。
//
// 之所以不用 CreateCursor 自定义光标：那套 API 要求位图是单色/彩色 AND+XOR 掩码，
// 在 DPI 缩放和多屏混合缩放下经常出现"光标被拉伸成马赛克"，而且还得处理
// GetSystemMetrics(SM_CXCURSOR) 之类的兼容问题。自己在 WM_PAINT 里画线
// 简单、可控，而且顺便天然支持"拖拽中准星跟着走"。
func (ov *overlay) paintCrosshair(w, h int) {
	if ov.memDC == 0 || ov.brush == 0 {
		return
	}
	mx, my := ov.mouse.X, ov.mouse.Y
	if mx < 0 || my < 0 || mx >= w || my >= h {
		// 鼠标不在客户区里（拖拽出界）就不画，避免在屏幕边缘画出半截怪线。
		return
	}
	// 横线：贯穿整个宽度，1 像素高，交点在鼠标 y 上
	fillRect(ov.memDC, ov.brush, image.Rect(0, my, w, my+crosshairPx))
	// 竖线：贯穿整个高度，1 像素宽，交点在鼠标 x 上
	fillRect(ov.memDC, ov.brush, image.Rect(mx, 0, mx+crosshairPx, h))
}

// ---------------------------------------------------------------------------
// lParam 坐标解析
//
// WM_MOUSEMOVE 的 lParam 里，x 在低 16 位、y 在高 16 位，都是有符号的。
// 副屏在主屏左边时客户区坐标本身不会为负（客户区从 0 开始），但用
// lParam 打包 PostMessage 时同样要按 uint16 截断，所以两边都要做有符号扩展，
// 保持对称 —— 这样测试里 ma 出来的消息和真实鼠标消息走的是同一条代码路径。
// ---------------------------------------------------------------------------

func getXLParam(lp uintptr) int { return int(int16(lp & 0xffff)) }

func getYLParam(lp uintptr) int { return int(int16((lp >> 16) & 0xffff)) }

// makeLParam 把客户区坐标打包成 lParam（按 uint16 截断，允许负值）。
//
// 生产和测试共用这一个函数：测试用 PostMessage 造出来的鼠标消息，
// 和真实鼠标消息走的是同一条解包代码路径，所以测试结果有说服力。
func makeLParam(x, y int) uintptr {
	return uintptr(uint16(x)) | uintptr(uint16(y))<<16
}
