package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"chainproxy/internal/app"
	"chainproxy/internal/config"
	"chainproxy/internal/ports"
	"chainproxy/internal/wgproxy"
)

// Version 由构建脚本注入。
var Version = "dev"

// API 是暴露给前端的全部能力。
//
// 设计原则：
//   - 前端拿到的都是"已经算好、已经格式化"的数据（例如协议显示名、
//     今日流量的可读文本），协议细节不出后端
//   - 所有可能失败的操作都返回 error，前端用统一的方式弹通知
type API struct {
	// app 是后端状态机（internal/app）。名字保持不变，免得改动散落到每个方法里。
	app *app.App

	// wapp 是 Wails v3 的应用对象：事件、剪贴板、对话框都从它上面取。
	wapp *application.App
	// notif 是系统通知服务（v3 的 pkg/services/notifications）。
	notif *notifications.NotificationService
	// mainWin 是主窗口。托盘/快捷键的"显示窗口"、导入时的隐藏/恢复都靠它。
	mainWin application.Window

	unsub func()

	// clipGuard 负责「敏感内容（分享链接 / 凭据）复制后延迟清理剪贴板」。
	// 见 clipboard_guard.go 顶部：为什么全项目只有这一类复制要清，以及
	// 为什么到点不是无条件清空。
	clipGuard *clipboardGuard

	mu       sync.Mutex
	scanning bool

	// devOpen 是开发期的自测开关（-open 启动参数），
	// 让界面启动后直接打开某个面板，方便截图检查排版。
	devOpen string

	// 以下两个 Once 用于"只提示一次"的场景：收进托盘、快捷键注册失败。
	trayHintOnce sync.Once

	// 外壳（托盘/快捷键）的初始化结果。
	//
	// 为什么要记：托盘与快捷键是在 app.Run() 之前建立的，那时后端还没加载、
	// 界面也还没起来，而 GUI 版用 -H windowsgui 构建、没有控制台，
	// 于是这类信息不记下来就等于凭空消失。ServiceStartup 起来后会把它
	// 写进日志文件并（失败的）补一条界面通知。
	trayReady    bool
	trayIconSize int
	shortcutOK   []string
	shortcutFail []string

	// 当前已注册的全局快捷键组合，改绑定时要先按这份名单注销掉。
	shortcutMu          sync.Mutex
	registeredShortcuts []string

	// pendingWarnings 是"后端还没就绪时"产生的告警，等 ServiceStartup 补投。
	pendingWarnings []string
}

// NewAPI 创建 API 实例。devOpen 为空表示正常运行。
func NewAPI(devOpen string) *API {
	a := &API{devOpen: devOpen}
	// 读写仍然走 a 上的那两个方法（它们自己处理 wapp 还没就位的情况），
	// 这样清理逻辑本身与 Wails 解耦、可单测（见 clipboard_guard.go）。
	a.clipGuard = newClipboardGuard(a.clipboardText, a.setClipboard, sensitiveClipboardTTL)
	return a
}

