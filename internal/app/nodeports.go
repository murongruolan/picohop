package app

import (
	"strings"

	"github.com/murongruolan/picohop/internal/proxy"
)

// UnassignNodePorts 把若干节点的本地端口「置于未分配」：先停掉正在跑的，再把端口清成 0。
//
// 【为什么要先停】端口为 0 的节点起不来（net.Listen 遇到 0 会随便挑一个临时端口，
// 审查 #21 记过这个坑），所以拿掉端口之前必须先把服务停掉；否则会留下
// "激活中却没有端口"的坏状态，配置校验会直接报这个错。
//
// 返回真正被改动的节点数（本来就是未分配的重复操作不会计数）。
func (a *App) UnassignNodePorts(ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}

	// 先停掉正在运行的（与 DeleteNodes 同一套路：Stop 不持锁）
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
	count := 0
	var names []string
	for i := range a.cfg.Nodes {
		id := a.cfg.Nodes[i].ID
		if !want[id] {
			continue
		}
		if a.cfg.Nodes[i].ListenPort == 0 && !a.cfg.Nodes[i].Active {
			continue // 本来就是"未分配"，不重复计数
		}
		a.cfg.Nodes[i].ListenPort = 0
		a.cfg.Nodes[i].Active = false
		delete(a.servers, id)
		delete(a.errs, id)
		count++
		names = append(names, a.cfg.Nodes[i].Name)
	}
	a.mu.Unlock()

	if count == 0 {
		return 0, nil
	}
	a.Logf("已把 %d 个节点的本地端口置于未分配：%s", count, strings.Join(names, "、"))
	a.emit(EventNodes, "")
	return count, a.Save()
}
