package config

import "testing"

// TestParseProxySpec 覆盖用户实际会粘贴的几种写法。
//
// 用户提供的住宅代理格式正是 "user:pass@host:port"，
// 这个解析错了整条链路都不通，因此单独锁死。
func TestParseProxySpec(t *testing.T) {
	cases := []struct {
		in                 string
		typ, user, pass    string
		host               string
		port               int
	}{
		{"fuckoai_2026-zone-custom-region-US:HVu78BgQcny7@hk.rrp.bestgo.work:10000",
			"socks5", "fuckoai_2026-zone-custom-region-US", "HVu78BgQcny7", "hk.rrp.bestgo.work", 10000},
		{"hk.rrp.bestgo.work:10000", "socks5", "", "", "hk.rrp.bestgo.work", 10000},
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

// TestValidatePortConflicts 锁死"端口不能重复"这条硬性要求。
func TestValidatePortConflicts(t *testing.T) {
	cfg := Default()
	cfg.Upstreams = []Upstream{
		{ID: "a", Name: "住宅1", Type: UpstreamTypeSocks5, Enabled: true, ListenPort: 10810, ServerHost: "h", ServerPort: 1},
		{ID: "b", Name: "住宅2", Type: UpstreamTypeSocks5, Enabled: true, ListenPort: 10810, ServerHost: "h", ServerPort: 1},
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

	// 与前置端口相同同样要报错（否则会自我拨号死循环）
	cfg2 := Default()
	cfg2.Upstreams = []Upstream{
		{ID: "a", Name: "住宅1", Type: UpstreamTypeSocks5, ListenPort: 10808, ServerHost: "h", ServerPort: 1},
	}
	issues2 := cfg2.Validate()
	found = false
	for _, i := range issues2 {
		if i.Field == "listenPort" {
			found = true
		}
	}
	if !found {
		t.Fatalf("与前置端口相同应当报错，实际 issues=%v", issues2)
	}
}