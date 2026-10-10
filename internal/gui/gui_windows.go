//go:build windows

package gui

import (
	"fmt"

	"chainproxy/internal/app"
)

// Available 在 Windows 上总是可用。
func Available() bool { return true }

// runNative 由真正的 Win32 实现提供（见 win_window.go）。
// 若该文件未参与构建，则退回到命令行模式。
var runNative func(*app.App) error

// Run 启动图形界面。
func Run(a *app.App) error {
	if runNative == nil {
		fmt.Println("图形界面尚未构建，退回命令行模式。")
		return runFallback(a)
	}
	return runNative(a)
}

func runFallback(a *app.App) error {
	if err := a.Start(); err != nil {
		fmt.Println("启动失败：", err)
		return err
	}
	fmt.Println("按 Ctrl+C 退出。")
	select {}
}
