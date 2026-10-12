package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Front 是全局前置代理配置。
//
// 只有「节点把前置选为 global」或者「WireGuard 需要外层 UDP」时才会用到它。
type Front struct {
	// Type: socks5 / http / direct
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	// TimeoutMs: 建立前置连接 + 前置握手 + 请求上游服务器的总超时。
	TimeoutMs int `json:"timeoutMs"`
}

// 前置代理类型。
const (
	FrontTypeSocks5 = "socks5"
	FrontTypeHTTP   = "http"
	FrontTypeDirect = "direct"
)

// Address 返回前置代理的 host:port。
func (f Front) Address() string {
	return net.JoinHostPort(f.Host, strconv.Itoa(f.Port))
}

// Timeout 返回前置代理超时时长（毫秒）。
func (f Front) Timeout() int {
	if f.TimeoutMs > 0 {
		return f.TimeoutMs
	}
	return 10000
}

// Describe 返回一行可读描述。
func (f Front) Describe() string {
	switch f.Type {
	case FrontTypeDirect:
		return "直连（不使用前置代理）"
	case FrontTypeHTTP:
		s := fmt.Sprintf("HTTP %s", f.Address())
		if f.Username != "" {
			s += fmt.Sprintf(" [%s]", f.Username)
		}
		return s
	default:
		s := fmt.Sprintf("SOCKS5 %s", f.Address())
		if f.Username != "" {
			s += fmt.Sprintf(" [%s]", f.Username)
		}
		return s
	}
}

// TLSOptions 是 TLS / Reality 配置。
type TLSOptions struct {
	Enabled       bool     `json:"enabled"`
	ServerName    string   `json:"serverName"` // SNI
	ALPN          []string `json:"alpn,omitempty"`
	AllowInsecure bool     `json:"allowInsecure"`
	// Fingerprint 是 uTLS 指纹；Reality 必须启用 uTLS，所以这里非空。
	Fingerprint string `json:"fingerprint,omitempty"`

	// Reality（VLESS + XTLS-Reality）
	RealityPublicKey string `json:"realityPublicKey,omitempty"`
	RealityShortID   string `json:"realityShortId,omitempty"`
	RealitySpiderX   string `json:"realitySpiderX,omitempty"`
}

// EnabledReality 说明是否配置了 Reality。
func (t TLSOptions) EnabledReality() bool {
	return t.Enabled && strings.TrimSpace(t.RealityPublicKey) != ""
}

// TransportOptions 是传输层配置（vmess / vless / trojan 使用）。
type TransportOptions struct {
	// Network: tcp / ws / grpc / http / httpupgrade
	Network string `json:"network"`
	// Path 用于 ws / http / httpupgrade。
	Path string `json:"path,omitempty"`
	// Host 是 ws / http 的 Host 头（也是 httpupgrade 的 host）。
	Host string `json:"host,omitempty"`
	// ServiceName 用于 grpc。
	ServiceName string `json:"serviceName,omitempty"`
	// HeaderType 用于 tcp 伪装（none / http）。
	HeaderType string `json:"headerType,omitempty"`
}

// Label 返回传输层的一行显示名，用于列表的「传输协议」列。
func (t TransportOptions) Label(tls TLSOptions) string {
	n := strings.TrimSpace(t.Network)
	if n == "" {
		n = "tcp"
	}
	switch n {
	case "tcp":
		if tls.EnabledReality() {
			return "reality"
		}
		if tls.Enabled {
			return "tcp+tls"
		}
		return "tcp"
	default:
		if tls.EnabledReality() {
			return n + "+reality"
		}
		if tls.Enabled {
			return n + "+tls"
		}
		return n
	}
}

// WireGuardConfig 描述一个纯用户态 WireGuard 隧道。
//
// 隧道本身不改动系统路由表；外层 UDP 通过前置代理的 SOCKS5 UDP ASSOCIATE 送出。
// 服务端地址与端口统一放在 Node.Server / Node.Port 里，
// 这样界面的「地址 / 远程端口」两列对所有协议都是同一套取法。
type WireGuardConfig struct {
	PrivateKey    string `json:"privateKey"`
	Address       string `json:"address"` // 隧道内地址，如 "10.7.0.2/32"
	PeerPublicKey string `json:"peerPublicKey"`
	// PresharedKey 是可选的预共享密钥（wg-quick 配置里的 PresharedKey）。
	// 不少服务商和家用路由器默认开启；缺了它会一直握手失败，
	// 而 WireGuard 协议本身**不会告诉你**是密钥不对还是网络不通，所以必须显式支持。
	PresharedKey        string   `json:"presharedKey,omitempty"`
	AllowedIPs          string   `json:"allowedIPs"`
	PersistentKeepalive int      `json:"persistentKeepalive"`
	MTU                 int      `json:"mtu"`
	DNSServers          []string `json:"dnsServers,omitempty"`
}

