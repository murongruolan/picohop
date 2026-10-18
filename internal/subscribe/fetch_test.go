package subscribe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/murongruolan/picohop/internal/config"
)

// TestParseUserInfo 响应头解析：格式、缺字段、空值、自定义前缀都要容忍。
//
// 这些边界都有实测出处：订阅 1 的 expire 是**空的**（长期有效），
// 订阅 2 用 4102329600（2100 年）表示永久，面板还可能加 x-amz-meta- 之类前缀。
func TestParseUserInfo(t *testing.T) {
	cases := []struct {
		in   string
		want config.SubscriptionUserInfo
	}{
		{"upload=1573351156; download=57632231604; total=214748364800; expire=",
			config.SubscriptionUserInfo{Upload: 1573351156, Download: 57632231604, Total: 214748364800}},
		{"upload=1; download=2; total=3; expire=4102329600",
			config.SubscriptionUserInfo{Upload: 1, Download: 2, Total: 3, Expire: 4102329600}},
		{"upload=9409020665830; download=9409020665830; total=26388279066624; expire=4102329600",
			config.SubscriptionUserInfo{Upload: 9409020665830, Download: 9409020665830, Total: 26388279066624, Expire: 4102329600}},
		{"total=100", config.SubscriptionUserInfo{Total: 100}},           // 只给一个字段
		{"", config.SubscriptionUserInfo{}},                              // 空头
		{"garbage", config.SubscriptionUserInfo{}},                       // 没有 =
		{"upload=abc; total=10", config.SubscriptionUserInfo{Total: 10}}, // 坏值跳过
		{"x-amz-meta-upload=7; total=10", config.SubscriptionUserInfo{Upload: 7, Total: 10}},
	}
	for _, c := range cases {
		if got := ParseUserInfo(c.in); got != c.want {
			t.Fatalf("ParseUserInfo(%q) = %+v，期望 %+v", c.in, got, c.want)
		}
	}
}

// TestUserInfoDerived 已用/剩余/到期时间的派生值。Total 未知时必须回 0 让界面显示"—"。
func TestUserInfoDerived(t *testing.T) {
	u := config.SubscriptionUserInfo{Upload: 100, Download: 200, Total: 1000}
	if u.Used() != 300 {
		t.Fatalf("Used=%d，期望 300", u.Used())
	}
	if u.Remaining() != 700 {
		t.Fatalf("Remaining=%d，期望 700", u.Remaining())
	}
	if (config.SubscriptionUserInfo{}).Remaining() != 0 {
		t.Fatal("Total 未知时 Remaining 应为 0")
	}
	// 超额也不能回负数
	if (config.SubscriptionUserInfo{Upload: 9000, Total: 1000}).Remaining() != 0 {
		t.Fatal("超额时 Remaining 应为 0")
	}
	if !(config.SubscriptionUserInfo{}).ExpireTime().IsZero() {
		t.Fatal("Expire=0 应表示没有到期时间")
	}
	// 【断言必须与时区无关】4102329600 是机场常用的"永久"写法（≈2100 年）。
	// 只断言 unix 值原样往返、年份落在合理区间，不去卡具体哪一天。
	exp := config.SubscriptionUserInfo{Expire: 4102329600}.ExpireTime()
	if exp.Unix() != 4102329600 {
		t.Fatalf("到期时间没有原样换算：%d", exp.Unix())
	}
	if y := exp.Year(); y < 2090 || y > 2110 {
		t.Fatalf("4102329600 应换算成 2100 年前后，实际 %d", y)
	}
}

// TestParseContentDisposition 分组名的来源。两种大小写都是实测见过的。
func TestParseContentDisposition(t *testing.T) {
	cases := []struct{ in, want string }{
		{"attachment;filename*=UTF-8''%E9%A1%B6%E7%BA%A7%E6%9C%BA%E5%9C%BA", "顶级机场"}, // 实测订阅 1
		{"attachment; filename*=utf-8''edgetunnel", "edgetunnel"},                    // 实测订阅 2
		{`attachment; filename="my-airport.yaml"`, "my-airport"},
		{"", ""},
	}
	for _, c := range cases {
		if got := ParseContentDisposition(c.in); got != c.want {
			t.Fatalf("ParseContentDisposition(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestParseProfileTitle profile-title 支持 base64: 前缀（用来带 emoji）。
func TestParseProfileTitle(t *testing.T) {
	if got := ParseProfileTitle("base64:8J+agCBNeVZQTg=="); !strings.Contains(got, "MyVPN") {
		t.Fatalf("base64 头解析错：%q", got)
	}
	if got := ParseProfileTitle("plain-name"); got != "plain-name" {
		t.Fatalf("明文头解析错：%q", got)
	}
}

// TestFetchHeadersAndUA 一次真实形状的抓取：正文 + 四个响应头，并核对 UA 头。
func TestFetchHeadersAndUA(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("subscription-userinfo", "upload=1; download=2; total=100; expire=")
		w.Header().Set("profile-update-interval", "24")
		w.Header().Set("profile-web-page-url", "https://panel.example.com")
		w.Header().Set("content-disposition", "attachment;filename*=UTF-8''%E9%A1%B6%E7%BA%A7%E6%9C%BA%E5%9C%BA")
		_, _ = w.Write([]byte(yamlSample))
	}))
	defer srv.Close()

	resp, err := Fetch(context.Background(), srv.URL, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch 失败：%v", err)
	}
	if gotUA != config.DefaultSubscriptionUA {
		t.Fatalf("UA = %q，期望默认值 %q", gotUA, config.DefaultSubscriptionUA)
	}
	if resp.UserInfo.Total != 100 || resp.UserInfo.Expire != 0 {
		t.Fatalf("流量头解析错：%+v", resp.UserInfo)
	}
	if resp.UpdateIntervalHours != 24 {
		t.Fatalf("更新间隔 %d，期望 24", resp.UpdateIntervalHours)
	}
	if resp.WebPageURL != "https://panel.example.com" {
		t.Fatalf("面板地址 %q", resp.WebPageURL)
	}
	if resp.SuggestedName != "顶级机场" {
		t.Fatalf("建议分组名 %q，期望 顶级机场", resp.SuggestedName)
	}
	// 正文能接着解析
	r, err := Parse(resp.Body)
	if err != nil || r.Sketch.Usable == 0 {
		t.Fatalf("正文解析失败：%v，可用 %d", err, r.Sketch.Usable)
	}
}

// TestFetchCustomUA 逐订阅 UA 覆盖。
func TestFetchCustomUA(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte("proxies: []\n"))
	}))
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.URL, FetchOptions{UserAgent: "clash-verge/v1.6.0"}); err != nil {
		t.Fatalf("Fetch 失败：%v", err)
	}
	if gotUA != "clash-verge/v1.6.0" {
		t.Fatalf("UA = %q，期望逐订阅覆盖值", gotUA)
	}
}

