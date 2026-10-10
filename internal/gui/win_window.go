//go:build windows

package gui

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"chainproxy/internal/app"
	"chainproxy/internal/config"
	"chainproxy/internal/proxy"
	"chainproxy/internal/tester"
)

// ---------------------------------------------------------------------------
// 控件 ID 分组
// ---------------------------------------------------------------------------

const (
	gHeader   = 1
	gFront    = 2
	gUpstream = 3
	gLog      = 4
	gStatus   = 5
)

var (
	idHeaderTitle = groupID(gHeader) + 1
	idHeaderState = groupID(gHeader) + 2
	idHeaderDot   = groupID(gHeader) + 3
	idBtnToggle   = groupID(gHeader) + 10
	idBtnTestAll  = groupID(gHeader) + 11
	idBtnOpenDir  = groupID(gHeader) + 12

	idFrontGroup   = groupID(gFront) + 1
	idFrontType    = groupID(gFront) + 2
	idFrontHost    = groupID(gFront) + 3
	idFrontPort    = groupID(gFront) + 4
	idFrontUser    = groupID(gFront) + 5
	idFrontPass    = groupID(gFront) + 6
	idFrontTime    = groupID(gFront) + 7
	idBtnFrontSave = groupID(gFront) + 20
	idBtnFrontTest = groupID(gFront) + 21
	idFrontResult  = groupID(gFront) + 22

	idUpGroup     = groupID(gUpstream) + 1
	idUpList      = groupID(gUpstream) + 2
	idBtnUpAdd    = groupID(gUpstream) + 20
	idBtnUpEdit   = groupID(gUpstream) + 21
	idBtnUpDel    = groupID(gUpstream) + 22
	idBtnUpMoveU  = groupID(gUpstream) + 23
	idBtnUpMoveD  = groupID(gUpstream) + 24
	idBtnUpToggle = groupID(gUpstream) + 25
	idBtnUpTest   = groupID(gUpstream) + 26

	idLogGroup    = groupID(gLog) + 1
	idLogEdit     = groupID(gLog) + 2
	idBtnLogClear = groupID(gLog) + 20
	idBtnLogCopy  = groupID(gLog) + 21

	idStatusText = groupID(gStatus) + 1
)

const (
	winWidth  = 1060
	winHeight = 762
)

var className = mustUTF16("ChainProxyMainWindow")

// winMain 是主窗口的全部状态。
type winMain struct {
	app   *app.App
	inst  uintptr
	hwnd  uintptr
	dpi   int32
	fonts *uiFont

	l layout

	// 控件句柄
	headerState ctrl
	headerDot   ctrl
	btnToggle   ctrl
	frontResult ctrl
	// 列表
	list         ctrl
	listHeader   ctrl
	columnWidths []int32
	logEdit      ctrl
	statusText   ctrl

	frontType ctrl
	frontHost ctrl
	frontPort ctrl
	frontUser ctrl
	frontPass ctrl
	frontTime ctrl

	// 行状态，供 NM_CUSTOMDRAW 上色（与列表行一一对应）
	mu        sync.Mutex
	rowStates []string
	selected  int

	pendingMu sync.Mutex
	pending   map[uintptr]string
	seq       uintptr

	closing      bool
	timerTicks   int
	cmdTotal     int
	cmdLast      int32
	cmdNotif     int32
	cmdHits      map[int32]int
	refreshTicks int
	closedCh     chan struct{}

	editMu sync.Mutex
	editID string // 正在编辑的住宅代理 ID，空表示新增
}

func init() { runNative = runWindow }

