// Package engine 用内嵌的 sing-box 库提供 VMess / VLESS / Shadowsocks / Trojan /
// Hysteria2 五种协议的出口实现。
//
// # 为什么是 sing-box
//
// 这五种协议的握手、加密、复用、Reality 抗主动探测等细节极其容易写错，
// 而 sing-box 已经把它们实现并验证过了。把它当**库**内嵌（而不是拉起一个
// 外部进程）的好处是：
//   - 仍然是单文件可执行程序，不需要额外分发 core 可执行文件
//   - 出口对象在本进程内，可以被我们的 mixed 入口直接复用
//   - 不需要为一个节点开一个子进程、管一套生命周期
//
// # 怎么表达"前置代理"
//
// 每个节点对应一个**只有出口、没有入口**的 sing-box 实例：
//
//	outbounds[0] = 前置出口（direct / socks5 / http）  tag=cp-front
//	outbounds[1] = 本节点出口，detour=cp-front          tag=cp-node
//
// 这就是 sing-box 的链式代理（等价于 Xray 的 dialerProxy）：
// cp-node 要连自己的服务器时，先经由 cp-front 出去。
//
// 当节点选择"直连"时不生成 cp-front、也不写 detour
// （sing-box 明确不允许 direct 出口有 detour）。
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/buf"
	singjson "github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"chainproxy/internal/config"
	"chainproxy/internal/proxy"
)

// 出口标签。配置里生成的是这两个名字，取用时也用它们。
const (
	tagFront = "cp-front"
	tagNode  = "cp-node"
)

// New 按节点配置创建一个 sing-box 出口。
//
// front 是已经解析好的前置（直连 / 全局 / 另一个节点的本地端口），
// 由上层负责解析，engine 这里只负责把它翻译成 sing-box 的配置。
func New(n config.Node, front config.Front, logf func(string, ...any)) (proxy.Upstream, error) {
	if !config.UsesSingBox(n.Protocol) {
		return nil, fmt.Errorf("engine 不支持协议 %q（只负责 vmess/vless/shadowsocks/trojan/hysteria2）", n.Protocol)
	}
	if err := BuildCheck(); err != nil {
		return nil, err
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Upstream{node: n, front: front, logf: logf}, nil
}

// BuildCheck 检查当前二进制是否带了必要的构建标签。
//
// 没带的话 Reality 与 Hysteria2 会在运行期才失败，这里提前给出可操作的提示。
func BuildCheck() error {
	if buildTagsOK {
		return nil
	}
	return fmt.Errorf("当前程序缺少构建标签 %s，导致 Reality / Hysteria2 不可用；"+
		"请用 -tags \"with_utls,with_quic\" 重新编译（scripts/build.ps1 已内置）",
		strings.Join(missingBuildTags(), ","))
}

// Upstream 是 sing-box 支撑的出口实现，满足 proxy.Upstream 接口。
type Upstream struct {
	node  config.Node
	front config.Front
	logf  func(string, ...any)

	mu   sync.Mutex
	inst *box.Box
	ob   adapter.Outbound
}

var _ proxy.Upstream = (*Upstream)(nil)

// Describe 返回一行可读描述。
func (u *Upstream) Describe() string { return u.node.Describe() }

// DialTimeout 返回单条链路建立的超时。
func (u *Upstream) DialTimeout() time.Duration {
	return time.Duration(u.node.Timeout()) * time.Millisecond
}

// SupportsUDP 说明该出口能否转发 UDP。
//
// 五种协议都支持 UDP（VMess/VLESS 走 packet 编码、Shadowsocks 原生支持、
// Trojan 用 UDP ASSOCIATE、Hysteria2 本身就是 QUIC），
// 所以这里统一返回 true，真正的失败会在 DialUDP 时报出来。
func (u *Upstream) SupportsUDP() bool { return true }

// Start 生成配置、拉起 sing-box 实例，并取出节点出口备用。
func (u *Upstream) Start(ctx context.Context, _ proxy.Front) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.inst != nil {
		return nil
	}

	raw, err := BuildConfigJSON(u.node, u.front)
	if err != nil {
		return err
	}
	u.logf("[engine] 「%s」sing-box 配置: %s", u.node.Name, string(raw))

	// include.Context 把 sing-box 全部协议的注册表挂进 ctx；
	// 缺了它，option 反序列化会报 "missing outbound options registry in context"。
	//
	// 这里**故意每个实例都用一个新的 ctx**：box.New 会往 ctx 里注册本实例的
	// 各种 Manager（例如 adapter.OutboundManager），共享 ctx 会让多个实例互相覆盖。
	sbCtx := include.Context(context.Background())

	opts, err := singjson.UnmarshalExtendedContext[option.Options](sbCtx, raw)
	if err != nil {
		return fmt.Errorf("sing-box 配置解析失败: %w", err)
	}

	inst, err := box.New(box.Options{
		Context:           sbCtx,
		Options:           opts,
		PlatformLogWriter: &platformWriter{logf: u.logf, name: u.node.Name},
	})
	if err != nil {
		return fmt.Errorf("sing-box 实例构造失败: %w", err)
	}
	if err := inst.Start(); err != nil {
		_ = inst.Close()
		return fmt.Errorf("sing-box 实例启动失败: %w", err)
	}

	ob, ok := inst.Outbound().Outbound(tagNode)
	if !ok {
		_ = inst.Close()
		return fmt.Errorf("sing-box 实例里没有找到出口 %q", tagNode)
	}

	u.inst = inst
	u.ob = ob
	u.logf("[engine] 「%s」已就绪（%s）", u.node.Name, u.Describe())
	return nil
}

