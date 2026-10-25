package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/murongruolan/picohop/internal/config"
	"github.com/murongruolan/picohop/internal/subscribe"
)

// SubscriptionPreview 是"粘上链接先看会发生什么"的结果，**不落盘**。
//
// 之所以要这一步：机场订阅最容易出的三类问题（链接过期返回网页、UA 不被机场认导致
// 只给几个节点、格式我们不认识）在用户眼里长得一模一样。先预览就能把"能拿到多少个节点、
// 跳过多少个、为什么"摆出来，用户确认之后再真正导入。
type SubscriptionPreview struct {
	// URLMasked 是打码后的链接：日志与界面里只允许出现这个，绝不出现带 token 的原文。
	URLMasked     string   `json:"urlMasked"`
	SuggestedName string   `json:"suggestedName"`
	Format        string   `json:"format"`
	Total         int      `json:"total"`
	Usable        int      `json:"usable"`
	Skipped       []string `json:"skipped"`
	FakeRows      int      `json:"fakeRows"`
	Duplicated    int      `json:"duplicated"`
	Invalid       int      `json:"invalid"`
	Notes         []string `json:"notes"`

	UserInfo            config.SubscriptionUserInfo `json:"userInfo"`
	UpdateIntervalHours int                         `json:"updateIntervalHours"`
	WebPageURL          string                      `json:"webPageUrl"`

	// SampleNames 是前几个节点名（预览时给用户看一眼，确认是不是他以为的那个机场）。
	SampleNames []string `json:"sampleNames"`
	ContentType string   `json:"contentType"`
	DurationMs  int64    `json:"durationMs"`
	// UAUsed 是这次实际用的 UA —— 用户遇到"只给几个节点"时要靠它判断该不该换。
	UAUsed string `json:"uaUsed"`
}

// SubscriptionReport 是一次导入/更新的结果。
type SubscriptionReport struct {
	Preview SubscriptionPreview `json:"preview"`
	MergeStats
}

// ImportSubscriptionRequest 是"把一条订阅导进来"的入参。
type ImportSubscriptionRequest struct {
	URL       string `json:"url"`
	Name      string `json:"name"`
	UserAgent string `json:"userAgent"`
	ViaProxy  bool   `json:"viaProxy"`
	// UpdateMinutes 为 0 表示不自动更新（自动更新在阶段 2 落地）。
	UpdateMinutes int `json:"updateMinutes"`
}

// PreviewSubscription 抓一次、解析一次，把结果摆在用户面前，**不动配置**。
func (a *App) PreviewSubscription(ctx context.Context, req ImportSubscriptionRequest) (SubscriptionPreview, error) {
	sub := config.Subscription{
		URL:       strings.TrimSpace(req.URL),
		UserAgent: strings.TrimSpace(req.UserAgent),
		ViaProxy:  req.ViaProxy,
	}
	if sub.URL == "" {
		return SubscriptionPreview{}, errors.New("订阅链接是空的")
	}
	if _, err := url.Parse(sub.URL); err != nil {
		return SubscriptionPreview{}, fmt.Errorf("订阅链接不是合法 URL：%w", err)
	}
	resp, nodes, sk, err := a.fetchAndParse(ctx, sub)
	if err != nil {
		return SubscriptionPreview{}, err
	}
	return a.buildPreview(sub, resp, nodes, sk), nil
}