// runWindow 是 Windows 下的真实入口。
//
// 【关键】必须 LockOSThread。窗口是在"调用 CreateWindowEx 的那个 OS 线程"上创建的，
// 而 Windows 只把这个窗口的消息投递到该线程的消息队列。Go 的 goroutine 默认会在
// 多个 OS 线程之间迁移，一旦迁移，GetMessage 就再也收不到这个窗口的消息——
// 表现是"首次启动正常、之后界面偶尔整体卡死、Responding=False"。
// 这是纯 Win32 + Go 最经典也最容易踩的坑，这里锁死以避免复发。
func runWindow(a *app.App) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	setProcessDPIAware()
	initCommonControls()

	w := &winMain{
		app:      a,
		inst:     getModuleHandle(),
		pending:  map[uintptr]string{},
		closedCh: make(chan struct{}),
	}

	wc := WNDCLASSEX{
		Style:         0x0008 | 0x0002 | 0x0001, // CS_DBLCLKS | CS_HREDRAW | CS_VREDRAW
		LpfnWndProc:   syscall.NewCallback(w.wndProc),
		HInstance:     w.inst,
		HCursor:       loadCursor(idcArrowW),
		HbrBackground: getSysColorBrush(systemColorBtnFace),
		LpszClassName: className,
	}
	if r := registerClassExW(&wc); r == 0 {
		return fmt.Errorf("注册窗口类失败（可能已有实例在运行）")
	}

	style := uint32(wsOverlappedWindow | wsClipChildren)
	// 固定尺寸的小工具窗口：不做缩放，省掉一整套响应式布局，也不怕误操作把控件拉乱。
	style &^= wsThickFrame
	style &^= wsMaximizeBox

	w.hwnd = createWindowExW(0, "ChainProxyMainWindow", "ChainProxy 链式代理",
		style, 0, 0, scaleDPI(winWidth, 96), scaleDPI(winHeight, 96), 0, 0, w.inst)
	if w.hwnd == 0 {
		return fmt.Errorf("创建窗口失败（errno=%d）", errno())
	}
	w.dpi = 96
	if r, _, _ := pGetDpiForWindow.Call(w.hwnd); r != 0 {
		w.dpi = int32(r)
	}
	w.fonts = initFonts(w.dpi)
	w.layoutControls()

	// 事件订阅：把后台 goroutine 的日志/状态变更投递回 UI 线程
	a.Subscribe(func(e app.Event) {
		switch e.Kind {
		case "log":
			w.postLog(e.Text)
		default:
			w.postRefresh()
		}
	})

	// 载入配置到界面
	w.loadFrontFields()

	showWindow(w.hwnd, swShow)
	updateWindow(w.hwnd)
	setForeground(w.hwnd)

	setTimer(w.hwnd, 1, 1000)

	go a.AutoStart()
	go w.pumpTestQueue()
	dumpStacksOnDemand()

	// 对话框自检：打开一次编辑对话框（maybeDumpDialog 会自动导出并关闭）
	if os.Getenv("CHAINPROXY_UI_DUMP_DIALOG") != "" {
		w.editUpstreamDialog(config.Upstream{
			Name:       "示例住宅代理",
			Type:       config.UpstreamTypeSocks5,
			Enabled:    true,
			ListenHost: "127.0.0.1",
			ListenPort: 10810,
			TimeoutMs:  15000,
		}, false)
	}

	// 自检模式：导出界面状态后直接退出（不进入消息循环）。
	if maybeDumpUI(w) {
		if w.fonts != nil {
			w.fonts.destroy()
		}
		return nil
	}

	// 消息循环
	var msg MSG
	for {
		r := getMessage(&msg)
		if r <= 0 {
			break
		}
		translateMessage(&msg)
		dispatchMessage(&msg)
	}

	close(w.closedCh)
	if w.fonts != nil {
		w.fonts.destroy()
	}
	return nil
}

// ---------------------------------------------------------------------------
// 布局
// ---------------------------------------------------------------------------

