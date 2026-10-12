package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"chainproxy/internal/config"
	"chainproxy/internal/proxy"
)

// ------------------------------------------------------------------
// 节点增删改查
// ------------------------------------------------------------------

// AddNode 新增一个节点。
//
// 端口为 0 时自动分配一个空闲端口（规则见 internal/ports）。
func (a *App) AddNode(n config.Node) (config.Node, error) {
	a.mu.Lock()
	// 【归一化必须在这里做，别指望 Validate】校验是读路径、不再回写配置
	// （见 config.NormalizeNode 与 config.Validate 的注释）。补协议/监听地址/ID
	// 只能由写入路径显式做，而且必须在下面按 ID 分配端口之前 ——
	// 端口分配器的 key 就是节点 ID。
	a.cfg.NormalizeNode(&n)
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	if n.StatsDay == "" {
		n.StatsDay = config.Today()
	}
	// 【审查 #28】名字必须在锁内定：uniqueNameLocked 要读整份节点表。
	// 而下面自动分配端口时**必须放锁**去探测，所以这里要先把"名字是不是
	// 自动生成的"记下来 —— 放锁期间别的节点可能抢走同一个默认名字，
	// 落盘时得在锁内重算一次。
	autoName := n.Name == ""
	if autoName {
		n.Name = a.suggestNameLocked(n.Protocol)
	}
	needPort := n.ListenPort == 0
	if !needPort {
		// 端口是用户填的：不需要任何探测，就在这一把锁里落盘
		a.cfg.Nodes = append(a.cfg.Nodes, n)
	}
	a.mu.Unlock()

	if needPort {
		// 【审查 #21】分配不到端口时必须在这里停住：allocator.Next() 耗尽时
		// 返回 ports.NoPort(0)，而 0 在**这个函数里**正好是"待自动分配"的语义，
		// 放过去就会新增一个监听端口 0 的节点（net.Listen 遇到 0 会随便挑一个
		// 临时端口，等于配置和实际监听完全对不上），或者写进一个 65536 之类的
		// 非法端口，让配置校验一直报错。
		//
		// 【审查 #28】探测（最多 512 次 net.Listen）在 allocPort 里放到锁外做，
		// 落盘前它会带着最新的占用表复查一次这个端口还没被别人抢走。
		if _, err := a.allocPort(n.ID, func(port int) {
			n.ListenPort = port
			if autoName {
				n.Name = a.suggestNameLocked(n.Protocol)
			}
			a.cfg.Nodes = append(a.cfg.Nodes, n)
		}); err != nil {
			if errors.Is(err, errNoFreePort) {
				return config.Node{}, fmt.Errorf("本机没有可用的本地端口（已扫到 65535），请先释放一个端口再新增")
			}
			return config.Node{}, err
		}
	}

	a.Logf("已新增节点「%s」（%s，本地端口 %d）", n.Name, config.ProtocolLabel(n.Protocol), n.ListenPort)
	a.emit(EventNodes, "")
	return n, a.Save()
}

// UpdateNode 按 ID 替换一个节点。
//
// 如果该节点正在运行且关键字段变了，就重启它（保持运行状态）。
func (a *App) UpdateNode(n config.Node) (config.Node, error) {
	a.mu.Lock()
	idx := a.indexOfLocked(n.ID)
	if idx < 0 {
		a.mu.Unlock()
		return config.Node{}, fmt.Errorf("未找到要更新的节点")
	}
	old := a.cfg.Nodes[idx]
	// 编辑对话框可能把监听地址清空、或送来一个空协议；补默认值是**写路径**的活
	// （Validate 已改成只读，不会再替我们补，见 config.NormalizeNode）。
	a.cfg.NormalizeNode(&n)
	// 保留运行时才知道的字段，避免编辑一次就把统计和最近使用时间清空
	n.CreatedAt = old.CreatedAt
	n.StatsDay = old.StatsDay
	n.UpBytes = old.UpBytes
	n.DownBytes = old.DownBytes
	n.LastUsed = old.LastUsed
	// 测试结果是节点属性，界面会单独更新，这里保留旧值
	n.Active = old.Active
	a.cfg.Nodes[idx] = n
	a.mu.Unlock()

	a.Logf("已更新节点「%s」", n.Name)
	a.emit(EventNodes, "")
	if err := a.Save(); err != nil {
		return n, err
	}

	// 正在运行就直接重启，让新配置立刻生效
	if a.serverOf(n.ID) != nil {
		if err := a.restartNode(n.ID); err != nil {
			a.Notify("error", "重启节点失败", fmt.Sprintf("「%s」：%v", n.Name, err))
		}
	}
	return n, nil
}