// Stop 关闭 sing-box 实例。
func (u *Upstream) Stop() error {
	u.mu.Lock()
	inst := u.inst
	u.inst = nil
	u.ob = nil
	u.mu.Unlock()
	if inst != nil {
		return inst.Close()
	}
	return nil
}

// Dial 建立到 target（"host:port"）的 TCP 连接。
func (u *Upstream) Dial(ctx context.Context, _ proxy.Front, target string) (net.Conn, error) {
	ob, err := u.outbound()
	if err != nil {
		return nil, err
	}
	conn, err := ob.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(target))
	if err != nil {
		return nil, fmt.Errorf("%s 连接 %s 失败: %w", config.ProtocolLabel(u.node.Protocol), target, err)
	}
	return conn, nil
}

// DialUDP 建立 UDP 通道。
func (u *Upstream) DialUDP(ctx context.Context, _ proxy.Front) (proxy.UDPChannel, error) {
	ob, err := u.outbound()
	if err != nil {
		return nil, err
	}
	pc, err := ob.ListenPacket(ctx, M.Socksaddr{})
	if err != nil {
		return nil, fmt.Errorf("%s 打开 UDP 通道失败: %w", config.ProtocolLabel(u.node.Protocol), err)
	}
	return newUDPChannel(pc)
}

func (u *Upstream) outbound() (adapter.Outbound, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ob == nil {
		return nil, fmt.Errorf("sing-box 出口尚未启动")
	}
	return u.ob, nil
}

// ------------------------------------------------------------------
// 配置生成
// ------------------------------------------------------------------

