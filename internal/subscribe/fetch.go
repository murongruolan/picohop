package subscribe

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/murongruolan/picohop/internal/config"
)

// 抓取相关的默认值。都做成常量，方便测试与文档引用。
const (
	// DefaultTimeout 是下载订阅的整请求超时。
	DefaultTimeout = 20 * time.Second
	// MaxRedirects 是跟随重定向的上限（机场常用 302 跳到 CDN）。
	MaxRedirects = 10
	// MaxBodyBytes 是正文上限：实测最大的样本 687 KB，给到 32 MB 已经很宽松，
	// 同时避免"订阅链接指向一个电影文件"这种低级事故把内存打满。
	MaxBodyBytes = 32 << 20
)

// FetchOptions 是一次下载的可选项。
type FetchOptions struct {
	// UserAgent 为空则用 config.DefaultSubscriptionUA。
	UserAgent string
	// ProxyURL 非空时经它下载（形如 socks5://127.0.0.1:10809）。
	// 默认空 = 直连：订阅域名常常只在代理下可达，但反过来"代理还没起来就去更新"
	// 会死锁，所以默认直连、由用户显式打开。
	ProxyURL string
	// Timeout 为 0 时用 DefaultTimeout。
	Timeout time.Duration
}

// Response 是一次订阅下载的结果（正文 + 机场给的元信息）。
//
// 【为什么把响应头单独解析出来】subscription-userinfo / profile-update-interval /
// profile-web-page-url / content-disposition 是机场展示流量、到期、分组名、面板地址的
// 唯一渠道 —— 实测两个真实机场都给了前两个，分组名还直接写在 content-disposition 里。
type Response struct {
	Body        []byte
	StatusCode  int
	ContentType string

	UserInfo            config.SubscriptionUserInfo
	UpdateIntervalHours int
	WebPageURL          string
	// SuggestedName 来自 content-disposition 的 filename* / filename，或 profile-title。
	// 实测："顶级机场"（UTF-8'' 大写）与 "edgetunnel"（utf-8'' 小写）两种写法都出现过。
	SuggestedName string
}

// Fetch 下载订阅正文。
func Fetch(ctx context.Context, rawURL string, opts FetchOptions) (*Response, error) {
	ua := strings.TrimSpace(opts.UserAgent)
	if ua == "" {
		ua = config.DefaultSubscriptionUA
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	// URL 里内嵌 user:pass@ 时转成 Basic 认证头，并把凭据从 URL 里抹掉
	// （否则请求行里会带着它，日志/错误信息更容易漏出去）。
	fetchURL := strings.TrimSpace(rawURL)
	basic := ""
	if u, err := url.Parse(fetchURL); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok {
			basic = base64.StdEncoding.EncodeToString([]byte(u.User.Username() + ":" + pw))
		}
		u.User = nil
		fetchURL = u.String()
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if p := strings.TrimSpace(opts.ProxyURL); p != "" {
		pu, err := url.Parse(p)
		if err != nil {
			return nil, fmt.Errorf("代理地址无效：%w", err)
		}
		transport.Proxy = http.ProxyURL(pu)
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return fmt.Errorf("重定向超过 %d 次", MaxRedirects)
			}
			// 跨主机跳转时不要把 UA/认证头带过去
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("订阅链接不是合法 URL：%w", sanitizeURLError(err, fetchURL))
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")
	if basic != "" {
		req.Header.Set("Authorization", "Basic "+basic)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载订阅失败：%w", sanitizeURLError(err, fetchURL))
	}
	defer func() { _ = resp.Body.Close() }()

	out := &Response{StatusCode: resp.StatusCode, ContentType: resp.Header.Get("Content-Type")}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("读取订阅正文失败：%w", err)
	}
	out.Body = body

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, fmt.Errorf("订阅返回 HTTP %d（%s）", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	h := resp.Header
	out.UserInfo = ParseUserInfo(h.Get("subscription-userinfo"))
	out.UpdateIntervalHours = parsePositiveInt(h.Get("profile-update-interval"))
	out.WebPageURL = strings.TrimSpace(h.Get("profile-web-page-url"))
	out.SuggestedName = ParseContentDisposition(h.Get("content-disposition"))
	if out.SuggestedName == "" {
		out.SuggestedName = ParseProfileTitle(h.Get("profile-title"))
	}
	return out, nil
}

// ParseUserInfo 解析 subscription-userinfo 响应头。
//
// 格式：upload=1234; download=2234; total=1024000; expire=2218532293
//   - upload/download/total 单位是**字节**，expire 是 **Unix 秒**；
//   - **允许缺字段**，也允许值为空（实测有机场给 "expire="）；
//   - 有些面板会加自定义前缀（如 x-amz-meta-），一并容忍。
func ParseUserInfo(v string) config.SubscriptionUserInfo {
	var out config.SubscriptionUserInfo
	v = strings.TrimSpace(v)
	if v == "" {
		return out
	}
	for _, part := range strings.Split(v, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(kv[0], "x-amz-meta-"), "x-obs-meta-")))
		val := strings.TrimSpace(kv[1])
		if val == "" {
			continue
		}
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil || n < 0 {
			continue
		}
		switch key {
		case "upload":
			out.Upload = n
		case "download":
			out.Download = n
		case "total":
			out.Total = n
		case "expire":
			out.Expire = n
		}
	}
	return out
}

