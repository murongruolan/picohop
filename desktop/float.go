package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// ---------------------------------------------------------------------------
// 浮动对话框：把某个对话框放到**主窗口之外**的独立 OS 窗口里显示。
//
// 为什么必须这么做：DOM 元素只能画在它所属窗口的客户区里，不管怎么移动
// 都会被主窗口的边界裁掉。想要"弹窗能拖到主窗口外面、还永远压在主窗口上面"，
// 唯一的办法就是给它一个真正的窗口 —— 而这正是从 Wails v2 升到 v3 的原因
// （v2 连创建窗口的 API 都没有）。
//
// 实现方式刻意做成"复用同一份前端"：浮动窗口加载的还是同一个 SPA，
// 只是带 ?float=<kind> 参数。这样对话框组件、它的校验与保存逻辑一行都不用重写，
// 差别只有两点：
//   - 关掉对话框时要把结果回传给 Go（见 ui.onNodeDialogClosed）
//   - CSS 把主界面藏起来、让对话框铺满整个窗口（见 styles.css 的 .cp-float-window）
// ---------------------------------------------------------------------------

// floatSession 是一次浮动对话框的生命周期。
type floatSession struct {
	kind    string
	payload string
	window  application.Window
	result  chan string
	// closing 表示"这次关闭是我们自己发起的"，用来让关窗钩子放行。
	closing atomic.Bool
	// reused 表示这一轮是复用已有窗口（不是新建）。
	reused bool
	// wakeAt 是"我们让窗口开始进入这一轮"的时刻，用来量前端多久才能把对话框显示出来。
	// 首次打开和复用打开都会记 —— 用户反馈"打开明显卡"，快了多少必须有数，不能凭感觉。
	wakeAt time.Time
}

var (
	floatMu      sync.Mutex
	floatCurrent *floatSession

	// floatPool 保存"已经建好、当前没在用"的浮动窗口（按 kind 复用）。
	//
	// 【为什么需要它】用户反馈"打开添加节点的弹窗有明显的延迟感"。原因是**每次打开
	// 都要新建一个 WebView2 控制器，并把整个前端（1.15MB JS + Element Plus）加载解析
	// 一遍** —— 这两块加起来就是几百毫秒到一秒。窗口本身完全可以复用：
	// 关闭时只隐藏，下次打开让窗口里的前端重新进入"浮动对话框模式"即可，
	// 既不新建 WebView2、也不重新加载页面，于是打开是"立刻"的。
	floatPool = map[string]application.Window{}
)

// 各类浮动对话框的初始窗口尺寸。宽度要留够表单。
var floatSizes = map[string][2]int{
	"node": {820, 680},
}

// 各类浮动对话框的窗口标题。
//
// 窗口是无边框的，所以在界面上看不到它 —— 但标题仍然有用：
// 它决定任务栏/Alt+Tab 里显示什么，也是自测截图按标题定位窗口的依据
// （主窗口叫"PicoHop · 链式代理"，浮动窗口必须能区分开）。
var floatTitles = map[string]string{
	"node": "PicoHop · 添加 / 编辑节点",
}

