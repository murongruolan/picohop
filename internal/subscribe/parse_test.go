package subscribe

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/murongruolan/picohop/internal/config"
	"github.com/murongruolan/picohop/internal/share"
)

// 一份"机场风格"的 YAML：emoji 名、vmess 的 port 写成字符串、reality、ws/grpc、
// hysteria2 的端口跳跃与字符串带宽、ss、以及两类我们要跳过的东西。
const yamlSample = `
port: 7890
mode: rule
proxies:
  - name: "🇺🇸美国洛杉矶1号"
    type: vmess
    server: us1.example.com
    port: "443"
    uuid: 11111111-2222-3333-4444-555555555555
    alterId: 0
    cipher: auto
    tls: true
    network: ws
    ws-opts:
      path: /ws
      headers:
        Host: us1.example.com
  - name: "🇭🇰香港2号"
    type: vless
    server: hk2.example.com
    port: 8443
    uuid: 22222222-3333-4444-5555-666666666666
    flow: xtls-rprx-vision
    tls: true
    servername: hk2.example.com
    client-fingerprint: chrome
    reality-opts:
      public-key: PUBKEY123
      short-id: ab12
    network: grpc
    grpc-opts:
      grpc-service-name: gs
  - name: "🇯🇵日本3号"
    type: trojan
    server: jp3.example.com
    port: 443
    password: pw3
    sni: jp3.example.com
    skip-cert-verify: true
    alpn: [h2, http/1.1]
  - name: "🇸🇬新加坡4号"
    type: ss
    server: sg4.example.com
    port: 8388
    cipher: aes-256-gcm
    password: pw4
  - name: "🇩🇪德国5号"
    type: hysteria2
    server: de5.example.com
    port: 443
    password: pw5
    obfs: salamander
    obfs-password: obfspw
    up: "50 Mbps"
    down: "200 Mbps"
    ports: "20000-30000"
    fingerprint: AABBCCDD
  - name: "🇫🇷法国6号"
    type: tuic
    server: fr6.example.com
    port: 443
    uuid: 33333333-4444-5555-6666-777777777777
    password: pw6
  - name: "🇬🇧英国7号"
    type: vless
    server: uk7.example.com
    port: 443
    uuid: 44444444-5555-6666-7777-888888888888
    encryption: mlkem768x25519plus
    tls: true
proxy-groups:
  - name: "PROXY"
    type: select
    proxies: ["🇺🇸美国洛杉矶1号"]
rules:
  - MATCH,PROXY
`

func mustParse(t *testing.T, body string) Result {
	t.Helper()
	r, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse 失败：%v", err)
	}
	return r
}

