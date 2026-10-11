// Package config 定义 ChainProxy 的配置模型、默认值、持久化与校验。
//
// 配置是纯 JSON，存放在 <dataDir>/config.json，方便手工查看与备份。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// CurrentVersion 是当前配置结构的版本号。
//
//	v1：只有 front + upstreams（socks5 / http / wireguard 三种住宅上游）
//	v2：front + settings + nodes（8 种协议，每节点独立本地端口与前置选择）
const CurrentVersion = 2

// Settings 是全局设置。
//
// 刻意保持精简：只保留主流客户端/主流客户端里我们**切实会用到**的项，
// 不照搬它们臃肿的设置面板。
type Settings struct {
	// DefaultListenHost 是新增节点时默认填写的本地监听地址。
	DefaultListenHost string `json:"defaultListenHost"`
	// AllowLan 为真时监听 0.0.0.0（供局域网其它设备使用）。
	// 默认 false：只监听回环，既安全，又不会每次启动都弹 Windows 防火墙提示。
	AllowLan bool `json:"allowLan"`

	// PortStart 是新增节点时的本地端口起始值。
	PortStart int `json:"portStart"`

	// IdleTimeoutSec 是链路空闲多久后自动断开，0 表示用默认值。
	IdleTimeoutSec int `json:"idleTimeoutSec"`

	// AutoActivateOnLaunch 为真时，程序启动后自动激活上次处于激活状态的节点。
	//
	// 注意：这与"注册开机自启动"完全无关 —— 本程序任何情况下都不注册自启动。
	AutoActivateOnLaunch bool `json:"autoActivateOnLaunch"`

	// ---- 测试 ----
	// TCPTestTimeoutMs 是 TCP 延迟测试的超时。
	TCPTestTimeoutMs int `json:"tcpTestTimeoutMs"`
	// TestConcurrency 是批量测试时的并发数。
	TestConcurrency int `json:"testConcurrency"`
	// RealTestURL 是"真连接延迟"要请求的地址，应返回 204/200 且极小的响应体。
	RealTestURL string `json:"realTestUrl"`
	// SpeedTestURL 是测速用的下载地址。
	SpeedTestURL string `json:"speedTestUrl"`
	// SpeedTestSeconds 是单节点测速的采样时长（秒）。
	SpeedTestSeconds int `json:"speedTestSeconds"`

	// ---- 界面 ----
	// Theme: system / light / dark
	Theme string `json:"theme"`
	// NotificationDurationMs 是通知的停留时长，0 表示不自动关闭。
	NotificationDurationMs int `json:"notificationDurationMs"`
	// LogLines 是界面日志面板保留的最大行数。
	LogLines int `json:"logLines"`

	// ---- 底部栏目 ----
	//
	// 这三项是界面的布局偏好，跟着配置一起走：
	//
	//	BottomPanelView  当前视图：traffic / network / logs / sites / connections
	//	BottomPanelHeight 展开时身体的高度（像素）
	//	BottomPanelOpen  是否展开
	//
	// 空/0 由界面侧补默认值（logs / 200 / false），不在这里塞默认值 ——
	// 免得"用户真的把高度调到 0"和"没设过"混在一起。
	BottomPanelView   string `json:"bottomPanelView"`
	BottomPanelHeight int    `json:"bottomPanelHeight"`
	BottomPanelOpen   bool   `json:"bottomPanelOpen"`

	// ---- 全局快捷键 ----
	//
	// 空字符串表示"用默认值"；要**关掉**某一条请显式写 "none"。
	// （不用空字符串表示关闭，是因为"空"在迁移与老配置里天然等同于"没设过"。）
	//
	// ShortcutToggleWindow 显示/隐藏主窗口。
	ShortcutToggleWindow string `json:"shortcutToggleWindow"`
	// ShortcutToggleAll 激活全部节点；若已有节点在跑则全部停止。
	//
	// 默认**不用** Ctrl+Alt+A —— 那个组合被微信/QQ 的截图占用了（用户实测冲突）。
	ShortcutToggleAll string `json:"shortcutToggleAll"`
}

