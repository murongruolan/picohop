// Package share 负责「节点 ↔ 分享链接」的编解码，以及分享二维码的生成。
//
// 为什么单独成包：分享链接是一份对外契约，要和主流客户端/ sing-box /主流客户端
// 等客户端互通。格式细节（base64 的四种变体、参数名拼写、vmess JSON 里
// 野生的大小写和"字符串还是数字"）多到离谱，混进 GUI 或配置包只会让
// 一处改动崩掉一片。这里只做纯函数：不读文件、不碰本地端口、不打日志，
// 可以被单独测到底。
//
// 另一个刻意的设计：编解码只覆盖「描述远端服务器」的字段。
// ListenHost / ListenPort / Front / Active / CreatedAt / StatsDay 这些
// 属于本机状态，导入时必须由调用方自己决定（尤其是本地端口要弹窗让用户设），
// 从别人的链接里带进来只会覆盖用户的本地设置。
package share

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"chainproxy/internal/config"
)

// 包级错误。抽出来是为了让调用方（和测试）能用 errors.Is 判定原因，
// 而不是去匹配错误文本。
var (
	// ErrUnsupportedProtocol 表示该协议没有通用的分享链接格式。
	//
	// socks5 / http / wireguard 属于"本机自用/自建"协议，主流客户端同样不导出它们，
	// 硬造一个格式只会得到一个别的客户端不认识的链接。
	ErrUnsupportedProtocol = errors.New("share: 该协议没有通用的分享链接格式")
	// ErrNoLink 表示一段文本里找不到任何可识别的分享链接。
	ErrNoLink = errors.New("share: 文本里没有可识别的分享链接")
)

// schemeToProtocol 是分享链接 scheme 到 config.Protocol* 的映射。
//
// hy2 是 hysteria2 的官方短别名，两者解析结果完全一致。
var schemeToProtocol = map[string]string{
	"vmess":     config.ProtocolVMess,
	"vless":     config.ProtocolVLESS,
	"trojan":    config.ProtocolTrojan,
	"ss":        config.ProtocolShadowsocks,
	"hysteria2": config.ProtocolHysteria2,
	"hy2":       config.ProtocolHysteria2,
}

// ------------------------------------------------------------------
// 对外 API
// ------------------------------------------------------------------

// Encode 把一个节点编码成分享链接。
//
// Protocol 为 socks5 / http / wireguard（以及任何未知值）时返回
// ErrUnsupportedProtocol：这三种协议没有通用的分享链接格式。
func Encode(n config.Node) (string, error) {
	switch n.Protocol {
	case config.ProtocolVMess:
		return encodeVMess(n)
	case config.ProtocolVLESS:
		return encodeVLESS(n)
	case config.ProtocolTrojan:
		return encodeTrojan(n)
	case config.ProtocolShadowsocks:
		return encodeShadowsocks(n)
	case config.ProtocolHysteria2:
		return encodeHysteria2(n)
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedProtocol, config.ProtocolLabel(n.Protocol))
	}
}

// Decode 解析单条分享链接。前后空白、包裹引号、BOM 都会自动去掉。
//
// 如果拿到的是「整段剪贴板文本」（可能带前缀说明、多行、一行多条），
// 应该用 DecodeAny —— 它会自己从文本里挑出链接。
func Decode(link string) (config.Node, error) {
	// 先削 BOM 再去空白：BOM（U+FEFF）不是空白字符，
	// 顺序反了的话 "\ufeff  \"ss://..." 前面的空格会挡住引号处理。
	// 从 Windows 记事本/部分聊天软件复制出来的文本经常带 BOM。
	raw := strings.TrimPrefix(link, "\ufeff")
	// 引号和尾随标点可能叠加（"链接"，这种形态很常见），所以反复削到不再变化。
	for i := 0; i < 4; i++ {
		trimmed := strings.TrimPrefix(strings.TrimSpace(raw), "\ufeff")
		trimmed = strings.Trim(trimmed, "\"'`<>")
		trimmed = strings.TrimSpace(strings.TrimRight(trimmed, trailingJunk))
		if trimmed == raw {
			break
		}
		raw = trimmed
	}
	if raw == "" {
		return config.Node{}, errors.New("share: 分享链接为空")
	}

	scheme, rest, ok := splitScheme(raw)
	if !ok {
		// 【审查 #23】不要在这里回显 raw。本包的错误会被
		// desktop/api_import.go 原样塞进 ImportResult.Message 并显示在通知弹窗里，
		// 而进到这里的典型输入正是"复制时被截断的链接"（少了 ://）——
		// 前 60 字节里往往就是密码 / UUID。只报原因和文本长度：
		// 用户仍能判断"是不是没复制全"，凭据却不会出现在屏幕（截图、录屏同样泄露）。
		return config.Node{}, fmt.Errorf("share: 不是分享链接（缺少 scheme://，读到 %d 字节）", len(raw))
	}
	if _, ok := schemeToProtocol[scheme]; !ok {
		return config.Node{}, fmt.Errorf("share: 不支持的分享链接类型 %s://", scheme)
	}
	parts := parseRawURI(rest)

	switch schemeToProtocol[scheme] {
	case config.ProtocolVMess:
		return decodeVMess(parts)
	case config.ProtocolVLESS:
		return decodeVLESS(parts)
	case config.ProtocolTrojan:
		return decodeTrojan(parts)
	case config.ProtocolShadowsocks:
		return decodeShadowsocks(parts)
	default:
		return decodeHysteria2(parts)
	}
}

