//go:build windows

package gui

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"

	"chainproxy/internal/config"
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

	dgWGGroup = 0x7040
	dgWGPriv  = 0x7041
	dgWGAddr  = 0x7042
	dgWGPub   = 0x7043
	dgWGEHost = 0x7044
	dgWGEPort = 0x7045
	dgWGAllow = 0x7046
	dgWGKeep  = 0x7047
	dgWGMTU   = 0x7048
)

// dgParse 属于"住宅代理"控件组，不能写成 const（groupID 是函数）。
var dgParse = id(groupID(gUpstream) | 40)

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

	wc := WNDCLASSEX{
		Style:         0x0008 | 0x0002,
		LpfnWndProc:   syscall.NewCallback(d.wndProc),
		HInstance:     d.inst,
		HCursor:       loadCursor(idcArrowW),
		HbrBackground: getSysColorBrush(systemColorBtnFace),
		LpszClassName: dlgClassName,
	}
	registerClassExW(&wc) // 重复注册返回 0，属正常情况，忽略

	d.hwnd = createWindowExW(wsExDlgModalFrame, "ChainProxyEditDialog", title,
		wsPopup|wsCaption|wsBorder|wsSysMenu,
		0, 0, scaleDPI(620, d.dpi()), scaleDPI(700, d.dpi()), d.parent.hwnd, 0, d.inst)
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

