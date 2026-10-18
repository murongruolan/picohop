// Package subscribe 负责「机场订阅」的解析：把一个订阅链接拿到的东西变成一批
// config.Node，并把机场塞进节点列表里的信息行过滤掉。
//
// 【为什么单独成包】解析的坑极多（两种形态、base64 多种变体、八种协议的字段映射、
// 机场伪信息行、重名、带宽单位换算），而且全程要能单测。混进 app 或 engine 会让
// 那两处继续膨胀；这里只做"字节 → 节点"，不碰配置落盘、不碰本地端口。
//
// 【设计取舍：不 import mihomo 的 config/convert】
// 那两个包确实能解析，但实测会让 go.mod 多出 13 个间接依赖（age/sevenzip/xz/ppmd/
// maxminddb/sing-tun …），而它们多支持的协议（ssr/tuic/anytls/naive）我们引擎本来
// 也跑不了。所以这里自己来：YAML 用一个最小结构体，分享链接复用 internal/share。
package subscribe

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/murongruolan/picohop/internal/config"
	"github.com/murongruolan/picohop/internal/share"
)

// 解析结果里认定的形态。
const (
	FormatClashYAML = "clash-yaml"
	FormatLinks     = "links"
	FormatSIP008    = "sip008"
	FormatHTML      = "html"
)

var (
	// ErrEmpty 表示订阅是空的（0 字节或全空白）。
	ErrEmpty = errors.New("订阅内容是空的")
	// ErrHTML 表示拿到的是网页而不是订阅 —— 通常是链接过期、或者 UA 不被机场认。
	ErrHTML = errors.New("订阅返回的是网页（多半是链接已过期，或该机场不认这个 User-Agent）")
	// ErrUnknownFormat 表示两种形态都试过，都不是。
	ErrUnknownFormat = errors.New("认不出订阅格式（既不是 Clash YAML，也不是分享链接列表）")
)

// Sketch 是一次解析的"形状摘要"，只服务界面预览与排查；不含任何凭据。
type Sketch struct {
	Format     string
	Total      int            // 识别到的条目总数（含被过滤/跳过的）
	Usable     int            // 最终可用的节点数
	Skipped    map[string]int // 协议不支持而跳过的计数，键是协议名
	FakeRows   int            // 被当成"机场信息行"过滤掉的条数
	Duplicated int            // 因与前面节点同身份而丢弃的条数
	Invalid    int            // 字段不全/我们暂不支持其参数而丢弃的条数
	Notes      []string       // 给人看的补充说明（例如 "vless-encryption 暂不支持"）
}

// Result 是解析产出。
type Result struct {
	Nodes  []config.Node
	Sketch Sketch
}

// Parse 把订阅正文解析成节点。
//
// 嗅探顺序（与 mihomo 的 proxy-provider 一致，外加我们自己的容错兜底）：
//
//  1. 先按 YAML 解出顶层 proxies，非空即认为是 Clash YAML（机场的 YAML 里
//     rules / rule-providers / tun / dns 一概不看——那不是我们的架构）；
//  2. 否则按分享链接列表：先原文（明文多行），再容错整体 base64；
//  3. 再试 SIP008 JSON；
//  4. 内容是 HTML 就明确报错（区分"链接过期/UA 不认"与"格式不认识"，
//     这两种问题对用户的修复动作完全不同）。
func Parse(body []byte) (Result, error) {
	text := string(body)
	if strings.TrimSpace(text) == "" {
		return Result{}, ErrEmpty
	}

	if proxies, ok := clashProxies(body); ok {
		sk := Sketch{Format: FormatClashYAML, Total: len(proxies), Skipped: map[string]int{}}
		return finish(mapAll(proxies, &sk), sk), nil
	}
	if nodes, ok := parseLinks(text); ok {
		return finish(nodes, Sketch{Format: FormatLinks, Total: len(nodes), Skipped: map[string]int{}}), nil
	}
	if nodes, ok := parseLinksFromBase64(text); ok {
		return finish(nodes, Sketch{Format: FormatLinks, Total: len(nodes), Skipped: map[string]int{}}), nil
	}
	if nodes, ok := parseSIP008(body); ok {
		return finish(nodes, Sketch{Format: FormatSIP008, Total: len(nodes), Skipped: map[string]int{}}), nil
	}

	low := strings.ToLower(text)
	if strings.Contains(low, "<html") || strings.Contains(low, "<!doctype html") || strings.Contains(low, "<head") {
		return Result{}, ErrHTML
	}
	return Result{}, ErrUnknownFormat
}

