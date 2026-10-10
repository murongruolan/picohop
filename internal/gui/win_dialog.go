//go:build windows

package gui

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"chainproxy/internal/config"
	"chainproxy/internal/wgproxy"
)

// 住宅代理编辑对话框的控件 ID
const (
	dgName    = 0x7001
	dgType    = 0x7002
	dgHost    = 0x7003
	dgPort    = 0x7004
	dgUser    = 0x7005
	dgPass    = 0x7006
	dgQuick   = 0x7007
	dgListen  = 0x7008
	dgLHost   = 0x7009
	dgEnable  = 0x700A
	dgOK      = 0x700B
	dgCancel  = 0x700C
	dgStatus  = 0x700D
	dgLUser   = 0x700E
	dgLPass   = 0x700F
	dgTimeout = 0x7010

	dgServerGroup = 0x7020

	dgWGGroup   = 0x7040
	dgWGPriv    = 0x7041
	dgWGAddr    = 0x7042
	dgWGPub     = 0x7043
	dgWGEHost   = 0x7044
	dgWGEPort   = 0x7045
	dgWGAllow   = 0x7046
	dgWGKeep    = 0x7047
	dgWGMTU     = 0x7048
	dgWGPSK     = 0x7049
	dgWGGen     = 0x7050
	dgWGPubShow = 0x7051
	dgWGCopyPub = 0x7052
	// 密钥都是 44 个字符，手动框选极易选不全，所以每个密钥字段都配一个复制按钮
	dgWGCopyPriv = 0x7053
	dgWGCopyPeer = 0x7054
	dgWGCopyPSK  = 0x7055
)

// dgParse 属于"住宅代理"控件组，不能写成 const（groupID 是函数）。
var dgParse = id(groupID(gUpstream) + 40)

var dlgClassName = mustUTF16("ChainProxyEditDialog")

var dlgTypes = []string{"socks5", "http", "wireguard"}

// dlgState 是"添加/编辑住宅代理"对话框的全部状态。
type dlgState struct {
	parent *winMain
	inst   uintptr
	hwnd   uintptr
	up     config.Upstream
	isEdit bool

	ok     bool
	closed bool
	l      layout
	s      func(int32) int32

	cName, cType, cQuick, cLHost, cListen ctrl
	cLUser, cLPass, cEnable, cStatus      ctrl
	cHost, cPort, cUser, cPass, cTimeout  ctrl
	cServerGroup                          ctrl
	cServerCtrls, cServerLbls             []ctrl
	cWGGroup                              ctrl
	cWGPriv, cWGAddr, cWGPub              ctrl
	cWGEHost, cWGEPort, cWGAllow          ctrl
	cWGKeep, cWGMTU                       ctrl
	cWGPSK                                ctrl
	cWGPubShow                            ctrl
	wgCtrls, wgLbls                       []ctrl
}

// editUpstreamDialog 打开"添加/编辑住宅代理"对话框。
// 返回 true 表示用户点了确定且校验通过。
func (w *winMain) editUpstreamDialog(u config.Upstream, isEdit bool) bool {
	st := &dlgState{parent: w, inst: w.inst, up: u, isEdit: isEdit}
	st.run()
	return st.ok
}

func (d *dlgState) run() {
	// 同 runWindow：模态对话框也必须在创建它的 OS 线程上跑消息循环。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	title := "添加住宅代理"
	if d.isEdit {
		title = "编辑住宅代理"
	}

	ensureDlgClass(d.inst)

	// 把"当前打开的对话框"登记进去，供共用的窗口过程路由。
	// 必须在 createWindowExW 之前设置，否则窗口一创建就可能先收到消息。
	dlgCurrent.Store(d)
	defer dlgCurrent.Store(nil)

	d.hwnd = createWindowExW(wsExDlgModalFrame, "ChainProxyEditDialog", title,
		wsPopup|wsCaption|wsBorder|wsSysMenu,
		0, 0, scaleDPI(780, d.dpi()), scaleDPI(790, d.dpi()), d.parent.hwnd, 0, d.inst)
	if d.hwnd == 0 {
		d.parent.warn(fmt.Sprintf("创建对话框失败（errno=%d）", errno()))
		return
	}

	d.build()
	centerOn(d.hwnd, d.parent.hwnd)
	showWindow(d.hwnd, swShow)
	updateWindow(d.hwnd)
	setForeground(d.hwnd)
	d.cName.focus()

	maybeDumpDialog(d)

	// 模态：屏蔽主窗口，并在自己的消息循环里转，直到 closed
	enableWindow(d.parent.hwnd, false)
	for !d.closed {
		var msg MSG
		if getMessage(&msg) <= 0 {
			break
		}
		translateMessage(&msg)
		dispatchMessage(&msg)
	}
	enableWindow(d.parent.hwnd, true)
	destroyWindow(d.hwnd)
	setForeground(d.parent.hwnd)
}

