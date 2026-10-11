// Command desktop 是 ChainProxy 的图形界面入口（Wails v3 + Vue 3）。
//
// 界面层的职责边界很清晰：它只通过 API（见 api.go）与后端交互，
// 不直接碰 proxy / engine / config 这些包 —— 协议知识只存在于后端一处。
//
// # 从 v2 迁到 v3 的要点
//
// v2 是"单窗口 + 声明式"：一个 wails.Run(options.App{...}) 把应用、那唯一一个窗口、
// 事件循环全包在一起，没有创建第二个窗口的 API。v3 改成过程式：
// application.New() → app.Window.NewWithOptions(...) → app.Run()，
// 窗口变成一等对象，要几个开几个 —— 这是"对话框能飘到主窗口之外"的前提。
//
// 另外两处容易踩的差别：
//   - v3 的前端桥是 npm 包 @wailsio/runtime（Call.ByName / Events.On），
//     不再有 v2 那种 window.go.main.API.* 的全局对象。
//   - 构建不需要 wails3 CLI / Taskfile：官方 Windows 任务真正的命令就是一条 go build
//     （带 production 标签），见 scripts/build.ps1。
package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"chainproxy/internal/config"

	// 注册两种出口实现（proxy 通过注册函数解耦，见各自的 register.go）
	_ "chainproxy/internal/engine"
	_ "chainproxy/internal/wgproxy"
)

//go:embed all:frontend/dist
var assets embed.FS

// 托盘图标必须嵌进二进制：托盘是"关窗后仍然活着"的那部分，
// 如果图标去读磁盘文件，改天文件不在就变成空白托盘，很难查。
//
//go:embed build/windows/icon.ico
var trayIcon []byte

// 主窗口的名字。托盘菜单与全局快捷键都靠它把窗口找回来。
const mainWindowName = "main"

func main() {
	// 【开发期自测用】-open <对话框名> 让界面启动后直接把某个面板打开。
	//
	// 存在的意义：要检查对话框的排版就得真的打开它，而"模拟鼠标点击"这种
	// 自动化手段会去动用户的鼠标、甚至在焦点丢失时点到别的程序上（实测踩过）。
	// 用这个参数让界面自己打开面板，是纯只读、零副作用的做法。
	//
	// 取值：addnode / edit / settings / import / logs / share / portonly /
	//       contextmenu / notify / columns / enterkey / resize / dialogdrag
	api := NewAPI(parseBootFlag("open"))
	notif := notifications.New()

	wapp := application.New(application.Options{
		Name:        "ChainProxy",
		Description: "链式代理",
		Services: []application.Service{
			// API 走 ServiceStartup/ServiceShutdown 钩子（见 api.go）
			application.NewService(api),
			// 系统通知：后端在"节点掉线""已收进托盘"这类场景下发真正的 Windows 通知
			application.NewService(notif),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Windows: application.WindowsOptions{
			// WebView2 的浏览器配置目录默认落在 %APPDATA%\<程序名>，那是项目目录之外。
			// 本项目的规矩是"一切都在 F:\chain_proxy 内"，所以显式指到数据目录下。
			// 顺带的好处：清数据时删掉 data\ 就够了，不会在用户漫游配置里留残留。
			WebviewUserDataPath: filepath.Join(config.DataDir(), "webview"),

			// 关掉"最后一个窗口关闭就退出"：这是托盘常驻的前提。
			// 真正的退出走托盘菜单的"退出"，或者界面上的退出按钮（api.Quit）。
			DisableQuitOnLastWindowClosed: true,
		},
	})
	api.attach(wapp, notif)

	// 刻意做成中等尺寸：这是个"看一眼、点两下"的工具，
	// 摊满整个屏幕反而难用。信息密度靠列表的列显示来控制。
	mainWin := wapp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             mainWindowName,
		Title:            mainWindowTitle,
		Width:            1180,
		Height:           740,
		MinWidth:         920,
		MinHeight:        580,
		BackgroundColour: application.NewRGB(20, 22, 28),
		URL:              "/",
		Windows: application.WindowsWindow{
			// 标题栏明暗跟着设置的初始值走。运行期再改主题由 api.SetWindowTheme
			// 直接调 Win32 的 DwmSetWindowAttribute 处理（v3 没有运行期 setter）。
			Theme: api.initialWindowTheme(),
			// 保留系统自带的窗口阴影与圆角。
			// 不要透明/亚克力：会让 Element Plus 的暗色主题发灰、文字发虚。
			DisableFramelessWindowDecorations: false,
		},
	})
	api.setMainWindow(mainWin)

	// 点右上角的 × 不退出，而是收进托盘 —— 代理是要常驻的，
	// 手一抖关掉窗口导致代理断掉、还找不到程序去哪儿了，是最糟的体验。
	mainWin.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		api.minimiseToTray()
	})

	setupTray(wapp, api)
	setupGlobalShortcuts(api)

	if err := wapp.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}
}