// Config 是完整的应用配置。
type Config struct {
	Version  int      `json:"version"`
	Front    Front    `json:"front"`
	Settings Settings `json:"settings"`
	Nodes    []Node   `json:"nodes"`

	// Path 记录配置文件自身路径（不参与序列化）。
	Path string `json:"-"`
}

// Default 返回内置默认配置。
func Default() *Config {
	return &Config{
		Version: CurrentVersion,
		Front: Front{
			Type:      FrontTypeSocks5,
			Host:      "127.0.0.1",
			Port:      10808,
			TimeoutMs: 10000,
		},
		Settings: Settings{
			DefaultListenHost:      "127.0.0.1",
			AllowLan:               false,
			PortStart:              10810,
			IdleTimeoutSec:         300,
			AutoActivateOnLaunch:   true,
			TCPTestTimeoutMs:       5000,
			TestConcurrency:        8,
			RealTestURL:            "http://cp.cloudflare.com/generate_204",
			SpeedTestURL:           "https://speed.cloudflare.com/__down?bytes=104857600",
			SpeedTestSeconds:       8,
			Theme:                  "system",
			NotificationDurationMs: 4000,
			LogLines:               500,

			ShortcutToggleWindow: "Ctrl+Alt+C",
			// 不用 Ctrl+Alt+A：微信/QQ 的截图占着它
			ShortcutToggleAll: "Ctrl+Alt+Shift+A",
		},
		Nodes: []Node{},
	}
}

// ------------------------------------------------------------------
// 校验
// ------------------------------------------------------------------

