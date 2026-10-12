package app

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"chainproxy/internal/config"
	"chainproxy/internal/proxy"
	"chainproxy/internal/tester"
)

// 可执行的测试种类（与主流客户端的右键菜单对齐）。
const (
	TestTCP   = "tcp"   // TCP 延迟：直连服务器地址做一次握手
	TestReal  = "real"  // 真连接延迟：走完整链路请求一次 generate_204
	TestSpeed = "speed" // 速度：走完整链路下载若干秒
	TestExit  = "exit"  // 出口 IP：走完整链路查一次出口 IP
)

// TestNodes 对若干节点执行指定测试。
//
// 测试全部在后台跑，结果写回节点配置（延迟/速度）并广播事件；
// 同一时刻只允许一批测试在跑，避免把带宽和 CPU 互相打满导致数据失真。
func (a *App) TestNodes(ids []string, kind string) error {
	if len(ids) == 0 {
		return fmt.Errorf("请先选择要测试的节点")
	}
	switch kind {
	case TestTCP, TestReal, TestSpeed, TestExit:
	default:
		return fmt.Errorf("未知的测试类型 %q", kind)
	}

	a.testMu.Lock()
	if a.testRunning {
		a.testMu.Unlock()
		return fmt.Errorf("已有测试任务正在进行，请等它结束或先停止")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.testCancel = cancel
	a.testRunning = true
	a.testMu.Unlock()

	// 把节点快照出来，测试期间用户改了配置也不影响本次结果。
	type job struct {
		node config.Node
	}
	jobs := make([]job, 0, len(ids))
	for _, id := range ids {
		if n, ok := a.GetNode(id); ok {
			jobs = append(jobs, job{node: n})
		}
	}

	conc := a.Settings().TestConcurrency
	if conc < 1 {
		conc = 8
	}

	go func() {
		defer func() {
			a.testMu.Lock()
			a.testRunning = false
			a.testCancel = nil
			a.testMu.Unlock()
			a.persistStats(true)
			a.emit(EventNodes, "")
		}()

		a.Logf("开始测试：%s（%d 个节点，并发 %d）", testLabel(kind), len(jobs), conc)

		sem := make(chan struct{}, conc)
		var wg sync.WaitGroup
		// 【这个 label 不是装饰，别删】裸 `break` 只跳出它所在的那个 select，
		// for 会**继续**把剩下的节点一个个派发出去：ctx 已经取消，这些测试必然
		// 失败，却仍会被 updateTest 写成"连不上 + 延迟清零" —— 用户点了"停止"，
		// 结果反而把没测的节点全判成了无效项（审查 #14）。必须 break 到 label，
		// 一次结束整轮派发。
		//
		// 取消检查合并进"等并发额度"这一句（用 select 而不是裸 `sem <- ...`）：
		// 额度满时循环正是阻塞在这里，取消后不该再多跑一个节点。
	dispatch:
		for i, j := range jobs {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				a.Logf("测试已取消：不再派发剩余 %d 个节点", len(jobs)-i)
				break dispatch
			}
			wg.Add(1)
			go func(n config.Node) {
				defer wg.Done()
				defer func() { <-sem }()
				// 【必须写在 defer 里】recover() 只有被 defer 的函数**直接**调用才有效；
				// 写在 goroutine 体里恒返回 nil —— 那样单个节点 panic 会带走整个进程（GUI 直接消失）。
				defer func() {
					if r := recover(); r != nil {
						a.Logf("测试「%s」时发生异常：%v", n.Name, r)
					}
				}()
				a.runOneTest(ctx, n, kind)
			}(j.node)
		}
		wg.Wait()
		cancel()
	}()

	return nil
}

// CancelTests 停止当前测试任务。
func (a *App) CancelTests() {
	a.testMu.Lock()
	cancel := a.testCancel
	running := a.testRunning
	a.testMu.Unlock()
	if running && cancel != nil {
		cancel()
		a.Notify("warning", "已停止测试", "当前测试任务已被取消")
	}
}

// Testing 返回是否正在测试。
func (a *App) Testing() bool {
	a.testMu.Lock()
	defer a.testMu.Unlock()
	return a.testRunning
}

func testLabel(kind string) string {
	switch kind {
	case TestTCP:
		return "TCP 延迟"
	case TestReal:
		return "真连接延迟"
	case TestSpeed:
		return "速度"
	case TestExit:
		return "出口 IP"
	default:
		return kind
	}
}

