package wgproxy

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"

	"github.com/murongruolan/picohop/internal/proxy"
)

// socksBind 实现 wireguard-go 的 conn.Bind 接口，
// 把 WireGuard 的外层 UDP 包封装成 RFC 1928 的 SOCKS5 UDP 报文，
// 经前置代理的 UDP ASSOCIATE 通道收发。
//
// 这是整个 "WireGuard-over-前置代理" 的关键零件：
// 前置代理只把 UDP 数据报从它的出口 IP 转发出去，
// 因此 WireGuard 服务端看到的握手包来源就是"前置节点的 IP + 随机端口"，
// 与从境外直连完全一致。
//
// 一个必须记住的约定：
// wireguard-go 的 Device.BindUpdate() 会**先 Close 旧 bind 再 Open 新 bind**
// （改端口、换密钥、重新选路时都会发生）。因此这里的 Close() 只能
// "结束当前这一次会话"，绝不能把 SOCKS5 通道本身关掉、也不能置成永久关闭，
// 否则下一次 Open 会拿到 net.ErrClosed，表现为：
//
//	IPC error -98: failed to set listen_port: use of closed network connection
//
// 真正"彻底关闭"用 shutdown()。
type socksBind struct {
	ch   proxy.UDPChannel
	peer netip.AddrPort
	dev  *device.Device
	logf func(format string, args ...any)

	mu     sync.Mutex
	done   chan struct{} // 当前会话的结束信号
	isDown bool          // 是否已彻底关闭
}

var _ conn.Bind = (*socksBind)(nil)

// Open 启动本次会话的接收循环。WireGuard 客户端一般 listen_port=0（随机端口），
// 真实端口由前置代理那一侧决定，这里无需也不能绑定本地端口。
func (b *socksBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	dbg("Open(port=%d) 被调用", port)
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.isDown {
		return nil, 0, net.ErrClosed
	}
	// 防御：上一次会话还没收尾就先结束它
	b.endSessionLocked()
	b.done = make(chan struct{})
	return []conn.ReceiveFunc{b.receive}, 0, nil
}

func (b *socksBind) endSessionLocked() {
	if b.done != nil {
		select {
		case <-b.done:
		default:
			close(b.done)
		}
	}
}

// sessionChannel 在**同一把锁下**成对取出"当前会话的 UDP 通道 + 结束信号"。
//
// 【为什么必须成对取】shutdown() 会在锁内把 b.ch 置 nil、b.done 也置 nil。
// 收包回调 receive() 若裸读 b.ch（或先取 done 再裸读 ch），有三种坏情况：
//  1. 读到 nil 的 b.ch -> nil 接口方法调用 -> **panic**（节点启停瞬间进程崩溃）；
//  2. 读到"已被 close 的 ch" -> 每个数据报都报错；
//  3. 在 shutdown 之后调用"必要时新建 done"的旧版 sessionDone()，会拿到一个
//     **永远不会被关闭**的通道 -> 收包循环再也醒不过来。
//
// 返回 ok=false 表示已彻底关闭：调用方必须安静退出，不得解引用。
func (b *socksBind) sessionChannel() (proxy.UDPChannel, chan struct{}, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ch == nil || b.done == nil {
		return nil, nil, false
	}
	return b.ch, b.done, true
}

func (b *socksBind) down() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.isDown
}