// CloneNode 克隆一个节点（新 ID、新端口、名字加"副本"）。
func (a *App) CloneNode(id string) (config.Node, error) {
	a.mu.Lock()
	idx := a.indexOfLocked(id)
	if idx < 0 {
		a.mu.Unlock()
		return config.Node{}, fmt.Errorf("未找到要克隆的节点")
	}
	src := a.cfg.Nodes[idx]
	dup := src
	dup.ID = config.NewID()
	dup.Active = false
	dup.CreatedAt = time.Now()
	dup.LastUsed = time.Time{}
	dup.LatencyMs, dup.RealMs, dup.SpeedMBps, dup.LatencyKind = 0, 0, 0, ""
	// 克隆出来的节点从没测过：连"上次成功值的时间/失败标记"一起清掉。
	// 只清数值不清标记的话，新节点会顶着原节点那份失败标记，界面把它的空值
	// 显示成"失败"（审查 #19 引入的字段，克隆路径同样得跟上）。
	dup.LatencyAt, dup.SpeedAt = time.Time{}, time.Time{}
	dup.LatencyFailed, dup.SpeedFailed = false, false
	dup.UpBytes, dup.DownBytes, dup.StatsDay = 0, 0, config.Today()
	a.mu.Unlock()

	// 【审查 #21】克隆也必须先确认真的分到了端口，否则会把 0（=待分配）
	// 或 65536 这类坏端口写进配置里。dup 还没 append 进 a.cfg.Nodes，
	// 分配失败时什么都不用回滚，配置保持原样。
	//
	// 【审查 #28】端口探测同样放到锁外（见 allocPort），落盘前在锁内复查；
	// 名字也留到那一步在锁内最终定 —— 放锁期间另一个克隆可能已经拿走了
	// 「XX-副本」这个名字。
	if _, err := a.allocPort("", func(port int) {
		dup.ListenPort = port
		dup.Name = a.uniqueNameLocked(src.Name + "-副本")
		a.cfg.Nodes = append(a.cfg.Nodes, dup)
	}); err != nil {
		if errors.Is(err, errNoFreePort) {
			return config.Node{}, fmt.Errorf("本机没有可用的本地端口（已扫到 65535），请先释放一个端口再克隆")
		}
		return config.Node{}, err
	}

	a.Logf("已克隆节点「%s」→「%s」（本地端口 %d）", src.Name, dup.Name, dup.ListenPort)
	a.emit(EventNodes, "")
	return dup, a.Save()
}

