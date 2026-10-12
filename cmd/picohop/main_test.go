package main

import (
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
