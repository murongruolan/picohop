// Package tester 提供"一键测试"能力：
// 分层探测前置代理与各条链路各自的出口 IP 与延迟。
package tester

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DialFunc 是 tester 需要的最小拨号能力（与 proxy.Front / proxy.Upstream 兼容）。
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Result 是一次测试的结果。
type Result struct {
	OK        bool
	IP        string
	Latency   time.Duration
	Via       string
	Err       error
	Elapsed   time.Duration
	Timestamp time.Time
}

// String 返回一行可读结果。
func (r Result) String() string {
	if !r.OK {
		return fmt.Sprintf("失败：%v", r.Err)
	}
	return fmt.Sprintf("%s（%d ms）", r.IP, r.Latency.Milliseconds())
}

var ipRe = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)

// FetchIP 通过指定的拨号函数访问 rawURL 并解析出出口 IP。
func FetchIP(ctx context.Context, dial DialFunc, rawURL, via string) Result {
	res := Result{Via: via, Timestamp: time.Now()}
	if rawURL == "" {
		rawURL = "https://api.ipify.org?format=json"
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		res.Err = fmt.Errorf("测试地址非法: %w", err)
		return res
	}
	port := 443
	if u.Scheme == "http" {
		port = 80
	}
	if ps := u.Port(); ps != "" {
		fmt.Sscanf(ps, "%d", &port)
	}
	hostPort := net.JoinHostPort(u.Hostname(), fmt.Sprint(port))

	client := &http.Client{
		Timeout: 25 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(c context.Context, network, addr string) (net.Conn, error) {
				// 忽略 addr 里携带的域名，统一用解析出来的 host:port，
				// 目的是保证域名确实被"交给远端代理解析"。
				return dial(c, network, hostPort)
			},
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		res.Err = err
		return res
	}
	resp, err := client.Do(req)
	if err != nil {
		res.Elapsed = time.Since(start)
		res.Err = err
		return res
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	res.Elapsed = time.Since(start)
	if err != nil {
		res.Err = err
		return res
	}
	if resp.StatusCode != http.StatusOK {
		res.Err = fmt.Errorf("HTTP %d", resp.StatusCode)
		return res
	}
	res.IP = ExtractIP(string(body))
	if res.IP == "" {
		res.Err = fmt.Errorf("无法从响应中解析出出口 IP：%s", truncate(string(body), 120))
		return res
	}
	res.Latency = res.Elapsed
	res.OK = true
	return res
}

// ExtractIP 支持 api.ipify.org 的 JSON 形式与纯文本形式。
func ExtractIP(body string) string {
	trimmed := strings.TrimSpace(body)
	var payload struct {
		IP     string `json:"ip"`
		Origin string `json:"origin"`
		Query  string `json:"query"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err == nil {
		for _, v := range []string{payload.IP, payload.Origin, payload.Query} {
			if ip := firstIP(v); ip != "" {
				return ip
			}
		}
	}
	return firstIP(trimmed)
}

func firstIP(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if net.ParseIP(s) != nil {
		return s
	}
	if m := ipRe.FindString(s); m != "" {
		return m
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Compare 判断链路出口与前置出口是否不同（即链式代理是否真的生效）。
func Compare(front, chain Result) string {
	if !front.OK || !chain.OK {
		return "无法比较：至少有一侧测试失败"
	}
	if front.IP == chain.IP {
		return fmt.Sprintf("⚠️ 链路出口 IP 与前置出口 IP 相同（%s），流量没有真正走住宅代理", chain.IP)
	}
	return fmt.Sprintf("✅ 链式代理生效：前置 %s → 住宅 %s", front.IP, chain.IP)
}