// DecodeAny 解析一段可能包含多条链接的文本（例如整个剪贴板内容，
// 可能带前后缀、多行、每行一条）。解析不出任何节点时返回 ErrNoLink。
//
// 其中个别链接解析失败不会影响其它链接：剪贴板里混着失效链接是常态，
// 为了它们把能用的节点一起丢掉才是最糟的行为。
// 返回顺序＝链接在文本里出现的顺序，不做去重（是否去重由调用方决定）。
func DecodeAny(text string) ([]config.Node, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("%w: 文本为空", ErrNoLink)
	}
	var (
		nodes    []config.Node
		firstErr error
	)
	for _, link := range extractLinks(text) {
		n, err := Decode(link)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		nodes = append(nodes, n)
	}
	if len(nodes) == 0 {
		if firstErr != nil {
			return nil, fmt.Errorf("%w（首条错误：%v）", ErrNoLink, firstErr)
		}
		return nil, ErrNoLink
	}
	return nodes, nil
}

// Detect 判断一段文本里第一条链接的协议类型，返回 (协议, 是否识别成功)。
// 协议取值用 config.Protocol* 常量。
//
// 判定标准是"真的能解析出节点"，而不是"看起来像"：
// 只有前后缀文本时返回 ("", false)，不会给出一个解析不了的协议名。
func Detect(text string) (string, bool) {
	for _, link := range extractLinks(text) {
		n, err := Decode(link)
		if err == nil {
			return n.Protocol, true
		}
	}
	return "", false
}

// ------------------------------------------------------------------
// base64 / 转义 / JSON 标量等基础工具
// ------------------------------------------------------------------

// decodeBase64Loose 是统一的 base64 解码入口。
//
// 必须同时容忍：标准表与 URL-safe 表 × 带 padding 与不带 padding。
// 实践里 vmess 和 ss 的链接四种组合都出现过（主流客户端用标准表+padding，
// 不少机场面板用 URL-safe 且去掉 padding，还有的被 URL 编码后再粘出来），
// 只认一种就会挑食。顺带把折行产生的空白去掉——聊天窗口复制经常被折行。
func decodeBase64Loose(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, " \t\r\n") {
		s = strings.Join(strings.Fields(s), "")
	}
	if s == "" {
		return nil, errors.New("share: Base64 内容为空")
	}
	encodings := []*base64.Encoding{
		base64.RawURLEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.StdEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		} else {
			lastErr = err
		}
	}
	return nil, fmt.Errorf("share: Base64 解码失败: %w", lastErr)
}

// decodeBase64Body 先按原样解，失败且内容里有 '%' 时再做一次百分号解码。
//
// 原因：有些客户端会把整段 base64 再 URL 编码一次（'=' 变 %3D），
// 而 base64 自身含 '+'，所以不能无脑先做 QueryUnescape（'+' 会变成空格）。
// 只有原样解失败时才退到百分号解码。
func decodeBase64Body(s string) ([]byte, error) {
	data, err := decodeBase64Loose(s)
	if err == nil {
		return data, nil
	}
	if strings.Contains(s, "%") {
		if data, err2 := decodeBase64Loose(unescapePath(s)); err2 == nil {
			return data, nil
		}
	}
	return nil, err
}

