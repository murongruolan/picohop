package main

import (
	"errors"
	"flag"
	"reflect"
	"testing"
)

// 本文件是审查 #29 的回归测试：CLI 批量导入的 -port 不许把一批节点全撞在同一个端口上。
//
// 【缺陷】runImport 以前对每一条链接都做 `n.ListenPort = *port`，
// 于是一批节点全部监听同一个本地端口，只有第一个能真的起来，其余的点启动
// 才报"端口绑定失败"。现在 -port 是起始端口，由 batchPorts 依次顺延、
// 跳过已被占用的端口。
//
// free 是注入进来的，所以这里不真的去 bind 端口：不依赖开发机当前的占用情况，
// 也就不会偶发变红。

func TestBatchPortsSkipsUsedPorts(t *testing.T) {
	used := map[int]bool{10810: true, 10811: true, 10813: true}
	free := func(p int) bool { return p >= 1024 && p <= 65535 && !used[p] }

	got := batchPorts(10810, 3, free)
	want := []int{10812, 10814, 10815}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("应当跳过已占用端口依次顺延：want %v，got %v", want, got)
	}

	// 两两不同，且每个都真的是空闲端口 —— 这两条正是缺陷本身
	seen := map[int]bool{}
	for _, p := range got {
		if seen[p] {
			t.Fatalf("一批节点拿到了同一个端口 %d：%v", p, got)
		}
		seen[p] = true
		if !free(p) {
			t.Fatalf("返回了没验证过（仍被占用）的端口 %d", p)
		}
	}
}

func TestBatchPortsGivesUpInsteadOfGuessing(t *testing.T) {
	// 1) 区间尽头：只有 65535 可用，却要 3 个端口
	got := batchPorts(65534, 3, func(p int) bool { return p == 65535 })
	if want := []int{65535, 0, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("端口耗尽时应当返回 0（而不是猜一个坏端口）：want %v，got %v", want, got)
	}

	// 2) 全都占着：一个都分不出来，也不许返回任何候选
	all := batchPorts(1024, 2, func(int) bool { return false })
	if want := []int{0, 0}; !reflect.DeepEqual(all, want) {
		t.Fatalf("没有空闲端口时应当全是 0：want %v，got %v", want, all)
	}

	// 3) 单个节点：就是这一个端口
	if got := batchPorts(10810, 1, func(p int) bool { return p == 10810 }); !reflect.DeepEqual(got, []int{10810}) {
		t.Fatalf("单个节点应当直接用起始端口：got %v", got)
	}
}

// TestParseFlagsStopsOnHelp 是「CLI 补 -h / -help」的回归测试：三种写法都必须让调用方
// **直接返回**，而不是继续往下执行。
//
// 【为什么这不是"少打印一段帮助"】六处以前都写成 `_ = fs.Parse(args)`，返回值被丢掉了，
// 而 flag.ContinueOnError 下 `-h` 返回的是 flag.ErrHelp —— 丢掉它就等于把帮助请求当成
// 没看见：`picohop run -h` 会真的去启动节点（激活 + 保存配置）。用户以为在看帮助，
// 实际改了他的配置。这条测试锁的就是"必须停下来"。
func TestParseFlagsReturnsErrHelp(t *testing.T) {
	for _, arg := range []string{"-h", "-help", "--help"} {
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		fs.String("node", "", "")
		err := parseFlags(fs, []string{arg})
		if !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("%s 应当返回 flag.ErrHelp（调用方据此以退出码 0 结束），got %v", arg, err)
		}
	}
}

// TestParseFlagsBadFlagIsNotHelp 锁住"参数有误"与"请求帮助"必须能分开。
//
// 【为什么要单独锁这一条】两者都要让调用方停下来，但**退出码不同**：帮助是正常行为（0），
// 参数写错是错误（2）。第一版修复把两者都当成"停下来"，于是 `picohop ls -nope`
// 打完错误信息仍然退出 0 —— 脚本会把一个拼错的命令当成成功。
func TestParseFlagsBadFlagIsNotHelp(t *testing.T) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.String("node", "", "")
	err := parseFlags(fs, []string{"-nope"})
	if err == nil {
		t.Fatal("未定义的参数应当返回错误")
	}
	if errors.Is(err, flag.ErrHelp) {
		t.Fatal("参数有误不能被当成\"请求帮助\"：那样退出码会是 0")
	}
}

// TestParseFlagsAcceptsKnownFlag 正常参数仍然要能解析出来 —— 别把修复做成"一律返回 false"。
func TestParseFlagsAcceptsKnownFlag(t *testing.T) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	only := fs.String("node", "", "")
	if err := parseFlags(fs, []string{"-node", "abc"}); err != nil {
		t.Fatalf("正常参数应当返回 nil，got %v", err)
	}
	if *only != "abc" {
		t.Fatalf("-node 没解析出来：got %q", *only)
	}
}

// TestHelpRequestedTopLevel 顶层 `-h` 的判定。
//
// 【为什么这一条单独重要】splitCommand 把以 "-" 开头的参数一律当成子命令参数，所以
// `picohop -h` 会变成 cmd="ls" + args=["-h"] —— 少了这道判断，它就会去**打开配置列节点**，
// 而不是打印帮助。有子命令时（`picohop run -h`）不属于这里：那种 `-h` 归子命令自己。
func TestHelpRequestedTopLevel(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"ls"}, false},
		{[]string{"-h"}, true},
		{[]string{"--help"}, true},
		{[]string{"-help"}, true},
		{[]string{"-config", "x.json", "-h"}, true},
		{[]string{"run", "-h"}, false}, // 子命令自己的 -h，由 parseFlags 负责
		{[]string{"run"}, false},
	}
	for _, c := range cases {
		if got := helpRequested(c.args); got != c.want {
			t.Fatalf("helpRequested(%q) = %v，want %v", c.args, got, c.want)
		}
	}
}