// ParseContentDisposition 从 content-disposition 里取文件名（机场拿它当分组名）。
//
// 支持两种写法：
//
//	attachment; filename="x.yaml"
//	attachment; filename*=UTF-8''%E9%A1%B6%E7%BA%A7%E6%9C%BA%E5%9C%BA     ← 实测订阅 1
//	attachment; filename*=utf-8''edgetunnel                              ← 实测订阅 2（小写 charset）
func ParseContentDisposition(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	for _, part := range strings.Split(v, ";") {
		p := strings.TrimSpace(part)
		low := strings.ToLower(p)
		switch {
		case strings.HasPrefix(low, "filename*="):
			raw := strings.TrimSpace(p[len("filename*="):])
			// 形如 UTF-8''<percent-encoded>，charset 与 language 大小写都可能变
			if i := strings.Index(raw, "''"); i >= 0 {
				raw = raw[i+2:]
			}
			if dec, err := url.PathUnescape(raw); err == nil {
				if name := cleanName(dec); name != "" {
					return name
				}
			}
		case strings.HasPrefix(low, "filename="):
			raw := strings.Trim(strings.TrimSpace(p[len("filename="):]), `"`)
			if name := cleanName(raw); name != "" {
				return name
			}
		}
	}
	return ""
}

// ParseProfileTitle 解析 profile-title 头；支持 "base64:<...>" 前缀（用来带 emoji）。
func ParseProfileTitle(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(v), "base64:") {
		if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v[len("base64:"):])); err == nil {
			return cleanName(string(dec))
		}
	}
	return cleanName(v)
}

// cleanName 只做两件事：去掉扩展名与首尾空白，并限长。
// 机场给的 filename 常是 "xxx.yaml"、"edgetunnel" 这类，用它当默认分组名。
func cleanName(s string) string {
	s = strings.TrimSpace(s)
	for _, ext := range []string{".yaml", ".yml", ".txt", ".json", ".conf"} {
		if strings.HasSuffix(strings.ToLower(s), ext) {
			s = s[:len(s)-len(ext)]
			break
		}
	}
	s = strings.TrimSpace(s)
	if len([]rune(s)) > 64 {
		s = string([]rune(s)[:64])
	}
	return s
}

func parsePositiveInt(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// IsNetworkError 判断错误是不是"网络层面拿不到东西"，用于给用户区分
// "订阅域名被墙/超时"与"格式不认"这两种完全不同的修复动作。
func IsNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne interface{ Timeout() bool }
	return errors.As(err, &ne) && ne.Timeout()
}

// sanitizeURLError 把 HTTP 客户端错误里带的 URL 换成打码版。
//
// 【为什么必须做这一层】Go 的 *url.Error 会把**完整 URL**（含 query 里的 token）
// 拼进 Error() 字符串：`Get "https://…/sub?token=abc": context deadline exceeded`。
// 订阅链接等于账号口令，一旦它进了日志/通知/错误提示，就等于把口令写进了文件。
// 实测踩到过：机场超时时错误信息里就是明文 token —— 外面那层对大 URL 打码挡不住它。
func sanitizeURLError(err error, rawURL string) error {
	if err == nil {
		return nil
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		// ue.URL 可能是重定向之后的地址（同样可能带 token），一样打码
		target := strings.TrimSpace(ue.URL)
		if target == "" {
			target = rawURL
		}
		return fmt.Errorf("%s %s: %w", ue.Op, MaskURL(target), ue.Err)
	}
	// 拿不到结构体（例如 url.Parse 的早期错误）就在字符串层面替换原文
	out := err.Error()
	if rawURL != "" {
		out = strings.ReplaceAll(out, rawURL, MaskURL(rawURL))
	}
	return errors.New(out)
}
