package share

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image/png"
	"reflect"
	"strings"
	"testing"

	"chainproxy/internal/config"
)

// ------------------------------------------------------------------
// 冻结的样本：真实客户端会产生的链接形态
// ------------------------------------------------------------------
//
// 这些样本是手写并固化在测试里的，**不是**用本包的 Encode 现场生成的：
// 自己编自己解只能证明"自洽"，证明不了能读懂别人的链接。
// 里面的服务器地址、UUID、密钥全是编的，不含任何真实节点信息。
const (
	// vmess（主流客户端风格：port/aid 是字符串，全部字段都写出来）：
	// ws + tls，ps="香港 ws 节点"，alpn="h2,http/1.1"，fp=chrome
	sampleVMessStd = "eyJ2IjoiMiIsInBzIjoi6aaZ5rivIHdzIOiKgueCuSIsImFkZCI6InZtZXNzLmV4YW1wbGUuY29tIiwicG9ydCI6IjQ0MyIsImlkIjoiYjgzMTM4MWQtNjMyNC00ZDUzLWFkNGYtOGNkYTQ4YjMwODExIiwiYWlkIjoiMCIsInNjeSI6ImF1dG8iLCJuZXQiOiJ3cyIsInR5cGUiOiJub25lIiwiaG9zdCI6InZtZXNzLmV4YW1wbGUuY29tIiwicGF0aCI6Ii9yYXkiLCJ0bHMiOiJ0bHMiLCJzbmkiOiJ2bWVzcy5leGFtcGxlLmNvbSIsImFscG4iOiJoMixodHRwLzEuMSIsImZwIjoiY2hyb21lIn0="

	// vmess：字段名全大写 + port/aid 是数字（老客户端/手改过的链接长这样）
	sampleVMessUpper = "eyJWIjoiMiIsIlBTIjoiVXBwZXJDYXNlIE5vZGUiLCJBREQiOiJ1cHBlci5leGFtcGxlLmNvbSIsIlBPUlQiOjg0NDMsIklEIjoiZDNmMWEyYjQtNWM2ZC00ZTdmLThhOWItMGMxZDJlM2Y0YTViIiwiQUlEIjowLCJTQ1kiOiJhZXMtMTI4LWdjbSIsIk5FVCI6InRjcCIsIlRZUEUiOiJodHRwIiwiSE9TVCI6InVwcGVyLmV4YW1wbGUuY29tIiwiUEFUSCI6Ii8iLCJUTFMiOiJ0bHMiLCJTTkkiOiJ1cHBlci5leGFtcGxlLmNvbSIsIkFMUE4iOiJoMiIsIkZQIjoiZmlyZWZveCJ9"

	// ss SIP002 的 userinfo：base64url("aes-256-gcm:Password123")，无 padding
	sampleSSUserinfo = "YWVzLTI1Ni1nY206UGFzc3dvcmQxMjM"

	// ss 旧格式：base64("chacha20-ietf-poly1305:pwd123@5.6.7.8:8389")，无 padding
	sampleSSLegacy = "Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTpwd2QxMjNANS42LjcuODo4Mzg5"

	// 编的 Reality 公钥（x25519 公钥的 base64url 形态，43 字符）
	sampleRealityPubKey = "jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0"

	sampleVLESSURL = "vless://b831381d-6324-4d53-ad4f-8cda48b30811@reality.example.com:443" +
		"?type=tcp&security=reality&pbk=" + sampleRealityPubKey +
		"&fp=chrome&sni=www.microsoft.com&sid=6ba85179e30d4fc2&spx=%2F&flow=xtls-rprx-vision&mport=443" +
		"#Reality%20%E7%A4%BA%E4%BE%8B"

	sampleTrojanURL = "trojan://tr0jan%40pass%3Aword@trojan.example.com:443" +
		"?security=tls&type=ws&path=%2Fws%3Fed%3D2048&host=trojan.example.com" +
		"&sni=trojan.example.com&allowInsecure=1&alpn=h2%2Chttp%2F1.1&fp=chrome#Trojan%20%E7%A4%BA%E4%BE%8B%20%231"

	sampleHysteria2URL = "hysteria2://letmein%40hy2@hy2.example.com:443" +
		"?obfs=salamander&obfs-password=obfspwd&sni=hy2.example.com&insecure=1" +
		"&up=120&down=380&pinSHA256=aB3%2B%2FxY%3Dz&alpn=h3#HY2%20%E7%A4%BA%E4%BE%8B"

	sampleHy2AliasURL = "hy2://pwd123@h2.example.com:8443#%E7%9F%AD%E5%88%AB%E5%90%8D"
)

// sampleVMessNoPad / sampleVMessURLSafe 是同一份 JSON 的另外两种 base64 变体，
// 从 sampleVMessStd 机械变换得到（保持"四种变体都从同一份真实内容来"）。
var (
	sampleVMessNoPad   = strings.TrimRight(sampleVMessStd, "=")
	sampleVMessURLSafe = strings.NewReplacer("+", "-", "/", "_").Replace(sampleVMessNoPad)
)

// schemeOf 返回协议在分享链接里的 scheme（注意 shadowsocks 用的是 ss）。
func schemeOf(proto string) string {
	if proto == config.ProtocolShadowsocks {
		return "ss"
	}
	return proto
}

func mustDecode(t *testing.T, link string) config.Node {
	t.Helper()
	n, err := Decode(link)
	if err != nil {
		t.Fatalf("Decode(%s) 失败: %v", truncate(link, 80), err)
	}
	return n
}

