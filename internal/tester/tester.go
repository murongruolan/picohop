// Package tester 提供节点的三种测试能力（对齐主流客户端的语义）：
//
//	TCPPing  —— "测试 TCP 延迟"：只对服务器地址做一次 TCP 连接，最快、最粗
//	RealPing —— "测试真连接延迟"：走完整代理链路做一次 HTTP 请求，最准
//	Speed    —— "测试速度"：走完整代理链路下载一段数据，算 MB/s
//
// 另外保留 FetchIP：链式代理这个场景里"出口 IP 到底是谁"是最有价值的诊断。
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
	"strconv"
	"strings"
	"time"
)

// DialFunc 是测试需要的最小拨号能力（与 proxy.Front / proxy.Upstream 兼容）。
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// ------------------------------------------------------------------
// TCP 延迟
// ------------------------------------------------------------------

// TCPPing 对 host:port 建立一次 TCP 连接并返回耗时。
//
// 注意这是**不经代理**的直连测量：它回答的是"这台服务器在我这里
// TCP 握手要多久"，用来快速筛掉完全不可达的节点。
// 这正是主流客户端「测试 TCP 延迟」的行为。
func TCPPing(ctx context.Context, host string, port int, timeout time.Duration) (time.Duration, error) {
	if strings.TrimSpace(host) == "" {
		return 0, fmt.Errorf("服务器地址为空")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := &net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return 0, err
	}
	elapsed := time.Since(start)
	_ = conn.Close()
	return elapsed, nil
}

// ------------------------------------------------------------------
// 真连接延迟
// ------------------------------------------------------------------

// RealPing 经 dial 走完整链路请求 rawURL，返回"从发起请求到收到响应头"的耗时。
//
// 用 generate_204 这类地址最好：响应体为空，耗时几乎全是链路成本。
func RealPing(ctx context.Context, dial DialFunc, rawURL string, timeout time.Duration) (time.Duration, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bound, err := targetDial(dial, rawURL)
	if err != nil {
		return 0, err
	}
	client := newHTTPClient(bound, timeout)
	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, fmt.Errorf("测试地址非法: %w", err)
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	elapsed := time.Since(start)
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return elapsed, nil
}

// ------------------------------------------------------------------
// 测速
// ------------------------------------------------------------------

// Speed 经 dial 下载 rawURL，最多采样 maxSeconds 秒，返回 (MB/s, 下载字节数)。
//
// 单位说明：1 MB = 1024*1024 字节。之所以不用 10^6，是为了和
// 这类工具（主流客户端/主流客户端）界面上"MB/s"的口径一致。
func Speed(ctx context.Context, dial DialFunc, rawURL string, maxSeconds int, timeout time.Duration) (float64, int64, error) {
	if maxSeconds <= 0 {
		maxSeconds = 8
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(maxSeconds)*time.Second+timeout)
	defer cancel()

	bound, err := targetDial(dial, rawURL)
	if err != nil {
		return 0, 0, err
	}
	client := newHTTPClient(bound, timeout)
	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("测速地址非法: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// 只采样 maxSeconds 秒，时间到就主动断开 —— 测速地址通常是 100MB 的大文件，
	// 不掐断会把用户的流量白跑掉。
	//
	// 【为什么计时起点放在这里，而不是 client.Do 之前】分子（total）只是下面这个
	// 窗口里读到的字节：建链、TLS 握手、等响应头这些时间里**一个字节都不计入**。
	// 如果分母从 client.Do 之前起算，就等于把这些"零字节的时间"也摊进了速度里，
	// 节点被系统性低估 —— 建链/首字节越慢（跨洲节点、TLS 握手慢）低估越狠，
	// 几倍的差距都出现过。所以计时起点和采样窗口必须共用同一个 now，
	// 让分子分母都只覆盖真正的传输窗口（审查 #20）。
	// 别再把它挪回 client.Do 之前。
	windowStart := time.Now()
	deadline := windowStart.Add(time.Duration(maxSeconds) * time.Second)
	buf := make([]byte, 64*1024)
	var total int64
	for {
		if time.Now().After(deadline) {
			break
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			total += int64(n)
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			if total == 0 {
				return 0, 0, rerr
			}
			break
		}
	}
	// 分母与上面的 windowStart 配对：同样是"传输窗口"，不含建链/首字节等待，
	// 这样它和 total 的口径才一致（审查 #20）。
	elapsed := time.Since(windowStart).Seconds()
	if elapsed <= 0 || total == 0 {
		return 0, 0, fmt.Errorf("没有收到数据")
	}
	mbps := float64(total) / (1024 * 1024) / elapsed
	return mbps, total, nil
}

// ------------------------------------------------------------------
// 出口 IP
// ------------------------------------------------------------------

// Result 是一次出口 IP 探测的结果。
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
	bound, err := targetDial(dial, rawURL)
	if err != nil {
		res.Err = err
		return res
	}
	client := newHTTPClient(bound, 25*time.Second)
	defer client.CloseIdleConnections()

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

// Compare 判断链路出口与前置出口是否不同（即链式代理是否真的生效）。
func Compare(front, chain Result) string {
	if !front.OK || !chain.OK {
		return "无法比较：至少有一侧测试失败"
	}
	if front.IP == chain.IP {
		return fmt.Sprintf("⚠️ 链路出口 IP 与前置出口 IP 相同（%s），流量没有真正走后面的出口", chain.IP)
	}
	return fmt.Sprintf("✅ 链式代理生效：前置 %s → 出口 %s", front.IP, chain.IP)
}

// ------------------------------------------------------------------
// 内部工具
// ------------------------------------------------------------------

// newHTTPClient 造一个"所有连接都走 dial"的 HTTP 客户端。
//
// dial 必须是已经绑定好目标的函数（见 targetDial）。
func newHTTPClient(dial DialFunc, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext:       dial,
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
}

// targetDial 把一个"能连到任意地址"的拨号函数，绑定成"只连 rawURL 那个目标"的函数。
//
// 为什么必须绕这一下：http.Transport 交给 DialContext 的 addr 可能已经被
// 本地解析成 IP 了，而代理场景下我们要的是**把域名交给远端出口去解析**。
// 所以这里统一用 URL 里原始的 host:port，忽略 Transport 传来的地址。
func targetDial(dial DialFunc, rawURL string) (DialFunc, error) {
	hostPort, err := hostPortOf(rawURL)
	if err != nil {
		return nil, err
	}
	if dial == nil {
		dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{Timeout: 15 * time.Second}
			return d.DialContext(ctx, network, addr)
		}
	}
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dial(ctx, network, hostPort)
	}, nil
}

// hostPortOf 从 URL 里取出 host:port，缺端口时按 scheme 补 443/80。
func hostPortOf(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("测试地址非法: %w", err)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("测试地址缺少主机名: %q", rawURL)
	}
	port := 443
	if u.Scheme == "http" {
		port = 80
	}
	if ps := u.Port(); ps != "" {
		if v, perr := strconv.Atoi(ps); perr == nil {
			port = v
		}
	}
	return net.JoinHostPort(u.Hostname(), strconv.Itoa(port)), nil
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