// ServiceStartup 是 Wails v3 的服务启动钩子（v2 里是 OnStartup + ctx 字段）。
//
// 注意它由 app.Run() 内部调用，所以此时窗口已经创建、wapp/notif/mainWin 都已就位。
func (a *API) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	inst, err := app.New(config.ConfigPath())
	if err != nil {
		// 配置坏了也得让界面起来，否则用户没有办法修
		log.Printf("读取配置失败：%v", err)
	}
	a.app = inst
	if inst == nil {
		a.dialogError("ChainProxy 启动失败",
			fmt.Sprintf("无法读取配置：%v\n\n请检查 %s", err, config.ConfigPath()))
		return nil
	}

	// 把后端事件转发到前端。
	//
	// 注意：这个回调可能在持有 App 内部锁时被调用，所以这里**只做转发**，
	// 绝不能再回头调用 a.app 的任何方法。
	a.unsub = inst.Subscribe(func(e app.Event) {
		a.Emit("cp:"+e.Kind, e.Text)
	})

	inst.Logf("ChainProxy %s 已启动，配置文件：%s", Version, config.ConfigPath())

	// 把外壳（托盘/快捷键）的初始化结果写进日志，并把后端就绪前攒下的告警补投给界面。
	a.reportShellStatus(inst)

	// 【先自愈系统代理，再做别的】
	//
	// 改系统代理是**进程外**状态：上次被强杀/崩溃时来不及还原，用户就会在
	// "浏览器指向一个没人监听的端口"的状态下继续用整台机器。所以启动第一件事
	// 是检查有没有留下备份，有就按备份还原。
	if restoreSystemProxyIfNeeded() {
		inst.Logf("检测到上次退出时未还原的系统代理，已自动恢复成原来的设置")
	}
	// 备份既然已经还原，开关也必须跟着对齐 —— 否则界面显示"全局代理开着"，
	// 而实际系统代理已经被我们还原了，两边说的不是一回事。
	if s := inst.Settings(); s.SystemProxy {
		s.SystemProxy = false
		_ = inst.SetSettings(s)
		inst.Logf("启动时把「全局代理模式」置为关闭（系统代理已还原；要重新开启请点一下）")
	}

	inst.AutoActivate()
	return nil
}

// ServiceShutdown 是 Wails v3 的服务关闭钩子（v2 里是 OnShutdown）。
func (a *API) ServiceShutdown() error {
	// 退出前一定要把系统代理还回去：这是**进程外**的状态，漏了就是用户整机断网。
	//
	// 【判据不是设置里的开关】SystemProxy 只是"用户想开全局代理"的意图，它会被
	// 别的操作整体覆盖掉 —— 「恢复默认设置」就是用 Default().Settings 整个盖上
	// （里面 SystemProxy 是 false），而注册表没人动。这时若只看开关就会跳过还原：
	// 程序一关、注册表还指着 127.0.0.1:<节点端口>，用户整机断网（审查 #5）。
	// 所以判据交给 systemProxyNeedsRestore（备份文件还在 = 注册表确实被改过）。
	if a.app != nil {
		if systemProxyNeedsRestore(a.app.Settings().SystemProxy) {
			if err := disableSystemProxy(); err != nil {
				log.Printf("退出时还原系统代理失败：%v", err)
			}
		}
	}
	if a.unsub != nil {
		a.unsub()
	}
	// 停掉"敏感内容延迟清理"的定时器：退出过程中不再去碰剪贴板
	// （理由见 clipboardGuard.release 的注释）。
	a.clipGuard.release()
	if a.app != nil {
		a.app.Close()
	}
	return nil
}

// systemProxyNeedsRestore 判断"要不要把系统代理还回去"。
//
// 【为什么不能只看开关】开关会被「恢复默认设置」这类整体覆盖 Settings 的操作
// 清成 false，而注册表还被我们改着（见 ServiceShutdown 的注释）。
// 第二项判据——备份文件还在——才是"我们确实改过注册表且没还原"的物证：
// enableSystemProxy 是先落盘备份再改注册表，disableSystemProxy 是还原成功才删备份。
//
// 【为什么不能干脆无条件还原】没开过全局代理、也没备份时，我们从没碰过注册表；
// 这时还原会把**用户自己**（或别的软件）配的系统代理清掉，等于换一种方式断网。
func systemProxyNeedsRestore(flagOn bool) bool {
	return flagOn || systemProxyBackupExists()
}

// reportShellStatus 把托盘/快捷键的初始化结果写进日志，并补投攒下的告警。
//
// 这是"GUI 版没有控制台"这一约束下的取证手段：结果写进 data\chainproxy.log，
// 出问题时能直接读到托盘与快捷键到底有没有建起来。
func (a *API) reportShellStatus(inst *app.App) {
	if inst == nil {
		return
	}
	if a.trayReady {
		inst.Logf("外壳：托盘已就绪（图标 %d 字节）", a.trayIconSize)
	} else {
		inst.Logf("外壳：⚠️ 托盘未建立")
	}
	if len(a.shortcutOK) > 0 {
		inst.Logf("外壳：已注册全局快捷键 %s", strings.Join(a.shortcutOK, "、"))
	}
	for _, f := range a.shortcutFail {
		inst.Logf("外壳：⚠️ %s", f)
	}
	a.flushWarnings()
}

