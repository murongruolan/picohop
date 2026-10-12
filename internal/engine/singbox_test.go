package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	singjson "github.com/sagernet/sing/common/json"

	"chainproxy/internal/config"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
)

// parseIntoOptions 用 sing-box 自己的解析器把生成的配置读一遍。
//
// 这一步是这些测试的核心价值所在：字段名写错（例如把 public_key 写成 pubkey）
// 在纯 JSON 断言里看不出来，但 sing-box 的解析器会直接报"unknown field"。
// 所以"能被它解析成功"就等于"字段名确实是对的"。
func parseIntoOptions(t *testing.T, raw []byte) option.Options {
	t.Helper()
	ctx := include.Context(context.Background())
	opts, err := singjson.UnmarshalExtendedContext[option.Options](ctx, raw)
	if err != nil {
		t.Fatalf("sing-box 解析生成的配置失败（说明字段名或取值有问题）：%v\n配置：\n%s", err, raw)
	}
	return opts
}

// decodeMap 把配置读成 map，用于结构断言。
func decodeMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("解析 JSON 失败：%v", err)
	}
	return m
}

func outbounds(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	list, ok := m["outbounds"].([]any)
	if !ok {
		t.Fatalf("配置里没有 outbounds")
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		o, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("outbounds 元素不是对象")
		}
		out = append(out, o)
	}
	return out
}

// TestParseStrictnessIsReal 先证明"解析成功"这件事确实有约束力。
//
// 如果 sing-box 对未知字段是静默忽略的，那下面那些"能解析成功"的断言
// 就毫无意义 —— 所以这里先验证它真的会报错。
func TestParseStrictnessIsReal(t *testing.T) {
	bad := []byte(`{"outbounds":[{"type":"vless","tag":"x","server":"a","server_port":443,
	  "uuid":"u","tls":{"enabled":true,"pubkey_typo":"oops"}}]}`)
	ctx := include.Context(context.Background())
	if _, err := singjson.UnmarshalExtendedContext[option.Options](ctx, bad); err == nil {
		t.Skip("这个版本的 sing-box 会静默忽略未知字段，因此本文件的解析断言约束力有限（仍保留作为烟雾测试）")
	}
}