func (d *dlgState) build() {
	f := d.parent.fonts
	d.l = layout{parent: d.hwnd, inst: d.inst, fRegular: f.regular, fBold: f.bold, fMono: f.mono}
	s := func(v int32) int32 { return scaleDPI(v, d.dpi()) }

	d.cName = d.l.edit(dgName, s(78), s(13), s(240), s(24), 0)
	d.l.label("名称", groupID(gUpstream)|1, s(30), s(17), s(44), s(18))
	d.l.label("类型", groupID(gUpstream)|2, s(340), s(17), s(40), s(18))
	d.cType = d.l.combo(dgType, dlgTypes, s(384), s(13), s(150), s(160))
	d.cEnable = d.l.checkbox("启用此代理", dgEnable, s(440), s(46), s(130), s(22))

	// 快速导入
	d.l.group("快速导入 —— 可直接粘贴 user:pass@host:port", groupID(gUpstream)|3, s(18), s(78), s(564), s(62))
	d.cQuick = d.l.edit(dgQuick, s(30), s(98), s(400), s(24), 0)
	d.l.button("解析并填入", dgParse, s(438), s(96), s(132), s(26))

	// 本地端口
	d.l.group("本地端口 —— 浏览器 / 软件里填这个", groupID(gUpstream)|5, s(18), s(148), s(564), s(96))
	d.l.label("地址", groupID(gUpstream)|6, s(30), s(178), s(34), s(18))
	d.cLHost = d.l.edit(dgLHost, s(68), s(174), s(130), s(24), 0)
	d.l.label("端口", groupID(gUpstream)|7, s(206), s(178), s(34), s(18))
	d.cListen = d.l.edit(dgListen, s(244), s(174), s(84), s(24), esNumber)
	d.l.make(control{Class: "STATIC", Text: "端口全局唯一，重复会在保存时拦截", Style: wsChild | wsVisible | ssLeft, ID: groupID(gUpstream) | 30}, s(342), s(180), s(232), s(18)).font(f.tiny)

	d.l.label("端口账号", groupID(gUpstream)|8, s(30), s(212), s(56), s(18))
	d.cLUser = d.l.edit(dgLUser, s(90), s(208), s(110), s(24), 0)
	d.l.label("密码", groupID(gUpstream)|9, s(212), s(212), s(34), s(18))
	d.cLPass = d.l.edit(dgLPass, s(250), s(208), s(110), s(24), esPassword)
	d.l.make(control{Class: "STATIC", Text: "默认只监听 127.0.0.1，可留空", Style: wsChild | wsVisible | ssLeft, ID: groupID(gUpstream) | 31}, s(372), s(214), s(200), s(18)).font(f.tiny)

	// 住宅代理服务器
	d.cServerGroup = d.l.group("住宅代理服务器 —— 本程序会经前置代理连接它", dgServerGroup, s(18), s(252), s(564), s(108))
	d.l.make(control{Class: "STATIC", Text: "DNS 由住宅代理解析，本地不做污染的解析", Style: wsChild | wsVisible | ssLeft, ID: groupID(gUpstream) | 32}, s(30), s(340), s(540), s(18)).font(f.tiny)
	d.cServerLbls = append(d.cServerLbls,
		d.l.label("地址", groupID(gUpstream)|10, s(30), s(282), s(40), s(18)),
		d.l.label("端口", groupID(gUpstream)|11, s(352), s(282), s(34), s(18)),
		d.l.label("用户名", groupID(gUpstream)|12, s(30), s(316), s(48), s(18)),
		d.l.label("密码", groupID(gUpstream)|13, s(230), s(316), s(34), s(18)),
		d.l.label("超时ms", groupID(gUpstream)|14, s(396), s(316), s(50), s(18)),
	)
	d.cHost = d.l.edit(dgHost, s(74), s(278), s(270), s(24), 0)
	d.cPort = d.l.edit(dgPort, s(390), s(278), s(84), s(24), esNumber)
	d.cUser = d.l.edit(dgUser, s(82), s(312), s(140), s(24), 0)
	d.cPass = d.l.edit(dgPass, s(268), s(312), s(120), s(24), esPassword)
	d.cTimeout = d.l.edit(dgTimeout, s(450), s(312), s(84), s(24), esNumber)
	d.cServerCtrls = []ctrl{d.cHost, d.cPort, d.cUser, d.cPass, d.cTimeout}

	// WireGuard
	d.cWGGroup = d.l.group("WireGuard 隧道 —— 纯用户态，不需要驱动、不改系统路由", dgWGGroup, s(18), s(368), s(564), s(228))
	wgField := func(label string, cid int32, lx, ex, ey, ew int32) ctrl {
		d.wgLbls = append(d.wgLbls, d.l.label(label, id(cid+1000), s(lx), s(ey+4), s(ex-lx-4), s(18)))
		ct := d.l.edit(id(cid), s(ex), s(ey), s(ew), s(24), 0)
		d.wgCtrls = append(d.wgCtrls, ct)
		return ct
	}
	d.cWGPriv = wgField("私钥", dgWGPriv, 30, 84, 396, 470)
	d.cWGAddr = wgField("隧道地址", dgWGAddr, 30, 90, 428, 464)
	d.cWGPub = wgField("对端公钥", dgWGPub, 30, 90, 460, 464)
	d.cWGEHost = wgField("服务器", dgWGEHost, 30, 84, 492, 300)
	d.wgLbls = append(d.wgLbls, d.l.label("端口", groupID(gUpstream)|50, s(392), s(496), s(34), s(18)))
	d.cWGEPort = d.l.edit(dgWGEPort, s(430), s(492), s(100), s(24), esNumber)
	d.wgCtrls = append(d.wgCtrls, d.cWGEPort)
	d.cWGAllow = wgField("AllowedIPs", dgWGAllow, 30, 104, 524, 300)
	d.wgLbls = append(d.wgLbls, d.l.label("Keepalive", groupID(gUpstream)|51, s(414), s(528), s(62), s(18)))
	d.cWGKeep = d.l.edit(dgWGKeep, s(480), s(524), s(80), s(24), esNumber)
	d.wgCtrls = append(d.wgCtrls, d.cWGKeep)
	d.cWGMTU = wgField("MTU", dgWGMTU, 30, 68, 556, 100)
	d.wgLbls = append(d.wgLbls, d.l.make(control{
		Class: "STATIC",
		Text:  "隧道地址示例 10.7.0.2/32；域名一律本地解析后送入隧道",
		Style: wsChild | wsVisible | ssLeft, ID: groupID(gUpstream) | 52,
	}, s(184), s(560), s(380), s(18)))
	d.wgLbls[len(d.wgLbls)-1].font(f.tiny)

	d.cStatus = d.l.make(control{
		Class: "STATIC", Text: "", Style: wsChild | wsVisible | ssLeft | ssEndEllipsis, ID: dgStatus,
	}, s(18), s(604), s(564), s(20))

	d.l.button("取消", dgCancel, s(400), s(628), s(90), s(30))
	d.l.defButton("确定", dgOK, s(496), s(628), s(86), s(30))

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

func (d *dlgState) wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmCommand:
		switch id(int32(wParam & 0xFFFF)) {
		case dgOK:
			d.accept()
		case dgCancel:
			d.ok = false
			d.closed = true
		case dgParse:
			d.applyQuickImport()
		case dgType:
			d.typeOnChange()
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
