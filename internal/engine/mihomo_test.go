package engine

import (
	"strings"
	"testing"

	"chainproxy/internal/config"
	"chainproxy/internal/proxy"
)

// TestNewMihomoFrontPlan 锁死"第一跳怎么走、哪些必须拒绝"。
//
// 【为什么这是本项目最不能接受的失败方式】MihomoUpstream 的出站只会连**节点自己的
// 服务器**。配了前置却不把这条连接交给前置，它就会从**本机 IP** 出去 —— 链式代理 /
// 住宅出口被绕开，而且完全静默（链路照样能通）。所以：配了前置必须**真的经前置**
// （装上 mihomoDialer），确实做不到的形态必须**拒绝启动**，两者都不许含糊。
func TestNewMihomoFrontPlan(t *testing.T) {
	directFront := config.Front{Type: config.FrontTypeDirect, TimeoutMs: 10000}
	globalSocks := config.Front{Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10808, TimeoutMs: 10000}
	httpFront := config.Front{Type: config.FrontTypeHTTP, Host: "127.0.0.1", Port: 8080, TimeoutMs: 10000}

	vless := func(front string) config.Node {
		return config.Node{
			ID: "n1", Name: "测试节点", Protocol: config.ProtocolVLESS,
			Server: "1.2.3.4", Port: 443, UUID: "11111111-2222-3333-4444-555555555555",
			Front: front,
		}
	}

	t.Run("直连（含空值/空白/零值前置）放行，且不装拨号器", func(t *testing.T) {
		for _, c := range []struct {
			name  string
			node  config.Node
			front config.Front
		}{
			{"前置为空 + 显式直连", vless(""), directFront},
			{"前置是纯空白 + 显式直连", vless("   "), directFront},
			{"前置为空 + 零值 Front（没给前置信息）", vless(""), config.Front{}},
			{"节点选全局前置、而全局前置本身就是 direct（等效直连）", vless(config.FrontGlobal), directFront},
		} {
			up, err := NewMihomo(c.node, c.front, nil)
			if err != nil {
				t.Fatalf("%s：直连形态不该被拒，实际 %v", c.name, err)
			}
			if mu := up.(*MihomoUpstream); mu.dialer != nil {
				t.Errorf("%s：直连时不该装经前置的拨号器（那会让 mihomo 自己的 "+
					"interface-name / routing-mark 等选项失效）", c.name)
			}
		}
	})

	t.Run("配了前置必须经前置：装上拨号器而不是拒绝", func(t *testing.T) {
		for _, c := range []struct {
			name  string
			node  config.Node
			front config.Front
		}{
			{"用全局前置", vless(config.FrontGlobal), globalSocks},
			{"全局前置类型残缺（空串 = 按 socks5 跑）", vless(config.FrontGlobal),
				config.Front{Host: "127.0.0.1", Port: 10808}},
			{"前置指向另一个节点", vless("node-b-id"), globalSocks},
			{"节点自称直连、调用方却递了非直连前置", vless(""), globalSocks},
			{"以另一个节点为前置（本地端口）", vless("node-b-id"),
				config.Front{Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10810}},
		} {
			up, err := NewMihomo(c.node, c.front, nil)
			if err != nil {
				t.Fatalf("%s：能经前置的节点不该被拒，实际 %v", c.name, err)
			}
			if mu := up.(*MihomoUpstream); mu.dialer == nil {
				t.Fatalf("%s：必须装上经前置的拨号器，否则会从本机 IP 静默直连", c.name)
			}
		}
	})

	t.Run("确实做不到的形态必须拒绝启动", func(t *testing.T) {
		hy2 := func(front string) config.Node {
			n := vless(front)
			n.Protocol = config.ProtocolHysteria2
			n.Password = "pw"
			n.UUID = ""
			n.TLS.Enabled = true
			n.TLS.ServerName = "sni.example.com"
			return n
		}
		for _, c := range []struct {
			name  string
			node  config.Node
			front config.Front
		}{
			{"节点前置指向另一个节点，解析出来却是直连（两边不一致）", vless("node-b-id"), directFront},
			{"Hysteria2 是纯 QUIC，而 HTTP 前置没有 UDP 转发", hy2(""), httpFront},
		} {
			up, err := NewMihomo(c.node, c.front, nil)
			if err == nil {
				t.Fatalf("%s：必须拒绝（否则会静默用本机 IP 直连），实际放行 %v", c.name, up)
			}
			msg := err.Error()
			for _, want := range []string{"拒绝", c.node.Name} {
				if !strings.Contains(msg, want) {
					t.Errorf("%s：错误信息要说到点子上，缺 %q：%s", c.name, want, msg)
				}
			}
		}
	})
}