// TestParseClashYAML 覆盖字段映射：它是整个订阅功能里最容易悄悄错的一块。
func TestParseClashYAML(t *testing.T) {
	r := mustParse(t, yamlSample)
	if r.Sketch.Format != FormatClashYAML {
		t.Fatalf("形态识别为 %q，期望 %q", r.Sketch.Format, FormatClashYAML)
	}
	if r.Sketch.Usable != 5 {
		t.Fatalf("可用节点 %d 个，期望 5（tuic 跳过、vless-encryption 跳过）", r.Sketch.Usable)
	}
	if r.Sketch.Skipped["tuic"] != 1 {
		t.Fatalf("tuic 跳过计数 %d，期望 1", r.Sketch.Skipped["tuic"])
	}
	if r.Sketch.Skipped["vless"] != 1 {
		t.Fatalf("带 encryption 的 vless 应被跳过，计数 %d，期望 1", r.Sketch.Skipped["vless"])
	}
	foundNote := false
	for _, n := range r.Sketch.Notes {
		if strings.Contains(n, "encryption") {
			foundNote = true
		}
	}
	if !foundNote {
		t.Fatal("跳过的原因应当记进 Notes，方便排查")
	}

	byName := map[string]config.Node{}
	for _, n := range r.Nodes {
		byName[n.Name] = n
	}

	// vmess：port 是字符串 "443"，必须照样解出 443；ws 的 path/Host 要落位
	vm := byName["🇺🇸美国洛杉矶1号"]
	if vm.Protocol != config.ProtocolVMess || vm.Port != 443 {
		t.Fatalf("vmess 映射错：protocol=%s port=%d", vm.Protocol, vm.Port)
	}
	if vm.UUID != "11111111-2222-3333-4444-555555555555" || vm.Security != "auto" {
		t.Fatalf("vmess 凭据/加密映射错：%+v", vm)
	}
	if vm.Transport.Network != "ws" || vm.Transport.Path != "/ws" || vm.Transport.Host != "us1.example.com" {
		t.Fatalf("ws 传输映射错：%+v", vm.Transport)
	}
	if !vm.TLS.Enabled {
		t.Fatal("vmess 应启用 TLS")
	}

	// vless：reality + grpc + flow + uTLS 指纹
	vl := byName["🇭🇰香港2号"]
	if vl.Protocol != config.ProtocolVLESS || vl.Flow != "xtls-rprx-vision" {
		t.Fatalf("vless 映射错：%+v", vl)
	}
	if vl.TLS.RealityPublicKey != "PUBKEY123" || vl.TLS.RealityShortID != "ab12" {
		t.Fatalf("reality 映射错：%+v", vl.TLS)
	}
	if vl.TLS.Fingerprint != "chrome" || vl.TLS.ServerName != "hk2.example.com" {
		t.Fatalf("uTLS/SNI 映射错：%+v", vl.TLS)
	}
	if vl.Transport.ServiceName != "gs" {
		t.Fatalf("grpc service-name 映射错：%+v", vl.Transport)
	}

	// trojan：跳过证书校验 + alpn
	tj := byName["🇯🇵日本3号"]
	if tj.Protocol != config.ProtocolTrojan || !tj.TLS.AllowInsecure || len(tj.TLS.ALPN) != 2 {
		t.Fatalf("trojan 映射错：%+v", tj)
	}

	// ss：cipher → Method
	ss := byName["🇸🇬新加坡4号"]
	if ss.Protocol != config.ProtocolShadowsocks || ss.Method != "aes-256-gcm" || ss.Password != "pw4" {
		t.Fatalf("ss 映射错：%+v", ss)
	}

	// hysteria2：端口跳跃、字符串带宽换算、obfs、证书 pin
	hy := byName["🇩🇪德国5号"]
	if hy.Protocol != config.ProtocolHysteria2 {
		t.Fatalf("hysteria2 协议错：%+v", hy)
	}
	if hy.UpMbps != 50 || hy.DownMbps != 200 {
		t.Fatalf("带宽换算错：up=%d down=%d（期望 50/200）", hy.UpMbps, hy.DownMbps)
	}
	if hy.Ports != "20000-30000" || hy.Obfs != "salamander" || hy.ObfsPassword != "obfspw" {
		t.Fatalf("hysteria2 细节映射错：%+v", hy)
	}
	if hy.PinSHA256 != "AABBCCDD" {
		t.Fatalf("hysteria2 证书 pin 映射错：%+v", hy)
	}
}

// TestFakeInfoRowIsFilteredBeforeDedup 守住真实机场的那个坑。
//
// 实测：机场把"剩余流量：144.86 GB"做成**克隆真节点连接信息**的条目，而且排在真节点**前面**。
// 如果实现里先去重，留下的就是这条信息行、真节点被当成重复项丢掉 —— 这个测试就是钉住顺序。
func TestFakeInfoRowIsFilteredBeforeDedup(t *testing.T) {
	// 两条完全相同的连接信息：第一条是伪信息行，第二条才是真节点
	const body = `
proxies:
  - name: "剩余流量：144.86 GB"
    type: hysteria2
    server: 198.255.5.156
    port: 50000
    password: same-pw
  - name: "套餐到期：长期有效"
    type: hysteria2
    server: 198.255.5.156
    port: 50000
    password: same-pw
  - name: "🇺🇸美国洛杉矶1号"
    type: hysteria2
    server: 198.255.5.156
    port: 50000
    password: same-pw
`
	r := mustParse(t, body)
	if r.Sketch.FakeRows != 2 {
		t.Fatalf("伪信息行过滤数 %d，期望 2", r.Sketch.FakeRows)
	}
	if r.Sketch.Usable != 1 {
		t.Fatalf("可用节点 %d，期望 1", r.Sketch.Usable)
	}
	if got := r.Nodes[0].Name; got != "🇺🇸美国洛杉矶1号" {
		t.Fatalf("存活下来的是 %q —— 说明先去重后过滤，顺序反了", got)
	}
}

