// Package app 是 ChainProxy 的应用状态机：
// 持有配置、管理所有节点服务的生命周期、维护今日统计、对外广播事件。
//
// 它是界面层（desktop/）唯一需要打交道的对象 —— 界面不直接碰 proxy 包。
package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"chainproxy/internal/config"
	"chainproxy/internal/engine"
	"chainproxy/internal/proxy"
)

// 节点运行状态。
const (
	StateRunning = "running"
	StateStopped = "stopped"
	StateError   = "error"
)

// 事件类型（广播给界面）。
const (
	EventLog      = "log"      // 一行日志
	EventNodes    = "nodes"    // 节点列表/状态变化，界面应重新拉取
	EventStats    = "stats"    // 流量统计刷新（高频，1 秒一次）
	EventNotify   = "notify"   // 通用通知（右上角弹出）
	EventTestDone = "testDone" // 某项测试完成
)

// Event 是应用向外广播的事件。
type Event struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// App 是整个应用的运行体。
type App struct {
	mu      sync.RWMutex
	cfg     *config.Config
	cfgPath string

	// 每个节点一个监听服务，key 是节点 ID。
	servers map[string]*proxy.Server

	// 最近一次启动失败的原因（按节点 ID）。
	errs map[string]string

	// 各节点最近一次探测到的出口 IP（按节点 ID，不落盘）。
	exitIPs map[string]string

	logger  *log.Logger
	logFile *os.File
	logs    *ringBuffer

	// 统计基线：服务内部的计数是"本次运行累计"，
	// 用它的增量去累加"今日"统计，停掉再启动才不会重复计。
	baseUp   map[string]int64
	baseDown map[string]int64

	// 最近一次每秒采样到的总速率（字节/秒）。
	// 用原子量是为了让 Summary() 这种"不加锁的读取"也能安全拿到最新值 ——
	// tickStats 每秒写一次，界面随时可能读。
	upRate   atomic.Int64
	downRate atomic.Int64

	// 连接表：活动连接与最近关闭的连接（"连接"视图用）。
	// 与网络日志一样单独一把锁 —— 它们都由转发的 goroutine 高频触发。
	connMu      sync.Mutex
	conns       map[uint64]ConnRecord
	closedConns []ConnRecord

	// 网络日志（内嵌 sing-box 的连接流水）。
	// 单独一把锁：它由内核的日志回调高频写入，不该和主锁互相等。
	netLogMu  sync.Mutex
	netLog    []NetLogEntry
	netLogSeq int64

	// trafficByNode 是**按节点**的每秒速率历史（受 mu 保护）。
	//
	// 为什么要有它：流量统计的图可以切"看哪个节点"，只有一条总量曲线时那个下拉没有数据可画。
	// 内存代价：1 秒一个样本、每样本 24 字节、保留 1 小时 → 每节点约 86KB。
	trafficByNode map[string][]TrafficSample

	// traffic 是每秒速率的历史环形缓冲（受 mu 保护），给"流量统计"画图用。
	// 由 Socialticks 写入，见 TrafficHistory。
	traffic []TrafficSample

	dirty   bool // 统计改动是否需要落盘
	lastDay string

	subsMu sync.Mutex
	subs   []subscriber
	subSeq uint64

	closing  chan struct{}
	closeOne sync.Once

	// 测试任务的取消函数（同一时刻只允许一批测试在跑）。
	testMu      sync.Mutex
	testCancel  context.CancelFunc
	testRunning bool
}

