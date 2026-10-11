//go:build !windows

package main

// 本项目的图形界面只面向 Windows（托盘、圈选二维码、DwmSetWindowAttribute
// 都是 Win32 能力）。这两个空实现只是为了让 `go vet ./...` / `go build ./...`
// 在别的平台上也能通过。

func applyImmersiveDarkMode(string, bool) bool { return false }

func systemIsDark() bool { return false }