// TestFakeRowKeywordsDoNotEatRealNodes 反向验证：别把"带流量字样的真节点"误杀。
func TestFakeRowKeywordsDoNotEatRealNodes(t *testing.T) {
	for _, name := range []string{"🇺🇸美国 大流量节点", "机场专线-香港", "🇯🇵日本 IEPL 直连"} {
		if isFakeRow(name) {
			t.Fatalf("真节点名 %q 被当成伪信息行误杀", name)
		}
	}
	for _, name := range []string{"剩余流量：144.86 GB", "套餐到期：长期有效", "距离下次重置：3 天", "官网：example.com"} {
		if !isFakeRow(name) {
			t.Fatalf("伪信息行 %q 没被识别", name)
		}
	}
}

func testLinks() []string {
	out := []string{}
	for _, n := range []config.Node{
		{Protocol: config.ProtocolVLESS, Name: "🇺🇸美国01", Server: "us1.example.com", Port: 443, UUID: "11111111-2222-3333-4444-555555555555"},
		{Protocol: config.ProtocolTrojan, Name: "🇭🇰香港02", Server: "hk2.example.com", Port: 8443, Password: "pw2"},
		{Protocol: config.ProtocolShadowsocks, Name: "🇯🇵日本03", Server: "jp3.example.com", Port: 8388, Password: "pw3", Method: "aes-256-gcm"},
	} {
		l, err := share.Encode(n)
		if err != nil {
			panic(err)
		}
		out = append(out, l)
	}
	return out
}

// TestParseLinksPlainAndBase64 覆盖分享链接那条路：明文、标准 base64、
// URL-safe 无 padding、以及夹了空白/换行的 base64（机场三种写法都见过）。
func TestParseLinksPlainAndBase64(t *testing.T) {
	plain := strings.Join(testLinks(), "\n") + "\n"

	cases := map[string]string{
		"明文":                 plain,
		"标准 base64":          base64.StdEncoding.EncodeToString([]byte(plain)),
		"URL-safe 无 padding": strings.TrimRight(base64.URLEncoding.EncodeToString([]byte(plain)), "="),
		"夹空白与换行的 base64":     chunk(base64.StdEncoding.EncodeToString([]byte(plain)), 40),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			r, err := Parse([]byte(body))
			if err != nil {
				t.Fatalf("解析失败：%v", err)
			}
			if r.Sketch.Usable != 3 {
				t.Fatalf("可用节点 %d，期望 3（形态=%s）", r.Sketch.Usable, r.Sketch.Format)
			}
			if r.Sketch.Format != FormatLinks {
				t.Fatalf("形态识别为 %q", r.Sketch.Format)
			}
		})
	}
}

