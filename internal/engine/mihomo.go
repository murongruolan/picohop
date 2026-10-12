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
}

// NewMihomo 构造一个走 mihomo 出站层的出口。
//
// 真正的出站实例在 Start 里创建（与 sing-box 侧一致：Start 才"拉起实例"）。
func NewMihomo(n config.Node, front config.Front, logf func(string, ...any)) (proxy.Upstream, error) {
	if !mihomoSupports(n.Protocol) {
		return nil, fmt.Errorf("mihomo 适配层暂不支持协议 %q（本步只接了 vless；其余协议按迁移契约第 ④ 步逐条扩）", n.Protocol)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &MihomoUpstream{node: n, front: front, logf: logf}, nil
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
	out, err := buildMihomoOutbound(u.node)
	if err != nil {
		return err
	}
	u.mu.Lock()
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
	return nil, fmt.Errorf("mihomo 适配层：UDP 通道尚未接入（迁移契约第 ④ 步；当前只接了 TCP）")
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
