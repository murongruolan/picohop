//go:build windows

package gui

// wndProc 是主窗口的消息处理函数。
//
// 必须保持纯粹：所有耗时动作（测试、启停）都丢到 goroutine 里，
// 否则界面会在测试期间"假死"。
func (w *winMain) wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmAppRefresh:
		w.refreshTicks++
		w.refreshList()
		w.refreshStatus()
		return 0

	case wmAppLog:
		w.appendLog(w.takePending(lParam))
		return 0

	case wmTimer:
		if wParam == 1 && !w.closing {
			// 每秒刷新一次流量与状态
			w.timerTicks++
			w.refreshList()
			w.refreshStatus()
		}
		return 0

	case wmCommand:
		w.handleCommand(int32(wParam&0xFFFF), int32((wParam>>16)&0xFFFF))
		return 0

	case wmKeyDown:
		if wParam == 0x26 && uintptr(hwnd) == uintptr(w.list) { // VK_UP
			w.selectDelta(-1)
			return 0
		}
		if wParam == 0x28 && uintptr(hwnd) == uintptr(w.list) { // VK_DOWN
			w.selectDelta(+1)
			return 0
		}
		return 0

	case wmCtlColorStatic:
		return w.handleCtlColorStatic(wParam, lParam)

	case wmClose:
		w.onClose()
		return 0

	case wmDestroy:
		w.closing = true
		killTimer(hwnd, 1)
		postQuitMessage(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

// ---------------------------------------------------------------------------
// 命令分发
// ---------------------------------------------------------------------------

func (w *winMain) handleCommand(cid int32, notif int32) {
	w.cmdTotal++
	w.cmdLast = cid
	w.cmdNotif = notif
	if w.cmdHits == nil {
		w.cmdHits = map[int32]int{}
	}
	// 按 ID 精确计数：EN_CHANGE 之类的通知会不断覆盖 cmdLast，
	// 只看"最后一条"无法判断某个按钮到底有没有被点到。
	w.cmdHits[cid]++

	if notif != 0 { // 0 == BN_CLICKED
		return
	}
	switch id(cid) {
	case idBtnToggle:
		go w.toggleRunning()
	case idBtnTestAll:
		w.enqueueTest("all", "")
	case idBtnFrontSave:
		w.saveFront()
	case idBtnFrontTest:
		w.enqueueTest("front", "")
	case idBtnUpAdd:
		w.addUpstream()
	case idBtnUpEdit:
		w.editSelected()
	case idBtnUpDel:
		w.deleteSelected()
	case idBtnUpMoveU:
		w.moveSelected(-1)
	case idBtnUpMoveD:
		w.moveSelected(+1)
	case idBtnUpToggle:
		w.toggleSelected()
	case idBtnUpTest:
		w.testSelected()
	case idBtnUpToggle:
		w.toggleSelected()
	case idBtnLogClear:
		w.logEdit.setText("")
	case idBtnLogCopy:
		w.copyLog()
	case idBtnOpenDir:
		w.openDataDir()
	}
}

func (w *winMain) toggleRunning() {
	if w.app.IsRunning() {
		w.app.Stop()
		w.logLine("已停止全部链路，所有本地端口已释放。")
	} else {
		if err := w.app.Start(); err != nil {
			w.warn("启动失败：" + err.Error())
		}
	}
	w.postRefresh()
}

// ---------------------------------------------------------------------------
// ListView 通知
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 静态控件着色
// ---------------------------------------------------------------------------

var pGetDlgCtrlID = user32.NewProc("GetDlgCtrlID")

func getCtrlID(hwnd uintptr) int32 {
	r, _, _ := pGetDlgCtrlID.Call(hwnd)
	return int32(r)
}

var pGetStockBrushProxy = createSolidBrush(colHeaderBg)

func (w *winMain) handleCtlColorStatic(wParam, lParam uintptr) uintptr {
	switch id(getCtrlID(lParam)) {
	case idHeaderDot:
		if w.app.IsRunning() {
			setTextColor(wParam, colRunning)
		} else {
			setTextColor(wParam, colStopped)
		}
		setBkMode(wParam, transparentBrushMode)
		return uintptr(pGetStockBrushProxy)
	case idHeaderState:
		setTextColor(wParam, colText)
		setBkMode(wParam, transparentBrushMode)
		return uintptr(pGetStockBrushProxy)
	case idFrontResult:
		setTextColor(wParam, colMuted)
		setBkMode(wParam, transparentBrushMode)
		return uintptr(pGetStockBrushProxy)
	case idStatusText:
		setTextColor(wParam, colMuted)
		setBkMode(wParam, transparentBrushMode)
		return uintptr(pGetStockBrushProxy)
	}
	return uintptr(getSysColorBrush(systemColorBtnFace))
}

// ---------------------------------------------------------------------------
// 关闭
// ---------------------------------------------------------------------------

func (w *winMain) onClose() {
	w.closing = true
	killTimer(w.hwnd, 1)

	// 停止链路会释放端口，这一步不能省，否则下次启动可能占不上。
	func() {
		defer func() { _ = recover() }()
		w.app.Stop()
	}()

	showWindow(w.hwnd, swHide)
	destroyWindow(w.hwnd)
}