// runOneTest 执行单个节点的一项测试，并把结果写回配置。
func (a *App) runOneTest(ctx context.Context, n config.Node, kind string) {
	s := a.Settings()

	// 【为什么写回要包一层】测试被取消（用户点"停止"）之后，链路上的一切都会
	// 立刻失败（TCP 握手拿到的是 context canceled），但那**不是节点的结论**：
	// 原样写回会把节点标成"连不上" —— 紧接着「移除测试无效项」就会删掉它 ——
	// 同时把已经测出来的延迟/速度用 0 覆盖。所以取消后一律不写：宁可不写，
	// 也不能写错（审查 #14）。
	//
	// 【判据是 ctx.Err()，不是"这次测试失败了"】失败路径里本来就有一部分是
	// "记成功/清结论"（真连接、测速、出口 IP 失败时不代表节点不可用，见各分支
	// 注释）；只有"取消"才是整批结果作废的信号，别把两者混起来。
	//
	// m 里的 ok 由调用方显式给出"本次到底有没有测出数值"：失败路径统一传零值
	// （m.ok == false），updateTest 据此**保留上次的有效数值**而不是写 0（审查 #19）。
	write := func(unreachable bool, m measured) {
		if ctx.Err() != nil {
			return
		}
		a.updateTest(n.ID, kind, unreachable, m)
	}

	switch kind {
	case TestTCP:
		timeout := time.Duration(s.TCPTestTimeoutMs) * time.Millisecond
		start := time.Now()
		d, err := tester.TCPPing(ctx, n.Server, n.Port, timeout)
		if err != nil {
			a.Logf("「%s」TCP 延迟测试失败：%v", n.Name, err)
			write(true, measured{})
			return
		}
		a.Logf("「%s」TCP 延迟 %d ms（%d ms 内完成）", n.Name, d.Milliseconds(), time.Since(start).Milliseconds())
		write(false, measured{ok: true, ms: d.Milliseconds()})
		return
	}

	// 其余三种都要真正把链路建起来
	chain, err := a.openTestChain(ctx, n)
	if err != nil {
		a.Logf("「%s」%s测试失败：无法建立链路：%v", n.Name, testLabel(kind), err)
		write(true, measured{})
		return
	}
	defer chain.Close()

	dial := func(c context.Context, network, addr string) (net.Conn, error) {
		return chain.Dial(c, network, addr)
	}

	switch kind {
	case TestReal:
		url := s.RealTestURL
		if strings.TrimSpace(url) == "" {
			url = "http://cp.cloudflare.com/generate_204"
		}
		d, err := tester.RealPing(ctx, dial, url, 15*time.Second)
		if err != nil {
			a.Logf("「%s」真连接延迟测试失败：%v", n.Name, err)
			// 链路已经建起来了 —— 请求失败说明不了节点无效（可能是目标站点
			// 不可达/被墙），所以这里记"不算无效"，把之前可能的失败结论清掉。
			// 但**没有测出数值**，因此不碰 RealMs：上一次测出来的延迟要留着，
			// 只是标记成"最近一次延迟测试没测出数值"（审查 #19）。
			write(false, measured{})
			return
		}
		a.Logf("「%s」真连接延迟 %d ms", n.Name, d.Milliseconds())
		write(false, measured{ok: true, ms: d.Milliseconds()})

	case TestSpeed:
		url := s.SpeedTestURL
		secs := s.SpeedTestSeconds
		mbps, total, err := tester.Speed(ctx, dial, url, secs, 15*time.Second)
		if err != nil {
			a.Logf("「%s」速度测试失败：%v", n.Name, err)
			// 同上：链路通了就算"节点有效"，测速失败与节点是否可用无关，
			// 也因此不该把上一次测出来的速度写成 0。
			write(false, measured{})
			return
		}
		a.Logf("「%s」速度 %.2f MB/s（%s 用时约 %d 秒）", n.Name, mbps, FormatBytes(total), secs)
		write(false, measured{ok: true, speed: mbps})

	case TestExit:
		res := tester.FetchIP(ctx, dial, "https://api.ipify.org?format=json", n.Name)
		if !res.OK {
			a.Logf("「%s」出口 IP 探测失败：%v", n.Name, res.Err)
			// 同上：出口 IP 查不到 ≠ 节点不可用（查询服务本身也可能不通）。
			write(false, measured{})
			return
		}
		a.Logf("「%s」出口 IP = %s（%d ms）", n.Name, res.IP, res.Latency.Milliseconds())
		write(false, measured{ok: true, exitIP: res.IP})
	}
}

// chain 是一条用于测试的临时链路。
type chain struct {
	front proxy.Front
	up    proxy.Upstream
}

func (c *chain) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if network == "udp" {
		return nil, fmt.Errorf("测试链路暂不支持 UDP")
	}
	return c.up.Dial(ctx, c.front, addr)
}

func (c *chain) Close() {
	if c.up != nil {
		_ = c.up.Stop()
	}
	if c.front != nil {
		_ = c.front.Close()
	}
}

