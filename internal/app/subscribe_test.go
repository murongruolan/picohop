package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/murongruolan/picohop/internal/config"
)

// newSubTestApp 造一个带订阅的干净 App（沿用 newTestApp 的做法：数据目录指向 t.TempDir）。
func newSubTestApp(t *testing.T, subs []config.Subscription) *App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PICOHOP_DATA_DIR", dir)
	cfg := config.Default()
	cfg.Subscriptions = subs
	path := filepath.Join(dir, "config.json")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	a, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

// subServer 起一个假机场：返回 YAML + 全套响应头（形状照抄真实机场实测）。
func subServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("subscription-userinfo", "upload=1573351156; download=57632231604; total=214748364800; expire=")
		w.Header().Set("profile-update-interval", "24")
		w.Header().Set("profile-web-page-url", "https://panel.example.com")
		w.Header().Set("content-disposition", "attachment;filename*=UTF-8''%E9%A1%B6%E7%BA%A7%E6%9C%BA%E5%9C%BA")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const subYAML = `
proxies:
  - name: "剩余流量：144.86 GB"
    type: hysteria2
    server: fake.example.com
    port: 50000
    password: same-pw
  - name: "🇺🇸美国洛杉矶1号"
    type: hysteria2
    server: fake.example.com
    port: 50000
    password: same-pw
  - name: "🇭🇰香港2号"
    type: vless
    server: hk2.example.com
    port: 8443
    uuid: 11111111-2222-3333-4444-555555555555
`

// TestPreviewSubscriptionDoesNotTouchConfig 预览只看不动配置。
func TestPreviewSubscriptionDoesNotTouchConfig(t *testing.T) {
	srv := subServer(t, subYAML)
	a := newSubTestApp(t, nil)

	p, err := a.PreviewSubscription(context.Background(), ImportSubscriptionRequest{URL: srv.URL + "/sub?token=deadbeefdeadbeef"})
	if err != nil {
		t.Fatalf("预览失败：%v", err)
	}
	if p.Usable != 2 || p.FakeRows != 1 {
		t.Fatalf("预览统计错：可用 %d（期望 2），过滤信息行 %d（期望 1）", p.Usable, p.FakeRows)
	}
	if p.SuggestedName != "顶级机场" {
		t.Fatalf("建议分组名 %q，期望来自 content-disposition 的「顶级机场」", p.SuggestedName)
	}
	if p.UserInfo.Total != 214748364800 || p.UpdateIntervalHours != 24 {
		t.Fatalf("响应头没解析出来：%+v", p)
	}
	if strings.Contains(p.URLMasked, "deadbeef") {
		t.Fatalf("预览里的链接没打码：%q", p.URLMasked)
	}
	// 配置一点没动
	if len(a.Subscriptions()) != 0 {
		t.Fatal("预览不该写配置")
	}
	if n, _ := a.GetNode("nope"); n.ID != "" {
		t.Fatal("预览不该造节点")
	}
}

// TestImportSubscriptionCreatesGroup 导入：建记录（名字取自机场给的 filename）+ 全部节点
// 处于"未分配端口、未激活"。
func TestImportSubscriptionCreatesGroup(t *testing.T) {
	srv := subServer(t, subYAML)
	a := newSubTestApp(t, nil)

	rep, err := a.ImportSubscription(context.Background(), ImportSubscriptionRequest{URL: srv.URL})
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	if rep.Added != 2 {
		t.Fatalf("新增 %d 个，期望 2", rep.Added)
	}
	subs := a.Subscriptions()
	if len(subs) != 1 {
		t.Fatalf("订阅数 %d，期望 1", len(subs))
	}
	if subs[0].Subscription.Name != "顶级机场" {
		t.Fatalf("分组名 %q，期望采用机场给的 filename", subs[0].Subscription.Name)
	}
	if subs[0].Nodes != 2 || subs[0].Pending != 2 || subs[0].Active != 0 {
		t.Fatalf("订阅计数错：%+v", subs[0])
	}
	// 配置里落盘的节点也必须是未分配端口
	for _, n := range a.NodeConfigs() {
		if n.SubID != subs[0].Subscription.ID {
			t.Fatalf("节点没归到订阅：%+v", n)
		}
		if n.ListenPort != 0 || n.Active {
			t.Fatalf("导入的节点不该占端口/激活：%+v", n)
		}
	}
	// 落盘过了：从磁盘重新读一遍，订阅与节点都还在
	cfg2, err := config.Load(a.cfgPath)
	if err != nil {
		t.Fatalf("重新读配置失败：%v", err)
	}
	if len(cfg2.Subscriptions) != 1 || len(cfg2.Nodes) != 2 {
		t.Fatalf("落盘内容不对：订阅 %d 条，节点 %d 个", len(cfg2.Subscriptions), len(cfg2.Nodes))
	}
	if cfg2.Subscriptions[0].Name != "顶级机场" || cfg2.Nodes[0].SubID == "" {
		t.Fatalf("落盘内容不对：%+v", cfg2.Subscriptions[0])
	}
	if got := a.Subscriptions()[0].Subscription.Format; got != "clash-yaml" {
		t.Fatalf("形态没记下来：%q", got)
	}
}