// OpenFloatDialog 打开一个浮动对话框，并**阻塞等待**它的结果。
//
// kind 决定显示哪个对话框；payloadJSON 是它需要的初始数据（例如编辑时的节点）。
// 返回空串表示用户取消、或者直接把窗口关掉了。
//
// 前端在对话框结束时调 SubmitFloatDialog 回传结果，于是这个阻塞调用被唤醒。
func (a *API) OpenFloatDialog(kind, payloadJSON, devOpen string) (string, error) {
	if a.wapp == nil {
		return "", fmt.Errorf("应用尚未就绪")
	}
	size, ok := floatSizes[kind]
	if !ok {
		return "", fmt.Errorf("不支持的浮动对话框类型：%s", kind)
	}

	// 同一时刻只允许一个浮动对话框。再来一个就把它叫到前面 ——
	// 叠多个浮动窗口既挡视线，也让"结果该回给谁"变得含糊。
	floatMu.Lock()
	if floatCurrent != nil {
		cur := floatCurrent
		floatMu.Unlock()
		if cur.window != nil {
			cur.window.Show()
			cur.window.Focus()
		}
		return "", fmt.Errorf("已经有一个浮动对话框打开了")
	}
	sess := &floatSession{kind: kind, payload: payloadJSON, result: make(chan string, 1)}
	floatCurrent = sess
	floatMu.Unlock()

	// 位置每次重算（下面复用分支也要用）：用户要求"每次都在主界面中间打开"，
	// 而窗口是**复用**的 —— 不复位就会停在上次关闭的地方。
	fx, fy := a.floatPosition(size[0], size[1])

	// 【复用：已经有建好的同类型窗口就直接唤醒它】
	//
	// 放在 payload/会话登记之后、建窗口之前 —— 会话必须先生效，
	// 因为窗口里的前端一被唤醒就会来取 payload。
	floatMu.Lock()
	reused := floatPool[kind]
	floatMu.Unlock()
	if reused != nil {
		sess.window = reused
		sess.reused = true
		sess.wakeAt = time.Now()
		// 让窗口里的前端重新进入浮动模式（它会自己去 GetFloatPayload 取新数据）。
		//
		// 用 JS 调用而不是 SetURL 重新导航：重新导航会把整个前端再解析一遍，
		// 而那正是我们要省掉的开销。
		// ExecJS 没有返回值（v3 里它在窗口未就绪时会静默忽略），所以只调不问结果；
		// 真正的确认靠前端：它一被唤醒就会来取 payload（见 GetFloatPayload 的计时日志）。
		// 复位到主窗口中央：窗口是复用的，不搬回来就会停在用户上次拖到的位置
		reused.SetPosition(fx, fy)
		reused.ExecJS("window.__cpFloatReopen && window.__cpFloatReopen()")
		reused.Show()
		reused.Focus()
		return <-sess.result, nil
	}

	title := floatTitles[kind]
	if title == "" {
		title = "PicoHop · " + kind
	}
	// 不是 AlwaysOnTop：用户明确说过"它只比主窗口优先级高就行，现在是比任何应用
	// 优先级都高"。所以改成把浮动窗口设成**主窗口的附属窗口**：
	// 永远在主窗口之上，但不会盖住别的程序（见 setWindowOwner 的注释）。
	// 拖动由前端在标题栏上设 CSS `--wails-draggable: drag` 实现（见 styles.css）。
	win := a.wapp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "float-" + kind,
		Title:            title,
		Width:            size[0],
		Height:           size[1],
		MinWidth:         560,
		MinHeight:        360,
		Frameless:        true,
		AlwaysOnTop:      false,
		InitialPosition:  application.WindowXY,
		X:                fx,
		Y:                fy,
		BackgroundColour: application.NewRGB(20, 22, 28),
		URL:              floatURL(kind, devOpen),
		Windows: application.WindowsWindow{
			Theme: a.initialWindowTheme(),
			// 无边框窗口保留系统阴影与圆角，看起来才不像"一块贴上去的板子"
			DisableFramelessWindowDecorations: false,
		},
	})
	sess.window = win
	sess.wakeAt = time.Now()
	floatMu.Lock()
	floatPool[kind] = win
	floatMu.Unlock()

	// 把浮动窗口挂到主窗口名下（附属窗口）。
	// 窗口是异步建出来的，句柄要等一下才有，所以放到 goroutine 里重试几次。
	// 这一步失败不影响功能（只是层级退化成普通窗口），所以只记日志。
	go func() {
		for i := 0; i < 40; i++ {
			time.Sleep(50 * time.Millisecond)
			child := findOwnWindow(title)
			if child == 0 {
				continue
			}
			owner := findOwnWindow(mainWindowTitle)
			if owner == 0 {
				continue
			}
			if !setWindowOwner(child, owner) {
				a.logf("浮动窗口设为附属窗口失败（不影响使用，只是层级退化成普通窗口）")
			}
			return
		}
		a.logf("浮动窗口句柄未能定位，未设为附属窗口（其标题为 %q）", title)
	}()

	// 用户直接点浮动窗口的关闭（或 Esc）→ 按"取消"处理。
	//
	// 两处细节：
	//   - 必须先 e.Cancel()：默认行为是销毁窗口，那样等待方就永远醒不来了
	//   - 真正的关闭放到 goroutine 里异步做：在窗口事件回调里同步关窗是危险的
	// 注意这里**动态查**当前会话，而不是捕获上面那个 sess：
	// 窗口会跨多次打开复用，而钩子只在建窗口时注册一次 —— 捕获旧 sess 的话，
	// 第二次打开时它已经不成立了（旧会话的 closing 是 true，钩子会放行真正的关闭，
	// 于是用户一点 X 窗口就被销毁、复用池里留了个死窗口）。
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		floatMu.Lock()
		cur := floatCurrent
		floatMu.Unlock()
		if cur == nil || cur.closing.Load() {
			return // 没有进行中的会话，或我们自己发起的，放行
		}
		e.Cancel()
		go a.finishFloat("", true)
	})

	// 阻塞等结果。这是"调用方（主窗口的前端）await 它"的实现：
	// JS 那边一个 await 就能拿到用户在浮动窗口里的操作结果。
	return <-sess.result, nil
}

