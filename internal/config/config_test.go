package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseProxySpec 覆盖用户实际会粘贴的几种写法。
//
// 用户提供的住宅代理格式正是 "user:pass@host:port"，
// 这个解析错了整条链路都不通，因此单独锁死。
//
// 注意：这里用的是**假凭据**。真实凭据不应该出现在任何被 git 跟踪的文件里。
func TestParseProxySpec(t *testing.T) {
	cases := []struct {
		in              string
		typ, user, pass string
		host            string
		port            int
	}{
		{"demo-zone-custom-region-US:ExamplePass123@proxy.example.com:10000",
			"socks5", "demo-zone-custom-region-US", "ExamplePass123", "proxy.example.com", 10000},
		{"proxy.example.com:10000", "socks5", "", "", "proxy.example.com", 10000},
		{"socks5://user:p%40ss@1.2.3.4:1080", "socks5", "user", "p@ss", "1.2.3.4", 1080},
		{"http://user:pass@1.2.3.4:8080", "http", "user", "pass", "1.2.3.4", 8080},
		{"socks5h://example.com", "socks5", "", "", "example.com", 1080},
		{"http://example.com", "http", "", "", "example.com", 80},
	}
	for _, c := range cases {
		typ, user, pass, host, port, err := ParseProxySpec(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if typ != c.typ || user != c.user || pass != c.pass || host != c.host || port != c.port {
			t.Errorf("%q => (%s,%s,%s,%s,%d), want (%s,%s,%s,%s,%d)",
				c.in, typ, user, pass, host, port, c.typ, c.user, c.pass, c.host, c.port)
		}
	}
}

// TestValidatePortConflicts 锁死"本地端口不能重复"这条硬性要求。
func TestValidatePortConflicts(t *testing.T) {
	cfg := Default()
	cfg.Nodes = []Node{
		{ID: "a", Name: "节点1", Protocol: ProtocolSOCKS5, ListenPort: 10810, Server: "h", Port: 1},
		{ID: "b", Name: "节点2", Protocol: ProtocolSOCKS5, ListenPort: 10810, Server: "h", Port: 1},
	}
	issues := cfg.Validate()
	found := false
	for _, i := range issues {
		if i.Field == "listenPort" {
			found = true
		}
	}
	if !found {
		t.Fatalf("重复端口应当报错，实际 issues=%v", issues)
	}
}

// TestValidateRejectsSelfFront 锁死"不能把自己当前置"。
func TestValidateRejectsSelfFront(t *testing.T) {
	cfg := Default()
	cfg.Nodes = []Node{
		{ID: "a", Name: "节点1", Protocol: ProtocolSOCKS5, ListenPort: 10810, Server: "h", Port: 1, Front: "a"},
	}
	issues := cfg.Validate()
	if !hasField(issues, "front") {
		t.Fatalf("自己当前置应当报错，实际 issues=%v", issues)
	}
}

// TestValidateDetectsFrontCycle 锁死"前置链不能成环"。
//
// 成环的后果是激活时无限递归，必须在配置层就拦下来。
func TestValidateDetectsFrontCycle(t *testing.T) {
	cfg := Default()
	cfg.Nodes = []Node{
		{ID: "a", Name: "A", Protocol: ProtocolSOCKS5, ListenPort: 10810, Server: "h", Port: 1, Front: "b"},
		{ID: "b", Name: "B", Protocol: ProtocolSOCKS5, ListenPort: 10811, Server: "h", Port: 1, Front: "a"},
	}
	issues := cfg.Validate()
	if !hasField(issues, "front") {
		t.Fatalf("前置环应当报错，实际 issues=%v", issues)
	}
}

// TestValidateSingleNodeFrontOK 单向前置链是合法的。
func TestValidateSingleNodeFrontOK(t *testing.T) {
	cfg := Default()
	cfg.Nodes = []Node{
		{ID: "a", Name: "A", Protocol: ProtocolSOCKS5, ListenPort: 10810, Server: "h", Port: 1},
		{ID: "b", Name: "B", Protocol: ProtocolVLESS, ListenPort: 10811, Server: "h", Port: 1,
			UUID: "11111111-2222-3333-4444-555555555555", Front: "a"},
	}
	issues := cfg.Validate()
	for _, i := range issues {
		if i.Field == "front" {
			t.Fatalf("合法前置链不应报错：%v", i)
		}
	}
}

// TestValidateMissingFrontNode 引用了不存在的节点应当报错。
func TestValidateMissingFrontNode(t *testing.T) {
	cfg := Default()
	cfg.Nodes = []Node{
		{ID: "b", Name: "B", Protocol: ProtocolSOCKS5, ListenPort: 10811, Server: "h", Port: 1, Front: "nope"},
	}
	issues := cfg.Validate()
	if !hasField(issues, "front") {
		t.Fatalf("引用不存在的前置节点应当报错，实际 issues=%v", issues)
	}
}

// TestValidateProtocolRequiredFields 各协议的必填字段。
func TestValidateProtocolRequiredFields(t *testing.T) {
	cases := []struct {
		name  string
		node  Node
		field string
	}{
		{"vmess 缺 uuid", Node{Protocol: ProtocolVMess, Server: "h", Port: 1}, "uuid"},
		{"vless 缺 uuid", Node{Protocol: ProtocolVLESS, Server: "h", Port: 1}, "uuid"},
		{"ss 缺 method", Node{Protocol: ProtocolShadowsocks, Server: "h", Port: 1, Password: "p"}, "method"},
		{"ss 缺 password", Node{Protocol: ProtocolShadowsocks, Server: "h", Port: 1, Method: "aes-128-gcm"}, "password"},
		{"trojan 缺 password", Node{Protocol: ProtocolTrojan, Server: "h", Port: 1, TLS: TLSOptions{Enabled: true}}, "password"},
		{"trojan 缺 tls", Node{Protocol: ProtocolTrojan, Server: "h", Port: 1, Password: "p"}, "tls"},
		{"hysteria2 缺 password", Node{Protocol: ProtocolHysteria2, Server: "h", Port: 1, TLS: TLSOptions{Enabled: true}}, "password"},
		{"hysteria2 缺 tls", Node{Protocol: ProtocolHysteria2, Server: "h", Port: 1, Password: "p"}, "tls"},
		{"hysteria2 有混淆缺混淆密码", Node{Protocol: ProtocolHysteria2, Server: "h", Port: 1, Password: "p",
			Obfs: "salamander", TLS: TLSOptions{Enabled: true}}, "obfsPassword"},
		{"wireguard 缺配置", Node{Protocol: ProtocolWireGuard, Server: "h", Port: 1}, "wireguard"},
		{"服务器地址为空", Node{Protocol: ProtocolSOCKS5, Port: 1}, "server"},
		{"端口非法", Node{Protocol: ProtocolSOCKS5, Server: "h", Port: 70000}, "port"},
	}
	for i, c := range cases {
		cfg := Default()
		n := c.node
		n.ID = "id"
		n.Name = "n"
		n.ListenPort = 10810 + i
		cfg.Nodes = []Node{n}
		if !hasField(cfg.Validate(), c.field) {
			t.Errorf("%s：应当报字段 %q，实际 %v", c.name, c.field, cfg.Validate())
		}
	}
}

// TestMigrateV1ToV2 锁死旧配置迁移：**行为必须保持**。
//
// 最关键的一条：v1 里"没有 frontOverride"意味着走全局前置，
// 迁移后必须变成 Front=global，否则用户的住宅代理会突然变成直连。
func TestMigrateV1ToV2(t *testing.T) {
	v1 := `{
	  "version": 1,
	  "front": {"type": "socks5", "host": "127.0.0.1", "port": 10808, "timeoutMs": 10000},
	  "upstreams": [
	    {"id": "res-1", "name": "住宅1", "type": "socks5", "enabled": true,
	     "listenHost": "127.0.0.1", "listenPort": 10810,
	     "serverHost": "proxy.example.com", "serverPort": 10000,
	     "username": "u", "password": "p", "timeoutMs": 20000,
	     "frontOverride": {"type": "direct"}},
	    {"id": "res-2", "name": "住宅2", "type": "wireguard", "enabled": true,
	     "listenHost": "127.0.0.1", "listenPort": 10811,
	     "wireguard": {"privateKey": "priv", "address": "10.7.0.2/32",
	                   "peerPublicKey": "pub", "endpointHost": "wg.example.com",
	                   "endpointPort": 51820, "allowedIPs": "0.0.0.0/0",
	                   "persistentKeepalive": 25, "mtu": 1420}}
	  ],
	  "defaultListenHost": "127.0.0.1",
	  "startOnLaunch": true
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("版本应为 %d，实际 %d", CurrentVersion, cfg.Version)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("节点数应为 2，实际 %d", len(cfg.Nodes))
	}

	// 第 1 条：v1 里显式写了 frontOverride=direct → 迁移后应当是直连
	n1 := cfg.Nodes[0]
	if n1.Front != FrontDirect {
		t.Errorf("frontOverride=direct 应迁移为直连，实际 %q", n1.Front)
	}
	if n1.Protocol != ProtocolSOCKS5 || n1.Server != "proxy.example.com" || n1.Port != 10000 {
		t.Errorf("socks5 字段迁移错误：%+v", n1)
	}
	if n1.Username != "u" || n1.Password != "p" || n1.ListenPort != 10810 || !n1.Active {
		t.Errorf("凭据/端口/激活状态迁移错误：%+v", n1)
	}

	// 第 2 条：v1 里没有 frontOverride → 语义是"走全局前置"
	n2 := cfg.Nodes[1]
	if n2.Front != FrontGlobal {
		t.Errorf("未指定 frontOverride 应迁移为全局前置，实际 %q", n2.Front)
	}
	if n2.Protocol != ProtocolWireGuard {
		t.Errorf("协议应为 wireguard，实际 %q", n2.Protocol)
	}
	// 端点必须从 wireguard.endpointHost/Port 搬到 Node.Server/Port
	if n2.Server != "wg.example.com" || n2.Port != 51820 {
		t.Errorf("WireGuard 端点未迁移到 Server/Port：%s:%d", n2.Server, n2.Port)
	}
	if n2.WireGuard == nil || n2.WireGuard.PrivateKey != "priv" || n2.WireGuard.Address != "10.7.0.2/32" {
		t.Errorf("WireGuard 字段迁移错误：%+v", n2.WireGuard)
	}

	// 迁移后的配置必须能通过校验
	if issues := cfg.Validate(); len(issues) > 0 {
		t.Errorf("迁移后的配置不应有问题：%v", issues)
	}

	// 再存再读一次，确认 v2 结构稳定（不会因为往返而丢字段）
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Nodes) != 2 || again.Nodes[1].Server != "wg.example.com" {
		t.Errorf("二次读取丢字段：%+v", again.Nodes)
	}
}

// TestNormalizeFillsDefaults 确认空配置会被补上默认值。
func TestNormalizeFillsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"nodes":[{"name":"x","protocol":"vmess","server":"h","port":443}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	n := cfg.Nodes[0]
	if n.ID == "" {
		t.Error("ID 应当被自动补齐")
	}
	if n.Security != "auto" {
		t.Errorf("VMess 的 security 默认应为 auto，实际 %q", n.Security)
	}
	if n.Transport.Network != "tcp" {
		t.Errorf("传输层默认应为 tcp，实际 %q", n.Transport.Network)
	}
	if n.StatsDay == "" {
		t.Error("StatsDay 应当被补齐")
	}
	if n.CreatedAt.IsZero() {
		t.Error("CreatedAt 应当被补齐")
	}
	if cfg.Settings.PortStart != 10810 {
		t.Errorf("默认起始端口应为 10810，实际 %d", cfg.Settings.PortStart)
	}
}

// TestListenHostEffective 确认 AllowLan 的语义。
func TestListenHostEffective(t *testing.T) {
	cfg := Default()
	n := Node{ListenHost: "127.0.0.1"}
	if got := cfg.ListenHostEffective(n); got != "127.0.0.1" {
		t.Errorf("默认应当只听回环，实际 %q", got)
	}
	cfg.Settings.AllowLan = true
	if got := cfg.ListenHostEffective(n); got != "0.0.0.0" {
		t.Errorf("打开 AllowLan 后应当监听 0.0.0.0，实际 %q", got)
	}
	// 关掉 AllowLan 时，"非回环"一律拉回回环，只有回环地址允许留下。
	//
	// 这组用例是回归护栏：曾经这里只拦 ""/0.0.0.0/::，**具体地址原样返回**，
	// 于是 AllowLan 明明是关的，配置里写个 192.168.1.10 就能把代理绑到
	// 局域网网卡上，等于开了一个不要认证的开放代理。
	cfg.Settings.AllowLan = false
	cases := []struct{ in, want string }{
		{"", "127.0.0.1"},               // 空值 = 未指定 = 所有网卡
		{"0.0.0.0", "127.0.0.1"},        // IPv4 通配
		{"::", "127.0.0.1"},             // IPv6 通配
		{"[::]", "127.0.0.1"},           // 通配 + 方括号写法
		{"192.168.1.10", "127.0.0.1"},   // 具体网卡地址
		{"10.0.0.5", "127.0.0.1"},       // 具体网卡地址
		{" 192.168.1.10 ", "127.0.0.1"}, // 前后空格也不能漏
		{"myhost.local", "127.0.0.1"},   // 主机名无法证明只绑回环
		{"127.0.0.1", "127.0.0.1"},      // 显式回环保留
		{"127.0.0.2", "127.0.0.2"},      // 127.0.0.0/8 都是回环
		{"::1", "::1"},                  // IPv6 回环
		{"localhost", "localhost"},      // 回环名字
	}
	for _, tc := range cases {
		n.ListenHost = tc.in
		if got := cfg.ListenHostEffective(n); got != tc.want {
			t.Errorf("AllowLan 关闭时 listenHost=%q 应当得到 %q，实际 %q", tc.in, tc.want, got)
		}
	}
}

// TestProtocolMetadata 确认协议元信息自洽。
func TestProtocolMetadata(t *testing.T) {
	all := Protocols()
	if len(all) != 8 {
		t.Fatalf("应当有 8 种协议，实际 %d", len(all))
	}
	seen := map[string]bool{}
	for _, p := range all {
		if p.Value == "" || p.Label == "" || p.DefaultPort == 0 {
			t.Errorf("协议元信息不完整：%+v", p)
		}
		if seen[p.Value] {
			t.Errorf("协议重复：%s", p.Value)
		}
		seen[p.Value] = true
	}
	for _, want := range []string{ProtocolSOCKS5, ProtocolHTTP, ProtocolWireGuard,
		ProtocolVMess, ProtocolVLESS, ProtocolShadowsocks, ProtocolTrojan, ProtocolHysteria2} {
		if !seen[want] {
			t.Errorf("缺少协议 %s", want)
		}
		if !IsSupportedProtocol(want) {
			t.Errorf("IsSupportedProtocol(%s) 应为真", want)
		}
	}
	if IsSupportedProtocol("nope") {
		t.Error("未知协议不应被认作受支持")
	}
}

// TestTransportLabel 确认列表「传输协议」列的取值。
func TestTransportLabel(t *testing.T) {
	cases := []struct {
		node Node
		want string
	}{
		{Node{Protocol: ProtocolSOCKS5}, "socks5"},
		{Node{Protocol: ProtocolHTTP}, "http"},
		{Node{Protocol: ProtocolWireGuard}, "udp"},
		{Node{Protocol: ProtocolShadowsocks, Method: "aes-128-gcm"}, "aes-128-gcm"},
		{Node{Protocol: ProtocolHysteria2}, "quic"},
		{Node{Protocol: ProtocolHysteria2, Obfs: "salamander"}, "quic+salamander"},
		{Node{Protocol: ProtocolVLESS, Transport: TransportOptions{Network: "tcp"}}, "tcp"},
		{Node{Protocol: ProtocolVLESS, Transport: TransportOptions{Network: "tcp"},
			TLS: TLSOptions{Enabled: true}}, "tcp+tls"},
		{Node{Protocol: ProtocolVLESS, Transport: TransportOptions{Network: "ws"},
			TLS: TLSOptions{Enabled: true}}, "ws+tls"},
		{Node{Protocol: ProtocolVLESS, Transport: TransportOptions{Network: "tcp"},
			TLS: TLSOptions{Enabled: true, RealityPublicKey: "k"}}, "reality"},
	}
	for _, c := range cases {
		if got := c.node.TransportLabel(); got != c.want {
			t.Errorf("%+v => %q, want %q", c.node, got, c.want)
		}
	}
}

// TestConfigJSONRoundTrip 确认配置序列化不丢字段（尤其是新加的协议字段）。
func TestConfigJSONRoundTrip(t *testing.T) {
	cfg := Default()
	cfg.Nodes = []Node{{
		ID: "x", Name: "n", Protocol: ProtocolVLESS, ListenPort: 10810,
		Server: "h", Port: 443, UUID: "u", Flow: "xtls-rprx-vision",
		Transport: TransportOptions{Network: "ws", Path: "/p", Host: "hh"},
		TLS: TLSOptions{Enabled: true, ServerName: "s", ALPN: []string{"h2"},
			Fingerprint: "chrome", RealityPublicKey: "pbk", RealityShortID: "sid"},
		Front: "global",
	}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var back Config
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	n := back.Nodes[0]
	if n.Flow != "xtls-rprx-vision" || n.Transport.Path != "/p" ||
		n.TLS.RealityPublicKey != "pbk" || len(n.TLS.ALPN) != 1 || n.Front != "global" {
		t.Errorf("往返丢字段：%+v", n)
	}
	if n.TLS.EnabledReality() != true {
		t.Error("EnabledReality 应为真")
	}
}

// TestDataDirIsRepoRoot 锁死"数据目录必须在仓库根下"。
//
// 这个测试是有来历的：`go run` 生成的可执行文件位于 Go 构建缓存里，
// 早先的实现按"exe 同级目录"算数据目录，于是命令行读到的是
// `<缓存目录>\data\config.json`，和界面读到的根本不是同一份配置。
func TestDataDirIsRepoRoot(t *testing.T) {
	got := DataDir()
	// 测试进程的 cwd 就是包目录（internal/config），它带 go.mod 的祖先就是仓库根
	if _, err := os.Stat(filepath.Join(got, "config.json")); err == nil {
		// 文件已存在，说明目录确实是实际使用中的那个
	}
	if !strings.HasSuffix(got, string(filepath.Separator)+"data") {
		t.Fatalf("数据目录应以 data 结尾，实际 %q", got)
	}
	root := filepath.Dir(got)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("数据目录的上一级应当是有 go.mod 的仓库根，实际 %q（err=%v）", root, err)
	}
}

// TestFindRepoRoot 直接测向上找 go.mod 的逻辑。
func TestFindRepoRoot(t *testing.T) {
	// 从本包目录往上找，必须能找到仓库根
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := findRepoRoot(pkgDir)
	if root == "" {
		t.Fatal("应当能从包目录找到仓库根")
	}
	// 【为什么不再断言目录名】目录名是每台机器上的本地细节，换个目录名不该让测试变红。
	// 真正要保证的是"找到的确实是本项目这个仓库根"，而 go.mod 里的 module 路径才是
	// 这个仓库的身份 —— 它跟着代码走，不跟着目录走。
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("读 %s 失败：%v", filepath.Join(root, "go.mod"), err)
	}
	first := strings.TrimSpace(strings.SplitN(string(gomod), "\n", 2)[0])
	if want := "module github.com/murongruolan/picohop"; first != want {
		t.Fatalf("仓库根的 go.mod 第一行应当是 %q，实际 %q", want, first)
	}
	// 从明显不存在 go.mod 的深处往上找也应当能兜住（返回根或空，不能死循环）
	_ = findRepoRoot(filepath.Join(pkgDir, "a", "b", "c"))
}

func hasField(issues []Issue, field string) bool {
	for _, i := range issues {
		if i.Field == field {
			return true
		}
	}
	return false
}

// TestNoRealCredentialsInRepo 是一条自检：确保测试数据里没有真实凭据的痕迹。
//
// 这里的"禁止字符串"是**拼出来的**，不能直接写成完整字面量 ——
// 否则这条检查会命中它自己定义的那一行（踩过这个坑）。
func TestNoRealCredentialsInRepo(t *testing.T) {
	forbidden := []string{"bestgo" + ".work", "fuck" + "oai"}
	dir := filepath.Dir(".")
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, f := range forbidden {
			if strings.Contains(string(data), f) {
				t.Errorf("%s 里出现了疑似真实凭据的痕迹 %q", path, f)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