// Issue 是一条配置问题。
type Issue struct {
	Scope   string `json:"scope"` // 节点 ID 或 "前置代理"/"设置"
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (i Issue) Error() string { return i.Scope + ": " + i.Message }

// Validate 做完整的配置校验：类型、端口范围、端口重复、必填字段、
// 前置引用是否存在、前置链是否成环。
//
// 返回全部问题而不是遇到第一个就返回，便于界面一次性展示。
func (c *Config) Validate() []Issue {
	var issues []Issue
	add := func(scope, field, format string, args ...any) {
		issues = append(issues, Issue{Scope: scope, Field: field, Message: fmt.Sprintf(format, args...)})
	}

	// ---- 全局前置代理 ----
	switch c.Front.Type {
	case FrontTypeSocks5, FrontTypeHTTP:
		if strings.TrimSpace(c.Front.Host) == "" {
			add("前置代理", "host", "地址不能为空")
		}
		if err := validPort(c.Front.Port); err != nil {
			add("前置代理", "port", "%v", err)
		}
	case FrontTypeDirect:
		// 直连模式下无需任何字段
	default:
		add("前置代理", "type", "不支持的前置类型 %q（可选 socks5 / http / direct）", c.Front.Type)
	}

	// ---- 节点列表 ----
	seenPort := map[int]string{}
	seenName := map[string]bool{}
	seenID := map[string]bool{}

	for i := range c.Nodes {
		n := &c.Nodes[i]
		scope := n.Name
		if strings.TrimSpace(scope) == "" {
			scope = fmt.Sprintf("节点 #%d", i+1)
		}

		if strings.TrimSpace(n.ID) == "" {
			n.ID = NewID()
		}
		if seenID[n.ID] {
			add(scope, "id", "内部 ID 重复：%s", n.ID)
		}
		seenID[n.ID] = true

		if strings.TrimSpace(n.Name) == "" {
			add(scope, "name", "别名不能为空")
		} else if seenName[n.Name] {
			add(scope, "name", "别名重复：%s", n.Name)
		}
		seenName[n.Name] = true

		n.Protocol = ProtocolOrDefault(n.Protocol)
		if !IsSupportedProtocol(n.Protocol) {
			add(scope, "protocol", "不支持的协议 %q", n.Protocol)
		}

		// 本地监听端口
		if err := validPort(n.ListenPort); err != nil {
			add(scope, "listenPort", "%v", err)
		} else {
			if prev, dup := seenPort[n.ListenPort]; dup {
				add(scope, "listenPort", "本地端口 %d 已被「%s」占用，不能重复", n.ListenPort, prev)
			}
			seenPort[n.ListenPort] = scope
		}
		if strings.TrimSpace(n.ListenHost) == "" {
			n.ListenHost = c.Settings.DefaultListenHost
		}

		// 前置引用
		kind := n.FrontKind()
		if kind != FrontDirect && kind != FrontGlobal {
			if !seenExists(c.Nodes, kind) {
				// 引用了一个还不存在的节点：可能是"后添加的节点还没保存"，
				// 也可能是被前置的节点已被删除。两种都提示，让用户自己决定。
				add(scope, "front", "前置节点不存在（%s），请重新选择", kind)
			}
			if kind == n.ID {
				add(scope, "front", "不能把自己当作前置节点")
			}
		}

		// 远端服务器
		if n.Protocol == ProtocolWireGuard {
			w := n.WireGuard
			if w == nil {
				add(scope, "wireguard", "缺少 WireGuard 配置")
			} else {
				if strings.TrimSpace(w.PrivateKey) == "" {
					add(scope, "privateKey", "私钥不能为空")
				}
				if strings.TrimSpace(w.PeerPublicKey) == "" {
					add(scope, "peerPublicKey", "对端公钥不能为空")
				}
				if strings.TrimSpace(w.Address) == "" {
					add(scope, "address", "隧道地址不能为空，例如 10.7.0.2/32")
				} else if strings.Count(w.Address, "/") != 1 {
					add(scope, "address", "隧道地址格式应为 CIDR，例如 10.7.0.2/32")
				}
			}
			if strings.TrimSpace(n.Server) == "" {
				add(scope, "server", "服务端地址不能为空")
			}
			if err := validPort(n.Port); err != nil {
				add(scope, "port", "%v", err)
			}
			continue
		}

		if strings.TrimSpace(n.Server) == "" {
			add(scope, "server", "服务器地址不能为空")
		}
		if err := validPort(n.Port); err != nil {
			add(scope, "port", "%v", err)
		}

		switch n.Protocol {
		case ProtocolVMess:
			if strings.TrimSpace(n.UUID) == "" {
				add(scope, "uuid", "VMess 必须填写 UUID")
			}
		case ProtocolVLESS:
			if strings.TrimSpace(n.UUID) == "" {
				add(scope, "uuid", "VLESS 必须填写 UUID")
			}
		case ProtocolShadowsocks:
			if strings.TrimSpace(n.Method) == "" {
				add(scope, "method", "Shadowsocks 必须选择加密方法")
			}
			if n.Password == "" {
				add(scope, "password", "Shadowsocks 必须填写密码")
			}
		case ProtocolTrojan:
			if n.Password == "" {
				add(scope, "password", "Trojan 必须填写密码")
			}
			if !n.TLS.Enabled {
				add(scope, "tls", "Trojan 必须启用 TLS")
			}
		case ProtocolHysteria2:
			if n.Password == "" {
				add(scope, "password", "Hysteria2 必须填写密码")
			}
			if !n.TLS.Enabled {
				add(scope, "tls", "Hysteria2 必须启用 TLS")
			}
			if strings.TrimSpace(n.Obfs) != "" && n.ObfsPassword == "" {
				add(scope, "obfsPassword", "启用了混淆就必须填写混淆密码")
			}
		}
	}

	// ---- 前置链成环 ----
	issues = append(issues, c.validateCycles()...)

	// ---- 设置 ----
	if c.Settings.PortStart < 1 || c.Settings.PortStart > 65535 {
		add("设置", "portStart", "端口起始值 %d 非法（应在 1-65535 之间）", c.Settings.PortStart)
	}
	if c.Settings.TestConcurrency < 1 {
		add("设置", "testConcurrency", "测试并发数必须大于 0")
	}

	return issues
}

// validateCycles 检查"节点 A 以前置 B、B 又以前置 A"这类环路。
//
// 环路的后果是激活时无限递归，必须在配置层就拦下来。
func (c *Config) validateCycles() []Issue {
	var issues []Issue
	byID := map[string]Node{}
	for _, n := range c.Nodes {
		byID[n.ID] = n
	}
	for _, start := range c.Nodes {
		seen := map[string]bool{start.ID: true}
		cur := start
		for {
			next := cur.FrontNodeID()
			if next == "" {
				break
			}
			if seen[next] {
				issues = append(issues, Issue{
					Scope:   start.Name,
					Field:   "front",
					Message: "前置链形成了环路，请重新选择前置节点",
				})
				break
			}
			seen[next] = true
			n, ok := byID[next]
			if !ok {
				break
			}
			cur = n
		}
	}
	return issues
}

func seenExists(list []Node, id string) bool {
	for _, n := range list {
		if n.ID == id {
			return true
		}
	}
	return false
}

func validPort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("端口 %d 非法（应在 1-65535 之间）", p)
	}
	return nil
}