// escapeUserinfo 按 RFC 3986 的 userinfo 规则做百分号编码：
// 除 unreserved（A-Za-z0-9-._~）外一律编码。
//
// 不能直接复用 url.PathEscape：它不编码 '@' 和 ':'，而 '@' 正是 userinfo
// 与 host 的分隔符——密码里带 @ 的 trojan 链接会被对端拆成两个 @。
func escapeUserinfo(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		// 逐个 UTF-8 字节编码，非 ASCII 字符自然变成多段 %XX。
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// escapeFragment 编码 '#' 后面的别名。
//
// url.QueryEscape 把空格编成 '+'，但 fragment 不是表单，客户端（含本包）
// 按百分号解码，'+' 会原样留下——别名里的空格就变成了加号。
// 所以把 '+' 换回 %20。
func escapeFragment(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// unescapePath 做百分号解码，遇到非法转义（例如密码里裸着的 '%' 或中文）
// 就原样返回。用户手写的凭据里什么字符都有，不能因为解码失败就判死整条链接。
//
// 用 PathUnescape 而不是 QueryUnescape：userinfo 里的 '+' 是字面加号，
// 不是空格。
func unescapePath(s string) string {
	if v, err := url.PathUnescape(s); err == nil {
		return v
	}
	return s
}

// unescapeQuery 解码查询参数值：这里 '+' 按惯例是空格。
func unescapeQuery(s string) string {
	if v, err := url.QueryUnescape(s); err == nil {
		return v
	}
	return s
}

// parseQuery 解析查询串，key 统一转小写（参数名大小写不敏感）。
//
// 不用 url.ParseQuery：它对非法百分号转义会让整串报错，
// 而"多了一个 &foo=%zz 参数整条链接就导不进来"是绝对不能接受的。
// 这里能解就解，解不了保留原样，未知参数留给调用方忽略。
func parseQuery(raw string) url.Values {
	q := url.Values{}
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		q.Add(strings.ToLower(unescapeQuery(k)), unescapeQuery(v))
	}
	return q
}

// firstParam 依次取第一个非空参数值，用于同一含义的多种参数名。
func firstParam(q url.Values, keys ...string) string {
	for _, k := range keys {
		if v := q.Get(k); v != "" {
			return v
		}
	}
	return ""
}

// boolValue 解析分享链接里的布尔值。各家写法不一：1 / true / yes 都见过。
func boolValue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// atoiLoose 宽松取整：解析失败按 0 处理（调用方一般再判断 >0）。
func atoiLoose(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}

// normalizeNetwork 归一化传输层名称，空值按 tcp 处理。
//
// "h2" 与 "http" 在 Xray 里指的是同一件事（HTTP/2），
// 两种写法都有人生成，内部统一成 config 里使用的 http。
func normalizeNetwork(s string) string {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case "", "tcp", "raw":
		// raw 是 Xray 新版本对 tcp 的改名，本项目内部仍叫 tcp。
		return "tcp"
	case "h2", "h2c":
		return "http"
	default:
		return v
	}
}

// cleanHeaderType 归一化 tcp 伪装的 headerType。
// "none" 与空串语义相同，统一成空串，免得配置里多出一个没意义的 none。
func cleanHeaderType(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "none" {
		return ""
	}
	return s
}

// splitALPN 拆 ALPN 列表。链接里用逗号分隔，且常常被 URL 编码成 %2C，
// 到这里时百分号已经解开了，所以只需要处理逗号。
func splitALPN(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := make([]string, 0, 2)
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// truncate 截断字符串，供测试失败信息等**诊断输出**使用。
//
// 【审查 #23】不要再把它用于 error 里回显链接 / userinfo：截断不是脱敏，
// 前 40～60 字节通常正好是 "scheme://密码" 或 base64 之后的凭据，
// 而本包的 error 会一路走到界面通知弹窗（见 desktop/api_import.go）。
// 错误信息只写原因和位置（字节数），不写内容。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ------------------------------------------------------------------
// 链接拆分
// ------------------------------------------------------------------

// uriParts 是一条分享链接拆开后的各部分。userinfo 保留**未解码**的原文，
// 因为 ss 的 userinfo 是 base64 而不是百分号编码，必须先判断形态再解码。
type uriParts struct {
	body     string // 去掉 ?query 与 #fragment 之后剩下的部分
	fragment string // 已解码的别名
	userinfo string // 原始 userinfo（未解码）
	host     string
	port     int
	query    url.Values
}

// splitScheme 拆出 scheme 和它后面的内容，scheme 大小写不敏感
// （见过 VMESS://、Vless:// 这类写法）。
func splitScheme(s string) (scheme, rest string, ok bool) {
	i := strings.Index(s, "://")
	if i <= 0 {
		return "", "", false
	}
	return strings.ToLower(strings.TrimSpace(s[:i])), s[i+3:], true
}

// parseRawURI 手工拆分 "userinfo@host:port?query#fragment"。
//
// 不用 net/url.Parse 的原因：ss 的旧格式、vmess 的 base64 体、
// 以及用户手抄时留下的非法百分号转义，都会让它直接报错，
// 而我们对这些情况必须是"尽力解析"。
func parseRawURI(rest string) uriParts {
	var p uriParts
	// 先切 fragment：'#' 不在 base64 / userinfo 的合法字符集里，
	// 所以第一个 '#' 就是分隔符。
	if i := strings.Index(rest, "#"); i >= 0 {
		p.fragment = unescapeQuery(rest[i+1:])
		rest = rest[:i]
	}
	rawQuery := ""
	if i := strings.Index(rest, "?"); i >= 0 {
		rawQuery = rest[i+1:]
		rest = rest[:i]
	}
	p.query = parseQuery(rawQuery)
	p.body = rest
	// 用 LastIndex：密码里可能有未转义的 '@'，而 host:port 部分不含 '@'，
	// 最后一个才是真正的分隔符。
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		p.userinfo = rest[:i]
		rest = rest[i+1:]
	}
	p.host, p.port = splitHostPort(rest)
	return p
}

// splitHostPort 拆 host:port，兼容 IPv6 字面量（[::1]:443）与只给主机名的写法。
func splitHostPort(s string) (string, int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, atoiLoose(p)
	}
	// 没有端口（或只有 "[::1]"）：去掉方括号，端口留 0 交给调用方判定。
	return strings.Trim(s, "[]"), 0
}