// BuildConfigJSON 生成该节点的 sing-box 配置（JSON）。
//
// 单独导出是为了让测试可以直接断言生成的配置内容，
// 不需要真的去启动一个实例。
func BuildConfigJSON(n config.Node, front config.Front) ([]byte, error) {
	nodeOut, err := buildNodeOutbound(n)
	if err != nil {
		return nil, err
	}

	cfg := map[string]any{
		// 级别保持 warn。曾经为了拿"连接流水"改成 info，实测**一条都没有** ——
		// 我们的 sing-box 只当出口、且绕过它的路由器直连 outbound，
		// 那条 "outbound connection to" 是路由器在路由时才打的（详见 proxy/connlog.go）。
		// 连接流水改由我们自己的 handler 上报。
		//
		// 另一条路仍然有效：PlatformLogWriter 收了**全部级别**（sing-box 的
		// observableLogger 只要挂了 platform writer 就不再按级别提前返回），
		// 由它决定哪些进网络日志、哪些进用户日志。
		"log": map[string]any{
			"disabled": false,
			"level":    "warn",
		},
		// 不做 TUN、不做系统代理，只当一个出口用。
		"route": map[string]any{
			"auto_detect_interface": false,
		},
	}

	outbounds := make([]any, 0, 2)
	if fo := buildFrontOutbound(front); fo != nil {
		outbounds = append(outbounds, fo)
		// 只有存在前置出口时才挂 detour。
		nodeOut["detour"] = tagFront
	}
	nodeOut["tag"] = tagNode
	outbounds = append(outbounds, nodeOut)
	cfg["outbounds"] = outbounds

	return json.MarshalIndent(cfg, "", "  ")
}

// buildFrontOutbound 把前置代理翻译成 sing-box 出口。
//
// 返回 nil 表示"直连"，也就是不需要前置出口、也不需要 detour。
func buildFrontOutbound(front config.Front) map[string]any {
	host := strings.TrimSpace(front.Host)
	if front.Type == config.FrontTypeDirect || host == "" || front.Port <= 0 {
		return nil
	}
	base := map[string]any{
		"tag":         tagFront,
		"server":      host,
		"server_port": front.Port,
	}
	switch front.Type {
	case config.FrontTypeHTTP:
		base["type"] = "http"
	default:
		base["type"] = "socks"
		base["version"] = "5"
	}
	if front.Username != "" {
		base["username"] = front.Username
	}
	if front.Password != "" {
		base["password"] = front.Password
	}
	return base
}

// buildNodeOutbound 生成节点自身的出口配置（不含 tag / detour）。
func buildNodeOutbound(n config.Node) (map[string]any, error) {
	m := map[string]any{
		"server":      n.Server,
		"server_port": n.Port,
	}

	switch n.Protocol {
	case config.ProtocolVMess:
		m["type"] = "vmess"
		m["uuid"] = n.UUID
		if n.Security != "" {
			m["security"] = n.Security
		}
		if n.AlterID != 0 {
			m["alter_id"] = n.AlterID
		}
		applyTransport(m, n)
		applyTLS(m, n)

	case config.ProtocolVLESS:
		m["type"] = "vless"
		m["uuid"] = n.UUID
		if strings.TrimSpace(n.Flow) != "" {
			m["flow"] = strings.TrimSpace(n.Flow)
		}
		applyTransport(m, n)
		applyTLS(m, n)

	case config.ProtocolShadowsocks:
		m["type"] = "shadowsocks"
		m["method"] = n.Method
		m["password"] = n.Password

	case config.ProtocolTrojan:
		m["type"] = "trojan"
		m["password"] = n.Password
		applyTransport(m, n)
		applyTLS(m, n)

	case config.ProtocolHysteria2:
		m["type"] = "hysteria2"
		m["password"] = n.Password
		// 带宽为 0 表示不声明，让 Brutal 拥塞控制自己去探测；
		// 显式写 0 反而可能被当成"限速到 0"，所以只有大于 0 才写。
		if n.UpMbps > 0 {
			m["up_mbps"] = n.UpMbps
		}
		if n.DownMbps > 0 {
			m["down_mbps"] = n.DownMbps
		}
		if strings.TrimSpace(n.Obfs) != "" {
			m["obfs"] = map[string]any{
				"type":     strings.TrimSpace(n.Obfs),
				"password": n.ObfsPassword,
			}
		}
		applyTLS(m, n)

	default:
		return nil, fmt.Errorf("不支持的协议 %q", n.Protocol)
	}

	return m, nil
}