// ------------------------------------------------------------------
// 查询辅助
// ------------------------------------------------------------------

// Index 返回指定 ID 的节点下标，找不到返回 -1。
func (c *Config) Index(id string) int {
	for i := range c.Nodes {
		if c.Nodes[i].ID == id {
			return i
		}
	}
	return -1
}

// Find 按 ID 取出节点副本。
func (c *Config) Find(id string) (Node, bool) {
	if i := c.Index(id); i >= 0 {
		return c.Nodes[i], true
	}
	return Node{}, false
}

// ListenHostEffective 返回节点实际应该监听的地址。
//
// AllowLan 打开时统一监听 0.0.0.0；否则强制回环 —— 即使某个节点被手工
// 写成了别的地址，也拉回 127.0.0.1，避免不小心把代理暴露到局域网。
func (c *Config) ListenHostEffective(n Node) string {
	host := strings.TrimSpace(n.ListenHost)
	if c.Settings.AllowLan {
		if host == "" || host == "127.0.0.1" || host == "localhost" {
			return "0.0.0.0"
		}
		return host
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return "127.0.0.1"
	}
	return host
}

// ------------------------------------------------------------------
// 载入 / 保存 / 迁移
// ------------------------------------------------------------------

// Load 从 path 读取配置；文件不存在时返回默认配置。
func Load(path string) (*Config, error) {
	cfg := Default()
	cfg.Path = path

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return cfg, nil
	}

	// 先探测版本：v1 的字段名与 v2 完全不同，必须先迁移再反序列化。
	var probe struct {
		Version int `json:"version"`
	}
	_ = json.Unmarshal(data, &probe)
	if probe.Version < CurrentVersion {
		migrated, err := migrate(data, probe.Version)
		if err != nil {
			return nil, fmt.Errorf("迁移旧配置失败 (%s): %w", path, err)
		}
		data = migrated
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("配置文件格式错误 (%s): %w", path, err)
	}
	cfg.Path = path
	cfg.normalize()
	return cfg, nil
}

// Save 把配置原子写入 path（先写临时文件再改名，避免半截文件）。
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存配置失败: %w", err)
	}
	c.Path = path
	return nil
}

// configV1 是旧版（v1）配置的外形，只用于迁移。
type configV1 struct {
	Version           int          `json:"version"`
	Front             Front        `json:"front"`
	Upstreams         []upstreamV1 `json:"upstreams"`
	DefaultListenHost string       `json:"defaultListenHost"`
	IdleTimeoutSec    int          `json:"idleTimeoutSec"`
	StartOnLaunch     bool         `json:"startOnLaunch"`
	AutoAllocatePort  bool         `json:"autoAllocatePort"`
	TestURL           string       `json:"testUrl"`
}

type upstreamV1 struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Type           string       `json:"type"`
	Enabled        bool         `json:"enabled"`
	ListenHost     string       `json:"listenHost"`
	ListenPort     int          `json:"listenPort"`
	ListenUsername string       `json:"listenUsername"`
	ListenPassword string       `json:"listenPassword"`
	FrontOverride  *Front       `json:"frontOverride,omitempty"`
	ServerHost     string       `json:"serverHost"`
	ServerPort     int          `json:"serverPort"`
	Username       string       `json:"username"`
	Password       string       `json:"password"`
	TimeoutMs      int          `json:"timeoutMs"`
	WireGuard      *wireGuardV1 `json:"wireguard,omitempty"`
}

type wireGuardV1 struct {
	PrivateKey          string   `json:"privateKey"`
	Address             string   `json:"address"`
	PeerPublicKey       string   `json:"peerPublicKey"`
	PresharedKey        string   `json:"presharedKey"`
	EndpointHost        string   `json:"endpointHost"`
	EndpointPort        int      `json:"endpointPort"`
	AllowedIPs          string   `json:"allowedIPs"`
	PersistentKeepalive int      `json:"persistentKeepalive"`
	MTU                 int      `json:"mtu"`
	DNSServers          []string `json:"dnsServers"`
}

