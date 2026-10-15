// Package config 定义 PicoHop 的配置模型、默认值、持久化与校验。
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
	"sync/atomic"
	"time"
)

// CurrentVersion 是当前配置结构的版本号。
//
//	v1：只有 front + upstreams（socks5 / http / wireguard 三种住宅上游）
//	v2：front + settings + nodes（8 种协议，每节点独立本地端口与前置选择）
const CurrentVersion = 2

// DefaultIdleTimeoutSec 是链路空闲断开的默认秒数（Settings.IdleTimeoutSec 为 0 或负数时用它）。
//
// 【为什么要有这个常量】这个默认值以前只写在 Default() 里，而 internal/proxy
// 又自己写死了一个 300 —— 两处一旦分叉，"用户看到的默认值"和"真正生效的值"
// 就会不一致。默认值只在这里定义一次，Default() / normalize() / Settings.IdleTimeout() 共用。
const DefaultIdleTimeoutSec = 300

// DefaultRealTestURL 是「真连接延迟」的默认测试地址。
//
// 【为什么是它】这是最通行的「真连接测试地址」，主流客户端一般也用它。
// 地址不同，量出来的延迟就不是一个口径：204 空响应
// 既不带响应体、也不额外跳转，最接近"一次纯粹的网络往返"。
//
// 【为什么只在这里定义一次】以前 Default() 里写了一个地址、internal/app 的兜底
// 又写死另一个，于是"设置里显示的默认值"和"空值时真正用的地址"不是同一个东西。
const DefaultRealTestURL = "https://www.google.com/generate_204"

// DefaultRealTestAttempts 是「真连接延迟」的默认取样次数（结果取最小值）。
//
// 【为什么默认 2 次而不是 1 次】为了与主流客户端同口径 —— 它也是循环两次、取成功值
// 里的最小者。而"最小值"明显更小的真正原因是：第 2 次取样**复用了第 1 次已经建好的
// 连接**，量到的是"隧道建立之后一次 HTTP 往返"；第 1 次含建链、协议握手、TLS，
// 是冷连接。只测 1 次的话，同一个节点会比主流客户端高出几倍，用户会以为是我们慢。
const DefaultRealTestAttempts = 2

// MaxRealTestAttempts 是取样次数上限。再多只是拖长测试时间，最小值也不会更小。
const MaxRealTestAttempts = 5