// openTestChain 按节点的前置配置临时建一条链路（不占用本地端口）。
//
// 这样"没激活也能测"，用户可以先测完再决定要不要激活。
func (a *App) openTestChain(ctx context.Context, n config.Node) (*chain, error) {
	frontCfg, err := a.resolveFront(n)
	if err != nil {
		return nil, err
	}
	front := proxy.NewFront(frontCfg)
	up, err := proxy.NewUpstream(n, frontCfg, a.logfFor(n.Name))
	if err != nil {
		front.Close()
		return nil, err
	}
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := up.Start(startCtx, front); err != nil {
		front.Close()
		return nil, err
	}
	return &chain{front: front, up: up}, nil
}

// measured 是一次测试**实际测出来的数值**。
//
// 【为什么必须有一个显式的 ok，不能拿"数值是不是 0"去猜】（审查 #19）失败路径
// 以前统一传 0，updateTest 又一律写回，于是"这次没测出数值"被记成了"测出来就是
// 0" —— 上一次的有效延迟/速度当场被抹掉，界面也从此分不清"没测过"和"测过但失败"。
// 而光看 0 是分不清的：一次**成功**但耗时不足 1ms 的测试同样是 0 ms（本机/局域网
// 节点很常见，见审查 #8）。所以这里由调用方显式声明本次到底有没有得到数值。
type measured struct {
	ok     bool    // 本次是否真的测出了数值（false = 失败/无结果）
	ms     int64   // 延迟（TCP / 真连接）
	speed  float64 // 速度
	exitIP string  // 出口 IP
}

// updateTest 把测试结果写回节点并广播。
//
// unreachable 是"本次测试明确证明这个节点连不上"，**必须由调用方显式给出**：
// 绝不能从 latencyMs == 0 反推失败 —— 用「真连接延迟」测时结果写进 RealMs、
// LatencyMs 恒为 0，而一次成功但耗时不足 1ms 的测试同样是 0（审查 #8 就是
// 靠这个反推把全部节点判成了无效项）。
//
// m 是本次**真正测出来的数值**（m.ok == false 表示这次没测出数值）。
//
// 【失败不清零】（审查 #19）以前失败也走同一条写回路径，于是"连不上 + 延迟 0"
// 会把上一次测出来的有效延迟/速度一起抹掉，而且抹得无声无息：界面上既看不到
// 旧值、也分不清"没测过"和"测过但失败"。现在数值只在 m.ok 时才覆盖，并各自
// 记下测量时间（LatencyAt / SpeedAt）；失败只更新"最近一次测试的结论"
// （TestFailed / TestedAt / TestedAddr）与该族的失败标记
// （LatencyFailed / SpeedFailed）。界面据此分三态：没测过 / 有效 / 上次成功值
// （已过期），见 config.Node 那四个字段的注释。
//
// 失败结论与时间戳仍然按"谁最后测谁覆盖"更新：一次成功的测试必须把上一次的
// 失败结论清掉，否则一个已经恢复的节点会一直被当成无效项。
func (a *App) updateTest(id, kind string, unreachable bool, m measured) {
	now := time.Now()
	a.mu.Lock()
	idx := a.indexOfLocked(id)
	if idx >= 0 {
		node := &a.cfg.Nodes[idx]
		node.TestFailed = unreachable
		node.TestedAt = now
		// 记下"这次结论是对哪个地址得出的"，供 KnownUnreachable 挡掉
		// "改了服务器地址之后旧结论还被拿来删节点"。
		node.TestedAddr = node.TestKey()
		switch kind {
		case TestTCP:
			// LatencyKind 只在真的测出数值时才改：没测出来的话，界面上该继续
			// 显示上一次成功测出的那个值（并标成过期），而不是把这一列清空。
			if m.ok {
				node.LatencyMs = m.ms
				node.LatencyKind = "tcp"
				node.LatencyAt = now
				node.LatencyFailed = false
			} else {
				node.LatencyFailed = true
			}
		case TestReal:
			if m.ok {
				node.RealMs = m.ms
				node.LatencyKind = "real"
				node.LatencyAt = now
				node.LatencyFailed = false
			} else {
				node.LatencyFailed = true
			}
		case TestSpeed:
			if m.ok {
				node.SpeedMBps = m.speed
				node.SpeedAt = now
				node.SpeedFailed = false
			} else {
				node.SpeedFailed = true
			}
		case TestExit:
			// 【出口 IP 刻意不做"上次成功值"】它是"当前的实际出口"，探测失败时
			// 保留旧值比没有更危险 —— 用户会按一个过期的 IP 判断自己在哪落地。
			// 所以这一族仍然清理，与延迟/速度的处理**故意不同**。
			if a.exitIPs == nil {
				a.exitIPs = map[string]string{}
			}
			if m.exitIP == "" {
				delete(a.exitIPs, id)
			} else {
				a.exitIPs[id] = m.exitIP
			}
		}
		a.dirty = true
	}
	a.mu.Unlock()
	a.emit(EventTestDone, id+"\x00"+kind)
}

