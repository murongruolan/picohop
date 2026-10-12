package engine

// mihomo 出站适配层（迁移第 ③④ 步）。
//
// 【为什么这样用 mihomo】mihomo 常规用法是"配置文件驱动 + hub.Parse 起一个全局引擎"，
// 那样一个进程只能有一个引擎，我们的"每个节点一个独立出口 + 各自一个本地端口、
// 可同时激活多个"就不成立了。
//
// 所以这里只把它当**拨号器库**用：每个节点建一个**独立出站实例**，
// 直接调 DialContext / ListenPacketContext，**不经 hub.Parse、不建 inbound**。
// 这一点在迁移第 1 轮已用探针实测过（独立拨通 xhttp + REALITY 节点并拿到 204）：
//   ✅ 隧道建立（382 ms）-> 204.197.163.238:30022
//   ✅ 响应首行：HTTP/1.1 204 No Content
//
// 【前置（链式代理）怎么表达】mihomo 的常规做法是 dialer-proxy，那要一个全局 hub；
// 我们走的是每个出站自己的 BasicOption.DialerForAPI —— 见 mihomo_dialer.go 的完整说明。
//
// 【与 sing-box 那个实现的关系】两者实现同一个 proxy.Upstream 接口，形状完全一致，
// 上层（handler / 链路 / 统计 / 网络日志）不需要知道底下是谁。

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"

	"chainproxy/internal/config"
	"chainproxy/internal/proxy"
)

// MihomoUpstream 用 mihomo 的出站层实现 proxy.Upstream。
type MihomoUpstream struct {
	node  config.Node
	front config.Front
	logf  func(string, ...any)

	// dialer 是"经前置"的拨号器；直连时为 nil（出站用 mihomo 自己的默认拨号器）。
	// 出站是构造时建好的，所以它也必须在这里存一份：Start 的兜底重建要用。
	dialer C.Dialer

	mu  sync.Mutex
	out C.ProxyAdapter
	// built 是构造时就建好的出站实例；Stop 只清 out（激活态），built 留着以便重新激活。
	built C.ProxyAdapter
}