func (w *winMain) layoutControls() {
	w.l = layout{parent: w.hwnd, inst: w.inst, dpi: w.dpi, fRegular: w.fonts.regular, fBold: w.fonts.bold, fMono: w.fonts.mono}

	s := func(v int32) int32 { return scaleDPI(v, w.dpi) }

	// 关键：**按真实客户区尺寸排版**，而不是按"我以为的窗口尺寸"。
	// 上一版按 1060x762 排版，而客户区只有 1044x723，
	// 结果右边整整一截、下边整整一截掉在窗口外。
	var cr RECT
	pGetClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&cr)))
	cw := cr.Right - cr.Left
	const pad = 16
	inner := cw - pad*2

	// ---- 顶部标题栏 ----
	title := w.l.make(control{Class: "STATIC", Text: "ChainProxy 链式代理", Style: wsChild | wsVisible | ssLeft, ID: idHeaderTitle}, s(pad), s(10), s(320), s(30))
	title.font(w.fonts.title)
	w.l.make(control{
		Class: "STATIC",
		Text:  "本地主流客户端作前置 → 住宅 IP 作出口",
		Style: wsChild | wsVisible | ssLeft, ID: groupID(gHeader) + 4,
	}, s(pad), s(40), s(470), s(20)).font(w.fonts.tiny)

	// 右上角按钮：宽度按文字实测，先从右往左摆好
	bTest, wTest := w.l.buttonAuto("一键测试", idBtnTestAll, 0, s(18), s(76), s(32))
	bToggle, wToggle := w.l.buttonAuto("启动", idBtnToggle, 0, s(18), s(76), s(32))
	xToggle := cw - pad - wToggle
	xTest := xToggle - s(10) - wTest
	moveCtrl(bToggle, xToggle, s(18), wToggle, s(32))
	moveCtrl(bTest, xTest, s(18), wTest, s(32))
	w.btnToggle = bToggle

	// 状态点 + 状态文字必须排在按钮**左边**，否则会被按钮盖住看不见
	stateW := s(200)
	xState := xTest - s(16) - stateW
	w.headerDot = w.l.make(control{Class: "STATIC", Text: "○", Style: wsChild | wsVisible | ssCenter, ID: idHeaderDot},
		xState-s(38), s(18), s(32), s(24))
	w.headerState = w.l.make(control{Class: "STATIC", Text: "已停止", Style: wsChild | wsVisible | ssLeft, ID: idHeaderState},
		xState, s(20), stateW, s(22))

	// ---- ① 前置代理 ----
	fy := s(70)
	fh := s(150)
	w.l.group(w.l.fitGroupTitle("① 前置代理 —— 通常就是本地主流客户端的 127.0.0.1:10808（mixed 协议）", inner-s(28)),
		idFrontGroup, s(pad), fy, inner, fh)

	rowY := fy + s(28)
	editH := s(24)

	// 第一行：类型 / 地址 / 端口 / 用户名 / 密码
	// （"超时"挪到第二行——第一行放不下，实测会整块溢出客户区）
	cx := s(pad + 16)
	cx = w.placeLabel("类型", groupID(gFront)+40, cx, rowY+s(4))
	w.frontType = w.l.combo(idFrontType, []string{"socks5", "http", "直连"}, cx, rowY, s(78), s(130))
	cx += s(78) + s(14)

	cx = w.placeLabel("地址", groupID(gFront)+41, cx, rowY+s(4))
	w.frontHost = w.l.edit(idFrontHost, cx, rowY, s(126), editH, 0)
	cx += s(126) + s(14)

	cx = w.placeLabel("端口", groupID(gFront)+42, cx, rowY+s(4))
	w.frontPort = w.l.edit(idFrontPort, cx, rowY, s(66), editH, esNumber)
	cx += s(66) + s(14)

	cx = w.placeLabel("用户名", groupID(gFront)+43, cx, rowY+s(4))
	w.frontUser = w.l.edit(idFrontUser, cx, rowY, s(104), editH, 0)
	cx += s(104) + s(14)

	cx = w.placeLabel("密码", groupID(gFront)+44, cx, rowY+s(4))
	w.frontPass = w.l.edit(idFrontPass, cx, rowY, s(104), editH, esPassword)
	cx += s(104) + s(14)

	// 第二行：超时 + 按钮 + 测试结果
	row2 := rowY + s(40)
	tx := w.placeLabel("超时 ms", groupID(gFront)+45, s(pad+16), row2+s(4))
	w.frontTime = w.l.edit(idFrontTime, tx, row2, s(76), editH, esNumber)
	tx += s(76) + s(20)

	bSave, wSave := w.l.buttonAuto("保存前置设置", idBtnFrontSave, tx, row2, s(104), s(28))
	tx += wSave + s(10)
	bTestF, wTestF := w.l.buttonAuto("测试前置", idBtnFrontTest, tx, row2, s(80), s(28))
	tx += wTestF + s(14)
	_, _ = bSave, bTestF
	w.frontResult = w.l.make(control{
		Class: "STATIC", Text: "", Style: wsChild | wsVisible | ssLeft | ssEndEllipsis, ID: idFrontResult,
	}, tx, row2+s(6), cw-pad-s(14)-tx, s(20))

	// ---- ② 住宅代理 ----
	uy := fy + fh + s(12)
	uh := s(246)
	w.l.group(w.l.fitGroupTitle("② 住宅代理（出口）—— 每个代理绑定一个独立本地端口", inner-s(28)),
		idUpGroup, s(pad), uy, inner, uh)

	by := uy + s(12)
	bh := s(26)
	right := cw - pad - s(12)
	placeBtn := func(text string, cid id, minW int32) {
		c, bw := w.l.buttonAuto(text, cid, 0, by, minW, bh)
		right -= bw
		moveCtrl(c, right, by, bw, bh)
		right -= s(8)
	}
	placeBtn("+ 添加住宅代理", idBtnUpAdd, s(120))
	placeBtn("编辑", idBtnUpEdit, s(52))
	placeBtn("删除", idBtnUpDel, s(52))
	placeBtn("▼", idBtnUpMoveD, s(26))
	placeBtn("▲", idBtnUpMoveU, s(26))
	placeBtn("测试选中", idBtnUpTest, s(80))
	placeBtn("启用 / 禁用", idBtnUpToggle, s(96))

	ly := uy + s(44)
	w.listHeader = w.l.make(control{
		Class: "EDIT",
		Style: wsChild | wsVisible | wsBorder | wsExClientEdge | esLeft | esReadOnly,
		Ex:    wsExClientEdge,
		ID:    groupID(gUpstream) + 8,
	}, s(pad+10), ly, inner-s(20), s(20))
	w.listHeader.font(w.fonts.mono)
	w.listHeader.setText(renderTableHeader())

	w.list = w.l.multiline(idUpList, s(pad+10), ly+s(24), inner-s(20), uh-s(52), esReadOnly)
	w.list.font(w.fonts.mono)

	// ---- ③ 日志 ----
	gy := uy + uh + s(12)
	gh := s(160)
	w.l.group(w.l.fitGroupTitle("③ 运行状态与测试输出", inner-s(28)), idLogGroup, s(pad), gy, inner, gh)
	w.logEdit = w.l.multiline(idLogEdit, s(pad+10), gy+s(24), inner-s(20), gh-s(74), esReadOnly)
	w.logEdit.font(w.fonts.mono)

	lx := s(pad + 10)
	bClr, wClr := w.l.buttonAuto("清空", idBtnLogClear, lx, gy+gh-s(46), s(56), s(28))
	lx += wClr + s(8)
	bCp, wCp := w.l.buttonAuto("复制全部", idBtnLogCopy, lx, gy+gh-s(46), s(80), s(28))
	lx += wCp + s(8)
	bDir, _ := w.l.buttonAuto("打开配置目录", idBtnOpenDir, lx, gy+gh-s(46), s(104), s(28))
	_, _, _ = bClr, bCp, bDir

	// ---- 底部状态栏 ----
	w.statusText = w.l.make(control{
		Class: "STATIC", Text: "", Style: wsChild | wsVisible | ssLeft | ssEndEllipsis, ID: idStatusText,
	}, s(pad), gy+gh+s(2), inner, s(20))

	w.refreshList()
	w.refreshStatus()
}

