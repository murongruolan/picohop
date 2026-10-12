package proxy

import (
	"strings"
	"testing"
	"time"

	"chainproxy/internal/config"
)

// TestNewServerRefusesAnonymousLanListen 锁死审查 #22：
// 监听地址非回环（= 界面上的「允许局域网」，app.startNode 会把 ListenHost
// 解析成 0.0.0.0）+ 没有本地端口鉴权 = 给整条局域网开匿名开放代理。
//
// 【为什么在这一层测】node.ListenHost 传到这里时**已经是解析好的实际监听地址**，
// 而"要不要鉴权"也是在 NewServer 里定的。拒绝发生在这里，就没有任何一条路径
// 能绕过它去 bind —— 哪怕日后又冒出第二个调用方忘了检查。
//
// 【为什么只构造、不 Start】拒绝发生在构造阶段，连监听套接字都不会创建；
// 测试里也不必真的把端口绑到 0.0.0.0（那会弹 Windows 防火墙对话框）。
//
// 【半套凭据为什么也要拒】密码留空时 SOCKS5（RFC 1929）与 HTTP Basic 都允许
// 零长度密码：局域网里任何人猜到用户名就能用。所以"配齐"才算数。
func TestNewServerRefusesAnonymousLanListen(t *testing.T) {
	stubMihomoFactory(t)

	cases := []struct {
		name       string
		host       string
		user, pass string
		wantErr    bool
	}{
		{"IPv4 通配 + 完全没凭据 → 拒绝", "0.0.0.0", "", "", true},
		{"IPv6 通配 + 完全没凭据 → 拒绝", "::", "", "", true},
		{"具体网卡地址 + 完全没凭据 → 拒绝", "192.168.1.10", "", "", true},
		{"主机名（无法证明是回环）+ 没凭据 → 拒绝", "myhost.local", "", "", true},
		{"只填用户名 → 半套凭据也拒绝", "0.0.0.0", "u", "", true},
		{"只填密码 → 半套凭据也拒绝", "0.0.0.0", "", "p", true},
		{"用户名密码都是空白字符 → 不算凭据", "0.0.0.0", "   ", "  ", true},

		{"IPv4 通配 + 凭据配齐 → 放行", "0.0.0.0", "u", "p", false},
		{"具体网卡地址 + 凭据配齐 → 放行", "192.168.1.10", "u", "p", false},
		{"回环 + 没凭据 → 放行（只听本机，本来就无需鉴权）", "127.0.0.1", "", "", false},
		{"127.0.0.2（整个 127/8 都是回环）+ 没凭据 → 放行", "127.0.0.2", "", "", false},
		{"localhost + 没凭据 → 放行", "localhost", "", "", false},
		{"空值（Start 会回落到回环）+ 没凭据 → 放行", "", "", "", false},
		{" 127.0.0.1 （前后空格）+ 没凭据 → 放行", " 127.0.0.1 ", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := config.Node{
				ID: "n1", Name: "n1", Protocol: config.ProtocolVLESS,
				ListenHost:     tc.host,
				ListenUsername: tc.user,
				ListenPassword: tc.pass,
			}
			s, err := NewServer(node, config.Front{Type: config.FrontTypeDirect}, time.Minute, nil)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("listenHost=%q 且没有凭据，却构造成功了 —— "+
						"节点会绑在 %s 上且不校验任何凭据，局域网匿名代理又回来了（审查 #22）",
						tc.host, tc.host)
				}
				if s != nil {
					t.Fatal("拒绝时不许返回半成品 Server（调用方可能忽略 err 继续用）")
				}
				// 报错必须是可操作的：告诉用户去哪儿补凭据，而不是一句"非法参数"
				if !strings.Contains(err.Error(), "本地端口鉴权") {
					t.Fatalf("报错必须点明「本地端口鉴权」，实际：%v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("listenHost=%q 配置合法（有凭据或只对本机可见），不该被拒绝：%v", tc.host, err)
			}
			if s == nil {
				t.Fatal("放行时必须返回可用的 Server")
			}
		})
	}
}

// TestListenHostFallbackSharedByStartAndGuard 确认空监听地址的兜底只有一份规则。
//
// 【为什么单独测】NewServer 用 listenHost() 判断"这次监听是否对局域网开放"，
// Start 用同一个函数决定 bind 到哪儿。两边一旦分叉，就会出现最危险的组合：
// 检查时按回环放行、实际却绑到 0.0.0.0。这里把这条规则钉死在函数上。
func TestListenHostFallbackSharedByStartAndGuard(t *testing.T) {
	cases := map[string]string{
		"":            "127.0.0.1",
		"   ":         "127.0.0.1",
		"127.0.0.1":   "127.0.0.1",
		" 0.0.0.0 ":   "0.0.0.0",
		"192.168.1.5": "192.168.1.5",
	}
	for in, want := range cases {
		if got := listenHost(in); got != want {
			t.Errorf("listenHost(%q) 应为 %q，实际 %q", in, want, got)
		}
	}
}