// Quit 退出程序（界面上的"退出"按钮、托盘菜单的"退出"）。
func (a *API) Quit() {
	if a.app != nil {
		a.app.Close()
	}
	if a.wapp != nil {
		a.wapp.Quit()
	}
}

// ------------------------------------------------------------------
// 元信息
// ------------------------------------------------------------------

// MetaView 是界面启动时需要的静态信息。
type MetaView struct {
	Version            string                `json:"version"`
	DataDir            string                `json:"dataDir"`
	ConfigPath         string                `json:"configPath"`
	LogPath            string                `json:"logPath"`
	Protocols          []config.ProtocolInfo `json:"protocols"`
	ShadowsocksMethods []string              `json:"shadowsocksMethods"`
	VMessSecurities    []string              `json:"vmessSecurities"`
	TransportNetworks  []string              `json:"transportNetworks"`
	UTLSFingerprints   []string              `json:"utlsFingerprints"`
	// 内核自检字段（engineOk / engineError）已删除：它们原本反映 sing-box 的
	// 构建标签是否齐全，而 sing-box 已摘除、mihomo 不需要构建标签 ——
	// "唯一的内核有没有编译进来"是**编译期**的事（缺了根本编不过），
	// 没有必要再由运行时字段表达。见 docs/MIHOMO_MIGRATION.md 的结项报告。
	GoVersion string `json:"goVersion"`
	// AuthNone 用来告诉界面：本地端口默认不需要鉴权（只监听回环）
	DefaultPortStart int `json:"defaultPortStart"`
	RandomPortMin    int `json:"randomPortMin"`
	RandomPortMax    int `json:"randomPortMax"`
	// DevOpen 是开发期自测参数（-open xxx），生产为空。
	DevOpen string `json:"devOpen"`
}

// GetMeta 返回静态元信息。
func (a *API) GetMeta() MetaView {
	m := MetaView{
		Version:            Version,
		DataDir:            config.DataDir(),
		ConfigPath:         config.ConfigPath(),
		LogPath:            config.LogPath(),
		Protocols:          config.Protocols(),
		ShadowsocksMethods: config.ShadowsocksMethods(),
		VMessSecurities:    config.VMessSecurities(),
		TransportNetworks:  config.TransportNetworks(),
		UTLSFingerprints:   config.UTLSFingerprints(),
		DefaultPortStart:   ports.DefaultStart,
		RandomPortMin:      ports.RandomMin,
		RandomPortMax:      ports.RandomMax,
		DevOpen:            a.devOpen,
	}
	return m
}

// ------------------------------------------------------------------
// 状态
// ------------------------------------------------------------------

// GetNodes 返回节点列表视图（含运行状态、统计、测试结果）。
func (a *API) GetNodes() []app.NodeView {
	if a.app == nil {
		return []app.NodeView{}
	}
	return a.app.Nodes()
}

// SetGlobalNode 设置"全局节点"（整个列表只能有一个；传空串表示取消）。
//
// 它只是"指定用哪个节点做全局代理"，本身不改系统设置 —— 改系统代理是
// SetSystemProxyMode(true) 的事。分开是有意的：指定节点没有副作用，随时可改。
func (a *API) SetGlobalNode(id string) error {
	if a.app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	s := a.app.Settings()
	s.GlobalNodeID = id
	if err := a.app.SetSettings(s); err != nil {
		return err
	}
	if id == "" {
		a.app.Logf("已取消全局节点")
	} else {
		a.app.Logf("已把「%s」设为全局节点", a.nodeName(id))
	}
	return nil
}

