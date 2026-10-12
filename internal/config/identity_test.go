package config

import (
	"strings"
	"testing"
)

// TestNodeIdentityKey 锁死导入查重判据的边界（审查 #29）。
//
// IdentityKey 是"这个节点连的是哪个远端入口"的指纹，App.AddNode 拿它查重。
// 这里要钉住两件事：
//  1. 判据只跟远端入口有关 —— 别名/备注/前置/本地端口/统计怎么改都不该变；
//  2. 判据里不能出现凭据原文 —— key 会在内存里传来传去，
//     将来很可能被顺手打进日志或错误信息（凭据只留在 Node 自己身上）。
func TestNodeIdentityKey(t *testing.T) {
	base := Node{
		Name: "日本-01", Protocol: ProtocolVLESS, Server: "JP.Example.com", Port: 443,
		UUID: "11111111-2222-3333-4444-555555555555",
	}
	key := base.IdentityKey()

	// 只有"本地/展示"字段不同 → 还是同一个远端入口
	same := base
	same.Name = "换了个别名"
	same.Remark = "备注"
	same.ListenPort = 12345
	same.Front = "other-node"
	same.UpBytes, same.DownBytes = 1, 2
	if got := same.IdentityKey(); got != key {
		t.Errorf("别名/备注/本地端口/前置不该影响查重判据：%q != %q", got, key)
	}

	// 服务器大小写、前后空白不影响（用户手输的来源很多）
	loose := base
	loose.Server = "  jp.example.com  "
	if got := loose.IdentityKey(); got != key {
		t.Errorf("服务器大小写/空白不该影响查重判据：%q != %q", got, key)
	}

	// 换凭据 / 换端口 / 换协议 / 换服务器 → 都是另一个远端入口
	for _, mut := range []func(*Node){
		func(n *Node) { n.UUID = "99999999-2222-3333-4444-555555555555" },
		func(n *Node) { n.Port = 8443 },
		func(n *Node) { n.Protocol = ProtocolVMess },
		func(n *Node) { n.Server = "us.example.com" },
	} {
		other := base
		mut(&other)
		if got := other.IdentityKey(); got == key {
			t.Errorf("换掉远端入口的关键字段之后判据应当变化：%+v", other)
		}
	}

	// 凭据原文绝不能出现在 key 里
	if strings.Contains(key, base.UUID) || strings.Contains(key, "11111111") {
		t.Errorf("查重判据里出现了凭据原文：%q", key)
	}

	// WireGuard 的凭据是密钥对：同地址不同密钥必须是两个节点
	wgA := Node{Protocol: ProtocolWireGuard, Server: "wg.example.com", Port: 51820,
		WireGuard: &WireGuardConfig{PrivateKey: "priv-a", PeerPublicKey: "peer-a"}}
	wgB := wgA
	wgB.WireGuard = &WireGuardConfig{PrivateKey: "priv-b", PeerPublicKey: "peer-a"}
	if wgA.IdentityKey() == wgB.IdentityKey() {
		t.Error("同地址不同私钥的 WireGuard 节点不该被判成同一个")
	}
	if strings.Contains(wgA.IdentityKey(), "priv-a") {
		t.Errorf("查重判据里出现了 WireGuard 私钥原文：%q", wgA.IdentityKey())
	}
}
