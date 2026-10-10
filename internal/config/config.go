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
	"strconv"
	"strings"
)

// 支持的前置代理类型。
const (
	FrontTypeSocks5 = "socks5"
	FrontTypeHTTP   = "http"
	FrontTypeDirect = "direct"
)

// 支持的住宅上游类型。
const (
	UpstreamTypeSocks5    = "socks5"
	UpstreamTypeHTTP      = "http"
	UpstreamTypeWireGuard = "wireguard"
)

// 前置代理配置：所有住宅上游共用的"第一跳"。
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

// WireGuardConfig 描述一个纯用户态 WireGuard 隧道。
// 隧道本身不改动系统路由表；外层 UDP 通过前置代理的 SOCKS5 UDP ASSOCIATE 送出。
type WireGuardConfig struct {
	PrivateKey        string `json:"privateKey"`
	Address           string `json:"address"` // 隧道内地址，如 "10.7.0.2/32"
	PeerPublicKey     string `json:"peerPublicKey"`
	EndpointHost      string `json:"endpointHost"`
	EndpointPort      int    `json:"endpointPort"`
	AllowedIPs        string `json:"allowedIPs"` // 默认 "0.0.0.0/0, ::/0"
	PersistentKeepalive int  `json:"persistentKeepalive"`
	MTU               int    `json:"mtu"`
	// DNSServers 供未来"隧道内解析"使用；当前版本域名一律本地解析。
	DNSServers []string `json:"dnsServers"`
}

// Upstream 是一条"住宅出口链路"的配置。
type Upstream struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"` // socks5 / http / wireguard
	Enabled bool   `json:"enabled"`

	// ListenHost/ListenPort 是本程序对外开放的 mixed 端口（同时接受 SOCKS5 与 HTTP）。
	ListenHost string `json:"listenHost"`
	ListenPort int    `json:"listenPort"`

	// 前置代理鉴权（我们自己的端口是否需要用户名密码；默认不需要，因为只监听回环）。
	ListenUsername string `json:"listenUsername"`
	ListenPassword string `json:"listenPassword"`

	// 住宅代理服务器地址与鉴权（socks5 / http 类型使用）。
	ServerHost string `json:"serverHost"`
	ServerPort int    `json:"serverPort"`
	Username   string `json:"username"`
	Password   string `json:"password"`

	// 超时（毫秒），0 表示沿用全局。
	TimeoutMs int `json:"timeoutMs"`

	// WireGuard 类型专用。
	WireGuard *WireGuardConfig `json:"wireguard,omitempty"`
}

// Config 是完整的应用配置。
type Config struct {
	Version int `json:"version"`
	Front   Front `json:"front"`

	Upstreams []Upstream `json:"upstreams"`

	// DefaultListenHost 是新增上游时默认填写的监听地址。
	DefaultListenHost string `json:"defaultListenHost"`

	// IdleTimeoutSec: 链路空闲多久后自动断开，0 表示用默认值。
	IdleTimeoutSec int `json:"idleTimeoutSec"`

	// StartOnLaunch: 启动程序时自动占用端口并开始服务。
	// 注意：这与"注册开机自启动"无关，本程序任何情况下都不注册自启动。
	StartOnLaunch bool `json:"startOnLaunch"`

	// AutoAllocatePort: 添加上游时端口被占用则自动顺延寻找空闲端口。
	AutoAllocatePort bool `json:"autoAllocatePort"`

	// TestURL: 一键测试使用的公网 IP 查询地址。
	TestURL string `json:"testUrl"`

	// Path 记录配置文件自身路径（不参与序列化比较）。
	Path string `json:"-"`
}

// Default 返回内置默认配置。
func Default() *Config {
	return &Config{
		Version: 1,
		Front: Front{
			Type:      FrontTypeSocks5,
			Host:      "127.0.0.1",
			Port:      10808,
			TimeoutMs: 10000,
		},
		Upstreams:        []Upstream{},
		DefaultListenHost: "127.0.0.1",
		IdleTimeoutSec:   300,
		StartOnLaunch:    true,
		AutoAllocatePort: true,
		TestURL:          "https://api.ipify.org?format=json",
	}
}