// migrate 把 v1 配置转成 v2。
//
// 迁移原则：**尽量保持原有行为不变**。
//
//	v1 里"没有 frontOverride"意味着"走全局前置"，所以迁移后要显式写成
//	Front=global，否则用户的住宅代理会突然变成直连。
//	v1 里"frontOverride 是 direct"才迁移成直连。
func migrate(data []byte, version int) ([]byte, error) {
	var old configV1
	if err := json.Unmarshal(data, &old); err != nil {
		return nil, err
	}
	out := Default()
	out.Front = old.Front
	if out.Front.Type == "" {
		out.Front.Type = FrontTypeSocks5
	}
	if out.Front.TimeoutMs == 0 {
		out.Front.TimeoutMs = 10000
	}
	if old.DefaultListenHost != "" {
		out.Settings.DefaultListenHost = old.DefaultListenHost
	}
	if old.IdleTimeoutSec > 0 {
		out.Settings.IdleTimeoutSec = old.IdleTimeoutSec
	}
	out.Settings.AutoActivateOnLaunch = old.StartOnLaunch
	if old.TestURL != "" {
		out.Settings.RealTestURL = old.TestURL
	}

	for i, u := range old.Upstreams {
		n := Node{
			ID:             u.ID,
			Name:           u.Name,
			Protocol:       ProtocolOrDefault(u.Type),
			ListenHost:     u.ListenHost,
			ListenPort:     u.ListenPort,
			ListenUsername: u.ListenUsername,
			ListenPassword: u.ListenPassword,
			Server:         u.ServerHost,
			Port:           u.ServerPort,
			Username:       u.Username,
			Password:       u.Password,
			Active:         u.Enabled,
		}
		if n.ID == "" {
			n.ID = NewID()
		}
		if n.Name == "" {
			n.Name = fmt.Sprintf("节点%d", i+1)
		}

		// 前置：保留 v1 的语义
		switch {
		case u.FrontOverride == nil:
			n.Front = FrontGlobal
		case u.FrontOverride.Type == FrontTypeDirect:
			n.Front = FrontDirect
		default:
			// v1 允许为单条链路指定一个"临时 socks5/http 前置"。
			// v2 的前置只能指向"本项目内的节点"，无法表达任意临时地址，
			// 因此退回全局前置（语义最接近），并在备注里说明。
			n.Front = FrontGlobal
			n.Remark = strings.TrimSpace(n.Remark + " 原链路专属前置 " + u.FrontOverride.Describe() + "，已回退为全局前置")
		}

		if u.WireGuard != nil {
			n.Protocol = ProtocolWireGuard
			n.Server = u.WireGuard.EndpointHost
			n.Port = u.WireGuard.EndpointPort
			n.WireGuard = &WireGuardConfig{
				PrivateKey:          u.WireGuard.PrivateKey,
				Address:             u.WireGuard.Address,
				PeerPublicKey:       u.WireGuard.PeerPublicKey,
				PresharedKey:        u.WireGuard.PresharedKey,
				AllowedIPs:          u.WireGuard.AllowedIPs,
				PersistentKeepalive: u.WireGuard.PersistentKeepalive,
				MTU:                 u.WireGuard.MTU,
				DNSServers:          u.WireGuard.DNSServers,
			}
		}
		out.Nodes = append(out.Nodes, n)
	}

	out.Version = CurrentVersion
	return json.MarshalIndent(out, "", "  ")
}

