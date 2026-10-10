package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"chainproxy/internal/app"
	"chainproxy/internal/config"
	"chainproxy/internal/engine"
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
	ctx context.Context
	app *app.App

	unsub func()

	mu       sync.Mutex
	scanning bool

	// devOpen 是开发期的自测开关（-open 启动参数），
	// 让界面启动后直接打开某个面板，方便截图检查排版。
	devOpen string
}

// NewAPI 创建 API 实例。devOpen 为空表示正常运行。
func NewAPI(devOpen string) *API { return &API{devOpen: devOpen} }

// startup 由 Wails 在窗口创建后调用。
func (a *API) startup(ctx context.Context) {
	a.ctx = ctx

	inst, err := app.New(config.ConfigPath())
	if err != nil {
		// 配置坏了也得让界面起来，否则用户没有办法修
		wruntime.LogErrorf(ctx, "读取配置失败：%v", err)
	}
	a.app = inst
	if inst == nil {
		wruntime.MessageDialog(ctx, wruntime.MessageDialogOptions{
			Type:    wruntime.ErrorDialog,
			Title:   "ChainProxy 启动失败",
			Message: fmt.Sprintf("无法读取配置：%v\n\n请检查 %s", err, config.ConfigPath()),
		})
		return
	}

	// 把后端事件转发到前端。
	//
	// 注意：这个回调可能在持有 App 内部锁时被调用，所以这里**只做转发**，
	// 绝不能再回头调用 a.app 的任何方法。
	a.unsub = inst.Subscribe(func(e app.Event) {
		wruntime.EventsEmit(ctx, "cp:"+e.Kind, e.Text)
	})

	if err := engine.BuildCheck(); err != nil {
		inst.Logf("⚠️ %v", err)
		inst.Notify("warning", "内嵌引擎能力不完整", err.Error())
	}

	inst.Logf("ChainProxy %s 已启动，配置文件：%s", Version, config.ConfigPath())
	inst.AutoActivate()
}

// shutdown 由 Wails 在退出前调用。
func (a *API) shutdown(ctx context.Context) {
	if a.unsub != nil {
		a.unsub()
	}
	if a.app != nil {
		a.app.Close()
	}
}

// Quit 退出程序（界面上的"退出"按钮）。
func (a *API) Quit() {
	if a.app != nil {
		a.app.Close()
	}
	wruntime.Quit(a.ctx)
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
	EngineOK           bool                  `json:"engineOk"`
	EngineError        string                `json:"engineError"`
	GoVersion          string                `json:"goVersion"`
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
	if err := engine.BuildCheck(); err != nil {
		m.EngineOK = false
		m.EngineError = err.Error()
	} else {
		m.EngineOK = true
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
	return a.app.Settings()
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
	if err := a.app.SetSettings(s); err != nil {
		return err
	}
	a.app.Notify("success", "设置已保存", "")
	return nil
}

// ResetSettings 把设置恢复为默认值（保留节点与前置）。
func (a *API) ResetSettings() (config.Settings, error) {
	if a.app == nil {
		return config.Settings{}, fmt.Errorf("应用尚未就绪")
	}
	d := config.Default().Settings
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
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
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

// RemoveInvalidNodes 移除"测试无效"（TCP 都连不上）的节点。
func (a *API) RemoveInvalidNodes() (int, error) {
	if a.app == nil {
		return 0, fmt.Errorf("应用尚未就绪")
	}
	n, err := a.app.RemoveInvalidNodes()
	if err != nil {
		return n, err
	}
	if n == 0 {
		a.app.Notify("info", "没有测试无效的节点", "所有节点都至少能完成一次 TCP 握手")
	}
	return n, nil
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
// 说明：Wails v2 没有多窗口 API，标题栏只能这样整体切换。
func (a *API) SetWindowTheme(theme string) {
	if a.ctx == nil {
		return
	}
	switch theme {
	case "dark":
		wruntime.WindowSetDarkTheme(a.ctx)
	case "light":
		wruntime.WindowSetLightTheme(a.ctx)
	default:
		wruntime.WindowSetSystemDefaultTheme(a.ctx)
	}
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