// ImportSubscription 把一条订阅导进来：建记录 + 合并节点。
func (a *App) ImportSubscription(ctx context.Context, req ImportSubscriptionRequest) (SubscriptionReport, error) {
	if strings.TrimSpace(req.URL) == "" {
		return SubscriptionReport{}, errors.New("订阅链接是空的")
	}
	sub := config.Subscription{
		ID:            config.NewID(),
		Name:          strings.TrimSpace(req.Name),
		URL:           strings.TrimSpace(req.URL),
		UserAgent:     strings.TrimSpace(req.UserAgent),
		ViaProxy:      req.ViaProxy,
		UpdateMinutes: req.UpdateMinutes,
	}
	if sub.Name == "" {
		sub.Name = "订阅"
	}
	// 【与设置里的 UA 相同时就不要记进订阅】User-Agent 属于"设置"：用户在
	// 「设置 → 订阅」里改一次，所有订阅都该跟着变。只有用户给某一条订阅专门填了
	// 一个**不一样**的 UA 时，才把它记在订阅自己身上（那时它才是真正的逐条覆盖）。
	if strings.EqualFold(sub.UserAgent, strings.TrimSpace(a.subUA())) {
		sub.UserAgent = ""
	}

	resp, nodes, sk, err := a.fetchAndParse(ctx, sub)
	if err != nil {
		// 失败也要让用户看到原因（但不写入配置）：订阅记录不建。
		return SubscriptionReport{}, err
	}
	if len(nodes) == 0 {
		return SubscriptionReport{}, fmt.Errorf("这条订阅里没有可用节点（%s）", describeSketch(sk))
	}
	preview := a.buildPreview(sub, resp, nodes, sk)
	if sub.Name == "订阅" && resp.SuggestedName != "" {
		// 分组名优先用机场自己在 content-disposition 里给的名字 ——
		// 实测两家都给了，分别是"顶级机场"与"edgetunnel"。
		sub.Name = resp.SuggestedName
	}

	a.mu.Lock()
	// 【先补默认值再 append】NormalizeSubscription 负责分配 ID 与排序；
	// 先 append 再补会补到一份副本上，配置里留下的还是没 ID 的那份。
	a.cfg.NormalizeSubscription(&sub)
	a.cfg.Subscriptions = append(a.cfg.Subscriptions, sub)
	stats := mergeSubNodes(a.cfg, sub.ID, nodes)
	// 把这次观察到的元信息记进订阅记录（界面显示流量/到期用）
	a.applyResponseMetaLocked(sub.ID, resp, sk, stats)
	a.mu.Unlock()

	a.Logf("已导入订阅「%s」：新增 %d、刷新 %d、标记下架 %d（%s）",
		sub.Name, stats.Added, stats.Updated, stats.Missing, describeSketch(sk))
	a.Notify("success", "订阅已导入", fmt.Sprintf("「%s」新增 %d 个节点，共 %d 个可用", sub.Name, stats.Added, len(nodes)))
	a.emit(EventNodes, "")
	if err := a.Save(); err != nil {
		return SubscriptionReport{}, err
	}
	return SubscriptionReport{Preview: preview, MergeStats: stats}, nil
}

// RefreshSubscription 重新抓取并增量合并某条订阅。
func (a *App) RefreshSubscription(ctx context.Context, id string) (SubscriptionReport, error) {
	a.mu.RLock()
	idx := -1
	for i := range a.cfg.Subscriptions {
		if a.cfg.Subscriptions[i].ID == id {
			idx = i
			break
		}
	}
	var sub config.Subscription
	if idx >= 0 {
		sub = a.cfg.Subscriptions[idx]
	}
	a.mu.RUnlock()
	if idx < 0 {
		return SubscriptionReport{}, errors.New("订阅不存在")
	}
	if strings.TrimSpace(sub.URL) == "" {
		return SubscriptionReport{}, errors.New("这条订阅没有链接")
	}

	resp, nodes, sk, err := a.fetchAndParse(ctx, sub)
	if err != nil {
		a.mu.Lock()
		if i := a.subIndexLocked(id); i >= 0 {
			a.cfg.Subscriptions[i].LastError = err.Error()
		}
		a.mu.Unlock()
		_ = a.Save()
		return SubscriptionReport{}, err
	}
	if len(nodes) == 0 {
		msg := fmt.Sprintf("这条订阅没有解析出可用节点（%s）", describeSketch(sk))
		a.mu.Lock()
		if i := a.subIndexLocked(id); i >= 0 {
			a.cfg.Subscriptions[i].LastError = msg
		}
		a.mu.Unlock()
		_ = a.Save()
		return SubscriptionReport{}, errors.New(msg)
	}
	preview := a.buildPreview(sub, resp, nodes, sk)

	a.mu.Lock()
	stats := mergeSubNodes(a.cfg, id, nodes)
	a.applyResponseMetaLocked(id, resp, sk, stats)
	a.mu.Unlock()

	a.Logf("已更新订阅「%s」：新增 %d、刷新 %d、恢复 %d、标记下架 %d（%s）",
		sub.Name, stats.Added, stats.Updated, stats.Restored, stats.Missing, describeSketch(sk))
	a.emit(EventNodes, "")
	if err := a.Save(); err != nil {
		return SubscriptionReport{}, err
	}
	return SubscriptionReport{Preview: preview, MergeStats: stats}, nil
}

