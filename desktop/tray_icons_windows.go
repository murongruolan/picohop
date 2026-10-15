//go:build windows

package main

import _ "embed"

// 托盘图标：三种底色各一份多尺寸 .ico，全部由 .\scripts\gen_icons.ps1 从
// desktop\build\icon-master.png 生成 —— 不要手改生成物，改母图后重跑脚本。
//
// 【为什么 Windows 托盘用 ICO 而不是 PNG】托盘最终落到 Shell_NotifyIcon 的图标句柄，
// Wails v3 那条链路（pkg/application/systemtray_windows.go 的 setIcon）按 ICO 解析。
// 改动前这里嵌的就是 Wails 生成的那份 icon.ico，一直工作正常，所以沿用同一格式。
//
// 三种底色各表示什么、为什么用"换底色"而不是"换形状"：见 tray_state.go。
//
//go:embed build/icons/tray/tray-normal.ico
var trayIconNormal []byte

//go:embed build/icons/tray/tray-global.ico
var trayIconGlobal []byte

//go:embed build/icons/tray/tray-tun.ico
var trayIconTun []byte

// windowIcon 是本窗口的图标。
//
// 【Windows 上它目前是备用的】v3 没有"运行期设置窗口图标"的 API：
// application.WindowsWindow 只有 DisableIcon、没有 Icon 字段，顶层
// WebviewWindowOptions 也没有。标题栏 / 任务栏 / 资源管理器里那个图标来自
// **exe 里的 RT_GROUP_ICON 资源**，它由 desktop\rsrc_windows_amd64.syso 提供
// （gen_icons.ps1 -Syso 生成；Go 链接器按 GOOS/GOARCH 自动挑 .syso，构建命令不用改）。
// 这份 embed 留着是为了：① 哪天 v3 补上运行期 API 就能直接用；
// ② 其它平台走同一套代码路径时不用分叉。
//
//go:embed build/windows/icon.ico
var windowIcon []byte
