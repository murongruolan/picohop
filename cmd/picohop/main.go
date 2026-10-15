// Command picohop 是无界面的命令行入口。
//
// 它存在的意义有两个：
//  1. 排障：出问题时不用开界面就能看到链路到底通不通、出口 IP 是谁
//  2. 自动化验证：脚本可以直接调它做端到端测试
//
// 图形界面在 desktop/ 目录（Wails + Vue）。
//
// 用法：
//
//	picohop ls                      列出节点与运行状态
//	picohop run                     启动节点并前台运行（Ctrl+C 退出）
//	picohop test [-kind real]       跑指定的测试
//	picohop validate                只校验配置
//	picohop fetch                   探测各节点出口 IP
//	picohop import <链接>           从分享链接导入节点
//	picohop express <节点>          导出节点的分享链接
//	picohop wgkey                   生成 WireGuard 密钥对
//	picohop version                 打印版本
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/murongruolan/picohop/internal/app"
	"github.com/murongruolan/picohop/internal/config"
	"github.com/murongruolan/picohop/internal/ports"
	"github.com/murongruolan/picohop/internal/proxy"
	"github.com/murongruolan/picohop/internal/share"
	"github.com/murongruolan/picohop/internal/wgproxy"

	// 注册 mihomo 出口（proxy 通过注册函数解耦，见 internal/engine/register.go）。
	// 它是**唯一**的代理内核：sing-box 与它的回退已经删除。
	_ "github.com/murongruolan/picohop/internal/engine"
)

// Version 由构建脚本注入。
var Version = "dev"

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	cmd, args := splitCommand(os.Args[1:])
	if cmd == "" {
		cmd = "ls"
	}

	// 【为什么必须在动配置之前拦掉】splitCommand 把以 "-" 开头的参数一律当成子命令参数，
	// 于是 `picohop -h` 会变成 cmd=""（落到 ls）+ args=["-h"] —— 结果是**打开配置去列节点**
	// 而不是打印帮助（更早的版本里 `run -h` 甚至会真的启动节点、激活并保存配置）。
	// 帮助是只读请求，必须在 newApp() 之前结束。
	if helpRequested(os.Args[1:]) {
		usage()
		return
	}

	switch cmd {
	case "ls", "list":
		runList(args)
	case "run":
		runService(args)
	case "test":
		runTest(args)
	case "validate":
		runValidate(args)
	case "fetch":
		runFetch(args)
	case "import":
		runImport(args)
	case "express":
		runExpress(args)
	case "wgkey":
		runWGKey(args)
	case "engines":
		runEngines(args)
	case "version", "-v", "--version":
		fmt.Printf("PicoHop %s (%s/%s, %s)\n", Version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`PicoHop 链式代理（命令行）

用法:
  picohop ls                列出节点与运行状态
  picohop run               启动节点并前台运行（Ctrl+C 退出）
  picohop test              跑测试（-kind tcp|real|speed|exit，默认 real）
  picohop validate          只校验配置，不启动任何服务
  picohop fetch             探测各节点出口 IP
  picohop import <链接>     从分享链接导入节点
  picohop express <节点>    导出节点的分享链接
  picohop engines           自检内嵌内核（mihomo）是否可用
  picohop wgkey             生成 WireGuard 密钥对
  picohop version           打印版本

通用参数（写在子命令前面或后面都可以）:
  -config <路径>   指定配置文件（默认 <数据目录>/config.json）
  -h, -help        打印帮助；写在子命令后面则打印该子命令的参数

子命令参数:
  run      -node <节点>  -seconds <秒>
  test     -node <节点>  -kind tcp|real|speed|exit  -timeout <秒>
  fetch    -node <节点>
  import   -port <端口>  -front <前置>  -activate
  wgkey    -private <私钥>  -psk

节点参数可以是 ID、别名或别名的一部分（可用逗号分隔多个）。

本程序不会注册任何开机自启动项。`)
}