// DeleteNodes 删除若干节点，并释放它们的端口。
//
// 如果有别的节点把这个节点当前置，删除后那些节点的前置会失效，
// 这里会把它们的前置改回"直连"并明确告知，避免留下一个静默不通的配置。
func (a *App) DeleteNodes(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}

	// 先停掉正在运行的
	a.mu.RLock()
	running := make([]*proxy.Server, 0, len(ids))
	for _, id := range ids {
		if s := a.servers[id]; s != nil {
			running = append(running, s)
		}
	}
	a.mu.RUnlock()
	for _, s := range running {
		_ = s.Stop()
	}

	a.mu.Lock()
	kept := make([]config.Node, 0, len(a.cfg.Nodes))
	var names []string
	for _, n := range a.cfg.Nodes {
		if want[n.ID] {
			names = append(names, n.Name)
			delete(a.servers, n.ID)
			delete(a.errs, n.ID)
			delete(a.baseUp, n.ID)
			delete(a.baseDown, n.ID)
			// 顺带清掉它的流量历史：不清的话每删一个节点就残留约 86KB
			delete(a.trafficByNode, n.ID)
			continue
		}
		kept = append(kept, n)
	}
	a.cfg.Nodes = kept

	// 修掉指向已删除节点的前置引用
	var orphans []string
	for i := range a.cfg.Nodes {
		fid := a.cfg.Nodes[i].FrontNodeID()
		if fid != "" && want[fid] {
			a.cfg.Nodes[i].Front = config.FrontDirect
			orphans = append(orphans, a.cfg.Nodes[i].Name)
		}
	}
	a.mu.Unlock()

	a.Logf("已删除 %d 个节点：%s", len(names), strings.Join(names, "、"))
	if len(orphans) > 0 {
		a.Notify("warning", "前置已被删除", fmt.Sprintf("这些节点的前置已改回「直连」：%s", strings.Join(orphans, "、")))
	}
	a.emit(EventNodes, "")
	return a.Save()
}

// GetNode 按 ID 取出节点副本。
func (a *App) GetNode(id string) (config.Node, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.Find(id)
}

// NodeConfigs 返回节点配置副本（顺序与配置一致）。
//
// 注意与 stats.go 里的 Nodes() 区分：那个返回的是给界面用的视图。
func (a *App) NodeConfigs() []config.Node {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]config.Node, len(a.cfg.Nodes))
	copy(out, a.cfg.Nodes)
	return out
}

// CountNodes 返回节点总数。
func (a *App) CountNodes() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.cfg.Nodes)
}

// ------------------------------------------------------------------
// 前置解析
// ------------------------------------------------------------------

// resolveFront 把节点的"前置"取值解析成一个具体的前置代理配置。
//
//	直连    → direct
//	global  → 设置里的全局前置
//	节点 ID → 用那个节点的本地端口当 SOCKS5 前置
//
// 以节点为前置时**永远走 127.0.0.1**：这是本机内部调用，
// 前置节点就算监听着 0.0.0.0 也没必要绕出去。
func (a *App) resolveFront(n config.Node) (config.Front, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.resolveFrontLocked(n)
}

func (a *App) resolveFrontLocked(n config.Node) (config.Front, error) {
	switch n.FrontKind() {
	case config.FrontDirect:
		return config.Front{Type: config.FrontTypeDirect, TimeoutMs: 10000}, nil
	case config.FrontGlobal:
		f := a.cfg.Front
		if f.TimeoutMs == 0 {
			f.TimeoutMs = 10000
		}
		return f, nil
	default:
		idx := a.cfg.Index(n.FrontNodeID())
		if idx < 0 {
			return config.Front{}, fmt.Errorf("前置节点不存在，请重新选择前置")
		}
		p := a.cfg.Nodes[idx]
		return config.Front{
			Type:      config.FrontTypeSocks5,
			Host:      "127.0.0.1",
			Port:      p.ListenPort,
			Username:  p.ListenUsername,
			Password:  p.ListenPassword,
			TimeoutMs: 10000,
		}, nil
	}
}

// DescribeFront 返回某节点前置的可读描述（给界面用）。
func (a *App) DescribeFront(n config.Node) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.describeFrontLocked(n)
}

// ------------------------------------------------------------------
// 激活 / 停止
// ------------------------------------------------------------------

// ActivationPlan 是"激活某个节点"时需要连带激活的节点顺序。
type ActivationPlan struct {
	// Order 是从最前面（被依赖的）到最后面的节点 ID 顺序。
	Order []string
	// Auto 是因为"作为别人的前置"而被自动带上的节点 ID。
	Auto []string
}