// Node 是一个「可本地使用的代理出口」—— 也就是界面上的一行。
//
// 它同时承担三件事：
//  1. 描述怎么连到远端服务器（协议 + 地址 + 凭据 + 传输层）
//  2. 描述本地怎么监听（127.0.0.1:<port> 的 mixed 端口）
//  3. 描述它的第一跳是谁（Front：直连 / 全局前置 / 另一个节点）
type Node struct {
	ID       string `json:"id"`
	Name     string `json:"name"`     // 别名
	Protocol string `json:"protocol"` // 见 Protocol* 常量
	Remark   string `json:"remark"`   // 备注

	// ---- 本地监听 ----
	ListenHost string `json:"listenHost"`
	ListenPort int    `json:"listenPort"`
	// ListenUsername/ListenPassword 是本节点本地端口的可选鉴权。
	//
	// 默认留空（只监听回环，不需要鉴权）。一旦打开了设置里的"允许局域网连接"，
	// 就必须填上 —— 否则等于在局域网上开了一个匿名代理（审查 #22：
	// 这种组合现在会被 proxy.NewServer **直接拒绝**，见 HasListenAuth）。
	ListenUsername string `json:"listenUsername,omitempty"`
	ListenPassword string `json:"listenPassword,omitempty"`

	// Front 决定第一跳："" 直连 / "global" 全局前置 / 其它节点的 ID。
	Front string `json:"front"`

	// ---- 远端服务器 ----
	Server string `json:"server"`
	Port   int    `json:"port"`

	// ---- 凭据 ----
	Username string `json:"username,omitempty"` // socks5 / http
	Password string `json:"password,omitempty"` // socks5 / http / ss / trojan / hysteria2

	// ---- VMess / VLESS ----
	UUID     string `json:"uuid,omitempty"`
	AlterID  int    `json:"alterId,omitempty"`
	Security string `json:"security,omitempty"` // VMess 加密方式
	Flow     string `json:"flow,omitempty"`     // VLESS 流控

	// ---- Shadowsocks ----
	Method string `json:"method,omitempty"`

	// ---- Hysteria2 ----
	UpMbps       int    `json:"upMbps,omitempty"`
	DownMbps     int    `json:"downMbps,omitempty"`
	Obfs         string `json:"obfs,omitempty"` // "" 或 "salamander"
	ObfsPassword string `json:"obfsPassword,omitempty"`
	PinSHA256    string `json:"pinSHA256,omitempty"`

	// ---- 传输层 / TLS（vmess / vless / trojan / hysteria2） ----
	Transport TransportOptions `json:"transport"`
	TLS       TLSOptions       `json:"tls"`

	// ---- WireGuard ----
	WireGuard *WireGuardConfig `json:"wireguard,omitempty"`

	// ---- 运行时 / 统计 ----
	// Active 表示"用户希望它处于激活状态"。程序启动时会按它自动激活。
	Active bool `json:"active"`
	// LastUsed 最近一次真正被使用的时间（有流量经过时刷新）。
	LastUsed time.Time `json:"lastUsed,omitempty"`

	// 最近一次测试结果缓存
	// LatencyKind 记住最后一次测试的种类（tcp / real）。
	// 界面上的「延迟」列只显示一个值，靠它决定显示 LatencyMs 还是 RealMs：
	// 用户要的是「测真连接就显示真连接、测 TCP 就显示 TCP」，谁最后测谁覆盖。
	LatencyKind string  `json:"latencyKind,omitempty"`
	LatencyMs   int64   `json:"latencyMs,omitempty"` // TCP 延迟
	RealMs      int64   `json:"realMs,omitempty"`    // 真连接延迟
	SpeedMBps   float64 `json:"speedMBps,omitempty"`

	// LatencyAt / SpeedAt 是上面那些**数值**各自测出来的时间（零值 = 从没测出过），
	// LatencyFailed / SpeedFailed 是"最近一次同类测试有没有测出数值"。
	//
	// 【为什么必须单独记，不能拿数值 0 去猜】（审查 #19）测试失败时以前会顺手把
	// 延迟/速度一并写成 0：既抹掉了上一次真正测出来的数据，界面也从此分不清
	// "没测过"和"测过但失败"。现在数值只在**真的测出来**时才覆盖（见
	// app.updateTest），失败只把这一族的 Failed 置真，于是界面有三态：
	//
	//	没测过     ：数值为 0、时间为零值   → 显示 "—"
	//	有效       ：数值非 0、Failed 为假  → 正常显示（按快慢着色）
	//	上次成功值 ：数值非 0、Failed 为真  → 变灰 + 提示"这是上次成功的结果（xx 前）"
	//
	// Failed 是**按族**记的（延迟一族含 TCP 与真连接）：一次测速失败不该把延迟
	// 也标成过期。反过来，跨族测试（例如测了一次速度）也不该把延迟值标成过期 ——
	// 所以这里记的是"这一族最近一次测试"的结论，而不是全局那一次。
	LatencyAt     time.Time `json:"latencyAt,omitempty"`
	LatencyFailed bool      `json:"latencyFailed,omitempty"`
	SpeedAt       time.Time `json:"speedAt,omitempty"`
	SpeedFailed   bool      `json:"speedFailed,omitempty"`

	// TestFailed / TestedAt / TestedAddr 是「最近一次测试的结论」，只服务于
	// 一件事：判断这个节点算不算「测试无效」（见下面的 KnownUnreachable）。
	//
	// 【为什么必须单独记一个布尔，不能拿 LatencyMs == 0 当判据】
	// 那正是审查 #8 的坑：用「真连接延迟」测时结果写进 RealMs，LatencyMs 恒为 0；
	// 一次成功但耗时不足 1ms 的测试（本机 / 局域网节点）同样是 0。于是
	// 「移除测试无效项」会把**所有**节点都当成无效项删掉。
	TestFailed bool      `json:"testFailed,omitempty"` // 最近一次测试是否明确连不上
	TestedAt   time.Time `json:"testedAt,omitempty"`   // 那次测试的完成时间
	TestedAddr string    `json:"testedAddr,omitempty"` // 那次测试的地址指纹（server:port）

	// ---- 今日统计（双向） ----
	// StatsDay 是统计归属的日期（本地时区 "2006-01-02"）；
	// 跨天时由上层清零，避免把昨天的量算进今天。
	StatsDay  string `json:"statsDay,omitempty"`
	UpBytes   int64  `json:"upBytes,omitempty"`
	DownBytes int64  `json:"downBytes,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
}

// ------------------------------------------------------------------
// Node 上的取值辅助
// ------------------------------------------------------------------

// Address 返回远端服务器的 host:port。
func (n Node) Address() string {
	return net.JoinHostPort(n.Server, strconv.Itoa(n.Port))
}

// TestResultTTL 是「测试结论」的有效期。
//
// 一次失败只能证明"测的那一刻它连不上"，不能证明现在仍然连不上（服务器可能已经
// 恢复、线路可能只是抖了一下）。超过这个时间就请用户重新测一次再决定要不要删 ——
// 删除不可撤销，宁可少删、让用户多测一次。
//
// 取 24 小时是为了不打扰"测完随手清理一遍"的正常用法：那种用法里测试与清理
// 之间只隔几分钟到几小时。
const TestResultTTL = 24 * time.Hour

// TestKey 是"测试结论对应哪个连接目标"的指纹（小写 server + port）。
//
// 只看地址，不看别名/备注/前置：改别名不影响"它连不上"这个事实，
// 但改服务器地址或端口之后，旧结论说的就是另一个目标了，不能再当删除依据。
func (n Node) TestKey() string {
	return net.JoinHostPort(strings.ToLower(strings.TrimSpace(n.Server)), strconv.Itoa(n.Port))
}

// IdentityKey 是"这个节点连的是哪个远端入口"的指纹，用于**导入查重**（审查 #29）。
//
// 只包含"换一个就说明是另一个远端"的字段：协议 + 服务器 + 端口 + 凭据摘要。
//
// 【为什么不带别名 / 备注 / 前置 / 本地端口 / 统计】
// 改别名、改备注都不等于换了节点；本地端口不同只是"同一个远端的第二个入口"，
// 那是「克隆」的语义、不该被查重拦下。把这些算进 key 会让查重形同虚设。
//
// 【为什么凭据只进摘要、不直接拼进 key】
// key 会被当 map 的键在内存里传来传去，将来很可能被顺手打进日志或错误信息里。
// 摘要（SHA-256 取前 8 字节，只看是否相等）保证 key 里永远不含凭据原文 ——
// 明文凭据只留在 Node 自己身上（审查 #23/#25 对"凭据不入日志"的一贯要求）。
func (n Node) IdentityKey() string {
	h := sha256.New()
	// 用 \x00 分隔各字段：否则 ("ab","c") 与 ("a","bc") 会拼出同一个指纹。
	// 只有"换了就必须当成另一个账号"的字段进来：ss 的加密方法、hysteria2 的
	// obfs 密码也算在内；flow / TLS / 传输层只是同一账号的不同用法（审查 #29
	// 给的判据就是 server+port+凭据），刻意不进 key，免得漏判重复。
	parts := []string{n.UUID, n.Username, n.Password, n.Method, n.ObfsPassword}
	if n.WireGuard != nil {
		// WireGuard 的"凭据"是密钥对，不进来的话所有同地址的 wg 节点都会被判成同一个
		parts = append(parts, n.WireGuard.PrivateKey, n.WireGuard.PeerPublicKey)
	}
	for _, p := range parts {
		h.Write([]byte(strings.TrimSpace(p)))
		h.Write([]byte{0})
	}

	return strings.Join([]string{
		strings.ToLower(strings.TrimSpace(n.Protocol)),
		strings.ToLower(strings.TrimSpace(n.Server)),
		strconv.Itoa(n.Port),
		hex.EncodeToString(h.Sum(nil)[:8]),
	}, "|")
}

// KnownUnreachable 判断"最近一次测试明确证明它连不上，且这个结论现在还算数"。
//
// 这是「移除测试无效项」的唯一判据，三个条件缺一不可：
//  1. TestFailed —— **测过**、且明确失败。从没测过的节点在这里直接返回 false，
//     绝不因为"没有结论"就把人删掉；
//  2. 结论没过 TestResultTTL —— 过期的失败不能作为删除依据；
//  3. TestedAddr 与当前地址一致 —— 用户改过服务器地址/端口之后，
//     旧结论描述的是另一个目标（这正是审查 #19 说的"结果不会过期"，
//     这里只把删除判据管住，不改延迟列的显示语义）。
func (n Node) KnownUnreachable(now time.Time) bool {
	if !n.TestFailed || n.TestedAt.IsZero() {
		return false
	}
	if now.Sub(n.TestedAt) > TestResultTTL {
		return false
	}
	return n.TestedAddr != "" && n.TestedAddr == n.TestKey()
}

// FrontKind 归一化前置取值。
func (n Node) FrontKind() string {
	v := strings.TrimSpace(n.Front)
	if v == "" {
		return FrontDirect
	}
	return v
}

// UsesGlobalFront 说明该节点用的是全局前置代理。
func (n Node) UsesGlobalFront() bool { return n.FrontKind() == FrontGlobal }

// HasListenAuth 说明该节点**配齐了**本地端口鉴权（用户名与密码都不为空）。
//
// 【为什么"配齐"要单独判定，而不是看 ListenUsername/ListenPassword 非空就完事】
// proxy.NewServer 里"要不要鉴权"的判据是"两个字段任一非空"（保持原有语义：
// 只填用户名、密码留空也算开了鉴权）。但对**对局域网开放**的监听来说，
// 半套凭据等于没凭据：密码留空时，局域网里任何人只要猜到用户名就能连上
// （SOCKS5 的 RFC 1929 允许零长度密码，HTTP 的 Basic 也一样）。
// 所以"允许局域网"这一侧要求配齐，判据收在这里一处，proxy 与界面共用，
// 免得两边各写一套、日后只改一处（审查 #22）。
//
// 空白字符一律按"没填"处理：用户名写成一个空格挡不住任何人。
func (n Node) HasListenAuth() bool {
	return strings.TrimSpace(n.ListenUsername) != "" && strings.TrimSpace(n.ListenPassword) != ""
}

// FrontNodeID 返回作为前置的那个节点 ID；直连或全局前置时返回空串。
func (n Node) FrontNodeID() string {
	v := n.FrontKind()
	if v == FrontDirect || v == FrontGlobal {
		return ""
	}
	return v
}

// TransportLabel 返回列表「传输协议」列要显示的文本。
func (n Node) TransportLabel() string {
	switch n.Protocol {
	case ProtocolSOCKS5:
		return "socks5"
	case ProtocolHTTP:
		return "http"
	case ProtocolWireGuard:
		return "udp"
	case ProtocolShadowsocks:
		if n.Method != "" {
			return n.Method
		}
		return "shadowsocks"
	case ProtocolHysteria2:
		if strings.TrimSpace(n.Obfs) != "" {
			return "quic+" + n.Obfs
		}
		return "quic"
	default:
		return n.Transport.Label(n.TLS)
	}
}

// Timeout 返回该节点的连接超时（毫秒）。目前统一使用内置默认值，
// 保留方法是为了将来支持每节点超时。
func (n Node) Timeout() int { return 15000 }

// NeedsCredentials 返回该节点是否配置了账号密码。
func (n Node) NeedsCredentials() bool {
	return n.Username != "" || n.Password != ""
}

// Describe 返回一行可读描述，用于日志与「地址」列的补充信息。
func (n Node) Describe() string {
	switch n.Protocol {
	case ProtocolWireGuard:
		return fmt.Sprintf("WireGuard %s", n.Address())
	case ProtocolHysteria2:
		return fmt.Sprintf("Hysteria2 %s", n.Address())
	default:
		s := fmt.Sprintf("%s %s", ProtocolLabel(n.Protocol), n.Address())
		if n.Username != "" {
			s += fmt.Sprintf(" [%s]", n.Username)
		}
		return s
	}
}

// TotalBytes 返回今日双向流量总和。
func (n Node) TotalBytes() int64 { return n.UpBytes + n.DownBytes }