// SaveSubscriptionMeta 改订阅的名字/UA/间隔/开关（不动节点）。
func (a *App) SaveSubscriptionMeta(sub config.Subscription) (config.Subscription, error) {
	if strings.TrimSpace(sub.ID) == "" {
		return config.Subscription{}, errors.New("订阅 ID 为空")
	}
	a.mu.Lock()
	i := a.subIndexLocked(sub.ID)
	if i < 0 {
		a.mu.Unlock()
		return config.Subscription{}, errors.New("订阅不存在")
	}
	cur := a.cfg.Subscriptions[i]
	cur.Name = strings.TrimSpace(sub.Name)
	if cur.Name == "" {
		cur.Name = "订阅"
	}
	cur.UserAgent = strings.TrimSpace(sub.UserAgent)
	cur.ViaProxy = sub.ViaProxy
	cur.UpdateMinutes = sub.UpdateMinutes
	cur.Disabled = sub.Disabled
	cur.Sort = sub.Sort
	a.cfg.Subscriptions[i] = cur
	a.mu.Unlock()

	a.Logf("已更新订阅设置「%s」（UA=%s，间隔=%d 分钟，经代理=%v）",
		cur.Name, cur.EffectiveUA(a.cfg.Settings.SubscriptionUserAgent), cur.UpdateMinutes, cur.ViaProxy)
	a.emit(EventSubscriptions, "")
	return cur, a.Save()
}

// DeleteSubscription 删除一条订阅；alsoNodes 为真时连它的节点一起删。
//
// 【为什么默认行为要问清楚】订阅节点可能是用户精心配过前置/端口的，删订阅顺手删节点
// 属于破坏性操作，所以由调用方明确决定，而不是"删了就一起没"。
func (a *App) DeleteSubscription(id string, alsoNodes bool) error {
	a.mu.RLock()
	i := -1
	for k := range a.cfg.Subscriptions {
		if a.cfg.Subscriptions[k].ID == id {
			i = k
			break
		}
	}
	var name string
	var nodeIDs []string
	if i >= 0 {
		name = a.cfg.Subscriptions[i].Name
		for _, n := range a.cfg.Nodes {
			if n.SubID == id {
				nodeIDs = append(nodeIDs, n.ID)
			}
		}
	}
	a.mu.RUnlock()
	if i < 0 {
		return errors.New("订阅不存在")
	}

	if alsoNodes && len(nodeIDs) > 0 {
		if err := a.DeleteNodes(nodeIDs); err != nil {
			return err
		}
	}

	a.mu.Lock()
	kept := make([]config.Subscription, 0, len(a.cfg.Subscriptions))
	for _, s := range a.cfg.Subscriptions {
		if s.ID != id {
			kept = append(kept, s)
		}
	}
	a.cfg.Subscriptions = kept
	if !alsoNodes {
		// 保留节点时把它们变成"手动节点"（清掉归属），否则它们会挂在一个不存在的订阅上。
		for k := range a.cfg.Nodes {
			if a.cfg.Nodes[k].SubID == id {
				a.cfg.Nodes[k].SubID = ""
				a.cfg.Nodes[k].SubMissing = false
			}
		}
	}
	a.mu.Unlock()

	a.Logf("已删除订阅「%s」%s", name, map[bool]string{true: "（连同它的节点）", false: "（节点保留为手动节点）"}[alsoNodes])
	a.emit(EventNodes, "")
	a.emit(EventSubscriptions, "")
	return a.Save()
}

// ClearMissingNodes 清理某条订阅里"机场已下架"的节点，返回清理数量。
func (a *App) ClearMissingNodes(id string) (int, error) {
	a.mu.RLock()
	var ids []string
	for _, n := range a.cfg.Nodes {
		if n.SubID == id && n.SubMissing {
			ids = append(ids, n.ID)
		}
	}
	a.mu.RUnlock()
	if len(ids) == 0 {
		return 0, nil
	}
	if err := a.DeleteNodes(ids); err != nil {
		return 0, err
	}
	a.Logf("已清理 %d 个「机场已移除」的订阅节点", len(ids))
	// 归零记录里的计数
	a.mu.Lock()
	if i := a.subIndexLocked(id); i >= 0 {
		a.cfg.Subscriptions[i].Missing = 0
	}
	a.mu.Unlock()
	_ = a.Save()
	return len(ids), nil
}

