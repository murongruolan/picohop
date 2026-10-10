package wgproxy

import (
	"testing"
)

// TestSplitAllowedIPs 校验逗号列表能被正确拆成"一行一个前缀"。
//
// 这是踩过坑的地方：UAPI 的 allowed_ip 必须是单值，
// 写成 "allowed_ip=a/24, b/24" 会解析失败。全隧道配置 "0.0.0.0/0, ::/0"
// 正是这种写法，用户拿到手的配置十有八九就是这个，所以必须钉死。
func TestSplitAllowedIPs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"0.0.0.0/0, ::/0", []string{"0.0.0.0/0", "::/0"}},
		{"0.0.0.0/0", []string{"0.0.0.0/0"}},
		{" 192.168.31.0/24 ,10.8.0.0/24 ", []string{"192.168.31.0/24", "10.8.0.0/24"}},
		{"", []string{"0.0.0.0/0"}}, // 空值退化成默认全隧道
	}
	for _, c := range cases {
		got := splitAllowedIPs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitAllowedIPs(%q) 得到 %v，期望 %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitAllowedIPs(%q)[%d] = %q，期望 %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// TestPrivateKeyValidation 私钥格式必须给出明确错误，而不是静默算出一个错的公钥。
func TestPrivateKeyValidation(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"<<<把你的私钥粘贴到这里>>>",
		"tooshort",
		"!!!!not-base64!!!!",
	}
	for _, k := range bad {
		if _, err := PublicKeyFromPrivate(k); err == nil {
			t.Errorf("私钥 %q 应当被拒绝，却通过了解析", k)
		}
	}
	// 正常私钥（含首尾空白，模拟粘贴带上换行）应通过
	good := "sGZUVwUocDPP+hMyfH0qzuQP2BBOGm0Vqa0mCsH0+ns="
	if _, err := PublicKeyFromPrivate("\n " + good + " \r\n"); err != nil {
		t.Errorf("带空白的合法私钥应能解析，却失败：%v", err)
	}
}
