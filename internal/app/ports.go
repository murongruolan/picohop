package app

import (
	"strconv"

	"chainproxy/internal/config"
	"chainproxy/internal/ports"
)

// allocatorLocked 构造一个端口分配器，并把"本项目已经占用的端口"填进去。
//
// excludeID 用于编辑/重启场景：**被编辑或被重启的那个节点自己的端口
// 不应该算作"被别人占用"**，否则每次重启都会误报端口冲突。
//
// 调用方必须已持有 a.mu（读锁或写锁）。
func (a *App) allocatorLocked(excludeID string) *ports.Allocator {
	al := ports.New()
	al.Host = "127.0.0.1"
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
