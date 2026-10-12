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
	"errors"
	"fmt"
	"net"
	"regexp"
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

	// mu 保护 inst/ob 这对"实例 + 节点出口"的启停状态（审查 #26）。
	//
	// 必须是读写锁而不是普通互斥锁：拨号时要**在整个建链期间**持读锁
	// （理由见 Dial），用普通锁会让同一节点上的所有连接互相排队 ——
	// 一次拨号等到 DialTimeout，后面所有请求都卡住。
	mu   sync.RWMutex
	inst *box.Box
	ob   adapter.Outbound
}

// errNotStarted 表示出口还没启动，或刚被 Stop 清掉。
//
// 它必须是"取用时读到 nil 就报错"：绝不允许在出口已经失效之后还把它交出去用
// （见 Dial 的说明，审查 #26）。
var errNotStarted = errors.New("sing-box 出口尚未启动")

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
	// 【凭据不入日志】配置里有 uuid/密码/reality 公钥等，直接打印等于把凭据抄到日志里
	// （任何本机进程都能读；用户贴日志求助就是二次泄露）。
	// 但整份配置对排查很有用（例如那次 xhttp 报错就是靠它定位的），所以**脱敏后照打**。
	u.logf("[engine] 「%s」sing-box 配置（已脱敏）: %s", u.node.Name, redactConfigJSON(string(raw)))

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
//
// 【为什么先在写锁里清空、再在锁外 Close（审查 #26）】
// 旧实现是"取出出口指针 → 解锁 → 用它拨号"，而 Stop 在这之间把 inst/ob 清空并
// Close 掉整个实例 —— 两边没有任何同步，于是拨号可能在一个**正在被拆掉的实例**上
// 进行（use-after-close）。这里把"失效"放进写锁：写锁必须等到所有在途拨号
// （它们持读锁，见 Dial）收尾之后才拿得到，所以 Close 绝不可能与使用重叠；
// 清空之后再进来的拨号只会读到 nil，拿到明确的"尚未启动"错误。
//
// Close 放在解锁之后（而不是锁内）：它会回调日志，而上层拿到日志后可能去拿自己的锁，
// 在自己的锁里调 Stop 的路径又确实存在过（审查 #4）—— 在锁内调用未知代码是死锁的温床，
// 而这里不需要它：清空字段已经在锁内完成，新拨号已经不可能再拿到旧出口。
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
//
// 【为什么读锁要一直持到建链结束（审查 #26）】
// 旧的 outbound() 只负责"在锁内取出 u.ob 指针"，指针一离开锁，Stop 就能把实例关掉：
// 拨号与关闭之间是 use-after-close 的赛跑。这不是理论窗口 ——
// 每个客户端连接都在自己的 goroutine 里拨号（handler.socksConnect），
// 而"停止/重启节点"随时可能被点（stopNode / restartNode），
// 于是节点启停瞬间就会出现偶发失败，甚至在带内部状态的出口
// （hysteria2/QUIC、多路复用的 ws/grpc）上踩到已释放资源。
//
// 所以取用与使用必须在同一把读锁里完成：要么拨号先拿到读锁（Stop 的写锁必须等它收尾，
// 实例在拨号期间不会被关），要么 Stop 先清空（这里读到 nil，明确报错）。
// **不要把 u.ob 带出这把锁再用** —— 那正是本缺陷的成因。
func (u *Upstream) Dial(ctx context.Context, _ proxy.Front, target string) (net.Conn, error) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if u.ob == nil {
		return nil, errNotStarted
	}
	conn, err := u.ob.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(target))
	if err != nil {
		return nil, fmt.Errorf("%s 连接 %s 失败: %w", config.ProtocolLabel(u.node.Protocol), target, err)
	}
	return conn, nil
}

