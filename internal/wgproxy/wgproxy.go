// Package wgproxy 实现"WireGuard 上游"：让一条 WireGuard 隧道整体跑在前置代理里面。
//
// 原理（三层纯用户态，不需要 wintun 驱动、不需要管理员权限、不改系统路由表）：
//
//	┌───────────────────────────┐
//	│ gvisor netstack           │  用户态 TCP/IP 栈，提供 net.Conn 给上层
//	├───────────────────────────┤
//	│ wireguard-go device       │  加解密 + 握手 + 会话
//	├───────────────────────────┤
//	│ socksBind（自研）          │  把 WG 的外层 UDP 包封成 SOCKS5 UDP 报文，
//	│                           │  经前置代理的 UDP ASSOCIATE 通道送出去
//	└───────────────────────────┘
//	              ↓
//	    主流客户端127.0.0.1:10808 (UDP ASSOCIATE)
//
// 与 socks5 / http 上游的区别：WG 上游没有"第二次握手"，
// 隧道在 Start() 时就已建立，Dial() 直接从用户态网络栈出栈。
package wgproxy

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"chainproxy/internal/config"
	"chainproxy/internal/proxy"
)

// 握手等待的两个常数。
const (
	// handshakeTimeout 是"等首包握手"的总时长。
	handshakeTimeout = 12 * time.Second
	// handshakePollInterval 是轮询 UAPI 里 last_handshake_time 的间隔。
	handshakePollInterval = 300 * time.Millisecond
)

// errStoppedDuringHandshake 表示隧道还没握手完就被 Stop 掉了。
var errStoppedDuringHandshake = errors.New("WireGuard 隧道在握手完成前被停止")

// Upstream 是一条 WireGuard 链路的实现，满足 proxy.Upstream 接口。
type Upstream struct {
	node  config.Node
	front proxy.Front
	logf  func(format string, args ...any)

	mu     sync.Mutex
	dev    *device.Device
	tnet   *netstack.Net
	tunDev tun.Device
	bind   *socksBind
	local  netip.Addr
	closed bool

	// startMu 串行化**整个** Start（含下面那段不持 mu 的"等握手"）。
	//
	// 【为什么需要第二把锁（审查 #27）】mu 保护的是"状态"：Stop() 与 Dial() 必须能
	// 随时拿到它，绝不能被 12 秒的握手等待挡住。但"启动过程"本身仍然必须串行 ——
	// 两个并发的 Start 若各建一条隧道，后一个会覆盖前一个，
	// 前一条的设备/UDP 套接字/收包协程就再也没人关了。
	startMu sync.Mutex

	// stopping 是"当前这一轮 Start"的停机信号：stopLocked 会关掉它，
	// 让在锁外等握手的 Start 立刻收手，而不是干等满 12 秒。每轮 Start 换一个新的。
	stopping chan struct{}

	// handshaking 表示"设备已建好、但握手还没确认"。
	//
	// 【为什么必须显式记这个状态】旧实现里 Dial 靠"和 Start 抢同一把锁"顺带保证
	// 不会用到还没握手的隧道；现在 Start 在等握手时已经放开锁，就必须自己拒绝
	// 这个窗口里的拨号（fail-closed），不能把没通的隧道当通的用。
	handshaking bool
}

// New 按配置构造 WireGuard 出口（此时还不会建立隧道）。
func New(node config.Node, front proxy.Front) (proxy.Upstream, error) {
	if node.WireGuard == nil {
		return nil, fmt.Errorf("缺少 WireGuard 配置")
	}
	return &Upstream{node: node, front: front}, nil
}

func (u *Upstream) Describe() string { return u.node.Describe() }

func (u *Upstream) SupportsUDP() bool { return false } // 用户态栈支持 UDP，但先不开放，避免增加不确定性

func (u *Upstream) DialTimeout() time.Duration {
	return time.Duration(u.node.Timeout()) * time.Millisecond
}

func (u *Upstream) log(format string, args ...any) {
	if u.logf != nil {
		u.logf("[WireGuard %s] "+format, append([]any{u.node.Name}, args...)...)
	}
}