// applyTransport 写入传输层配置。
//
// 注意：sing-box 的 transport.type 只接受 http/ws/quic/grpc/httpupgrade，
// **没有 tcp** —— 用 tcp 时必须整个不写 transport 字段。
func applyTransport(m map[string]any, n config.Node) {
	network := strings.ToLower(strings.TrimSpace(n.Transport.Network))
	if network == "" {
		network = "tcp"
	}
	switch network {
	case "tcp":
		// http 伪装头（主流客户端的 tcp+http header）sing-box 不支持，忽略。
		return
	case "ws":
		t := map[string]any{"type": "ws"}
		if p := strings.TrimSpace(n.Transport.Path); p != "" {
			t["path"] = p
		}
		if h := strings.TrimSpace(n.Transport.Host); h != "" {
			t["headers"] = map[string]any{"Host": h}
		}
		m["transport"] = t
	case "grpc":
		t := map[string]any{"type": "grpc"}
		if s := strings.TrimSpace(n.Transport.ServiceName); s != "" {
			t["service_name"] = s
		}
		m["transport"] = t
	case "http", "h2":
		t := map[string]any{"type": "http"}
		if h := strings.TrimSpace(n.Transport.Host); h != "" {
			// http 传输的 host 是数组（可以有多个域名轮换）
			t["host"] = []string{h}
		}
		if p := strings.TrimSpace(n.Transport.Path); p != "" {
			t["path"] = p
		}
		m["transport"] = t
	case "httpupgrade":
		t := map[string]any{"type": "httpupgrade"}
		if h := strings.TrimSpace(n.Transport.Host); h != "" {
			t["host"] = h
		}
		if p := strings.TrimSpace(n.Transport.Path); p != "" {
			t["path"] = p
		}
		m["transport"] = t
	default:
		// 不认识的传输层类型：原样透传，让 sing-box 自己报错，
		// 这样用户能看到"不支持 ws 之外的 xxx"这种明确信息。
		m["transport"] = map[string]any{"type": network}
	}
}