// TestFront 直接经全局前置代理访问公网 IP 查询服务，返回前置的出口 IP。
func (a *App) TestFront(ctx context.Context) tester.Result {
	a.mu.RLock()
	fc := a.cfg.Front
	a.mu.RUnlock()

	if fc.Type == config.FrontTypeDirect {
		return tester.Result{Via: "直连", Err: fmt.Errorf("全局前置代理类型是「直连」，没有可测试的对象")}
	}
	front := proxy.NewFront(fc)
	defer front.Close()
	res := tester.FetchIP(ctx, func(c context.Context, network, addr string) (net.Conn, error) {
		return front.DialTCP(c, addr)
	}, "https://api.ipify.org?format=json", "全局前置代理 "+front.Describe())

	if res.OK {
		a.Logf("全局前置代理出口 IP = %s（%d ms）", res.IP, res.Latency.Milliseconds())
	} else {
		a.Logf("全局前置代理测试失败：%v", res.Err)
	}
	return res
}

// ExitIPs 返回各节点最近一次探测到的出口 IP。
func (a *App) ExitIPs() map[string]string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[string]string, len(a.exitIPs))
	for k, v := range a.exitIPs {
		out[k] = v
	}
	return out
}

// InvalidPreview 是「移除测试无效项」的预览结果：界面据此弹确认框。
//
// 【为什么要分成"预览 + 按名单删"两步】这是不可撤销的批量删除，界面必须
// 先把"要删几个、叫什么"摆给用户看。所以后端不再"自己挑一批就删"，而是
// 把名单交给界面，用户确认后原样送回来 —— 用户看到的名单就是被删的名单。
type InvalidPreview struct {
	IDs   []string `json:"ids"`
	Names []string `json:"names"`

	// Untested / Stale 是被**排除**的两类，计数给界面解释"为什么一个都没删"。
	// 它们不是"无效项"，只是"没有证据"。
	Untested int `json:"untested"` // 从没测过
	Stale    int `json:"stale"`    // 测过且失败，但结论已经不算数（过期，或地址被改过）

	// StaleAfterHours 是结论有效期（小时）。让界面直接引用后端的数字，
	// 免得 24 这个值在前端再抄一份、改一处漏一处。
	StaleAfterHours int `json:"staleAfterHours"`
}

// PreviewInvalidNodes 列出"测试无效"的节点：最近一次测试明确证明连不上，
// 且结论仍然有效（见 config.Node.KnownUnreachable）。只读，不动配置。
//
// 【判据为什么不是 latencyMs == 0】见 updateTest 与 config.Node 的注释：
// 那个判据会把从没测过的、以及用真连接延迟测通过的节点一起算成无效项。
func (a *App) PreviewInvalidNodes() InvalidPreview {
	now := time.Now()
	out := InvalidPreview{
		IDs:             []string{},
		Names:           []string{},
		StaleAfterHours: int(config.TestResultTTL.Hours()),
	}

	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, n := range a.cfg.Nodes {
		if n.KnownUnreachable(now) {
			out.IDs = append(out.IDs, n.ID)
			out.Names = append(out.Names, n.Name)
			continue
		}
		// 没被选中的要分清原因：界面靠这两个计数给出"为什么没删"的解释。
		// Stale 包含"结论过期"与"结论对应的地址已被改过"两种 —— 都不是删除依据。
		if n.TestedAt.IsZero() {
			out.Untested++
		} else if n.TestFailed {
			out.Stale++
		}
	}
	return out
}

// RemoveInvalidNodes 删除这些节点（ids 来自 PreviewInvalidNodes）。
//
// 删除前**按当前配置再核一遍**：确认框还开着的时候测试可能刚好跑通、或者
// 用户把服务器地址改掉了 —— 那些节点这时就不该删。宁可少删、绝不删错：
// 删除不可撤销，而"少删了一个"用户再点一次就行。
func (a *App) RemoveInvalidNodes(ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}

	now := time.Now()
	a.mu.RLock()
	bad := make([]string, 0, len(ids))
	for _, n := range a.cfg.Nodes {
		if want[n.ID] && n.KnownUnreachable(now) {
			bad = append(bad, n.ID)
		}
	}
	a.mu.RUnlock()

	if len(bad) == 0 {
		return 0, nil
	}
	if err := a.DeleteNodes(bad); err != nil {
		return 0, err
	}
	return len(bad), nil
}