// TestFrontedNodeStaysOnMihomo 走真注册链，锁死"配了前置的节点由 mihomo 自己经前置拨号"。
//
// A′ 之前这里断言的是"回退 sing-box"；前置在适配层里接通之后，正确行为变成
// **继续用 mihomo**（回退反而说明前置没接上）。而且 sing-box 已经摘除，
// 现在**没有任何回退可走** —— 这条测试同时守住"前置节点仍然被正常接住"。
// 只做构造不启动，所以不需要网络。
func TestFrontedNodeStaysOnMihomo(t *testing.T) {
	n := config.Node{
		ID: "n1", Name: "配了前置的节点", Protocol: config.ProtocolVLESS,
		Server: "1.2.3.4", Port: 443, UUID: "11111111-2222-3333-4444-555555555555",
		Front: "node-b-id",
	}
	up, err := proxy.NewUpstream(n, config.Front{
		Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10808, TimeoutMs: 10000,
	}, nil)
	if err != nil {
		t.Fatalf("配了前置的节点应当由 mihomo 经前置接住，实际报错 %v", err)
	}
	mu, ok := up.(*MihomoUpstream)
	if !ok {
		t.Fatalf("配了前置的节点必须走 mihomo（没有别的内核了），实际 %T", up)
	}
	if mu.dialer == nil {
		t.Fatal("必须装上经前置的拨号器，否则会从本机 IP 静默直连")
	}
}