// normalize 填补缺失字段，避免旧配置文件导致空值。
func (c *Config) normalize() {
	d := Default()
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.Front.Type == "" {
		c.Front.Type = d.Front.Type
	}
	if c.Front.Host == "" && c.Front.Type != FrontTypeDirect {
		c.Front.Host = d.Front.Host
	}
	if c.Front.Port == 0 {
		c.Front.Port = d.Front.Port
	}
	if c.Front.TimeoutMs == 0 {
		c.Front.TimeoutMs = d.Front.TimeoutMs
	}

	s := &c.Settings
	if s.DefaultListenHost == "" {
		s.DefaultListenHost = d.Settings.DefaultListenHost
	}
	if s.PortStart == 0 {
		s.PortStart = d.Settings.PortStart
	}
	if s.IdleTimeoutSec == 0 {
		s.IdleTimeoutSec = d.Settings.IdleTimeoutSec
	}
	if s.TCPTestTimeoutMs == 0 {
		s.TCPTestTimeoutMs = d.Settings.TCPTestTimeoutMs
	}
	if s.TestConcurrency == 0 {
		s.TestConcurrency = d.Settings.TestConcurrency
	}
	if s.RealTestURL == "" {
		s.RealTestURL = d.Settings.RealTestURL
	}
	if s.SpeedTestURL == "" {
		s.SpeedTestURL = d.Settings.SpeedTestURL
	}
	if s.SpeedTestSeconds == 0 {
		s.SpeedTestSeconds = d.Settings.SpeedTestSeconds
	}
	if s.Theme == "" {
		s.Theme = d.Settings.Theme
	}
	if s.LogLines == 0 {
		s.LogLines = d.Settings.LogLines
	}
	// 快捷键：空 = 用默认值；显式写 "none" = 不注册这一条
	// （不拿空字符串表示"关闭"，否则老配置与"没设过"就没法区分了）
	if s.ShortcutToggleWindow == "" {
		s.ShortcutToggleWindow = d.Settings.ShortcutToggleWindow
	}
	if s.ShortcutToggleAll == "" {
		s.ShortcutToggleAll = d.Settings.ShortcutToggleAll
	}

	if c.Nodes == nil {
		c.Nodes = []Node{}
	}
	for i := range c.Nodes {
		n := &c.Nodes[i]
		n.Protocol = ProtocolOrDefault(n.Protocol)
		if n.ListenHost == "" {
			n.ListenHost = s.DefaultListenHost
		}
		if n.Name == "" {
			n.Name = fmt.Sprintf("节点%d", i+1)
		}
		if n.ID == "" {
			n.ID = NewID()
		}
		if n.Transport.Network == "" {
			n.Transport.Network = "tcp"
		}
		if n.Protocol == ProtocolVMess && n.Security == "" {
			n.Security = "auto"
		}
		if n.Protocol == ProtocolWireGuard && n.WireGuard != nil {
			if n.WireGuard.AllowedIPs == "" {
				n.WireGuard.AllowedIPs = "0.0.0.0/0, ::/0"
			}
			if n.WireGuard.PersistentKeepalive == 0 {
				n.WireGuard.PersistentKeepalive = 25
			}
			if n.WireGuard.MTU == 0 {
				n.WireGuard.MTU = 1420
			}
		}
		if n.CreatedAt.IsZero() {
			n.CreatedAt = nowTime()
		}
		if n.StatsDay == "" {
			n.StatsDay = Today()
		}
	}
}

// ------------------------------------------------------------------
// 路径
// ------------------------------------------------------------------

// DataDir 返回配置与日志的存放目录。
//
// 解析顺序（每一步都刻意留了，因为运行方式实在太多）：
//
//  1. 环境变量 CHAINPROXY_DATA_DIR
//  2. 从可执行文件所在目录**向上找 go.mod**，找到就用 <仓库根>/data
//     —— 这一条同时覆盖了两种情况：
//     wails dev / wails build 产物在 desktop\build\bin\，
//     go run 产物在 .cache\gobuild\<hash>\ 里（都是仓库内的子目录）
//  3. 第 2 步找不到 go.mod（说明 exe 被拷出去了）→ 用 exe 同级的 data\
//  4. 兜底：当前工作目录下的 data\
//
// 之所以要"向上找 go.mod"而不是简单地看 exe 同级目录：
// `go run ./cmd/chainproxy validate` 生成的可执行文件位于 Go 构建缓存里，
// 按 exe 同级目录算会得到一个**缓存目录里的 data\**，于是命令行读到的
// 和界面读到的根本不是同一份配置 —— 这个坑实测踩过一次。
func DataDir() string {
	if v := strings.TrimSpace(os.Getenv("CHAINPROXY_DATA_DIR")); v != "" {
		return v
	}
	if exe, err := os.Executable(); err == nil {
		if root := findRepoRoot(filepath.Dir(exe)); root != "" {
			return filepath.Join(root, "data")
		}
		return filepath.Join(filepath.Dir(exe), "data")
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Join(wd, "data")
	}
	return "data"
}