// sameNode 逐字段比对解析结果。
//
// 用显式字段列表而不是 reflect.DeepEqual(整个结构)：失败信息能直接说出
// 是哪个字段不对，而且"哪些字段本该被填"这件事本身也成了测试的一部分。
func sameNode(t *testing.T, got, want config.Node) {
	t.Helper()
	fields := []struct {
		name string
		got  any
		want any
	}{
		{"Name", got.Name, want.Name},
		{"Protocol", got.Protocol, want.Protocol},
		{"Server", got.Server, want.Server},
		{"Port", got.Port, want.Port},
		{"UUID", got.UUID, want.UUID},
		{"AlterID", got.AlterID, want.AlterID},
		{"Security", got.Security, want.Security},
		{"Flow", got.Flow, want.Flow},
		{"Method", got.Method, want.Method},
		{"Password", got.Password, want.Password},
		{"Transport.Network", got.Transport.Network, want.Transport.Network},
		{"Transport.Path", got.Transport.Path, want.Transport.Path},
		{"Transport.Host", got.Transport.Host, want.Transport.Host},
		{"Transport.ServiceName", got.Transport.ServiceName, want.Transport.ServiceName},
		{"Transport.HeaderType", got.Transport.HeaderType, want.Transport.HeaderType},
		{"TLS.Enabled", got.TLS.Enabled, want.TLS.Enabled},
		{"TLS.ServerName", got.TLS.ServerName, want.TLS.ServerName},
		{"TLS.ALPN", got.TLS.ALPN, want.TLS.ALPN},
		{"TLS.AllowInsecure", got.TLS.AllowInsecure, want.TLS.AllowInsecure},
		{"TLS.Fingerprint", got.TLS.Fingerprint, want.TLS.Fingerprint},
		{"TLS.RealityPublicKey", got.TLS.RealityPublicKey, want.TLS.RealityPublicKey},
		{"TLS.RealityShortID", got.TLS.RealityShortID, want.TLS.RealityShortID},
		{"TLS.RealitySpiderX", got.TLS.RealitySpiderX, want.TLS.RealitySpiderX},
		{"UpMbps", got.UpMbps, want.UpMbps},
		{"DownMbps", got.DownMbps, want.DownMbps},
		{"Obfs", got.Obfs, want.Obfs},
		{"ObfsPassword", got.ObfsPassword, want.ObfsPassword},
		{"PinSHA256", got.PinSHA256, want.PinSHA256},
	}
	for _, f := range fields {
		if !reflect.DeepEqual(f.got, f.want) {
			t.Errorf("%s: 得到 %#v，期望 %#v", f.name, f.got, f.want)
		}
	}
	// 本机状态字段一个都不能被链接带进来：导入时本地端口要单独弹窗设置，
	// 被别人的链接覆盖会直接和用户现有配置打架。
	if got.ListenHost != "" || got.ListenPort != 0 || got.Front != "" || got.Active ||
		got.StatsDay != "" || !got.CreatedAt.IsZero() || got.WireGuard != nil ||
		got.UpBytes != 0 || got.DownBytes != 0 {
		t.Errorf("解析结果里混进了本机状态字段: %+v", got)
	}
}

// ------------------------------------------------------------------
// 1. 往返测试
// ------------------------------------------------------------------

