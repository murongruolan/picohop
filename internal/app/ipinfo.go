package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/murongruolan/picohop/internal/proxy"
)

// ------------------------------------------------------------------
// 我的 IP 信息（本机直连 IP）
// ------------------------------------------------------------------

// IPInfo 是本机直连出口的 IP 信息。
//
// 注意"直连"二字：这个查询**必须绕过任何代理**，否则在开了系统代理时
// 拿回来的是节点出口的 IP —— 而用户要的是"本机自己的 IP"，正好相反。
type IPInfo struct {
	IP       string `json:"ip"`
	Country  string `json:"country"` // ISO 两字母，界面用它本地化成国名
	Region   string `json:"region"`
	City     string `json:"city"`
	Postal   string `json:"postal"`
	Org      string `json:"org"` // 形如 "AS4134 CHINANET BACKBONE"
	ASN      string `json:"asn"` // 从 Org 里拆出来的 "AS4134"
	Timezone string `json:"timezone"`
	Lat      string `json:"lat"`
	Lon      string `json:"lon"`
	At       int64  `json:"at"` // 取到的时间（Unix 毫秒）
	Err      string `json:"err,omitempty"`
}

// defaultIPInfoURL 是默认的查询接口。
//
// 选它是实测出来的：api.ip.sb 返回 403、ip-api.com 直连超时，而 ipinfo.io 754ms 返回
// 且字段齐全（国家代码 / 城市 / 地区 / 经纬度 / 组织 / 时区）。
// 接口可以改（Settings.IPInfoURL），换一个不至于要改代码。
const defaultIPInfoURL = "https://ipinfo.io/json"

// ipinfoRaw 对齐 ipinfo.io 的响应字段（该接口是扁平的）。
type ipinfoRaw struct {
	IP       string `json:"ip"`
	City     string `json:"city"`
	Region   string `json:"region"`
	Country  string `json:"country"`
	Loc      string `json:"loc"`
	Org      string `json:"org"`
	Postal   string `json:"postal"`
	Timezone string `json:"timezone"`
}

// FetchIPInfo 查一次本机直连 IP 信息。
func (a *App) FetchIPInfo(ctx context.Context) IPInfo {
	url := strings.TrimSpace(a.Settings().IPInfoURL)
	if url == "" {
		url = defaultIPInfoURL
	}

	client := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			// 【核心】显式不走任何代理。
			// 不设它的话，http.Transport 默认会用 ProxyFromEnvironment，
			// 环境里若有 HTTP(S)_PROXY 就会被带走 —— 那拿到的就是出口 IP，
			// 与"本机自己的 IP"正好相反。
			Proxy: nil,
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return IPInfo{Err: err.Error(), At: time.Now().UnixMilli()}
	}
	req.Header.Set("User-Agent", "PicoHop")

	resp, err := client.Do(req)
	if err != nil {
		return IPInfo{Err: err.Error(), At: time.Now().UnixMilli()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return IPInfo{Err: fmt.Sprintf("接口返回 %s", resp.Status), At: time.Now().UnixMilli()}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return IPInfo{Err: err.Error(), At: time.Now().UnixMilli()}
	}

	var raw ipinfoRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return IPInfo{Err: "接口返回的内容无法解析: " + err.Error(), At: time.Now().UnixMilli()}
	}

	info := IPInfo{
		IP:       raw.IP,
		Country:  strings.ToUpper(raw.Country),
		Region:   raw.Region,
		City:     raw.City,
		Postal:   raw.Postal,
		Org:      raw.Org,
		Timezone: raw.Timezone,
		At:       time.Now().UnixMilli(),
	}
	// loc 形如 "38.0414,114.4786"
	if lat, lon, ok := strings.Cut(raw.Loc, ","); ok {
		info.Lat = strings.TrimSpace(lat)
		info.Lon = strings.TrimSpace(lon)
	}
	// org 形如 "AS4134 CHINANET BACKBONE"：把自治域拆出来单独显示
	if asn, rest, ok := strings.Cut(raw.Org, " "); ok && strings.HasPrefix(strings.ToUpper(asn), "AS") {
		info.ASN = asn
		info.Org = rest
	}
	if info.IP == "" {
		info.Err = "接口没有返回 IP"
	}
	return info
}

// ------------------------------------------------------------------
// 网站测试
// ------------------------------------------------------------------