// TestRefreshSubscriptionMarksMissingAndAddsNew 更新：机场删掉的标下架、新增的加进来。
//
// 这里用"机场内容会变"的服务器，而不是去改订阅 URL —— URL 按设计不可改
// （SaveSubscriptionMeta 只管名字/UA/间隔/开关）。
func TestRefreshSubscriptionMarksMissingAndAddsNew(t *testing.T) {
	var mu sync.Mutex
	body := `
proxies:
  - name: "🇺🇸美国洛杉矶1号"
    type: hysteria2
    server: fake.example.com
    port: 50000
    password: same-pw
  - name: "🇭🇰香港2号"
    type: vless
    server: hk2.example.com
    port: 8443
    uuid: 11111111-2222-3333-4444-555555555555
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	a := newSubTestApp(t, nil)
	if _, err := a.ImportSubscription(context.Background(), ImportSubscriptionRequest{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if got := len(a.NodeConfigs()); got != 2 {
		t.Fatalf("导入后节点数 %d，期望 2", got)
	}
	subID := a.Subscriptions()[0].Subscription.ID

	// 机场改内容：去掉香港、加一个日本
	mu.Lock()
	body = `
proxies:
  - name: "🇺🇸美国洛杉矶1号"
    type: hysteria2
    server: fake.example.com
    port: 50000
    password: same-pw
  - name: "🇯🇵日本3号"
    type: vless
    server: jp3.example.com
    port: 8443
    uuid: 22222222-3333-4444-5555-666666666666
`
	mu.Unlock()

	rep, err := a.RefreshSubscription(context.Background(), subID)
	if err != nil {
		t.Fatalf("更新失败：%v", err)
	}
	if rep.Added != 1 || rep.Missing != 1 {
		t.Fatalf("更新统计错：%+v", rep)
	}
	missing := 0
	for _, n := range a.NodeConfigs() {
		if n.SubID == subID && n.SubMissing {
			missing++
		}
	}
	if missing != 1 {
		t.Fatalf("被下架的节点应标记且保留，实际标记 %d 个", missing)
	}
	if got := a.Subscriptions()[0].Missing; got != 1 {
		t.Fatalf("订阅计数里的 Missing=%d，期望 1", got)
	}

	// 清理已过期
	n, err := a.ClearMissingNodes(subID)
	if err != nil || n != 1 {
		t.Fatalf("清理失败：n=%d err=%v", n, err)
	}
}

// TestEnableSubscriptionNodeAllocatesAndActivates 点「启用」才分配端口并真的监听起来。
func TestEnableSubscriptionNodeAllocatesAndActivates(t *testing.T) {
	srv := subServer(t, `
proxies:
  - name: "🇺🇸本地测试节点"
    type: socks5
    server: 127.0.0.1
    port: 9
`)
	a := newSubTestApp(t, nil)
	if _, err := a.ImportSubscription(context.Background(), ImportSubscriptionRequest{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	n := a.NodeConfigs()[0]
	if n.ListenPort != 0 {
		t.Fatalf("导入时不该有端口：%d", n.ListenPort)
	}

	got, err := a.EnableSubscriptionNode(n.ID)
	if err != nil {
		t.Fatalf("启用失败：%v", err)
	}
	if got.ListenPort == 0 || !got.Active {
		t.Fatalf("启用后应有端口且为激活：%+v", got)
	}
	// 真的在监听（最强的证据）
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", got.ListenPort))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("启用后端口 %d 没有在监听：%v", got.ListenPort, err)
	}
	_ = conn.Close()

	// 停掉，别让测试留着监听
	if err := a.Deactivate([]string{n.ID}); err != nil {
		t.Fatalf("停止失败：%v", err)
	}
}

// TestActivateAllSkipsPending 与 TestStartNodeRejectsPending：未分配端口的订阅节点
// 不能被"激活全部"顺手拉起来，也不能被单独启动。
func TestActivateAllSkipsPending(t *testing.T) {
	srv := subServer(t, `
proxies:
  - name: "🇺🇸A"
    type: socks5
    server: 127.0.0.1
    port: 9
  - name: "🇭🇰B"
    type: socks5
    server: 127.0.0.1
    port: 9
`)
	a := newSubTestApp(t, nil)
	if _, err := a.ImportSubscription(context.Background(), ImportSubscriptionRequest{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if err := a.ActivateAll(); err != nil {
		t.Fatalf("激活全部出错：%v", err)
	}
	for _, n := range a.NodeConfigs() {
		if n.Active || n.ListenPort != 0 {
			t.Fatalf("未分配的订阅节点不该被激活：%+v", n)
		}
	}
	// 【单独激活 = 自动分配端口】"激活全部"会跳过，但用户**指名**让某个订阅节点跑起来时，
	// 分配端口正是他的意图（否则还得先去别处点一次"启用"，多一步而且没道理）。
	id := a.NodeConfigs()[0].ID
	if err := a.Activate([]string{id}); err != nil {
		t.Fatalf("显式激活订阅节点应当自动分配端口并成功：%v", err)
	}
	n, _ := a.GetNode(id)
	if n.ListenPort == 0 || !n.Active {
		t.Fatalf("显式激活后应已分配端口并激活：%+v", n)
	}
	_ = a.Deactivate([]string{id})
}

// TestDeleteSubscriptionKeepsNodesAsManual 删订阅时默认保留节点（转成手动节点）。
func TestDeleteSubscriptionKeepsNodesAsManual(t *testing.T) {
	srv := subServer(t, `
proxies:
  - name: "🇺🇸A"
    type: socks5
    server: 127.0.0.1
    port: 9
`)
	a := newSubTestApp(t, nil)
	if _, err := a.ImportSubscription(context.Background(), ImportSubscriptionRequest{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	subID := a.Subscriptions()[0].Subscription.ID
	if err := a.DeleteSubscription(subID, false); err != nil {
		t.Fatalf("删除订阅失败：%v", err)
	}
	if len(a.Subscriptions()) != 0 {
		t.Fatal("订阅记录应被删掉")
	}
	nodes := a.NodeConfigs()
	if len(nodes) != 1 {
		t.Fatalf("节点应保留，实际 %d 个", len(nodes))
	}
	if nodes[0].SubID != "" {
		t.Fatalf("保留的节点应变成手动节点（SubID 清空）：%+v", nodes[0])
	}
	// 连节点一起删
	a2 := newSubTestApp(t, nil)
	srv2 := subServer(t, "proxies:\n  - {name: \"🇺🇸A\", type: socks5, server: 127.0.0.1, port: 9}\n")
	if _, err := a2.ImportSubscription(context.Background(), ImportSubscriptionRequest{URL: srv2.URL}); err != nil {
		t.Fatal(err)
	}
	if err := a2.DeleteSubscription(a2.Subscriptions()[0].Subscription.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(a2.NodeConfigs()) != 0 {
		t.Fatal("明确要求连节点一起删时应当清空")
	}
}

// TestPreviewSubscriptionFailures 三类失败要能区分（链接过期/UA 不认/格式不认识）。
func TestPreviewSubscriptionFailures(t *testing.T) {
	htmlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><head><title>订阅已过期</title></head></html>"))
	}))
	defer htmlSrv.Close()
	a := newSubTestApp(t, nil)
	if _, err := a.PreviewSubscription(context.Background(), ImportSubscriptionRequest{URL: htmlSrv.URL}); err == nil {
		t.Fatal("HTML 应报错")
	} else if !strings.Contains(err.Error(), "解析订阅失败") {
		t.Fatalf("错误信息应说明是解析失败：%v", err)
	}
	if _, err := a.PreviewSubscription(context.Background(), ImportSubscriptionRequest{URL: "://bad"}); err == nil {
		t.Fatal("非法 URL 应报错")
	}
	// 下载失败（404）也不该把 URL 原文写进错误
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer bad.Close()
	_, err := a.PreviewSubscription(context.Background(), ImportSubscriptionRequest{URL: bad.URL + "/sub?token=deadbeefdeadbeef"})
	if err == nil {
		t.Fatal("404 应报错")
	}
	if strings.Contains(err.Error(), "deadbeef") {
		t.Fatalf("错误信息里出现了 token：%v", err)
	}
}