// TestEncodeDecodeRoundTrip 给每个支持的协议构造"字段填满"的节点，
// Encode → Decode 后逐字段比对，并要求二次编码得到完全相同的链接。
func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   config.Node
		// want 留空表示"应与 in 相同"（传输层留空时解析端会补 tcp）。
		want config.Node
	}{
		{
			name: "vmess tcp + headerType http + TLS",
			in: config.Node{
				Name: "vmess tcp 示例", Protocol: config.ProtocolVMess,
				Server: "1.2.3.4", Port: 8080,
				UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", AlterID: 64, Security: "auto",
				Transport: config.TransportOptions{Network: "tcp", HeaderType: "http", Path: "/", Host: "cdn.example.com"},
				TLS: config.TLSOptions{
					Enabled: true, ServerName: "cdn.example.com",
					ALPN: []string{"h2", "http/1.1"}, Fingerprint: "chrome",
				},
			},
		},
		{
			name: "vmess ws + TLS",
			in: config.Node{
				Name: "vmess ws 示例", Protocol: config.ProtocolVMess,
				Server: "ws.example.com", Port: 443,
				UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Security: "aes-128-gcm",
				Transport: config.TransportOptions{Network: "ws", Path: "/ray?ed=2048", Host: "ws.example.com"},
				TLS:       config.TLSOptions{Enabled: true, ServerName: "ws.example.com"},
			},
		},
		{
			name: "vmess grpc + 别名含 & 和 #",
			in: config.Node{
				Name: "香港 A & B #1 (中转)", Protocol: config.ProtocolVMess,
				Server: "grpc.example.com", Port: 8443,
				UUID: "d3f1a2b4-5c6d-4e7f-8a9b-0c1d2e3f4a5b", Security: "auto",
				Transport: config.TransportOptions{Network: "grpc", ServiceName: "grpc-svc"},
				TLS:       config.TLSOptions{Enabled: true, ServerName: "grpc.example.com", Fingerprint: "firefox"},
			},
		},
		{
			name: "vless reality + flow",
			in: config.Node{
				Name: "reality 节点", Protocol: config.ProtocolVLESS,
				Server: "reality.example.com", Port: 443,
				UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Flow: "xtls-rprx-vision",
				Transport: config.TransportOptions{Network: "tcp"},
				TLS: config.TLSOptions{
					Enabled: true, ServerName: "www.microsoft.com", Fingerprint: "chrome",
					RealityPublicKey: sampleRealityPubKey, RealityShortID: "6ba85179e30d4fc2",
					RealitySpiderX: "/",
				},
			},
		},
		{
			name: "vless ws + TLS + ALPN + allowInsecure",
			in: config.Node{
				Name: "vless ws", Protocol: config.ProtocolVLESS,
				Server: "vless.example.com", Port: 443,
				UUID:      "b831381d-6324-4d53-ad4f-8cda48b30811",
				Transport: config.TransportOptions{Network: "ws", Path: "/ws", Host: "vless.example.com"},
				TLS: config.TLSOptions{
					Enabled: true, ServerName: "vless.example.com", AllowInsecure: true,
					ALPN: []string{"h2", "http/1.1"},
				},
			},
		},
		{
			name: "vless tcp 明文（security=none 往返）",
			in: config.Node{
				Name: "vless 明文", Protocol: config.ProtocolVLESS,
				Server: "plain.example.com", Port: 80,
				UUID:      "b831381d-6324-4d53-ad4f-8cda48b30811",
				Transport: config.TransportOptions{Network: "tcp", HeaderType: "http", Path: "/", Host: "plain.example.com"},
			},
		},
		{
			name: "trojan ws + TLS",
			in: config.Node{
				Name: "trojan 示例", Protocol: config.ProtocolTrojan,
				Server: "trojan.example.com", Port: 443, Password: "tr0jan@pass:word/1",
				Transport: config.TransportOptions{Network: "ws", Path: "/ws?ed=2048", Host: "trojan.example.com"},
				TLS: config.TLSOptions{
					Enabled: true, ServerName: "trojan.example.com", AllowInsecure: true,
					ALPN: []string{"h2"}, Fingerprint: "safari",
				},
			},
		},
		{
			name: "trojan grpc + TLS",
			in: config.Node{
				Name: "trojan grpc", Protocol: config.ProtocolTrojan,
				Server: "trojan.example.com", Port: 8443, Password: "pwd123",
				Transport: config.TransportOptions{Network: "grpc", ServiceName: "tj-svc"},
				TLS:       config.TLSOptions{Enabled: true, ServerName: "trojan.example.com"},
			},
		},
		{
			name: "shadowsocks SIP002",
			in: config.Node{
				Name: "ss 示例", Protocol: config.ProtocolShadowsocks,
				Server: "ss.example.com", Port: 8388,
				Method: "aes-256-gcm", Password: "p@ss:w/ord+x",
				// ss 链接里没有传输层/ TLS 的概念，解析端会把空 Network 补成 tcp。
			},
		},
		{
			name: "hysteria2 全字段",
			in: config.Node{
				Name: "hy2 示例", Protocol: config.ProtocolHysteria2,
				Server: "hy2.example.com", Port: 443, Password: "letmein@hy2",
				UpMbps: 120, DownMbps: 380,
				Obfs: "salamander", ObfsPassword: "obfspwd",
				PinSHA256: "aB3+/xY=z9Kq",
				TLS: config.TLSOptions{
					Enabled: true, ServerName: "hy2.example.com", AllowInsecure: true,
					ALPN: []string{"h3"}, Fingerprint: "chrome",
				},
			},
		},
		{
			name: "hysteria2 最小字段",
			in: config.Node{
				Name: "hy2 最小", Protocol: config.ProtocolHysteria2,
				Server: "hy2.example.com", Port: 8443, Password: "pwd",
				TLS: config.TLSOptions{Enabled: true},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := c.want
			if want.Protocol == "" {
				want = c.in
			}
			if want.Transport.Network == "" {
				// 解析端的归一化：空 Network 一律补 tcp。
				want.Transport.Network = "tcp"
			}

			link, err := Encode(c.in)
			if err != nil {
				t.Fatalf("Encode 失败: %v", err)
			}
			if !strings.HasPrefix(link, schemeOf(c.in.Protocol)+"://") {
				t.Errorf("链接前缀不是 %s:// : %s", schemeOf(c.in.Protocol), link)
			}
			got, err := Decode(link)
			if err != nil {
				t.Fatalf("Decode(%s) 失败: %v", link, err)
			}
			sameNode(t, got, want)

			// 二次编码必须得到逐字节相同的链接。这条比字段比对更狠：
			// 能抓出"字段对了但归一化不对称/有损"的问题。
			again, err := Encode(got)
			if err != nil {
				t.Fatalf("二次 Encode 失败: %v", err)
			}
			if again != link {
				t.Errorf("二次编码结果不一致:\n第一次: %s\n第二次: %s", link, again)
			}
		})
	}
}

// TestEncodeFragmentSpace 锁死别名里的空格编码成 %20 而不是 '+'。
//
// fragment 不是表单，客户端按百分号解码，'+' 会原样留下变成加号，
// 别名就成了 "香港+节点"，用户看到会莫名其妙。
func TestEncodeFragmentSpace(t *testing.T) {
	link, err := Encode(config.Node{
		Protocol: config.ProtocolVLESS, Server: "a.example.com", Port: 443,
		UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Name: "香港 节点 1",
		TLS: config.TLSOptions{Enabled: true},
	})
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}
	i := strings.Index(link, "#")
	if i < 0 {
		t.Fatalf("别名丢失: %s", link)
	}
	fragment := link[i+1:]
	if strings.Contains(fragment, "+") || !strings.Contains(fragment, "%20") {
		t.Errorf("别名里的空格必须编码成 %%20: %s", fragment)
	}
	if got := mustDecode(t, link).Name; got != "香港 节点 1" {
		t.Errorf("别名往返不一致: %q", got)
	}
}