// PlanActivation 计算要把这些节点激活，需要按什么顺序启动哪些节点。
//
// 规则：如果 A 以前置 B，那么必须先启动 B；顺着前置链一直往前找。
// 顺带做环路检测（配置校验也会查，但改动频率高，这里再兜一道）。
func (a *App) PlanActivation(ids []string) (ActivationPlan, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	byID := map[string]config.Node{}
	for _, n := range a.cfg.Nodes {
		byID[n.ID] = n
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	state := map[string]int{}
	var order []string
	var auto []string
	explicit := map[string]bool{}
	for _, id := range ids {
		explicit[id] = true
		if _, ok := byID[id]; !ok {
			return ActivationPlan{}, fmt.Errorf("节点不存在")
		}
	}

	var visit func(id string, chain []string) error
	visit = func(id string, chain []string) error {
		switch state[id] {
		case black:
			return nil
		case gray:
			return fmt.Errorf("前置链形成了环路：%s", strings.Join(chain, " → "))
		}
		state[id] = gray
		n, ok := byID[id]
		if !ok {
			return fmt.Errorf("节点不存在")
		}
		if fid := n.FrontNodeID(); fid != "" {
			if err := visit(fid, append(chain, n.Name)); err != nil {
				return err
			}
		}
		state[id] = black
		order = append(order, id)
		return nil
	}

	for _, id := range ids {
		if err := visit(id, []string{byID[id].Name}); err != nil {
			return ActivationPlan{}, err
		}
	}

	// auto 从最终的启动顺序里筛出来，而不是在递归途中记录 ——
	// 这样它和 Order 的顺序一致（都是"实际启动顺序"），界面可以直接照着念
	// "将按 A → B → C 的顺序启动"。
	for _, id := range order {
		if !explicit[id] {
			auto = append(auto, id)
		}
	}
	return ActivationPlan{Order: order, Auto: auto}, nil
}

// Activate 激活若干节点（连同它们所需的前置节点）。
func (a *App) Activate(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	plan, err := a.PlanActivation(ids)
	if err != nil {
		return err
	}

	var started, failed []string
	for _, id := range plan.Order {
		a.mu.Lock()
		idx := a.indexOfLocked(id)
		if idx < 0 {
			a.mu.Unlock()
			continue
		}
		n := a.cfg.Nodes[idx]
		a.cfg.Nodes[idx].Active = true
		already := a.servers[id] != nil && a.servers[id].Running()
		a.mu.Unlock()

		if already {
			continue
		}
		if err := a.startNode(id); err != nil {
			failed = append(failed, fmt.Sprintf("「%s」：%v", n.Name, err))
			continue
		}
		started = append(started, n.Name)
	}

	_ = a.Save()
	a.emit(EventNodes, "")

	if len(failed) > 0 {
		a.Notify("error", "部分节点激活失败", strings.Join(failed, "\n"))
	}
	if len(started) > 0 && len(failed) == 0 {
		a.Notify("success", "已激活", strings.Join(started, "、"))
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s", strings.Join(failed, "；"))
	}
	return nil
}

// Deactivate 停止若干节点。
//
// 如果有别的**运行中的**节点把这个节点当前置，那它也会一起被停掉
// （否则那些节点会指向一个已经没人监听的端口，变成静默不通）。
func (a *App) Deactivate(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}

	// 把"依赖这些节点"的节点找出来，连带一起停
	a.mu.RLock()
	for changed := true; changed; {
		changed = false
		for _, n := range a.cfg.Nodes {
			if want[n.ID] {
				continue
			}
			fid := n.FrontNodeID()
			if fid != "" && want[fid] && a.servers[n.ID] != nil {
				want[n.ID] = true
				changed = true
			}
		}
	}
	dependents := make([]string, 0)
	names := make([]string, 0)
	for _, n := range a.cfg.Nodes {
		if want[n.ID] {
			names = append(names, n.Name)
			if !containsStr(ids, n.ID) {
				dependents = append(dependents, n.Name)
			}
		}
	}
	a.mu.RUnlock()

	for id := range want {
		_ = a.stopNode(id)
	}

	a.mu.Lock()
	for i := range a.cfg.Nodes {
		if want[a.cfg.Nodes[i].ID] {
			a.cfg.Nodes[i].Active = false
		}
	}
	a.mu.Unlock()

	a.Logf("已停止：%s", strings.Join(names, "、"))
	if len(dependents) > 0 {
		a.Notify("info", "连带停止了依赖节点", strings.Join(dependents, "、"))
	}
	a.persistStats(true)
	a.emit(EventNodes, "")
	return a.Save()
}

