package app

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/murongruolan/picohop/internal/config"
)

// NodeView 是界面列表里的一行 —— 把配置、运行状态、统计合成一份扁平数据。
//
// 刻意让界面拿到的是"已经算好、已经格式化"的东西：
//   - 类型/传输协议/前置都给了显示用的文本，前端不需要知道协议细节
//   - 今日流量给了字节数和格式化文本，前端直接显示
//
// 这样协议相关的知识只存在于后端一处。
type NodeView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	ProtocolLabel string `json:"protocolLabel"`
	Transport     string `json:"transport"`
	Server        string `json:"server"`
	Port          int    `json:"port"`
	ListenHost    string `json:"listenHost"`
	ListenPort    int    `json:"listenPort"`
	// ListenAuth 说明这个节点是否**配齐**了本地端口鉴权（用户名 + 密码）。
	//
	// 【为什么界面需要它】打开「允许局域网」后，没配齐鉴权的节点会被
	// proxy.NewServer 拒绝启动（审查 #22）。设置页要能提前把"哪几个节点
	// 会被拦下"点名说出来，而不是等用户一个个点激活再看报错。
	// 判据与后端完全同一处：config.Node.HasListenAuth。
	ListenAuth bool   `json:"listenAuth"`
	Front      string `json:"front"`
	FrontLabel string `json:"frontLabel"`
	Remark     string `json:"remark"`
	TLSEnabled bool   `json:"tlsEnabled"`
	Active     bool   `json:"active"`

	State      string `json:"state"` // running / stopped / error
	StateLabel string `json:"stateLabel"`
	Detail     string `json:"detail"`

	LatencyMs int64 `json:"latencyMs"`
	// LatencyKind 见 config.Node，界面靠它决定「延迟」列显示哪个值
	LatencyKind string  `json:"latencyKind"`
	RealMs      int64   `json:"realMs"`
	SpeedMBps   float64 `json:"speedMbps"`
	// LatencyAt / LatencyFailed 说明「延迟」列里那个值是**什么时候**测出来的、
	// 以及最近一次延迟测试有没有测出数值；SpeedAt / SpeedFailed 同理用于速度列。
	//
	// 【界面为什么要这三态】审查 #19：失败时不再把旧值写成 0，所以界面必须自己
	// 分清"没测过（无值、无时间）/ 有效（有值、最近一次同类测试成功）/
	// 上次成功值（有值但最近一次同类测试没测出，已过期）"。判断放在这里算好，
	// 前端只负责显示（见 NodeCell.vue）。
	LatencyAt     time.Time `json:"latencyAt"`
	LatencyFailed bool      `json:"latencyFailed"`
	SpeedAt       time.Time `json:"speedAt"`
	SpeedFailed   bool      `json:"speedFailed"`
	// ExitIP 是最近一次"出口 IP"测试的结果（不落盘）。
	ExitIP string `json:"exitIp"`

	TodayUp    int64  `json:"todayUp"`
	TodayDown  int64  `json:"todayDown"`
	TodayTotal int64  `json:"todayTotal"`
	TodayText  string `json:"todayText"`

	ActiveConn int64 `json:"activeConn"`
	TotalConn  int64 `json:"totalConn"`

	LastUsed  time.Time `json:"lastUsed"`
	CreatedAt time.Time `json:"createdAt"`
}

// Summary 是顶部的总览。
// TrafficSample 是一次每秒采样的**总速率**快照（字节/秒）。
//
// 为什么在 Go 侧留历史、而不是让界面自己攒：界面侧的刷新是去抖合并的
// （见 store.js 的 scheduleRefresh），拿到的样本时间间隔不规则；而这里是
// 精确的每秒一次，画曲线才有意义。
type TrafficSample struct {
	At   int64 `json:"at"` // Unix 毫秒
	Up   int64 `json:"up"` // 上行 字节/秒
	Down int64 `json:"down"`
}

// trafficHistorySeconds 是历史缓冲保留的秒数。
// 1 秒一个样本、每个 24 字节 → 约 86KB，可以放心常驻内存。
const trafficHistorySeconds = 3600