// FrontAddress 返回前置代理的 host:port。
func (f Front) Address() string {
	return net.JoinHostPort(f.Host, strconv.Itoa(f.Port))
}

// Timeout 返回前置代理超时时长。
func (f Front) Timeout() int {
	if f.TimeoutMs > 0 {
		return f.TimeoutMs
	}
	return 10000
}

// ServerAddress 返回住宅代理服务器的 host:port。
func (u Upstream) ServerAddress() string {
	return net.JoinHostPort(u.ServerHost, strconv.Itoa(u.ServerPort))
}

// Timeout 返回该上游的超时时长（毫秒）。
func (u Upstream) Timeout() int {
	if u.TimeoutMs > 0 {
		return u.TimeoutMs
	}
	return 15000
}

// NeedsCredentials 返回该上游是否配置了住宅代理账号密码。
func (u Upstream) NeedsCredentials() bool {
	return u.Username != "" || u.Password != ""
}

// Describe 返回一行可读描述，用于 GUI 列表与日志。
func (u Upstream) Describe() string {
	switch u.Type {
	case UpstreamTypeWireGuard:
		if u.WireGuard != nil {
			return fmt.Sprintf("WireGuard %s:%d", u.WireGuard.EndpointHost, u.WireGuard.EndpointPort)
		}
		return "WireGuard (未配置)"
	default:
		s := fmt.Sprintf("%s %s", strings.ToUpper(u.Type), u.ServerAddress())
		if u.Username != "" {
			s += fmt.Sprintf(" [%s]", u.Username)
		}
		return s
	}
}

// Issue 是一条配置问题。
type Issue struct {
	Scope   string `json:"scope"` // "front" 或上游名称
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (i Issue) Error() string { return i.Scope + ": " + i.Message }

// Validate 做完整的配置校验：类型、端口范围、端口重复、必填字段、WireGuard 字段。
// 返回全部问题而不是遇到第一个就返回，便于 GUI 一次性展示。
func (c *Config) Validate() []Issue {
	var issues []Issue
	add := func(scope, field, format string, args ...any) {
		issues = append(issues, Issue{Scope: scope, Field: field, Message: fmt.Sprintf(format, args...)})
	}

	// ---- 前置代理 ----
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

	// ---- 上游列表 ----
	seenPort := map[int]string{}
	seenName := map[string]bool{}
	seenID := map[string]bool{}

	for i := range c.Upstreams {
		u := &c.Upstreams[i]
		scope := u.Name
		if strings.TrimSpace(scope) == "" {
			scope = fmt.Sprintf("住宅代理 #%d", i+1)
		}

		if strings.TrimSpace(u.ID) == "" {
			u.ID = NewID()
		}
		if seenID[u.ID] {
			add(scope, "id", "内部 ID 重复：%s", u.ID)
		}
		seenID[u.ID] = true

		if strings.TrimSpace(u.Name) == "" {
			add(scope, "name", "名称不能为空")
		} else if seenName[u.Name] {
			add(scope, "name", "名称重复：%s", u.Name)
		}
		seenName[u.Name] = true

		// 类型
		switch u.Type {
		case UpstreamTypeSocks5, UpstreamTypeHTTP, UpstreamTypeWireGuard:
		default:
			add(scope, "type", "不支持的类型 %q（可选 socks5 / http / wireguard）", u.Type)
		}

		// 监听端口
		if err := validPort(u.ListenPort); err != nil {
			add(scope, "listenPort", "%v", err)
		} else {
			if prev, dup := seenPort[u.ListenPort]; dup {
				add(scope, "listenPort", "端口 %d 已被「%s」占用，不能重复", u.ListenPort, prev)
			}
			seenPort[u.ListenPort] = scope
			// 与前置代理端口冲突时会造成自我拨号死循环，明确拦下
			if c.Front.Type != FrontTypeDirect && c.Front.Port == u.ListenPort {
				add(scope, "listenPort", "端口 %d 与前置代理端口相同，会造成环路，请换一个", u.ListenPort)
			}
		}
		if strings.TrimSpace(u.ListenHost) == "" {
			u.ListenHost = "127.0.0.1"
		}

		// 服务器地址
		if u.Type == UpstreamTypeWireGuard {
			w := u.WireGuard
			if w == nil {
				add(scope, "wireguard", "缺少 WireGuard 配置")
				break
			}
			if strings.TrimSpace(w.PrivateKey) == "" {
				add(scope, "privateKey", "私钥不能为空")
			}
			if strings.TrimSpace(w.PeerPublicKey) == "" {
				add(scope, "peerPublicKey", "对端公钥不能为空")
			}
			if strings.TrimSpace(w.EndpointHost) == "" {
				add(scope, "endpointHost", "服务端地址不能为空")
			}
			if err := validPort(w.EndpointPort); err != nil {
				add(scope, "endpointPort", "%v", err)
			}
			if strings.TrimSpace(w.Address) == "" {
				add(scope, "address", "隧道地址不能为空，例如 10.7.0.2/32")
			} else if strings.Count(w.Address, "/") != 1 {
				add(scope, "address", "隧道地址格式应为 CIDR，例如 10.7.0.2/32")
			}
			break
		}

		if strings.TrimSpace(u.ServerHost) == "" {
			add(scope, "serverHost", "住宅代理地址不能为空")
		}
		if err := validPort(u.ServerPort); err != nil {
			add(scope, "serverPort", "%v", err)
		}
	}

	return issues
}

func validPort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("端口 %d 非法（应在 1-65535 之间）", p)
	}
	return nil
}