// Start 建立 UDP 外层通道 → 用户态网络栈 → WireGuard 设备 → 等待握手。
//
// 【锁的纪律（审查 #27）】12 秒的握手等待**绝不能**放在 u.mu 里做：
// 那会把 Stop()（停止/重启节点、取消测试、端口占用回滚）和 Dial() 一起堵住，
// 现象是"点了停止没反应、要卡十几秒"。所以这里拆成两段：
// setupDevice 持锁把设备建好（建好即发布，Stop 当场就能关掉它），
// 等握手在**锁外**做，并且可以被 Stop 立刻打断。
func (u *Upstream) Start(ctx context.Context, front proxy.Front) error {
	u.startMu.Lock()
	defer u.startMu.Unlock()

	dev, stopCh, err := u.setupDevice(ctx, front)
	if err != nil {
		return err
	}
	if dev == nil {
		return nil // 已经有一条建好且握手确认过的隧道
	}

	// --- 6. 等待握手完成（不持 u.mu）---
	werr := u.waitHandshake(ctx, stopCh, dev.IpcGet, handshakeTimeout)

	u.mu.Lock()
	defer u.mu.Unlock()
	u.handshaking = false
	if u.dev != dev || u.stopping != stopCh {
		// 这一轮已经被 Stop 换掉了：设备早已被 stopLocked 关干净，
		// 此刻 u.* 可能已经属于下一轮，这里绝不能再碰，只需要不报成功。
		if werr == nil {
			werr = errStoppedDuringHandshake
		}
		return werr
	}
	if werr != nil {
		// 握手失败时把设备状态打出来，便于区分"配置没生效"和"对端不回包"：
		// 看得到 public_key / endpoint / allowed_ip / preshared_key 就说明配置进去了，
		// 再看 tx_bytes / rx_bytes 就能判断包到底有没有发出去、有没有收到。
		u.log("握手失败，设备状态：\n%s", u.dumpDeviceState())
		_ = u.stopLocked()
		return werr
	}
	u.log("隧道已建立，出口经 %s:%d", u.node.Server, u.node.Port)
	u.log("设备状态：\n%s", u.dumpDeviceState())
	return nil
}

