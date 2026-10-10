package wgproxy

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// PublicKeyFromPrivate 由私钥推导出公钥，返回 base64 形式（wg 工具的标准写法）。
//
// 用途：WireGuard 最常见的配置错误就是"双方公钥对不上"，
// 而协议在这种情况下**只会静默丢包**，不报任何错，排查起来极其痛苦。
// 把"本端公钥"直接显示给用户，就能一眼比对服务端 peer 配置。
func PublicKeyFromPrivate(priv string) (string, error) {
	raw, err := decodeKey(priv)
	if err != nil {
		return "", err
	}
	// 与 wg 一致：私钥先做 clamping
	raw[0] &= 248
	raw[31] &= 127
	raw[31] |= 64
	pub, err := curve25519.X25519(raw, curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("推导公钥失败：%w", err)
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

// decodeKey 把 32 字节密钥从 base64（wg 默认）或 hex（UAPI）还原成原始字节。
func decodeKey(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, fmt.Errorf("无法识别的密钥格式（应为 32 字节 base64 或 hex）")
}