// ------------------------------------------------------------------
// 载入 / 保存
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

// normalize 填补缺失字段，避免旧配置文件导致空指针。
func (c *Config) normalize() {
	d := Default()
	if c.Version == 0 {
		c.Version = d.Version
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
	if c.DefaultListenHost == "" {
		c.DefaultListenHost = d.DefaultListenHost
	}
	if c.IdleTimeoutSec == 0 {
		c.IdleTimeoutSec = d.IdleTimeoutSec
	}
	if c.TestURL == "" {
		c.TestURL = d.TestURL
	}
	if c.Upstreams == nil {
		c.Upstreams = []Upstream{}
	}
	for i := range c.Upstreams {
		u := &c.Upstreams[i]
		if u.Type == "" {
			u.Type = UpstreamTypeSocks5
		}
		if u.ListenHost == "" {
			u.ListenHost = c.DefaultListenHost
		}
		if u.Name == "" {
			u.Name = fmt.Sprintf("住宅代理%d", i+1)
		}
		if u.ID == "" {
			u.ID = NewID()
		}
	}
}

// DataDir 返回配置与日志的存放目录。
//
// 优先级：环境变量 CHAINPROXY_DATA_DIR > 可执行文件所在目录下的 data/。
// 这样无论从 scripts\run.ps1 还是双击 dist\chainproxy.exe 启动，数据都留在项目内。
func DataDir() string {
	if v := strings.TrimSpace(os.Getenv("CHAINPROXY_DATA_DIR")); v != "" {
		return v
	}
	exe, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exe), "data")
	}
	return "data"
}

// ConfigPath 返回默认配置文件路径。
func ConfigPath() string { return filepath.Join(DataDir(), "config.json") }

// LogPath 返回默认日志文件路径。
func LogPath() string { return filepath.Join(DataDir(), "chainproxy.log") }

// ------------------------------------------------------------------
// user:pass@host:port 解析
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
			typ = UpstreamTypeSocks5
		case strings.HasPrefix(scheme, "http"):
			typ = UpstreamTypeHTTP
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
			typ = UpstreamTypeSocks5
		}
		return typ, user, pass, strings.Trim(s, "[]"), defaultPortFor(typ), nil
	}
	port, e = strconv.Atoi(p)
	if e != nil {
		return "", "", "", "", 0, fmt.Errorf("端口 %q 不是数字", p)
	}
	if typ == "" {
		typ = UpstreamTypeSocks5
	}
	return typ, user, pass, h, port, nil
}

func defaultPortFor(typ string) int {
	if typ == UpstreamTypeHTTP {
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