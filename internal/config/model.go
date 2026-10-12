package config

import (
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
	// 强烈建议填上 —— 否则等于在局域网上开了一个匿名代理。
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
