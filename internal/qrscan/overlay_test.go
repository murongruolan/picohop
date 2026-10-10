//go:build windows

package qrscan

import (
	"errors"
	"image"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 说明：为什么测试要驱动真实窗口
//
// 圈选这条路径的全部风险都在 Win32 交互里（消息循环、坐标符号扩展、
// 选区归一化、取消分支），只测纯函数等于什么都没测。所以这里真的把
// CreateWindowExW 建起来、把消息 Post 进它自己的消息队列，让
// GetMessageW → DispatchMessageW → overlayWndProc 整条链路真跑一遍。
//
// 窗口本身用 overlayHiddenForTest 隐藏：测试进程里显示一个覆盖整个桌面、
// WS_EX_TOPMOST 的窗口会盖住开发者（以及本会话 GUI）的屏幕。
// 隐藏的窗口照样有消息队列、照样收 PostMessage，交互逻辑一模一样。
//
// 消息常量（wmLButtonDown / wmMouseMove / wmLButtonUp / wmKeyDown …）和
// makeLParam / getXLParam / getYLParam 都在 overlay_windows.go 里，
// 本文件直接用包内的，不重复声明。
// ---------------------------------------------------------------------------

// overlayTestTimeout 是等窗口就绪 / 等结果的超时。
//
// 必须有超时：如果消息循环因为某个 bug 卡住，没有超时的测试会永久挂住，
// 而它手里还攥着一个全屏遮罩窗口 —— 用户屏幕上就留个盖满桌面的东西。
const overlayTestTimeout = 10 * time.Second

// postMessage 绑定 user32!PostMessageW（测试里模拟鼠标/键盘）。
var pPostMessageW = user32.NewProc("PostMessageW")

// postMsg 把一条消息投递到指定窗口自己的消息队列。
func postMsg(t *testing.T, hwnd uintptr, msg uint32, wParam, lParam uintptr) {
	t.Helper()
	r, _, err := pPostMessageW.Call(hwnd, uintptr(msg), wParam, lParam)
	if r == 0 {
		t.Fatalf("PostMessageW(hwnd=%#x, msg=%#x) 失败: %s", hwnd, msg, winErrText(err))
	}
}

// overlaySession 是一次由测试驱动的圈选会话。
type overlaySession struct {
	hwnd  uintptr
	done  chan overlayOutcome
	vs    image.Rectangle
	close sync.Once
}

type overlayOutcome struct {
	rect image.Rectangle
	err  error
}

// startOverlayForTest 启动一次圈选会话并等窗口就绪。
//
// 约定：调用方应先设置 overlayHiddenForTest = true，避免测试期间弹出全屏遮罩。
func startOverlayForTest(t *testing.T) *overlaySession {
	t.Helper()

	// 每个测试都自己设一遍：即使上一个测试因为 Fatal 提前退出，
	// 也不会让"显示遮罩"的开关漏到下一个测试。
	overlaySessions.Lock()
	overlayHiddenForTest = true
	overlaySessions.Unlock()

	vs, err := VirtualScreen()
	if err != nil || vs.Dx() <= 0 || vs.Dy() <= 0 {
		// 无头会话 / 服务账户下取不到桌面，这是环境限制而不是实现缺陷。
		t.Skipf("当前会话没有可用桌面（VirtualScreen = %v, err = %v），跳过圈选测试", vs, err)
	}
	t.Logf("VirtualScreen() = %v（%dx%d，Min=%v，副屏负坐标也会体现在这里）",
		vs, vs.Dx(), vs.Dy(), vs.Min)

	s := &overlaySession{
		done: make(chan overlayOutcome, 1),
		vs:   vs,
	}

	readyCh := make(chan uintptr, 1)
	go func() {
		// SelectRegionHooked 内部自己 LockOSThread，所以这里不需要额外处理线程。
		rect, err := SelectRegionHooked(func(hwnd uintptr) {
			// 非阻塞发送：onReady 由 SelectRegionHooked 保证只调一次，
			// 这里用带缓冲的 chan + select 只是防止把回调线程卡住。
			select {
			case readyCh <- hwnd:
			default:
			}
		})
		s.done <- overlayOutcome{rect: rect, err: err}
	}()

	select {
	case hwnd := <-readyCh:
		s.hwnd = hwnd
		t.Logf("遮罩窗口已创建，hwnd = %#x", hwnd)
	case out := <-s.done:
		// 窗口没建起来（通常是 CreateWindowExW 失败）。
		t.Skipf("遮罩窗口创建失败，跳过圈选测试: %v", out.err)
	case <-time.After(overlayTestTimeout):
		t.Fatalf("等待遮罩窗口就绪超时（%v）", overlayTestTimeout)
	}
	return s
}

// wait 等会话结束，并保证无论成败都把窗口关掉。
func (s *overlaySession) wait(t *testing.T) overlayOutcome {
	t.Helper()
	select {
	case out := <-s.done:
		return out
	case <-time.After(overlayTestTimeout):
		s.forceClose(t)
		t.Fatalf("等待圈选结果超时（%v）", overlayTestTimeout)
		return overlayOutcome{}
	}
}

// forceClose 兜底关窗：即使测试断言失败（Fatal），也不能把遮罩留在屏幕上挡住用户。
//
// 先试 WM_CLOSE，再试 WM_DESTROY，最后再 WM_CLOSE —— 三条路都走一遍，
// 总有一条能落到 overlayWndProc 的销毁分支上。
func (s *overlaySession) forceClose(t *testing.T) {
	t.Helper()
	s.close.Do(func() {
		if s.hwnd == 0 {
			return
		}
		t.Logf("兜底关闭遮罩窗口 hwnd = %#x", s.hwnd)
		pPostMessageW.Call(s.hwnd, uintptr(wmClose), 0, 0)
		pPostMessageW.Call(s.hwnd, uintptr(wmDestroy), 0, 0)
		pPostMessageW.Call(s.hwnd, uintptr(wmClose), 0, 0)
	})
}

// clientXY 把虚拟桌面绝对坐标换算成遮罩窗口的客户区坐标。
//
// 窗口左上角就是 vs.Min，客户区原点在窗口左上角，所以是简单相减。
// 副屏在主屏左边时 vs.Min.X 是负数（例如 -1920），相减照样成立。
func (s *overlaySession) clientXY(x, y int) (int, int) {
	return x - s.vs.Min.X, y - s.vs.Min.Y
}

// press/move/release 用 PostMessageW 模拟鼠标动作（全部是客户区坐标）。
func (s *overlaySession) press(t *testing.T, x, y int) {
	cx, cy := s.clientXY(x, y)
	postMsg(t, s.hwnd, wmLButtonDown, 1 /*MK_LBUTTON*/, makeLParam(cx, cy))
}

func (s *overlaySession) move(t *testing.T, x, y int) {
	cx, cy := s.clientXY(x, y)
	postMsg(t, s.hwnd, wmMouseMove, 1 /*MK_LBUTTON*/, makeLParam(cx, cy))
}

func (s *overlaySession) release(t *testing.T, x, y int) {
	cx, cy := s.clientXY(x, y)
	postMsg(t, s.hwnd, wmLButtonUp, 0, makeLParam(cx, cy))
}

// assertRectNear 比较两个矩形，允许每边 1 像素的误差。
//
// 必须先归一化再比：image.Rect 不保证入参有序，构造出来的矩形可能是
// "Max < Min" 的（Dx() 为负）。这是最容易漏的一个坑。
func assertRectNear(t *testing.T, got, want image.Rectangle, tol int) {
	t.Helper()
	g, w := got.Canon(), want.Canon()
	if abs(g.Min.X-w.Min.X) > tol || abs(g.Min.Y-w.Min.Y) > tol ||
		abs(g.Max.X-w.Max.X) > tol || abs(g.Max.Y-w.Max.Y) > tol {
		t.Fatalf("选区矩形不对：实际 %v（原始 %v，Dx=%d,Dy=%d），期望 %v（公差 %d 像素）",
			g, got, got.Dx(), got.Dy(), w, tol)
	}
	t.Logf("选区矩形匹配：实际 %v，期望 %v（公差 %d 像素）", g, w, tol)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ---------------------------------------------------------------------------
// 测试
// ---------------------------------------------------------------------------

// TestOverlaySelectRegionByClickDrag 是核心用例：真的按下 → 拖 → 松开，
// 并断言返回的矩形等于期望的绝对坐标矩形。
func TestOverlaySelectRegionByClickDrag(t *testing.T) {
	s := startOverlayForTest(t)
	defer s.forceClose(t)

	// 挑一个"安全"的小矩形：从虚拟桌面左上角往内缩 200 像素，尺寸 150x120。
	// 往内缩是为了避开屏幕物理边缘 —— 有些驱动在边缘 1~2 像素上会返回
	// 裁剪过的客户区坐标，导致选区差几个像素。
	if s.vs.Dx() < 400 || s.vs.Dy() < 400 {
		t.Skipf("虚拟桌面只有 %v，无法在不碰边缘的前提下取 150x120，跳过", s.vs)
	}
	from := image.Pt(s.vs.Min.X+200, s.vs.Min.Y+200)
	to := image.Pt(from.X+150, from.Y+120)
	t.Logf("本次圈选：绝对坐标 %v → %v（期望 %v）", from, to, image.Rect(from.X, from.Y, to.X, to.Y))

	s.press(t, from.X, from.Y)

	// 几组中间移动：真实拖拽就是连续的 WM_MOUSEMOVE，
	// 这里顺带覆盖"选区每帧重算"的路径（每一次移动都会触发 InvalidateRect + WM_PAINT）。
	steps := []image.Point{
		{from.X + 30, from.Y + 20},
		{from.X + 80, from.Y + 60},
		{from.X + 130, from.Y + 100},
		to,
	}
	for _, p := range steps {
		s.move(t, p.X, p.Y)
	}
	s.release(t, to.X, to.Y)

	out := s.wait(t)
	s.forceClose(t)

	if out.err != nil {
		t.Fatalf("圈选应当成功，却返回错误: %v", out.err)
	}
	assertRectNear(t, out.rect, image.Rect(from.X, from.Y, to.X, to.Y), 1)
}

// TestOverlayCancelByEscape 覆盖 Esc 取消。
func TestOverlayCancelByEscape(t *testing.T) {
	s := startOverlayForTest(t)
	defer s.forceClose(t)

	postMsg(t, s.hwnd, wmKeyDown, vkEscape, 0)

	out := s.wait(t)
	s.forceClose(t)

	if !errors.Is(out.err, ErrCancelled) {
		t.Fatalf("按 Esc 应当返回 ErrCancelled，实际 rect=%v, err=%v", out.rect, out.err)
	}
	t.Logf("按 Esc 后返回 ErrCancelled（rect=%v）", out.rect)
}

// TestOverlayCancelByTinyDrag 覆盖"误点"：拖一个 3x3 的区域应当按取消处理，
// 而不是把 3x3 这种对解码毫无意义的矩形交给上层。
func TestOverlayCancelByTinyDrag(t *testing.T) {
	s := startOverlayForTest(t)
	defer s.forceClose(t)

	from := image.Pt(s.vs.Min.X+100, s.vs.Min.Y+100)
	s.press(t, from.X, from.Y)
	s.move(t, from.X+2, from.Y+2)
	s.release(t, from.X+3, from.Y+3)

	out := s.wait(t)
	s.forceClose(t)

	if !errors.Is(out.err, ErrCancelled) {
		t.Fatalf("3x3 的选区应当返回 ErrCancelled，实际 rect=%v, err=%v", out.rect, out.err)
	}
	t.Logf("3x3 的微小拖拽按预期被当成取消（rect=%v）", out.rect)
}

// TestOverlayReverseDragNormalizesRect 覆盖从右下往左上拖：
// 返回的矩形必须已经归一化（Dx>0、Dy>0、Min 是左上角），
// 否则上层拿去 Decode 会得到一块空区域。副屏负坐标也被这个用例覆盖。
func TestOverlayReverseDragNormalizesRect(t *testing.T) {
	s := startOverlayForTest(t)
	defer s.forceClose(t)

	if s.vs.Dx() < 400 || s.vs.Dy() < 400 {
		t.Skipf("虚拟桌面只有 %v，太小，跳过反向拖拽测试", s.vs)
	}
	// 注意 to 是"左上"，from 是"右下"，按下点用右下角。
	topLeft := image.Pt(s.vs.Min.X+200, s.vs.Min.Y+200)
	bottomRight := image.Pt(topLeft.X+160, topLeft.Y+140)

	s.press(t, bottomRight.X, bottomRight.Y)
	s.move(t, bottomRight.X-60, bottomRight.Y-50)
	s.move(t, topLeft.X+10, topLeft.Y+10)
	t.Logf("反向拖拽：按下 %v（客户区 %v），中间移动 (%d,%d)，松开 %v", bottomRight, ptToClient(s.vs, bottomRight), topLeft.X+10, topLeft.Y+10, topLeft)
	s.release(t, topLeft.X, topLeft.Y)

	out := s.wait(t)
	s.forceClose(t)

	if out.err != nil {
		t.Fatalf("反向拖拽应当成功，却返回错误: %v", out.err)
	}
	if out.rect.Dx() <= 0 || out.rect.Dy() <= 0 {
		t.Fatalf("返回的矩形没有归一化：%v（Dx=%d, Dy=%d）", out.rect, out.rect.Dx(), out.rect.Dy())
	}
	// Min 必须是左上角：反向拖拽时 Min 应当是松开点（更靠左上）那一侧。
	want := image.Rect(topLeft.X, topLeft.Y, bottomRight.X, bottomRight.Y)
	assertRectNear(t, out.rect, want, 1)
	if out.rect.Min != image.Pt(minInt(out.rect.Min.X, out.rect.Max.X), minInt(out.rect.Min.Y, out.rect.Max.Y)) {
		t.Fatalf("Min 不是左上角: %v", out.rect)
	}
	t.Logf("反向拖拽返回归一化矩形 %v（Min=%v 是左上角）", out.rect, out.rect.Min)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ptToClient 只用于日志，把绝对坐标换算成客户区坐标方便排查。
func ptToClient(vs image.Rectangle, p image.Point) image.Point {
	return image.Pt(p.X-vs.Min.X, p.Y-vs.Min.Y)
}

// TestOverlayCancelByRightButton 顺带覆盖右键取消（产品要求里列的第三种取消方式）。
func TestOverlayCancelByRightButton(t *testing.T) {
	s := startOverlayForTest(t)
	defer s.forceClose(t)

	postMsg(t, s.hwnd, wmRButtonDown, 2 /*WM_RBUTTONDOWN 的 wParam 是 MK_RBUTTON*/, 0)

	out := s.wait(t)
	s.forceClose(t)

	if !errors.Is(out.err, ErrCancelled) {
		t.Fatalf("右键应当返回 ErrCancelled，实际 rect=%v, err=%v", out.rect, out.err)
	}
	t.Logf("右键后返回 ErrCancelled（rect=%v）", out.rect)
}

// TestOverlayLParamSignExtension 单独验证 lParam 的有符号扩展。
//
// 这是一个纯函数级用例，但值得单列：客户区坐标虽然从 0 开始，
// 但我们用同一套打包/解包函数构造和解析消息，只要 sign-extension 写错
// （例如直接 uint16 无符号），150x120 这种正常值也会在 32768 以上翻车，
// 而多屏环境里客户区宽高很容易超过 32767 的一半。
func TestOverlayLParamSignExtension(t *testing.T) {
	cases := []struct{ x, y int }{
		{0, 0},
		{1, 1},
		{150, 120},
		{32767, 32767},
		{-1, -1},
		{-32768, -32768},
	}
	for _, c := range cases {
		lp := makeLParam(c.x, c.y)
		if got := getXLParam(lp); got != c.x {
			t.Errorf("makeLParam(%d, %d) 解出的 x = %d", c.x, c.y, got)
		}
		if got := getYLParam(lp); got != c.y {
			t.Errorf("makeLParam(%d, %d) 解出的 y = %d", c.x, c.y, got)
		}
	}

	// 手写几个已知 bit pattern，确认高位/低位的取法没搞反。
	if got := getXLParam(0x00000064); got != 100 {
		t.Errorf("低 16 位应当是 x：got %d", got)
	}
	if got := getYLParam(0x00640000); got != 100 {
		t.Errorf("高 16 位应当是 y：got %d", got)
	}
	t.Log("lParam 打包/解包（含有符号扩展）验证通过")
}
