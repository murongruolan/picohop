package wgproxy

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// KeyPair 是一对本端密钥。
//
// 使用约定（WireGuard 的安全模型就建立在这上面）：
//   - **私钥只留在本机**，任何时候都不要发给服务端；
//   - 把**公钥**交给服务端，让它在 peer 里登记；
//   - 服务端回给你：它的公钥、Endpoint，以及分配给你的隧道 IP。
type KeyPair struct {
	PrivateKey string // base64，本机保管
	PublicKey  string // base64，交给服务端
}

// GenerateKeyPair 生成一对新的 WireGuard 密钥（base64，与 `wg genkey` / `wg pubkey` 等价）。
func GenerateKeyPair() (KeyPair, error) {
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		return KeyPair{}, fmt.Errorf("生成随机数失败：%w", err)
	}
	return keyPairFromRaw(priv)
}

// keyPairFromRaw 由 32 字节原始私钥导出密钥对。
//
// 夹位（clamping）是 Curve25519 的要求，wg 工具也是这么做的；
// 不做的话推导出来的公钥会和 `wg pubkey` 不一致。
func keyPairFromRaw(priv []byte) (KeyPair, error) {
	if len(priv) != 32 {
		return KeyPair{}, fmt.Errorf("私钥长度必须是 32 字节，实际 %d", len(priv))
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return KeyPair{}, fmt.Errorf("推导公钥失败：%w", err)
	}
	return KeyPair{
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
	}, nil
}

// PublicKeyFromPrivate 由私钥推导出公钥，返回 base64 形式。
//
// 用途：WireGuard 最常见的配置错误就是"双方公钥对不上"，
// 而协议在这种情况下**只会静默丢包**，不报任何错，排查起来极其痛苦。
// 把"本端公钥"直接显示给用户，就能一眼比对服务端 peer 配置。
func PublicKeyFromPrivate(priv string) (string, error) {
	raw, err := decodeKey(priv)
	if err != nil {
		return "", err
	}
	kp, err := keyPairFromRaw(raw)
	if err != nil {
		return "", err
	}
	return kp.PublicKey, nil
}

// GeneratePresharedKey 生成一个预共享密钥（base64）。
// 双方填同一个值即可；服务端没要求就不用填。
func GeneratePresharedKey() (string, error) {
	psk := make([]byte, 32)
	if _, err := rand.Read(psk); err != nil {
		return "", fmt.Errorf("生成预共享密钥失败：%w", err)
	}
	return base64.StdEncoding.EncodeToString(psk), nil
}

// decodeKey 把 32 字节密钥从 base64（wg 默认）或 hex（UAPI）还原成原始字节。
func decodeKey(s string) ([]byte, error) {
	s = trimSpaceASCII(s)
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, fmt.Errorf("无法识别的密钥格式（应为 32 字节 base64 或 hex）")
}

// trimSpaceASCII 去掉首尾的空白字符（用户从别处复制粘贴时常带空格/换行）。
func trimSpaceASCII(s string) string {
	start, end := 0, len(s)
	for start < end {
		switch s[start] {
		case ' ', '\t', '\r', '\n', '\v', '\f':
			start++
			continue
		}
		break
	}
	for end > start {
		switch s[end-1] {
		case ' ', '\t', '\r', '\n', '\v', '\f':
			end--
			continue
		}
		break
	}
	return s[start:end]
}
