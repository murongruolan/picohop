package config

// 支持的节点协议。
//
// 命名与主流客户端/ sing-box / 通用分享链接保持一致，便于互相导入导出。
const (
	ProtocolSOCKS5      = "socks5"
	ProtocolHTTP        = "http"
	ProtocolWireGuard   = "wireguard"
	ProtocolVMess       = "vmess"
	ProtocolVLESS       = "vless"
	ProtocolShadowsocks = "shadowsocks"
	ProtocolTrojan      = "trojan"
	ProtocolHysteria2   = "hysteria2"
)

// 节点「前置」取值的三个来源。
//
// 前置是链路的第一跳：节点要先连上前置，才能去连自己的服务器。
const (
	// FrontDirect 直连：不经过任何前置。
	//
	// 空字符串是刻意的：它是 Node 结构体的零值，
	// 于是"没选前置"天然就等于"直连"，不需要额外初始化。
	FrontDirect = ""
	// FrontGlobal 使用设置里的全局前置代理（通常是本机的主流客户端）。
	FrontGlobal = "global"
	// 其余取值一律当作「本项目内另一个节点的 ID」，
	// 用那个节点自己的本地端口（mixed）作为前置。
)

// ProtocolInfo 描述一种协议的元信息。
//
// 前端直接读它来决定表单该显示哪些字段 —— 这样"支持哪些协议、
// 每种协议需要什么参数"这件事只有一份定义，不会前后端各写一遍。
type ProtocolInfo struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	DefaultPort int    `json:"defaultPort"`

	// 表单字段开关
	ShowUUID      bool `json:"showUuid"`      // vmess / vless
	ShowAlterID   bool `json:"showAlterId"`   // vmess
	ShowSecurity  bool `json:"showSecurity"`  // vmess 加密方式
	ShowFlow      bool `json:"showFlow"`      // vless 流控
	ShowMethod    bool `json:"showMethod"`    // shadowsocks 加密方法
	ShowPassword  bool `json:"showPassword"`  // ss / trojan / hysteria2
	ShowUsername  bool `json:"showUsername"`  // socks5 / http
	ShowTransport bool `json:"showTransport"` // 传输层 network/path/host/serviceName
	ShowTLS       bool `json:"showTls"`
	ShowReality   bool `json:"showReality"`   // TLS 里的 Reality 子项
	ShowHysteria2 bool `json:"showHysteria2"` // 带宽/混淆/PinSHA256
	ShowWireGuard bool `json:"showWireGuard"`

	TLSDefault  bool `json:"tlsDefault"`  // 新建时 TLS 默认勾选
	TLSRequired bool `json:"tlsRequired"` // 强制 TLS（trojan / hysteria2）
	SupportsUDP bool `json:"supportsUdp"`
}

// Protocols 返回全部受支持协议的元信息，顺序即界面下拉框顺序。
func Protocols() []ProtocolInfo {
	return []ProtocolInfo{
		{
			Value: ProtocolSOCKS5, Label: "SOCKS5", DefaultPort: 1080,
			ShowUsername: true, ShowPassword: true, SupportsUDP: true,
		},
		{
			Value: ProtocolHTTP, Label: "HTTP", DefaultPort: 8080,
			ShowUsername: true, ShowPassword: true,
		},
		{
			Value: ProtocolWireGuard, Label: "WireGuard", DefaultPort: 51820,
			ShowWireGuard: true, SupportsUDP: true,
		},
		{
			Value: ProtocolVMess, Label: "VMess", DefaultPort: 443,
			ShowUUID: true, ShowAlterID: true, ShowSecurity: true,
			ShowTransport: true, ShowTLS: true, TLSDefault: true, SupportsUDP: true,
		},
		{
			Value: ProtocolVLESS, Label: "VLESS", DefaultPort: 443,
			ShowUUID: true, ShowFlow: true,
			ShowTransport: true, ShowTLS: true, ShowReality: true, TLSDefault: true, SupportsUDP: true,
		},
		{
			Value: ProtocolShadowsocks, Label: "Shadowsocks", DefaultPort: 8388,
			ShowMethod: true, ShowPassword: true, SupportsUDP: true,
		},
		{
			Value: ProtocolTrojan, Label: "Trojan", DefaultPort: 443,
			ShowPassword: true, ShowTransport: true, ShowTLS: true,
			TLSDefault: true, TLSRequired: true, SupportsUDP: true,
		},
		{
			Value: ProtocolHysteria2, Label: "Hysteria2", DefaultPort: 443,
			ShowPassword: true, ShowHysteria2: true, ShowTLS: true,
			TLSDefault: true, TLSRequired: true, SupportsUDP: true,
		},
	}
}

// ProtocolLabel 返回协议的显示名；未知协议原样返回。
func ProtocolLabel(p string) string {
	for _, info := range Protocols() {
		if info.Value == p {
			return info.Label
		}
	}
	if p == "" {
		return "未知"
	}
	return p
}

// ProtocolOrDefault 把空协议补成 socks5（兼容早期配置）。
func ProtocolOrDefault(p string) string {
	if p == "" {
		return ProtocolSOCKS5
	}
	return p
}

// IsSupportedProtocol 判断协议是否受支持。
func IsSupportedProtocol(p string) bool {
	for _, info := range Protocols() {
		if info.Value == p {
			return true
		}
	}
	return false
}

// UsesSingBox 说明该协议的出口由内嵌 sing-box 引擎实现，
// 而不是本项目自研的 socks5/http/WireGuard 实现。
func UsesSingBox(p string) bool {
	switch p {
	case ProtocolVMess, ProtocolVLESS, ProtocolShadowsocks, ProtocolTrojan, ProtocolHysteria2:
		return true
	default:
		return false
	}
}

// ShadowsocksMethods 是主流且被 sing-box 支持的加密方法，供界面下拉框使用。
func ShadowsocksMethods() []string {
	return []string{
		"aes-128-gcm", "aes-192-gcm", "aes-256-gcm",
		"chacha20-ietf-poly1305", "xchacha20-ietf-poly1305",
		"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm",
		"2022-blake3-chacha20-poly1305",
		"none",
	}
}

// VMessSecurities 是 VMess 的加密方式，供界面下拉框使用。
func VMessSecurities() []string {
	return []string{"auto", "aes-128-gcm", "chacha20-poly1305", "none", "zero"}
}

// TransportNetworks 是传输层可选值，供界面下拉框使用。
func TransportNetworks() []string {
	return []string{"tcp", "ws", "grpc", "http", "httpupgrade"}
}

// UTLSFingerprints 是 uTLS 指纹，供界面下拉框使用（Reality 必须启用）。
func UTLSFingerprints() []string {
	return []string{"chrome", "firefox", "safari", "ios", "android", "edge", "360", "qq", "random", "randomized"}
}