// TestVlessOptionMapping 锁死"我们的节点 → mihomo vless 选项"的映射。
//
// 为什么值得单测：这是整条迁移里最容易错、错了又最难查的一环
// （症状是"节点连不上"，而原因可能只是 Host 放错了字段）。
// 纯函数 + 表驱动：不用起网络就能断言每个字段落对了地方。
//
// 其中三条是**实测/源码确认过的**，特别容易被写错：
//  1. mihomo 的 REALITY 必须显式给 client-fingerprint（否则报 uTLS 那句）；
//  2. HTTPOptions 没有 Host 字段，Host 要放进 Headers；
//  3. httpupgrade 在 mihomo 里复用 WSOpts（V2rayHttpUpgrade = true）；
//  4. h2 用 HTTP2Opts（Host 是数组），不是 HTTPOpts。
func TestVlessOptionMapping(t *testing.T) {
	base := func() config.Node {
		n := config.Node{
			ID: "n1", Name: "测试节点", Protocol: config.ProtocolVLESS,
			Server: "1.2.3.4", Port: 443, UUID: "11111111-2222-3333-4444-555555555555",
		}
		n.TLS.Enabled = true
		n.TLS.ServerName = "www.example.com"
		return n
	}

	t.Run("tcp 默认传输不写 transport 字段", func(t *testing.T) {
		o, err := vlessOption(base())
		if err != nil {
			t.Fatal(err)
		}
		if o.Network != "" {
			t.Fatalf("tcp 时 Network 应为空，实际 %q", o.Network)
		}
		if o.Server != "1.2.3.4" || o.Port != 443 || o.ServerName != "www.example.com" {
			t.Fatalf("基础字段映射错误：%+v", o)
		}
		if !o.TLS {
			t.Fatal("TLS 没传过去")
		}
	})

	t.Run("ws", func(t *testing.T) {
		n := base()
		n.Transport.Network = "ws"
		n.Transport.Path = "/p"
		n.Transport.Host = "cdn.example.com"
		o, err := vlessOption(n)
		if err != nil {
			t.Fatal(err)
		}
		if o.Network != "ws" || o.WSOpts.Path != "/p" {
			t.Fatalf("ws 映射错误：net=%q path=%q", o.Network, o.WSOpts.Path)
		}
		if got := o.WSOpts.Headers["Host"]; got != "cdn.example.com" {
			t.Fatalf("ws Host 应在 Headers[Host]，实际 %q", got)
		}
	})

	t.Run("grpc", func(t *testing.T) {
		n := base()
		n.Transport.Network = "grpc"
		n.Transport.ServiceName = "svc"
		o, err := vlessOption(n)
		if err != nil {
			t.Fatal(err)
		}
		if o.Network != "grpc" || o.GrpcOpts.GrpcServiceName != "svc" {
			t.Fatalf("grpc 映射错误：net=%q svc=%q", o.Network, o.GrpcOpts.GrpcServiceName)
		}
	})

	t.Run("http 伪装：Host 放 Headers 而不是 Host 字段", func(t *testing.T) {
		n := base()
		n.Transport.Network = "http"
		n.Transport.Path = "/h"
		n.Transport.Host = "h.example.com"
		o, err := vlessOption(n)
		if err != nil {
			t.Fatal(err)
		}
		if o.Network != "http" {
			t.Fatalf("http 的 network 应为 http，实际 %q", o.Network)
		}
		if len(o.HTTPOpts.Path) != 1 || o.HTTPOpts.Path[0] != "/h" {
			t.Fatalf("http Path 应是数组，实际 %v", o.HTTPOpts.Path)
		}
		if len(o.HTTPOpts.Headers["Host"]) != 1 || o.HTTPOpts.Headers["Host"][0] != "h.example.com" {
			t.Fatalf("http Host 应放 Headers[Host]（HTTPOptions 没有 Host 字段），实际 %v", o.HTTPOpts.Headers)
		}
	})

	t.Run("h2 用 HTTP2Opts", func(t *testing.T) {
		n := base()
		n.Transport.Network = "h2"
		n.Transport.Path = "/h2"
		n.Transport.Host = "h2.example.com"
		o, err := vlessOption(n)
		if err != nil {
			t.Fatal(err)
		}
		if o.Network != "http" { // mihomo 里 HTTP/2 也是 network="http"
			t.Fatalf("h2 的 network 应为 http，实际 %q", o.Network)
		}
		if o.HTTP2Opts.Path != "/h2" || len(o.HTTP2Opts.Host) != 1 || o.HTTP2Opts.Host[0] != "h2.example.com" {
			t.Fatalf("h2 映射错误：path=%q host=%v", o.HTTP2Opts.Path, o.HTTP2Opts.Host)
		}
	})

	t.Run("httpupgrade 复用 WSOpts", func(t *testing.T) {
		n := base()
		n.Transport.Network = "httpupgrade"
		n.Transport.Path = "/u"
		n.Transport.Host = "u.example.com"
		o, err := vlessOption(n)
		if err != nil {
			t.Fatal(err)
		}
		if o.Network != "httpupgrade" || o.WSOpts.Path != "/u" || !o.WSOpts.V2rayHttpUpgrade {
			t.Fatalf("httpupgrade 映射错误：net=%q path=%q flag=%v",
				o.Network, o.WSOpts.Path, o.WSOpts.V2rayHttpUpgrade)
		}
	})

	t.Run("xhttp（本次迁移的起因）", func(t *testing.T) {
		n := base()
		n.Transport.Network = "xhttp"
		n.Transport.Path = "/63ffaa"
		n.Transport.Host = "www.amazon.com"
		o, err := vlessOption(n)
		if err != nil {
			t.Fatal(err)
		}
		if o.Network != "xhttp" || o.XHTTPOpts.Path != "/63ffaa" || o.XHTTPOpts.Host != "www.amazon.com" {
			t.Fatalf("xhttp 映射错误：net=%q path=%q host=%q", o.Network, o.XHTTPOpts.Path, o.XHTTPOpts.Host)
		}
	})

	t.Run("REALITY 缺指纹必须在构造前拦住", func(t *testing.T) {
		n := base()
		n.TLS.RealityPublicKey = "PUBKEY"
		n.TLS.RealityShortID = "abcd"
		// 故意不设 Fingerprint
		if _, err := vlessOption(n); err == nil {
			t.Fatal("REALITY 缺 client-fingerprint 应当报错，而不是发出去让 mihomo 报 uTLS 那句")
		}
	})

	t.Run("REALITY 带指纹正常映射", func(t *testing.T) {
		n := base()
		n.TLS.RealityPublicKey = "PUBKEY"
		n.TLS.RealityShortID = "abcd"
		n.TLS.Fingerprint = "chrome"
		o, err := vlessOption(n)
		if err != nil {
			t.Fatal(err)
		}
		if o.ClientFingerprint != "chrome" {
			t.Fatalf("指纹没传过去：%q", o.ClientFingerprint)
		}
		if o.RealityOpts.PublicKey != "PUBKEY" || o.RealityOpts.ShortID != "abcd" {
			t.Fatalf("RealityOpts 映射错误：%+v", o.RealityOpts)
		}
	})

	t.Run("必填缺失与未知传输都要明确报错", func(t *testing.T) {
		n := base()
		n.Server = ""
		if _, err := vlessOption(n); err == nil {
			t.Fatal("缺服务器地址应报错")
		}
		n = base()
		n.Port = 0
		if _, err := vlessOption(n); err == nil {
			t.Fatal("端口非法应报错")
		}
		n = base()
		n.UUID = ""
		if _, err := vlessOption(n); err == nil {
			t.Fatal("缺 UUID 应报错")
		}
		n = base()
		n.Transport.Network = "quic-unknown"
		if _, err := vlessOption(n); err == nil {
			t.Fatal("未知传输应报错")
		}
	})
}