// TestBuildConfigVLESSReality 验证 VLESS + Reality + http 传输 + 前置。
func TestBuildConfigVLESSReality(t *testing.T) {
	n := config.Node{
		Name:      "reality",
		Protocol:  config.ProtocolVLESS,
		Server:    "jp1.example.com",
		Port:      443,
		UUID:      "11111111-2222-3333-4444-555555555555",
		Flow:      "xtls-rprx-vision",
		Transport: config.TransportOptions{Network: "http", Path: "/63ffaa10", Host: "www.amazon.com"},
		TLS: config.TLSOptions{
			Enabled:          true,
			ServerName:       "www.amazon.com",
			Fingerprint:      "chrome",
			RealityPublicKey: "j9hlAZUR9UIfQJdv8Ti6lW07zonxbYOjJufVxILOTTw",
			RealityShortID:   "d423dbf66168796e",
			ALPN:             []string{"h2", "http/1.1"},
		},
	}
	front := config.Front{Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10808}

	raw, err := BuildConfigJSON(n, front)
	if err != nil {
		t.Fatal(err)
	}
	obs := outbounds(t, decodeMap(t, raw))
	if len(obs) != 2 {
		t.Fatalf("有前置时应当有 2 个出口，实际 %d", len(obs))
	}

	// 前置出口
	if obs[0]["type"] != "socks" || obs[0]["tag"] != tagFront {
		t.Errorf("前置出口不对：%+v", obs[0])
	}
	if obs[0]["server"] != "127.0.0.1" || obs[0]["server_port"] != float64(10808) {
		t.Errorf("前置地址不对：%+v", obs[0])
	}

	// 节点出口
	node := obs[1]
	if node["type"] != "vless" || node["tag"] != tagNode {
		t.Errorf("节点出口不对：%+v", node)
	}
	if node["uuid"] != n.UUID || node["flow"] != "xtls-rprx-vision" {
		t.Errorf("UUID/Flow 不对：%+v", node)
	}
	// 有前置就必须挂 detour —— 这是"链式代理"的全部实现
	if node["detour"] != tagFront {
		t.Errorf("应当挂 detour=%s，实际 %v", tagFront, node["detour"])
	}

	tlsObj, _ := node["tls"].(map[string]any)
	if tlsObj == nil || tlsObj["enabled"] != true {
		t.Fatalf("tls 段不对：%+v", node["tls"])
	}
	if tlsObj["server_name"] != "www.amazon.com" {
		t.Errorf("SNI 不对：%v", tlsObj["server_name"])
	}
	reality, _ := tlsObj["reality"].(map[string]any)
	if reality == nil || reality["public_key"] != n.TLS.RealityPublicKey || reality["short_id"] != n.TLS.RealityShortID {
		t.Errorf("Reality 段不对：%+v", tlsObj["reality"])
	}
	utls, _ := tlsObj["utls"].(map[string]any)
	if utls == nil || utls["enabled"] != true || utls["fingerprint"] != "chrome" {
		t.Errorf("uTLS 段不对（Reality 必须有它）：%+v", tlsObj["utls"])
	}

	tr, _ := node["transport"].(map[string]any)
	if tr == nil || tr["type"] != "http" || tr["path"] != "/63ffaa10" {
		t.Errorf("传输层不对：%+v", node["transport"])
	}
	hosts, _ := tr["host"].([]any)
	if len(hosts) != 1 || hosts[0] != "www.amazon.com" {
		t.Errorf("http 传输的 host 应当是数组：%+v", tr["host"])
	}

	// 必须能被 sing-box 真正解析
	parseIntoOptions(t, raw)
}

// TestBuildConfigDirectFrontHasNoDetour 验证"直连模式"的形态。
//
// sing-box 明确不允许 direct 出口挂 detour，所以直连时
// 既不能生成前置出口，也不能写 detour 字段。
func TestBuildConfigDirectFrontHasNoDetour(t *testing.T) {
	n := config.Node{
		Name: "direct", Protocol: config.ProtocolShadowsocks,
		Server: "sg.example.com", Port: 8388,
		Method: "aes-128-gcm", Password: "pw",
	}
	raw, err := BuildConfigJSON(n, config.Front{Type: config.FrontTypeDirect})
	if err != nil {
		t.Fatal(err)
	}
	obs := outbounds(t, decodeMap(t, raw))
	if len(obs) != 1 {
		t.Fatalf("直连时只应有 1 个出口，实际 %d", len(obs))
	}
	if _, has := obs[0]["detour"]; has {
		t.Error("直连时不允许出现 detour")
	}
	if obs[0]["tag"] != tagNode || obs[0]["type"] != "shadowsocks" {
		t.Errorf("出口不对：%+v", obs[0])
	}
	if obs[0]["method"] != "aes-128-gcm" || obs[0]["password"] != "pw" {
		t.Errorf("SS 参数不对：%+v", obs[0])
	}
	parseIntoOptions(t, raw)
}