// parseFlags 解析子命令参数并把输出接过来。返回值：
//   - nil            → 解析成功，继续执行
//   - flag.ErrHelp   → 已把用法打到 stdout（显式请求帮助，不是错误）
//   - 其他错误        → 已把错误与用法打到 stderr
//
// 【为什么必须处理返回值】九处以前都写成 `_ = fs.Parse(args)`，把返回值丢掉了。
// flag.ContinueOnError 下 `-h` 会让 Parse 返回 flag.ErrHelp，丢掉它就等于**把帮助请求
// 当成没看见、继续往下执行**：
//   - `picohop run -h` 会真的去启动节点（激活 + 保存配置）—— 用户以为在看帮助，实际改了配置；
//   - `picohop -h` 更隐蔽：splitCommand 把 `-h` 当成子命令参数、cmd 空串落到 "ls"，
//     于是它打开配置去列节点（见 main 里的 helpRequested）。
//
// 这里刻意**不** os.Exit：退出码那件事交给 mustFlags，这样这个函数可以被单测直接调用。
func parseFlags(fs *flag.FlagSet, args []string) error {
	var buf bytes.Buffer
	fs.SetOutput(&buf) // 自己接管输出，免得一半走 stdout、一半走 stderr
	err := fs.Parse(args)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, flag.ErrHelp):
		fmt.Print(buf.String())
		return flag.ErrHelp
	default:
		fmt.Fprint(os.Stderr, buf.String())
		fmt.Fprintf(os.Stderr, "参数有误：%v\n", err)
		return err
	}
}

// mustFlags 是子命令里的统一入口：解析成功返回 true；否则按约定结束进程。
//
// 【为什么帮助与参数有误要分开】显式请求帮助是**正常**行为（退出码 0）；
// 参数写错是错误（退出码 2）。以前两者都被忽略，于是 `picohop ls -nope` 打完错误信息
// 仍然退出 0 —— 脚本会把一个拼错的命令当成成功。
func mustFlags(fs *flag.FlagSet, args []string) bool {
	err := parseFlags(fs, args)
	if err == nil {
		return true
	}
	if errors.Is(err, flag.ErrHelp) {
		return false // 帮助已打印，退出码 0
	}
	os.Exit(2)
	return false // 不可达
}

// subUsage 给子命令装一个说人话的用法头（默认那句 "Usage of run:" 既生硬、
// 也不说明这个子命令是干什么的）。
func subUsage(fs *flag.FlagSet, desc string) {
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "%s\n\n用法: picohop %s [参数]\n\n参数:\n", desc, fs.Name())
		fs.PrintDefaults()
	}
}

// helpRequested 判断"没有给子命令、只给了帮助开关"（`picohop -h` / `--help`）。
//
// 有子命令时不在这里处理 —— 那种 `-h` 属于子命令自己，由 parseFlags 负责
// （`picohop run -h` 打印 run 的参数，而不是全局帮助）。
func helpRequested(args []string) bool {
	if cmd, _ := splitCommand(args); cmd != "" {
		return false
	}
	for _, a := range args {
		switch a {
		case "-h", "-help", "--help":
			return true
		}
	}
	return false
}

// splitCommand 从命令行里挑出子命令，并返回子命令自己的参数。
//
// 之所以不能简单地看 args[0]：通用参数（-config）允许写在子命令前面，
// 例如 `picohop -config x.json fetch`。
//
// 同时把 -config 从子命令参数里剔掉 —— newApp() 是直接从 os.Args 读它的，
// 而各个子命令的 FlagSet 并没有定义这个选项，留在里面会让 flag.Parse 报错。
func splitCommand(args []string) (string, []string) {
	takesValue := map[string]bool{"-config": true, "--config": true}
	cmd := ""
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if takesValue[a] {
			i++ // 连带跳过它的值
			continue
		}
		if strings.HasPrefix(a, "-config=") || strings.HasPrefix(a, "--config=") {
			continue
		}
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			continue
		}
		if cmd == "" {
			cmd = a
			continue
		}
		rest = append(rest, a)
	}
	return cmd, rest
}