// finish 做收尾：过滤机场信息行 → 去重 → 统计。
//
// 【顺序不能反】实测过：机场把"剩余流量：144.86 GB"这种信息行做成**克隆真节点连接信息**
// 的条目（server/port/凭据与某个真节点完全相同）。如果先去重，留下的会是排在前面的
// 那条信息行，真节点反被当成重复项丢掉。所以必须**先按名字过滤信息行，再去重**。
func finish(in []config.Node, sk Sketch) Result {
	if sk.Skipped == nil {
		sk.Skipped = map[string]int{}
	}
	seen := map[string]bool{}
	out := make([]config.Node, 0, len(in))
	for _, n := range in {
		if isFakeRow(n.Name) {
			sk.FakeRows++
			continue
		}
		if n.Server == "" || n.Port < 1 || n.Port > 65535 {
			sk.Invalid++
			continue
		}
		key := n.IdentityKey()
		if seen[key] {
			sk.Duplicated++
			continue
		}
		seen[key] = true
		out = append(out, n)
	}
	sk.Usable = len(out)
	return Result{Nodes: out, Sketch: sk}
}

// fakeRowKeywords 是机场往节点列表里塞"公告类伪节点"时常用的名字特征。
//
// 这份清单照抄业界通行做法（subconverter 的 exclude_remarks 一族），
// 实测两个真实机场都命中："剩余流量：144.86 GB"、"套餐到期：长期有效"。
//
// 【为什么不收"流量"、"机场"这类宽词】真实节点名里出现"大流量节点""机场专线"完全可能，
// 宽词会误杀。宁可漏掉一条伪行（用户看得见、也能手动删），也不要吃掉真节点。
var fakeRowKeywords = []string{
	"剩余流量", "套餐到期", "距离下次", "到期时间", "过期时间", "重置时间",
	"剩余", "过期", "到期", "expire", "expir", "reset",
	"官网", "订阅", "请更新", "节点异常", "更新时间", "客服",
}