// ------------------------------------------------------------------
// 2. 真实样本测试
// ------------------------------------------------------------------

func TestDecodeRealSamples(t *testing.T) {
	t.Run("vmess 标准 base64(JSON)", func(t *testing.T) {
		sameNode(t, mustDecode(t, "vmess://"+sampleVMessStd), config.Node{
			Name: "香港 ws 节点", Protocol: config.ProtocolVMess,
			Server: "vmess.example.com", Port: 443,
			UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Security: "auto",
			Transport: config.TransportOptions{Network: "ws", Host: "vmess.example.com", Path: "/ray"},
			TLS: config.TLSOptions{
				Enabled: true, ServerName: "vmess.example.com", Fingerprint: "chrome",
				ALPN: []string{"h2", "http/1.1"},
			},
		})
	})

	t.Run("vmess 全大写字段 + 数字 port/aid", func(t *testing.T) {
		sameNode(t, mustDecode(t, "vmess://"+sampleVMessUpper), config.Node{
			Name: "UpperCase Node", Protocol: config.ProtocolVMess,
			Server: "upper.example.com", Port: 8443,
			UUID: "d3f1a2b4-5c6d-4e7f-8a9b-0c1d2e3f4a5b", Security: "aes-128-gcm",
			Transport: config.TransportOptions{
				Network: "tcp", HeaderType: "http", Host: "upper.example.com", Path: "/",
			},
			TLS: config.TLSOptions{
				Enabled: true, ServerName: "upper.example.com",
				ALPN: []string{"h2"}, Fingerprint: "firefox",
			},
		})
	})

	t.Run("ss SIP002", func(t *testing.T) {
		sameNode(t, mustDecode(t, "ss://"+sampleSSUserinfo+"@ss.example.com:8388#SS%20SIP002%20%E7%A4%BA%E4%BE%8B"),
			config.Node{
				Name: "SS SIP002 示例", Protocol: config.ProtocolShadowsocks,
				Server: "ss.example.com", Port: 8388,
				Method: "aes-256-gcm", Password: "Password123",
				// ss 本身没有传输层，解析端的归一化会把空 Network 补成 tcp。
				Transport: config.TransportOptions{Network: "tcp"},
			})
	})

	t.Run("ss 旧格式", func(t *testing.T) {
		sameNode(t, mustDecode(t, "ss://"+sampleSSLegacy+"#SS%20Legacy"), config.Node{
			Name: "SS Legacy", Protocol: config.ProtocolShadowsocks,
			Server: "5.6.7.8", Port: 8389,
			Method: "chacha20-ietf-poly1305", Password: "pwd123",
			Transport: config.TransportOptions{Network: "tcp"},
		})
	})

	t.Run("vless + reality", func(t *testing.T) {
		sameNode(t, mustDecode(t, sampleVLESSURL), config.Node{
			Name: "Reality 示例", Protocol: config.ProtocolVLESS,
			Server: "reality.example.com", Port: 443,
			UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Flow: "xtls-rprx-vision",
			Transport: config.TransportOptions{Network: "tcp"},
			TLS: config.TLSOptions{
				Enabled: true, ServerName: "www.microsoft.com", Fingerprint: "chrome",
				RealityPublicKey: sampleRealityPubKey, RealityShortID: "6ba85179e30d4fc2",
				RealitySpiderX: "/",
			},
		})
	})

	t.Run("trojan + ws + 转义密码", func(t *testing.T) {
		sameNode(t, mustDecode(t, sampleTrojanURL), config.Node{
			Name: "Trojan 示例 #1", Protocol: config.ProtocolTrojan,
			Server: "trojan.example.com", Port: 443,
			Password:  "tr0jan@pass:word",
			Transport: config.TransportOptions{Network: "ws", Path: "/ws?ed=2048", Host: "trojan.example.com"},
			TLS: config.TLSOptions{
				Enabled: true, ServerName: "trojan.example.com", AllowInsecure: true,
				ALPN: []string{"h2", "http/1.1"}, Fingerprint: "chrome",
			},
		})
	})

	t.Run("hysteria2 + obfs + 带宽", func(t *testing.T) {
		sameNode(t, mustDecode(t, sampleHysteria2URL), config.Node{
			Name: "HY2 示例", Protocol: config.ProtocolHysteria2,
			Server: "hy2.example.com", Port: 443, Password: "letmein@hy2",
			UpMbps: 120, DownMbps: 380,
			Obfs: "salamander", ObfsPassword: "obfspwd",
			PinSHA256: "aB3+/xY=z",
			TLS: config.TLSOptions{
				Enabled: true, ServerName: "hy2.example.com",
				ALPN: []string{"h3"}, AllowInsecure: true,
			},
			Transport: config.TransportOptions{Network: "tcp"},
		})
	})

	t.Run("hy2 别名", func(t *testing.T) {
		n := mustDecode(t, sampleHy2AliasURL)
		if n.Protocol != config.ProtocolHysteria2 {
			t.Errorf("hy2:// 应解析成 hysteria2，实际 %s", n.Protocol)
		}
		if n.Server != "h2.example.com" || n.Port != 8443 || n.Password != "pwd123" || n.Name != "短别名" {
			t.Errorf("hy2 别名解析结果不对: %+v", n)
		}
		// hysteria2 一定跑在 QUIC + TLS 上，链接里不写 security 也必须启用 TLS。
		if !n.TLS.Enabled {
			t.Error("hysteria2 必须启用 TLS")
		}
	})

	t.Run("样本解析结果能再编码回链接", func(t *testing.T) {
		// 样本 → Node → 链接 → Node，两边字段必须一致：
		// 证明"读别人的链接"和"写自己的链接"用的是同一套字段映射。
		for _, link := range []string{
			"vmess://" + sampleVMessStd, "vmess://" + sampleVMessUpper,
			"ss://" + sampleSSUserinfo + "@ss.example.com:8388#SS",
			"ss://" + sampleSSLegacy + "#SS",
			sampleVLESSURL, sampleTrojanURL, sampleHysteria2URL,
		} {
			first := mustDecode(t, link)
			mine, err := Encode(first)
			if err != nil {
				t.Fatalf("Encode(%s) 失败: %v", truncate(link, 40), err)
			}
			sameNode(t, mustDecode(t, mine), first)
		}
	})
}