// EnableSubscriptionNode 给订阅节点分配本地端口并启动它。
//
// 【为什么这么薄】"显式激活就自动分配端口"这条规则已经落在 App.Activate 里
// （见那里的注释），所以这里只是把"启用一个节点"表达成一个明确入口 ——
// 界面在端口列上点「未分配」时用它，语义比直接调 Activate 清楚。
func (a *App) EnableSubscriptionNode(id string) (config.Node, error) {
	if _, ok := a.GetNode(id); !ok {
		return config.Node{}, errors.New("节点不存在")
	}
	if err := a.Activate([]string{id}); err != nil {
		return config.Node{}, err
	}
	out, _ := a.GetNode(id)
	a.emit(EventNodes, "")
	return out, nil
}

// subIndexLocked 在已持锁的前提下按 ID 找订阅下标。
func (a *App) subIndexLocked(id string) int {
	for i := range a.cfg.Subscriptions {
		if a.cfg.Subscriptions[i].ID == id {
			return i
		}
	}
	return -1
}

// applyResponseMetaLocked 把这次抓取的响应头与解析统计写进订阅记录（调用方持锁）。
func (a *App) applyResponseMetaLocked(id string, resp *subscribe.Response, sk subscribe.Sketch, st MergeStats) {
	i := a.subIndexLocked(id)
	if i < 0 {
		return
	}
	s := &a.cfg.Subscriptions[i]
	s.LastUpdate = time.Now()
	s.LastError = ""
	s.UserInfo = resp.UserInfo
	s.UpdateIntervalHours = resp.UpdateIntervalHours
	s.WebPageURL = resp.WebPageURL
	s.Format = sk.Format
	s.Skipped = sk.Skipped
	s.FakeRows = sk.FakeRows
	s.Missing = st.Missing
}

// fetchAndParse 是三条路径（预览/导入/更新）共用的"抓 + 解"。
//
// 【错误信息里绝不出现 URL 原文】订阅链接带着个人 token，等于账号口令：
// 一律用 subscribe.MaskURL 打码后再进错误信息与日志。
func (a *App) fetchAndParse(ctx context.Context, sub config.Subscription) (*subscribe.Response, []config.Node, subscribe.Sketch, error) {
	ua := sub.EffectiveUA(a.subUA())
	opts := subscribe.FetchOptions{UserAgent: ua}
	if sub.ViaProxy {
		if p := a.subscriptionProxy(); p != "" {
			opts.ProxyURL = p
		} else {
			a.Logf("订阅「%s」勾了「经代理更新」，但当前没有可用的本机代理（前置是直连且没有节点在跑），本次改走直连", sub.Name)
		}
	}
	resp, err := subscribe.Fetch(ctx, sub.URL, opts)
	if err != nil {
		return nil, nil, subscribe.Sketch{}, fmt.Errorf("下载订阅失败（%s）：%w", subscribe.MaskURL(sub.URL), err)
	}
	res, perr := subscribe.Parse(resp.Body)
	if perr != nil {
		return resp, nil, subscribe.Sketch{}, fmt.Errorf("解析订阅失败（%s）：%w", subscribe.MaskURL(sub.URL), perr)
	}
	return resp, res.Nodes, res.Sketch, nil
}

// buildPreview 组织"导之前先看"的那份结果。
func (a *App) buildPreview(sub config.Subscription, resp *subscribe.Response, nodes []config.Node, sk subscribe.Sketch) SubscriptionPreview {
	names := make([]string, 0, 5)
	for _, n := range nodes {
		if len(names) == 5 {
			break
		}
		names = append(names, n.Name)
	}
	return SubscriptionPreview{
		URLMasked:           subscribe.MaskURL(sub.URL),
		SuggestedName:       resp.SuggestedName,
		Format:              sk.Format,
		Total:               sk.Total,
		Usable:              sk.Usable,
		Skipped:             sk.SortedSkipped(),
		FakeRows:            sk.FakeRows,
		Duplicated:          sk.Duplicated,
		Invalid:             sk.Invalid,
		Notes:               sk.Notes,
		UserInfo:            resp.UserInfo,
		UpdateIntervalHours: resp.UpdateIntervalHours,
		WebPageURL:          resp.WebPageURL,
		SampleNames:         names,
		ContentType:         resp.ContentType,
		UAUsed:              sub.EffectiveUA(a.subUA()),
	}
}