// ActivateAll 激活全部节点。
func (a *App) ActivateAll() error {
	a.mu.RLock()
	ids := make([]string, 0, len(a.cfg.Nodes))
	for _, n := range a.cfg.Nodes {
		ids = append(ids, n.ID)
	}
	a.mu.RUnlock()
	return a.Activate(ids)
}

// StopAll 停止全部节点。
func (a *App) StopAll() {
	a.mu.RLock()
	servers := make([]*proxy.Server, 0, len(a.servers))
	for _, s := range a.servers {
		servers = append(servers, s)
	}
	a.mu.RUnlock()

	for _, s := range servers {
		_ = s.Stop()
	}
	a.mu.Lock()
	a.servers = map[string]*proxy.Server{}
	a.baseUp = map[string]int64{}
	a.baseDown = map[string]int64{}
	a.mu.Unlock()

	a.emit(EventNodes, "")
}

// RestartAll 重启所有正在运行的节点（前置/设置变化后调用）。
func (a *App) RestartAll() {
	a.mu.RLock()
	ids := make([]string, 0)
	for id, s := range a.servers {
		if s.Running() {
			ids = append(ids, id)
		}
	}
	a.mu.RUnlock()
	if len(ids) == 0 {
		return
	}
	a.StopAll()
	if err := a.Activate(ids); err != nil {
		a.Logf("重启节点时出错：%v", err)
	}
}

// AutoActivate 按配置在启动后自动激活上次处于激活状态的节点。
func (a *App) AutoActivate() {
	a.mu.RLock()
	want := a.cfg.Settings.AutoActivateOnLaunch
	ids := make([]string, 0)
	for _, n := range a.cfg.Nodes {
		if n.Active {
			ids = append(ids, n.ID)
		}
	}
	a.mu.RUnlock()
	if !want || len(ids) == 0 {
		return
	}
	go func() {
		if !a.sleepCtx(300 * time.Millisecond) {
			return
		}
		a.Logf("启动时自动激活上次处于激活状态的节点（%d 个）", len(ids))
		if err := a.Activate(ids); err != nil {
			a.Logf("自动激活出错：%v", err)
		}
	}()
}

// ------------------------------------------------------------------
// 单个节点的启停
// ------------------------------------------------------------------