// New 创建应用实例；cfg 为 nil 时从磁盘载入。
func New(cfgPath string) (*App, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	// 【引擎选择】必须在任何节点被创建之前生效（New 之后马上会有人调 AutoActivate）。
	// 配置里没写就是空串 -> sing-box（与换装前行为一致）。
	proxy.SetEngineMode(cfg.Settings.EngineMode)
	a := &App{
		cfg:      cfg,
		cfgPath:  cfgPath,
		servers:  map[string]*proxy.Server{},
		errs:     map[string]string{},
		baseUp:   map[string]int64{},
		baseDown: map[string]int64{},
		// 预分配：1 秒一个样本，够 1 小时
		traffic:       make([]TrafficSample, 0, trafficHistorySeconds),
		trafficByNode: make(map[string][]TrafficSample),
		netLog:        make([]NetLogEntry, 0, netLogCap),
		conns:         make(map[uint64]ConnRecord),
		logs:          newRingBuffer(1000),
		closing:       make(chan struct{}),
		lastDay:       config.Today(),
	}

	// 【安全】先把数据目录收紧到"当前用户 + Administrators + SYSTEM"再往里写东西。
	//
	// 为什么卡在这个位置：data\ 里是**全套凭据**（config.json），而它默认建在仓库
	// 目录下、继承盘符根目录那条 `Authenticated Users:(M)` —— 同机任何一个本地用户
	// 都能读改。这里必须早于日志文件被创建：晚一步，chainproxy.log（里面有本机出口
	// IP 与节点别名）就会带着宽松的继承权限落盘。详见 config.EnsureDataDir。
	//
	// 失败不阻断启动（数据目录可能被指到 FAT/网络盘上，那里没有 NTFS ACL），
	// 但**绝不能静默**：stderr 与日志各留一条警告。
	hardenErr := config.EnsureDataDir()
	if hardenErr != nil {
		fmt.Fprintf(os.Stderr, "警告：数据目录权限没能收紧（%v），其他本地用户可能读到里面的凭据\n", hardenErr)
	}

	if f, err := os.OpenFile(config.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		a.logFile = f
		a.logger = log.New(f, "", log.LstdFlags)
	} else {
		a.logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	if hardenErr != nil {
		a.logger.Printf("警告：数据目录权限没能收紧（%v）：其他本地用户可能读到 config.json 里的凭据", hardenErr)
	}

	a.rollDayLocked()
	go a.statsLoop()
	// 把"连接流水"接进网络日志缓冲。
	//
	// 两个来源：
	//   1. 我们自己的代理 handler —— **主要来源**，它才知道"谁连了哪儿"；
	//   2. 内嵌 sing-box 的日志 —— 补充（例如握手失败、配置告警）。
	//
	// 都放在 app 这边注册，因为缓冲归 app 所有；引擎与 handler 只提供进程级接收器。
	proxy.SetConnSink(a.trackConnEvent)
	engine.SetNetworkLogSink(a.appendNetworkLog)

	return a, nil
}

// Close 停止所有服务并释放资源。
func (a *App) Close() {
	a.closeOne.Do(func() { close(a.closing) })
	a.StopAll()
	a.persistStats(true)
	if a.logFile != nil {
		_ = a.logFile.Close()
	}
}

// ------------------------------------------------------------------
// 事件
// ------------------------------------------------------------------

type subscriber struct {
	id uint64
	fn func(Event)
}

// Subscribe 注册事件回调，返回取消函数。
//
// 【约定】回调必须**快速返回**，并且不允许再同步调用 App 的方法
// （事件可能在持有 App 内部锁时发出）。界面层只做"转发到前端"这件事。
func (a *App) Subscribe(fn func(Event)) func() {
	a.subsMu.Lock()
	a.subSeq++
	s := subscriber{id: a.subSeq, fn: fn}
	a.subs = append(a.subs, s)
	a.subsMu.Unlock()
	return func() {
		a.subsMu.Lock()
		for i, x := range a.subs {
			if x.id == s.id {
				a.subs = append(a.subs[:i], a.subs[i+1:]...)
				break
			}
		}
		a.subsMu.Unlock()
	}
}

func (a *App) emit(kind, text string) {
	a.subsMu.Lock()
	subs := make([]subscriber, len(a.subs))
	copy(subs, a.subs)
	a.subsMu.Unlock()
	for _, s := range subs {
		s.fn(Event{Kind: kind, Text: text})
	}
}

// Notify 给界面推一条通知。
//
// level 取 element-plus 的语义：primary / success / warning / info / error。
func (a *App) Notify(level, title, text string) {
	a.emit(EventNotify, encodeNotify(level, title, text))
}

// Logf 写日志并广播到界面。
func (a *App) Logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if a.logger != nil {
		a.logger.Println(line)
	}
	a.logs.add(line)
	a.emit(EventLog, line)
}

// Logs 返回界面日志面板要显示的最近若干行。
func (a *App) Logs() []string { return a.logs.snapshot() }

// ------------------------------------------------------------------
// 配置访问
// ------------------------------------------------------------------

// ConfigPath 返回配置文件路径。
func (a *App) ConfigPath() string { return a.cfgPath }

// Save 把当前配置落盘。
func (a *App) Save() error {
	// 【锁内序列化，锁外落盘】
	// 原先这里是 `cfg := a.cfg`（只是复制指针）然后就放锁、跑到锁外 json.Marshal ——
	// 而每秒的统计协程持写锁改 UpBytes/DownBytes，两者并发读写同一份数据。
	// 现在：持 RLock 只做便宜的 cpu 序列化，磁盘 I/O 放到锁外做（不长时间占锁）。
	a.mu.RLock()
	data, err := a.cfg.Encode()
	path := a.cfgPath
	a.mu.RUnlock()
	if err != nil {
		return err
	}
	return config.WriteAtomic(path, data)
}