// Settings 是全局设置。
//
// 刻意保持精简：只保留主流客户端里我们**切实会用到**的项，
// 不照搬它们臃肿的设置面板。
type Settings struct {
	// DefaultListenHost 是新增节点时默认填写的本地监听地址。
	DefaultListenHost string `json:"defaultListenHost"`
	// AllowLan 为真时监听 0.0.0.0（供局域网其它设备使用）。
	// 默认 false：只监听回环，既安全，又不会每次启动都弹 Windows 防火墙提示。
	AllowLan bool `json:"allowLan"`

	// PortStart 是新增节点时的本地端口起始值。
	PortStart int `json:"portStart"`

	// IdleTimeoutSec 是链路空闲多久后自动断开，0 表示用默认值
	// （DefaultIdleTimeoutSec）。取用时请走 Settings.IdleTimeout()，
	// 别自己乘 time.Second —— 那样会把 0 当成"永不超时"。
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
	// RealTestAttempts 是"真连接延迟"的取样次数，结果取这些样本里的最小值。
	// 0 表示用默认值（DefaultRealTestAttempts）—— 见该常量的注释：第 2 次起会
	// 复用连接，所以"最小值"对应的是隧道建立之后的往返，与主流客户端同口径。
	RealTestAttempts int `json:"realTestAttempts"`
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

	// IPInfoURL 是"我的 IP 信息"查询用的接口地址；空表示用内置默认值
	// （https://ipinfo.io/json —— 实测直连可用且字段齐全）。
	// 做成可配置是因为这类免费接口随时可能限流或失效，换一个不该要改代码。
	IPInfoURL string `json:"ipInfoUrl"`

	// ---- 全局节点 / 系统代理 ----
	//
	// 与主流客户端一样的两种用法：
	//   分端口模式：每个节点各开一个本地端口，自己把程序指到某个端口；
	//   全局代理模式：把 Windows 系统代理指到**全局节点**的端口，全机走它。
	//
	// GlobalNodeID 只能有一个（空 = 未设置）。
	// SystemProxy 为真时表示"全局代理模式开着" —— 注意它**不**在启动时自动恢复：
	// 上次退出时若指向的端口已经没人监听，自动恢复等于把浏览器指向死端口。
	// 启动时先还原系统代理、把这个开关置回 false，由用户点一下再开（见 docs）。
	GlobalNodeID string `json:"globalNodeId"`
	SystemProxy  bool   `json:"systemProxy"`

	// 【已删除】EngineMode（json engineMode）曾经用来在 sing-box 与 mihomo 之间切内核。
	// sing-box 摘除后只剩 mihomo 一个内核，没有可切的东西 —— 设置里那个开关也随之删除。
	// 老配置里残留的 engineMode 字段会被 JSON 反序列化**忽略**（不需要迁移代码），
	// 这与它历史上"空串 = sing-box"的默认值无关了。理由见 docs/MIHOMO_MIGRATION.md。

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

// IdleTimeout 返回链路空闲多久自动断开（对应界面「网络设置 → 空闲自动断开」）。
//
// 【为什么必须走这个方法，而不是直接用 IdleTimeoutSec】
// 存储形式是"秒 + 0 表示没设过"，而下游中继（internal/proxy.Relay）里
// idle <= 0 的含义是**不设空闲超时**（连接永远不断）。直接透传 0 就把
// "没设过"变成了"永不超时" —— 从"15 秒就断"翻到另一个极端：死连接再也不回收。
// 所以 0 和负数一律按默认值处理（fail-closed），调用方不要再自己去乘 time.Second。
func (s Settings) IdleTimeout() time.Duration {
	sec := s.IdleTimeoutSec
	if sec <= 0 {
		sec = DefaultIdleTimeoutSec
	}
	return time.Duration(sec) * time.Second
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
			IdleTimeoutSec:         DefaultIdleTimeoutSec,
			AutoActivateOnLaunch:   true,
			TCPTestTimeoutMs:       5000,
			TestConcurrency:        8,
			RealTestURL:            DefaultRealTestURL,
			RealTestAttempts:       DefaultRealTestAttempts,
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
		// 【这里是读路径，一行都不许写配置】
		// 所以取**值拷贝**而不是 &c.Nodes[i]：即使后人误加了一句回写，
		// 也伤不到共享的那份配置（写回只改到这个局部副本，不会静默生效）。
		//
		// 为什么较真：App.Validate 持的是 RLock，而 RLock 允许**多个读者同时进入**。
		// 本函数历史上会就地补 ID、补协议、补监听地址 —— 于是"点一下校验"和并发的
		// 读者（节点列表、每秒统计、Save 时的序列化）会对同一片内存一读一写，
		// 是实实在在的 data race。补默认值是**改配置**，只能由显式写入路径做：
		// 载入走 normalize()，新增/更新节点走 App.AddNode / App.UpdateNode
		// （两者都调 Config.NormalizeNode）。别再把这些补丁挪回来。
		n := c.Nodes[i]
		scope := n.Name
		if strings.TrimSpace(scope) == "" {
			scope = fmt.Sprintf("节点 #%d", i+1)
		}

		// 缺 ID 不再偷偷补（那就是写配置），直接报出来。
		// 正常路径下不可能为空：Load -> normalize()、AddNode / UpdateNode -> NormalizeNode
		// 都会补 ID；能走到这里说明配置被手工改坏了，值得让用户看见。
		if strings.TrimSpace(n.ID) == "" {
			add(scope, "id", "内部 ID 缺失（配置可能被手工改过）；重新载入配置会自动补齐")
		} else if seenID[n.ID] {
			add(scope, "id", "内部 ID 重复：%s", n.ID)
		}
		seenID[n.ID] = true

		if strings.TrimSpace(n.Name) == "" {
			add(scope, "name", "别名不能为空")
		} else if seenName[n.Name] {
			add(scope, "name", "别名重复：%s", n.Name)
		}
		seenName[n.Name] = true

		// 空协议按 socks5 处理（与 NormalizeNode / normalize() 同一规则），
		// 但只在**局部变量**上取默认值，不回写 n.Protocol。
		proto := ProtocolOrDefault(n.Protocol)
		if !IsSupportedProtocol(proto) {
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
		// 监听地址：这里以前会把空值补成 Settings.DefaultListenHost —— 那也是写配置，
		// 已挪到写入路径的 NormalizeNode。校验本身不需要它：
		// 下游真正决定监听地址的是 ListenHostEffective，它对空值有 fail-closed 兜底。

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
		if proto == ProtocolWireGuard {
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

		switch proto {
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
			if err := validatePortRanges(n.Ports); err != nil {
				add(scope, "ports", "端口跳跃写法有误：%v", err)
			}
			if err := validateHopInterval(n.HopInterval); err != nil {
				add(scope, "hopInterval", "跳跃间隔写法有误：%v", err)
			}
		}
	}

	// ---- 前置链成环 ----
	issues = append(issues, c.validateCycles()...)

	// ---- 设置 ----
	if c.Settings.PortStart < 1 || c.Settings.PortStart > 65535 {
		add("设置", "portStart", "端口起始值 %d 非法（应在 1-65535 之间）", c.Settings.PortStart)
	}
	// 0 是"用默认值"（见 mergeWithDefaults），不算错；上限之外才是配置错误。
	if c.Settings.RealTestAttempts < 0 || c.Settings.RealTestAttempts > MaxRealTestAttempts {
		add("设置", "realTestAttempts", "真连接延迟的取样次数应在 1-%d 之间", MaxRealTestAttempts)
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
	// 【为什么这里必须"只认回环"，而不是"只拦通配地址"】
	// 这段判断曾经是白名单式的：只把 ""/0.0.0.0/:: 拉回回环，任何
	// 具体地址都原样返回。于是配置里写着 192.168.1.10、10.0.0.5、
	// 或某个解析到局域网 IP 的主机名时，即使 AllowLan 是关的，
	// 监听套接字也会**真的 bind 到那块网卡上** —— 用户以为"只允许本机"
	// （界面上的默认值、以及本函数的注释都是这么写的），实际却给整条
	// 局域网开了一个不需要认证的开放代理。而 listenHost 是能被
	// data/config.json 手工编辑 / 被旧配置残留带进来的，不能假定它干净。
	//
	// 所以判定反过来：只有确认是回环地址才放行，其余（空值、通配地址、
	// 具体 IP、无法解析的主机名）一律拉回 127.0.0.1 —— 即 fail-closed。
	// 别再改回"白名单拉回"的写法。
	if IsLoopbackListenHost(host) {
		return host
	}
	return "127.0.0.1"
}

// IsLoopbackListenHost 判断监听地址是否只对本机可见。
//
// "只对本机可见"的合法形式：127.0.0.0/8 里的任意地址、IPv6 的 ::1，
// 以及名字 localhost。用 net.ParseIP 判定而不是字符串比较，是为了让
// 127.0.0.2 这类同样纯回环的地址不被误伤；空串、0.0.0.0、::、具体
// 网卡地址、普通主机名（无法证明是回环）都返回 false，交由调用方
// 按"非回环"处理。
//
// 【为什么导出】internal/proxy 也要用它：监听地址非回环 + 没有本地端口鉴权
// = 局域网匿名代理（审查 #22），那里必须拒绝启动。这条"什么算回环"的规则
// 只能有一份，否则两边判据漂移就会出现"config 认为安全、proxy 认为危险"
// （或反过来）的缝。别再拷一份过去。
func IsLoopbackListenHost(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		return false
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	// net.JoinHostPort 会给 IPv6 字面量加方括号，这里容忍手工写成的 [::1]。
	if len(h) > 1 && strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
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
	data, err := c.Encode()
	if err != nil {
		return err
	}
	if err := WriteAtomic(path, data); err != nil {
		return err
	}
	c.Path = path
	return nil
}

// Encode 只做序列化，不碰磁盘。
//
// 【为什么要把它单独拆出来】调用方（App.Save）必须在**持锁**的情况下序列化：
// 配置对象会被每秒的统计协程持续改写（UpBytes/DownBytes 等），
// 只复制一个 *Config 指针、然后跑到锁外去 json.Marshal，等于没保护 ——
// 会与统计协程并发读写同一份数据（data race，甚至序列化出撕裂的内容）。
// 拆开之后：**锁内只做 cpu 序列化**（便宜），**锁外做磁盘 I/O**（慢，不该持锁）。
func (c *Config) Encode() ([]byte, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化配置失败: %w", err)
	}
	return append(data, '\n'), nil
}

// atomicSeq 给临时文件名提供**进程内唯一**的序号。
// 【为什么不用时间戳】Windows 的墙钟精度很粗，同一瞬间多个 goroutine 会拿到相同的
// UnixNano，临时名照样撞车（实测：并发保存仍会把文件写坏）。
var atomicSeq atomic.Uint64

// WriteAtomic 把 data 原子地写到 path：先写**唯一名字**的临时文件，再 rename。
//
// 【为什么临时名必须唯一】原先固定用 path+".tmp"：两个并发保存
// （例如 GUI 与 CLI 同时跑、或两个 goroutine）会写同一个临时文件、
// 互相截断，随后 rename 把**损坏的 JSON** 发布成正式配置 —— 最坏情况是节点全丢。
// rename 在同一卷上是原子的，所以只要各自写自己的临时文件，最终文件就一定是
// "某一次完整写入"的内容。
func WriteAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), atomicSeq.Add(1))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存配置失败: %w", err)
	}
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