// extractLinks 从任意文本里按出现顺序挑出所有分享链接。
//
// 不用"按行 split"：剪贴板里经常是一条链接后面跟着一堆说明文字，
// 或者几行链接被合并成一行（空格/逗号分隔），按行处理都会漏。
// 这里以 "://" 为锚点，向前取 scheme、向后取到空白为止，
// 天然同时覆盖多行、前后缀、一行多条三种情况。
func extractLinks(text string) []string {
	var out []string
	for from := 0; from < len(text); {
		i := strings.Index(text[from:], "://")
		if i < 0 {
			break
		}
		pos := from + i
		// 向前吃掉整个 scheme 词，这样 "vless://" 里的 "ss://"
		// 不会被当成一条 ss 链接（它是同一个词的尾巴）。
		start := pos
		for start > 0 && isSchemeByte(text[start-1]) {
			start--
		}
		word := strings.ToLower(text[start:pos])
		scheme := word
		if _, ok := schemeToProtocol[word]; !ok {
			// 整段不是已知 scheme，再按后缀匹配一次：
			// 编号列表 "2.vless://..." 会把 "2.vless" 整段吃进来，
			// 但它的后缀正是 "vless"。误判的代价只是多试一次解码
			// （解不出来就跳过），漏判的代价是丢一个节点，所以宁可松一点。
			scheme = ""
			for s := range schemeToProtocol {
				if strings.HasSuffix(word, s) && len(s) > len(scheme) {
					scheme = s
				}
			}
			start = pos - len(scheme)
		}
		if scheme == "" {
			// 不是我们认识的 scheme（例如 https://），继续往后找。
			from = pos + 3
			continue
		}
		token := linkToken(text[pos+3:])
		out = append(out, text[start:pos+3]+token)
		from = pos + 3 + len(token)
	}
	return out
}

// isSchemeByte 判断字节是否可能是 scheme 的一部分
// （RFC 3986：ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )）。
func isSchemeByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '+' || c == '-' || c == '.'
}

// trailingJunk 是链接末尾粘上来的标点（中英文都要处理：
// 从微信/QQ/文档里复制常带全角标点）。
const trailingJunk = ",;)]}，；）】》"

// linkToken 从 "://" 之后截出链接正文：遇到空白、引号、尖括号即结束，
// 再去掉右侧粘上来的标点。'=' 等 base64 填充字符不能算标点，必须保留。
//
// 已知取舍：如果有人手写的链接里别名带**未编码的空格**（"#我的 节点"），
// 这里会把别名截成 "我的"。正规生成器（含本包）都会编成 %20，
// 而"把后面那段中文说明文字一起吃进别名"要糟糕得多，所以取截断。
func linkToken(s string) string {
	end := len(s)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c == '"' || c == '\'' || c == '<' || c == '>' || c == '`' {
			end = i
			break
		}
	}
	return strings.TrimRight(s[:end], trailingJunk)
}

// ------------------------------------------------------------------
// 公共参数映射
// ------------------------------------------------------------------

// security 参数的三种归一化取值；空串表示"链接里没写"。
const (
	secReality = "reality"
	secTLS     = "tls"
	secNone    = "none"
)

// securityValue 归一化 security 参数。
//
// 空串只表示"没写"，默认值交给调用方决定：vless 没写就是明文，
// trojan 没写按惯例仍是 TLS，两者不能共用一个默认值。
func securityValue(q url.Values) string {
	switch v := strings.ToLower(strings.TrimSpace(q.Get("security"))); v {
	case "reality":
		return secReality
	case "tls", "xtls":
		return secTLS
	case "none", "false", "0":
		return secNone
	default:
		return ""
	}
}

// applyParams 把查询参数映射到 Node。
//
// 未知参数一律忽略（只是不读它，不报错）：主流客户端的链接里带着大量
// 客户端私有参数（mport、remarks、spx、encryption、pqv...），
// 只要多一个参数就失败，用户手里九成的链接都导不进来。
// tlsDefault 是"链接里没写 security"时的默认值。
func applyParams(n *config.Node, q url.Values, tlsDefault bool) {
	if v := q.Get("type"); v != "" {
		n.Transport.Network = normalizeNetwork(v)
	}
	if v := q.Get("headertype"); v != "" {
		n.Transport.HeaderType = cleanHeaderType(v)
	}
	if v := q.Get("path"); v != "" {
		n.Transport.Path = v
	}
	if v := q.Get("host"); v != "" {
		n.Transport.Host = v
	}
	if v := q.Get("servicename"); v != "" {
		n.Transport.ServiceName = v
	}
	if v := q.Get("flow"); v != "" {
		n.Flow = v
	}

	switch securityValue(q) {
	case secReality, secTLS:
		n.TLS.Enabled = true
	case secNone:
		n.TLS.Enabled = false
	default:
		n.TLS.Enabled = tlsDefault
	}

	if v := q.Get("sni"); v != "" {
		n.TLS.ServerName = v
	} else if v := q.Get("peer"); v != "" {
		// trojan-go 等客户端用 peer 表示 SNI。
		n.TLS.ServerName = v
	}
	// 同名参数出现多次（alpn=h2&alpn=http/1.1）时全部保留。
	if v := strings.Join(q["alpn"], ","); v != "" {
		n.TLS.ALPN = splitALPN(v)
	}
	if boolValue(q.Get("allowinsecure")) || boolValue(q.Get("insecure")) {
		n.TLS.AllowInsecure = true
	}
	if v := q.Get("fp"); v != "" {
		n.TLS.Fingerprint = v
	}

	n.TLS.RealityPublicKey = firstParam(q, "pbk", "publickey")
	n.TLS.RealityShortID = firstParam(q, "sid", "shortid")
	n.TLS.RealitySpiderX = firstParam(q, "spx", "spiderx")
	// 给了公钥就是 Reality，不管 security 写没写（有的链接只写 pbk）。
	if n.TLS.RealityPublicKey != "" {
		n.TLS.Enabled = true
	}

	n.Obfs = q.Get("obfs")
	n.ObfsPassword = firstParam(q, "obfs-password", "obfspassword")
	if v := atoiLoose(firstParam(q, "up", "upmbps")); v > 0 {
		n.UpMbps = v
	}
	if v := atoiLoose(firstParam(q, "down", "downmbps")); v > 0 {
		n.DownMbps = v
	}
	n.PinSHA256 = q.Get("pinsha256")
}

