package wgproxy

import (
	"strings"
	"testing"
)

// TestPublicKeyFromPrivateKnownVector 用一份真实配置校验推导结果。
//
// 这组值来自实际使用的配置：私钥 → 公钥必须与 WireGuard 服务端登记的完全一致。
// 公钥对不上时协议只会静默丢包，所以这个推导必须分毫不差
// （尤其是 Curve25519 的夹位处理，少做一步结果就完全不同）。
func TestPublicKeyFromPrivateKnownVector(t *testing.T) {
	const (
		priv = "sGZUVwUocDPP+hMyfH0qzuQP2BBOGm0Vqa0mCsH0+ns="
		want = "7HJHCIBX8qHxHEh5bD4gy9NkF7TNFbC2JZkWHfFO0Ws="
	)
	got, err := PublicKeyFromPrivate(priv)
	if err != nil {
		t.Fatalf("推导失败：%v", err)
	}
	if got != want {
		t.Fatalf("推导结果不对：\n  得到 %s\n  期望 %s", got, want)
	}
}

// TestGenerateKeyPair 生成后必须能自洽地推出同一个公钥，并且每次都不一样。
func TestGenerateKeyPair(t *testing.T) {
	a, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}
	b, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}
	if a.PrivateKey == b.PrivateKey {
		t.Fatal("两次生成的私钥相同，随机源有问题")
	}
	if a.PrivateKey == "" || a.PublicKey == "" {
		t.Fatal("生成了空密钥")
	}

	// 由私钥反推公钥，必须与生成时给的一致
	back, err := PublicKeyFromPrivate(a.PrivateKey)
	if err != nil {
		t.Fatalf("反推失败：%v", err)
	}
	if back != a.PublicKey {
		t.Fatalf("反推公钥不一致：%s vs %s", back, a.PublicKey)
	}
}

// TestDecodeKeyFormats 私钥常见两种写法：base64（wg 默认）与 hex（UAPI）。
func TestDecodeKeyFormats(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	// base64 形式
	if _, err := PublicKeyFromPrivate(kp.PrivateKey); err != nil {
		t.Fatalf("base64 私钥解析失败：%v", err)
	}
	// 带首尾空白（用户复制粘贴时常带）
	if _, err := PublicKeyFromPrivate("  " + kp.PrivateKey + "\n"); err != nil {
		t.Fatalf("带空白的私钥应当能解析：%v", err)
	}
	// 非法输入要报错，而不是返回一个"看起来像公钥"的垃圾
	if _, err := PublicKeyFromPrivate("not-a-key"); err == nil {
		t.Fatal("非法密钥应当报错")
	}
	if _, err := PublicKeyFromPrivate(""); err == nil {
		t.Fatal("空密钥应当报错")
	}
}

// TestGeneratePresharedKey 预共享密钥必须是 32 字节 base64。
func TestGeneratePresharedKey(t *testing.T) {
	p, err := GeneratePresharedKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(p) == 0 || strings.ContainsAny(p, " \t\n") {
		t.Fatalf("预共享密钥格式异常：%q", p)
	}
	if _, err := decodeKey(p); err != nil {
		t.Fatalf("生成的预共享密钥无法解析：%v", err)
	}
}