// placeLabel 造一个**左对齐**标签，返回下一个控件应放置的 x（已含 8px 间距）。
//
// 最初用的是右对齐（把标签贴到 x 的左侧），结果每行第一个标签都被推到负坐标，
// 也就是整列标签都跑到窗口左边界外面——这正是"界面布局错位"的直接原因。
// 布局体检现在会检测负坐标，这类问题不会再溜过去。
func (w *winMain) placeLabel(text string, cid id, x, y int32) int32 {
	lw := w.l.measure(w.l.fRegular, text) + 10
	w.l.make(control{Class: "STATIC", Text: text, Style: wsChild | wsVisible | ssLeft, ID: cid}, x, y, lw, w.l.s(18))
	return x + lw + w.l.s(8)
}

// moveCtrl 移动并按需缩放已创建的控件。
func moveCtrl(c ctrl, x, y, w, h int32) {
	if c == 0 {
		return
	}
	if w <= 0 || h <= 0 {
		pMoveWindow.Call(uintptr(c), uintptr(x), uintptr(y), 0, 0)
		return
	}
	pMoveWindow.Call(uintptr(c), uintptr(x), uintptr(y), uintptr(w), uintptr(h))
}

// ---------------------------------------------------------------------------
// 配置读写
// ---------------------------------------------------------------------------

func (w *winMain) loadFrontFields() {
	f := w.app.GetFront()
	w.frontType.comboSelect(int32(indexOf([]string{"socks5", "http", "直连"}, f.Type)))
	if f.Type == "" {
		w.frontType.comboSelect(0)
	}
	w.frontHost.setText(f.Host)
	w.frontPort.setIntValue(f.Port)
	w.frontUser.setText(f.Username)
	w.frontPass.setText(f.Password)
	w.frontTime.setIntValue(f.TimeoutMs)
	w.frontTypeOnChange()
}