// SiteTestResult 是一个"站点 × 节点"的测试结果。
//
// 之所以带 NodeID：这个结构同时喂两个界面 —— 普通的网站测试列表（按当前节点），
// 以及"网站 × 节点可达性矩阵"（同一个站点经不同节点通不通）。
type SiteTestResult struct {
	Site   string `json:"site"`
	URL    string `json:"url"`
	NodeID string `json:"nodeId"`
	Node   string `json:"node"`
	OK     bool   `json:"ok"`
	MS     int64  `json:"ms"`
	Status int    `json:"status"`
	Err    string `json:"err,omitempty"`
}

// TestSites 通过各节点的**本地监听端口**逐个测站点。
//
// 为什么走本地端口而不是直接调上游：这条路径和浏览器走的完全一样
// （监听口 → 前置 → 出口），测出来的才是用户真正会遇到的延迟。
//
// 节点之间并行、同一节点内串行：既不让总耗时等于"站点数×节点数×超时"，
// 也不至于把某一个节点的连接数打爆。
func (a *App) TestSites(ctx context.Context, sites []string, nodeIDs []string, timeout time.Duration) []SiteTestResult {
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	// 挑出要测的节点（没指定就测全部运行中的）
	type target struct {
		id   string
		name string
		port int
	}
	var targets []target
	views := a.Nodes()
	want := map[string]bool{}
	for _, id := range nodeIDs {
		want[id] = true
	}
	for _, v := range views {
		if v.State != StateRunning || v.ListenPort == 0 {
			continue
		}
		if len(want) > 0 && !want[v.ID] {
			continue
		}
		targets = append(targets, target{id: v.ID, name: v.Name, port: v.ListenPort})
	}
	if len(targets) == 0 {
		return []SiteTestResult{{Err: "没有正在运行的节点"}}
	}

	var (
		mu      sync.Mutex
		results []SiteTestResult
		wg      sync.WaitGroup
	)
	for _, tg := range targets {
		wg.Add(1)
		go func(tg target) {
			defer wg.Done()
			for _, site := range sites {
				r := testOneSite(ctx, tg.id, tg.name, tg.port, site, timeout)
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
			}
		}(tg)
	}
	wg.Wait()
	return results
}

func testOneSite(ctx context.Context, nodeID, nodeName string, port int, site string, timeout time.Duration) SiteTestResult {
	url := site
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}
	r := SiteTestResult{Site: site, URL: url, NodeID: nodeID, Node: nodeName}

	parsed, err := parseHostPort(url)
	if err != nil {
		r.Err = "地址不合法"
		return r
	}
	proxyAddr := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))

	start := time.Now()
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			// 只走这一个节点，绝不回退到直连 —— 否则"测试失败"会被直连成功掩盖
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return proxy.DialSOCKS5(ctx, proxyAddr, addr, timeout)
			},
			// 有些站点对 HTTP/2 或连接复用敏感，测延迟用最朴素的方式
			DisableKeepAlives: true,
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err == nil {
		req.Header.Set("User-Agent", "PicoHop")
		req.Header.Set("Range", "bytes=0-2047") // 只要头一点点，测的是连通性不是带宽
		var resp *http.Response
		resp, err = client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			r.Status = resp.StatusCode
			// 2xx/3xx/4xx 都算"通了"（404 也说明链路是通的）；5xx 也不算链路问题
			r.OK = resp.StatusCode > 0 && resp.StatusCode < 600
		}
	}
	r.MS = time.Since(start).Milliseconds()
	if err != nil {
		r.Err = shortenNetErr(err)
	}
	_ = parsed
	return r
}

func parseHostPort(raw string) (string, error) {
	u := raw
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	return u, nil
}

// shortenNetErr 把 net/http 那串很长的错误压成一句人话。
func shortenNetErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "context deadline exceeded"), strings.Contains(msg, "timeout"):
		return "超时"
	case strings.Contains(msg, "connection refused"):
		return "连接被拒绝"
	case strings.Contains(msg, "certificate"), strings.Contains(msg, "tls"):
		return "证书/TLS 失败"
	case strings.Contains(msg, "socks5"), strings.Contains(msg, "SOCKS5"):
		return msg
	}
	// 其余取最后一段（通常是 "dial tcp ...: xxx"）
	if i := strings.LastIndex(msg, ": "); i >= 0 && i+2 < len(msg) {
		return msg[i+2:]
	}
	return msg
}