type Summary struct {
	Total     int    `json:"total"`
	Running   int    `json:"running"`
	Error     int    `json:"error"`
	TodayUp   int64  `json:"todayUp"`
	TodayDown int64  `json:"todayDown"`
	TodayText string `json:"todayText"`
	// 实时速率（字节/秒），来自每秒采样。界面用它显示 ↑↓。
	UpRate   int64 `json:"upRate"`
	DownRate int64 `json:"downRate"`
	// MemMB 是进程内存占用（MB）。
	//
	// 为什么用 Go 运行时自己的数字而不是 Win32 工作集：内核（sing-box）跑在**本进程里**，
	// 而界面在另一个 WebView2 进程里 —— 所以"Go 运行时占用"恰好对应主流客户端界面上那个
	// "内核占用"，比整进程工作集更贴切，也省掉一次系统调用。
	MemMB        int64  `json:"memMB"`
	ActiveConn   int64  `json:"activeConn"`
	FrontDesc    string `json:"frontDesc"`
	FrontOK      bool   `json:"frontOk"`
	ConfigPath   string `json:"configPath"`
	DataDir      string `json:"dataDir"`
	AllowLan     bool   `json:"allowLan"`
	StartOnBoot  string `json:"startOnBoot"` // 恒为"不注册"，用来在界面上明确说明
	ListeningAll []int  `json:"listeningAll"`
}

// Nodes 返回全部节点的视图（顺序与配置一致）。
func (a *App) Nodes() []NodeView {
	a.mu.RLock()
	nodes := make([]config.Node, len(a.cfg.Nodes))
	copy(nodes, a.cfg.Nodes)
	servers := make(map[string]*proxyStats, len(a.servers))
	for id, s := range a.servers {
		st := s.Stats()
		servers[id] = &proxyStats{
			running:    s.Running(),
			addr:       s.Addr(),
			up:         st.Up.Load(),
			down:       st.Down.Load(),
			conns:      st.Conns.Load(),
			totalConns: st.TotalConn.Load(),
			errs:       st.Errors.Load(),
			lastErr:    s.LastError(),
		}
	}
	errs := make(map[string]string, len(a.errs))
	for k, v := range a.errs {
		errs[k] = v
	}
	exits := make(map[string]string, len(a.exitIPs))
	for k, v := range a.exitIPs {
		exits[k] = v
	}
	a.mu.RUnlock()

	out := make([]NodeView, 0, len(nodes))
	for _, n := range nodes {
		v := a.buildView(n, servers[n.ID], errs[n.ID])
		v.ExitIP = exits[n.ID]
		out = append(out, v)
	}
	return out
}

type proxyStats struct {
	running    bool
	addr       string
	up, down   int64
	conns      int64
	totalConns int64
	errs       int64
	lastErr    string
}

func (a *App) buildView(n config.Node, st *proxyStats, errMsg string) NodeView {
	v := NodeView{
		ID:            n.ID,
		Name:          n.Name,
		Protocol:      n.Protocol,
		ProtocolLabel: config.ProtocolLabel(n.Protocol),
		Transport:     n.TransportLabel(),
		Server:        n.Server,
		Port:          n.Port,
		ListenHost:    n.ListenHost,
		ListenPort:    n.ListenPort,
		ListenAuth:    n.HasListenAuth(),
		Front:         n.Front,
		FrontLabel:    a.DescribeFront(n),
		Remark:        n.Remark,
		TLSEnabled:    n.TLS.Enabled,
		Active:        n.Active,
		State:         StateStopped,
		StateLabel:    "未激活",
		LatencyMs:     n.LatencyMs,
		LatencyKind:   n.LatencyKind,
		RealMs:        n.RealMs,
		SpeedMBps:     n.SpeedMBps,
		LatencyAt:     n.LatencyAt,
		LatencyFailed: n.LatencyFailed,
		SpeedAt:       n.SpeedAt,
		SpeedFailed:   n.SpeedFailed,
		TodayUp:       n.UpBytes,
		TodayDown:     n.DownBytes,
		TodayTotal:    n.TotalBytes(),
		TodayText:     FormatBytes(n.TotalBytes()),
		LastUsed:      n.LastUsed,
		CreatedAt:     n.CreatedAt,
	}

	if st != nil && st.running {
		v.State = StateRunning
		v.StateLabel = "已激活"
		v.ActiveConn = st.conns
		v.TotalConn = st.totalConns
		v.Detail = fmt.Sprintf("监听 %s · 活动连接 %d", st.addr, st.conns)
		return v
	}

	if errMsg != "" {
		v.State = StateError
		v.StateLabel = "错误"
		v.Detail = errMsg
		return v
	}
	if n.Active {
		v.Detail = "已激活但未在运行"
	}
	return v
}

