// Package app 是 ChainProxy 的应用状态机：
// 持有配置、管理所有链路服务的生命周期、向 GUI/CLI 暴露状态与测试能力。
package app

import (
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"chainproxy/internal/config"
	"chainproxy/internal/netutil"
	"chainproxy/internal/proxy"
)

// 单条链路的状态。
const (
	StateRunning = "running"
	StateStopped = "stopped"
	StateError   = "error"
	StateOff     = "off" // 已配置但被禁用
)

// StatusRow 是 GUI 列表里的一行。
type StatusRow struct {
	ID         string
	Name       string
	Type       string
	ListenHost string
	ListenPort int
	Server     string
	State      string
	Detail     string
	UpBytes    int64
	DownBytes  int64
	ActiveConn int64
	TotalConn  int64
}

// Event 是应用向外广播的事件。
type Event struct {
	Kind string // "log" | "state" | "config"
	Text string
}

// App 是整个应用的运行体。
type App struct {
	mu       sync.RWMutex
	cfg      *config.Config
	cfgPath  string
	servers  map[string]*proxy.Server
	running  bool
	front    proxy.Front
	logger   *log.Logger
	logFile  *os.File
	subsMu   sync.Mutex
	subs     []subscriber
	subSeq   uint64
	startErr string
}

// New 创建应用实例；cfg 为 nil 时从磁盘载入。
func New(cfgPath string) (*App, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	a := &App{
		cfg:     cfg,
		cfgPath: cfgPath,
		servers: map[string]*proxy.Server{},
	}

	if f, err := os.OpenFile(config.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		a.logFile = f
		a.logger = log.New(f, "", log.LstdFlags)
	} else {
		a.logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	return a, nil
}

// Close 停止所有服务并释放资源。
func (a *App) Close() {
	a.Stop()
	a.mu.Lock()
	fe := a.front
	a.front = nil
	a.mu.Unlock()
	if fe != nil {
		_ = fe.Close()
	}
	if a.logFile != nil {
		_ = a.logFile.Close()
	}
}

// ------------------------------------------------------------------
// 事件
// ------------------------------------------------------------------

// subscriber 是一个带唯一 ID 的回调，便于精确取消。
type subscriber struct {
	id uint64
	fn func(Event)
}

// Subscribe 注册事件回调，返回取消函数。
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

// Logf 写日志并广播到 GUI。
func (a *App) Logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if a.logger != nil {
		a.logger.Println(line)
	}
	a.emit("log", line)
}

// ------------------------------------------------------------------
// 配置访问
// ------------------------------------------------------------------

// Config 返回配置指针（只读使用；修改请走下面的方法）。
func (a *App) Config() *config.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

// SetFrontConfig 更新前置代理设置（不自动重启，需要调用 Reload 或 Restart）。
func (a *App) SetFrontConfig(f config.Front) error {
	a.mu.Lock()
	a.cfg.Front = f
	a.mu.Unlock()
	return a.Save()
}

// AddUpstream 添加一条住宅代理配置。
func (a *App) AddUpstream(u config.Upstream) error {
	a.mu.Lock()
	if u.ID == "" {
		u.ID = config.NewID()
	}
	if u.ListenHost == "" {
		u.ListenHost = a.cfg.DefaultListenHost
	}
	if u.ListenPort == 0 {
		u.ListenPort = a.nextFreePortLocked()
	}
	a.cfg.Upstreams = append(a.cfg.Upstreams, u)
	a.mu.Unlock()
	a.emit("config", "")
	return a.Save()
}

// UpdateUpstream 替换一条住宅代理配置（按 ID 匹配）。
func (a *App) UpdateUpstream(u config.Upstream) error {
	a.mu.Lock()
	idx := a.indexOfLocked(u.ID)
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("未找到要更新的住宅代理：%s", u.Name)
	}
	wasRunning := a.running && a.cfg.Upstreams[idx].Enabled
	a.cfg.Upstreams[idx] = u
	a.mu.Unlock()

	if wasRunning {
		// 运行中改配置：重启这一条，使新配置立刻生效
		if err := a.restartUpstream(u.ID); err != nil {
			return err
		}
	}
	a.emit("config", "")
	return a.Save()
}

// DeleteUpstream 删除一条住宅代理配置，并释放它的端口。
func (a *App) DeleteUpstream(id string) error {
	a.mu.Lock()
	idx := a.indexOfLocked(id)
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("未找到要删除的住宅代理")
	}
	name := a.cfg.Upstreams[idx].Name
	srv := a.servers[id]
	delete(a.servers, id)
	a.cfg.Upstreams = append(a.cfg.Upstreams[:idx], a.cfg.Upstreams[idx+1:]...)
	a.mu.Unlock()

	if srv != nil {
		_ = srv.Stop()
	}
	a.Logf("已删除住宅代理「%s」并释放其端口", name)
	a.emit("config", "")
	return a.Save()
}