// SetSystemProxyMode 开/关"全局代理模式"。
//
// 开：把 Windows 系统代理指到全局节点的本地端口；关：还原（有备份按备份还原）。
// **必须先备份再改**：进程外状态一旦没还原，用户就整机断网（见 sysproxy_windows.go）。
func (a *API) SetSystemProxyMode(on bool) error {
	if a.app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	if !on {
		if err := disableSystemProxy(); err != nil {
			return err
		}
		s := a.app.Settings()
		s.SystemProxy = false
		_ = a.app.SetSettings(s)
		a.app.Logf("已关闭全局代理模式，系统代理已还原")
		a.app.Notify("info", "系统代理已还原", "")
		return nil
	}

	s := a.app.Settings()
	if s.GlobalNodeID == "" {
		return fmt.Errorf("还没有设置全局节点：先在节点右键菜单里选「设为全局节点」")
	}
	var port int
	var state string
	for _, v := range a.app.Nodes() {
		if v.ID == s.GlobalNodeID {
			port, state = v.ListenPort, v.State
			break
		}
	}
	if port == 0 {
		return fmt.Errorf("全局节点不存在或没有本地端口")
	}
	if state != "running" {
		return fmt.Errorf("全局节点没有在运行：先激活它，否则系统代理会指向一个没人监听的口")
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if err := enableSystemProxy(addr); err != nil {
		return err
	}
	s.SystemProxy = true
	if err := a.app.SetSettings(s); err != nil {
		return err
	}
	a.app.Logf("已开启全局代理模式：系统代理 → %s（全局节点「%s」）", addr, a.nodeName(s.GlobalNodeID))
	a.app.Notify("success", "系统代理已指向全局节点", addr)
	return nil
}

// nodeName 查节点名（日志与通知里用；查不到就回退成 id）。
func (a *API) nodeName(id string) string {
	if a.app == nil {
		return id
	}
	for _, v := range a.app.Nodes() {
		if v.ID == id {
			return v.Name
		}
	}
	return id
}

// GetConnections 返回连接表（活动的在前，随后是最近关闭的）。
func (a *API) GetConnections() []app.ConnRecord {
	if a.app == nil {
		return nil
	}
	return a.app.Connections()
}

// CloseAllConnections 关闭全部活动连接，返回请求关闭的条数。
func (a *API) CloseAllConnections() int {
	if a.app == nil {
		return 0
	}
	return a.app.CloseAllConnections()
}

// GetIPInfo 查一次本机直连 IP 信息（底部栏「网站测试」右侧那块）。
func (a *API) GetIPInfo() app.IPInfo {
	if a.app == nil {
		return app.IPInfo{Err: "应用尚未就绪"}
	}
	return a.app.FetchIPInfo(context.Background())
}

// TestSites 通过各节点的本地端口测站点可达性与延迟。
//
// 返回的是"站点 × 节点"的结果：界面既可以用它画普通列表，
// 也可以用它画"哪个站点经哪个节点通"的矩阵。
func (a *API) TestSites(sites []string, nodeIDs []string) []app.SiteTestResult {
	if a.app == nil {
		return nil
	}
	return a.app.TestSites(context.Background(), sites, nodeIDs, 6*time.Second)
}

// GetNetworkLogs 返回序号大于 afterSeq 的网络日志（游标式增量拉取），
// 第二个返回值是当前最大序号。
func (a *API) GetNetworkLogs(afterSeq int64) ([]app.NetLogEntry, int64) {
	if a.app == nil {
		return nil, 0
	}
	return a.app.NetworkLogs(afterSeq)
}

// ClearNetworkLogs 清空网络日志。
func (a *API) ClearNetworkLogs() {
	if a.app != nil {
		a.app.ClearNetworkLogs()
	}
}

// GetTrafficHistory 返回最近 seconds 秒的每秒速率样本（流量统计画曲线用）。
//
// nodeID 为空表示全部节点（总量），否则只看那一个节点 —— 用户要求图可以按节点切。
// 放在 Go 侧留历史的原因见 internal/app/stats.go 的 TrafficSample 注释。
func (a *API) GetTrafficHistory(seconds int, nodeID string) []app.TrafficSample {
	if a.app == nil {
		return nil
	}
	return a.app.TrafficHistory(seconds, nodeID)
}

// GetSummary 返回顶部总览。
func (a *API) GetSummary() app.Summary {
	if a.app == nil {
		return app.Summary{}
	}
	return a.app.Summary()
}

// GetLogs 返回最近若干行日志。
func (a *API) GetLogs() []string {
	if a.app == nil {
		return nil
	}
	lines := a.app.Logs()
	if len(lines) > 400 {
		lines = lines[len(lines)-400:]
	}
	return lines
}

// GetSettings 返回全局设置。
func (a *API) GetSettings() config.Settings {
	if a.app == nil {
		return config.Default().Settings
	}
	s := a.app.Settings()
	// 归一化后再交给界面：老配置里可能存着 "Ctrl+Alt+ "（空格键的旧写法），
	// 注册时虽然已经能自愈，但界面上不该继续显示一个看不见的裸空格。
	s.ShortcutToggleWindow = normalizeAccel(s.ShortcutToggleWindow)
	s.ShortcutToggleAll = normalizeAccel(s.ShortcutToggleAll)
	return s
}

// SaveSettings 保存全局设置。
func (a *API) SaveSettings(s config.Settings) error {
	if a.app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	if s.PortStart < 1024 || s.PortStart > 65535 {
		return fmt.Errorf("端口起始值 %d 非法（应在 1024-65535 之间）", s.PortStart)
	}
	if s.TestConcurrency < 1 || s.TestConcurrency > 64 {
		return fmt.Errorf("测试并发数应在 1-64 之间")
	}
	// 存之前归一化，让"空格键"这类写法在配置里就落成规范名字（Space）
	s.ShortcutToggleWindow = normalizeAccel(s.ShortcutToggleWindow)
	s.ShortcutToggleAll = normalizeAccel(s.ShortcutToggleAll)
	if err := a.app.SetSettings(s); err != nil {
		return err
	}
	// 快捷键可能刚被改过：立刻按新配置重新注册，并逐条报告结果。
	// 放在这里（而不是让前端额外再调一次）是为了让"保存了但没生效"不可能发生。
	if list := a.ApplyShortcuts(); len(list) > 0 {
		a.reportShortcuts(list)
		for _, st := range list {
			if st.Accel != "" && !st.OK {
				a.app.Notify("warning", "快捷键没能生效",
					fmt.Sprintf("%s（%s）：%s", st.Accel, st.Label, st.Message))
			}
		}
	}
	a.app.Notify("success", "设置已保存", "")
	return nil
}

// ShortcutStatuses 返回当前快捷键的注册情况（界面用来显示"已生效 / 被占用"）。
func (a *API) ShortcutStatuses() []ShortcutStatus {
	if a.app == nil {
		return nil
	}
	// 不重新注册，只报告当前状态：把"查询"和"改绑定"分开，
	// 免得每次打开设置面板都把快捷键注销重注册一遍（那会短暂失效）。
	saved := map[string]bool{}
	a.shortcutMu.Lock()
	for _, accel := range a.registeredShortcuts {
		saved[strings.ToLower(accel)] = true
	}
	a.shortcutMu.Unlock()

	out := a.shortcutDefs()
	for i := range out {
		st := &out[i]
		if st.Accel == "" {
			st.Message = "未启用"
			continue
		}
		st.OK = saved[strings.ToLower(st.Accel)]
		if !st.OK {
			st.Message = "未生效（可能被别的程序占用）"
		}
	}
	return out
}

// ResetSettings 把设置恢复为默认值（保留节点与前置）。
func (a *API) ResetSettings() (config.Settings, error) {
	if a.app == nil {
		return config.Settings{}, fmt.Errorf("应用尚未就绪")
	}
	// 【为什么恢复默认前要先还系统代理】Default().Settings 里 SystemProxy=false，
	// 直接覆盖就成了"设置说没开、注册表和备份文件还在"两份说法；更要命的是
	// 退出时的还原会因此被跳过，用户一关程序就整机断网（审查 #5）。
	// 所以先还注册表，成功了再写设置；还不了就整体失败（fail-closed），
	// 绝不留下"界面显示关了、实际还开着"的状态。判据同退出还原，
	// 见 systemProxyNeedsRestore（没开过、也没备份时不去碰注册表）。
	if systemProxyNeedsRestore(a.app.Settings().SystemProxy) {
		if err := disableSystemProxy(); err != nil {
			return config.Settings{}, fmt.Errorf("恢复默认设置前还原系统代理失败：%w", err)
		}
		a.app.Logf("恢复默认设置：先把系统代理还原成原来的设置")
	}
	d := config.Default().Settings
	// 【底部栏的布局偏好不参与"恢复默认"】
	//
	// 这三项不是"设置项"，而是"上次看到哪儿"：展开/收起、停在哪个页签、拖了多高。
	// 用 Default().Settings 整个盖上就等于让用户点一次按钮就永久丢掉它，
	// 现象与"偏好根本没落盘"一模一样（用户实报过：重启后不是展开的、也不是上次那个页签）。
	// 所以只重置真正的行为设置，布局偏好原样带过去。
	cur := a.app.Settings()
	d.BottomPanelOpen = cur.BottomPanelOpen
	d.BottomPanelView = cur.BottomPanelView
	d.BottomPanelHeight = cur.BottomPanelHeight
	if err := a.app.SetSettings(d); err != nil {
		return d, err
	}
	a.app.Notify("success", "设置已恢复默认", "")
	return d, nil
}

// GetFront 返回全局前置代理配置。
func (a *API) GetFront() config.Front {
	if a.app == nil {
		return config.Default().Front
	}
	return a.app.Front()
}

// SaveFront 保存全局前置代理配置。
func (a *API) SaveFront(f config.Front) error {
	if a.app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	if f.Type != config.FrontTypeDirect {
		if strings.TrimSpace(f.Host) == "" {
			return fmt.Errorf("前置代理地址不能为空")
		}
		if f.Port < 1 || f.Port > 65535 {
			return fmt.Errorf("前置代理端口 %d 非法", f.Port)
		}
	}
	if err := a.app.SetFront(f); err != nil {
		return err
	}
	a.app.Notify("success", "全局前置代理已保存", f.Describe())
	return nil
}

// TestFront 测试全局前置代理的连通性并返回出口 IP。
func (a *API) TestFront() map[string]any {
	if a.app == nil {
		return map[string]any{"ok": false, "error": "应用尚未就绪"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := a.app.TestFront(ctx)
	if !r.OK {
		return map[string]any{"ok": false, "error": fmt.Sprint(r.Err)}
	}
	a.app.Notify("success", "前置代理可用", fmt.Sprintf("出口 IP %s（%d ms）", r.IP, r.Latency.Milliseconds()))
	return map[string]any{"ok": true, "ip": r.IP, "ms": r.Latency.Milliseconds()}
}

// ------------------------------------------------------------------
// 节点增删改
// ------------------------------------------------------------------

// AddNode 新增节点。
func (a *API) AddNode(n config.Node) (config.Node, error) {
	if a.app == nil {
		return config.Node{}, fmt.Errorf("应用尚未就绪")
	}
	out, err := a.app.AddNode(n)
	if err != nil {
		return config.Node{}, err
	}
	a.app.Notify("success", "已添加节点", fmt.Sprintf("「%s」本地端口 %d", out.Name, out.ListenPort))
	return out, nil
}

// UpdateNode 更新节点。
func (a *API) UpdateNode(n config.Node) (config.Node, error) {
	if a.app == nil {
		return config.Node{}, fmt.Errorf("应用尚未就绪")
	}
	out, err := a.app.UpdateNode(n)
	if err != nil {
		return config.Node{}, err
	}
	a.app.Notify("success", "已保存节点", "「"+out.Name+"」")
	return out, nil
}

// DeleteNodes 删除节点。
func (a *API) DeleteNodes(ids []string) error {
	if a.app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	if len(ids) == 0 {
		return fmt.Errorf("请先选择要移除的节点")
	}
	if err := a.app.DeleteNodes(ids); err != nil {
		return err
	}
	a.app.Notify("success", "已移除", fmt.Sprintf("%d 个节点", len(ids)))
	return nil
}

// CloneNode 克隆节点。
func (a *API) CloneNode(id string) (config.Node, error) {
	if a.app == nil {
		return config.Node{}, fmt.Errorf("应用尚未就绪")
	}
	out, err := a.app.CloneNode(id)
	if err != nil {
		return config.Node{}, err
	}
	a.app.Notify("success", "已克隆节点", fmt.Sprintf("「%s」本地端口 %d", out.Name, out.ListenPort))
	return out, nil
}

// GetNode 按 ID 取节点完整配置（编辑对话框用）。
func (a *API) GetNode(id string) (config.Node, error) {
	if a.app == nil {
		return config.Node{}, fmt.Errorf("应用尚未就绪")
	}
	n, ok := a.app.GetNode(id)
	if !ok {
		return config.Node{}, fmt.Errorf("未找到该节点")
	}
	return n, nil
}

// Validate 返回当前配置的全部问题。
func (a *API) Validate() []config.Issue {
	if a.app == nil {
		return nil
	}
	return a.app.Validate()
}

// ------------------------------------------------------------------
// 激活 / 停止
// ------------------------------------------------------------------

// Activate 激活节点（会自动连带激活它的前置节点）。
func (a *API) Activate(ids []string) error {
	if a.app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	if len(ids) == 0 {
		return fmt.Errorf("请先选择节点")
	}
	if err := a.app.Activate(ids); err != nil {
		return err
	}
	return nil
}

// Deactivate 停止节点。
func (a *API) Deactivate(ids []string) error {
	if a.app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	if len(ids) == 0 {
		return fmt.Errorf("请先选择节点")
	}
	return a.app.Deactivate(ids)
}

// ActivateAll 激活全部节点。
func (a *API) ActivateAll() error {
	if a.app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	return a.app.ActivateAll()
}

// DeactivateAll 停止全部节点并释放端口。
func (a *API) DeactivateAll() error {
	if a.app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	a.app.StopAll()
	a.app.Notify("info", "已停止全部节点", "端口均已释放")
	return nil
}

// PlanActivation 返回激活某节点会连带启动哪些节点（用于界面提示）。
func (a *API) PlanActivation(ids []string) map[string]any {
	out := map[string]any{"order": []string{}, "auto": []string{}, "error": ""}
	if a.app == nil {
		out["error"] = "应用尚未就绪"
		return out
	}
	plan, err := a.app.PlanActivation(ids)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	// 把 ID 换成别名，界面直接显示
	view := a.app.Nodes()
	nameOf := map[string]string{}
	for _, v := range view {
		nameOf[v.ID] = v.Name
	}
	auto := make([]string, 0, len(plan.Auto))
	for _, id := range plan.Auto {
		if n, ok := nameOf[id]; ok {
			auto = append(auto, n)
		}
	}
	out["auto"] = auto
	return out
}

// ------------------------------------------------------------------
// 端口
// ------------------------------------------------------------------

// SuggestPort 返回"下一个默认端口"。
//
// 【审查 #21】没有可用端口时返回 ports.NoPort(0)，不返回没验证过的候选：
// 界面拿到 0 会显示"请填写本地代理端口"，属于 fail-closed。
func (a *API) SuggestPort(excludeID string) int {
	if a.app == nil {
		return ports.DefaultStart
	}
	return a.app.SuggestPort(excludeID)
}

// RandomPort 随机一个空闲端口。
//
// 失败（多次随机都撞上占用）时返回 ports.ErrNoFreePort，
// 界面上要显示成"多次随机仍未随机到空闲端口"。
func (a *API) RandomPort(excludeID string) (int, error) {
	if a.app == nil {
		return 0, fmt.Errorf("应用尚未就绪")
	}
	return a.app.RandomPort(excludeID)
}

// CheckPort 检测端口可用性，返回给界面直接显示的结果。
func (a *API) CheckPort(port int, excludeID string) ports.Status {
	if a.app == nil {
		return ports.Status{Port: port, Message: "应用尚未就绪"}
	}
	return a.app.PortStatus(port, excludeID)
}

// UsedPorts 返回本项目已占用的端口。
func (a *API) UsedPorts(excludeID string) map[string]string {
	if a.app == nil {
		return map[string]string{}
	}
	return a.app.UsedPorts(excludeID)
}

// ------------------------------------------------------------------
// 其它
// ------------------------------------------------------------------

// ResetTodayStats 清零今日统计（传空数组表示全部）。
func (a *API) ResetTodayStats(ids []string) error {
	if a.app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	if err := a.app.ResetTodayStats(ids); err != nil {
		return err
	}
	a.app.Notify("success", "已清零今日统计", "")
	return nil
}

// PreviewInvalidNodes 返回"测试无效"的节点名单（只读，不动配置）。
//
// 界面拿它弹确认框：写明要删几个、叫什么、不可撤销；用户确认后再把 ids
// 原样交给 RemoveInvalidNodes。判据见 internal/app 的 PreviewInvalidNodes。
func (a *API) PreviewInvalidNodes() (app.InvalidPreview, error) {
	if a.app == nil {
		return app.InvalidPreview{}, fmt.Errorf("应用尚未就绪")
	}
	return a.app.PreviewInvalidNodes(), nil
}

// RemoveInvalidNodes 删除界面确认过的这些节点。
func (a *API) RemoveInvalidNodes(ids []string) (int, error) {
	if a.app == nil {
		return 0, fmt.Errorf("应用尚未就绪")
	}
	return a.app.RemoveInvalidNodes(ids)
}

// OpenPath 在资源管理器里打开某个路径。
func (a *API) OpenPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("路径为空")
	}
	// 文件就先定位到它所在的目录
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return exec.Command("explorer.exe", "/select,"+filepath.Clean(path)).Start()
	}
	if err := exec.Command("explorer.exe", filepath.Clean(path)).Start(); err != nil {
		return fmt.Errorf("打开失败：%w", err)
	}
	return nil
}