// TestTransportMapping 验证传输层映射，重点是 **tcp 必须完全不写 transport 字段**。
//
// sing-box 的 transport.type 只接受 http/ws/grpc/quic/httpupgrade，没有 tcp；
// 写了 "tcp" 会被直接拒绝。这是一个很容易踩的坑。
func TestTransportMapping(t *testing.T) {
	cases := []struct {
		network   string
		wantType  string // 空表示不应出现 transport 段
		extraKey  string
		extraWant any
	}{
		{network: "tcp", wantType: ""},
		{network: "", wantType: ""},
		{network: "ws", wantType: "ws", extraKey: "path", extraWant: "/ws"},
		{network: "grpc", wantType: "grpc", extraKey: "service_name", extraWant: "svc"},
		{network: "http", wantType: "http", extraKey: "path", extraWant: "/h"},
		{network: "h2", wantType: "http"},
		{network: "httpupgrade", wantType: "httpupgrade", extraKey: "path", extraWant: "/u"},
	}
	for _, c := range cases {
		t.Run("network="+c.network, func(t *testing.T) {
			n := config.Node{
				Name: "t", Protocol: config.ProtocolVLESS, Server: "a", Port: 443,
				UUID:      "u",
				Transport: config.TransportOptions{Network: c.network, Path: pickPath(c.wantType), Host: "h", ServiceName: "svc"},
			}
			raw, err := BuildConfigJSON(n, config.Front{Type: config.FrontTypeDirect})
			if err != nil {
				t.Fatal(err)
			}
			obs := outbounds(t, decodeMap(t, raw))
			tr, has := obs[0]["transport"].(map[string]any)
			if c.wantType == "" {
				if has {
					t.Fatalf("tcp/%s 不应生成 transport 段，实际 %+v", c.network, tr)
				}
			} else {
				if !has {
					t.Fatalf("应当生成 transport 段")
				}
				if tr["type"] != c.wantType {
					t.Errorf("transport.type 应为 %s，实际 %v", c.wantType, tr["type"])
				}
				if c.extraKey != "" {
					if got := tr[c.extraKey]; got != c.extraWant {
						t.Errorf("transport.%s 应为 %v，实际 %v", c.extraKey, c.extraWant, got)
					}
				}
			}
			parseIntoOptions(t, raw)
		})
	}
}

func pickPath(wantType string) string {
	switch wantType {
	case "ws":
		return "/ws"
	case "http":
		return "/h"
	case "httpupgrade":
		return "/u"
	default:
		return ""
	}
}

// TestWireGuardAndHysteria2NotBuiltBySingBox 确认两件事：
//   - WireGuard 不走 sing-box（我们用自研实现，因此不应该被 engine 接受）
//   - Hysteria2 的 obfs / 带宽容差被正确写入
func TestHysteria2Options(t *testing.T) {
	n := config.Node{
		Name: "hy2", Protocol: config.ProtocolHysteria2,
		Server: "kr.example.com", Port: 443, Password: "pw",
		UpMbps: 50, DownMbps: 200, Obfs: "salamander", ObfsPassword: "op",
		PinSHA256: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		TLS:       config.TLSOptions{Enabled: true, ServerName: "kr.example.com", AllowInsecure: true},
	}
	raw, err := BuildConfigJSON(n, config.Front{Type: config.FrontTypeDirect})
	if err != nil {
		t.Fatal(err)
	}
	node := outbounds(t, decodeMap(t, raw))[0]
	if node["type"] != "hysteria2" || node["password"] != "pw" {
		t.Errorf("HY2 基本字段不对：%+v", node)
	}
	if node["up_mbps"] != float64(50) || node["down_mbps"] != float64(200) {
		t.Errorf("带宽容差不对：%+v", node)
	}
	obfs, _ := node["obfs"].(map[string]any)
	if obfs == nil || obfs["type"] != "salamander" || obfs["password"] != "op" {
		t.Errorf("混淆段不对：%+v", node["obfs"])
	}
	tlsObj, _ := node["tls"].(map[string]any)
	if tlsObj["insecure"] != true {
		t.Errorf("insecure 没写进去：%+v", tlsObj)
	}
	if _, has := tlsObj["certificate_public_key_sha256"]; !has {
		t.Errorf("PinSHA256 没写进去：%+v", tlsObj)
	}
	parseIntoOptions(t, raw)

	// 带宽为 0 时不应写入（否则可能被当成"限速到 0"）
	n2 := n
	n2.UpMbps, n2.DownMbps = 0, 0
	raw2, err := BuildConfigJSON(n2, config.Front{Type: config.FrontTypeDirect})
	if err != nil {
		t.Fatal(err)
	}
	node2 := outbounds(t, decodeMap(t, raw2))[0]
	if _, has := node2["up_mbps"]; has {
		t.Error("带宽为 0 时不应写入 up_mbps")
	}
}