func isFakeRow(name string) bool {
	n := strings.TrimSpace(name)
	if n == "" {
		return false
	}
	low := strings.ToLower(n)
	for _, k := range fakeRowKeywords {
		if strings.Contains(low, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- Clash YAML

type clashDoc struct {
	Proxies []map[string]any `yaml:"proxies"`
	// 有的订阅还在用旧字段名（大写开头），一并认掉。
	Proxy []map[string]any `yaml:"Proxy"`
}

func clashProxies(body []byte) ([]map[string]any, bool) {
	var doc clashDoc
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, false
	}
	if len(doc.Proxies) > 0 {
		return doc.Proxies, true
	}
	if len(doc.Proxy) > 0 {
		return doc.Proxy, true
	}
	return nil, false
}

// MapAll 是 parseClashYAML 的"带统计"版本：YAML 与 SIP008 共用。
func mapAll(proxies []map[string]any, sk *Sketch) []config.Node {
	out := make([]config.Node, 0, len(proxies))
	for _, p := range proxies {
		typ := strings.ToLower(asString(p["type"]))
		n, err := mapProxy(p)
		if err != nil {
			if typ != "" {
				sk.Skipped[typ]++
			} else {
				sk.Invalid++
			}
			if errors.Is(err, errUnsupportedParam) {
				addNote(sk, err.Error())
			}
			continue
		}
		out = append(out, n)
	}
	return out
}

func addNote(sk *Sketch, note string) {
	for _, n := range sk.Notes {
		if n == note {
			return
		}
	}
	sk.Notes = append(sk.Notes, note)
}

// ---------------------------------------------------------------- 分享链接

func parseLinks(text string) ([]config.Node, bool) {
	nodes, err := share.DecodeAny(text)
	if err != nil || len(nodes) == 0 {
		return nil, false
	}
	return nodes, true
}

// parseLinksFromBase64 是"满是坑"的那一步：机场常把整份链接列表再整体 base64 一次，
// 而且变体很多（标准/URL-safe/带不带 padding/夹不夹空白）。
//
// 实测：标准 base64 与 URL-safe 无 padding 两种都真出现过，所以四种变体都试。
func parseLinksFromBase64(text string) ([]config.Node, bool) {
	dec, ok := DecodeBase64Loose(text)
	if !ok {
		return nil, false
	}
	nodes, err := share.DecodeAny(string(dec))
	if err != nil || len(nodes) == 0 {
		return nil, false
	}
	return nodes, true
}

// DecodeBase64Loose 尽最大努力把一段文本当整体 base64 解开：
// 去掉空白、补 padding，然后按 标准/URL-safe × 带/不带 padding 依次尝试。
func DecodeBase64Loose(s string) ([]byte, bool) {
	t := strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', ' ', '\t':
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len(t) < 16 {
		return nil, false
	}
	padded := t
	if m := len(padded) % 4; m != 0 {
		padded += strings.Repeat("=", 4-m)
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(padded); err == nil {
			return b, true
		}
	}
	return nil, false
}

// ---------------------------------------------------------------- SIP008 JSON

type sip008 struct {
	Servers []struct {
		Server     string `json:"server"`
		ServerPort int    `json:"server_port"`
		Method     string `json:"method"`
		Password   string `json:"password"`
		Remarks    string `json:"remarks"`
	} `json:"servers"`
}

func parseSIP008(body []byte) ([]config.Node, bool) {
	var doc sip008
	trimmed := strings.TrimSpace(string(body))
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil || len(doc.Servers) == 0 {
		// 也接受裸数组形态
		var arr []struct {
			Server     string `json:"server"`
			ServerPort int    `json:"server_port"`
			Method     string `json:"method"`
			Password   string `json:"password"`
			Remarks    string `json:"remarks"`
		}
		if err2 := json.Unmarshal([]byte(trimmed), &arr); err2 != nil || len(arr) == 0 {
			return nil, false
		}
		out := make([]config.Node, 0, len(arr))
		for _, s := range arr {
			out = append(out, config.Node{
				Protocol: config.ProtocolShadowsocks, Name: s.Remarks,
				Server: s.Server, Port: s.ServerPort, Method: s.Method, Password: s.Password,
			})
		}
		return out, true
	}
	out := make([]config.Node, 0, len(doc.Servers))
	for _, s := range doc.Servers {
		out = append(out, config.Node{
			Protocol: config.ProtocolShadowsocks, Name: s.Remarks,
			Server: s.Server, Port: s.ServerPort, Method: s.Method, Password: s.Password,
		})
	}
	return out, true
}

// ---------------------------------------------------------------- 字段映射

var errUnsupportedParam = errors.New("机场用了我们暂不支持的参数")

// mapProxy 把一份 Clash/mihomo 的 proxy 映射成我们的 Node。
//
// 【为什么用 map[string]any 而不是强类型】机场的 YAML 是野生的：同一个字段可能是
// 字符串也可能是数字（实测 vmess 的 port 有 "8443" 这种），布尔也有 "true" 写法。
// 所以所有取值都过一遍容错转换，缺字段按零值处理。
func mapProxy(m map[string]any) (config.Node, error) {
	typ := strings.ToLower(asString(m["type"]))
	n := config.Node{
		Name:   strings.TrimSpace(asString(m["name"])),
		Server: strings.TrimSpace(asString(m["server"])),
		Port:   asInt(m["port"]),
	}

	// ---- 传输层 ----
	network := strings.ToLower(asString(m["network"]))
	switch network {
	case "", "tcp", "ws", "grpc", "http", "h2", "httpupgrade", "xhttp":
		n.Transport.Network = network
	default:
		// 放过不认识的网络名（mihomo 里没有 httpupgrade 这个 network，但订阅里
		// 有时写 it）——交给引擎层决定，这里原样带上。
		n.Transport.Network = network
	}
	switch network {
	case "ws":
		o := asMap(m["ws-opts"])
		n.Transport.Path = asString(o["path"])
		h := asMap(o["headers"])
		n.Transport.Host = firstNonEmpty(asString(h["Host"]), asString(h["host"]))
	case "grpc":
		o := asMap(m["grpc-opts"])
		n.Transport.ServiceName = asString(o["grpc-service-name"])
	case "h2", "http":
		if o := asMap(m["h2-opts"]); o != nil {
			n.Transport.Path = asString(o["path"])
			n.Transport.Host = firstOfStringOrSlice(o["host"])
		}
		if o := asMap(m["http-opts"]); o != nil {
			n.Transport.Path = firstOfStringOrSlice(o["path"])
			if h := asMap(o["headers"]); h != nil {
				n.Transport.Host = firstOfStringOrSlice(h["Host"])
			}
		}
	case "httpupgrade":
		o := asMap(m["httpupgrade-opts"])
		n.Transport.Path = asString(o["path"])
		n.Transport.Host = asString(o["host"])
	case "xhttp":
		o := asMap(m["xhttp-opts"])
		n.Transport.Path = asString(o["path"])
		n.Transport.Host = asString(o["host"])
	}

	// ---- TLS / Reality ----
	n.TLS.Enabled = asBool(m["tls"])
	n.TLS.ServerName = firstNonEmpty(asString(m["sni"]), asString(m["servername"]), asString(m["peer"]))
	n.TLS.ALPN = asStrings(m["alpn"])
	n.TLS.AllowInsecure = asBool(m["skip-cert-verify"])
	n.TLS.Fingerprint = firstNonEmpty(asString(m["client-fingerprint"]), asString(m["client_fingerprint"]))
	if ro := asMap(m["reality-opts"]); ro != nil {
		n.TLS.Enabled = true
		n.TLS.RealityPublicKey = asString(ro["public-key"])
		n.TLS.RealityShortID = asString(ro["short-id"])
	}

	// ---- 协议特有 ----
	switch typ {
	case "vmess":
		n.Protocol = config.ProtocolVMess
		n.UUID = asString(m["uuid"])
		n.AlterID = asInt(m["alterId"])
		n.Security = asString(m["cipher"])
	case "vless":
		n.Protocol = config.ProtocolVLESS
		n.UUID = asString(m["uuid"])
		n.Flow = asString(m["flow"])
		// 【fail-closed】mihomo 1.19.32 有 VLESS 的 encryption 参数，我们的模型
		// 还没有对应字段：与其静默导入一个连不上的节点，不如明确跳过并计数。
		if enc := strings.TrimSpace(asString(m["encryption"])); enc != "" && enc != "none" {
			return config.Node{}, fmt.Errorf("%w：vless encryption=%s", errUnsupportedParam, enc)
		}
	case "trojan":
		n.Protocol = config.ProtocolTrojan
		n.Password = asString(m["password"])
	case "ss":
		n.Protocol = config.ProtocolShadowsocks
		n.Method = asString(m["cipher"])
		if n.Method == "" {
			n.Method = asString(m["method"])
		}
		n.Password = asString(m["password"])
	case "hysteria2":
		n.Protocol = config.ProtocolHysteria2
		n.Password = asString(m["password"])
		n.Obfs = strings.ToLower(asString(m["obfs"]))
		n.ObfsPassword = asString(m["obfs-password"])
		n.UpMbps = parseMbps(m["up"])
		n.DownMbps = parseMbps(m["down"])
		// 端口跳跃：mihomo 写 "1000-2000,3000"，与我们模型里的写法完全一致，直接搬
		n.Ports = asString(m["ports"])
		// hysteria2 的 fingerprint 是**证书 pin**（不是 uTLS 指纹），别搞混
		n.PinSHA256 = asString(m["fingerprint"])
	case "http":
		n.Protocol = config.ProtocolHTTP
		n.Username = asString(m["username"])
		n.Password = asString(m["password"])
	case "socks5":
		n.Protocol = config.ProtocolSOCKS5
		n.Username = asString(m["username"])
		n.Password = asString(m["password"])
	default:
		return config.Node{}, fmt.Errorf("暂不支持的协议：%s", typ)
	}

	// 地址类字段兜底：reality 的 servername 若空则用 server
	if n.TLS.ServerName == "" && n.TLS.Enabled {
		n.TLS.ServerName = n.Server
	}
	return n, nil
}

// parseMbps 把 mihomo 的 hysteria2 带宽写法换算成 Mbps 整数。
//
// mihomo 里 up/down 是**字符串**，形如 "50 Mbps"、"100 mbps"、"50"、也有直接给数字的。
// 我们的模型存 Mbps int，所以这里取第一个数字再取整（向下取整足够表达"限速大概多少"）。
func parseMbps(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		if s == "" {
			return 0
		}
		num := strings.Builder{}
		for _, r := range s {
			if (r >= '0' && r <= '9') || r == '.' {
				num.WriteRune(r)
			} else if num.Len() > 0 {
				break
			}
		}
		if num.Len() == 0 {
			return 0
		}
		f := 0.0
		if _, err := fmt.Sscanf(num.String(), "%f", &f); err != nil {
			return 0
		}
		return int(f)
	}
	return 0
}

// ---------------------------------------------------------------- 容错取值

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	}
	return fmt.Sprintf("%v", v)
}