// TestFetchBasicAuthFromURL URL 里内嵌 user:pass 时要转成 Basic 头，且**不留在 URL 里**。
func TestFetchBasicAuthFromURL(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_, _ = w.Write([]byte("proxies: []\n"))
	}))
	defer srv.Close()

	u := strings.Replace(srv.URL, "http://", "http://user:pw@", 1) + "/sub"
	if _, err := Fetch(context.Background(), u, FetchOptions{}); err != nil {
		t.Fatalf("Fetch 失败：%v", err)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Fatalf("没有生成 Basic 头：%q", gotAuth)
	}
	if gotPath != "/sub" {
		t.Fatalf("请求路径 %q，期望 /sub（凭据不该出现在 URL 里）", gotPath)
	}
}

// TestFetchFollowsRedirect 机场常用 302 跳到 CDN。
func TestFetchFollowsRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("proxies:\n  - {name: \"🇺🇸A\", type: ss, server: a.example.com, port: 8388, cipher: aes-256-gcm, password: p}\n"))
	}))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	resp, err := Fetch(context.Background(), redirect.URL, FetchOptions{})
	if err != nil {
		t.Fatalf("跟随重定向失败：%v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("最终状态码 %d", resp.StatusCode)
	}
}

// TestFetchErrorsNeverLeakToken 传输层错误也不能把 token 带出来。
//
// 【这一条是真机场实测踩出来的】机场超时时 Go 返回的是
// `Get "https://…/sub?token=abc": context deadline exceeded` —— *url.Error 会把完整
// URL 拼进 Error() 字符串，外面那层对"订阅 URL"打码根本挡不住它，
// 而这条错误会被写进配置（LastError）、日志与通知。
func TestFetchErrorsNeverLeakToken(t *testing.T) {
	const token = "deadbeefdeadbeef"

	// 1) 超时：服务器不回包也不关连接，客户端到点自己断
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
	}))
	defer slow.Close()
	_, err := Fetch(context.Background(), slow.URL+"/sub?token="+token, FetchOptions{Timeout: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("应当超时")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("超时错误里出现了 token：%v", err)
	}

	// 2) 域名解析不了
	_, err = Fetch(context.Background(), "http://no-such-host.invalid/sub?token="+token, FetchOptions{Timeout: 2 * time.Second})
	if err == nil {
		t.Fatal("应当失败")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("连接错误里出现了 token：%v", err)
	}

	// 3) URL 本身非法（url.Parse 的早期错误）
	_, err = Fetch(context.Background(), "http://%zz/sub?token="+token, FetchOptions{})
	if err == nil {
		t.Fatal("应当失败")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("URL 解析错误里出现了 token：%v", err)
	}
}

// TestFetchNon2xx 非 2xx 要报错（并且错误信息里不能带 URL 的 token）。
func TestFetchNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), srv.URL+"/sub?token=deadbeefdeadbeef", FetchOptions{})
	if err == nil {
		t.Fatal("403 应当报错")
	}
	if strings.Contains(err.Error(), "deadbeef") {
		t.Fatalf("错误信息里出现了 token：%v", err)
	}
}

// TestFetchBadProxy 代理地址非法时明确报错，而不是悄悄直连。
func TestFetchBadProxy(t *testing.T) {
	if _, err := Fetch(context.Background(), "https://example.com/sub", FetchOptions{ProxyURL: "://bad"}); err == nil {
		t.Fatal("非法代理地址应当报错")
	}
}

// TestFetchTimeout ctx 取消要能中断（界面上的"取消"按钮靠它）。
func TestFetchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_, _ = w.Write([]byte("proxies: []\n"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := Fetch(ctx, srv.URL, FetchOptions{}); err == nil {
		t.Fatal("超时应当报错")
	}
}