// Summary 返回总览信息。
func (a *App) Summary() Summary {
	views := a.Nodes()
	s := Summary{
		Total:      len(views),
		FrontDesc:  a.Front().Describe(),
		FrontOK:    a.Front().Type != config.FrontTypeDirect,
		ConfigPath: a.cfgPath,
		DataDir:    config.DataDir(),
		AllowLan:   a.Settings().AllowLan,
		// 本程序在设计上就不注册开机自启动，界面上把它明确写出来，
		// 免得用户到处找这个开关。
		StartOnBoot: "不注册（本程序不会开机自启）",
	}
	for _, v := range views {
		switch v.State {
		case StateRunning:
			s.Running++
			s.ActiveConn += v.ActiveConn
			s.ListeningAll = append(s.ListeningAll, v.ListenPort)
		case StateError:
			s.Error++
		}
		s.TodayUp += v.TodayUp
		s.TodayDown += v.TodayDown
	}
	s.TodayText = FormatBytes(s.TodayUp + s.TodayDown)
	// 速率取最近一次采样的值：它就是"过去 1 秒的增量"，比界面上做差更准
	s.UpRate = a.upRate.Load()
	s.DownRate = a.downRate.Load()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s.MemMB = int64(ms.Sys / 1024 / 1024)
	sort.Ints(s.ListeningAll)
	return s
}

// ------------------------------------------------------------------
// 今日统计
// ------------------------------------------------------------------

// statsLoop 每秒把各服务的累计计数折算成"今日增量"。
//
// 为什么要折算而不是直接用服务的累计值：服务对象在"停止再启动"后会被重建，
// 计数从 0 开始；而"今日统计"必须跨重启累加，所以只能记增量。
func (a *App) statsLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastPersist := time.Now()
	for {
		select {
		case <-a.closing:
			return
		case <-ticker.C:
		}
		a.tickStats()
		a.emit(EventStats, "")
		if time.Since(lastPersist) >= 30*time.Second {
			a.persistStats(false)
			lastPersist = time.Now()
		}
	}
}

func (a *App) tickStats() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.rollDayLocked()

	now := time.Now()
	var tickUp, tickDown int64
	// 本轮的按节点增量（循环里拿不到就别的地方再算一遍）
	nodeDelta := make(map[string][2]int64, len(a.servers))
	for id, srv := range a.servers {
		idx := a.indexOfLocked(id)
		if idx < 0 {
			continue
		}
		st := srv.Stats()
		up, down := st.Up.Load(), st.Down.Load()

		dUp := up - a.baseUp[id]
		dDown := down - a.baseDown[id]
		if dUp < 0 {
			// 服务被重建过，重置基线
			a.baseUp[id] = up
			dUp = 0
		}
		if dDown < 0 {
			a.baseDown[id] = down
			dDown = 0
		}
		a.baseUp[id] = up
		a.baseDown[id] = down

		if dUp == 0 && dDown == 0 {
			continue
		}
		a.cfg.Nodes[idx].UpBytes += dUp
		a.cfg.Nodes[idx].DownBytes += dDown
		a.cfg.Nodes[idx].LastUsed = now
		a.dirty = true

		tickUp += dUp
		tickDown += dDown
		nodeDelta[id] = [2]int64{dUp, dDown}
	}

	// 留下来的这两个数原先算完就扔了 —— 于是界面既拿不到速率、也没法画曲线。
	a.upRate.Store(tickUp)
	a.downRate.Store(tickDown)
	sampleAt := now.UnixMilli()
	a.pushTrafficLocked(TrafficSample{At: sampleAt, Up: tickUp, Down: tickDown})

	// 按节点各写一条：**遍历配置里的所有节点**（不只是运行中的），
	// 这样每个节点的序列都按同一节拍推进，切节点看图时横轴才对得上。
	for _, n := range a.cfg.Nodes {
		d := nodeDelta[n.ID]
		a.pushNodeTrafficLocked(n.ID, TrafficSample{At: sampleAt, Up: d[0], Down: d[1]})
	}
}

// pushNodeTrafficLocked 追加某节点的一个采样并裁剪。调用方须持有 mu。
func (a *App) pushNodeTrafficLocked(nodeID string, s TrafficSample) {
	if a.trafficByNode == nil {
		a.trafficByNode = make(map[string][]TrafficSample)
	}
	ring := append(a.trafficByNode[nodeID], s)
	if n := len(ring); n > trafficHistorySeconds {
		copy(ring, ring[n-trafficHistorySeconds:])
		ring = ring[:trafficHistorySeconds]
	}
	a.trafficByNode[nodeID] = ring
}