func (d *dlgState) dpi() int32 {
	if d.parent != nil && d.parent.dpi > 0 {
		return d.parent.dpi
	}
	return 96
}

// ---------------------------------------------------------------------------
// 布局
// ---------------------------------------------------------------------------

// build 用**实测文字宽度**排版，不再靠估算。
// 每个"标签 + 输入框"对都由 labelRightAuto + 精确宽度组成，
// 配合 scripts\test_ui.ps1 的对话框布局体检可以杜绝文字被截断。
func (d *dlgState) build() {
	f := d.parent.fonts
	d.l = layout{parent: d.hwnd, inst: d.inst, dpi: d.dpi(),
		fRegular: f.regular, fBold: f.bold, fMono: f.mono}
	s := d.l.s

	// 以实际客户区为准
	var cr RECT
	pGetClientRect.Call(d.hwnd, uintptr(unsafe.Pointer(&cr)))
	cw := cr.Right - cr.Left
	const pad = 18
	inner := cw - pad*2

	// 便捷：造一个"左对齐标签 + 输入框"整体，返回下一个可用 x。
	//
	// 注意这里必须**左对齐**：早先用 labelRightAuto 把标签右对齐到 x 上，
	// 结果第一个标签被推到了负坐标（x - 标签宽），整列标签都跑到窗口左边界外面。
	labeled := func(text string, cid id, x, y, fieldW, h int32) int32 {
		lw := d.l.measure(d.l.fRegular, text) + 10
		d.l.make(control{Class: "STATIC", Text: text, Style: wsChild | wsVisible | ssLeft, ID: cid + 1000},
			x, y+s(4), lw, s(18))
		d.l.edit(id(cid), x+lw+s(8), y-s(4), fieldW, h, 0)
		return x + lw + s(8) + fieldW
	}

	// ---- 第一行：名称 / 类型 / 启用 ----
	nmX := s(pad)
	nlw := d.l.measure(d.l.fRegular, "名称") + 10
	d.l.make(control{Class: "STATIC", Text: "名称", Style: wsChild | wsVisible | ssLeft, ID: groupID(gUpstream) + 1},
		nmX, s(17), nlw, s(18))
	d.cName = d.l.edit(dgName, nmX+nlw+s(8), s(13), s(170), s(24), 0)

	x2 := s(280)
	tlw := d.l.measure(d.l.fRegular, "类型") + 10
	d.l.make(control{Class: "STATIC", Text: "类型", Style: wsChild | wsVisible | ssLeft, ID: groupID(gUpstream) + 2},
		x2, s(17), tlw, s(18))
	d.cType = d.l.combo(dgType, dlgTypes, x2+tlw+s(8), s(13), s(96), s(160))
	d.cEnable = d.l.checkbox("启用", dgEnable, cw-s(102), s(15), s(84), s(22))

	// ---- 快速导入 ----
	by := s(44)
	d.l.group(d.l.fitGroupTitle("快速导入 —— 可直接粘贴 user:pass@host:port", inner-s(20)), groupID(gUpstream)+3, s(pad), by, inner, s(62))
	// 按钮宽度先算出来，输入框再按"剩下的宽度"排，
	// 否则两者会互相压住（布局体检的重叠检测会报出来）。
	_, wParse := d.l.buttonAuto("解析并填入", dgParse, 0, by+s(18), s(96), s(28))
	xParse := cw - s(pad+12) - wParse
	moveCtrl(getCtrlByID(d.hwnd, dgParse), xParse, by+s(18), wParse, s(28))
	d.cQuick = d.l.edit(dgQuick, s(pad+12), by+s(20), xParse-s(pad+12)-s(12), s(24), 0)

	// ---- 本地端口 ----
	cy := s(114)
	d.l.group(d.l.fitGroupTitle("本地端口 —— 浏览器 / 软件里填这个", inner-s(20)), groupID(gUpstream)+5, s(pad), cy, inner, s(100))

	// hint 造一条"按剩余空间自动截断"的说明文字，避免文字溢出控件
	hint := func(text string, cid id, x, right, y int32) ctrl {
		w := right - x
		if w < s(40) {
			return 0
		}
		c := d.l.make(control{Class: "STATIC", Text: d.l.ellipsize(text, w-s(4)),
			Style: wsChild | wsVisible | ssLeft, ID: cid}, x, y, w, s(18))
		c.font(f.tiny)
		return c
	}

	rowA := cy + s(28)
	nx := labeled("地址", groupID(gUpstream)+6, s(pad+14), rowA, s(150), s(24))
	nx = labeled("端口", groupID(gUpstream)+7, nx+s(18), rowA, s(90), s(24))
	hint("端口全局唯一，重复会被拦截", groupID(gUpstream)+30, nx+s(18), cw-s(pad+14), rowA+s(4))

	rowB := rowA + s(34)
	nx = labeled("端口账号", groupID(gUpstream)+8, s(pad+14), rowB, s(110), s(24))
	nx = labeled("密码", groupID(gUpstream)+9, nx+s(18), rowB, s(110), s(24))
	hint("默认只监听 127.0.0.1，可留空", groupID(gUpstream)+31, nx+s(18), cw-s(pad+14), rowB+s(4))

	// ---- 住宅代理服务器 ----
	dy := s(222)
	d.cServerGroup = d.l.group(d.l.fitGroupTitle("住宅代理服务器 —— 本程序会经前置代理连接它", inner-s(20)), dgServerGroup, s(pad), dy, inner, s(100))
	rowC := dy + s(28)
	nx = labeled("地址", groupID(gUpstream)+10, s(pad+14), rowC, s(230), s(24))
	nx = labeled("端口", groupID(gUpstream)+11, nx+s(18), rowC, s(90), s(24))
	d.cServerLbls = append(d.cServerLbls,
		hint("域名交给住宅代理解析", groupID(gUpstream)+32, nx+s(18), cw-s(pad+14), rowC+s(4)))

	rowD := rowC + s(34)
	nx = labeled("用户名", groupID(gUpstream)+12, s(pad+14), rowD, s(84), s(24))
	nx = labeled("密码", groupID(gUpstream)+13, nx+s(14), rowD, s(88), s(24))
	nx = labeled("超时", groupID(gUpstream)+14, nx+s(14), rowD, s(76), s(24))
	d.cServerLbls = append(d.cServerLbls,
		hint("本地不做可能被污染的 DNS 解析", groupID(gUpstream)+33, s(pad+14), cw-s(pad+14), rowD+s(32)))

	// 上面 labeled() 造的是普通 EDIT，这里把需要联动的几个换成具名句柄
	d.cLHost = getCtrlByID(d.hwnd, groupID(gUpstream)+6)
	d.cListen = getCtrlByID(d.hwnd, groupID(gUpstream)+7)
	d.cLUser = getCtrlByID(d.hwnd, groupID(gUpstream)+8)
	d.cLPass = getCtrlByID(d.hwnd, groupID(gUpstream)+9)
	d.cHost = getCtrlByID(d.hwnd, groupID(gUpstream)+10)
	d.cPort = getCtrlByID(d.hwnd, groupID(gUpstream)+11)
	d.cUser = getCtrlByID(d.hwnd, groupID(gUpstream)+12)
	d.cPass = getCtrlByID(d.hwnd, groupID(gUpstream)+13)
	d.cTimeout = getCtrlByID(d.hwnd, groupID(gUpstream)+14)

	// ---- WireGuard ----
	wy := s(330)
	d.cWGGroup = d.l.group(d.l.fitGroupTitle("WireGuard 隧道 —— 纯用户态，不需要驱动、不改系统路由", inner-s(20)), dgWGGroup, s(pad), wy, inner, s(288))

	// wgRow 造一行"左对齐标签 + 输入框"，返回输入框右边界。
	wgRow := func(text string, cid id, y, fieldW int32) int32 {
		lw := d.l.measure(d.l.fRegular, text) + 10
		d.wgLbls = append(d.wgLbls, d.l.label(text, cid+1000, s(pad+14), y+s(4), lw, s(18)))
		ct := d.l.edit(id(cid), s(pad+14)+lw+s(8), y-s(4), fieldW, s(24), 0)
		d.wgCtrls = append(d.wgCtrls, ct)
		return s(pad+14) + lw + s(8) + fieldW
	}

	// wgKeyRow 造一行"标签 + 尽可能宽的输入框 + 一键复制按钮"。
	//
	// 密钥固定 44 个字符（实测约需 340px 才能完整显示），
	// 而用户手动框选时经常选不全 —— 之前就是这样把私钥抄成了 37 个字符，
	// 导致握手一直失败还查不出原因。所以这里：
	//   · 输入框给到"标签之后、复制按钮之前"的全部宽度；
	//   · 每个密钥字段都配一个复制按钮，彻底不用手选。
	btnW := s(90)
	fieldRight := cw - s(pad+14) - btnW - s(10)
	wgKeyRow := func(text string, fieldCid, btnCid id, y int32, readonly bool) int32 {
		lw := d.l.measure(d.l.fRegular, text) + 10
		x := s(pad+14) + lw + s(8)
		w := fieldRight - x
		d.wgLbls = append(d.wgLbls, d.l.label(text, fieldCid+1000, s(pad+14), y+s(4), lw, s(18)))

		style := uint32(wsChild | wsVisible | wsBorder | wsExClientEdge | esLeft)
		if readonly {
			style |= esReadOnly
		} else {
			style |= esAutoHScroll // 内容比框长时可以横向滚动看全
		}
		d.wgCtrls = append(d.wgCtrls,
			d.l.make(control{Class: "EDIT", Style: style, Ex: wsExClientEdge, ID: fieldCid},
				x, y-s(4), w, s(24)))

		bc, _ := d.l.buttonAuto("复制", btnCid, fieldRight+s(10), y-s(5), btnW, s(26))
		d.wgCtrls = append(d.wgCtrls, bc)
		return w
	}

	wgKeyRow("私钥", dgWGPriv, dgWGCopyPriv, wy+s(26), false)
	wgRow("隧道地址", dgWGAddr, wy+s(56), s(300))
	wgKeyRow("对端公钥", dgWGPub, dgWGCopyPeer, wy+s(86), false)

	hostRight := wgRow("服务器", dgWGEHost, wy+s(116), s(200))
	d.wgLbls = append(d.wgLbls, d.l.label("端口", groupID(gUpstream)+50, hostRight+s(16), wy+s(120), s(66), s(18)))
	d.cWGEPort = d.l.edit(dgWGEPort, hostRight+s(16)+s(66), wy+s(112), s(90), s(24), esNumber)
	d.wgCtrls = append(d.wgCtrls, d.cWGEPort)

	allowRight := wgRow("AllowedIPs", dgWGAllow, wy+s(146), s(250))
	d.wgLbls = append(d.wgLbls, d.l.label("Keepalive", groupID(gUpstream)+51, allowRight+s(16), wy+s(150), s(76), s(18)))
	d.cWGKeep = d.l.edit(dgWGKeep, allowRight+s(16)+s(76), wy+s(142), s(70), s(24), esNumber)
	d.wgCtrls = append(d.wgCtrls, d.cWGKeep)

	// 预共享密钥（wg-quick 的 PresharedKey）。不少服务商与家用路由器默认开启，
	// 缺了它会一直握手失败，而 WireGuard 不会告诉你原因，所以必须能填。
	wgKeyRow("预共享密钥", dgWGPSK, dgWGCopyPSK, wy+s(176), false)

	// MTU 行右侧放「生成密钥」
	mtuRight := wgRow("MTU", dgWGMTU, wy+s(206), s(90))
	bGen, _ := d.l.buttonAuto("生成密钥", dgWGGen, mtuRight+s(20), wy+s(204), s(120), s(28))
	d.wgCtrls = append(d.wgCtrls, bGen)

	// 最后一行：本端公钥（只读，跟着私钥实时变）。
	// 服务端的 peer 里必须登记这一个公钥，对不上只会静默丢包，所以直接摊在界面上。
	wgKeyRow("本端公钥", dgWGPubShow, dgWGCopyPub, wy+s(236), true)
	d.cWGPubShow = getCtrlByID(d.hwnd, dgWGPubShow)

	// 记住具名句柄
	d.cWGPriv = getCtrlByID(d.hwnd, dgWGPriv)
	d.cWGAddr = getCtrlByID(d.hwnd, dgWGAddr)
	d.cWGPub = getCtrlByID(d.hwnd, dgWGPub)
	d.cWGEHost = getCtrlByID(d.hwnd, dgWGEHost)
	d.cWGAllow = getCtrlByID(d.hwnd, dgWGAllow)
	d.cWGMTU = getCtrlByID(d.hwnd, dgWGMTU)
	d.cWGPSK = getCtrlByID(d.hwnd, dgWGPSK)

	d.cStatus = d.l.make(control{
		Class: "STATIC", Text: "", Style: wsChild | wsVisible | ssLeft | ssEndEllipsis, ID: dgStatus,
	}, s(pad), s(626), inner, s(20))

	// 底部按钮：右对齐，宽度按文字
	_, wOK := d.l.defButtonAuto("确定", dgOK, 0, s(656), s(76), s(30))
	_, wCancel := d.l.buttonAuto("取消", dgCancel, 0, s(656), s(76), s(30))
	xOK := cw - s(pad) - wOK
	moveCtrl(getCtrlByID(d.hwnd, dgOK), xOK, s(656), wOK, s(30))
	moveCtrl(getCtrlByID(d.hwnd, dgCancel), xOK-s(12)-wCancel, s(656), wCancel, s(30))

	d.load()
	d.typeOnChange()
}
func (d *dlgState) load() {
	u := d.up
	d.cName.setText(u.Name)

	t := u.Type
	if t == "" {
		t = config.UpstreamTypeSocks5
	}
	if i := indexOf(dlgTypes, t); i >= 0 {
		d.cType.comboSelect(int32(i))
	} else {
		d.cType.comboSelect(0)
	}

	d.cQuick.setText("")
	d.cHost.setText(u.ServerHost)
	d.cPort.setIntValue(u.ServerPort)
	d.cUser.setText(u.Username)
	d.cPass.setText(u.Password)

	lh := u.ListenHost
	if lh == "" {
		lh = "127.0.0.1"
	}
	d.cLHost.setText(lh)
	d.cListen.setIntValue(u.ListenPort)
	d.cLUser.setText(u.ListenUsername)
	d.cLPass.setText(u.ListenPassword)

	to := u.TimeoutMs
	if to == 0 {
		to = 15000
	}
	d.cTimeout.setIntValue(to)

	w := u.WireGuard
	if w == nil {
		w = &config.WireGuardConfig{}
	}
	d.cWGPriv.setText(w.PrivateKey)
	d.cWGAddr.setText(w.Address)
	d.cWGPub.setText(w.PeerPublicKey)
	d.cWGEHost.setText(w.EndpointHost)
	d.cWGEPort.setIntValue(w.EndpointPort)

	allowed := w.AllowedIPs
	if allowed == "" {
		allowed = "0.0.0.0/0, ::/0"
	}
	d.cWGAllow.setText(allowed)

	keep := w.PersistentKeepalive
	if keep == 0 {
		keep = 25
	}
	d.cWGKeep.setIntValue(keep)

	mtu := w.MTU
	if mtu == 0 {
		mtu = 1420
	}
	d.cWGMTU.setIntValue(mtu)

	// 载入后立刻刷新一次本端公钥，打开对话框就能看到
	d.refreshPubKey()

	d.cWGPSK.setText(w.PresharedKey)
}