func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%d", &n); err == nil {
			return n
		}
	case bool:
		if t {
			return 1
		}
	}
	return 0
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return s == "true" || s == "1" || s == "yes"
	case int:
		return t != 0
	case float64:
		return t != 0
	}
	return false
}

func asStrings(v any) []string {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil
		}
		return []string{s}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s := strings.TrimSpace(asString(e)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	}
	return nil
}

func firstOfStringOrSlice(v any) string {
	if ss := asStrings(v); len(ss) > 0 {
		return ss[0]
	}
	return ""
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// SortedSkipped 把 Skipped 转成"协议×次数"的稳定顺序，供界面显示。
func (s Sketch) SortedSkipped() []string {
	keys := make([]string, 0, len(s.Skipped))
	for k := range s.Skipped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s×%d", k, s.Skipped[k]))
	}
	return out
}

// MaskURL 把订阅链接打码成 "scheme://host/path?***"，用于日志与界面。
//
// 【为什么连路径也保留】用户要靠 host+路径认出是哪条订阅；而**token 几乎总在
// query 或最后一段**，所以 query 一律打掉、最后一段路径若像 token 也打掉。
func MaskURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	cut := len(s)
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		cut = i
	}
	head := s[:cut]
	// 去掉最后一段像 token 的路径（长且无点的段）
	if i := strings.LastIndex(head, "/"); i > 0 {
		last := head[i+1:]
		if len(last) >= 16 && !strings.Contains(last, ".") {
			head = head[:i+1] + "***"
		}
	}
	tail := ""
	if cut < len(s) {
		tail = "?***"
	}
	return head + tail
}