// MoveUpstream 调整顺序（-1 上移，+1 下移）。
func (a *App) MoveUpstream(id string, delta int) error {
	a.mu.Lock()
	idx := a.indexOfLocked(id)
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("未找到该住宅代理")
	}
	j := idx + delta
	if j < 0 || j >= len(a.cfg.Upstreams) {
		a.mu.Unlock()
		return nil
	}
	a.cfg.Upstreams[idx], a.cfg.Upstreams[j] = a.cfg.Upstreams[j], a.cfg.Upstreams[idx]
	a.mu.Unlock()
	a.emit("config", "")
	return a.Save()
}

// SetUpstreamEnabled 启用/停用一条住宅代理。
func (a *App) SetUpstreamEnabled(id string, enabled bool) error {
	a.mu.Lock()
	idx := a.indexOfLocked(id)
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("未找到该住宅代理")
	}
	a.cfg.Upstreams[idx].Enabled = enabled
	a.mu.Unlock()

	a.mu.RLock()
	running := a.running
	a.mu.RUnlock()
	if running {
		if enabled {
			if err := a.startUpstream(id); err != nil {
				return err
			}
		} else {
			a.mu.Lock()
			srv := a.servers[id]
			delete(a.servers, id)
			a.mu.Unlock()
			if srv != nil {
				_ = srv.Stop()
				a.Logf("已停止「%s」，端口 %d 已释放", srv.Addr(), srv.ListenPort())
			}
		}
	}
	a.emit("config", "")
	return a.Save()
}

// Save 把当前配置落盘。
func (a *App) Save() error {
	a.mu.RLock()
	cfg := a.cfg
	path := a.cfgPath
	a.mu.RUnlock()
	if err := cfg.Save(path); err != nil {
		return err
	}
	return nil
}

// ------------------------------------------------------------------
// 启停
// ------------------------------------------------------------------

// IsRunning 返回主开关状态。
func (a *App) IsRunning() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.running
}