// typeOnChange 按类型切换"住宅服务器"与"WireGuard"两组的可见性。
func (d *dlgState) typeOnChange() {
	wg := d.cType.comboText() == "wireguard"

	show := !wg
	d.cServerGroup.setVisible(show)
	for _, c := range d.cServerLbls {
		c.setVisible(show)
	}
	for _, c := range d.cServerCtrls {
		c.setVisible(show)
		c.setEnabled(show)
	}

	d.cWGGroup.setVisible(wg)
	for _, c := range d.wgCtrls {
		c.setVisible(wg)
		c.setEnabled(wg)
	}
	for _, l := range d.wgLbls {
		l.setVisible(wg)
	}
}

// ---------------------------------------------------------------------------
// 消息
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 对话框窗口类与共用窗口过程
//
// 【为什么必须共用】
// Win32 的窗口类**每个进程只能注册一次**：第二次调用 RegisterClassExW 会失败，
// 而类里保存的仍然是**第一次**注册时的窗口过程。
// 早先这里是每次打开对话框都 NewCallback(d.wndProc) + registerClassExW，
// 于是从**第二次打开对话框**开始，所有按钮消息都被派发到
// **第一次那个已经被销毁的 dlgState** 上 —— 表现就是"点生成密钥/复制公钥没反应"，
// 而且第一次打开时一切正常，极具迷惑性。
//
// 现在：类只注册一次，窗口过程是固定的一个，通过 dlgCurrent 路由到当前实例。
// ---------------------------------------------------------------------------