// appendTransportParam 写出传输层参数。
//
// tcp 是默认值，省略不写：分享链接最终要变成二维码，
// 内容越短二维码版本越低、越容易扫出来。
func appendTransportParam(q url.Values, t config.TransportOptions) {
	if net := normalizeNetwork(t.Network); net != "tcp" {
		q.Set("type", net)
	}
	if t.HeaderType != "" {
		q.Set("headerType", t.HeaderType)
	}
	if t.Path != "" {
		q.Set("path", t.Path)
	}
	if t.Host != "" {
		q.Set("host", t.Host)
	}
	if t.ServiceName != "" {
		q.Set("serviceName", t.ServiceName)
	}
}

// appendTLSParam 写出 TLS / Reality 参数。
func appendTLSParam(q url.Values, tls config.TLSOptions) {
	if tls.ServerName != "" {
		q.Set("sni", tls.ServerName)
	}
	if len(tls.ALPN) > 0 {
		q.Set("alpn", strings.Join(tls.ALPN, ","))
	}
	if tls.AllowInsecure {
		q.Set("allowInsecure", "1")
	}
	if tls.Fingerprint != "" {
		q.Set("fp", tls.Fingerprint)
	}
	if isReality(tls) {
		q.Set("pbk", tls.RealityPublicKey)
		if tls.RealityShortID != "" {
			q.Set("sid", tls.RealityShortID)
		}
		if tls.RealitySpiderX != "" {
			q.Set("spx", tls.RealitySpiderX)
		}
	}
}

// setSecurityParam 写出 security 参数。
//
// vless / trojan 都显式写出 none|tls|reality：留空时各家默认值不一致
// （有的当明文 tcp，有的当 tls），写清楚才没有歧义。
func setSecurityParam(q url.Values, tls config.TLSOptions) {
	switch {
	case isReality(tls):
		q.Set("security", secReality)
	case tls.Enabled:
		q.Set("security", secTLS)
	default:
		q.Set("security", secNone)
	}
}

// isReality 判定是否是 Reality：必须已启用 TLS 且给了公钥，
// 与 config.TLSOptions.EnabledReality 保持同一口径。
func isReality(tls config.TLSOptions) bool { return tls.EnabledReality() }

// ------------------------------------------------------------------
// 生成链接
// ------------------------------------------------------------------

// checkRemote 校验「远端服务器」三要素。
//
// 分享链接脱离本机配置独立存在，地址/端口写错时对端只会得到一个
// 永远连不上的节点，所以在导出这一侧就挡掉，比导入后才发现要好。
func checkRemote(n config.Node) error {
	if strings.TrimSpace(n.Server) == "" {
		return errors.New("share: 服务器地址为空，无法生成分享链接")
	}
	if n.Port <= 0 || n.Port > 65535 {
		return fmt.Errorf("share: 远程端口 %d 不合法", n.Port)
	}
	return nil
}

// buildLink 拼出 "scheme://userinfo@host:port?query#fragment"。
// 主机部分用 net.JoinHostPort，IPv6 字面量会自动加方括号。
func buildLink(scheme, userinfo string, n config.Node, q url.Values) string {
	link := scheme + "://" + userinfo + "@" + net.JoinHostPort(n.Server, strconv.Itoa(n.Port))
	if len(q) > 0 {
		link += "?" + q.Encode()
	}
	if n.Name != "" {
		link += "#" + escapeFragment(n.Name)
	}
	return link
}