// NewMihomo 构造一个走 mihomo 出站层的出口。
//
// front 是上层（internal/app 的 resolveFront）按节点的「前置」字段解析出来的
// **第一跳**：直连节点是 {Type: direct}，选全局前置的节点就是设置里那份前置，
// 以另一个节点为前置的节点则是一个指向它本地端口的 socks5。
//
// 【前置是怎么生效的】非直连时，我们把一个自己的 C.Dialer（mihomoDialer）装进
// 各协议的 BasicOption.DialerForAPI —— 出站"连服务器"那一跳因此经前置出去，
// TCP 与 UDP 两条路径都覆盖（证据与覆盖范围见 mihomo_dialer.go）。
// 直连时**不装**，让 mihomo 用它自己的默认拨号器（interface-name / routing-mark
// 等选项照旧生效）。
//
// 真正的出站实例在 Start 里创建（与 sing-box 侧一致：Start 才"拉起实例"）。
func NewMihomo(n config.Node, front config.Front, logf func(string, ...any)) (proxy.Upstream, error) {
	if !mihomoSupports(n.Protocol) {
		return nil, fmt.Errorf("mihomo 适配层暂不支持协议 %q（本步只接了 vless；其余协议按迁移契约第 ④ 步逐条扩）", n.Protocol)
	}
	viaFront, reject := mihomoFrontPlan(n, front)
	if reject != "" {
		// 【fail-closed】宁可这个节点起不来，也不许它从本机 IP 出去。
		return nil, fmt.Errorf("节点「%s」%s，mihomo 适配层做不了，已拒绝启动（避免静默用本机 IP 直连）", n.Name, reject)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	// 【为什么在构造时就把出站建出来】映射错误（字段缺失、传输不支持等）原本要等到
	// Start 才暴露，那样上层就没法回退了 —— 用户会看到"节点激活失败"，
	// 而这个节点本来是能用的（sing-box 下能跑，mihomo 只该是"换了个内核"）。
	// 构造时就建好，换装期不会让任何节点从可用变不可用。
	var dialer C.Dialer
	if viaFront {
		dialer = newMihomoDialer(front)
	}
	out, err := buildMihomoOutbound(n, dialer)
	if err != nil {
		return nil, err
	}
	return &MihomoUpstream{node: n, front: front, logf: logf, dialer: dialer, built: out}, nil
}

// mihomoFrontPlan 决定这个节点的第一跳怎么走，以及有没有"确实做不到"的情况。
//
// 返回 (viaFront, reject)：
//   - viaFront=true 表示要经前置拨号（调用方会把 mihomoDialer 装进出站）；
//   - reject 非空表示这种组合 mihomo 适配层做不到，必须**拒绝启动**。
//
// 【为什么判据看的是解析出来的 front，而不是节点的 Front 字段】
// 上层只有一个解析处（app.resolveFrontLocked）：直连 → {Type: direct}、
// global → 设置里那份前置、节点 ID → 指向该节点本地端口的 socks5。所以
// "节点字段"能表达的三种形态在 front 里都有对应，而**真正决定往哪拨的是 front**。
// 唯一的例外补一条一致性检查：节点以另一个节点为前置（那是**必须有**一跳 socks5 的
// 形态），解析出来却是直连 —— 正常流程不该出现，一律拒绝，因为按直连跑就是从本机 IP 出去。
// （反过来，"节点选全局前置、而全局前置本身被设成 direct"是合法配置：那就是不走前置，
// 与 sing-box 侧 buildFrontOutbound 对 direct 的处理一致，不能拒。）
//
// 【为什么"非显式 direct"都算有前置】proxy.NewFront 的 default 分支造的就是
// socks5Front：Type 不是 "direct"（空串也是）时跑起来都是一跳真实的前置
// （singbox 侧 buildFrontOutbound 同样这么判，见审查 #10）。零值 Front
// （什么都没填）是唯一的例外，按"调用方没给前置信息"处理，等同直连。
func mihomoFrontPlan(n config.Node, front config.Front) (bool, string) {
	if isDirectFront(front) {
		if n.FrontNodeID() != "" {
			return false, fmt.Sprintf("前置指向另一个节点（%s），但解析出来的第一跳却是直连", n.FrontNodeID())
		}
		return false, ""
	}
	// 经前置：目前只有"整个协议跑在 UDP 上、而前置只能走 TCP"这一种确实做不到。
	if n.Protocol == config.ProtocolHysteria2 && front.Type == config.FrontTypeHTTP {
		return true, "Hysteria2 整个协议跑在 QUIC(UDP) 上，而 HTTP 前置不支持 UDP 转发" +
			"（换成 SOCKS5 前置即可）"
	}
	return true, ""
}

// isDirectFront 判断一份前置配置是不是"直连"。
//
// 显式 direct 与**零值**（一个字段都没填 = 调用方没给前置信息）都算直连；
// 其余一律按"有前置"处理。别把它简化成只认 Type：空 Type 在 proxy.NewFront 的
// default 分支里就是 socks5 前置，漏判会让节点偷偷直连。
func isDirectFront(f config.Front) bool {
	if f.Type == config.FrontTypeDirect {
		return true
	}
	return f.Type == "" && strings.TrimSpace(f.Host) == "" && f.Port == 0
}

// mihomoSupports 目前只声明已经写好的协议。
//
// 刻意不用"所有协议都返回 true"的写法：宁愿在构造时就明确报错，
// 也不要等用户点了激活才发现没接。
func mihomoSupports(protocol string) bool {
	switch protocol {
	case config.ProtocolVLESS, config.ProtocolVMess, config.ProtocolTrojan,
		config.ProtocolShadowsocks, config.ProtocolHysteria2:
		return true
	default:
		return false
	}
}

func (u *MihomoUpstream) Describe() string { return u.node.Describe() }

func (u *MihomoUpstream) DialTimeout() time.Duration {
	return time.Duration(u.node.Timeout()) * time.Millisecond
}

// SupportsUDP 沿用本项目既有约定：这里先声明"能"，
// 真接不上时由 DialUDP 报出具体原因。
//
// 【现状】mihomo 侧的 UDP（ListenPacketContext）尚未接入（迁移契约第 ④ 步），
// 所以 DialUDP 会返回明确的错误而不是静默失败。
func (u *MihomoUpstream) SupportsUDP() bool { return true }

func (u *MihomoUpstream) Start(ctx context.Context, _ proxy.Front) error {
	u.mu.Lock()
	out := u.built
	if out == nil {
		// 兜底：正常流程里构造时就建好了
		built, err := buildMihomoOutbound(u.node, u.dialer)
		if err != nil {
			u.mu.Unlock()
			return err
		}
		u.built, out = built, built
	}
	u.out = out
	u.mu.Unlock()
	// 把"这一跳经谁出去"写进日志：出问题时用户能一眼看出前置有没有生效。
	via := ""
	if !isDirectFront(u.front) {
		via = fmt.Sprintf("；经前置 %s", u.front.Describe())
	}
	u.logf("「%s」mihomo 出站已就绪：%s（type=%v）%s", u.node.Name, out.Addr(), out.Type(), via)
	return nil
}

func (u *MihomoUpstream) Stop() error {
	u.mu.Lock()
	u.out = nil
	u.mu.Unlock()
	return nil
}

func (u *MihomoUpstream) outbound() (C.ProxyAdapter, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.out == nil {
		return nil, fmt.Errorf("mihomo 出站尚未启动")
	}
	return u.out, nil
}

// Dial 建立到 target（"host:port"）的连接。
func (u *MihomoUpstream) Dial(ctx context.Context, _ proxy.Front, target string) (net.Conn, error) {
	out, err := u.outbound()
	if err != nil {
		return nil, err
	}
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, fmt.Errorf("目标地址非法 %q: %w", target, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("目标端口非法 %q: %w", portStr, err)
	}
	// Metadata 是 constant.Metadata（mihomo 的接口签名里就是它）
	conn, err := out.DialContext(ctx, &C.Metadata{
		Host:    host,
		DstPort: uint16(port),
		NetWork: C.TCP,
	})
	if err != nil {
		return nil, fmt.Errorf("mihomo 建立链路失败: %w", err)
	}
	return conn, nil
}

// DialUDP 建立 UDP 通道。
//
// 【未接入】mihomo 的 UDP 走 ListenPacketContext（返回 C.PacketConn），
// 要把它包成 proxy.UDPChannel（SendToUDP / ReceiveFromUDP + quit 语义）才算完成，
// 见迁移契约第 ④ 步。这里明确报错，不假装成功。
func (u *MihomoUpstream) DialUDP(ctx context.Context, _ proxy.Front) (proxy.UDPChannel, error) {
	out, err := u.outbound()
	if err != nil {
		return nil, err
	}
	// 尽早失败：出站自己说它不支持 UDP 时，别等到用户发了第一个包才报。
	// （mihomo 的 Base.SupportUDP 读的就是各协议选项里的 udp 字段，我们的映射里都开了。）
	if !out.SupportUDP() {
		return nil, fmt.Errorf("mihomo 出站 %s 不支持 UDP: %w", u.node.Name, proxy.ErrUDPUnsupported)
	}
	// 目标按数据报给（SendToUDP），所以这里只建一个"空壳"，真正建通道延后到第一次发送。
	return newMihomoUDPChannel(u, ctx), nil
}

// ------------------------------------------------------------------
// 节点 → mihomo 选项的映射
// ------------------------------------------------------------------

// buildMihomoOutbound 按协议构造 mihomo 出站实例。
//
// dialer 非 nil 时会被装进该协议的 BasicOption.DialerForAPI：出站连服务器那一跳
// 因此走我们的前置（见 mihomo_dialer.go）。直连时传 nil，mihomo 用它自己的默认拨号器。
func buildMihomoOutbound(n config.Node, dialer C.Dialer) (C.ProxyAdapter, error) {
	switch n.Protocol {
	case config.ProtocolVLESS:
		opt, err := vlessOption(n)
		if err != nil {
			return nil, err
		}
		opt.BasicOption.DialerForAPI = dialer
		out, err := outbound.NewVless(opt)
		if err != nil {
			return nil, fmt.Errorf("构造 mihomo vless 出站失败: %w", err)
		}
		return out, nil
	case config.ProtocolVMess:
		opt, err := vmessOption(n)
		if err != nil {
			return nil, err
		}
		opt.BasicOption.DialerForAPI = dialer
		out, err := outbound.NewVmess(opt)
		if err != nil {
			return nil, fmt.Errorf("构造 mihomo vmess 出站失败: %w", err)
		}
		return out, nil
	case config.ProtocolTrojan:
		opt, err := trojanOption(n)
		if err != nil {
			return nil, err
		}
		opt.BasicOption.DialerForAPI = dialer
		out, err := outbound.NewTrojan(opt)
		if err != nil {
			return nil, fmt.Errorf("构造 mihomo trojan 出站失败: %w", err)
		}
		return out, nil
	case config.ProtocolShadowsocks:
		opt, err := shadowsocksOption(n)
		if err != nil {
			return nil, err
		}
		opt.BasicOption.DialerForAPI = dialer
		// 注意拼写：mihomo 是 NewShadowSocks（不是 NewShadowsocks）
		out, err := outbound.NewShadowSocks(opt)
		if err != nil {
			return nil, fmt.Errorf("构造 mihomo shadowsocks 出站失败: %w", err)
		}
		return out, nil
	case config.ProtocolHysteria2:
		opt, err := hysteria2Option(n)
		if err != nil {
			return nil, err
		}
		opt.BasicOption.DialerForAPI = dialer
		out, err := outbound.NewHysteria2(opt)
		if err != nil {
			return nil, fmt.Errorf("构造 mihomo hysteria2 出站失败: %w", err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("mihomo 适配层暂不支持协议 %q", n.Protocol)
	}
}

// mihomoTransports 是 mihomo 认的传输名（与我们 config 里的 network 基本同名，
// 但 h2 在 mihomo 里要写成 http）。
func mihomoTransport(network string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "", "tcp":
		return "", nil // 默认传输，不写字段
	case "ws", "grpc", "httpupgrade", "xhttp":
		return strings.ToLower(strings.TrimSpace(network)), nil
	case "http", "h2":
		return "http", nil
	default:
		return "", fmt.Errorf("mihomo 适配层不认识的传输 %q", network)
	}
}

// vlessOption 把我们的节点映射成 mihomo 的 vless 选项。
//
// 【为什么单独拆成纯函数】映射是最容易错、也最该被锁死的一环：
// 纯函数不需要起网络，表驱动单测就能断言每个字段落对了地方。
func vlessOption(n config.Node) (outbound.VlessOption, error) {
	var opt outbound.VlessOption
	if strings.TrimSpace(n.Server) == "" {
		return opt, fmt.Errorf("节点「%s」没有服务器地址", n.Name)
	}
	if n.Port <= 0 {
		return opt, fmt.Errorf("节点「%s」端口非法：%d", n.Name, n.Port)
	}
	if strings.TrimSpace(n.UUID) == "" {
		return opt, fmt.Errorf("节点「%s」缺少 UUID", n.Name)
	}

	network, err := mihomoTransport(n.Transport.Network)
	if err != nil {
		return opt, err
	}

	path := strings.TrimSpace(n.Transport.Path)
	host := strings.TrimSpace(n.Transport.Host)
	if host == "" {
		host = n.TLS.ServerName
	}

	opt = outbound.VlessOption{
		Name:              n.Name,
		Server:            n.Server,
		Port:              n.Port,
		UUID:              n.UUID,
		Flow:              n.Flow,
		TLS:               n.TLS.Enabled,
		ServerName:        n.TLS.ServerName,
		ClientFingerprint: n.TLS.Fingerprint,
		Network:           network,
		UDP:               true,
		XUDP:              true,
	}

	ours := strings.ToLower(strings.TrimSpace(n.Transport.Network))
	switch ours {
	case "ws":
		opt.WSOpts.Path = path
		if host != "" {
			opt.WSOpts.Headers = map[string]string{"Host": host}
		}
	case "grpc":
		opt.GrpcOpts.GrpcServiceName = strings.TrimSpace(n.Transport.ServiceName)
	case "http":
		opt.HTTPOpts.Path = []string{path}
		if host != "" {
			opt.HTTPOpts.Headers = map[string][]string{"Host": {host}}
		}
	case "h2":
		opt.HTTP2Opts.Path = path
		if host != "" {
			opt.HTTP2Opts.Host = []string{host}
		}
	case "httpupgrade":
		opt.WSOpts.Path = path
		opt.WSOpts.V2rayHttpUpgrade = true
		if host != "" {
			opt.WSOpts.Headers = map[string]string{"Host": host}
		}
	case "xhttp":
		opt.XHTTPOpts.Path = path
		opt.XHTTPOpts.Host = host
	}

	if pk := strings.TrimSpace(n.TLS.RealityPublicKey); pk != "" {
		// 【实测教训】mihomo 的 REALITY 必须显式给 client-fingerprint，
		// 否则报 "REALITY is based on uTLS, please set a client-fingerprint"
		// （sing-box 会自动推断，mihomo 不会）。这里提前拦住，并给出可操作的提示。
		if strings.TrimSpace(opt.ClientFingerprint) == "" {
			return opt, fmt.Errorf("节点「%s」是 REALITY，但没设「指纹」（uTLS client-fingerprint）："+
				"mihomo 必须显式指定（一般填 chrome）。请在节点的高级 TLS 设置里补上", n.Name)
		}
		opt.RealityOpts = outbound.RealityOptions{
			PublicKey: pk,
			ShortID:   strings.TrimSpace(n.TLS.RealityShortID),
		}
	}

	return opt, nil
}

// ------------------------------------------------------------------
// 其余四种协议的映射
//
// 与 vlessOption 同一套路：**纯函数**，表驱动单测锁死字段落位。
// 传输放行范围按各自 mihomo 结构体**实际有**的选项字段来（不是所有协议都有全部传输），
// 其余组合一律返回明确错误，不静默忽略。
// ------------------------------------------------------------------

// vmessOption 映射 VMess。
//
// 字段对照：我们的「加密方式」叫 Security，mihomo 里叫 **Cipher**（读源码确认）。
func vmessOption(n config.Node) (outbound.VmessOption, error) {
	var opt outbound.VmessOption
	if strings.TrimSpace(n.Server) == "" || n.Port <= 0 || strings.TrimSpace(n.UUID) == "" {
		return opt, fmt.Errorf("节点「%s」缺少服务器地址/端口/UUID", n.Name)
	}
	ours := strings.ToLower(strings.TrimSpace(n.Transport.Network))
	if ours == "xhttp" {
		return opt, fmt.Errorf("mihomo 的 vmess 不支持 xhttp 传输（只有 vless 有），节点「%s」", n.Name)
	}
	network, err := mihomoTransport(n.Transport.Network)
	if err != nil {
		return opt, err
	}
	opt = outbound.VmessOption{
		Name:       n.Name,
		Server:     n.Server,
		Port:       n.Port,
		UUID:       n.UUID,
		AlterID:    n.AlterID,
		Cipher:     n.Security,
		UDP:        true,
		Network:    network,
		TLS:        n.TLS.Enabled,
		ServerName: n.TLS.ServerName,
	}
	path := strings.TrimSpace(n.Transport.Path)
	host := strings.TrimSpace(n.Transport.Host)
	if host == "" {
		host = n.TLS.ServerName
	}
	switch strings.ToLower(strings.TrimSpace(n.Transport.Network)) {
	case "ws":
		opt.WSOpts.Path = path
		if host != "" {
			opt.WSOpts.Headers = map[string]string{"Host": host}
		}
	case "grpc":
		opt.GrpcOpts.GrpcServiceName = strings.TrimSpace(n.Transport.ServiceName)
	case "http":
		if path != "" {
			opt.HTTPOpts.Path = []string{path}
		}
		if host != "" {
			opt.HTTPOpts.Headers = map[string][]string{"Host": {host}}
		}
	case "h2":
		opt.HTTP2Opts.Path = path
		if host != "" {
			opt.HTTP2Opts.Host = []string{host}
		}
	case "httpupgrade":
		// mihomo 的 httpupgrade 复用 WSOpts（见 vless 那边的源码证据）
		opt.WSOpts.Path = path
		opt.WSOpts.V2rayHttpUpgrade = true
		if host != "" {
			opt.WSOpts.Headers = map[string]string{"Host": host}
		}
	case "xhttp":
		// 【编译器确认】mihomo 的 VmessOption 没有 XHTTPOpts —— xhttp 只有 vless 有。
		// 所以这里明确报错，而不是静默忽略传输（否则用户会得到"连不上但看不出原因"）。
		return opt, fmt.Errorf("mihomo 的 vmess 不支持 xhttp 传输（该传输只有 vless 有），节点「%s」请改用 vless+xhttp 或其它传输", n.Name)
	}
	return opt, nil
}

// trojanOption 映射 Trojan。
//
// 两点与其它协议不同：TLS 是协议自带；**SNI 的字段名就叫 SNI**（不是 ServerName）。
// 它的结构体只有 GrpcOpts / WSOpts 两种传输，别的组合明确报错。
func trojanOption(n config.Node) (outbound.TrojanOption, error) {
	var opt outbound.TrojanOption
	if strings.TrimSpace(n.Server) == "" || n.Port <= 0 || strings.TrimSpace(n.Password) == "" {
		return opt, fmt.Errorf("节点「%s」缺少服务器地址/端口/密码", n.Name)
	}
	network, err := mihomoTransport(n.Transport.Network)
	if err != nil {
		return opt, err
	}
	opt = outbound.TrojanOption{
		Name:              n.Name,
		Server:            n.Server,
		Port:              n.Port,
		Password:          n.Password,
		SNI:               n.TLS.ServerName,
		ClientFingerprint: n.TLS.Fingerprint,
		UDP:               true,
		Network:           network,
	}
	path := strings.TrimSpace(n.Transport.Path)
	host := strings.TrimSpace(n.Transport.Host)
	if host == "" {
		host = n.TLS.ServerName
	}
	switch strings.ToLower(strings.TrimSpace(n.Transport.Network)) {
	case "", "tcp":
	case "ws":
		opt.WSOpts.Path = path
		if host != "" {
			opt.WSOpts.Headers = map[string]string{"Host": host}
		}
	case "grpc":
		opt.GrpcOpts.GrpcServiceName = strings.TrimSpace(n.Transport.ServiceName)
	default:
		return opt, fmt.Errorf("mihomo trojan 目前只接了 ws/grpc 传输，收到 %q（其结构体只有这两个选项）", network)
	}
	return opt, nil
}

// shadowsocksOption 映射 Shadowsocks（无传输层；我们的加密方式字段叫 Method）。
func shadowsocksOption(n config.Node) (outbound.ShadowSocksOption, error) {
	var opt outbound.ShadowSocksOption
	if strings.TrimSpace(n.Server) == "" || n.Port <= 0 {
		return opt, fmt.Errorf("节点「%s」缺少服务器地址/端口", n.Name)
	}
	if strings.TrimSpace(n.Method) == "" {
		return opt, fmt.Errorf("节点「%s」缺少加密方式（method）", n.Name)
	}
	return outbound.ShadowSocksOption{
		Name:     n.Name,
		Server:   n.Server,
		Port:     n.Port,
		Password: n.Password,
		Cipher:   n.Method,
		UDP:      true,
	}, nil
}

// hysteria2Option 映射 Hysteria2。
//
// 注意 mihomo 的 Up/Down 是**带单位的字符串**（如 "100 Mbps"），
// 我们配置里是整数 Mbps → 这里格式化；为 0 时不写（让它用默认）。
func hysteria2Option(n config.Node) (outbound.Hysteria2Option, error) {
	var opt outbound.Hysteria2Option
	if strings.TrimSpace(n.Server) == "" || n.Port <= 0 {
		return opt, fmt.Errorf("节点「%s」缺少服务器地址/端口", n.Name)
	}
	opt = outbound.Hysteria2Option{
		Name:         n.Name,
		Server:       n.Server,
		Port:         n.Port,
		Password:     n.Password,
		SNI:          n.TLS.ServerName,
		Obfs:         n.Obfs,
		ObfsPassword: n.ObfsPassword,
	}
	if n.UpMbps > 0 {
		opt.Up = fmt.Sprintf("%d Mbps", n.UpMbps)
	}
	if n.DownMbps > 0 {
		opt.Down = fmt.Sprintf("%d Mbps", n.DownMbps)
	}
	return opt, nil
}