// OpenDataDir 打开数据目录。
func (a *API) OpenDataDir() error { return a.OpenPath(config.DataDir()) }

// OpenConfigFile 打开配置文件所在位置。
func (a *API) OpenConfigFile() error { return a.OpenPath(config.ConfigPath()) }

// OpenLogFile 打开日志文件所在位置。
func (a *API) OpenLogFile() error { return a.OpenPath(config.LogPath()) }

// SetWindowTheme 同步窗口标题栏的明暗，避免"内容深色、标题栏白色"的割裂感。
//
// 【v3 与 v2 的差别】v2 有 runtime.WindowSetDarkTheme/LightTheme/SystemDefaultTheme，
// v3 **没有运行期 setter**（明暗只能在创建窗口时用 WindowsWindow{Theme:...} 指定）。
// 所以这里直接调 Win32 的 DwmSetWindowAttribute，实现见 theme_windows.go。
func (a *API) SetWindowTheme(theme string) bool {
	return setTitleBarTheme(theme)
}

// ------------------------------------------------------------------
// WireGuard 密钥
// ------------------------------------------------------------------

// GenWireGuardKey 生成一对 WireGuard 密钥（顺带给一个预共享密钥）。
//
// 放在界面里而不是只给命令行：WireGuard 的标准流程要求私钥本地生成、
// 只把公钥交给服务端，用户不该为了这一步去装 wireguard-tools。
func (a *API) GenWireGuardKey() map[string]string {
	kp, err := wgproxy.GenerateKeyPair()
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	out := map[string]string{
		"privateKey": kp.PrivateKey,
		"publicKey":  kp.PublicKey,
	}
	if psk, perr := wgproxy.GeneratePresharedKey(); perr == nil {
		out["presharedKey"] = psk
	}
	if a.app != nil {
		a.app.Logf("已生成一对 WireGuard 密钥（公钥 %s）", kp.PublicKey)
	}
	return out
}
