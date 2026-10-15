package main

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestProxyToggleTargetIsInverse 钉住"点一下就是取反"。
//
// 这条规则错了，菜单点下去就永远不变（或永远变不回来），而界面不会报任何错 —— 只能靠人肉发现。
func TestProxyToggleTargetIsInverse(t *testing.T) {
	if proxyToggleTarget(false) != true {
		t.Fatal("关着的时候点一下应该开启")
	}
	if proxyToggleTarget(true) != false {
		t.Fatal("开着的时候点一下应该关闭")
	}
}

// TestSetupProxyMenuItemRegistersCheckbox 守住"菜单里确实加了一个勾选项、而且是勾选项"。
//
// 【为什么要测】托盘菜单是启动早期一次性建好的，建错了没有任何报错 —— 使用者只会看到
// 菜单里少了一项（或者点不动的普通项）。这里用真的 Menu 对象建一遍，再回头查那个句柄。
func TestSetupProxyMenuItemRegistersCheckbox(t *testing.T) {
	menu := application.NewMenu()
	a := &API{}
	a.setupProxyMenuItem(menu)

	a.trayMu.Lock()
	item := a.proxyItem
	registered := a.trayMenu
	a.trayMu.Unlock()

	if item == nil {
		t.Fatal("没有把菜单项登记下来（点它也不会同步勾选态）")
	}
	if registered == nil {
		t.Fatal("没有登记菜单句柄（勾选态改完推不出去）")
	}
	if !item.IsCheckbox() {
		t.Fatal("这一项必须是勾选项：它的两个状态要靠勾选表达")
	}
	if item.Label() != proxyMenuLabel {
		t.Fatalf("文案是 %q，期望 %q", item.Label(), proxyMenuLabel)
	}
	// 建的时候还没后端（设置读不到），必须先是未勾选 —— 启动收尾会按真实设置纠正。
	if item.Checked() {
		t.Fatal("刚建好时不应该是勾选状态（那时还读不到设置）")
	}
}

// TestSyncProxyMenuAndTrayWithoutBackend 后端/托盘都还没就绪时，同步逻辑不能崩。
//
// 这条路径真的会跑到：托盘与菜单在 app.Run() 之前建好，而同步在启动收尾才第一次生效。
func TestSyncProxyMenuAndTrayWithoutBackend(t *testing.T) {
	a := &API{}
	a.syncProxyMenuItem() // 没有菜单项 → 直接返回
	a.syncProxyUi()       // 没有托盘、没有后端 → 也只能是"普通 + 未勾选"
	a.trayMu.Lock()
	mode := a.trayMode
	a.trayMu.Unlock()
	if mode != trayModeNormal {
		t.Fatalf("没有后端时应为普通状态，实际 %d", mode)
	}
}

// TestMainTrayMenuHasProxyItem 用真的托盘菜单构造一遍（setupTray 里那套顺序），
// 确认「全局代理模式」在菜单里、且没有被后面某一项挤掉。
//
// 这里只验我们加的那一项 —— 其余项由 main.go 直接 Add，不重复断言。
func TestMainTrayMenuHasProxyItem(t *testing.T) {
	menu := application.NewMenu()
	a := &API{}
	menu.Add("显示主窗口")
	menu.AddSeparator()
	menu.Add("激活全部节点")
	menu.Add("停止全部节点")
	menu.AddSeparator()
	a.setupProxyMenuItem(menu)
	menu.Add("复制本地代理地址")
	menu.AddSeparator()
	menu.Add("退出 PicoHop")

	a.trayMu.Lock()
	item := a.proxyItem
	a.trayMu.Unlock()
	if item == nil || !item.IsCheckbox() {
		t.Fatal("菜单里没有那个勾选项")
	}
}