// encodeVMess 生成 vmess://base64(JSON)。
//
// 字段名和取值形态完全照主流客户端的 Vmess QR 结构：port / aid 都是**字符串**，
// 全部字段（哪怕为空）都写出来。这些"多余"的空字段是刻意的——
// 部分老客户端会直接读字段而不判 null，缺字段会崩。
// base64 用标准表 + padding，同样是对齐主流客户端的输出。
//
// 已知的有损点：vmess 的 QR JSON 里没有 allowInsecure 字段（主流客户端也不导出），
// 所以这一位在 vmess 链接上无法保真；TLS.AllowInsecure 只在
// vless / trojan / hysteria2 的查询参数里往返。
func encodeVMess(n config.Node) (string, error) {
	if err := checkRemote(n); err != nil {
		return "", err
	}
	if strings.TrimSpace(n.UUID) == "" {
		return "", errors.New("share: VMess 节点缺少 UUID")
	}
	security := strings.TrimSpace(n.Security)
	if security == "" {
		security = "auto"
	}
	headerType := n.Transport.HeaderType
	if headerType == "" {
		//主流客户端空值写 "none"，这里保持一致，免得对端把空串当非法值。
		headerType = "none"
	}
	tlsField := ""
	if n.TLS.Enabled {
		tlsField = "tls"
	}
	network := normalizeNetwork(n.Transport.Network)
	pathField := n.Transport.Path
	if network == "grpc" && n.Transport.ServiceName != "" {
		//主流客户端的 vmess JSON 里**没有** serviceName 字段：
		// grpc 的服务名各家都塞在 path 里，所以这里必须做同样的映射，
		// 否则导出的 grpc 节点在对端会连到一个空服务名上。
		pathField = n.Transport.ServiceName
	}
	code := vmessQRCode{
		V:    "2",
		PS:   n.Name,
		Add:  n.Server,
		Port: strconv.Itoa(n.Port),
		ID:   n.UUID,
		Aid:  strconv.Itoa(n.AlterID),
		Scy:  security,
		Net:  network,
		Type: headerType,
		Host: n.Transport.Host,
		Path: pathField,
		TLS:  tlsField,
		SNI:  n.TLS.ServerName,
		ALPN: strings.Join(n.TLS.ALPN, ","),
		FP:   n.TLS.Fingerprint,
	}
	data, err := json.Marshal(code)
	if err != nil {
		return "", fmt.Errorf("share: 生成 VMess JSON 失败: %w", err)
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(data), nil
}

// vmessQRCode 是 vmess 分享链接里的 JSON 结构，字段名/类型对齐主流客户端。
type vmessQRCode struct {
	V    string `json:"v"`
	PS   string `json:"ps"`
	Add  string `json:"add"`
	Port string `json:"port"`
	ID   string `json:"id"`
	Aid  string `json:"aid"`
	Scy  string `json:"scy"`
	Net  string `json:"net"`
	Type string `json:"type"`
	Host string `json:"host"`
	Path string `json:"path"`
	TLS  string `json:"tls"`
	SNI  string `json:"sni"`
	ALPN string `json:"alpn"`
	FP   string `json:"fp"`
}

// encodeVLESS 生成 vless://UUID@HOST:PORT?PARAMS#NAME。
func encodeVLESS(n config.Node) (string, error) {
	if err := checkRemote(n); err != nil {
		return "", err
	}
	if strings.TrimSpace(n.UUID) == "" {
		return "", errors.New("share: VLESS 节点缺少 UUID")
	}
	q := url.Values{}
	setSecurityParam(q, n.TLS)
	if n.Flow != "" {
		q.Set("flow", n.Flow)
	}
	appendTransportParam(q, n.Transport)
	appendTLSParam(q, n.TLS)
	return buildLink("vless", escapeUserinfo(n.UUID), n, q), nil
}

// encodeTrojan 生成 trojan://PASSWORD@HOST:PORT?PARAMS#NAME。
func encodeTrojan(n config.Node) (string, error) {
	if err := checkRemote(n); err != nil {
		return "", err
	}
	if n.Password == "" {
		return "", errors.New("share: Trojan 节点缺少密码")
	}
	q := url.Values{}
	setSecurityParam(q, n.TLS)
	appendTransportParam(q, n.Transport)
	appendTLSParam(q, n.TLS)
	return buildLink("trojan", escapeUserinfo(n.Password), n, q), nil
}

// encodeHysteria2 生成 hysteria2://PASSWORD@HOST:PORT?PARAMS#NAME。
//
// 不写 security 参数：hysteria2 一定跑在 QUIC + TLS 之上，
// 各家客户端都按 TLS 处理，写出来只是浪费二维码容量。
// 密码允许为空（自建的开放端口就是无认证的）。
func encodeHysteria2(n config.Node) (string, error) {
	if err := checkRemote(n); err != nil {
		return "", err
	}
	q := url.Values{}
	if n.Obfs != "" {
		q.Set("obfs", n.Obfs)
	}
	if n.ObfsPassword != "" {
		q.Set("obfs-password", n.ObfsPassword)
	}
	if n.UpMbps > 0 {
		q.Set("upmbps", strconv.Itoa(n.UpMbps))
	}
	if n.DownMbps > 0 {
		q.Set("downmbps", strconv.Itoa(n.DownMbps))
	}
	if n.PinSHA256 != "" {
		q.Set("pinSHA256", n.PinSHA256)
	}
	appendTLSParam(q, n.TLS)
	return buildLink("hysteria2", escapeUserinfo(n.Password), n, q), nil
}

// encodeShadowsocks 生成 SIP002 格式：
//
//	ss://base64url(method:password)@host:port#name
//
// userinfo 用 URL-safe 且不带 padding 的 base64（base64.RawURLEncoding）：
// 标准表里的 '+' '/' 在链接里既难看又容易被中间环节（IM、笔记软件）二次转义，
// 而 SIP002 明确允许 URL-safe 变体，主流客户端都认。
func encodeShadowsocks(n config.Node) (string, error) {
	if err := checkRemote(n); err != nil {
		return "", err
	}
	if strings.TrimSpace(n.Method) == "" {
		return "", errors.New("share: Shadowsocks 节点缺少加密方式")
	}
	userinfo := base64.RawURLEncoding.EncodeToString([]byte(n.Method + ":" + n.Password))
	return buildLink("ss", userinfo, n, nil), nil
}

// ------------------------------------------------------------------
// 解析链接
// ------------------------------------------------------------------

// parseVMessJSON 解出 vmess 的字段表。
//
// 标准形态是 base64(JSON)；少数客户端（以及被手改过的链接）直接写裸 JSON，
// 所以 base64 路线走不通时再试一次裸 JSON。
func parseVMessJSON(body string) (map[string]json.RawMessage, error) {
	if data, err := decodeBase64Body(body); err == nil {
		var m map[string]json.RawMessage
		if json.Unmarshal(data, &m) == nil && m != nil {
			return m, nil
		}
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return nil, fmt.Errorf("share: vmess 内容既不是 Base64(JSON) 也不是 JSON: %v", err)
	}
	return m, nil
}

// lowerKeys 把 vmess JSON 的字段名统一转小写。
//
// vmess 的 JSON 没有官方 schema，字段名大小写全靠各客户端自觉：
// 见过 "PS"/"Add"/"PORT"，也见过全大写。解析侧统一小写查表，
// 出错的概率比"要求对方严格小写"低得多。
func lowerKeys(m map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}

// jsonScalar 把 JSON 标量（字符串或数字）统一取成字符串。
//
// vmess 的 port / aid 有的客户端写字符串 "443"，有的写数字 443，
// 同一个客户端不同版本都可能不一样，所以不能直接 Unmarshal 到 string。
func jsonScalar(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	if s[0] == '"' {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return ""
		}
		return v
	}
	return s
}