// TestMihomoSupportsIsHonest 守住那条规则："只声明已经实现的东西"。
//
// 第 3 轮这版的断言是"vmess/trojan/ss/hysteria2 还没接、不该声称支持"；
// 第 6 轮把它们实现了，那条断言随之过时 —— 但**意图不变**：
// 适配层没实现的协议必须当场拒绝，而不是等用户点了"激活"才发现没接。
func TestMihomoSupportsIsHonest(t *testing.T) {
	for _, p := range []string{
		config.ProtocolVLESS, config.ProtocolVMess, config.ProtocolTrojan,
		config.ProtocolShadowsocks, config.ProtocolHysteria2,
	} {
		if !mihomoSupports(p) {
			t.Errorf("%s 已经实现，必须放行", p)
		}
	}
	if mihomoSupports("nope-not-a-protocol") {
		t.Error("不认识的协议不该声称支持")
	}
}

func TestOtherProtocolOptions(t *testing.T) {
	t.Run("vmess：Security→Cipher、AlterID、传输 ws", func(t *testing.T) {
		n := config.Node{
			ID: "v", Name: "v", Protocol: config.ProtocolVMess,
			Server: "1.1.1.1", Port: 443, UUID: "uuid-1", AlterID: 64, Security: "auto",
		}
		n.TLS.Enabled = true
		n.TLS.ServerName = "sni.example.com"
		n.Transport.Network = "ws"
		n.Transport.Path = "/w"
		o, err := vmessOption(n)
		if err != nil {
			t.Fatal(err)
		}
		if o.Cipher != "auto" || o.AlterID != 64 {
			t.Fatalf("Cipher/AlterID 映射错误：cipher=%q alterID=%d", o.Cipher, o.AlterID)
		}
		if o.Network != "ws" || o.WSOpts.Path != "/w" {
			t.Fatalf("ws 映射错误：net=%q path=%q", o.Network, o.WSOpts.Path)
		}
	})

	t.Run("trojan：SNI 字段 + 传输白名单", func(t *testing.T) {
		n := config.Node{
			ID: "t", Name: "t", Protocol: config.ProtocolTrojan,
			Server: "t.example.com", Port: 443, Password: "pw",
		}
		n.TLS.ServerName = "sni.example.com"
		o, err := trojanOption(n)
		if err != nil {
			t.Fatal(err)
		}
		if o.SNI != "sni.example.com" || o.Password != "pw" {
			t.Fatalf("trojan 映射错误：sni=%q pw=%q", o.SNI, o.Password)
		}
		n.Transport.Network = "xhttp" // trojan 结构体没有这个选项
		if _, err := trojanOption(n); err == nil {
			t.Fatal("trojan + xhttp 应当明确报错，而不是静默忽略传输")
		}
	})

	t.Run("shadowsocks：Method→Cipher", func(t *testing.T) {
		n := config.Node{
			ID: "s", Name: "s", Protocol: config.ProtocolShadowsocks,
			Server: "s.example.com", Port: 8388, Password: "pw", Method: "aes-256-gcm",
		}
		o, err := shadowsocksOption(n)
		if err != nil {
			t.Fatal(err)
		}
		if o.Cipher != "aes-256-gcm" || o.Password != "pw" {
			t.Fatalf("ss 映射错误：cipher=%q pw=%q", o.Cipher, o.Password)
		}
	})

	t.Run("hysteria2：Up/Down 带单位、Obfs 透传", func(t *testing.T) {
		n := config.Node{
			ID: "h", Name: "h", Protocol: config.ProtocolHysteria2,
			Server: "h.example.com", Port: 443, Password: "pw",
			UpMbps: 100, DownMbps: 500, Obfs: "salamander", ObfsPassword: "op",
		}
		n.TLS.ServerName = "sni.example.com"
		o, err := hysteria2Option(n)
		if err != nil {
			t.Fatal(err)
		}
		if o.Up != "100 Mbps" || o.Down != "500 Mbps" {
			t.Fatalf("Up/Down 应当是带单位字符串，实际 up=%q down=%q", o.Up, o.Down)
		}
		if o.Obfs != "salamander" || o.ObfsPassword != "op" || o.SNI != "sni.example.com" {
			t.Fatalf("hysteria2 映射错误：%+v", o)
		}
	})

	t.Run("hysteria2：PinSHA256 → 证书 pin（Fingerprint），不是 uTLS 指纹", func(t *testing.T) {
		// 【测试夹具一律用假值】真实节点的 pin 与凭据绝不进仓库（那是"哪个节点"的指纹）。
		// 这里只要是 32 字节 hex 就够 —— mihomo 的 pin 校验器只认这个形状。
		const pin = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
		n := config.Node{
			ID: "h", Name: "h", Protocol: config.ProtocolHysteria2,
			Server: "h.example.com", Port: 443, Password: "pw",
			PinSHA256: pin,
		}
		n.TLS.Enabled = true
		n.TLS.ServerName = "sni.example.com"
		// 同时给一个 uTLS 浏览器指纹：它**绝不能**落到证书 pin 那个字段上 ——
		// 这正是"证书 pin / client-fingerprint"最容易搞混的地方（hysteria2 的
		// 结构体里根本没有 ClientFingerprint，浏览器名喂给 Fingerprint 会被拒）。
		n.TLS.Fingerprint = "chrome"

		o, err := hysteria2Option(n)
		if err != nil {
			t.Fatal(err)
		}
		if o.Fingerprint != pin {
			t.Fatalf("证书 pin 没落到 Hysteria2Option.Fingerprint（实际 %q）；"+
				"少了它自签证书的 hysteria2 节点会报 x509 signed by unknown authority", o.Fingerprint)
		}
		if o.SkipCertVerify {
			t.Fatal("没开 allowInsecure 时不该跳过证书校验")
		}
		// 顺带把"这个字段真的是 sha256 pin"钉死：mihomo 会拿它去建 pin 校验器，
		// 形状不对（不是 32 字节 hex）当场构造失败。这一句同时防住"映射到
		// Certificate/PrivateKey（客户端证书）"这类改错字段的改法。
		if _, err := buildMihomoOutbound(n, nil); err != nil {
			t.Fatalf("带 pin 的 hysteria2 出站应当能构造出来，实际 %v", err)
		}
	})

	t.Run("hysteria2：allowInsecure → SkipCertVerify", func(t *testing.T) {
		n := config.Node{
			ID: "h2", Name: "h2", Protocol: config.ProtocolHysteria2,
			Server: "h.example.com", Port: 443, Password: "pw",
		}
		n.TLS.Enabled = true
		n.TLS.AllowInsecure = true

		o, err := hysteria2Option(n)
		if err != nil {
			t.Fatal(err)
		}
		if !o.SkipCertVerify {
			t.Fatal("「跳过校验」没往下传：用户勾了等于没勾，自签证书照样连不上")
		}
	})

	t.Run("没有 pin 时不许把 uTLS 指纹当成证书 pin", func(t *testing.T) {
		// 护栏：有人把映射改成 opt.Fingerprint = n.TLS.Fingerprint 的话，
		// mihomo 的 verifier 会因为 "chrome" 不是 32 字节 hex 而拒绝构造
		// （它专门为此提示改用 client-fingerprint）——这条测试就会红。
		n := config.Node{
			ID: "h3", Name: "h3", Protocol: config.ProtocolHysteria2,
			Server: "h.example.com", Port: 443, Password: "pw",
		}
		n.TLS.Enabled = true
		n.TLS.Fingerprint = "chrome"
		if _, err := buildMihomoOutbound(n, nil); err != nil {
			t.Fatalf("uTLS 指纹不该进证书 pin 字段，实际报错 %v", err)
		}
	})

	t.Run("五种协议都要被 mihomoSupports 放行", func(t *testing.T) {
		for _, p := range []string{
			config.ProtocolVLESS, config.ProtocolVMess, config.ProtocolTrojan,
			config.ProtocolShadowsocks, config.ProtocolHysteria2,
		} {
			if !mihomoSupports(p) {
				t.Fatalf("%s 已实现，应当放行", p)
			}
		}
	})
}