// newApp 按 -config 参数打开配置。
func newApp() *app.App {
	cfgPath := config.ConfigPath()
	for i, a := range os.Args {
		if (a == "-config" || a == "--config") && i+1 < len(os.Args) {
			cfgPath = os.Args[i+1]
		} else if v, ok := strings.CutPrefix(a, "-config="); ok {
			cfgPath = v
		}
	}
	inst, err := app.New(cfgPath)
	if err != nil {
		fatal("读取配置失败：%v", err)
	}
	return inst
}

// ------------------------------------------------------------------
// ls
// ------------------------------------------------------------------

func runList(args []string) {
	// 【为什么"没有参数"的子命令也要建 FlagSet】一是让 `-h` 能打印它自己的用法，
	// 二是让拼错的参数报错，而不是像以前那样把 args 整个丢掉、静默忽略。
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	subUsage(fs, "列出节点与运行状态")
	if !mustFlags(fs, args) {
		return
	}

	a := newApp()
	defer a.Close()

	views := a.Nodes()
	if len(views) == 0 {
		fmt.Println("还没有任何节点。用图形界面或 picohop import 添加。")
		return
	}
	fmt.Printf("%-14s %-11s %-9s %-26s %-7s %-12s %-8s %s\n",
		"别名", "类型", "本地端口", "服务器", "远程端口", "传输", "状态", "前置")
	for _, v := range views {
		fmt.Printf("%-14s %-11s %-9d %-26s %-7d %-12s %-8s %s\n",
			trunc(v.Name, 13), v.ProtocolLabel, v.ListenPort,
			trunc(v.Server, 25), v.Port, trunc(v.Transport, 11),
			stateText(v), trunc(v.FrontLabel, 34))
	}
	s := a.Summary()
	fmt.Printf("\n共 %d 个节点，运行中 %d 个；今日流量 %s\n", s.Total, s.Running, s.TodayText)
	fmt.Printf("全局前置：%s\n", s.FrontDesc)
	fmt.Printf("配置文件：%s\n", s.ConfigPath)
}

func stateText(v app.NodeView) string {
	switch v.State {
	case app.StateRunning:
		return "运行中"
	case app.StateError:
		return "错误"
	default:
		if v.Active {
			return "待启动"
		}
		return "未激活"
	}
}

// ------------------------------------------------------------------
// run
// ------------------------------------------------------------------

func runService(args []string) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	subUsage(fs, "启动节点并前台运行（Ctrl+C 退出）")
	only := fs.String("node", "", "只启动指定节点（ID/别名，可用逗号分隔）")
	seconds := fs.Int("seconds", 0, "运行多少秒后自动退出（0 表示一直运行）")
	if !mustFlags(fs, args) {
		return
	}

	a := newApp()
	defer a.Close()

	a.Subscribe(func(e app.Event) {
		if e.Kind == app.EventLog {
			fmt.Println(e.Text)
		}
	})

	if issues := a.Validate(); len(issues) > 0 {
		fmt.Println("配置存在问题：")
		for _, e := range issues {
			fmt.Printf("  - [%s] %s: %s\n", e.Scope, e.Field, e.Message)
		}
		os.Exit(1)
	}

	ids, err := resolveNodes(a, *only)
	if err != nil {
		fatal("选择节点失败：%v", err)
	}
	if len(ids) == 0 {
		a.AutoActivate()
		fmt.Println("已按配置自动激活上次处于激活状态的节点。")
	} else if err := a.Activate(ids); err != nil {
		fmt.Printf("启动失败：%v\n", err)
	}

	printStatus(a)
	fmt.Println("\n按 Ctrl+C 退出（退出时自动释放全部端口）。")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	if *seconds > 0 {
		select {
		case <-sig:
		case <-time.After(time.Duration(*seconds) * time.Second):
		}
	} else {
		<-sig
	}
	fmt.Println("\n正在退出……")
}