// TestVMessOptions 验证 VMess 的加密方式与 alterId。
func TestVMessOptions(t *testing.T) {
	n := config.Node{
		Name: "vm", Protocol: config.ProtocolVMess,
		Server: "hk.example.com", Port: 8443, UUID: "u", AlterID: 4, Security: "chacha20-poly1305",
		Transport: config.TransportOptions{Network: "ws", Path: "/ws", Host: "cdn.example.com"},
		TLS:       config.TLSOptions{Enabled: true, ServerName: "cdn.example.com"},
	}
	raw, err := BuildConfigJSON(n, config.Front{Type: config.FrontTypeDirect})
	if err != nil {
		t.Fatal(err)
	}
	node := outbounds(t, decodeMap(t, raw))[0]
	if node["type"] != "vmess" || node["security"] != "chacha20-poly1305" || node["alter_id"] != float64(4) {
		t.Errorf("VMess 字段不对：%+v", node)
	}
	tr, _ := node["transport"].(map[string]any)
	headers, _ := tr["headers"].(map[string]any)
	if headers["Host"] != "cdn.example.com" {
		t.Errorf("ws 的 Host 头不对：%+v", tr)
	}
	parseIntoOptions(t, raw)
}

// TestTrojanRequiresTLS 验证 Trojan 的密码与 TLS。
func TestTrojanOptions(t *testing.T) {
	n := config.Node{
		Name: "tj", Protocol: config.ProtocolTrojan,
		Server: "us.example.com", Port: 443, Password: "pw",
		Transport: config.TransportOptions{Network: "grpc", ServiceName: "gsvc"},
		TLS:       config.TLSOptions{Enabled: true, ServerName: "us.example.com"},
	}
	raw, err := BuildConfigJSON(n, config.Front{Type: config.FrontTypeDirect})
	if err != nil {
		t.Fatal(err)
	}
	node := outbounds(t, decodeMap(t, raw))[0]
	if node["type"] != "trojan" || node["password"] != "pw" {
		t.Errorf("Trojan 字段不对：%+v", node)
	}
	tr, _ := node["transport"].(map[string]any)
	if tr["type"] != "grpc" || tr["service_name"] != "gsvc" {
		t.Errorf("gRPC 传输层不对：%+v", tr)
	}
	parseIntoOptions(t, raw)
}

// TestHTTPFrontUsesHTTPOutbound 验证 http 前置映射成 http 出口。
func TestHTTPFrontUsesHTTPOutbound(t *testing.T) {
	n := config.Node{
		Name: "x", Protocol: config.ProtocolShadowsocks,
		Server: "a", Port: 1, Method: "aes-128-gcm", Password: "p",
	}
	front := config.Front{Type: config.FrontTypeHTTP, Host: "10.0.0.1", Port: 3128, Username: "u", Password: "p"}
	raw, err := BuildConfigJSON(n, front)
	if err != nil {
		t.Fatal(err)
	}
	obs := outbounds(t, decodeMap(t, raw))
	if obs[0]["type"] != "http" {
		t.Errorf("http 前置应当映射成 http 出口，实际 %v", obs[0]["type"])
	}
	if obs[0]["username"] != "u" || obs[0]["password"] != "p" {
		t.Errorf("前置鉴权没写进去：%+v", obs[0])
	}
	parseIntoOptions(t, raw)
}