// applyTLS 写入 TLS / Reality 配置。
func applyTLS(m map[string]any, n config.Node) {
	tls := n.TLS
	if !tls.Enabled {
		return
	}
	out := map[string]any{"enabled": true}

	sni := strings.TrimSpace(tls.ServerName)
	if sni == "" {
		// 没填 SNI 时退回服务器地址（很多导出的链接就是这样）。
		sni = strings.TrimSpace(n.Server)
	}
	if sni != "" && net.ParseIP(sni) == nil {
		out["server_name"] = sni
	}
	if tls.AllowInsecure {
		out["insecure"] = true
	}
	if len(tls.ALPN) > 0 {
		alpn := make([]string, 0, len(tls.ALPN))
		for _, a := range tls.ALPN {
			if v := strings.TrimSpace(a); v != "" {
				alpn = append(alpn, v)
			}
		}
		if len(alpn) > 0 {
			out["alpn"] = alpn
		}
	}
	if pin := strings.TrimSpace(n.PinSHA256); pin != "" {
		// Hysteria2 的证书公钥固定（ShareLink 里的 pinSHA256）。
		out["certificate_public_key_sha256"] = []string{pin}
	}

	if tls.EnabledReality() {
		// Reality 必须配合 uTLS，sing-box 在缺 uTLS 时会直接拒绝启动。
		fp := strings.TrimSpace(tls.Fingerprint)
		if fp == "" {
			fp = "chrome"
		}
		out["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		reality := map[string]any{
			"enabled":    true,
			"public_key": strings.TrimSpace(tls.RealityPublicKey),
		}
		if sid := strings.TrimSpace(tls.RealityShortID); sid != "" {
			reality["short_id"] = sid
		}
		// 注意：sing-box 的 reality 出口没有 spider_x 字段（那是 Xray 的客户端填充参数），
		// 所以 RealitySpiderX 只用于导入导出与分享，不参与连接。
		out["reality"] = reality
	} else if fp := strings.TrimSpace(tls.Fingerprint); fp != "" {
		out["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}

	m["tls"] = out
}

// ------------------------------------------------------------------
// 日志桥接
// ------------------------------------------------------------------

// platformWriter 把 sing-box 的日志转发到本项目自己的日志里。
//
// 刻意只转发 warn 以上：sing-box 在 info 级别会打一堆
// "network: updated default interface" 之类的噪音，
// 对一个带界面日志面板的桌面程序来说没有价值。
type platformWriter struct {
	logf func(string, ...any)
	name string
}

func (w *platformWriter) WriteMessage(level log.Level, message string) {
	// 【两条路，用途不同】
	//
	//   - 网络日志：**全部级别**都喂过去。连接流水（inbound connection from / to、
	//     outbound connection to）在 info 级，正是底部"网络日志"要展示的东西。
	//   - 用户日志：仍然只收 warn 以上 —— info 级会混进一堆
	//     "updated default interface" 之类的噪音，对操作日志没有价值。
	if sink := networkLogSink(); sink != nil {
		sink(w.name, log.FormatLevel(level), message)
	}
	if level < log.LevelWarn {
		return
	}
	w.logf("[sing-box][%s] %s", w.name, message)
}

// netSink 是"网络日志"的接收器（进程内唯一）。
var (
	netSinkMu sync.Mutex
	netSink   func(node, level, msg string)
)

// SetNetworkLogSink 注册网络日志接收器；传 nil 取消。
//
// 为什么不把它做成 New 的第四个参数：那要顺着
// app → proxy.NewServer → NewUpstream → engine.New 改四层签名（还有一批测试），
// 而这个接收器与"哪个节点"无关 —— 它最终落到 app 的一个环形缓冲里，
// 是**进程级唯一**的东西，一次注册比层层透传更贴切。
func SetNetworkLogSink(fn func(node, level, msg string)) {
	netSinkMu.Lock()
	netSink = fn
	netSinkMu.Unlock()
}

func networkLogSink() func(node, level, msg string) {
	netSinkMu.Lock()
	defer netSinkMu.Unlock()
	return netSink
}

// ------------------------------------------------------------------
// UDP 通道适配
// ------------------------------------------------------------------

// newUDPChannel 把 sing-box 返回的 UDP 套接字适配成本项目的 UDPChannel。
//
// sing-box 的 Outbound.ListenPacket 声明返回的是 net.PacketConn，但实际
// 绝大多数协议的实现都会额外交付 N.PacketConn（带"目标地址可能是域名"的能力）。
// 所以这里优先用它；万一某个协议只给了普通 net.PacketConn，也有兜底路径。
func newUDPChannel(pc net.PacketConn) (proxy.UDPChannel, error) {
	if np, ok := pc.(N.PacketConn); ok {
		return newSingUDPChannel(np), nil
	}
	if np, ok := pc.(nPacketConn); ok {
		return newNetUDPChannel(np), nil
	}
	_ = pc.Close()
	return nil, fmt.Errorf("无法适配 sing-box 返回的 UDP 通道（%T）", pc)
}

// nPacketConn 是"只实现了 ReadFrom/WriteTo"的兜底形态。
type nPacketConn interface {
	ReadFrom(p []byte) (n int, addr net.Addr, err error)
	WriteTo(p []byte, addr net.Addr) (n int, err error)
	Close() error
}

// 为什么要开一个后台收包协程：sing 的 ReadPacket / net.PacketConn.ReadFrom
// 都是阻塞且不可取消的，而我们的 ReceiveFromUDP 需要能被 quit 立刻叫醒
// （WireGuard 的 Bind 关闭流程强依赖这一点，否则会死锁）。
type udpPacket struct {
	payload []byte
	host    string
	port    int
}

// 队列长度：UDP 是实时流量，宁可丢旧包也不要堆积。
const udpQueueLen = 128

func deliver(ch chan udpPacket, done <-chan struct{}, p udpPacket) bool {
	select {
	case ch <- p:
		return true
	case <-done:
		return false
	default:
	}
	// 队列满：丢最旧的一条，保证实时性（DNS/QUIC 对陈旧数据报不敏感）
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- p:
		return true
	case <-done:
		return false
	}
}

// singUDPChannel 包装 N.PacketConn。
type singUDPChannel struct {
	pc   N.PacketConn
	pkts chan udpPacket
	done chan struct{}

	closeOnce sync.Once
}

func newSingUDPChannel(pc N.PacketConn) *singUDPChannel {
	c := &singUDPChannel{
		pc:   pc,
		pkts: make(chan udpPacket, udpQueueLen),
		done: make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *singUDPChannel) readLoop() {
	defer close(c.done)
	for {
		b := buf.New()
		dest, err := c.pc.ReadPacket(b)
		if err != nil {
			b.Release()
			return
		}
		payload := make([]byte, len(b.Bytes()))
		copy(payload, b.Bytes())
		b.Release()

		host := dest.Addr.String()
		if dest.IsFqdn() {
			host = dest.Fqdn
		}
		if !deliver(c.pkts, c.done, udpPacket{payload: payload, host: host, port: int(dest.Port)}) {
			return
		}
	}
}

func (c *singUDPChannel) SendToUDP(payload []byte, host string, port int) error {
	b := buf.NewSize(len(payload))
	if _, err := b.Write(payload); err != nil {
		b.Release()
		return err
	}
	// WritePacket 成功时接走 buffer 的所有权，失败时由它或我们释放。
	return c.pc.WritePacket(b, M.ParseSocksaddrHostPort(host, uint16(port)))
}

func (c *singUDPChannel) ReceiveFromUDP(quit <-chan struct{}, out []byte) (int, string, int, error) {
	select {
	case p := <-c.pkts:
		n := copy(out, p.payload)
		return n, p.host, p.port, nil
	case <-quit:
		return 0, "", 0, proxy.ErrUDPChannelClosed
	case <-c.done:
		return 0, "", 0, net.ErrClosed
	}
}

func (c *singUDPChannel) Close() error {
	var err error
	c.closeOnce.Do(func() { err = c.pc.Close() })
	return err
}

// netUDPChannel 是只拿到普通 net.PacketConn 时的兜底实现。
//
// 代价是目标地址只能是 IP（ReadFrom 给不出域名），
// 对 DNS/QUIC 这类"本来就是 IP"的流量没有影响。
type netUDPChannel struct {
	pc   nPacketConn
	pkts chan udpPacket
	done chan struct{}

	closeOnce sync.Once
}

func newNetUDPChannel(pc nPacketConn) *netUDPChannel {
	c := &netUDPChannel{
		pc:   pc,
		pkts: make(chan udpPacket, udpQueueLen),
		done: make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *netUDPChannel) readLoop() {
	defer close(c.done)
	b := make([]byte, 64*1024)
	for {
		n, addr, err := c.pc.ReadFrom(b)
		if err != nil {
			return
		}
		payload := make([]byte, n)
		copy(payload, b[:n])
		host, port := splitAddr(addr)
		if !deliver(c.pkts, c.done, udpPacket{payload: payload, host: host, port: port}) {
			return
		}
	}
}

func (c *netUDPChannel) SendToUDP(payload []byte, host string, port int) error {
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	_, err = c.pc.WriteTo(payload, addr)
	return err
}

func (c *netUDPChannel) ReceiveFromUDP(quit <-chan struct{}, out []byte) (int, string, int, error) {
	select {
	case p := <-c.pkts:
		n := copy(out, p.payload)
		return n, p.host, p.port, nil
	case <-quit:
		return 0, "", 0, proxy.ErrUDPChannelClosed
	case <-c.done:
		return 0, "", 0, net.ErrClosed
	}
}

func (c *netUDPChannel) Close() error {
	var err error
	c.closeOnce.Do(func() { err = c.pc.Close() })
	return err
}

func splitAddr(addr net.Addr) (string, int) {
	switch v := addr.(type) {
	case *net.UDPAddr:
		return v.IP.String(), v.Port
	case *net.TCPAddr:
		return v.IP.String(), v.Port
	case nil:
		return "", 0
	default:
		host, portStr, err := net.SplitHostPort(v.String())
		if err != nil {
			return v.String(), 0
		}
		port, _ := strconv.Atoi(portStr)
		return host, port
	}
}