// floatPosition 算浮动窗口的初始位置。
//
// 默认**居中在主窗口上**（用户要求），然后再夹进屏幕工作区 ——
// 主窗口贴着屏幕边缘、或者屏幕比浮动窗口还小时，不能跑到屏幕外面。
//
// 注意这只是**初始**位置：它是独立窗口，用户随时可以把它拖到主窗口之外，
// 甚至拖到另一个显示器上，主窗口不会把它裁剪掉。
func (a *API) floatPosition(w, h int) (int, int) {
	x, y := 240, 180
	if a.mainWin != nil {
		mx, my := a.mainWin.Position()
		mw, mh := a.mainWin.Size()
		// 居中在主窗口上。原先是从主窗口左上角偏移一点（"像从里面弹出来"），
		// 但用户明确要"在主界面中间打开" —— 居中更像原生弹窗，视线也不用找。
		if mw > 0 && mh > 0 {
			x, y = mx+(mw-w)/2, my+(mh-h)/2
		} else if mx != 0 || my != 0 {
			x, y = mx+80, my+60
		}
	}
	if a.wapp == nil {
		return x, y
	}
	s := a.wapp.Screen.GetPrimary()
	if s == nil || s.WorkArea.Width <= 0 || s.WorkArea.Height <= 0 {
		return x, y
	}
	wa := s.WorkArea
	if maxX := wa.X + wa.Width - w - 12; x > maxX {
		x = maxX
	}
	if maxY := wa.Y + wa.Height - h - 12; y > maxY {
		y = maxY
	}
	if x < wa.X+12 {
		x = wa.X + 12
	}
	if y < wa.Y+12 {
		y = wa.Y + 12
	}
	return x, y
}

// FloatWindowBounds 返回当前浮动窗口的位置与尺寸；没有浮动窗口时返回空 map。
//
// 为什么要暴露给前端：前端（主窗口）没法可靠地查询"另一个窗口"的几何信息 ——
// v3 前端的 Window.Get(name) 是相对于**当前窗口**的，从主窗口去查浮动窗口
// 会拿到主窗口自己的数据（实测踩过）。窗口名册在 Go 侧，这里由 Go 权威地回答。
func (a *API) FloatWindowBounds() map[string]int {
	floatMu.Lock()
	sess := floatCurrent
	floatMu.Unlock()
	if sess == nil || sess.window == nil {
		return map[string]int{}
	}
	x, y := sess.window.Position()
	w, h := sess.window.Size()
	return map[string]int{"x": x, "y": y, "width": w, "height": h}
}

// MainWindowBounds 返回主窗口的位置与尺寸（用于对比浮动窗口是否在它之外）。
func (a *API) MainWindowBounds() map[string]int {
	if a.mainWin == nil {
		return map[string]int{}
	}
	x, y := a.mainWin.Position()
	w, h := a.mainWin.Size()
	return map[string]int{"x": x, "y": y, "width": w, "height": h}
}