// setupDevice 建好一条 WireGuard 隧道（外层 UDP + 用户态栈 + 设备 + UAPI），但**不等握手**。
//
// 全程持 u.mu：中间资源必须"要么全部可见、要么全不可见"，否则并发的 Stop()
// 会看到半截状态（例如 bind 挂上了、dev 还是 nil）。建好之后立刻发布到 u.*，
// 正是为了握手等待期间的 Stop() 能真正把它们关掉。
//
// 返回 (nil, nil, nil) 表示已经有一条建好且握手确认过的隧道，调用方直接成功返回。
func (u *Upstream) setupDevice(ctx context.Context, front proxy.Front) (*device.Device, <-chan struct{}, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.dev != nil {
		return nil, nil, nil
	}
	w := u.node.WireGuard
	if w == nil {
		return nil, nil, fmt.Errorf("缺少 WireGuard 配置")
	}
	if front == nil {
		front = u.front
	}
	if front == nil {
		return nil, nil, fmt.Errorf("没有可用的前置代理")
	}

	// --- 1. 外层 UDP：经前置代理的 SOCKS5 UDP ASSOCIATE ---
	udpCh, err := front.DialUDP(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("无法经前置代理建立 UDP 通道：%w", err)
	}

	// --- 2. 解析密钥 ---
	privHex, err := keyToHex(w.PrivateKey)
	if err != nil {
		udpCh.Close()
		return nil, nil, fmt.Errorf("私钥无效：%w", err)
	}
	pubHex, err := keyToHex(w.PeerPublicKey)
	if err != nil {
		udpCh.Close()
		return nil, nil, fmt.Errorf("对端公钥无效：%w", err)
	}

	// --- 3. 用户态网络栈 ---
	localAddr, err := parseAddrWithPrefix(w.Address)
	if err != nil {
		udpCh.Close()
		return nil, nil, fmt.Errorf("隧道地址无效：%w", err)
	}
	u.local = localAddr.Addr()

	dnsAddrs := parseDNSServers(w.DNSServers)
	mtu := w.MTU
	if mtu < 576 || mtu > 9000 {
		mtu = 1420
	}
	tunDev, tnet, err := netstack.CreateNetTUN([]netip.Addr{u.local}, dnsAddrs, mtu)
	if err != nil {
		udpCh.Close()
		return nil, nil, fmt.Errorf("创建用户态网络栈失败：%w", err)
	}

	// --- 4. WireGuard 设备 ---
	bind := &socksBind{
		ch:   udpCh,
		peer: netip.AddrPortFrom(netip.Addr{}, 0),
		logf: u.log,
	}
	if ip := net.ParseIP(u.node.Server); ip != nil {
		a, _ := netip.AddrFromSlice(ip)
		bind.peer = netip.AddrPortFrom(a.Unmap(), uint16(u.node.Port))
	} else {
		// 域名形式交给 wireguard-go 解析不了（我们不提供系统解析），这里本地解析一次
		addrs, lerr := net.DefaultResolver.LookupNetIP(ctx, "ip", u.node.Server)
		if lerr != nil || len(addrs) == 0 {
			_ = tunDev.Close()
			udpCh.Close()
			return nil, nil, fmt.Errorf("无法解析 WireGuard 服务器地址 %q：%v", u.node.Server, lerr)
		}
		bind.peer = netip.AddrPortFrom(addrs[0], uint16(u.node.Port))
	}

	devLevel := device.LogLevelError
	if os.Getenv("CHAINPROXY_WG_VERBOSE") != "" {
		devLevel = device.LogLevelVerbose
	}
	dev := device.NewDevice(tunDev, bind, device.NewLogger(devLevel, "wg: "+u.node.Name+" "))
	bind.dev = dev

	// --- 5. UAPI 配置 ---
	allowed := strings.TrimSpace(w.AllowedIPs)
	if allowed == "" {
		allowed = "0.0.0.0/0, ::/0"
	}
	keepalive := w.PersistentKeepalive
	if keepalive < 0 {
		keepalive = 0
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "private_key=%s\n", privHex)
	fmt.Fprintf(&sb, "listen_port=0\n")
	fmt.Fprintf(&sb, "public_key=%s\n", pubHex)
	// ⚠️ UAPI 要求 allowed_ip **一行一个前缀**。
	// 写成 "allowed_ip=a/24, b/24" 会被解析成单个地址而报
	// "unexpected character"，这是很容易踩的一脚。
	for _, p := range splitAllowedIPs(allowed) {
		fmt.Fprintf(&sb, "allowed_ip=%s\n", p)
	}
	fmt.Fprintf(&sb, "endpoint=%s\n", bind.peer.String())
	fmt.Fprintf(&sb, "persistent_keepalive_interval=%d\n", keepalive)

	// PresharedKey 是可选的；有就必须发，否则对端会静默丢弃握手包。
	if ps := strings.TrimSpace(w.PresharedKey); ps != "" {
		psHex, perr := keyToHex(ps)
		if perr != nil {
			dev.Close()
			return nil, nil, fmt.Errorf("预共享密钥无效：%w", perr)
		}
		fmt.Fprintf(&sb, "preshared_key=%s\n", psHex)
	}

	if err := dev.IpcSet(sb.String()); err != nil {
		dev.Close()
		udpCh.Close()
		return nil, nil, fmt.Errorf("应用 WireGuard 配置失败：%w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, nil, fmt.Errorf("启动 WireGuard 失败：%w", err)
	}

	u.dev, u.tnet, u.tunDev, u.bind = dev, tnet, tunDev, bind
	// 【必须复位 closed】Stop() 之后同一个实例再次 Start 是允许的（重启同一条出口）。
	// 若不复位，下一轮 stopLocked 会因为 closed==true 直接返回：新建设备的收包协程
	// 与 UDP 套接字永远没人关（泄漏，且端口/连接一直挂着）。
	u.closed = false
	u.stopping = make(chan struct{})
	u.handshaking = true
	return dev, u.stopping, nil
}

// dumpDeviceState 返回 wireguard-go 的 UAPI 状态（含握手指纹、收发字节数）。
func (u *Upstream) dumpDeviceState() string {
	if u.dev == nil {
		return "(设备未创建)"
	}
	out, err := u.dev.IpcGet()
	if err != nil {
		return fmt.Sprintf("(读取设备状态失败: %v)", err)
	}
	return out
}

// waitHandshake 轮询 WireGuard 的 last_handshake，确认真的握手上了。
//
// get 由调用方传进来（生产上是 dev.IpcGet），而不是在这里读 u.dev，有两个原因：
//  1. 调用方是在持锁时把 dev 取出来的，而这段轮询恰恰在**解锁之后**跑。
//     直接读 u.dev 既是数据竞争，又**真的会 panic**：Stop 把 u.dev 置空之后
//     才被调度起来的轮询协程，会对着 nil 调 IpcGet；
//  2. 传函数就能在单测里注入"卡住不返回"的假 IpcGet，不必真建一个设备。
//
// 【为什么固定一个轮询协程 + done 通道（审查 #27）】
// IpcGet 要抢 wireguard-go 的 ipcMutex，可能长时间不返回，且**没法从外面打断**。
// 旧实现每轮（300ms）都新开一个协程、2 秒收不到就 continue：一次 12 秒的等待能堆出
// 好几个收不回来的协程，反复重连就是无限堆积，而它们醒来时设备可能已经被 Stop 关掉。
// 现在固定一个协程负责轮询，结果送进带缓冲的通道；调用方只 select
// 结果/超时/ctx 取消/停机信号，返回前 close(done)，轮询协程在下一次边界立刻退出。
func (u *Upstream) waitHandshake(ctx context.Context, stop <-chan struct{}, get func() (string, error), timeout time.Duration) error {
	done := make(chan struct{})
	defer close(done)

	type ipcResult struct {
		out string
		err error
	}
	// 缓冲 1：调用方已经返回（超时/取消）时，轮询协程也能把结果放下就走，不会卡在发送上。
	resCh := make(chan ipcResult, 1)
	ticker := time.NewTicker(handshakePollInterval)
	defer ticker.Stop()
	go func() {
		for {
			out, err := get()
			select {
			case resCh <- ipcResult{out, err}:
			case <-done:
				return
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-stop:
			// 设备已被 Stop() 关掉：如实报"被停止"，不要伪装成超时。
			return errStoppedDuringHandshake
		case <-timer.C:
			return fmt.Errorf("WireGuard 握手超时（%d 秒）：请确认前置代理支持 UDP、服务端地址/密钥正确，且服务端未被墙", int(timeout.Seconds()))
		case res := <-resCh:
			if res.err != nil {
				continue
			}
			if handshakeSeen(res.out) {
				return nil
			}
		}
	}
}

// handshakeSeen 判断 UAPI 状态里有没有出现有效的 last_handshake_time_sec。
//
// 【为什么单独拆成一个纯函数】不碰真实设备就能测；顺带把"什么算握上了"
// 这件事只留一份定义，免得以后有人在这里和别处各写一套。
func handshakeSeen(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		v := strings.TrimPrefix(line, "last_handshake_time_sec=")
		if v == line {
			continue
		}
		if secs, err := strconv.ParseInt(v, 10, 64); err == nil && secs > 0 {
			return true
		}
	}
	return false
}

// Dial 从用户态网络栈建立到目标的连接。
//
// 域名策略：先本地解析成 IP，再让 netstack 连 IP。
// 这样做是为了不依赖隧道内 DNS 是否可用（本版本 netstack 不跑完整 DNS 客户端）。
func (u *Upstream) Dial(ctx context.Context, front proxy.Front, target string) (net.Conn, error) {
	u.mu.Lock()
	tnet := u.tnet
	handshaking := u.handshaking
	u.mu.Unlock()
	if tnet == nil {
		return nil, fmt.Errorf("WireGuard 隧道尚未启动")
	}
	// 【为什么这里要显式拒绝（审查 #27）】Start 现在是在锁外等握手的，而这个窗口里
	// u.tnet 已经发布出来了。旧实现靠"Dial 和 Start 抢同一把锁"顺带保证拨号只发生在
	// 握手确认之后；锁不再覆盖握手等待，就得在这里自己把住，宁可明确报错，
	// 也不能把一条还没通的隧道当通的用（那会变成一堆查不出原因的连接超时）。
	if handshaking {
		return nil, fmt.Errorf("WireGuard 隧道正在建立（尚未完成握手），请稍后重试")
	}

	host, port, err := proxy.SplitHostPort(target, 443)
	if err != nil {
		return nil, err
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if _, perr := netip.ParseAddr(host); perr != nil {
		addrs, lerr := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
		if lerr != nil || len(addrs) == 0 {
			// 本地解析失败就交给 netstack 自己解析（会走隧道内 DNS）
			u.log("本地解析 %q 失败，改由隧道内 DNS 解析", host)
		} else {
			addr = netip.AddrPortFrom(addrs[0], uint16(port)).String()
		}
	}

	c, err := tnet.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("经 WireGuard 连接 %s 失败：%w", target, err)
	}
	return c, nil
}

// DialUDP 目前不支持（见 SupportsUDP）。
func (u *Upstream) DialUDP(ctx context.Context, front proxy.Front) (proxy.UDPChannel, error) {
	return nil, fmt.Errorf("%w", proxy.ErrUDPUnsupported)
}

// Stop 关闭设备、网络栈与 UDP 通道。
func (u *Upstream) Stop() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.stopLocked()
}