// Start 启动全部已启用的住宅代理链路。
// 任何单条链路的失败都不会影响其它链路——这正是"端口不能重复 + 互不影响"的要求。
func (a *App) Start() error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil
	}
	cfg := a.cfg
	front := proxy.NewFront(cfg.Front)
	a.front = front
	enabled := make([]config.Upstream, 0, len(cfg.Upstreams))
	for _, u := range cfg.Upstreams {
		if u.Enabled {
			enabled = append(enabled, u)
		}
	}
	a.running = true
	a.startErr = ""
	a.mu.Unlock()

	a.Logf("前置代理：%s", front.Describe())

	okCount := 0
	var firstErr error
	for _, u := range enabled {
		if err := a.startUpstreamWith(front, u); err != nil {
			a.Logf("启动「%s」失败：%v", u.Name, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		okCount++
	}
	a.mu.Lock()
	a.startErr = ""
	if okCount == 0 && firstErr != nil {
		a.running = false
	}
	a.mu.Unlock()

	if okCount == 0 && firstErr != nil {
		return firstErr
	}
	a.Logf("已启动 %d 条链路", okCount)
	a.emit("state", "")
	return nil
}

// Stop 停止全部链路，释放全部端口。
func (a *App) Stop() {
	a.mu.Lock()
	if !a.running && len(a.servers) == 0 {
		a.mu.Unlock()
		return
	}
	servers := a.servers
	a.servers = map[string]*proxy.Server{}
	a.running = false
	a.mu.Unlock()

	var wg sync.WaitGroup
	for _, s := range servers {
		wg.Add(1)
		go func(s *proxy.Server) {
			defer wg.Done()
			_ = s.Stop()
			a.Logf("已停止「%s」，端口 %d 已释放", s.Name(), s.ListenPort())
		}(s)
	}
	wg.Wait()
	a.emit("state", "")
}

func (a *App) startUpstream(id string) error {
	a.mu.RLock()
	idx := a.indexOfRLocked(id)
	front := a.front
	var u config.Upstream
	if idx >= 0 {
		u = a.cfg.Upstreams[idx]
	}
	a.mu.RUnlock()
	if idx < 0 {
		return fmt.Errorf("未找到该住宅代理")
	}
	if front == nil {
		return fmt.Errorf("链路服务尚未启动")
	}
	return a.startUpstreamWith(front, u)
}

func (a *App) startUpstreamWith(front proxy.Front, u config.Upstream) error {
	// 端口被占用时给出明确提示（GUI 会标红）
	if err := netutil.PortAvailable(u.ListenHost, u.ListenPort); err != nil {
		a.setUpstreamError(u.ID, err.Error())
		return fmt.Errorf("「%s」%w", u.Name, err)
	}

	srv, err := proxy.NewServer(u, front, a.Logf)
	if err != nil {
		return fmt.Errorf("「%s」构造失败: %w", u.Name, err)
	}
	if err := srv.Start(); err != nil {
		a.setUpstreamError(u.ID, err.Error())
		return fmt.Errorf("「%s」%w", u.Name, err)
	}

	a.mu.Lock()
	// 重启前先关掉旧实例，避免端口残留
	if old := a.servers[u.ID]; old != nil {
		_ = old.Stop()
	}
	a.servers[u.ID] = srv
	a.mu.Unlock()

	a.clearUpstreamError(u.ID)
	a.Logf("「%s」已启动，监听 %s（%s）", u.Name, srv.Addr(), u.Describe())
	a.emit("state", "")
	return nil
}

func (a *App) restartUpstream(id string) error {
	a.mu.Lock()
	srv := a.servers[id]
	delete(a.servers, id)
	a.mu.Unlock()
	if srv != nil {
		_ = srv.Stop()
	}
	return a.startUpstream(id)
}

// ------------------------------------------------------------------
// 错误与状态
// ------------------------------------------------------------------

var errStore = sync.Map{} // id -> string

func (a *App) setUpstreamError(id, msg string) { errStore.Store(id, msg) }

func (a *App) clearUpstreamError(id string) { errStore.Delete(id) }

func (a *App) upstreamError(id string) string {
	if v, ok := errStore.Load(id); ok {
		return v.(string)
	}
	return ""
}

// ClearAllErrors 清空所有错误标记（重新全局启动时调用）。
func (a *App) ClearAllErrors() {
	a.mu.RLock()
	ids := make([]string, 0, len(a.cfg.Upstreams))
	for _, u := range a.cfg.Upstreams {
		ids = append(ids, u.ID)
	}
	a.mu.RUnlock()
	for _, id := range ids {
		a.clearUpstreamError(id)
	}
}

// Status 返回所有住宅代理的状态行（顺序与配置一致）。
func (a *App) Status() []StatusRow {
	a.mu.RLock()
	cfg := a.cfg
	servers := make(map[string]*proxy.Server, len(a.servers))
	for k, v := range a.servers {
		servers[k] = v
	}
	running := a.running
	a.mu.RUnlock()

	rows := make([]StatusRow, 0, len(cfg.Upstreams))
	for _, u := range cfg.Upstreams {
		row := StatusRow{
			ID:         u.ID,
			Name:       u.Name,
			Type:       u.Type,
			ListenHost: u.ListenHost,
			ListenPort: u.ListenPort,
			Server:     u.Describe(),
			State:      StateStopped,
		}
		if !u.Enabled {
			row.State = StateOff
			row.Detail = "已禁用"
		}
		if srv, ok := servers[u.ID]; ok && srv.Running() {
			st := srv.Stats()
			row.State = StateRunning
			row.Detail = fmt.Sprintf("监听 %s · %d 个活动连接", srv.Addr(), st.Conns.Load())
			row.UpBytes = st.Up.Load()
			row.DownBytes = st.Down.Load()
			row.ActiveConn = st.Conns.Load()
			row.TotalConn = st.TotalConn.Load()
		} else if running && u.Enabled {
			if msg := a.upstreamError(u.ID); msg != "" {
				row.State = StateError
				row.Detail = msg
			} else {
				row.State = StateStopped
				row.Detail = "未启动"
			}
		} else if !u.Enabled {
			if msg := a.upstreamError(u.ID); msg != "" {
				row.Detail = msg
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// FrontStatus 返回前置代理的可读描述与连通性。
func (a *App) FrontStatus() (string, bool) {
	a.mu.RLock()
	fc := a.cfg.Front
	running := a.running
	front := a.front
	a.mu.RUnlock()

	desc := proxy.NewFront(fc).Describe()
	if !running && front == nil {
		return desc, false
	}
	if front != nil {
		return front.Describe(), true
	}
	return desc, true
}

// ------------------------------------------------------------------
// 内部工具
// ------------------------------------------------------------------

func (a *App) indexOfLocked(id string) int {
	for i := range a.cfg.Upstreams {
		if a.cfg.Upstreams[i].ID == id {
			return i
		}
	}
	return -1
}

func (a *App) indexOfRLocked(id string) int {
	for i := range a.cfg.Upstreams {
		if a.cfg.Upstreams[i].ID == id {
			return i
		}
	}
	return -1
}

// nextFreePortLocked 在配置内找一个不与任何已有上游冲突的端口。
func (a *App) nextFreePortLocked() int {
	used := map[int]bool{}
	for _, u := range a.cfg.Upstreams {
		used[u.ListenPort] = true
	}
	used[a.cfg.Front.Port] = true
	for p := 10810; p <= 10899; p++ {
		if !used[p] {
			return p
		}
	}
	sort.Ints(keysOf(used))
	for p := 20000; p <= 65535; p++ {
		if !used[p] {
			return p
		}
	}
	return 10810
}

func keysOf(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Validate 返回当前配置的全部问题。
func (a *App) Validate() []config.Issue {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.Validate()
}

// AutoStart 按配置在启动后自动开启链路。
func (a *App) AutoStart() {
	a.mu.RLock()
	want := a.cfg.StartOnLaunch
	a.mu.RUnlock()
	if !want {
		return
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := a.Start(); err != nil {
			a.Logf("自动启动失败：%v", err)
		}
	}()
}

// GetUpstream 按 ID 取出配置副本。
func (a *App) GetUpstream(id string) (config.Upstream, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if i := a.indexOfRLocked(id); i >= 0 {
		return a.cfg.Upstreams[i], true
	}
	return config.Upstream{}, false
}

// GetFront 返回前置代理配置副本。
func (a *App) GetFront() config.Front {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.Front
}