// TestBuildConfigRejectsUnusableFront 锁死"前置写坏了不许静默直连"（审查 #10）。
//
// 旧实现是 `host == "" || front.Port <= 0 → return nil`，nil 又表示"直连"，
// 于是这些配置会生成一份**没有前置出口、也没有 detour** 的配置：节点照常启动，
// 却用本机 IP 直接去连服务器 —— 前置（住宅代理 /主流客户端/ 公司出口）白挂了，
// 而且日志里一行异常都没有，用户看不出来。
//
// 现在必须返回错误（fail-closed）：宁可不启动这个节点，也不能从本机 IP 出去。
func TestBuildConfigRejectsUnusableFront(t *testing.T) {
	n := config.Node{
		Name: "n", Protocol: config.ProtocolShadowsocks,
		Server: "sg.example.com", Port: 8388, Method: "aes-128-gcm", Password: "pw",
	}
	cases := []struct {
		name  string
		front config.Front
	}{
		{"socks5 地址为空", config.Front{Type: config.FrontTypeSocks5, Port: 10808}},
		{"socks5 地址是空白字符", config.Front{Type: config.FrontTypeSocks5, Host: "   ", Port: 10808}},
		{"socks5 端口为 0", config.Front{Type: config.FrontTypeSocks5, Host: "127.0.0.1"}},
		{"socks5 端口为负数", config.Front{Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: -1}},
		{"socks5 端口超出范围", config.Front{Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 70000}},
		{"http 地址为空", config.Front{Type: config.FrontTypeHTTP, Port: 3128}},
		{"http 端口为 0", config.Front{Type: config.FrontTypeHTTP, Host: "10.0.0.1"}},
		// 空 type 在别处一律按 socks5 处理（proxy.NewFront / Front.Describe），
		// 所以它也不能被当成"没配前置"。
		{"type 为空且地址为空", config.Front{}},
		{"type 为空但地址写坏了", config.Front{Host: "10.0.0.1", Port: -1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := BuildConfigJSON(n, c.front)
			if err == nil {
				t.Fatalf("非法前置（%+v）必须报错，却生成了一份配置：\n%s", c.front, raw)
			}
			// 错误必须说清"发生了什么"与"为什么不能将就"：
			// 只说"配置非法"的话，后人很容易又顺手改回"那就算了，直连吧"。
			for _, want := range []string{"前置", "直连"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("错误信息应当包含 %q，实际：%s", want, err)
				}
			}
			t.Logf("实际错误信息：%s", err)
		})
	}
}

// TestExplicitDirectFrontIsStillDirect 确认收紧之后没有误伤"显式直连"。
//
// direct 是**用户明确选的**，不需要地址与端口；此时既不能生成前置出口，
// 也不能因为地址空而报错。
func TestExplicitDirectFrontIsStillDirect(t *testing.T) {
	n := config.Node{
		Name: "n", Protocol: config.ProtocolShadowsocks,
		Server: "sg.example.com", Port: 8388, Method: "aes-128-gcm", Password: "pw",
	}
	for _, c := range []config.Front{
		{Type: config.FrontTypeDirect},
		{Type: config.FrontTypeDirect, Host: "", Port: 0},
		// 历史遗留形态：类型是 direct、还留着上次填的地址端口，一样按直连处理
		{Type: config.FrontTypeDirect, Host: "127.0.0.1", Port: 10808},
	} {
		raw, err := BuildConfigJSON(n, c)
		if err != nil {
			t.Fatalf("显式直连不该报错（%+v）：%v", c, err)
		}
		obs := outbounds(t, decodeMap(t, raw))
		if len(obs) != 1 {
			t.Fatalf("显式直连只应有 1 个出口（%+v），实际 %d", c, len(obs))
		}
		if _, has := obs[0]["detour"]; has {
			t.Errorf("显式直连不允许出现 detour（%+v）", c)
		}
		parseIntoOptions(t, raw)
	}
}