func (w *winMain) frontTypeOnChange() {
	t := w.frontType.comboText()
	direct := t == "直连"
	w.frontHost.setEnabled(!direct)
	w.frontPort.setEnabled(!direct)
	w.frontUser.setEnabled(!direct)
	w.frontPass.setEnabled(!direct)
	w.frontTime.setEnabled(!direct)
}

func (w *winMain) collectFront() config.Front {
	t := w.frontType.comboText()
	if t == "" {
		t = "socks5"
	}
	return config.Front{
		Type:      t,
		Host:      strings.TrimSpace(w.frontHost.text()),
		Port:      w.frontPort.intValue(10808),
		Username:  strings.TrimSpace(w.frontUser.text()),
		Password:  w.frontPass.text(),
		TimeoutMs: w.frontTime.intValue(10000),
	}
}

func (w *winMain) saveFront() {
	f := w.collectFront()
	if f.Type != "直连" {
		if f.Host == "" {
			w.warn("请填写前置代理地址")
			w.frontHost.focus()
			return
		}
		if f.Port < 1 || f.Port > 65535 {
			w.warn("前置代理端口不合法（1-65535）")
			w.frontPort.focus()
			return
		}
	}
	if err := w.app.SetFrontConfig(f); err != nil {
		w.warn("保存失败：" + err.Error())
		return
	}
	w.logLine("已保存前置代理设置：" + proxy.NewFront(f).Describe())
	w.refreshStatus()
}

// ---------------------------------------------------------------------------
// 列表刷新
// ---------------------------------------------------------------------------

func (w *winMain) refreshList() {
	rows := w.app.Status()
	states := make([]string, 0, len(rows))
	for _, r := range rows {
		states = append(states, r.State)
	}

	w.mu.Lock()
	w.rowStates = states
	if w.selected >= len(rows) {
		w.selected = len(rows) - 1
	}
	if w.selected < -1 {
		w.selected = -1
	}
	sel := w.selected
	w.mu.Unlock()

	w.list.setText(renderUpstreamTable(rows, sel))
}

func stateText(s string) string {
	switch s {
	case app.StateRunning:
		return "运行中"
	case app.StateError:
		return "错误"
	case app.StateOff:
		return "已禁用"
	default:
		return "已停止"
	}
}

func (w *winMain) refreshStatus() {
	running := w.app.IsRunning()
	rows := w.app.Status()
	active := 0
	for _, r := range rows {
		if r.State == app.StateRunning {
			active++
		}
	}

	// 这两处文字会在运行期变化，必须保证"最长的一版"也塞得进控件，
	// 否则会像之前那样出现按钮文字被截断。
	if running {
		w.setHeaderText(fmt.Sprintf("运行中 · %d/%d 链路", active, len(rows)))
		w.headerDot.setText("●")
		w.setToggleText("停止")
	} else {
		w.setHeaderText("已停止 · 未占用端口")
		w.headerDot.setText("○")
		w.setToggleText("启动")
	}

	desc, _ := w.app.FrontStatus()
	w.statusText.setText(w.l.ellipsize(
		fmt.Sprintf("前置：%s    配置：%s    程序不注册任何开机自启动项", desc, config.ConfigPath()),
		scaleDPI(1004, w.dpi)))
}

// setHeaderText 写顶部状态文字，并保证它在 190px 内显示得下（放不下就自动截断）。
func (w *winMain) setHeaderText(txt string) {
	w.headerState.setText(w.l.ellipsize(txt, scaleDPI(178, w.dpi)))
}

// setToggleText 写主开关按钮文字。
// 「启动」与「停止」都是两个汉字，实测宽度一致，
// 所以按钮宽度在布局时一次定好即可，这里直接改文字。
func (w *winMain) setToggleText(s string) { w.btnToggle.setText(s) }

// ---------------------------------------------------------------------------
// 日志
// ---------------------------------------------------------------------------

func (w *winMain) logLine(s string) {
	w.postLog(s)
}

func (w *winMain) postLog(text string) {
	w.pendingMu.Lock()
	w.seq++
	id := w.seq
	w.pending[id] = text
	w.pendingMu.Unlock()
	postMessage(w.hwnd, wmAppLog, 0, id)
}