// setupTray 建立托盘图标与菜单。
//
// 菜单里的"激活/停止"是直接调 API 的（托盘回调在主线程，API 自己带锁），
// 这样不依赖界面是否打开。
func setupTray(wapp *application.App, api *API) {
	tray := wapp.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip("ChainProxy · 链式代理")

	menu := application.NewMenu()
	menu.Add("显示主窗口").OnClick(func(*application.Context) {
		api.ShowWindow()
	})
	menu.AddSeparator()
	menu.Add("激活全部节点").OnClick(func(*application.Context) {
		if err := api.ActivateAll(); err != nil {
			api.Errorf("激活失败：%v", err)
		}
	})
	menu.Add("停止全部节点").OnClick(func(*application.Context) {
		if err := api.DeactivateAll(); err != nil {
			api.Errorf("停止失败：%v", err)
		}
	})
	menu.Add("复制本地代理地址").OnClick(func(*application.Context) {
		api.CopyFirstLocalAddress()
	})
	menu.AddSeparator()
	menu.Add("退出 ChainProxy").OnClick(func(*application.Context) {
		api.Quit()
	})
	tray.SetMenu(menu)

	// 左键单击托盘 = 显示窗口（Windows 上最常见的预期）
	tray.OnClick(func() { api.ShowWindow() })
	tray.Run()
	// Run 返回即表示托盘已经建起来（失败会在这里之前就 panic/报错）。
	// 记下来是为了能在日志里核实 —— GUI 版没有控制台，不记就等于查不到。
	api.trayReady = true
	api.trayIconSize = len(trayIcon)
}

// setupGlobalShortcuts 首次按配置注册全局快捷键。
//
// 组合键本身是**可配置的**（设置 → 快捷键），这里只负责在启动时应用一次；
// 之后用户改绑定走 SaveSettings → API.ApplyShortcuts。
//
// 注册失败（被别的程序占用是最常见的）只记日志 + 提示一次，不打断启动 ——
// 快捷键是锦上添花，不该让主功能起不来。
func setupGlobalShortcuts(api *API) {
	list := api.ApplyShortcuts()
	for _, st := range list {
		if st.Accel == "" {
			continue
		}
		if st.OK {
			api.shortcutOK = append(api.shortcutOK, fmt.Sprintf("%s（%s）", st.Accel, st.Label))
			continue
		}
		api.shortcutFail = append(api.shortcutFail,
			fmt.Sprintf("注册全局快捷键 %s（%s）失败：%s", st.Accel, st.Label, st.Message))
	}
}

// parseBootFlag 读取 "-name value" 或 "-name=value" 形式的启动参数。
func parseBootFlag(name string) string {
	args := os.Args[1:]
	short := "-" + name
	long := "--" + name
	for i, a := range args {
		if a == short || a == long {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if v, ok := strings.CutPrefix(a, short+"="); ok {
			return v
		}
		if v, ok := strings.CutPrefix(a, long+"="); ok {
			return v
		}
	}
	return ""
}