func (a *App) startNode(id string) error {
	a.mu.Lock()
	idx := a.indexOfLocked(id)
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("节点不存在")
	}
	n := a.cfg.Nodes[idx]
	// 监听地址由 AllowLan 统一决定
	n.ListenHost = a.cfg.ListenHostEffective(n)

	frontCfg, err := a.resolveFrontLocked(n)
	if err != nil {
		a.setErrLocked(id, err.Error())
		a.mu.Unlock()
		return err
	}

	// 端口占用检查：端口是"本地代理端口"，被占了必须明确报出来。
	// 注意排除节点自己 —— 重启时它自己的端口当然算"自己占着"。
	//
	// 【审查 #28】探测（systemBusy 会真的 bind 一次）放在下面放锁之后做：
	// al 与 n 是在同一把锁下取的快照，所以"端口非法 / 被本项目别的节点占用"
	// 这一半仍然是**和配置读取一致**的原子判断（纯内存查表），
	// 只有"系统占用"那一半是锁外的系统调用。放锁窗口里配置若被改动，
	// 最终以 srv.Start() 的 bind 为准 —— 那时失败同样会报出来，不会静默生效。
	al := a.allocatorLocked(id)

	// 空闲超时来自全局设置（界面「网络设置 → 空闲自动断开」默认 300s）。
	// 【别再退回 node.Timeout()】那是拨号超时（固定 15s），拿它当空闲超时
	// 会让这个设置彻底失效，并且 15 秒静默就掐断链路。
	idle := a.cfg.Settings.IdleTimeout()
	a.mu.Unlock()

	if st := al.Check(n.ListenPort); !st.Free {
		a.mu.Lock()
		a.setErrLocked(id, st.Message)
		a.mu.Unlock()
		return fmt.Errorf("%s", st.Message)
	}

	srv, err := proxy.NewServer(n, frontCfg, idle, a.logfFor(n.Name))
	if err != nil {
		a.mu.Lock()
		a.setErrLocked(id, err.Error())
		a.mu.Unlock()
		return fmt.Errorf("构造失败: %w", err)
	}

	if err := srv.Start(); err != nil {
		a.mu.Lock()
		a.setErrLocked(id, err.Error())
		a.mu.Unlock()
		return err
	}

	// 【为什么必须先把旧服务摘出来、放锁之后才 Stop】
	// Stop() 会关监听、断开全部活动连接，并等内核把实例排空
	// （WireGuard 还要等收包协程退出、sing-box/mihomo 要等实例关闭），
	// 是一个**阻塞**调用。a.mu 是全局写锁，界面上的每一次读取
	// （节点列表、统计、端口自检）都得先拿它 —— 持着锁去 Stop，
	// 整个窗口就会跟着卡住，看起来就是"点了启动之后界面冻死"。
	//
	// 旧服务确实可能还在运行：从 startNode 读配置到抢锁发布之间没有锁保护，
	// 期间设置/节点可能已被改动或被另一次激活抢先发布，于是这里的
	// a.servers[id] 会是一个**监听地址与当前配置已经不同**、但仍在跑的实例。
	//
	// 【顺序】先在锁内发布新服务（任何时刻 map 里都是最新的那个），
	// 放锁后再停旧服务：
	//   - 反过来先停旧再发布，会给并发的 stopNode 留一个"map 里没有服务"
	//     的窗口，它停不掉任何东西，随后我们发布出去的新服务就没人管了
	//     （一直在监听、界面也停不掉）；
	//   - 先发布则不会漏掉新服务，而多停一次旧服务是无害的 ——
	//     Server.Stop 内部有 started 守卫，重复调用直接返回。
	a.mu.Lock()
	old := a.servers[id]
	a.servers[id] = srv
	delete(a.errs, id)
	a.mu.Unlock()

	if old != nil {
		_ = old.Stop()
	}

	a.Logf("「%s」已启动，监听 %s（前置：%s；出口：%s）",
		n.Name, srv.Addr(), a.DescribeFront(n), n.Describe())
	a.emit(EventNodes, "")
	return nil
}

func (a *App) stopNode(id string) error {
	a.mu.Lock()
	srv := a.servers[id]
	delete(a.servers, id)
	delete(a.errs, id)
	a.mu.Unlock()
	if srv != nil {
		_ = srv.Stop()
		a.Logf("「%s」已停止，端口 %d 已释放", srv.Name(), srv.ListenPort())
	}
	a.emit(EventNodes, "")
	return nil
}

func (a *App) restartNode(id string) error {
	_ = a.stopNode(id)
	return a.startNode(id)
}

// logfFor 给某条链路生成带名字前缀的日志函数。
func (a *App) logfFor(name string) func(string, ...any) {
	return func(format string, args ...any) {
		a.Logf("[%s] "+format, append([]any{name}, args...)...)
	}
}

func (a *App) setErrLocked(id, msg string) { a.errs[id] = msg }

// errOf 返回节点最近一次错误。
func (a *App) errOf(id string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.errs[id]
}

// ------------------------------------------------------------------
// 命名辅助
// ------------------------------------------------------------------

func (a *App) suggestNameLocked(protocol string) string {
	base := config.ProtocolLabel(protocol)
	return a.uniqueNameLocked(base)
}

func (a *App) uniqueNameLocked(base string) string {
	if strings.TrimSpace(base) == "" {
		base = "节点"
	}
	used := map[string]bool{}
	for _, n := range a.cfg.Nodes {
		used[n.Name] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; i < 10000; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !used[cand] {
			return cand
		}
	}
	return base + "-" + config.NewID()
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