// NormalizeNode 给单个节点补上缺失的默认值：协议、监听地址、内部 ID。
//
// 【为什么必须是一个"显式"的步骤，而不是塞回 Validate】
// 校验（Validate）是**读路径**：App.Validate 持 RLock 调它，而 RLock 下可以有
// 多个读者并发，任何回写都是 data race（详见 Validate 开头的注释）。
// 所以补默认值只能由**写入路径**显式调用：
//
//	载入配置   config.Load -> normalize() -> NormalizeNode
//	新增节点   App.AddNode
//	更新节点   App.UpdateNode
//
// 写成独立方法而不是在 Validate 里顺手补，是为了让"谁在改配置"一眼可见 ——
// 见到这个函数就说明是写操作。加/改默认值规则时请改这里，两处写入路径自动跟上。
func (c *Config) NormalizeNode(n *Node) {
	if n == nil {
		return
	}
	n.Protocol = ProtocolOrDefault(n.Protocol)
	if n.ListenHost == "" {
		n.ListenHost = c.Settings.DefaultListenHost
	}
	if n.ID == "" {
		n.ID = NewID()
	}
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
	// 0（没设过）与负数（非法值）都拉回默认值：负数如果原样留着，
	// 下游 Relay 会把它当成"不设空闲超时"，等于永远不断开。
	if s.IdleTimeoutSec <= 0 {
		s.IdleTimeoutSec = DefaultIdleTimeoutSec
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
	if s.RealTestAttempts <= 0 {
		s.RealTestAttempts = d.Settings.RealTestAttempts
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
		// 协议 / 监听地址 / ID 三项走 NormalizeNode —— 与 App 的新增、更新路径
		// 共用同一份规则，避免"载入时补一套、校验时又补另一套"再次分叉。
		c.NormalizeNode(n)
		if n.Name == "" {
			n.Name = fmt.Sprintf("节点%d", i+1)
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
//  1. 环境变量 PICOHOP_DATA_DIR
//  2. 从可执行文件所在目录**向上找 go.mod**，找到就用 <仓库根>/data
//     —— 这一条同时覆盖了两种情况：
//     wails dev / wails build 产物在 desktop\build\bin\，
//     go run 产物在 .cache\gobuild\<hash>\ 里（都是仓库内的子目录）
//  3. 第 2 步找不到 go.mod（说明 exe 被拷出去了）→ 用 exe 同级的 data\
//  4. 兜底：当前工作目录下的 data\
//
// 之所以要"向上找 go.mod"而不是简单地看 exe 同级目录：
// `go run ./cmd/picohop validate` 生成的可执行文件位于 Go 构建缓存里，
// 按 exe 同级目录算会得到一个**缓存目录里的 data\**，于是命令行读到的
// 和界面读到的根本不是同一份配置 —— 这个坑实测踩过一次。
//
// 【安全】这个目录里放着全套凭据，权限由 EnsureDataDir 收紧到
// "当前用户 + Administrators + SYSTEM"（见 datadir.go / datadir_windows.go），
// 不要绕过它自己去创建目录。
func DataDir() string {
	if v := strings.TrimSpace(os.Getenv("PICOHOP_DATA_DIR")); v != "" {
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
func LogPath() string { return filepath.Join(DataDir(), "picohop.log") }

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

// maxPortRangeSegments 是端口跳跃集合的段数上限。
//
// 【为什么是 28】这是 mihomo 自己的限制：它解析 "1000-2000,3000" 这类写法时
// （`common/utils/ranges.go`），超过 28 段直接报 "maximum support 28 ranges"。
// 我们提前拦住，让错误出现在"配置校验"这一步（界面上能直接指到那一格），
// 而不是延迟到构造出站时以一句 mihomo 内部报错冒出来。
const maxPortRangeSegments = 28

// validatePortRanges 校验端口跳跃的端口集合写法（例如 "1000-2000,3000"）。
//
// 空串合法 —— 表示不做端口跳跃。逗号与 / 等价（mihomo 会把 ',' 当成 '/'）。
func validatePortRanges(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	segs := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '/' })
	if len(segs) == 0 {
		return errors.New("没有解析出任何端口段")
	}
	if len(segs) > maxPortRangeSegments {
		return fmt.Errorf("段数 %d 超过上限 %d", len(segs), maxPortRangeSegments)
	}
	for _, seg := range segs {
		lo, hi, ok := cutRange(seg)
		if !ok {
			return fmt.Errorf("%q 不是合法的端口或区间", seg)
		}
		start, err := strconv.Atoi(lo)
		if err != nil || start < 1 || start > 65535 {
			return fmt.Errorf("%q 的起始端口不是 1-65535 之间的整数", seg)
		}
		if hi == "" {
			continue
		}
		end, err := strconv.Atoi(hi)
		if err != nil || end < 1 || end > 65535 {
			return fmt.Errorf("%q 的结束端口不是 1-65535 之间的整数", seg)
		}
		if end < start {
			return fmt.Errorf("%q 的起止写反了（应当小在前）", seg)
		}
	}
	return nil
}

// cutRange 把 "1000" / "1000-2000" 切成起止两段（单端口时第二段为空）。
func cutRange(seg string) (lo, hi string, ok bool) {
	head, tail, found := strings.Cut(strings.TrimSpace(seg), "-")
	head = strings.TrimSpace(head)
	if head == "" {
		return "", "", false
	}
	if !found {
		return head, "", true
	}
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return "", "", false
	}
	return head, tail, true
}

// validateHopInterval 校验端口跳跃间隔（"15" 或 "5-10"）。
//
// 【为什么不允许逗号】mihomo 只按 '-' 把它切成 1~2 段（`common/utils/range.go`），
// 写成 "5,10" 会被它当成一个数去解析而报错；这里提前给出人话。
func validateHopInterval(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.ContainsAny(s, ",/") {
		return errors.New("只能写一个值或一个区间（例如 15 或 5-10），不能有逗号")
	}
	lo, hi, ok := cutRange(s)
	if !ok {
		return fmt.Errorf("%q 不是合法的秒数或区间", s)
	}
	start, err := strconv.Atoi(lo)
	if err != nil || start < 1 {
		return fmt.Errorf("%q 不是正整数秒", s)
	}
	if hi == "" {
		return nil
	}
	end, err := strconv.Atoi(hi)
	if err != nil || end < 1 {
		return fmt.Errorf("%q 不是正整数秒区间", s)
	}
	if end < start {
		return fmt.Errorf("%q 的起止写反了（应当小在前）", s)
	}
	return nil
}
