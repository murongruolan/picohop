package app

import (
	"testing"

	"chainproxy/internal/config"
)

// TestNetworkLogCursor 锁死"游标式拉取"的语义：只给没见过的那部分。
//
// 这是网络日志的性能基础 —— 日志每秒可能进来十几条，每次都重传整段是不可接受的。
func TestNetworkLogCursor(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})

	a.appendNetworkLog("住宅代理1", "info", "inbound connection from 127.0.0.1:5000")
	a.appendNetworkLog("住宅代理1", "info", "outbound connection to example.com:443")
	a.appendNetworkLog("住宅代理2", "warn", "握手失败")

	all, latest := a.NetworkLogs(0)
	if len(all) != 3 {
		t.Fatalf("应当有 3 条，实际 %d 条", len(all))
	}
	if latest != all[len(all)-1].Seq {
		t.Fatalf("latest 应当是最后一条的序号，实际 latest=%d 最后=%d", latest, all[len(all)-1].Seq)
	}
	if all[0].Node != "住宅代理1" || all[2].Level != "warn" {
		t.Fatalf("字段没落对：%+v", all)
	}

	// 给最新序号：应当没有新东西
	if next, _ := a.NetworkLogs(latest); len(next) != 0 {
		t.Fatalf("给最新游标时不该返回任何条目，实际 %d 条", len(next))
	}
	// 给倒数第二条的序号：应当只多出最后一条
	next, _ := a.NetworkLogs(all[1].Seq)
	if len(next) != 1 || next[0].Node != "住宅代理2" {
		t.Fatalf("应当只返回最后一条，实际 %+v", next)
	}
}

// TestNetworkLogTrimKeepsCursorWorking 锁死裁剪之后游标仍然正确。
//
// 裁剪会让最旧的条目消失 —— 此时给一个"早就被裁掉"的游标，
// 必须返回**保留下来的全部**，而不是空、更不能崩（二分查找的边界）。
func TestNetworkLogTrimKeepsCursorWorking(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})

	total := netLogCap + 10
	for i := 0; i < total; i++ {
		a.appendNetworkLog("n", "info", "x")
	}

	kept, latest := a.NetworkLogs(0)
	if len(kept) != netLogCap {
		t.Fatalf("应当裁到 %d 条，实际 %d 条", netLogCap, len(kept))
	}
	if int64(latest) != int64(total) {
		t.Fatalf("序号应当一直递增到 %d，实际 %d", total, latest)
	}
	if kept[0].Seq != int64(total-netLogCap+1) {
		t.Fatalf("保留的应当是最新的那批，首条序号 %d（期望 %d）", kept[0].Seq, total-netLogCap+1)
	}

	// 一个早在裁剪前就被丢掉的游标：应当返回全部保留项，而不是空
	got, _ := a.NetworkLogs(1)
	if len(got) != netLogCap {
		t.Fatalf("被裁掉的游标应当返回全部保留项，实际 %d 条", len(got))
	}
}

// TestClearNetworkLogs 清空之后序号继续递增（界面游标不会因此回退）。
func TestClearNetworkLogs(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})
	a.appendNetworkLog("n", "info", "第一条")
	_, latest := a.NetworkLogs(0)

	a.ClearNetworkLogs()
	if got, _ := a.NetworkLogs(0); len(got) != 0 {
		t.Fatalf("清空后不该有内容，实际 %d 条", len(got))
	}

	a.appendNetworkLog("n", "info", "清空后的第一条")
	// 界面会拿着清空前的游标来问，必须只拿到新那条
	got, newLatest := a.NetworkLogs(latest)
	if len(got) != 1 || got[0].Text != "清空后的第一条" {
		t.Fatalf("应当只返回清空后的新条目，实际 %+v", got)
	}
	if newLatest != latest+1 {
		t.Fatalf("序号应当继续递增，previous=%d now=%d", latest, newLatest)
	}
}
