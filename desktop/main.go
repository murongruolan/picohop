// Command desktop 是 ChainProxy 的图形界面入口（Wails + Vue 3）。
//
// 界面层的职责边界很清晰：它只通过 API（见 api.go）与后端交互，
// 不直接碰 proxy / engine / config 这些包 —— 协议知识只存在于后端一处。
package main

import (
	"embed"
	"fmt"
	"os"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	// 注册两种出口实现（proxy 通过注册函数解耦，见各自的 register.go）
	_ "chainproxy/internal/engine"
	_ "chainproxy/internal/wgproxy"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// 【开发期自测用】-open <对话框名> 让界面启动后直接把某个面板打开。
	//
	// 存在的意义：要检查对话框的排版就得真的打开它，而"模拟鼠标点击"这种
	// 自动化手段会去动用户的鼠标、甚至在焦点丢失时点到别的程序上（实测踩过）。
	// 用这个参数让界面自己打开面板，是纯只读、零副作用的做法。
	//
	// 取值：addnode / edit / settings / import / logs / share / portonly / contextmenu
	api := NewAPI(parseBootFlag("open"))

	err := wails.Run(&options.App{
		Title: "ChainProxy · 链式代理",
		// 刻意做成中等尺寸：这是个"看一眼、点两下"的工具，
		// 摊满整个屏幕反而难用。信息密度靠列表的列显示来控制。
		Width:            1180,
		Height:           740,
		MinWidth:         920,
		MinHeight:        580,
		DisableResize:    false,
		Frameless:        false,
		AlwaysOnTop:      false,
		BackgroundColour: &options.RGBA{R: 20, G: 22, B: 28, A: 1},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        api.startup,
		OnShutdown:       api.shutdown,
		Bind:             []any{api},
		Windows: &windows.Options{
			// 不要透明/亚克力效果：这会让 Element Plus 的暗色主题发灰、文字发虚。
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			// 保留系统自带的窗口阴影与圆角
			DisableFramelessWindowDecorations: false,
			Theme:                             windows.SystemDefault,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
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
