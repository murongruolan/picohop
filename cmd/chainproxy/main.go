// Command chainproxy 是 ChainProxy 链式代理的主程序。
//
// 用法：
//
//	chainproxy            启动图形界面（Windows）
//	chainproxy run        只启动核心服务，不开界面
//	chainproxy test       用当前配置跑一遍分层测试
//	chainproxy validate   只校验配置与端口
//	chainproxy version    打印版本
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"chainproxy/internal/app"
	"chainproxy/internal/config"
	"chainproxy/internal/gui"
	"chainproxy/internal/tester"
)

// Version 由构建脚本注入。
var Version = "dev"

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	args := os.Args[1:]
	cmd := "gui"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	switch cmd {
	case "gui":
		runGUI(args)
	case "run":
		runService(args)
	case "test":
		runTest(args)
	case "validate":
		runValidate(args)
	case "version", "-v", "--version":
		fmt.Printf("ChainProxy %s (%s/%s, %s)\n", Version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`ChainProxy 链式代理

用法:
  chainproxy              启动图形界面
  chainproxy run          只启动核心服务（前台运行，Ctrl+C 退出）
  chainproxy test         用当前配置跑一遍分层测试（前置 + 各住宅链路）
  chainproxy validate     只校验配置与端口，不启动任何服务
  chainproxy version      打印版本号

通用参数:
  -config <路径>          指定配置文件（默认 <数据目录>/config.json）
  -front <host:port>      临时覆盖前置代理地址
  -timeout <秒>           测试超时（默认 25）

本程序不会注册任何开机自启动项。`)
}

func newApp() (*app.App, error) {
	fs := flag.NewFlagSet("chainproxy", flag.ContinueOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "配置文件路径")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, err
	}
	return app.New(*cfgPath)
}

// ------------------------------------------------------------------
// runService
// ------------------------------------------------------------------

func runGUI(args []string) {
	fs := flag.NewFlagSet("gui", flag.ContinueOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "配置文件路径")
	_ = fs.Parse(args)

	a, err := app.New(*cfgPath)
	if err != nil {
		fatal("启动失败：%v", err)
	}
	defer a.Close()

	if err := gui.Run(a); err != nil {
		fatal("界面退出：%v", err)
	}
}

func runService(args []string) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "配置文件路径")
	_ = fs.Parse(args)

	a, err := app.New(*cfgPath)
	if err != nil {
		fatal("启动失败：%v", err)
	}
	defer a.Close()

	a.Subscribe(func(e app.Event) {
		if e.Kind == "log" {
			fmt.Println(e.Text)
		}
	})

	if errs := a.Validate(); len(errs) > 0 {
		fmt.Println("配置存在问题：")
		for _, e := range errs {
			fmt.Printf("  - [%s] %s\n", e.Scope, e.Message)
		}
		fmt.Println("\n请在图形界面中修正后再试。")
		os.Exit(1)
	}

	fmt.Printf("配置文件：%s\n", *cfgPath)
	fmt.Printf("数据目录：%s\n\n", config.DataDir())
	if err := a.Start(); err != nil {
		fmt.Printf("启动失败：%v\n", err)
	}

	fmt.Println("\n当前链路：")
	printStatus(a)
	fmt.Println("\n按 Ctrl+C 退出（退出时自动释放全部端口）。")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\n正在退出……")
}

func printStatus(a *app.App) {
	rows := a.Status()
	if len(rows) == 0 {
		fmt.Println("  （尚未配置住宅代理）")
		return
	}
	for _, r := range rows {
		fmt.Printf("  %-14s %-10s %s:%d  [%s] %s\n", r.Name, r.Type, r.ListenHost, r.ListenPort, stateText(r.State), r.Detail)
	}
}

func stateText(s string) string {
	switch s {
	case app.StateRunning:
		return "运行中"
	case app.StateError:
		return "错误"
	case app.StateOff:
		return "已禁用"
	default:
		return "已停止"
	}
}

// ------------------------------------------------------------------
// test
// ------------------------------------------------------------------

func runTest(args []string) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "配置文件路径")
	timeout := fs.Int("timeout", 25, "测试超时（秒）")
	_ = fs.Parse(args)

	a, err := app.New(*cfgPath)
	if err != nil {
		fatal("启动失败：%v", err)
	}
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()

	fmt.Println("========================================")
	fmt.Printf("配置：%s\n", *cfgPath)
	fmt.Println("========================================")

	fmt.Println("\n[1/2] 测试前置代理")
	fr := a.TestFront(ctx)
	report("前置代理", fr)

	fmt.Println("\n[2/2] 测试住宅代理链路")
	rows := a.Status()
	if len(rows) == 0 {
		fmt.Println("  尚未配置任何住宅代理。")
		return
	}
	for _, r := range rows {
		u, _ := a.GetUpstream(r.ID)
		if !u.Enabled {
			fmt.Printf("  · %-14s 已跳过（禁用）\n", u.Name)
			continue
		}
		cr := a.TestUpstream(ctx, r.ID)
		report(u.Name+" → 127.0.0.1:"+fmt.Sprint(u.ListenPort), cr)
		fmt.Printf("      %s\n", tester.Compare(fr, cr))
	}
	fmt.Println()
}

func report(label string, r tester.Result) {
	if r.OK {
		fmt.Printf("  %-40s ✅ %s  %d ms\n", label, r.IP, r.Latency.Milliseconds())
		return
	}
	fmt.Printf("  %-40s ❌ %v\n", label, r.Err)
}

// ------------------------------------------------------------------
// validate
// ------------------------------------------------------------------

func runValidate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "配置文件路径")
	_ = fs.Parse(args)

	a, err := app.New(*cfgPath)
	if err != nil {
		fatal("读取配置失败：%v", err)
	}
	defer a.Close()

	fmt.Printf("配置：%s\n", *cfgPath)
	errs := a.Validate()
	if len(errs) == 0 {
		fmt.Println("✅ 配置校验通过")
		printStatus(a)
		return
	}
	fmt.Printf("❌ 发现 %d 个问题：\n", len(errs))
	for _, e := range errs {
		fmt.Printf("  - [%s] %s: %s\n", e.Scope, e.Field, e.Message)
	}
	os.Exit(1)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
