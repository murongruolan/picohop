package config

import "testing"

// TestFrontProxyURL 前置代理地址的拼法。
//
// 【为什么专门测这个】"经代理更新订阅"就是靠它把请求交给前置；前置几乎都要认证，
// 漏掉凭据会直接 407。密码里出现 @ : / 这类字符也很常见，不转义会把 URL 拼坏。
func TestFrontProxyURL(t *testing.T) {
	cases := []struct {
		name string
		in   Front
		want string
	}{
		{"直连没有代理地址", Front{Type: FrontTypeDirect, Host: "1.2.3.4", Port: 1080}, ""},
		{"类型为空", Front{Host: "1.2.3.4", Port: 1080}, ""},
		{"地址没填全", Front{Type: FrontTypeSocks5, Host: "1.2.3.4"}, ""},
		{"socks5 无认证", Front{Type: FrontTypeSocks5, Host: "1.2.3.4", Port: 1080}, "socks5://1.2.3.4:1080"},
		{"http", Front{Type: FrontTypeHTTP, Host: "proxy.example.com", Port: 8080}, "http://proxy.example.com:8080"},
		{
			"socks5 带认证（密码里有特殊字符）",
			Front{Type: FrontTypeSocks5, Host: "1.2.3.4", Port: 1080, Username: "user", Password: "p@ss:word/1"},
			"socks5://user:p%40ss%3Aword%2F1@1.2.3.4:1080",
		},
		{
			"http 带认证",
			Front{Type: FrontTypeHTTP, Host: "h", Port: 3128, Username: "u", Password: "p"},
			"http://u:p@h:3128",
		},
	}
	for _, c := range cases {
		if got := c.in.ProxyURL(); got != c.want {
			t.Fatalf("%s: ProxyURL() = %q，期望 %q", c.name, got, c.want)
		}
	}
}
