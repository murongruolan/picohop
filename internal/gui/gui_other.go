//go:build !windows

package gui

import (
	"fmt"

	"chainproxy/internal/app"
)

// Available 在非 Windows 平台上没有图形界面。
func Available() bool { return false }

// Run 提示用户当前构建不含 GUI，并建议使用 run 子命令。
func Run(a *app.App) error {
	fmt.Println("当前构建不包含图形界面（GUI 仅在 Windows 上可用）。")
	fmt.Println("请使用 `chainproxy run` 以纯命令行方式运行。")
	if err := a.Start(); err != nil {
		fmt.Println("启动失败：", err)
		return err
	}
	printStatusFallback(a)
	fmt.Println("\n按 Ctrl+C 退出。")
	select {}
}

func printStatusFallback(a *app.App) {
	fmt.Println("\n当前链路：")
	for _, r := range a.Status() {
		fmt.Printf("  %-14s %-10s %s:%d  %s\n", r.Name, r.Type, r.ListenHost, r.ListenPort, r.Detail)
	}
}
