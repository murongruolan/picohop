package engine

import (
	"strings"
	"testing"
)

// TestRedactConfigJSON 锁死"配置进日志前必须脱敏"。
//
// 背景：日志里原本躺着整份 sing-box 配置（uuid/密码/reality 公钥），
// 本机任意进程可读；用户贴日志求助就是二次泄露。
// 但配置本身对排查有价值，所以做法是**脱敏后照打**，而不是不打印。
func TestRedactConfigJSON(t *testing.T) {
	raw := `{"outbounds":[{"type":"vless","uuid":"11111111-2222-3333-4444-555555555555",` +
		`"tls":{"reality":{"public_key":"PUBKEY_SECRET","short_id":"deadbeef"}}},` +
		`{"type":"shadowsocks","password":"sspassword"}],` +
		`"server":"hk.example.com","server_port":443}`

	got := redactConfigJSON(raw)
	for _, secret := range []string{
		"11111111-2222-3333-4444-555555555555", "PUBKEY_SECRET", "deadbeef", "sspassword",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("脱敏后不该还有 %q：%s", secret, got)
		}
	}
	// 结构与键要留着，否则失去排查价值
	for _, keep := range []string{`"uuid":"***"`, `"public_key":"***"`, `"short_id":"***"`,
		`"password":"***"`, `"server":"hk.example.com"`, `"server_port":443`} {
		if !strings.Contains(got, keep) {
			t.Fatalf("脱敏后应当保留 %q：%s", keep, got)
		}
	}
}