// findRepoRoot 从 start 开始向上找带 go.mod 的目录，最多找 8 层。
func findRepoRoot(start string) string {
	dir := start
	for i := 0; i < 8; i++ {
		if strings.TrimSpace(dir) == "" || dir == filepath.Dir(dir) {
			return ""
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// ConfigPath 返回默认配置文件路径。
func ConfigPath() string { return filepath.Join(DataDir(), "config.json") }

// LogPath 返回默认日志文件路径。
func LogPath() string { return filepath.Join(DataDir(), "chainproxy.log") }

// Today 返回本地时区下的今天日期字符串。
func Today() string { return nowTime().Format("2006-01-02") }

// ------------------------------------------------------------------
// 解析 / 生成辅助
// ------------------------------------------------------------------

// ParseProxySpec 解析常见的代理地址写法，返回 (类型, 用户名, 密码, 主机, 端口)。
//
// 支持的形式：
//
//	user:pass@host:port
//	host:port
//	socks5://user:pass@host:port
//	http://user:pass@host:port
//	socks5h://host:port
func ParseProxySpec(input string) (typ, user, pass, host string, port int, err error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", "", "", "", 0, errors.New("代理地址为空")
	}

	// 1) 拆掉 scheme
	hasScheme := false
	if idx := strings.Index(s, "://"); idx >= 0 {
		hasScheme = true
		scheme := strings.ToLower(s[:idx])
		s = s[idx+3:]
		switch {
		case strings.HasPrefix(scheme, "socks"):
			typ = ProtocolSOCKS5
		case strings.HasPrefix(scheme, "http"):
			typ = ProtocolHTTP
		default:
			return "", "", "", "", 0, fmt.Errorf("不支持的协议前缀 %q", scheme)
		}
	}

	// 2) 拆掉路径
	if idx := strings.IndexAny(s, "/?#"); idx >= 0 {
		s = s[:idx]
	}

	// 3) 拆掉凭据（取最后一个 '@'，避免密码里含 '@' 时被提前切开）
	if idx := strings.LastIndex(s, "@"); idx >= 0 {
		cred := s[:idx]
		s = s[idx+1:]
		user = cred
		if ci := strings.Index(cred, ":"); ci >= 0 {
			user = cred[:ci]
			pass = cred[ci+1:]
		}
		// URI 形式（带 scheme）允许 %40 这类百分号转义；裸格式则按字面量处理，
		// 免得把真实密码里的 '%' 吃掉。
		if hasScheme {
			if v, err := url.PathUnescape(user); err == nil {
				user = v
			}
			if v, err := url.PathUnescape(pass); err == nil {
				pass = v
			}
		}
	}

	// 4) 拆 host:port，支持 IPv6 的 [::1]:port
	h, p, e := net.SplitHostPort(s)
	if e != nil {
		// 没写端口：允许 "host" 形式配合默认端口
		if typ == "" {
			typ = ProtocolSOCKS5
		}
		return typ, user, pass, strings.Trim(s, "[]"), defaultPortFor(typ), nil
	}
	port, e = strconv.Atoi(p)
	if e != nil {
		return "", "", "", "", 0, fmt.Errorf("端口 %q 不是数字", p)
	}
	if typ == "" {
		typ = ProtocolSOCKS5
	}
	return typ, user, pass, h, port, nil
}

func defaultPortFor(typ string) int {
	if typ == ProtocolHTTP {
		return 80
	}
	return 1080
}

// NewID 生成一个足够随机的短 ID（仅用于界面内部索引，无需密码学强度）。
func NewID() string {
	var b [8]byte
	if _, err := randRead(b[:]); err != nil {
		return strconv.FormatInt(nowNano(), 36)
	}
	return fmt.Sprintf("%x", b[:])
}

// SortNodesByCreated 按创建时间排序（仅供测试与导出使用）。
func SortNodesByCreated(list []Node) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
}