// jstr 取字段的标量字符串值；字段不存在返回空串。
func jstr(m map[string]json.RawMessage, key string) string {
	raw, ok := m[key]
	if !ok {
		return ""
	}
	return jsonScalar(raw)
}

// decodeVMess 解析 vmess://base64(JSON)。
func decodeVMess(p uriParts) (config.Node, error) {
	body := strings.TrimSpace(p.body)
	if body == "" {
		return config.Node{}, errors.New("share: vmess 链接缺少 Base64(JSON) 内容")
	}
	raw, err := parseVMessJSON(body)
	if err != nil {
		return config.Node{}, err
	}
	m := lowerKeys(raw)

	n := config.Node{Protocol: config.ProtocolVMess}
	// ps 是官方别名；个别客户端把别名放在 # 后面，作为兜底。
	n.Name = jstr(m, "ps")
	if n.Name == "" {
		n.Name = p.fragment
	}
	n.Server = jstr(m, "add")
	n.Port = atoiLoose(jstr(m, "port"))
	n.UUID = jstr(m, "id")
	n.AlterID = atoiLoose(jstr(m, "aid"))
	n.Security = jstr(m, "scy")
	if n.Security == "" {
		// 与主流客户端一致：vmess 没写加密方式时按 auto 处理。
		n.Security = "auto"
	}
	n.Transport.Network = normalizeNetwork(jstr(m, "net"))
	n.Transport.HeaderType = cleanHeaderType(jstr(m, "type"))
	n.Transport.Host = jstr(m, "host")
	// grpc 没有 path 的概念：服务名在主流客户端的 JSON 里就存在 path 字段里，
	// 所以按传输层把同一个字段落到模型的对应位置。
	if n.Transport.Network == "grpc" {
		n.Transport.ServiceName = jstr(m, "path")
	} else {
		n.Transport.Path = jstr(m, "path")
	}
	// tls 字段只有 "tls" / "" / "none" 三种取值，其余（含 reality）都当启用，
	// 因为写了非空值就说明链路要加密。
	if v := strings.ToLower(strings.TrimSpace(jstr(m, "tls"))); v != "" && v != "none" {
		n.TLS.Enabled = true
	}
	n.TLS.ServerName = jstr(m, "sni")
	n.TLS.ALPN = splitALPN(jstr(m, "alpn"))
	n.TLS.Fingerprint = jstr(m, "fp")

	return finalize(n)
}

