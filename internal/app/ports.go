package app

import (
	"errors"
	"strconv"

	"github.com/murongruolan/picohop/internal/config"
	"github.com/murongruolan/picohop/internal/ports"
)

// portProbe 只在测试里被赋值（见 ports_lock_test.go），生产恒为 nil（真去 bind）。
//
// 与 netlog.go 里的 timeNow 同一个做法：把不可控的副作用抽成可替换的变量，
// 于是"探测正在进行"变成一个可观测的事实 —— 审查 #28 的回归测试靠它卡住探测，
// 再去 TryLock 全局锁，断言探测没有发生在锁内。
var portProbe func(host string, port int) bool

// maxPortRaceRetries 是"锁外探测 + 锁内复查"被并发抢占时的重试上限。
//
// 抢占窗口只有"取快照到复查"之间那几微秒，连续 8 次都撞上在现实中不会发生；
// 真撞上了就 fail-closed 报错让用户重试一次 —— 绝不把一个**没复查过**的端口
// 写进配置。
const maxPortRaceRetries = 8

// errNoFreePort 表示 ports.Allocator.Next() 扫完候选也没找到空闲端口。
//
// 只当哨兵用：具体文案由调用方给，因为新增和克隆要提示的"下一步动作"不一样。
var errNoFreePort = errors.New("本机没有可用的本地端口（已扫到 65535）")

// errPortRace 表示候选端口在锁外探测期间被并发的写操作抢走了，且重试已用尽。
var errPortRace = errors.New("端口分配被并发操作反复抢占，请重试")

// allocatorLocked 构造一个端口分配器，并把"本项目已经占用的端口"填进去。
//
// excludeID 用于编辑/重启场景：**被编辑或被重启的那个节点自己的端口
// 不应该算作"被别人占用"**，否则每次重启都会误报端口冲突。
//
// 调用方必须已持有 a.mu（读锁或写锁）。
func (a *App) allocatorLocked(excludeID string) *ports.Allocator {
	al := ports.New()
	al.Host = "127.0.0.1"
	al.Probe = portProbe
	al.Start = a.cfg.Settings.PortStart
	if al.Start <= 0 {
		al.Start = ports.DefaultStart
	}
	for _, n := range a.cfg.Nodes {
		if n.ID == excludeID {
			continue
		}
		al.MarkProject(n.ListenPort, n.Name)
	}
	// 全局前置代理也占着一个端口（通常是本机的主流客户端10808），
	// 本地端口撞上它会造成"自己拨自己"的环路，必须避开。
	if a.cfg.Front.Type != config.FrontTypeDirect && a.cfg.Front.Host == "127.0.0.1" {
		al.MarkProject(a.cfg.Front.Port, "全局前置代理")
	}
	return al
}

// allocPort 在**不持有 a.mu** 的前提下挑一个空闲端口，最后在锁内把它定下来。
//
// 【审查 #28】老写法是持着 a.mu（全局写锁，界面每一次读取都要抢它）调
// Allocator.Next()，而 Next() 最坏要对 512 个候选各做一次 net.Listen；
// 系统调用一攒起来（端口密、机器忙），整个窗口就跟着冻住 ——
// 看起来就是"点了新增之后界面卡了"。所以这里拆成三步：
//
//  1. 锁内取"占用快照"（纯内存，见 allocatorLocked）；
//  2. 放锁，在快照上探测候选端口（net.Listen 全在这一步）；
//  3. 重新持锁，把候选和**最新**的占用表比一次（还是纯内存），没人抢就交给
//     commit 落盘。
//
// 第 3 步的复查不能省：第 2 步放锁期间，另一个新增/克隆可能已经挑走了同一个
// 端口；复查正是为了不把"两个节点共用一个端口"写进配置。
// 【别再改成复查时重新探测一次系统占用】那等于把 net.Listen 又搬回了锁里，
// 本缺陷就白修了；系统占用那一半在第 2 步刚查过，复查只需看占用表。
//
// commit 在**持锁**状态下被调用，负责把端口写进配置 —— 其中必须包含
// "让占用表能看见这个端口"的那一步（通常就是把自己追加进 a.cfg.Nodes）。
func (a *App) allocPort(excludeID string, commit func(port int)) (int, error) {
	for attempt := 0; attempt < maxPortRaceRetries; attempt++ {
		snap := a.Allocator(excludeID)
		port := snap.Next() // 探测在锁外
		if port == ports.NoPort {
			return ports.NoPort, errNoFreePort
		}

		a.mu.Lock()
		if _, taken := a.allocatorLocked(excludeID).Project[port]; !taken {
			commit(port)
			a.mu.Unlock()
			return port, nil
		}
		a.mu.Unlock()
	}
	return ports.NoPort, errPortRace
}

// Allocator 返回端口分配器（界面需要自查时用）。
func (a *App) Allocator(excludeID string) *ports.Allocator {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.allocatorLocked(excludeID)
}

// PortStatus 检测端口是否可用，返回给界面直接显示的结果。
func (a *App) PortStatus(port int, excludeID string) ports.Status {
	return a.Allocator(excludeID).Check(port)
}

// SuggestPort 返回"下一个默认端口"。
//
// 规则：从 settings.portStart(默认 10810) 起排，
// 取本项目内所有 >= 10800 的端口最大值 + 1，再顺延到真正空闲的那个。
//
// 【审查 #21】顺延不到空闲端口时返回 ports.NoPort(0)，**不返回没验证过的候选**。
// 界面拿到 0 会走"请填写本地代理端口"的提示分支（见 NodeDialog.checkPort），
// 属于 fail-closed：宁可让用户手填，也不给他一个必然起不来的端口。
func (a *App) SuggestPort(excludeID string) int {
	return a.Allocator(excludeID).Next()
}

// RandomPort 随机一个空闲端口。
//
// 失败时返回 ports.ErrNoFreePort，界面据此显示
// "多次随机仍未随机到空闲端口"。
func (a *App) RandomPort(excludeID string) (int, error) {
	return a.Allocator(excludeID).RandomFree()
}

// UsedPorts 返回"本项目已占用的端口 -> 占用它的节点名"。
func (a *App) UsedPorts(excludeID string) map[string]string {
	al := a.Allocator(excludeID)
	out := make(map[string]string, len(al.Project))
	for p, name := range al.Project {
		out[strconv.Itoa(p)] = name
	}
	return out
}
