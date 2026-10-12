package engine

// mihomo 的 UDP 通道包装（按目标建通道 —— 第 10 轮实测否掉"一次建一个"之后的正确设计）。
//
// 【为什么要按目标建】第 10 轮的实测结论：mihomo 的 pc.ResolveUDP(ctx, metadata)
// 需要一个具体目标才能解析出 IP，而我们的接口 DialUDP(ctx, front) **不带目标**
// （目标按每个数据报给：SendToUDP(payload, host, port)）。
// 传空 metadata 会直接报 "can't resolve ip: couldn't find ip"。
// 所以这里维护 map[host:port] → C.PacketConn，第一次发往某目标时才建。
// （mihomo 自己的 UDP 也是按会话/目标建 PacketConn 的。）

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	C "github.com/metacubex/mihomo/constant"

	"github.com/murongruolan/picohop/internal/proxy"
)

type mihomoUDPChannel struct {
	up  *MihomoUpstream
	ctx context.Context

	mu    sync.Mutex
	conns map[string]C.PacketConn
	keys  []string // 轮询顺序（map 本身无序，单独记一份稳定的键序列）
}

func newMihomoUDPChannel(up *MihomoUpstream, ctx context.Context) *mihomoUDPChannel {
	return &mihomoUDPChannel{
		up:    up,
		ctx:   ctx,
		conns: map[string]C.PacketConn{},
	}
}

// connFor 取（必要时建）某个目标对应的 PacketConn。
func (c *mihomoUDPChannel) connFor(host string, port int) (C.PacketConn, error) {
	key := net.JoinHostPort(host, strconv.Itoa(port))

	c.mu.Lock()
	defer c.mu.Unlock()
	if pc, ok := c.conns[key]; ok {
		return pc, nil
	}

	out, err := c.up.outbound()
	if err != nil {
		return nil, err
	}
	meta := &C.Metadata{NetWork: C.UDP, Host: host, DstPort: uint16(port)}
	if addr, perr := netip.ParseAddr(host); perr == nil {
		meta.DstIP = addr
	}
	pc, err := out.ListenPacketContext(c.ctx, meta)
	if err != nil {
		// 【不包装成自己的话术】不支持 UDP 的出站由 mihomo 的 Base 返回 C.ErrNotSupport，
		// 原样带上才看得出"是内核不支持"，而不是我们的 bug。
		return nil, fmt.Errorf("mihomo UDP 通道建立失败（目标 %s）: %w", key, err)
	}
	if err := pc.ResolveUDP(c.ctx, meta); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("mihomo UDP 目标解析失败（目标 %s）: %w", key, err)
	}
	c.conns[key] = pc
	c.keys = append(c.keys, key)
	return pc, nil
}

// SendToUDP 发送一个数据报到指定目标（首次发往该目标时按需建通道）。
func (c *mihomoUDPChannel) SendToUDP(payload []byte, host string, port int) error {
	pc, err := c.connFor(host, port)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ips, lerr := net.LookupIP(host)
		if lerr != nil || len(ips) == 0 {
			return fmt.Errorf("UDP 目标 %q 解析失败: %w", host, lerr)
		}
		ip = ips[0]
	}
	_, err = pc.WriteTo(payload, &net.UDPAddr{IP: ip, Port: port})
	return err
}

// ReceiveFromUDP 从**所有**已建通道里收包，并可被 quit 叫醒。
//
// 【实现取舍】先做"轮询"版本：逐个 conn 给一小段读超时，谁有包就返回；每轮都查 quit。
// 缺点是活跃 conn 会拖慢其它 conn 的读取；若以后发现这个缺点有实际影响，
// 再改成"每个 conn 一个读协程 + 共享 chan"（契约里记了这条升级路径）。
//
// 【为什么必须有超时】本项目在 WireGuard 侧吃过一次亏：阻塞读既不检查 quit、
// 底层又关不掉 → 收包协程永远醒不过来、device.Close() 跟着死锁。
func (c *mihomoUDPChannel) ReceiveFromUDP(quit <-chan struct{}, buf []byte) (int, string, int, error) {
	for {
		select {
		case <-quit:
			return 0, "", 0, proxy.ErrUDPChannelClosed
		default:
		}

		c.mu.Lock()
		keys := append([]string(nil), c.keys...)
		c.mu.Unlock()

		if len(keys) == 0 {
			// 还没有任何目标：小睡一下再查 quit（不能忙等）
			time.Sleep(50 * time.Millisecond)
			continue
		}

		for _, key := range keys {
			c.mu.Lock()
			pc := c.conns[key]
			c.mu.Unlock()
			if pc == nil {
				continue
			}
			_ = pc.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue // 这个目标暂时没包，看下一个
				}
				return 0, "", 0, err
			}
			if ua, ok := addr.(*net.UDPAddr); ok {
				return n, ua.IP.String(), ua.Port, nil
			}
			h, ps, splitErr := net.SplitHostPort(addr.String())
			if splitErr != nil {
				return n, addr.String(), 0, nil
			}
			port, _ := strconv.Atoi(ps)
			return n, h, port, nil
		}
	}
}

// Close 关掉所有已建通道。
func (c *mihomoUDPChannel) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var first error
	for k, pc := range c.conns {
		if err := pc.Close(); err != nil && first == nil {
			first = err
		}
		delete(c.conns, k)
	}
	c.keys = nil
	return first
}