// receive 是 wireguard-go 的收包回调：取一个数据报填进 bufs。
func (b *socksBind) receive(bufs [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
	// 成对取出、循环里只用局部变量：shutdown() 并发置空 b.ch 时，
	// 我们手里的 ch 要么有效、要么这里直接返回，绝不会解引用 nil。
	ch, done, ok := b.sessionChannel()
	if !ok {
		return 0, net.ErrClosed
	}
	for {
		select {
		case <-done:
			return 0, net.ErrClosed
		default:
		}

		if len(bufs) == 0 {
			return 0, errors.New("socksBind: 设备没有提供接收缓冲")
		}
		// 【不要替换设备给的缓冲区】
		// 早先这里写了 `if len(bufs[0]) < 2048 { bufs[0] = make([]byte, 2048) }`，
		// 想当然地"帮设备扩容"。结果 wireguard-go 的 RoutineReceiveIncoming
		// 是按它自己分配的缓冲数组来管理生命周期的，
		// 中途把它换掉会让收到的包无法被正确交给解密队列 ——
		// 现象是"握手响应明明收到了 92 字节，但握手永远不完成"。
		// 设备给的缓冲一定够大，直接用即可。
		n, srcHost, srcPort, err := ch.ReceiveFromUDP(done, bufs[0])
		if err != nil {
			// 会话被上层结束：安静退出，这是正常收尾路径。
			// 若在这里继续循环，device.Close() 会一直等下去（死锁）。
			if errors.Is(err, proxy.ErrUDPChannelClosed) {
				return 0, net.ErrClosed
			}
			if b.down() {
				return 0, net.ErrClosed
			}
			if errors.Is(err, net.ErrClosed) {
				return 0, net.ErrClosed
			}
			// 临时性错误（例如偶发读超时）**绝不能终结接收循环**：
			// 一旦 return，wireguard-go 就再也收不到包，握手会永久卡住，
			// 而且外层只会看到一句"握手超时"，完全指不到这里。
			// 因此除了"通道已关闭"以外，一律继续重试。
			continue
		}

		// 先记录"到底收到了什么"，再做后续处理。
		//
		// 这一点非常重要：如果回包被丢掉了，日志里必须有痕迹，
		// 否则只会看到"发出去、收不回来"，完全指不到是哪一步吃掉的。
		dbg("receive 收到 %d 字节，来自 %s:%d（配置对端 %s）", n, srcHost, srcPort, b.peer)

		addr, ok := addrFromHostPort(srcHost, srcPort)
		if !ok {
			dbg("  -> 来源地址无法解析，丢弃")
			continue
		}

		// 【刻意不按源地址过滤】
		//
		// 早先这里有一句 `if addr.Addr() != b.peer.Addr() { continue }`，
		// 想当然地"只收对端的包"。但那会造成一类**完全无声**的故障：
		//   · 端口映射（DNAT）若没配 SNAT，容器回包的源地址是容器内网 IP；
		//   · WireGuard 允许对端漫游，换网络后源地址本来就会变。
		// 这两种情况下回包都会被直接丢掉，外部只看到"握手超时"。
		//
		// wireguard-go 自带的 StdNetBind 也**不按源地址过滤** ——
		// 因为 WireGuard 的每个包都有密码学认证，
		// 真正的合法性由协议层判断，收包层没必要（也不该）做这件事。

		sizes[0] = n
		eps[0] = &socksEndpoint{dst: addr}
		return 1, nil
	}
}

// Send 把一批加密后的 UDP 包通过 SOCKS5 通道送到对端。
func (b *socksBind) Send(bufs [][]byte, ep conn.Endpoint) error {
	if ep == nil {
		return errors.New("socksBind: 目标端点为空")
	}
	b.mu.Lock()
	ch := b.ch
	down := b.isDown
	b.mu.Unlock()
	// 关闭过程中设备可能还会再调一次 Send，必须防住：
	// 直接解引用已被 shutdown 置空的 b.ch 会 panic。
	if down || ch == nil {
		return net.ErrClosed
	}
	dst := ep.DstIP()
	if !dst.IsValid() {
		return fmt.Errorf("socksBind: 无效的目标 IP %s", ep.DstToString())
	}
	// 这一版 conn.Endpoint 没有 DstPort()，只能从 "ip:port" 里拆出来。
	port, ok := portOf(ep.DstToString())
	if !ok {
		return fmt.Errorf("socksBind: 无法从 %q 解析出目标端口", ep.DstToString())
	}
	for _, buf := range bufs {
		if len(buf) == 0 {
			continue
		}
		dbg("Send %d 字节 -> %s:%d", len(buf), dst, port)
		if err := ch.SendToUDP(buf, dst.String(), int(port)); err != nil {
			return err
		}
	}
	return nil
}

// ParseEndpoint 解析 "host:port"。wireguard-go 的 IpcSet 会用它读 endpoint 字段。
func (b *socksBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		host, portStr, serr := net.SplitHostPort(s)
		if serr != nil {
			return nil, fmt.Errorf("无法解析 WireGuard 端点 %q：%w", s, err)
		}
		a, aerr := netip.ParseAddr(host)
		if aerr != nil {
			return nil, fmt.Errorf("端点 %q 的地址不是 IP（请先解析成 IP 再填）", s)
		}
		n, perr := strconv.ParseUint(portStr, 10, 16)
		if perr != nil {
			return nil, fmt.Errorf("端点 %q 的端口无效", s)
		}
		ap = netip.AddrPortFrom(a.Unmap(), uint16(n))
	}
	return &socksEndpoint{dst: ap}, nil
}

func (b *socksBind) SetMark(uint32) error { return nil } // 走前置代理，不需要 SO_MARK

func (b *socksBind) BatchSize() int { return 1 }

// Close 只结束"本次会话"，SOCKS5 通道留到 shutdown 再关。
func (b *socksBind) Close() error {
	dbg("Close() 被调用")
	b.mu.Lock()
	defer b.mu.Unlock()
	b.endSessionLocked()
	return nil
}

// shutdown 彻底关闭（隧道/进程退出时调用）。
func (b *socksBind) shutdown() error {
	dbg("shutdown() 被调用")
	b.mu.Lock()
	b.isDown = true
	b.endSessionLocked()
	b.done = nil
	ch := b.ch
	b.ch = nil
	b.mu.Unlock()

	if ch != nil {
		return ch.Close()
	}
	return nil
}

// ------------------------------------------------------------------
// 端点
// ------------------------------------------------------------------

type socksEndpoint struct {
	src netip.Addr
	dst netip.AddrPort
}

func (e *socksEndpoint) ClearSrc()           { e.src = netip.Addr{} }
func (e *socksEndpoint) SrcToString() string { return e.srcAddr().String() }
func (e *socksEndpoint) DstToString() string { return e.dst.String() }
func (e *socksEndpoint) DstIP() netip.Addr   { return e.dst.Addr() }
func (e *socksEndpoint) SrcIP() netip.Addr   { return e.src }

func (e *socksEndpoint) srcAddr() netip.AddrPort {
	if !e.src.IsValid() {
		return netip.AddrPortFrom(netip.IPv4Unspecified(), 0)
	}
	return netip.AddrPortFrom(e.src, 0)
}

// DstToBytes 用于 cookie 计算，格式必须与 StdNetBind 兼容（IPv4 4 字节 / IPv6 16 字节）。
func (e *socksEndpoint) DstToBytes() []byte {
	a := e.dst.Addr()
	if a.Is4() {
		b := a.As4()
		return b[:]
	}
	b := a.As16()
	return b[:]
}

// portOf 从 "ip:port" 中取出端口。
func portOf(addrPort string) (uint16, bool) {
	if ap, err := netip.ParseAddrPort(addrPort); err == nil {
		return ap.Port(), true
	}
	_, p, err := net.SplitHostPort(addrPort)
	if err != nil {
		return 0, false
	}
	n, perr := strconv.ParseUint(p, 10, 16)
	if perr != nil {
		return 0, false
	}
	return uint16(n), true
}

func addrFromHostPort(host string, port int) (netip.AddrPort, bool) {
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(a.Unmap(), uint16(port)), true
}