// stopLocked 是 Stop 的无锁版本。
//
// 必须单独拆出来：Start() 全程持有 u.mu，而握手失败时需要在 Start 内部调用
// 清理逻辑。若直接调 Stop() 就会二次加锁 —— sync.Mutex 不可重入，
// 表现是"握手超时后程序永久卡死、连错误提示都出不来"。
func (u *Upstream) stopLocked() error {
	if u.closed {
		return nil
	}
	u.closed = true
	// 先打断"锁外等握手"的那一轮：关掉信号之后 waitHandshake 立刻返回
	// （否则它要干等满 handshakeTimeout 才收手）。它也顺带告诉 Start：
	// 这一轮已经作废，不要再对 u.* 做任何事。
	if u.stopping != nil {
		close(u.stopping)
		u.stopping = nil
	}
	u.handshaking = false

	var firstErr error
	// 【顺序：必须先关 bind，再关设备】
	//
	// 原因：device.Close() 会**等待收包协程退出**，而收包协程正阻塞在
	// b.ch.ReceiveFromUDP(...) 上（UDP 套接字阻塞读，故意不设读超时）。
	// 若先调 device.Close()，收包协程永远等不到数据、device.Close() 就永远不返回 —— 死锁。
	// 实测表现是「点停止后界面卡死」「命令行 test 卡住不退出」。
	//
	// 反过来先 shutdown() 会关掉 UDP 套接字，阻塞读立刻返回，收包协程正常退出，
	// 之后 device.Close() 才能干净收尾。
	// 这期间设备可能还会再调一次 Send，而 b.ch 已置空 —— Send 里已加空值保护。
	if u.bind != nil {
		if err := u.bind.shutdown(); err != nil {
			firstErr = err
		}
	}
	// device.Close() 内部已经会关闭 tun 设备，所以不能再单独关一次
	// （netstack 的 netTun.Close 非幂等，第二次会 panic）。
	if u.dev != nil {
		u.dev.Close()
		u.dev = nil
	} else if u.tunDev != nil {
		_ = u.tunDev.Close()
	}
	u.tunDev = nil
	u.tnet = nil
	u.bind = nil
	return firstErr
}