// MoveFloatWindow 把当前浮动窗口移到 (x,y)；返回 false 表示没有浮动窗口。
//
// 它同时是"把弹窗挪回屏幕里"这一动作的实现 —— 用户把浮动窗口拖到屏幕外
// （或者换了一块更小的显示器）之后，界面上的按钮可以靠它把窗口叫回来。
func (a *API) MoveFloatWindow(x, y int) bool {
	floatMu.Lock()
	sess := floatCurrent
	floatMu.Unlock()
	if sess == nil || sess.window == nil {
		return false
	}
	sess.window.SetPosition(x, y)
	return true
}

// floatURL 拼浮动窗口要加载的地址。
//
// devOpen 是"透传给浮动窗口的自测参数"（正常调用传空串）。为什么要透传：
// 浮动窗口里的动作没法从外面触发（本项目彻底禁用了输入注入），
// 让它自己按参数跑一遍，才能验证"对话框结束 → 结果回传 → Go 唤醒等待方"
// 这条真实链路，而不是只验证 Go 这一半。
func floatURL(kind, devOpen string) string {
	url := "/?float=" + kind
	if devOpen != "" {
		// 自测参数都是简单标识符（addnode / floatsubmit 之类），做最小转义即可
		clean := strings.NewReplacer(" ", "%20", "&", "%26", "=", "%3D").Replace(devOpen)
		url += "&devopen=" + clean
	}
	return url
}

// GetFloatPayload 返回当前浮动对话框的初始数据（JSON 字符串）。
// 为什么要单独取：URL 里只带 kind（避免把长 JSON 塞进 URL），
// 真正的数据放这里传，前端挂载后调一次即可。
func (a *API) GetFloatPayload() string {
	floatMu.Lock()
	defer floatMu.Unlock()
	if floatCurrent == nil {
		return ""
	}
	// 前端一被唤醒就会来取 payload —— 所以这一刻就是"对话框快出来了"的可靠标志。
	// 记下来才知道复用到底省了多少（首次要建 WebView2 + 加载前端，复用不用）。
	if !floatCurrent.wakeAt.IsZero() {
		ms := time.Since(floatCurrent.wakeAt).Milliseconds()
		// 注意走应用日志而不是 log.Printf：后者写 stderr，而 GUI 构建没有控制台
		// —— 踩过，日志里一行都看不到。
		if floatCurrent.reused {
			a.logf("浮动窗口：复用唤醒 → 前端就绪 %dms", ms)
		} else {
			a.logf("浮动窗口：首次创建 → 前端就绪 %dms（含新建 WebView2 + 加载前端）", ms)
		}
	}
	return floatCurrent.payload
}

// SubmitFloatDialog 由浮动窗口里的前端调用：把结果交回等待方并关掉窗口。
func (a *API) SubmitFloatDialog(resultJSON string) error {
	a.finishFloat(resultJSON, true)
	return nil
}

// finishFloat 结束当前浮动会话；closeWindow 为真时把窗口也关掉。
//
// 注意 result 用的是带缓冲的 channel + 非阻塞发送：会话可能已经被
// 别的路径结束过（例如用户先关窗、前端又补一次 Submit），
// 这种竞争不该让任何一方卡住。
func (a *API) finishFloat(result string, closeWindow bool) {
	floatMu.Lock()
	sess := floatCurrent
	floatCurrent = nil
	floatMu.Unlock()
	if sess == nil {
		return
	}

	select {
	case sess.result <- result:
	default:
	}

	if closeWindow && sess.window != nil {
		sess.closing.Store(true)
		// 【只隐藏，不销毁】窗口留在 floatPool 里给下次打开复用。
		//
		// 隐藏是瞬时的，所以既不会出现"空白弹窗一闪"（用户报过），
		// 也不会像销毁那样让下次打开重新付一遍 WebView2 的创建成本。
		sess.window.Hide()
	}
}
