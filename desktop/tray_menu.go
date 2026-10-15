package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// 托盘菜单里那个「全局代理模式」勾选项的文案。
const proxyMenuLabel = "全局代理模式"

// setupProxyMenuItem 往托盘菜单里加一个勾选项，并把句柄登记到 API 上以便同步状态。
//
// 【为什么用勾选项而不是改文案】这一项只有开/关两态，标签固定不动、用勾选表达状态，
// 是 Windows 托盘菜单的惯例（也免得使用者每次都要读一遍标签猜"点下去是开还是关"）。
func (a *API) setupProxyMenuItem(menu *application.Menu) {
	item := menu.AddCheckbox(proxyMenuLabel, false)
	item.SetTooltip("全机流量经「全局节点」转发。需要先在节点右键菜单里设一个全局节点，并让它处于运行中。")
	item.OnClick(func(*application.Context) { a.toggleSystemProxyFromTray() })

	a.trayMu.Lock()
	a.proxyItem = item
	a.trayMenu = menu
	a.trayMu.Unlock()
	a.syncProxyMenuItem()
}

// proxyToggleTarget 是"点一下之后应该切成什么状态"：就是取反。
//
// 抽成函数是为了能被单测钉住 —— 这条规则写错，菜单点下去就永远不变（或永远变不回来）。
func proxyToggleTarget(currentlyOn bool) bool { return !currentlyOn }

// toggleSystemProxyFromTray 处理托盘点这一项。
func (a *API) toggleSystemProxyFromTray() {
	on := a.app != nil && a.app.Settings().SystemProxy
	if err := a.SetSystemProxyMode(proxyToggleTarget(on)); err != nil {
		// 【为什么不能只用 Errorf】GUI 构建是 -H windowsgui（没有控制台），
		// Errorf 只写 stderr，等于什么都没发生 —— 使用者点一下没反应，只能干瞪眼。
		// 所以这里写日志（data\picohop.log）+ 弹一条通知，说清为什么点不动。
		a.logf("托盘：全局代理模式切换失败：%v", err)
		if a.app != nil {
			a.app.Notify("error", "全局代理模式没能切换", err.Error())
		}
		a.syncProxyMenuItem() // 失败时把勾选态掰回现实
	}
}

// syncProxyMenuItem 让菜单的勾选态与"设置里到底开没开"一致。
//
// 【为什么不在点击处就地改】这个状态的来源不止托盘菜单一处：以后可能加界面开关，
// 现在还有自测入口（-open sysproxyon / sysproxyoff）。只在点击处改，别的路径一动，
// 菜单就会显示成过期状态 —— 那比没有这一项更糟。所以统一由这里同步，
// 而它被 syncProxyUi 在"启动时"与"切换成功之后"各调一次。
func (a *API) syncProxyMenuItem() {
	a.trayMu.Lock()
	item, menu := a.proxyItem, a.trayMenu
	a.trayMu.Unlock()
	if item == nil {
		return
	}
	on := a.app != nil && a.app.Settings().SystemProxy
	item.SetChecked(on)
	if menu != nil {
		// 勾选态要推给平台才会重画（Menu.Update 在应用还没跑起来时会自己返回）
		menu.Update()
	}
}

// syncProxyUi 把「全局代理模式」这件事在所有外壳表现上同步一遍：
// 托盘图标的底色 + 托盘菜单的勾选态。
//
// 只有这一处入口，就不会出现"图标变了、菜单没变"这种半拉子状态。
func (a *API) syncProxyUi() {
	a.refreshTrayMode()
	a.syncProxyMenuItem()
}