func printStatus(a *app.App) {
	fmt.Println("\n当前节点：")
	views := a.Nodes()
	if len(views) == 0 {
		fmt.Println("  （尚未配置节点）")
		return
	}
	for _, v := range views {
		fmt.Printf("  %-14s %-10s 127.0.0.1:%-6d [%s] %s\n",
			trunc(v.Name, 13), v.ProtocolLabel, v.ListenPort, stateText(v), v.Detail)
	}
}

// ------------------------------------------------------------------
// test
// ------------------------------------------------------------------

func runTest(args []string) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	subUsage(fs, "跑测试：tcp / real / speed / exit")
	only := fs.String("node", "", "只测试指定节点（ID/别名，可用逗号分隔）")
	kind := fs.String("kind", app.TestReal, "测试类型：tcp / real / speed / exit")
	timeout := fs.Int("timeout", 120, "整体超时（秒）")
	if !mustFlags(fs, args) {
		return
	}

	a := newApp()
	defer a.Close()

	a.Subscribe(func(e app.Event) {
		if e.Kind == app.EventLog {
			fmt.Println("  " + e.Text)
		}
	})

	ids, err := resolveNodes(a, *only)
	if err != nil {
		fatal("选择节点失败：%v", err)
	}
	if len(ids) == 0 {
		for _, v := range a.Nodes() {
			ids = append(ids, v.ID)
		}
	}
	if len(ids) == 0 {
		fmt.Println("没有可测试的节点。")
		return
	}

	fmt.Printf("开始 %s 测试，共 %d 个节点\n\n", *kind, len(ids))
	if err := a.TestNodes(ids, *kind); err != nil {
		fatal("启动测试失败：%v", err)
	}

	deadline := time.Now().Add(time.Duration(*timeout) * time.Second)
	for a.Testing() && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if a.Testing() {
		a.CancelTests()
		fmt.Println("超时，已取消。")
	}

	fmt.Println("\n结果：")
	// 【为什么要有星号】审查 #19 之后，测试失败**不再**把上一次的延迟/速度写成 0，
	// 所以一个失败节点也会显示上一次成功测出来的数字。不打标记它会被读成
	// "刚刚测出来就是这个数"，比显示空还糟。星号 = 本次没测出数值（见 staleMark）。
	marked := false
	for _, v := range a.Nodes() {
		if !containsID(ids, v.ID) {
			continue
		}
		tcp := staleMark(msText(v.LatencyMs), v.LatencyFailed)
		real := staleMark(msText(v.RealMs), v.LatencyFailed)
		spd := staleMark(speedText(v.SpeedMBps), v.SpeedFailed)
		if strings.HasSuffix(tcp, "*") || strings.HasSuffix(real, "*") || strings.HasSuffix(spd, "*") {
			marked = true
		}
		fmt.Printf("  %-16s TCP %-9s 真连接 %-9s 速度 %-12s 出口 %s\n",
			trunc(v.Name, 15), tcp, real, spd, orDash(v.ExitIP))
	}
	if marked {
		fmt.Println("  （* = 本次测试没有测出数值，显示的是上次成功值）")
	}
}

// ------------------------------------------------------------------
// validate
// ------------------------------------------------------------------

func runValidate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	subUsage(fs, "只校验配置，不启动任何服务、不改动配置")
	if !mustFlags(fs, args) {
		return
	}

	a := newApp()
	defer a.Close()

	fmt.Printf("配置：%s\n", a.ConfigPath())
	issues := a.Validate()
	if len(issues) == 0 {
		fmt.Println("✅ 配置校验通过")
		return
	}
	fmt.Printf("❌ 发现 %d 个问题：\n", len(issues))
	for _, e := range issues {
		fmt.Printf("  - [%s] %s: %s\n", e.Scope, e.Field, e.Message)
	}
	os.Exit(1)
}

// ------------------------------------------------------------------
// fetch
// ------------------------------------------------------------------