// TestStartFailsInsteadOfFallingBackToDirect 是本缺陷的端到端锁：
// 前置非法时出口**必须启动失败**，而不是"起来了、然后直连"。
//
// 为什么不只测 BuildConfigJSON：上层（proxy.Server.Start → Upstream.Start）
// 把"出口启动失败"如实报给用户、并把节点标成错误；只要 Start 返回 nil，
// 界面就会显示"已启动"，而流量其实从本机 IP 出去了。
// 所以这里断言两件事：Start 返回错误，且失败后 Dial 也不会可用
// （也就是没用"空出口 = 直连"糊过去）。
func TestStartFailsInsteadOfFallingBackToDirect(t *testing.T) {
	n := config.Node{
		Name: "n", Protocol: config.ProtocolVLESS,
		Server: "a.example.com", Port: 443, UUID: "u",
	}
	front := config.Front{Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 0}

	up, err := New(n, front, func(string, ...any) {})
	if err != nil {
		t.Fatalf("构造不该失败（构造时还不生成配置）：%v", err)
	}
	if err := up.Start(context.Background(), nil); err == nil {
		_ = up.Stop()
		t.Fatal("前置端口非法时 Start 必须失败，否则节点会静默从本机 IP 直连出去")
	} else if !strings.Contains(err.Error(), "前置") {
		t.Errorf("Start 的错误应当点明是前置的问题，实际：%v", err)
	}
	if _, err := up.Dial(context.Background(), nil, "example.com:443"); err == nil {
		t.Error("Start 失败后不应该还能拨号")
	}
}

// TestNewRejectsUnsupportedProtocol 确认 engine 只管它该管的那五个协议。
func TestNewRejectsUnsupportedProtocol(t *testing.T) {
	for _, p := range []string{config.ProtocolSOCKS5, config.ProtocolHTTP, config.ProtocolWireGuard} {
		if _, err := New(config.Node{Protocol: p}, config.Front{}, nil); err == nil {
			t.Errorf("%s 不应该由 engine 负责", p)
		}
	}
	for _, p := range []string{config.ProtocolVMess, config.ProtocolVLESS,
		config.ProtocolShadowsocks, config.ProtocolTrojan, config.ProtocolHysteria2} {
		if _, err := New(config.Node{Protocol: p}, config.Front{}, nil); err != nil {
			t.Errorf("%s 应当被 engine 接受，却报错：%v", p, err)
		}
	}
}

// TestBuildCheck 确认当前二进制带着必要标签。
func TestBuildCheck(t *testing.T) {
	if err := BuildCheck(); err != nil {
		t.Fatalf("测试是用 -tags with_utls,with_quic 跑的，BuildCheck 不该失败：%v", err)
	}
	if !strings.Contains(BuildConfigJSONMustString(t), "outbounds") {
		t.Error("生成的配置里应当有 outbounds")
	}
}

// BuildConfigJSONMustString 只是给上面的烟雾断言用的辅助。
func BuildConfigJSONMustString(t *testing.T) string {
	t.Helper()
	raw, err := BuildConfigJSON(config.Node{
		Protocol: config.ProtocolVLESS, Server: "a", Port: 1, UUID: "u",
	}, config.Front{Type: config.FrontTypeDirect})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestBuildOutboundXHTTP 锁死 xhttp 的失败方式。
//
// 背景：实测用户导入了一个 xhttp 传输的 VLESS 节点，我们原样透传给 sing-box，
// 用户看到的是 "unknown transport type: xhttp" —— 只有"哪里错"，没有"怎么办"。
// 现在必须返回中文且可操作的原因（提到内核不支持 + 可以换什么）。
func TestBuildOutboundXHTTP(t *testing.T) {
	n := config.Node{
		ID:       "xhttp-1",
		Name:     "①US1直连出口",
		Protocol: config.ProtocolVLESS,
		Server:   "204.197.163.238",
		Port:     30022,
		UUID:     "ed401d40-9c96-4217-8e6b-ab6ac21169c1",
	}
	n.Transport.Network = "xhttp"
	n.TLS.Enabled = true
	n.TLS.ServerName = "www.amazon.com"

	_, err := buildNodeOutbound(n)
	if err == nil {
		t.Fatal("xhttp 应当返回错误，而不是生成一个内核不认识的配置")
	}
	msg := err.Error()
	for _, want := range []string{"xhttp", "不支持"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息应当包含 %q，实际：%s", want, msg)
		}
	}
	t.Logf("实际错误信息：%s", msg)
}