func (w *winMain) takePending(id uintptr) string {
	w.pendingMu.Lock()
	s := w.pending[id]
	delete(w.pending, id)
	w.pendingMu.Unlock()
	return s
}

func (w *winMain) postRefresh() {
	postMessage(w.hwnd, wmAppRefresh, 0, 0)
}

func (w *winMain) appendLog(text string) {
	ts := time.Now().Format("15:04:05")
	lines := strings.Split(text, "\n")
	for _, ln := range lines {
		w.logEdit.addText(ts + "  " + ln + "\r\n")
	}
	// 控制日志行数，避免长时间运行把内存吃满。
	if w.logEdit.lineCount() > 500 {
		w.trimLog()
	}
	w.logEdit.scrollToEnd()
}

func (w *winMain) trimLog() {
	const keep = 300
	all := w.logEdit.text()
	lines := strings.Split(all, "\n")
	if len(lines) <= keep {
		return
	}
	w.logEdit.setText(strings.Join(lines[len(lines)-keep:], "\n"))
}

func (w *winMain) copyLog() {
	w.clipboard(w.logEdit.text())
}

// ---------------------------------------------------------------------------
// 测试
// ---------------------------------------------------------------------------

type testRequest struct {
	kind    string // "front" | "all" | "upstream"
	id      string
	fromRow bool
}

var (
	testQMu sync.Mutex
	testQ   = make(chan testRequest, 32)
)

func (w *winMain) pumpTestQueue() {
	for {
		select {
		case <-w.closedCh:
			return
		case req := <-testQ:
			w.doTest(req)
		}
	}
}

func (w *winMain) enqueueTest(kind, id string) {
	testQMu.Lock()
	defer testQMu.Unlock()
	select {
	case testQ <- testRequest{kind: kind, id: id}:
	default:
		w.logLine("测试队列已满，请稍候")
	}
}

func (w *winMain) doTest(req testRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	switch req.kind {
	case "front":
		w.logLine("── 测试前置代理 ──────────────────────────────")
		r := w.app.TestFront(ctx)
		w.reportResult("前置代理", r)
	case "upstream":
		u, ok := w.app.GetUpstream(req.id)
		if !ok {
			w.logLine("该住宅代理已被删除")
			return
		}
		w.logLine(fmt.Sprintf("── 测试「%s」（本地端口 %d）─────────────", u.Name, u.ListenPort))
		r := w.app.TestUpstream(ctx, req.id)
		w.reportResult(u.Name, r)
	case "all":
		w.logLine("════ 一键测试开始 ════")
		fr := w.app.TestFront(ctx)
		w.reportResult("前置代理", fr)

		rows := w.app.Status()
		if len(rows) == 0 {
			w.logLine("尚未配置任何住宅代理。点击「+ 添加住宅代理」开始。")
		}
		for _, row := range rows {
			u, ok := w.app.GetUpstream(row.ID)
			if !ok || !u.Enabled {
				w.logLine(fmt.Sprintf("「%s」已禁用，跳过。", row.Name))
				continue
			}
			w.logLine("── 链路：" + u.Name + " → 127.0.0.1:" + fmt.Sprint(u.ListenPort) + " ──")
			cr := w.app.TestUpstream(ctx, row.ID)
			w.reportResult(u.Name, cr)
			w.logLine("    " + tester.Compare(fr, cr))
		}
		w.logLine("════ 一键测试结束 ════")
		w.postRefresh()
	}
}

func (w *winMain) reportResult(name string, r tester.Result) {
	if r.OK {
		w.logLine(fmt.Sprintf("✅ %-14s 出口 IP %s   延迟 %d ms", name, r.IP, r.Latency.Milliseconds()))
		return
	}
	w.logLine(fmt.Sprintf("❌ %-14s %v", name, r.Err))
}

// ---------------------------------------------------------------------------
// 住宅代理增删改
// ---------------------------------------------------------------------------

func (w *winMain) addUpstream() {
	u := config.Upstream{
		Name:       w.defaultName(),
		Type:       config.UpstreamTypeSocks5,
		Enabled:    true,
		ListenHost: "127.0.0.1",
		ListenPort: w.nextFreePort(),
		TimeoutMs:  15000,
	}
	if w.editUpstreamDialog(u, false) {
		if err := w.app.AddUpstream(u); err != nil {
			w.warn("添加失败：" + err.Error())
			return
		}
		w.logLine("已添加住宅代理「" + u.Name + "」，本地端口 " + fmt.Sprint(u.ListenPort))
		w.refreshList()
		w.refreshStatus()
	}
}