// subUA 取"设置里的订阅 UA"（持读锁取快照，避免与设置写入并发）。
func (a *App) subUA() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.Settings.SubscriptionUserAgent
}

// describeSketch 把解析统计压成一句人话（日志与通知用）。
func describeSketch(sk subscribe.Sketch) string {
	parts := []string{fmt.Sprintf("形态 %s", sk.Format), fmt.Sprintf("可用 %d/%d", sk.Usable, sk.Total)}
	if sk.FakeRows > 0 {
		parts = append(parts, fmt.Sprintf("过滤机场信息行 %d", sk.FakeRows))
	}
	if sk.Duplicated > 0 {
		parts = append(parts, fmt.Sprintf("重复 %d", sk.Duplicated))
	}
	if sk.Invalid > 0 {
		parts = append(parts, fmt.Sprintf("字段不全 %d", sk.Invalid))
	}
	if len(sk.Skipped) > 0 {
		if list := sk.SortedSkipped(); len(list) > 0 {
			parts = append(parts, "跳过 "+strings.Join(list, " "))
		}
	}
	return strings.Join(parts, "，")
}

// subscriptionProxy 返回"经代理更新订阅"要用的代理地址；没有可用代理时返回空串。
//
// 两档，按顺序取第一个可用的：
//
//  1. **全局前置**（「设置 → 前置」那一组）：那正是用户为了"个别网站走住宅 IP"
//     准备的代理，更新订阅走它最符合直觉。前置有账号密码时**必须带上** ——
//     付费住宅代理几乎都要认证，不带就会 407（见 config.Front.ProxyURL）。
//  2. 前置是直连（或没填全）时，退而用**第一个正在运行的节点**的本机端口
//     （我们每个节点的端口都是 mixed，socks5 直接可用）。开了「允许局域网」
//     并给节点配了本地鉴权时，也要把凭据带上，否则会被自己的端口拒绝。
//
// 两档都拿不到就返回空串，调用方会明确记一行日志说"本次改走直连"（不静默）。
func (a *App) subscriptionProxy() string {
	a.mu.RLock()
	f := a.cfg.Front
	var node config.Node
	found := false
	for _, n := range a.cfg.Nodes {
		if n.ListenPort <= 0 {
			continue
		}
		if s := a.servers[n.ID]; s != nil && s.Running() {
			node = n
			found = true
			break
		}
	}
	a.mu.RUnlock()

	if u := f.ProxyURL(); u != "" {
		return u
	}
	if found {
		return nodeSocksURL(node)
	}
	return ""
}

// nodeSocksURL 拼出"经某个本机节点端口"的 socks5 地址，带上它的本地鉴权（若有）。
func nodeSocksURL(n config.Node) string {
	host := n.ListenHost
	if host == "" {
		host = "127.0.0.1"
	}
	u := &url.URL{
		Scheme: "socks5",
		Host:   net.JoinHostPort(host, strconv.Itoa(n.ListenPort)),
	}
	if n.ListenUsername != "" || n.ListenPassword != "" {
		u.User = url.UserPassword(n.ListenUsername, n.ListenPassword)
	}
	return u.String()
}

// SubscriptionsView 是给界面用的订阅快照（含节点计数）。
type SubscriptionsView struct {
	Subscription config.Subscription `json:"subscription"`
	// URLMasked 打码后的链接，界面直接显示它。
	URLMasked string `json:"urlMasked"`
	Nodes     int    `json:"nodes"`
	Pending   int    `json:"pending"`
	Missing   int    `json:"missing"`
	Active    int    `json:"active"`
}

// Subscriptions 返回全部订阅（按 Sort 排序，附带节点计数），界面分组头用它。
func (a *App) Subscriptions() []SubscriptionsView {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make([]SubscriptionsView, 0, len(a.cfg.Subscriptions))
	for _, s := range a.cfg.Subscriptions {
		v := SubscriptionsView{Subscription: s, URLMasked: subscribe.MaskURL(s.URL)}
		for _, n := range a.cfg.Nodes {
			if n.SubID != s.ID {
				continue
			}
			v.Nodes++
			if n.PortPending() {
				v.Pending++
			}
			if n.SubMissing {
				v.Missing++
			}
			if n.Active {
				v.Active++
			}
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Subscription.Sort < out[j].Subscription.Sort })
	return out
}