// Validate 返回当前配置的全部问题。
//
// 【这是读路径：持 RLock 就够了，但也要求 config.Validate **一行都不许写配置**】
// 以前 config.Validate 会在锁内就地补节点 ID / 协议 / 监听地址 —— 而 RLock 允许
// 多个读者同时进入，于是"点一下校验"会和并发的读者（节点列表、每秒统计、
// Save 时的序列化）对同一份 Nodes 一读一写，是实实在在的 data race。
// 现在补默认值全部挪到了显式写入路径（config.Load 的 normalize()、
// AddNode / UpdateNode 里的 NormalizeNode），config.Validate 只读，这里才敢用 RLock。
// 【后人注意】谁要是再往 Validate 里加回写语句，必须同时把这里换成 a.mu.Lock()；
// 更正确的做法是继续留在写入路径里补。
func (a *App) Validate() []config.Issue {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.Validate()
}

// Front 返回全局前置代理配置副本。
func (a *App) Front() config.Front {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.Front
}

// Settings 返回全局设置副本。
func (a *App) Settings() config.Settings {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.Settings
}

// SetFront 更新全局前置代理。
//
// 前置变了，所有"使用全局前置"的节点都必须重启才能生效，
// 这里直接全部重启（已在运行的节点重启后仍然是运行状态）。
func (a *App) SetFront(f config.Front) error {
	a.mu.Lock()
	a.cfg.Front = f
	a.mu.Unlock()
	a.Logf("全局前置代理已更新：%s", f.Describe())
	if err := a.Save(); err != nil {
		return err
	}
	a.RestartAll()
	a.emit(EventNodes, "")
	return nil
}

// SetSettings 更新全局设置。
//
// 监听地址（AllowLan）与超时都可能影响正在运行的服务，所以同样整体重启。
func (a *App) SetSettings(s config.Settings) error {
	a.mu.Lock()
	a.cfg.Settings = s
	a.mu.Unlock()
	if err := a.Save(); err != nil {
		return err
	}
	a.RestartAll()
	a.emit(EventNodes, "")
	return nil
}

// ------------------------------------------------------------------
// 内部工具
// ------------------------------------------------------------------

// indexOfLocked 返回节点下标（调用方需持锁）。
func (a *App) indexOfLocked(id string) int { return a.cfg.Index(id) }

// serverOf 返回正在运行的节点服务。
func (a *App) serverOf(id string) *proxy.Server {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.servers[id]
}

// describeFront 返回某节点前置的可读描述。
func (a *App) describeFrontLocked(n config.Node) string {
	switch n.FrontKind() {
	case config.FrontDirect:
		return "直连"
	case config.FrontGlobal:
		return "全局前置 · " + a.cfg.Front.Describe()
	default:
		if idx := a.cfg.Index(n.FrontNodeID()); idx >= 0 {
			p := a.cfg.Nodes[idx]
			return fmt.Sprintf("节点「%s」· SOCKS5 127.0.0.1:%d", p.Name, p.ListenPort)
		}
		return "前置节点缺失"
	}
}

// sleepCtx 是可被关闭信号打断的等待。
func (a *App) sleepCtx(d time.Duration) bool {
	select {
	case <-a.closing:
		return false
	case <-time.After(d):
		return true
	}
}

// trimAll 去掉所有空白（用于解析用户输入时容错）。
func trimAll(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// ------------------------------------------------------------------
// 环形日志缓冲
// ------------------------------------------------------------------

type ringBuffer struct {
	mu   sync.Mutex
	data []string
	next int
	full bool
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{data: make([]string, capacity)}
}

func (r *ringBuffer) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[r.next] = line
	r.next = (r.next + 1) % len(r.data)
	if r.next == 0 {
		r.full = true
	}
}

func (r *ringBuffer) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		out := make([]string, r.next)
		copy(out, r.data[:r.next])
		return out
	}
	out := make([]string, 0, len(r.data))
	out = append(out, r.data[r.next:]...)
	out = append(out, r.data[:r.next]...)
	return out
}

// encodeNotify 把通知编码成一段文本传给前端。
//
// 用 NUL 分隔是为了让前端一次 split 就能拿到三段，
// 避免为了一个通知定义一整个结构体再序列化。
func encodeNotify(level, title, text string) string {
	if level == "" {
		level = "info"
	}
	return level + "\x00" + title + "\x00" + text
}