// ------------------------------------------------------------------
// 密钥与地址解析
// ------------------------------------------------------------------

// keyToHex 把 WireGuard 密钥转成 UAPI 需要的十六进制。
// 兼容两种写法：wg-quick 用的 base64，和 wg 命令行 UAPI 用的 hex。
func keyToHex(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("密钥为空")
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return strings.ToLower(s), nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return hex.EncodeToString(b), nil
	}
	return "", fmt.Errorf("无法识别的密钥格式（应为 32 字节 hex 或 base64）")
}

// parseAddrWithPrefix 解析 "10.7.0.2/32"。
func parseAddrWithPrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Prefix{}, fmt.Errorf("隧道地址为空，例如 10.7.0.2/32")
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		a, aerr := netip.ParseAddr(s)
		if aerr != nil {
			return netip.Prefix{}, fmt.Errorf("格式应为 CIDR，例如 10.7.0.2/32")
		}
		return netip.PrefixFrom(a, a.BitLen()), nil
	}
	return p, nil
}

// parseDNSServers 解析可选的 DNS 列表（供 netstack 内部解析使用）。
func parseDNSServers(list []string) []netip.Addr {
	out := make([]netip.Addr, 0, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, a)
		}
	}
	return out
}

// splitAllowedIPs 把 "a/24, b/24" 这种写法拆成一个个前缀。
// UAPI 的 allowed_ip 必须是单值，逗号列表会解析失败。
func splitAllowedIPs(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		p := strings.TrimSpace(part)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []string{"0.0.0.0/0"}
	}
	return out
}