// DialUDP 建立 UDP 通道。
//
// 与 Dial 同理（审查 #26）：取出口 + 打开通道都在读锁内完成，
// 不允许出现"指针取出来之后实例才被关掉"的窗口。
//
// 这里只保护"打开通道"这一步。通道建成之后，Stop 关实例会不会连带关掉它，
// 取决于 sing-box 自己怎么收尾（TCP 连接同理）—— 那不属于本次修复的范围。
func (u *Upstream) DialUDP(ctx context.Context, _ proxy.Front) (proxy.UDPChannel, error) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if u.ob == nil {
		return nil, errNotStarted
	}
	pc, err := u.ob.ListenPacket(ctx, M.Socksaddr{})
	if err != nil {
		return nil, fmt.Errorf("%s 打开 UDP 通道失败: %w", config.ProtocolLabel(u.node.Protocol), err)
	}
	return newUDPChannel(pc)
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
	fo, err := buildFrontOutbound(front)
	if err != nil {
		// 前置不可用就直接失败（见 buildFrontOutbound 的说明）：
		// 配置生成不出来 → Start 失败 → 这个节点不会被启动，也就不会偷偷直连。
		return nil, err
	}
	if fo != nil {
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
// 返回 (nil, nil) 表示"直连"：不需要前置出口、也不需要 detour。
// 但**只有显式选了直连才算**；前置写了却没写全（地址空、端口非法）时返回错误。
//
// 【为什么非法前置必须报错，而不是当成直连（审查 #10）】
// 这里原本是 `host == "" || front.Port <= 0 → return nil`，也就是"前置写坏了就静默直连"。
// 后果很严重：这个节点会**用本机 IP 直接去连节点服务器**。而前置存在的意义恰恰是
// 不让本机 IP 露面（住宅代理、主流客户端、公司出口……），用户以为流量已经过前置，
// 实际上出口 IP 已经暴露出去了 —— 这类"链路能通、但身份暴露"的故障用户根本看不出来。
// 更糟的是它**完全静默**：日志里只有一行"已就绪"，没有任何异常可循。
// 而且它是可达的：startNode/Activate 一路都不跑 config.Validate（只有命令行 run 跑），
// 界面侧的端口/地址校验只挡自己那条输入路径，手工编辑（或被别的工具改写）的
// data/config.json —— 例如 host 是空白字符、port 为负数、作为前置的那个节点
// listenPort 非法 —— 都能一路走到这里。
//
// 所以按项目"宁可节点不启动，也不能静默从本机 IP 出去"的诉求改成 fail-closed：
// 明确报错、把"为什么"和"怎么办"写清楚，由上层把这行错误挂到该节点上。
// 别再改回"返回 nil 当直连"。
func buildFrontOutbound(front config.Front) (map[string]any, error) {
	// 只有显式的 direct 才是直连。空 type 在别处一律按 socks5 处理
	// （见 proxy.NewFront 的 default 分支与 config.Front.Describe），
	// 所以它同样需要合法地址，不能当成"没配前置"。
	if front.Type == config.FrontTypeDirect {
		return nil, nil
	}

	host := strings.TrimSpace(front.Host)
	if host == "" {
		return nil, fmt.Errorf("前置代理没有填写地址，拒绝退化成「直连」——" +
			"那会让本机 IP 直接暴露给节点服务器。请在设置里填好前置地址，或把该节点的前置显式改成「直连」")
	}
	if front.Port < 1 || front.Port > 65535 {
		return nil, fmt.Errorf("前置代理端口 %d 非法（应在 1-65535 之间），拒绝退化成「直连」——"+
			"那会让本机 IP 直接暴露给节点服务器。请填好前置端口，或把该节点的前置显式改成「直连」", front.Port)
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
	return base, nil
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
		if err := applyTransport(m, n); err != nil {
			return nil, err
		}
		applyTLS(m, n)

	case config.ProtocolVLESS:
		m["type"] = "vless"
		m["uuid"] = n.UUID
		if strings.TrimSpace(n.Flow) != "" {
			m["flow"] = strings.TrimSpace(n.Flow)
		}
		if err := applyTransport(m, n); err != nil {
			return nil, err
		}
		applyTLS(m, n)

	case config.ProtocolShadowsocks:
		m["type"] = "shadowsocks"
		m["method"] = n.Method
		m["password"] = n.Password

	case config.ProtocolTrojan:
		m["type"] = "trojan"
		m["password"] = n.Password
		if err := applyTransport(m, n); err != nil {
			return nil, err
		}
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
//
// 返回 error 的那些情况是**内核做不到**（不是配置写错了）：讲清楚"跑不起来、可以换什么"，
// 比把原样透传给 sing-box、让它报一句难懂的英文有用得多。
func applyTransport(m map[string]any, n config.Node) error {
	network := strings.ToLower(strings.TrimSpace(n.Transport.Network))
	if network == "" {
		network = "tcp"
	}
	switch network {
	case "tcp":
		// http 伪装头（主流客户端的 tcp+http header）sing-box 不支持，忽略。
		return nil
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
	case "xhttp", "splithttp":
		// 【实测】sing-box v1.14.3 没有 xhttp 传输（它的传输只有 http/ws/quic/grpc/httpupgrade）。
		// 用户拿一个 xhttp 节点来测，原来会得到 sing-box 那句
		// "outbounds[0].transport: unknown transport type: xhttp" —— 既没说是谁的问题，
		// 也没说该怎么办。这里直接把话说清楚。
		return fmt.Errorf("本程序内嵌的 sing-box 内核不支持 xhttp 传输（Xray 的较新传输，前身 SplitHTTP）——" +
			"这个节点在当前内核下跑不起来。" +
			"可选：① 用主流客户端（Xray 内核）跑它，再把本程序的「前置」指向主流客户端的 SOCKS5 口；" +
			"② 或在服务端改用 ws / grpc / h2 / httpupgrade 传输后重新导入")
	default:
		// 其它不认识的类型：原样透传，让 sing-box 自己报错 ——
		// 至少那是"内核真的不认识"，我们也不该替它猜。
		m["transport"] = map[string]any{"type": network}
	}
	return nil
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

// redactConfigJSON 把配置 JSON 里敏感字段的**值**替换成 ***，键与结构保留。
//
// 为什么要脱敏而不是干脆不打印：整份配置是排查内核问题最有力的证据
// （第 1 轮那次 "unknown transport type: xhttp" 就是靠它一眼定位的）。
// 敏感值一旦进了日志，就等于凭据长期驻留在纯文本文件里。
func redactConfigJSON(raw string) string {
	re := regexp.MustCompile(`(?i)"(uuid|password|public_key|short_id|private_key|obfs-password|psk|preshared_key|token|secret)"\s*:\s*"[^"]*"`)
	return re.ReplaceAllString(raw, `"$1":"***"`)
}