var (
	dlgClassOnce sync.Once
	dlgProcAddr  uintptr
	dlgCurrent   atomic.Pointer[dlgState]
)

func ensureDlgClass(inst uintptr) {
	dlgClassOnce.Do(func() {
		dlgProcAddr = syscall.NewCallback(dlgWndProc)
		wc := WNDCLASSEX{
			Style:         0x0008 | 0x0002,
			LpfnWndProc:   dlgProcAddr,
			HInstance:     inst,
			HCursor:       loadCursor(idcArrowW),
			HbrBackground: getSysColorBrush(systemColorBtnFace),
			LpszClassName: dlgClassName,
		}
		registerClassExW(&wc)
	})
}

// dlgWndProc 是对话框类的唯一窗口过程，把消息转给当前打开的那个对话框。
func dlgWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if d := dlgCurrent.Load(); d != nil {
		return d.handleMsg(hwnd, msg, wParam, lParam)
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func (d *dlgState) handleMsg(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmCommand:
		cid := int32(wParam & 0xFFFF)
		notif := int32((wParam >> 16) & 0xFFFF)

		// ⚠️ 通知码必须**按控件分别判断**，不能一句 `if notif != 0 { return }` 全滤掉：
		// 按钮的点击码是 BN_CLICKED(0)，但下拉框选中项时发的是 **CBN_SELCHANGE(1)**，
		// 编辑框内容变化发的是 **EN_CHANGE(0x300)**。
		// 早先就是被那句统一过滤吃掉了 CBN_SELCHANGE，
		// 导致"切换类型后下面那组输入框不跟着变"。

		// 类型下拉框换了选项 → 切换「住宅服务器」与「WireGuard」两组的显隐
		if cid == int32(dgType) && notif == cbnSelChange {
			d.typeOnChange()
			return 0
		}
		// 私钥一改就实时推导"本端公钥"：
		// 用户粘贴完私钥，立刻就能拿到该交给服务端的那一串。
		if cid == int32(dgWGPriv) && notif == enChange {
			d.refreshPubKey()
			return 0
		}
		// 到这里只处理按钮点击
		if notif != bnClicked {
			return 0
		}

		switch id(cid) {
		case dgOK:
			d.accept()
		case dgCancel:
			d.ok = false
			d.closed = true
		case dgParse:
			d.applyQuickImport()
		case dgWGGen:
			d.generateKeys()
		case dgWGCopyPub:
			d.copyToClipboard(d.cWGPubShow.text(), "本端公钥")
		case dgWGCopyPriv:
			// 私钥是 44 个字符，手动框选极易选不全（就因为这个吃过亏），
			// 所以必须给一个一键复制的口子。
			d.copyToClipboard(d.cWGPriv.text(), "私钥（注意保密，不要发给服务端）")
		case dgWGCopyPeer:
			d.copyToClipboard(d.cWGPub.text(), "对端公钥")
		case dgWGCopyPSK:
			d.copyToClipboard(d.cWGPSK.text(), "预共享密钥")
		}
		return 0
	case wmClose:
		d.ok = false
		d.closed = true
		return 0
	case wmDestroy:
		d.closed = true
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func (d *dlgState) setStatus(s string) {
	d.cStatus.setText(s)
	if strings.HasPrefix(s, "已填入") {
		d.cStatus.setText("✅ " + s)
	} else if s != "" {
		d.cStatus.setText("⚠ " + s)
	}
}

// applyQuickImport 解析 user:pass@host:port 并回填表单。
func (d *dlgState) applyQuickImport() {
	raw := strings.TrimSpace(d.cQuick.text())
	if raw == "" {
		d.setStatus("请先粘贴代理地址")
		return
	}
	typ, user, pass, host, port, err := config.ParseProxySpec(raw)
	if err != nil {
		d.setStatus("解析失败：" + err.Error())
		return
	}
	if typ == config.UpstreamTypeWireGuard {
		d.setStatus("这是 WireGuard 形式，请在下方 WireGuard 分组里填写密钥")
		d.cType.comboSelect(int32(indexOf(dlgTypes, config.UpstreamTypeWireGuard)))
		d.typeOnChange()
		d.cWGEHost.setText(host)
		d.cWGEPort.setIntValue(port)
		return
	}
	d.cType.comboSelect(int32(indexOf(dlgTypes, typ)))
	d.typeOnChange()
	d.cHost.setText(host)
	d.cPort.setIntValue(port)
	d.cUser.setText(user)
	d.cPass.setText(pass)

	name := strings.TrimSpace(d.cName.text())
	if name == "" || strings.HasPrefix(name, "住宅代理") {
		d.cName.setText(fmt.Sprintf("%s:%d", host, port))
	}
	d.setStatus(fmt.Sprintf("已填入 %s:%d（%s）", host, port, typ))
}

// accept 收集表单并做完整校验。
func (d *dlgState) accept() {
	u := d.up

	u.Name = strings.TrimSpace(d.cName.text())
	if u.Name == "" {
		d.setStatus("请填写名称")
		d.cName.focus()
		return
	}
	u.Enabled = d.cEnable.checked()
	u.Type = d.cType.comboText()
	u.ListenHost = strings.TrimSpace(d.cLHost.text())
	u.ListenPort = d.cListen.intValue(0)
	u.ListenUsername = strings.TrimSpace(d.cLUser.text())
	u.ListenPassword = d.cLPass.text()
	u.TimeoutMs = d.cTimeout.intValue(15000)

	if u.Type == config.UpstreamTypeWireGuard {
		u.ServerHost = ""
		u.ServerPort = 0
		u.Username = ""
		u.Password = ""
		u.WireGuard = &config.WireGuardConfig{
			PrivateKey:          strings.TrimSpace(d.cWGPriv.text()),
			Address:             strings.TrimSpace(d.cWGAddr.text()),
			PeerPublicKey:       strings.TrimSpace(d.cWGPub.text()),
			EndpointHost:        strings.TrimSpace(d.cWGEHost.text()),
			EndpointPort:        d.cWGEPort.intValue(51820),
			AllowedIPs:          strings.TrimSpace(d.cWGAllow.text()),
			PersistentKeepalive: d.cWGKeep.intValue(25),
			PresharedKey:        strings.TrimSpace(d.cWGPSK.text()),
			MTU:                 d.cWGMTU.intValue(1420),
		}
	} else {
		u.WireGuard = nil
		u.ServerHost = strings.TrimSpace(d.cHost.text())
		u.ServerPort = d.cPort.intValue(0)
	}

	if err := d.validate(&u); err != nil {
		d.setStatus(err.Error())
		return
	}

	d.up = u
	d.ok = true
	d.closed = true
}

func (d *dlgState) validate(u *config.Upstream) error {
	if u.Type == config.UpstreamTypeWireGuard {
		w := u.WireGuard
		switch {
		case w.PrivateKey == "":
			return fmt.Errorf("请填写 WireGuard 私钥")
		case w.PeerPublicKey == "":
			return fmt.Errorf("请填写 WireGuard 对端公钥")
		case w.EndpointHost == "":
			return fmt.Errorf("请填写 WireGuard 服务器地址")
		case w.EndpointPort < 1 || w.EndpointPort > 65535:
			return fmt.Errorf("WireGuard 服务器端口不合法（1-65535）")
		case w.Address == "" || !strings.Contains(w.Address, "/"):
			return fmt.Errorf("隧道地址请写成 CIDR，例如 10.7.0.2/32")
		}
	} else {
		switch {
		case u.ServerHost == "":
			return fmt.Errorf("请填写住宅代理服务器地址")
		case u.ServerPort < 1 || u.ServerPort > 65535:
			return fmt.Errorf("住宅代理端口不合法（1-65535）")
		}
	}

	if u.ListenHost == "" {
		u.ListenHost = "127.0.0.1"
	}
	if u.ListenPort < 1 || u.ListenPort > 65535 {
		return fmt.Errorf("本地端口不合法（1-65535）")
	}

	fc := d.parent.app.GetFront()
	if fc.Type != config.FrontTypeDirect && fc.Port == u.ListenPort {
		return fmt.Errorf("本地端口 %d 与前置代理端口相同，会造成环路，请换一个", u.ListenPort)
	}
	for _, other := range d.parent.app.Config().Upstreams {
		if other.ID == u.ID || other.ListenPort != u.ListenPort {
			continue
		}
		return fmt.Errorf("本地端口 %d 已被「%s」占用，请换一个", u.ListenPort, other.Name)
	}

	if d.parent.app.IsRunning() {
		if err := checkPortFree(u.ListenHost, u.ListenPort); err != nil {
			return fmt.Errorf("本地端口 %d 当前无法绑定：%v", u.ListenPort, err)
		}
	}
	return nil
}

// centerOn 把 child 居中覆盖在 parent 之上。
func centerOn(child, parent uintptr) {
	cr := getWindowRect(child)
	pr := getWindowRect(parent)
	cw := cr.Right - cr.Left
	ch := cr.Bottom - cr.Top
	px := (pr.Left + pr.Right) / 2
	py := (pr.Top + pr.Bottom) / 2
	setWindowPos(child, px-cw/2, py-ch/2, cw, ch)
}

// copyToClipboard 把内容写进剪贴板并给出状态提示。
// 空内容时明确拒绝，避免"复制了空串却提示成功"。
func (d *dlgState) copyToClipboard(text, what string) {
	if strings.TrimSpace(text) == "" {
		d.setStatus(what + " 是空的，没有可复制的内容")
		return
	}
	if err := putClipboard(text); err != nil {
		d.setStatus("复制失败：" + err.Error())
		return
	}
	d.setStatus("已复制" + what + "到剪贴板")
}

// refreshPubKey 由私钥实时推导"本端公钥"并显示。
//
// WireGuard 的安全模型是：私钥只在本地、公钥交给服务端。
// 而公钥对不上时协议只会**静默丢包**，不报任何错，
// 所以把推导出来的公钥直接摊在界面上，用户一眼就能核对。
func (d *dlgState) refreshPubKey() {
	priv := strings.TrimSpace(d.cWGPriv.text())
	if priv == "" {
		d.cWGPubShow.setText("")
		return
	}
	pub, err := wgproxy.PublicKeyFromPrivate(priv)
	if err != nil {
		d.cWGPubShow.setText("（私钥格式不对）")
		return
	}
	d.cWGPubShow.setText(pub)
}

// generateKeys 生成一对新密钥，填入私钥并显示公钥。
func (d *dlgState) generateKeys() {
	kp, err := wgproxy.GenerateKeyPair()
	if err != nil {
		d.setStatus("生成密钥失败：" + err.Error())
		return
	}
	d.cWGPriv.setText(kp.PrivateKey)
	d.cWGPubShow.setText(kp.PublicKey)
	d.setStatus("已生成新密钥对。请把「本端公钥」交给服务端登记，私钥不要外发")
}
