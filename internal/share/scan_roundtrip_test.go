package share

import (
	"bytes"
	"image/png"
	"testing"

	"chainproxy/internal/config"
	"chainproxy/internal/qrscan"
)

// TestQRScanToNodeRoundTrip 是本项目"分享 → 二维码 → 屏幕识别 → 导入"这条
// 完整链路的集成测试（唯一没覆盖的是"人在屏幕上拖拽圈选"那一步，
// 那一步由 internal/qrscan 的 overlay 测试单独覆盖）。
//
// 之所以要专门写这个测试：这条链路上任何一环出问题，表现都是
// "扫不出来"，而单看某一环的测试全是绿的 —— 只有串起来才能发现
// 例如"编码时把 SNI 写进了 path"这类两边各自自洽、合起来不对的问题。
func TestQRScanToNodeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		node config.Node
	}{
		{
			name: "VLESS + Reality + xhttp",
			node: config.Node{
				Name:      "日本-Reality",
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
				},
			},
		},
		{
			name: "VMess + ws + tls",
			node: config.Node{
				Name:      "香港-VMess",
				Protocol:  config.ProtocolVMess,
				Server:    "hk2.example.com",
				Port:      8443,
				UUID:      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				AlterID:   0,
				Security:  "auto",
				Transport: config.TransportOptions{Network: "ws", Path: "/ws", Host: "cdn.example.com"},
				TLS:       config.TLSOptions{Enabled: true, ServerName: "cdn.example.com"},
			},
		},
		{
			name: "Shadowsocks",
			node: config.Node{
				Name:     "狮城-SS",
				Protocol: config.ProtocolShadowsocks,
				Server:   "sg.example.com",
				Port:     8388,
				Method:   "chacha20-ietf-poly1305",
				Password: "ExamplePass!23",
			},
		},
		{
			name: "Trojan",
			node: config.Node{
				Name:      "美西-Trojan",
				Protocol:  config.ProtocolTrojan,
				Server:    "us.example.com",
				Port:      443,
				Password:  "TrojanPass!23",
				Transport: config.TransportOptions{Network: "grpc", ServiceName: "grpcsvc"},
				TLS:       config.TLSOptions{Enabled: true, ServerName: "us.example.com"},
			},
		},
		{
			name: "Hysteria2 + 混淆",
			node: config.Node{
				Name:         "韩国-HY2",
				Protocol:     config.ProtocolHysteria2,
				Server:       "kr.example.com",
				Port:         443,
				Password:     "Hy2Pass!23",
				UpMbps:       50,
				DownMbps:     200,
				Obfs:         "salamander",
				ObfsPassword: "obfsPass",
				TLS:          config.TLSOptions{Enabled: true, ServerName: "kr.example.com"},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 1) 节点 -> 分享链接
			link, err := Encode(c.node)
			if err != nil {
				t.Fatalf("Encode 失败：%v", err)
			}

			// 2) 链接 -> 二维码 PNG
			pngBytes, err := QRPNG(link, 480)
			if err != nil {
				t.Fatalf("QRPNG 失败：%v", err)
			}

			// 3) PNG -> 图片（模拟"屏幕上那张二维码"）
			img, err := png.Decode(bytes.NewReader(pngBytes))
			if err != nil {
				t.Fatalf("解码 PNG 失败：%v", err)
			}

			// 4) 图片 -> 文本（模拟屏幕圈选后的识别）
			scanned, err := qrscan.Decode(img)
			if err != nil {
				t.Fatalf("qrscan.Decode 失败：%v", err)
			}
			if scanned != link {
				t.Fatalf("识别出来的文本与原始链接不一致：\n  原始: %s\n  识别: %s", link, scanned)
			}

			// 5) 文本 -> 节点（模拟导入）
			back, err := Decode(scanned)
			if err != nil {
				t.Fatalf("Decode 失败：%v", err)
			}

			// 6) 逐字段比对"连接所必需的字段"
			if back.Protocol != c.node.Protocol {
				t.Errorf("协议不一致：%s != %s", back.Protocol, c.node.Protocol)
			}
			if back.Server != c.node.Server {
				t.Errorf("服务器不一致：%s != %s", back.Server, c.node.Server)
			}
			if back.Port != c.node.Port {
				t.Errorf("端口不一致：%d != %d", back.Port, c.node.Port)
			}
			if back.UUID != c.node.UUID {
				t.Errorf("UUID 不一致：%s != %s", back.UUID, c.node.UUID)
			}
			if back.Password != c.node.Password {
				t.Errorf("密码不一致：%s != %s", back.Password, c.node.Password)
			}
			if back.Method != c.node.Method {
				t.Errorf("加密方法不一致：%s != %s", back.Method, c.node.Method)
			}
			if back.Flow != c.node.Flow {
				t.Errorf("流控不一致：%s != %s", back.Flow, c.node.Flow)
			}
			// 只有 vmess / vless / trojan 才有"传输层"这个概念。
			// shadowsocks 与 hysteria2 本来就没有，而分享链接的约定是
			// "空值统一补成 tcp"（对这个两个协议是惰性字段），
			// 所以这里只对有传输层的协议做严格比对。
			usesTransport := c.node.Protocol == config.ProtocolVMess ||
				c.node.Protocol == config.ProtocolVLESS ||
				c.node.Protocol == config.ProtocolTrojan
			if usesTransport {
				if back.Transport.Network != c.node.Transport.Network {
					t.Errorf("传输层不一致：%s != %s", back.Transport.Network, c.node.Transport.Network)
				}
				if back.Transport.Path != c.node.Transport.Path {
					t.Errorf("路径不一致：%s != %s", back.Transport.Path, c.node.Transport.Path)
				}
				if back.Transport.ServiceName != c.node.Transport.ServiceName {
					t.Errorf("gRPC 服务名不一致：%s != %s",
						back.Transport.ServiceName, c.node.Transport.ServiceName)
				}
				// grpc 的服务名在 vmess 分享格式里是塞在 path 里的，
				// 所以对 VMess 单独放宽 Host 的比对（其它协议要求严格一致）。
				if c.node.Protocol != config.ProtocolVMess && back.Transport.Host != c.node.Transport.Host {
					t.Errorf("Host 不一致：%s != %s", back.Transport.Host, c.node.Transport.Host)
				}
			}
			if back.TLS.Enabled != c.node.TLS.Enabled {
				t.Errorf("TLS 开关不一致：%v != %v", back.TLS.Enabled, c.node.TLS.Enabled)
			}
			if back.TLS.ServerName != c.node.TLS.ServerName {
				t.Errorf("SNI 不一致：%s != %s", back.TLS.ServerName, c.node.TLS.ServerName)
			}
			if back.TLS.RealityPublicKey != c.node.TLS.RealityPublicKey {
				t.Errorf("Reality 公钥不一致：%s != %s", back.TLS.RealityPublicKey, c.node.TLS.RealityPublicKey)
			}
			if back.TLS.RealityShortID != c.node.TLS.RealityShortID {
				t.Errorf("Reality ShortID 不一致：%s != %s", back.TLS.RealityShortID, c.node.TLS.RealityShortID)
			}
			if back.Obfs != c.node.Obfs || back.ObfsPassword != c.node.ObfsPassword {
				t.Errorf("混淆参数不一致：%s/%s != %s/%s",
					back.Obfs, back.ObfsPassword, c.node.Obfs, c.node.ObfsPassword)
			}
			if back.UpMbps != c.node.UpMbps || back.DownMbps != c.node.DownMbps {
				t.Errorf("带宽声明不一致：%d/%d != %d/%d",
					back.UpMbps, back.DownMbps, c.node.UpMbps, c.node.DownMbps)
			}

			// 7) 导入进来的节点不应该带任何"本地"字段 —— 端口必须由用户确认
			if back.ListenPort != 0 {
				t.Errorf("导入的节点不该带本地端口，实际 %d", back.ListenPort)
			}
			if back.Front != "" {
				t.Errorf("导入的节点不该带前置，实际 %q", back.Front)
			}
			if back.Active {
				t.Error("导入的节点不该是激活状态")
			}
		})
	}
}