// ------------------------------------------------------------------
// 3. 容忍性测试
// ------------------------------------------------------------------

func TestDecodeTolerance(t *testing.T) {
	t.Run("未知参数不报错", func(t *testing.T) {
		link := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@1.2.3.4:443" +
			"?security=tls&sni=a.example.com&fp=chrome" +
			"&mport=443&remarks=xx&encryption=none&pqv=abc&extraUnknown=%zz&tfo=1&utls=1"
		n := mustDecode(t, link)
		if !n.TLS.Enabled || n.TLS.ServerName != "a.example.com" || n.TLS.Fingerprint != "chrome" {
			t.Errorf("已知参数没映射对: %+v", n.TLS)
		}
		// 非法百分号转义（%zz）不能让整条链接失败。
		if n.Server != "1.2.3.4" || n.Port != 443 {
			t.Errorf("地址端口解析错误: %s:%d", n.Server, n.Port)
		}
	})

	t.Run("vmess 字段大小写混写", func(t *testing.T) {
		raw := `{"Ps":"混写别名","aDd":"mix.example.com","PoRt":8443,` +
			`"Id":"b831381d-6324-4d53-ad4f-8cda48b30811","AiD":"0","sCy":"",` +
			`"nEt":"h2","TyPe":"","HoSt":"mix.example.com","PaTh":"/mix",` +
			`"TlS":"tls","SnI":"mix.example.com","AlPn":"h2,http/1.1"}`
		n := mustDecode(t, "vmess://"+base64.StdEncoding.EncodeToString([]byte(raw)))
		if n.Name != "混写别名" || n.Server != "mix.example.com" || n.Port != 8443 {
			t.Errorf("别名/地址/端口解析错误: %+v", n)
		}
		// scy 为空按主流客户端的惯例补 auto；h2 归一化成 http。
		if n.Security != "auto" {
			t.Errorf("Security 应补成 auto，实际 %q", n.Security)
		}
		if n.Transport.Network != "http" {
			t.Errorf("net=h2 应归一化成 http，实际 %q", n.Transport.Network)
		}
		if n.Transport.Path != "/mix" || n.Transport.Host != "mix.example.com" {
			t.Errorf("path/host 解析错误: %+v", n.Transport)
		}
		if !n.TLS.Enabled || n.TLS.ServerName != "mix.example.com" {
			t.Errorf("TLS 解析错误: %+v", n.TLS)
		}
	})

	t.Run("base64 四种变体", func(t *testing.T) {
		variants := map[string]string{
			"标准+padding":         sampleVMessStd,
			"标准无 padding":        sampleVMessNoPad,
			"URL-safe 无 padding": sampleVMessURLSafe,
			"URL-safe + padding": sampleVMessURLSafe + "=",
		}
		for name, body := range variants {
			n := mustDecode(t, "vmess://"+body)
			if n.Server != "vmess.example.com" || n.Port != 443 || n.UUID == "" {
				t.Errorf("%s: 解析结果不对: %+v", name, n)
			}
		}
		// ss 的旧格式同样两种都出现过。
		padded := base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pwd123@5.6.7.8:8389"))
		for name, body := range map[string]string{"无 padding": sampleSSLegacy, "有 padding": padded} {
			n := mustDecode(t, "ss://"+body+"#x")
			if n.Server != "5.6.7.8" || n.Port != 8389 || n.Password != "pwd123" {
				t.Errorf("ss 旧格式(%s)解析错误: %+v", name, n)
			}
		}
		// SIP002 的 userinfo 也两种都试。
		stdUserinfo := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:Password123"))
		for name, ui := range map[string]string{"URL-safe": sampleSSUserinfo, "标准+padding": stdUserinfo} {
			n := mustDecode(t, "ss://"+ui+"@ss.example.com:8388#x")
			if n.Method != "aes-256-gcm" || n.Password != "Password123" {
				t.Errorf("ss SIP002(%s)解析错误: %+v", name, n)
			}
		}
	})

	t.Run("整段 base64 被 URL 编码过", func(t *testing.T) {
		// 有些客户端会把 base64 里的 '=' 编成 %3D。
		body := strings.ReplaceAll(sampleVMessStd, "=", "%3D")
		n := mustDecode(t, "vmess://"+body)
		if n.Server != "vmess.example.com" {
			t.Errorf("解析结果不对: %+v", n)
		}
	})

	t.Run("BOM/引号/换行/尾随标点/折行", func(t *testing.T) {
		link := "\ufeff  \"ss://" + sampleSSUserinfo + "@ss.example.com:8388#SS 示例\"，\r\n"
		n := mustDecode(t, link)
		if n.Server != "ss.example.com" || n.Name != "SS 示例" {
			t.Errorf("削前后缀失败: %+v", n)
		}
		// 聊天窗口复制常被折行（base64 中间插了换行）。
		folded := "vmess://" + sampleVMessStd[:60] + "\n\t" + sampleVMessStd[60:]
		if n := mustDecode(t, folded); n.Server != "vmess.example.com" {
			t.Errorf("折行的 base64 解析失败: %+v", n)
		}
	})

	t.Run("ss 明文 userinfo", func(t *testing.T) {
		n := mustDecode(t, "ss://aes-256-gcm:plain%40pw@1.2.3.4:8388#明文%20userinfo")
		if n.Method != "aes-256-gcm" || n.Password != "plain@pw" || n.Name != "明文 userinfo" {
			t.Errorf("明文 userinfo 解析错误: %+v", n)
		}
	})

	t.Run("ss plugin 参数被忽略", func(t *testing.T) {
		link := "ss://" + sampleSSUserinfo + "@ss.example.com:8388" +
			"?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dexample.com#x"
		n := mustDecode(t, link)
		if n.Method != "aes-256-gcm" || n.Password != "Password123" {
			t.Errorf("带 plugin 的 SIP002 解析错误: %+v", n)
		}
	})

	t.Run("trojan 默认 TLS / security=none 覆盖", func(t *testing.T) {
		// 没写 security：trojan 协议本身要求 TLS，按启用处理。
		n := mustDecode(t, "trojan://pwd@1.2.3.4:443?peer=sni.example.com")
		if !n.TLS.Enabled || n.TLS.ServerName != "sni.example.com" {
			t.Errorf("trojan 默认值/peer 兜底不对: %+v", n.TLS)
		}
		// 写了 security=none 就尊重它。
		if n := mustDecode(t, "trojan://pwd@1.2.3.4:443?security=none"); n.TLS.Enabled {
			t.Errorf("security=none 时不应启用 TLS: %+v", n.TLS)
		}
	})

	t.Run("vless 无 security 即明文", func(t *testing.T) {
		n := mustDecode(t, "vless://b831381d-6324-4d53-ad4f-8cda48b30811@1.2.3.4:443")
		if n.TLS.Enabled {
			t.Errorf("vless 未写 security 不应启用 TLS: %+v", n.TLS)
		}
		if n.Transport.Network != "tcp" {
			t.Errorf("传输层应补成 tcp，实际 %q", n.Transport.Network)
		}
	})

	t.Run("只写 pbk 也当 reality", func(t *testing.T) {
		link := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@1.2.3.4:443" +
			"?pbk=" + sampleRealityPubKey + "&sid=abcd&spx=%2Fabc"
		n := mustDecode(t, link)
		if !n.TLS.Enabled || !n.TLS.EnabledReality() {
			t.Errorf("pbk 存在时应启用 TLS 并认作 reality: %+v", n.TLS)
		}
		if n.TLS.RealityPublicKey != sampleRealityPubKey || n.TLS.RealityShortID != "abcd" || n.TLS.RealitySpiderX != "/abc" {
			t.Errorf("reality 参数解析错误: %+v", n.TLS)
		}
	})

	t.Run("h2 归一化 + 重复 alpn + 编码过的路径", func(t *testing.T) {
		link := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@1.2.3.4:443" +
			"?type=h2&path=%2Fabc%3Fed%3D2048&alpn=h2&alpn=http%2F1.1&security=tls&allowInsecure=1"
		n := mustDecode(t, link)
		if n.Transport.Network != "http" {
			t.Errorf("type=h2 应归一化为 http，实际 %q", n.Transport.Network)
		}
		if n.Transport.Path != "/abc?ed=2048" {
			t.Errorf("path 解码错误: %q", n.Transport.Path)
		}
		if !reflect.DeepEqual(n.TLS.ALPN, []string{"h2", "http/1.1"}) {
			t.Errorf("重复 alpn 参数应全部保留，实际 %#v", n.TLS.ALPN)
		}
		if !n.TLS.AllowInsecure {
			t.Error("allowInsecure=1 未生效")
		}
	})

	t.Run("IPv6 地址", func(t *testing.T) {
		n := mustDecode(t, "vless://b831381d-6324-4d53-ad4f-8cda48b30811@[2001:db8::1]:443?security=tls")
		if n.Server != "2001:db8::1" || n.Port != 443 {
			t.Errorf("IPv6 解析错误: %s:%d", n.Server, n.Port)
		}
		// 再编回去必须带方括号，否则对端会把它当端口分隔符。
		link, err := Encode(n)
		if err != nil {
			t.Fatalf("Encode 失败: %v", err)
		}
		if !strings.Contains(link, "@[2001:db8::1]:443") {
			t.Errorf("IPv6 未被加方括号: %s", link)
		}
	})
}