// pushTrafficLocked 追加一个采样并裁掉超出保留时长的旧样本。调用方须持有 mu。
//
// 单独抽出来是为了能直接测"裁剪"这段算术 —— 它只在跑满一小时之后才会生效，
// 靠等是等不到的（那种测试没人会跑，等于没测）。
func (a *App) pushTrafficLocked(s TrafficSample) {
	a.traffic = append(a.traffic, s)
	if n := len(a.traffic); n > trafficHistorySeconds {
		// 一次挪掉一批，避免每秒都做一次全量拷贝
		copy(a.traffic, a.traffic[n-trafficHistorySeconds:])
		a.traffic = a.traffic[:trafficHistorySeconds]
	}
}

// TrafficHistory 返回最近 seconds 秒的速率样本（按时间升序）。
//
// nodeID 为空表示"全部节点"（总量）；给了节点 ID 就返回那个节点的曲线。
// seconds <= 0 或超过缓冲长度时，返回缓冲里的全部样本。
func (a *App) TrafficHistory(seconds int, nodeID string) []TrafficSample {
	a.mu.RLock()
	defer a.mu.RUnlock()
	ring := a.traffic
	if nodeID != "" {
		ring = a.trafficByNode[nodeID]
	}
	n := len(ring)
	if seconds > 0 && seconds < n {
		n = seconds
	}
	out := make([]TrafficSample, n)
	copy(out, ring[len(ring)-n:])
	return out
}

// rollDayLocked 跨天时把"今日统计"清零。
func (a *App) rollDayLocked() {
	today := config.Today()
	if a.lastDay == "" {
		a.lastDay = today
	}
	if a.lastDay == today {
		return
	}
	for i := range a.cfg.Nodes {
		if a.cfg.Nodes[i].StatsDay != today {
			a.cfg.Nodes[i].StatsDay = today
			a.cfg.Nodes[i].UpBytes = 0
			a.cfg.Nodes[i].DownBytes = 0
		}
	}
	a.lastDay = today
	a.dirty = true
}

// persistStats 把统计落盘。force 为真时无视 dirty 标记。
func (a *App) persistStats(force bool) {
	a.mu.Lock()
	if !force && !a.dirty {
		a.mu.Unlock()
		return
	}
	a.dirty = false
	cfg := a.cfg
	path := a.cfgPath
	a.mu.Unlock()
	if err := cfg.Save(path); err != nil {
		a.Logf("保存统计失败：%v", err)
	}
}

// ResetTodayStats 把指定节点（传空表示全部）的今日统计清零。
func (a *App) ResetTodayStats(ids []string) error {
	a.mu.Lock()
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for i := range a.cfg.Nodes {
		if len(want) == 0 || want[a.cfg.Nodes[i].ID] {
			a.cfg.Nodes[i].UpBytes = 0
			a.cfg.Nodes[i].DownBytes = 0
			a.cfg.Nodes[i].StatsDay = config.Today()
		}
	}
	a.mu.Unlock()
	a.emit(EventNodes, "")
	return a.Save()
}

// ------------------------------------------------------------------
// 格式化
// ------------------------------------------------------------------

// 流量单位表。用户要求"最小单位 KB，自动根据数字大小换算 MB GB TB PB"。
var byteUnits = []string{"KB", "MB", "GB", "TB", "PB"}

// FormatBytes 把字节数格式化成人类可读的字符串。
//
// 口径：1 KB = 1024 字节（和常见的网络工具一致）。
// 0 显示成 "0 KB" 而不是 "0 B"，因为界面上"最小单位是 KB"。
func FormatBytes(n int64) string {
	if n <= 0 {
		return "0 KB"
	}
	const unit = 1024
	// 先换算到 KB
	v := float64(n) / unit
	i := 0
	for v >= unit && i < len(byteUnits)-1 {
		v /= unit
		i++
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, byteUnits[i])
	}
	if v >= 10 {
		return fmt.Sprintf("%.1f %s", v, byteUnits[i])
	}
	return fmt.Sprintf("%.2f %s", v, byteUnits[i])
}

// FormatSpeed 格式化速度。
func FormatSpeed(mbps float64) string {
	if mbps <= 0 {
		return ""
	}
	if mbps >= 100 {
		return fmt.Sprintf("%.0f", mbps)
	}
	return fmt.Sprintf("%.2f", mbps)
}

// FormatLatency 格式化延迟。
func FormatLatency(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", ms)
}

// TrimSpaceAll 去掉字符串里所有空白，供界面做"模糊匹配"前的归一化。
func TrimSpaceAll(s string) string {
	return strings.Join(strings.Fields(s), "")
}