func runFetch(args []string) {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	subUsage(fs, "探测各节点（以及全局前置）的出口 IP")
	only := fs.String("node", "", "只测指定节点（ID/别名，可用逗号分隔）")
	if !mustFlags(fs, args) {
		return
	}

	a := newApp()
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Println("=== 全局前置代理 ===")
	fr := a.TestFront(ctx)
	if fr.OK {
		fmt.Printf("  ✅ %s  %d ms\n", fr.IP, fr.Latency.Milliseconds())
	} else {
		fmt.Printf("  ❌ %v\n", fr.Err)
	}

	ids, err := resolveNodes(a, *only)
	if err != nil {
		fatal("选择节点失败：%v", err)
	}
	if len(ids) == 0 {
		for _, v := range a.Nodes() {
			ids = append(ids, v.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	if err := a.TestNodes(ids, app.TestExit); err != nil {
		fatal("启动测试失败：%v", err)
	}
	for a.Testing() {
		time.Sleep(200 * time.Millisecond)
	}

	fmt.Println("\n=== 各节点出口 IP ===")
	for _, v := range a.Nodes() {
		if !containsID(ids, v.ID) {
			continue
		}
		mark := ""
		if fr.OK && v.ExitIP != "" {
			if v.ExitIP == fr.IP {
				mark = "  ⚠️ 与前置出口相同，说明没有真正走这个出口"
			} else {
				mark = "  ✅ 与前置出口不同"
			}
		}
		fmt.Printf("  %-16s %-18s %s\n", trunc(v.Name, 15), orDash(v.ExitIP), mark)
	}
}

// ------------------------------------------------------------------
// import / express
// ------------------------------------------------------------------

// batchPorts 给"一次导入多个节点"的场景分配本地端口（审查 #29）。
//
// 【缺陷】-port 以前被原样塞给这一批里的**每一个**节点：一条文本里带 3 条分享链接时，
// 3 个节点会拿到同一个本地端口，只有第一个能真的监听起来，另外两个要到点启动才报
// "端口绑定失败"—— 而坏配置已经落盘了。
//
// 现在的语义：-port 是这一批的**起始端口**，逐个往后找空闲端口，已占用的跳过。
// free 就是"这个端口现在能不能用"（本项目别的节点占着、本机别的程序占着都算不能用）。
// 返回长度与 count 相同；找不到空闲端口的位置是 ports.NoPort(0)，
// 调用方据此明确报错 —— 绝不返回一个没验证过的端口（与 ports.NoPort 的约定一致）。
//
// free 由调用方注入是为了让这个函数保持纯粹：测试直接给一张占用表就够了，
// 不必真的去 bind 端口（也就不会因为开发机上恰好跑着什么而偶发变红）。
func batchPorts(start, count int, free func(int) bool) []int {
	out := make([]int, 0, count)
	next := start
	for i := 0; i < count; i++ {
		p := next
		found := ports.NoPort
		for scanned := 0; scanned < ports.MaxScanAhead && p <= 65535; scanned++ {
			if free(p) {
				found = p
				break
			}
			p++
		}
		out = append(out, found)
		if found == ports.NoPort {
			// 后面已经没有空闲端口了：剩下的位置全部标失败，不再逐个空扫占用表。
			for j := i + 1; j < count; j++ {
				out = append(out, ports.NoPort)
			}
			return out
		}
		next = found + 1
	}
	return out
}

func runImport(args []string) {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	subUsage(fs, "从分享链接导入节点")
	port := fs.Int("port", 0, "指定本地端口（默认自动分配；一条文本里有多条链接时是起始端口）")
	front := fs.String("front", "", "前置：留空=直连，global=全局前置，或另一个节点的 ID")
	activate := fs.Bool("activate", false, "导入后立即激活")
	if !mustFlags(fs, args) {
		return
	}

	if fs.NArg() == 0 {
		fatal("请给出分享链接（vless:// vmess:// ss:// trojan:// hysteria2://）")
	}
	link := fs.Arg(0)

	// -port 至少要过 ports.Check 那一关（1-1023 是系统保留端口，>65535 非法）：
	// 这种值以前会被原样写进配置，节点要到点启动才报错。顺带保证下面的
	// "从起始端口往后顺延"是从一个合法端口起步的（审查 #29）。
	if *port != 0 && (*port < 1024 || *port > 65535) {
		fatal("本地端口 %d 非法，应在 1024-65535 之间", *port)
	}

	nodes, err := share.DecodeAny(link)
	if err != nil {
		fatal("解析分享链接失败：%v", err)
	}

	a := newApp()
	defer a.Close()

	// 【审查 #29】批量导入的端口必须各占一个：-port 只当起始端口。
	// 单个节点保持原语义 —— -port 就是它的端口，被占用时由 AddNode 明确报错，
	// 不悄悄换成用户没要的端口。
	var batch []int
	if *port > 0 && len(nodes) > 1 {
		batch = batchPorts(*port, len(nodes), func(p int) bool { return a.PortStatus(p, "").Free })
	}

	var added []string
	for i, n := range nodes {
		if *port > 0 {
			switch {
			case len(nodes) == 1:
				n.ListenPort = *port
			case batch[i] == ports.NoPort:
				fmt.Printf("  ❌ 导入「%s」失败：从端口 %d 起找不到空闲端口，请先释放一个端口再导入\n", n.Name, *port)
				continue
			default:
				n.ListenPort = batch[i]
			}
		}
		n.Front = *front
		out, aerr := a.AddNode(n)
		if aerr != nil {
			fmt.Printf("  ❌ 导入「%s」失败：%v\n", n.Name, aerr)
			continue
		}
		added = append(added, out.ID)
		fmt.Printf("  ✅ 已导入「%s」（%s，本地端口 %d）\n", out.Name, config.ProtocolLabel(out.Protocol), out.ListenPort)
	}
	if *activate && len(added) > 0 {
		if err := a.Activate(added); err != nil {
			fmt.Printf("  激活失败：%v\n", err)
		}
	}
}

func runExpress(args []string) {
	fs := flag.NewFlagSet("express", flag.ContinueOnError)
	subUsage(fs, "导出节点的分享链接")
	if !mustFlags(fs, args) {
		return
	}

	if fs.NArg() == 0 {
		fatal("请给出节点（ID 或别名）")
	}
	a := newApp()
	defer a.Close()

	ids, err := resolveNodes(a, fs.Arg(0))
	if err != nil || len(ids) == 0 {
		fatal("找不到节点 %q", fs.Arg(0))
	}
	for _, id := range ids {
		n, ok := a.GetNode(id)
		if !ok {
			continue
		}
		link, lerr := share.Encode(n)
		if lerr != nil {
			fmt.Printf("  ❌ 「%s」无法导出：%v\n", n.Name, lerr)
			continue
		}
		fmt.Println(link)
	}
}

// ------------------------------------------------------------------
// engines / wgkey
// ------------------------------------------------------------------

func runEngines(args []string) {
	fs := flag.NewFlagSet("engines", flag.ContinueOnError)
	subUsage(fs, "自检内嵌内核（mihomo）是否注册可用")
	if !mustFlags(fs, args) {
		return
	}

	fmt.Printf("Go 版本：%s\n", runtime.Version())
	// 【这个自检现在检查什么】以前它检查的是 sing-box 的构建标签（with_utls /
	// with_quic）—— 那两个标签随 sing-box 一起删掉了，mihomo 不需要它们。
	// 现在要确认的是"唯一的内核真的被编译进来且被注册了"：拿一个最小节点去
	// 问一次工厂，注册缺失会当场返回"当前构建未包含 …"。
	node := config.Node{
		ID: "selfcheck", Name: "自检", Protocol: config.ProtocolVLESS,
		Server: "127.0.0.1", Port: 443, UUID: "00000000-0000-0000-0000-000000000000",
	}
	if _, err := proxy.NewUpstream(node, config.Front{Type: config.FrontTypeDirect}, nil); err != nil {
		fmt.Printf("❌ 内核不可用：%v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ 内嵌 mihomo 已注册（唯一的代理内核，无需构建标签）")
	fmt.Println("   可用协议：VMess、VLESS（含 Reality）、Shadowsocks、Trojan、Hysteria2")
	fmt.Println("   支持传输：tcp、ws、grpc、http(h2)、httpupgrade、xhttp（vless）")
}

// runWGKey 生成 WireGuard 密钥对，或由给定私钥推导公钥。
//
// 为什么要有这个命令：WireGuard 的标准流程要求**私钥在本地生成、只把公钥交给服务端**，
// 但 `wg genkey` / `wg pubkey` 需要额外安装 wireguard-tools。
// 本程序已经把 Curve25519 打进来了，顺手提供，用户就不用再装东西。
func runWGKey(args []string) {
	fs := flag.NewFlagSet("wgkey", flag.ContinueOnError)
	subUsage(fs, "生成 WireGuard 密钥对")
	privIn := fs.String("private", "", "已有一个私钥时，只推导它的公钥（base64 或 hex）")
	psk := fs.Bool("psk", false, "顺带生成一个预共享密钥")
	if !mustFlags(fs, args) {
		return
	}

	if *privIn != "" {
		pub, err := wgproxy.PublicKeyFromPrivate(*privIn)
		if err != nil {
			fatal("推导失败：%v", err)
		}
		fmt.Printf("本端公钥 = %s\n", pub)
		fmt.Println()
		fmt.Println("把这一行交给服务端，让它登记成 peer 的公钥。私钥不要再发给任何人。")
		return
	}

	kp, err := wgproxy.GenerateKeyPair()
	if err != nil {
		fatal("生成失败：%v", err)
	}
	fmt.Println("=== WireGuard 密钥对（刚生成，请自行保存）===")
	fmt.Println()
	fmt.Printf("私钥（只留在本机，不要发给任何人）：\n  %s\n\n", kp.PrivateKey)
	fmt.Printf("公钥（把这个交给服务端登记为 peer）：\n  %s\n\n", kp.PublicKey)
	if *psk {
		p, perr := wgproxy.GeneratePresharedKey()
		if perr != nil {
			fatal("生成预共享密钥失败：%v", perr)
		}
		fmt.Printf("预共享密钥（双方填同一个值；服务端没要求就不用填）：\n  %s\n\n", p)
	}
	fmt.Println("接着服务端应该回给你：它自己的公钥、Endpoint（地址:端口），")
	fmt.Println("以及分配给你的隧道地址（例如 10.8.0.2/32）。")
}

// ------------------------------------------------------------------
// 工具
// ------------------------------------------------------------------

// resolveNodes 把用户给的节点标识（ID / 别名 / 别名的一部分）解析成 ID 列表。
func resolveNodes(a *app.App, spec string) ([]string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	views := a.Nodes()
	var out []string
	for _, part := range strings.Split(spec, ",") {
		key := strings.TrimSpace(part)
		if key == "" {
			continue
		}
		var matched []string
		for _, v := range views {
			if v.ID == key || v.Name == key || strings.Contains(v.Name, key) {
				matched = append(matched, v.ID)
			}
		}
		if len(matched) == 0 {
			return nil, fmt.Errorf("找不到节点 %q", key)
		}
		if len(matched) > 1 {
			return nil, fmt.Errorf("%q 匹配到多个节点，请用完整别名或 ID", key)
		}
		out = append(out, matched[0])
	}
	return out, nil
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func msText(v int64) string {
	if v <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d ms", v)
}

func speedText(v float64) string {
	if v <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f MB/s", v)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// staleMark 给"上次成功值"打一个星号。
//
// failed 表示最近一次同类测试没有测出数值；此时数值是**上一次**测出来的
// （审查 #19 起不再清零），必须让读的人看出来。没有数值就还是 "-"，不加星号
// （那是"没测过"，本来就看得出来）。
func staleMark(s string, failed bool) string {
	if failed && s != "-" {
		return s + "*"
	}
	return s
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
