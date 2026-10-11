//go:build !windows

package main

// 本项目的图形界面只面向 Windows（托盘、圈选二维码、DwmSetWindowAttribute
// 都是 Win32 能力）。这两个空实现只是为了让 `go vet ./...` / `go build ./...`
// 在别的平台上也能通过。

func applyImmersiveDarkMode(string, bool) bool { return false }

func systemIsDark() bool { return false }

// 把主窗口临时提到最前（其它平台空实现）。
func raiseMainWindowOnce() bool { return false }

// 等合成器画完一帧（Windows 才有；其它平台空实现）。
func dwmFlush() {}

// 把主窗口临时挪到屏幕外（Windows 才有；其它平台返回 false，调用方退回隐藏）。
func parkMainWindowOffscreen() (int, int, bool) { return 0, 0, false }

// 把窗口挪回原位置。
func restoreWindowPos(int, int) {}