// ------------------------------------------------------------------
// 4. Detect / DecodeAny
// ------------------------------------------------------------------

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
		ok   bool
	}{
		{"vmess", "vmess://" + sampleVMessStd, config.ProtocolVMess, true},
		{"vless 带前缀文本", "我的节点：\n1.vless://b831381d-6324-4d53-ad4f-8cda48b30811@a.example.com:443?security=tls#A", config.ProtocolVLESS, true},
		{"trojan", sampleTrojanURL, config.ProtocolTrojan, true},
		{"ss 旧格式", "ss://" + sampleSSLegacy + "#x", config.ProtocolShadowsocks, true},
		{"ss SIP002", "ss://" + sampleSSUserinfo + "@a.example.com:8388#x", config.ProtocolShadowsocks, true},
		{"hysteria2", sampleHysteria2URL, config.ProtocolHysteria2, true},
		{"hy2 别名", sampleHy2AliasURL, config.ProtocolHysteria2, true},
		{"多条按出现顺序取第一条", "ss://" + sampleSSUserinfo + "@a.example.com:8388#x vmess://" + sampleVMessStd,
			config.ProtocolShadowsocks, true},
		{"https 不是分享链接", "https://example.com/path", "", false},
		{"纯文本", "这里没有任何链接", "", false},
		{"空文本", "", "", false},
		{"只有坏的 vmess", "vmess://这不是base64", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Detect(c.text)
			if ok != c.ok || got != c.want {
				t.Errorf("Detect() = (%q, %v)，期望 (%q, %v)", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestDecodeAny(t *testing.T) {
	t.Run("多行文本混入无关行", func(t *testing.T) {
		text := strings.Join([]string{
			"订阅内容如下（共 3 条）：",
			"1. vmess://" + sampleVMessStd,
			"",
			"  2. vless://b831381d-6324-4d53-ad4f-8cda48b30811@v.example.com:443?security=tls&sni=v.example.com#Vless节点",
			"这一行是说明文字，不是链接。",
			"备用：ss://" + sampleSSUserinfo + "@s.example.com:8388#SS节点；",
			"结束",
		}, "\r\n")

		nodes, err := DecodeAny(text)
		if err != nil {
			t.Fatalf("DecodeAny 失败: %v", err)
		}
		if len(nodes) != 3 {
			t.Fatalf("应解析出 3 个节点，实际 %d: %+v", len(nodes), nodes)
		}
		// 顺序必须与出现顺序一致，否则导入后用户会对不上号。
		wantProtos := []string{config.ProtocolVMess, config.ProtocolVLESS, config.ProtocolShadowsocks}
		wantNames := []string{"香港 ws 节点", "Vless节点", "SS节点"}
		for i, n := range nodes {
			if n.Protocol != wantProtos[i] {
				t.Errorf("第 %d 条协议 = %s，期望 %s", i+1, n.Protocol, wantProtos[i])
			}
			if n.Name != wantNames[i] {
				t.Errorf("第 %d 条别名 = %q，期望 %q", i+1, n.Name, wantNames[i])
			}
		}
	})

	t.Run("一行里多条链接", func(t *testing.T) {
		text := "vmess://" + sampleVMessStd + " vless://b831381d-6324-4d53-ad4f-8cda48b30811@v.example.com:443?security=tls#V"
		nodes, err := DecodeAny(text)
		if err != nil {
			t.Fatalf("DecodeAny 失败: %v", err)
		}
		if len(nodes) != 2 {
			t.Fatalf("应解析出 2 个节点，实际 %d", len(nodes))
		}
	})

	t.Run("坏链接不影响好链接", func(t *testing.T) {
		text := "vmess://这不是base64\n" +
			"vless://b831381d-6324-4d53-ad4f-8cda48b30811@v.example.com:443?security=tls#好的\n" +
			"ss://@@@\n"
		nodes, err := DecodeAny(text)
		if err != nil {
			t.Fatalf("DecodeAny 失败: %v", err)
		}
		if len(nodes) != 1 || nodes[0].Name != "好的" {
			t.Fatalf("应只留下 1 个能用的节点，实际 %+v", nodes)
		}
	})

	t.Run("解析不出节点", func(t *testing.T) {
		for _, text := range []string{"", "   ", "这里没有链接", "https://example.com", "vmess://坏内容"} {
			if _, err := DecodeAny(text); !errors.Is(err, ErrNoLink) {
				t.Errorf("DecodeAny(%q) 应返回 ErrNoLink，实际 %v", text, err)
			}
		}
	})
}

// ------------------------------------------------------------------
// 5. 二维码
// ------------------------------------------------------------------

func TestQRPNG(t *testing.T) {
	link := "vmess://" + sampleVMessStd

	t.Run("默认尺寸是合法 PNG", func(t *testing.T) {
		data, err := QRPNG(link, 0)
		if err != nil {
			t.Fatalf("QRPNG 失败: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("QRPNG 返回空数据")
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("返回的不是合法 PNG: %v", err)
		}
		b := img.Bounds()
		if b.Dx() != b.Dy() || b.Dx() <= 0 {
			t.Errorf("二维码应为正方形，实际 %dx%d", b.Dx(), b.Dy())
		}
		if b.Dx() > defaultQRSize {
			t.Errorf("默认边长不应超过 %d，实际 %d", defaultQRSize, b.Dx())
		}
	})

	t.Run("指定尺寸", func(t *testing.T) {
		data, err := QRPNG(link, 256)
		if err != nil {
			t.Fatalf("QRPNG 失败: %v", err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("返回的不是合法 PNG: %v", err)
		}
		if b := img.Bounds(); b.Dx() > 256 || b.Dx() <= 0 {
			t.Errorf("256 像素请求得到 %d 像素", b.Dx())
		}
	})

	t.Run("尺寸过小时库会自动放大而不是失败", func(t *testing.T) {
		// go-qrcode 对过小的 size 是"静默放大到最小可用尺寸"，
		// 我们要保证它不会变成 0 尺寸图或 panic。
		data, err := QRPNG(link, 8)
		if err != nil {
			t.Fatalf("QRPNG 失败: %v", err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("返回的不是合法 PNG: %v", err)
		}
		if img.Bounds().Dx() <= 0 {
			t.Error("尺寸异常")
		}
	})

	t.Run("内容超容量返回 error 而不是 panic", func(t *testing.T) {
		huge := "vmess://" + strings.Repeat("A", 5000)
		if _, err := QRPNG(huge, 320); err == nil {
			t.Error("超长内容应返回 error")
		}
	})

	t.Run("空内容返回 error", func(t *testing.T) {
		for _, text := range []string{"", "   ", "\r\n"} {
			if _, err := QRPNG(text, 0); err == nil {
				t.Errorf("QRPNG(%q) 应返回 error", text)
			}
		}
	})
}

func TestQRDataURL(t *testing.T) {
	const prefix = "data:image/png;base64,"

	dataURL, err := QRDataURL("vless://b831381d-6324-4d53-ad4f-8cda48b30811@a.example.com:443?security=tls#A", 256)
	if err != nil {
		t.Fatalf("QRDataURL 失败: %v", err)
	}
	if !strings.HasPrefix(dataURL, prefix) {
		t.Fatalf("前缀不对: %.40s", dataURL)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
	if err != nil {
		t.Fatalf("data URL 里的 base64 非法: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("data URL 里的内容不是合法 PNG: %v", err)
	}

	if _, err := QRDataURL("", 0); err == nil {
		t.Error("空内容应返回 error")
	}
}

// ------------------------------------------------------------------
// 6. 错误路径
// ------------------------------------------------------------------

func TestEncodeErrors(t *testing.T) {
	t.Run("没有通用分享格式的协议", func(t *testing.T) {
		for _, proto := range []string{
			config.ProtocolSOCKS5, config.ProtocolHTTP, config.ProtocolWireGuard,
			"", "unknown-protocol",
		} {
			n := config.Node{Protocol: proto, Server: "1.2.3.4", Port: 443, Password: "p"}
			if _, err := Encode(n); !errors.Is(err, ErrUnsupportedProtocol) {
				t.Errorf("Encode(%q) 应返回 ErrUnsupportedProtocol，实际 %v", proto, err)
			}
		}
	})

	t.Run("远端信息不完整", func(t *testing.T) {
		cases := []struct {
			name string
			node config.Node
		}{
			{"缺地址", config.Node{Protocol: config.ProtocolVLESS, Port: 443, UUID: "u"}},
			{"缺端口", config.Node{Protocol: config.ProtocolVLESS, Server: "a.example.com", UUID: "u"}},
			{"端口越界", config.Node{Protocol: config.ProtocolVLESS, Server: "a.example.com", Port: 70000, UUID: "u"}},
			{"vmess 缺 UUID", config.Node{Protocol: config.ProtocolVMess, Server: "a.example.com", Port: 443}},
			{"vless 缺 UUID", config.Node{Protocol: config.ProtocolVLESS, Server: "a.example.com", Port: 443}},
			{"ss 缺加密方式", config.Node{Protocol: config.ProtocolShadowsocks, Server: "a.example.com", Port: 443, Password: "p"}},
			{"trojan 缺密码", config.Node{Protocol: config.ProtocolTrojan, Server: "a.example.com", Port: 443}},
		}
		for _, c := range cases {
			if _, err := Encode(c.node); err == nil {
				t.Errorf("%s: 应返回 error", c.name)
			}
		}
	})
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name string
		link string
	}{
		{"空串", ""},
		{"只有空白", "   \r\n"},
		{"纯文本", "这不是链接"},
		{"没有 scheme 分隔符", "vmess:abc"},
		{"不支持的 scheme", "https://example.com"},
		{"vmess 内容不是 base64/JSON", "vmess://不是base64"},
		{"vmess JSON 为空", "vmess://" + base64.StdEncoding.EncodeToString([]byte("{}"))},
		{"vless 缺 UUID", "vless://@1.2.3.4:443"},
		{"端口为 0", "vless://b831381d-6324-4d53-ad4f-8cda48b30811@1.2.3.4:0?security=tls"},
		{"缺地址", "trojan://pwd@:443"},
		{"ss 只有别名", "ss://#只有别名"},
		{"ss 凭据里没有冒号", "ss://YWJjZGVm@1.2.3.4:8388"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if n, err := Decode(c.link); err == nil {
				t.Errorf("Decode(%q) 应返回 error，实际解析出 %+v", c.link, n)
			}
		})
	}
}

// TestDecodeDoesNotSetLocalFields 单独再锁一次"不碰本机状态"：
// 导入后本地监听端口必须由用户单独设置，链接里带进来的任何东西都不能生效。
func TestDecodeDoesNotSetLocalFields(t *testing.T) {
	for _, link := range []string{
		"vmess://" + sampleVMessStd, sampleVLESSURL, sampleTrojanURL,
		"ss://" + sampleSSUserinfo + "@ss.example.com:8388#x", sampleHysteria2URL,
	} {
		n := mustDecode(t, link)
		if n.ListenHost != "" || n.ListenPort != 0 || n.Front != "" || n.Active ||
			!n.CreatedAt.IsZero() || n.StatsDay != "" || n.WireGuard != nil {
			t.Errorf("%s 解析结果混进了本机字段: %+v", truncate(link, 40), n)
		}
	}
}