// decodeVLESS 解析 vless://UUID@HOST:PORT?PARAMS#NAME。
func decodeVLESS(p uriParts) (config.Node, error) {
	n := config.Node{
		Protocol: config.ProtocolVLESS,
		Name:     p.fragment,
		UUID:     unescapePath(strings.TrimSpace(p.userinfo)),
		Server:   p.host,
		Port:     p.port,
	}
	if n.UUID == "" {
		return config.Node{}, errors.New("share: vless 链接缺少 UUID")
	}
	// vless 没写 security 就是明文（这是 URI 规范的默认值）。
	applyParams(&n, p.query, false)
	return finalize(n)
}

// decodeTrojan 解析 trojan://PASSWORD@HOST:PORT?PARAMS#NAME。
func decodeTrojan(p uriParts) (config.Node, error) {
	n := config.Node{
		Protocol: config.ProtocolTrojan,
		Name:     p.fragment,
		Password: unescapePath(strings.TrimSpace(p.userinfo)),
		Server:   p.host,
		Port:     p.port,
	}
	// trojan 与 vless 的默认值不同：trojan 协议本身就要求 TLS，
	// 所以链接里没写 security 时按"启用 TLS"处理（写了 security=none 才关）。
	applyParams(&n, p.query, true)
	return finalize(n)
}

// decodeHysteria2 解析 hysteria2://（或 hy2://）PASSWORD@HOST:PORT?PARAMS#NAME。
func decodeHysteria2(p uriParts) (config.Node, error) {
	n := config.Node{
		Protocol: config.ProtocolHysteria2,
		Name:     p.fragment,
		Password: unescapePath(strings.TrimSpace(p.userinfo)),
		Server:   p.host,
		Port:     p.port,
	}
	// hysteria2 跑在 QUIC 上，TLS 是协议的一部分，不可能关掉。
	applyParams(&n, p.query, true)
	n.TLS.Enabled = true
	return finalize(n)
}

// decodeShadowsocks 解析 ss:// 的两种格式：
//
//	SIP002：ss://base64web(method:password)@host:port?plugin=...#name
//	        也允许 userinfo 不做 base64 的明文形式（method:password@host:port）
//	旧格式：ss://base64(method:password@host:port)#name
//
// plugin 目前没有对应的模型字段（本项目不支持 ss 插件），直接忽略。
func decodeShadowsocks(p uriParts) (config.Node, error) {
	n := config.Node{Protocol: config.ProtocolShadowsocks, Name: p.fragment, Server: p.host, Port: p.port}

	userinfo := unescapePath(strings.TrimSpace(p.userinfo))
	if userinfo == "" {
		// 旧格式：整段（含 host:port）都在 base64 里。
		data, err := decodeBase64Body(strings.TrimSpace(p.body))
		if err != nil {
			return config.Node{}, fmt.Errorf("share: ss 链接内容无法解析（既没有 userinfo 也不是合法的旧格式）: %v", err)
		}
		inner := strings.TrimSpace(string(data))
		at := strings.LastIndex(inner, "@")
		if at < 0 {
			// 【审查 #23】inner 是解码后的 "method:password"（旧格式本来就该是
			// "method:password@host:port"），回显它等于把密码原文写进弹窗。
			// 只给原因和长度，足以让用户知道"链接被截断了/不是旧格式"。
			return config.Node{}, fmt.Errorf("share: ss 旧格式缺少 '@' 分隔（解出 %d 字节，无法分辨凭据与服务器地址）", len(inner))
		}
		n.Server, n.Port = splitHostPort(inner[at+1:])
		userinfo = unescapePath(inner[:at])
	}

	// SIP002 的 userinfo 是 base64url(method:password)，而 base64 字母表里
	// 没有 ':'，所以"含冒号"就说明是明文 userinfo。
	if !strings.Contains(userinfo, ":") {
		data, err := decodeBase64Body(userinfo)
		if err != nil {
			return config.Node{}, fmt.Errorf("share: ss 的 userinfo 既不是 Base64 也不含 ':' : %v", err)
		}
		userinfo = string(data)
	}
	method, password, ok := strings.Cut(userinfo, ":")
	if !ok {
		// 【审查 #23】userinfo 到这里已是解开后的凭据原文（明文，或"原本是
		// base64、刚被解出来"——base64 一样能还原密码），不能进错误信息。
		return config.Node{}, errors.New("share: ss 凭据缺少 'method:password' 形式（没有 ':' 分隔）")
	}
	n.Method = strings.TrimSpace(method)
	n.Password = password
	if n.Method == "" {
		return config.Node{}, errors.New("share: ss 链接缺少加密方式")
	}
	return finalize(n)
}

// finalize 补齐默认值并做基本校验。
func finalize(n config.Node) (config.Node, error) {
	if n.Transport.Network == "" {
		// 传输层留空时统一补 tcp：这样调用方拿到的 Network 永远可用，
		// 不需要在配置生成处到处判空。
		n.Transport.Network = "tcp"
	}
	if err := checkRemote(n); err != nil {
		return config.Node{}, err
	}
	return n, nil
}
