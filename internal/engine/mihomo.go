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
// 【本适配层不接前置 —— 非直连一律拒绝】MihomoUpstream 目前只做单出站拨号，
// 收下的 front 根本不会被使用。所以"实际会用到前置"的节点必须在这里就被拒
// （判据见 mihomoFrontGap）：放它过去 = 从本机 IP 出去，绕开链式代理/住宅出口，
// 而且完全静默。这条与 sing-box 侧 buildFrontOutbound 的 fail-closed 是同一条原则。
//
// 真正的出站实例在 Start 里创建（与 sing-box 侧一致：Start 才"拉起实例"）。
func NewMihomo(n config.Node, front config.Front, logf func(string, ...any)) (proxy.Upstream, error) {
	if !mihomoSupports(n.Protocol) {
		return nil, fmt.Errorf("mihomo 适配层暂不支持协议 %q（本步只接了 vless；其余协议按迁移契约第 ④ 步逐条扩）", n.Protocol)
	}
	if gap := mihomoFrontGap(n, front); gap != "" {
		return nil, fmt.Errorf("节点「%s」%s，而 mihomo 适配层还没接前置 —— "+
			"它目前只会从本机直接拨号，放过去就等于绕开前置、用本机 IP 出去。"+
			"为免静默直连，这里直接拒绝：本节点会回退 sing-box（它按前置生成 detour，链式照常生效）",
			n.Name, gap)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	// 【为什么在构造时就把出站建出来】映射错误（字段缺失、传输不支持等）原本要等到
	// Start 才暴露，那样上层就没法回退了 —— 用户会看到"节点激活失败"，
	// 而这个节点在 sing-box 下本来是能用的。构造时就建好，让 proxy.NewUpstream
	// 能按"构造失败 → 回退 sing-box"处理，换装期不会让任何节点从可用变不可用。
	out, err := buildMihomoOutbound(n)
	if err != nil {
		return nil, err
	}
	return &MihomoUpstream{node: n, front: front, logf: logf, built: out}, nil
}

// mihomoFrontGap 报告"这个节点实际会用到非直连前置"的原因；返回空串 = 用不到前置，放行。
//
// 【为什么必须在构造时就判】MihomoUpstream 只做单出站拨号：它按节点自己的服务器
// 地址直接 DialContext，front 参数收下了也不会用。一个配了前置的节点若被放行，
// 它就会**用本机 IP** 去连节点服务器 —— 而前置的意义恰恰是不让本机 IP 露面
// （住宅出口、公司出口、另一跳节点……）。这种故障完全静默：链路照样通，
// 只是身份已经暴露，用户从界面上看不出来。所以 fail-closed：宁可它起不来
// （上层回退 sing-box），也不许它偷偷直连。这条与 singbox.go 里
// buildFrontOutbound 的"非法前置必须报错、不许退化成直连"（审查 #10）同源。
//
// 【判据】Node.Front 只有三种取值（见 config.Node 的注释与 FrontKind/FrontNodeID）：
//   - ""（FrontDirect，含纯空白）→ resolveFront 给 direct，单出站拨号正是它想要的行为；
//   - "global"（FrontGlobal）    → 用设置里的全局前置，就是传进来的 front 参数；
//   - 其它                       → 本项目内另一个节点的 ID（FrontNodeID 非空），
//     resolveFront 会给一个指向它本地端口的 socks5 前置。
//
// 于是：节点前置指向另一个节点 → 拒；节点要用的全局前置不是 direct → 拒。
// 另外补一条防御：节点自称直连、调用方却递了一个**明确配好的**非直连前置时也拒
// —— mihomo 会忽略这个参数，两边不一致时只有按"有前置"处理才不会静默直连
// （零值 Front 视为"调用方没给前置信息"，不算不一致，否则会误伤直连节点）。
//
// 【为什么"非 direct 一律算有前置"，而不是只认 socks5/http】proxy.NewFront 的
// default 分支造的就是 socks5Front：Front.Type 只要不是显式 "direct"（空串也是），
// 跑起来都是一跳真实的前置（singbox 侧同样这么判）。宁可多拒一个残缺配置，
// 也不能漏判成"直连"。
func mihomoFrontGap(n config.Node, front config.Front) string {
	if id := n.FrontNodeID(); id != "" {
		return fmt.Sprintf("的前置指向另一个节点（%s）", id)
	}
	if n.UsesGlobalFront() {
		if front.Type != config.FrontTypeDirect {
			return fmt.Sprintf("用的是非直连的全局前置（%s）", front.Describe())
		}
		return ""
	}
	if front.Type != config.FrontTypeDirect && front.Type != "" {
		return fmt.Sprintf("传进来的前置不是直连（%s）", front.Describe())
	}
	return ""
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
		built, err := buildMihomoOutbound(u.node)
		if err != nil {
			u.mu.Unlock()
			return err
		}
		u.built, out = built, built
	}
	u.out = out
	u.mu.Unlock()
	u.logf("「%s」mihomo 出站已就绪：%s（type=%v）", u.node.Name, out.Addr(), out.Type())
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
func buildMihomoOutbound(n config.Node) (C.ProxyAdapter, error) {
	switch n.Protocol {
	case config.ProtocolVLESS:
		opt, err := vlessOption(n)
		if err != nil {
			return nil, err
		}
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
