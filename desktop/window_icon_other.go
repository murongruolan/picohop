//go:build !windows

package main

// applyWindowIcon 在非 Windows 平台是空操作，因为这两处各自有正路：
//   - Linux：窗口图标走 application.LinuxWindow.Icon（见 main.go 的窗口选项）；
//   - macOS：窗口标题栏不放图标，Dock 里的图标来自 .app 包内的 icons.icns。
func (a *API) applyWindowIcon() bool { return false }