// chunk 把长串按 n 个字符换行（模拟 MIME 风格换行）。
func chunk(s string, n int) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && i%n == 0 {
			b.WriteString("\r\n")
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestParseSIP008 覆盖 Shadowsocks 官方订阅格式的两种外形。
func TestParseSIP008(t *testing.T) {
	objects := `{"version":1,"servers":[{"server":"a.example.com","server_port":8388,"method":"aes-256-gcm","password":"p1","remarks":"🇺🇸A"},{"server":"b.example.com","server_port":8389,"method":"chacha20-ietf-poly1305","password":"p2","remarks":"🇭🇰B"}]}`
	array := `[{"server":"a.example.com","server_port":8388,"method":"aes-256-gcm","password":"p1","remarks":"🇺🇸A"}]`
	for name, body := range map[string]string{"对象外形": objects, "裸数组": array} {
		t.Run(name, func(t *testing.T) {
			r, err := Parse([]byte(body))
			if err != nil {
				t.Fatalf("解析失败：%v", err)
			}
			if r.Sketch.Format != FormatSIP008 {
				t.Fatalf("形态识别为 %q，期望 %q", r.Sketch.Format, FormatSIP008)
			}
			if r.Sketch.Usable == 0 {
				t.Fatal("没有解析出节点")
			}
			if r.Nodes[0].Protocol != config.ProtocolShadowsocks {
				t.Fatalf("协议应为 shadowsocks，实际 %q", r.Nodes[0].Protocol)
			}
		})
	}
}

// TestParseFailures 三种"失败"必须能区分开：这决定用户该怎么修。
func TestParseFailures(t *testing.T) {
	if _, err := Parse([]byte("   \n  ")); !errors.Is(err, ErrEmpty) {
		t.Fatalf("空内容应返回 ErrEmpty，实际 %v", err)
	}
	if _, err := Parse([]byte("<html><head><title>404</title></head></html>")); !errors.Is(err, ErrHTML) {
		t.Fatalf("HTML 应返回 ErrHTML，实际 %v", err)
	}
	if _, err := Parse([]byte("这不是订阅，只是一段中文说明。")); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("认不出的内容应返回 ErrUnknownFormat，实际 %v", err)
	}
}

// TestMaskURL 订阅链接是账号凭据，日志与界面里必须打码。
func TestMaskURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://a.example.com/api/v1/client/subscribe?token=deadbeefdeadbeef", "https://a.example.com/api/v1/client/subscribe?***"},
		{"https://b.example.com/sub/token1234567890abcdef", "https://b.example.com/sub/***"},
		{"https://c.example.com/x.yaml", "https://c.example.com/x.yaml"},
		{"", ""},
	}
	for _, c := range cases {
		if got := MaskURL(c.in); got != c.want {
			t.Fatalf("MaskURL(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
	// 打码后绝不能还带着 token
	if got := MaskURL("https://a.example.com/sub?token=deadbeefdeadbeef"); strings.Contains(got, "deadbeef") {
		t.Fatalf("打码后仍含 token：%q", got)
	}
}

// TestDuplicateWithinSubscription 订阅内重复的节点只留一条（实测机场会重复发同一节点）。
func TestDuplicateWithinSubscription(t *testing.T) {
	const body = `
proxies:
  - {name: "🇺🇸A", type: ss, server: a.example.com, port: 8388, cipher: aes-256-gcm, password: p}
  - {name: "🇺🇸A 副本", type: ss, server: a.example.com, port: 8388, cipher: aes-256-gcm, password: p}
`
	r := mustParse(t, body)
	if r.Sketch.Duplicated != 1 || r.Sketch.Usable != 1 {
		t.Fatalf("去重结果：Duplicated=%d Usable=%d，期望 1/1", r.Sketch.Duplicated, r.Sketch.Usable)
	}
}

// TestInvalidEntries 字段不全的条目要丢掉并计数（例如没有 server 或端口越界）。
func TestInvalidEntries(t *testing.T) {
	const body = `
proxies:
  - {name: "🇺🇸无端口", type: ss, server: a.example.com, cipher: aes-256-gcm, password: p}
  - {name: "🇺🇸端口越界", type: ss, server: b.example.com, port: 70000, cipher: aes-256-gcm, password: p}
  - {name: "🇺🇸正常", type: ss, server: c.example.com, port: 8388, cipher: aes-256-gcm, password: p}
`
	r := mustParse(t, body)
	if r.Sketch.Invalid != 2 {
		t.Fatalf("无效条目计数 %d，期望 2", r.Sketch.Invalid)
	}
	if r.Sketch.Usable != 1 {
		t.Fatalf("可用节点 %d，期望 1", r.Sketch.Usable)
	}
}

// TestSortedSkipped 跳过清单要按稳定顺序给界面用。
func TestSortedSkipped(t *testing.T) {
	sk := Sketch{Skipped: map[string]int{"tuic": 2, "ssr": 1, "anytls": 3}}
	got := sk.SortedSkipped()
	want := []string{"anytls×3", "ssr×1", "tuic×2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("SortedSkipped = %v，期望 %v", got, want)
	}
}