func (w *winMain) editSelected() {
	id := w.selectedID()
	if id == "" {
		w.warn("请先在列表中选择一条住宅代理")
		return
	}
	u, ok := w.app.GetUpstream(id)
	if !ok {
		w.warn("该住宅代理已不存在")
		return
	}
	if w.editUpstreamDialog(u, true) {
		if err := w.app.UpdateUpstream(u); err != nil {
			w.warn("保存失败：" + err.Error())
			return
		}
		w.logLine("已保存住宅代理「" + u.Name + "」")
		w.refreshList()
		w.refreshStatus()
	}
}

func (w *winMain) deleteSelected() {
	id := w.selectedID()
	if id == "" {
		w.warn("请先在列表中选择一条住宅代理")
		return
	}
	u, _ := w.app.GetUpstream(id)
	if u.ID == "" {
		w.warn("该住宅代理已不存在")
		return
	}
	res := messageBox(w.hwnd, fmt.Sprintf("确定删除「%s」？\n它的本地端口 %d 会被立即释放。", u.Name, u.ListenPort),
		"确认删除", 0x00000004|0x00000040) // MB_YESNO | MB_ICONWARNING
	if res != 6 {
		return
	}
	if err := w.app.DeleteUpstream(id); err != nil {
		w.warn("删除失败：" + err.Error())
		return
	}
	w.refreshList()
	w.refreshStatus()
}

func (w *winMain) moveSelected(delta int) {
	id := w.selectedID()
	if id == "" {
		return
	}
	if err := w.app.MoveUpstream(id, delta); err != nil {
		w.warn(err.Error())
		return
	}
	w.refreshList()
	w.refreshStatus()
}

func (w *winMain) toggleSelected() {
	id := w.selectedID()
	if id == "" {
		return
	}
	u, ok := w.app.GetUpstream(id)
	if !ok {
		return
	}
	want := !u.Enabled
	if err := w.app.SetUpstreamEnabled(id, want); err != nil {
		w.warn(err.Error())
	}
	w.refreshList()
	w.refreshStatus()
}

func (w *winMain) selectedID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	rows := w.app.Status()
	if w.selected < 0 || w.selected >= len(rows) {
		return ""
	}
	return rows[w.selected].ID
}

// selectDelta 用 ↑/↓ 键在列表里移动选中行。
func (w *winMain) selectDelta(d int) {
	w.mu.Lock()
	n := len(w.app.Status())
	if n == 0 {
		w.selected = -1
	} else {
		w.selected += d
		if w.selected < 0 {
			w.selected = 0
		}
		if w.selected >= n {
			w.selected = n - 1
		}
	}
	w.mu.Unlock()
	w.refreshList()
}

// selectCurrent 把选中行当作"测试对象"跑一遍。
func (w *winMain) testSelected() {
	id := w.selectedID()
	if id == "" {
		w.warn("请先用 ↑/↓ 选中一条住宅代理")
		return
	}
	w.enqueueTest("upstream", id)
}
func (w *winMain) defaultName() string {
	return fmt.Sprintf("住宅代理%d", len(w.app.Config().Upstreams)+1)
}

func (w *winMain) nextFreePort() int {
	cfg := w.app.Config()
	used := map[int]bool{cfg.Front.Port: true}
	for _, u := range cfg.Upstreams {
		used[u.ListenPort] = true
	}
	for p := 10810; p <= 10899; p++ {
		if used[p] {
			continue
		}
		if err := checkPortFree("127.0.0.1", p); err == nil {
			return p
		}
	}
	return 10810
}

// ---------------------------------------------------------------------------
// 杂项
// ---------------------------------------------------------------------------

func (w *winMain) warn(msg string) {
	messageBox(w.hwnd, msg, "ChainProxy", 0x00000010|0x00000000) // MB_ICONERROR
}

func (w *winMain) clipboard(text string) {
	_ = putClipboard(text)
}

func (w *winMain) openDataDir() {
	dir := config.DataDir()
	_ = os.MkdirAll(dir, 0o755)
	openExplorer(dir)
}

func itoa(v int) string     { return itoa64(int64(v)) }
func itoa64(v int64) string { return fmt.Sprintf("%d", v) }

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
