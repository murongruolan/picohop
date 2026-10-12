package engine

// 经前置的拨号器（迁移 A′）：让 mihomo 出站真的**绕经我们的前置**。
//
// 【为什么是这个机制】mihomo 表达链式代理的常规做法是 `dialer-proxy`，但那要一个
// 全局 hub + 出站注册表，与本项目"每节点一个独立出站、可同时激活多个"的用法冲突
// （理由见 mihomo.go 顶部）。而 adapter/outbound/base.go 的 `BasicOption` 里有一个
// `DialerForAPI C.Dialer` 字段，注释写明"the dialer used for API usage has **higher
// priority than all the above configurations**"，`NewDialer` 也优先取它 —— 所以只要
// 自己实现一个 C.Dialer 装进去，出站"连服务器"那一跳就会经它出去，完全不需要 hub。
//
// 【它盖住了哪些跳（读 mihomo 源码确认，不是猜）】
//   - vless / vmess / trojan：TCP 走 `dialContext -> dialer.DialContext(ctx,"tcp",addr)`；
//     UDP 也是 `ListenPacketContext -> dialContext`（XUDP / packet-addr 之后仍然是
//     同一条到服务器的连接），所以这两条都被 DialContext 覆盖；
//   - shadowsocks：TCP 走 DialContext，**原生 UDP 走 `dialer.ListenPacket(...)`**；
//   - hysteria2：整个协议跑在 QUIC 上，客户端把 `PacketListener`（= 这个拨号器）交给
//     `common.DialQuic`，后者用 `pDialer.ListenPacket(...)` 建 QUIC 的 UDP 套接字。
//
// 于是实现两个方法就够了：DialContext（tcp）与 ListenPacket（udp）。
//
// 【绝不静默直连】前置做不到的（例如 HTTP 前置没有 UDP 转发）一律返回明确错误，
// 而不是回落到本机直连 —— "配了前置却从本机 IP 出去"正是本项目最不能接受的失败方式。

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	C "github.com/metacubex/mihomo/constant"

	"chainproxy/internal/config"
	"chainproxy/internal/proxy"
)

// mihomoDialer 是满足 mihomo C.Dialer 的拨号器：所有连接都交给 proxy.Front。
//
// 【为什么只在前置非直连时才装】直连时根本不装（BasicOption.DialerForAPI 留 nil），
// 让 mihomo 用它自己的默认拨号器 —— 那样 interface-name / routing-mark / TFO 这些
// 选项照旧生效，我们不去抢它的活。
//
// 【前置实例为什么由自己造】出站要在**构造期**就建好（映射错误要当场暴露，见
// NewMihomo 的说明），而运行期的 Front 要到 Start 才由上层交进来。proxy.NewFront
// 造出来的实现都是无状态工厂（socks5 / http / direct：每次 Dial 自己建连接、
// Close 是空实现），所以自己持有一份不会与上层那份冲突 —— proxy.NewUpstream 给
// WireGuard 造 Front 用的也是同一套路。
type mihomoDialer struct {
	front   proxy.Front
	timeout time.Duration
}

var _ C.Dialer = (*mihomoDialer)(nil)

func newMihomoDialer(front config.Front) *mihomoDialer {
	return &mihomoDialer{
		front:   proxy.NewFront(front),
		timeout: time.Duration(front.Timeout()) * time.Millisecond,
	}
}

// DialContext 建立一条**经前置**的流式连接（network 只可能是 tcp*）。
func (d *mihomoDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if isUDPNetwork(network) {
		// 【为什么直接报错而不是直连】出站要 "udp" 直拨的只有 vmess 的 mkcp 传输
		// （我们没有映射它）与那些走 ListenPacket 的 QUIC 协议。真走到这里说明
		// mihomo 走了非常规路径：宁可失败，也绝不偷偷从本机 IP 出去。
		return nil, fmt.Errorf("mihomo 拨号器不支持经前置的 %q 直拨（经前置的 UDP 请走 ListenPacket）", network)
	}
	conn, err := d.front.DialTCP(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("经前置（%s）连接 %s 失败: %w", d.front.Describe(), address, err)
	}
	return conn, nil
}

// ListenPacket 经前置建一条 UDP 通道，桥接成 mihomo 要的 net.PacketConn。
//
// 【为什么 UDP 也必须经前置】Shadowsocks 的原生 UDP 与 Hysteria2 的 QUIC 都走这里；
// 若这里退回本机直连，等于把本机 IP 暴露出去。前置做不到 UDP 时（HTTP 前置）明确
// 报错，绝不静默直连。
//
// address / rAddrPort 是 mihomo 给的目标（"连到哪台服务器"）。我们的 UDP 通道是
// **非连接**的（按数据报给目标，见 proxy.UDPChannel），所以这里不需要用它：
// 上层每次 WriteTo 都会带上目标。
func (d *mihomoDialer) ListenPacket(ctx context.Context, _, _ string, _ netip.AddrPort) (net.PacketConn, error) {
	ch, err := d.front.DialUDP(ctx)
	if err != nil {
		return nil, fmt.Errorf("经前置（%s）建立 UDP 通道失败（HTTP 前置不支持 UDP 转发）: %w", d.front.Describe(), err)
	}
	return newFrontPacketConn(ch), nil
}

// isUDPNetwork 判断 mihomo 传来的 network 是不是 UDP 族（udp / udp4 / udp6）。
func isUDPNetwork(network string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(network)), "udp")
}
