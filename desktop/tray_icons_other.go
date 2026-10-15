//go:build !windows

package main

import _ "embed"

// 非 Windows 平台的托盘图标：PNG。
//
// 【为什么用 32 px 那份】托盘实际显示在 16~24 px（macOS 菜单栏 22 pt、Linux 指示器
// 22~24 px），32 px 下采样后边缘比直接给 16 px 干净；macOS 的 2x 屏也够用。
// 生成物在 desktop\build\icons\tray\ 下，另有 16/22/44/64 px 备其它场景。
//
//go:embed build/icons/tray/tray-normal-32.png
var trayIconNormal []byte

//go:embed build/icons/tray/tray-global-32.png
var trayIconGlobal []byte

//go:embed build/icons/tray/tray-tun-32.png
var trayIconTun []byte

// windowIcon 给 Linux 的窗口图标用（v3 的 application.LinuxWindow.Icon，见 main.go）。
// macOS 的窗口标题栏不放图标，Dock 里的图标来自 .app 包内的 icons.icns，
// 由构建产物提供，不走这里。
//
//go:embed build/linux/appicon.png
var windowIcon []byte
