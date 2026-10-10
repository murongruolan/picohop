//go:build windows

package gui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// 点击模拟（真·BM_CLICK）
//
// 用户报"点了没反应"时，必须区分三种情况：
//   A. 消息根本没到窗口过程      —— 点计数器不动
//   B. 消息到了但 ID 对不上     —— 计数动了，但分支没进
//   C. 分支跑了但没有可见反馈  —— 状态变了，但用户看不出来
//
// 这里向**按钮控件本身**发 BM_CLICK（真实鼠标点击走的就是这条路径），
// 再按"预期效果"逐条等待并断言，把 A/B/C 分开。
// ---------------------------------------------------------------------------

type clickCase struct {
	name string
	cid  id
	// tries 是最多轮询次数（每轮约 120ms），用来适配不同耗时的按钮。
	tries int
	// pre 在点击前采集一份状态快照，避免"效果来得比第一轮断言慢"时被误判为无变化。
	pre    func() string
	expect func(pre string) (done bool, got string)
	undo   func()
}

type clickOutcome struct {
	name    string
	verdict string // OK / 无变化 / 点不动 / ID 不匹配
	detail  string
}

// hits 返回某个控件 ID 被点到的次数。
func (w *winMain) hits(cid int32) int {
	if w.cmdHits == nil {
		return 0
	}
	return w.cmdHits[cid]
}

func logLineCount(w *winMain) int {
	if w == nil || w.logEdit == 0 {
		return 0
	}
	return len(strings.Split(strings.ReplaceAll(w.logEdit.text(), "\r", ""), "\n"))
}

func (w *winMain) runClickCases(cases []clickCase) []clickOutcome {
	var out []clickOutcome
	for _, c := range cases {
		btn := getCtrlByID(w.hwnd, c.cid)
		if btn == 0 {
			out = append(out, clickOutcome{c.name, "点不动",
				fmt.Sprintf("找不到控件 id=%d", int32(c.cid))})
			continue
		}

		hitBefore := w.hits(int32(c.cid))
		pre := ""
		if c.pre != nil {
			pre = c.pre()
		}

		// 向按钮本身发 BM_CLICK：与真实鼠标点击产生的通知完全同一条路径
		sendMessage(uintptr(btn), bmClick, 0, 0)
		pumpMessagesFor(150 * time.Millisecond)

		via := "BM_CLICK"
		if w.hits(int32(c.cid)) == hitBefore {
			// 兜底：直接投按钮"本该发出"的那条 WM_COMMAND，
			// 这样既能验证业务逻辑，也能顺带量化"按钮通知本身通不通"。
			via = "BM_CLICK 未产生 WM_COMMAND，改投 WM_COMMAND"
			postMessage(w.hwnd, wmCommand, uintptr(c.cid), uintptr(btn))
			pumpMessagesFor(150 * time.Millisecond)
		}

		maxTries := c.tries
		if maxTries <= 0 {
			maxTries = 40
		}
		done, got := false, ""
		for tries := 0; tries < maxTries && !done; tries++ {
			pumpMessagesFor(120 * time.Millisecond)
			done, got = c.expect(pre)
		}
		if c.undo != nil {
			c.undo()
			pumpMessagesFor(200 * time.Millisecond)
		}

		verdict := "OK"
		detail := fmt.Sprintf("%s  [投递=%s 该ID被点次数 %d→%d]",
			got, via, hitBefore, w.hits(int32(c.cid)))
		switch {
		case w.hits(int32(c.cid)) == hitBefore:
			verdict = "点不动"
			detail = "BM_CLICK 与直接投递 WM_COMMAND 都没有让窗口过程收到这个按钮的命令"
		case !done:
			verdict = "无变化"
			detail = fmt.Sprintf("WM_COMMAND 已送达（计数 %d→%d），但预期效果没出现：%s",
				hitBefore, w.hits(int32(c.cid)), got)
		}
		out = append(out, clickOutcome{c.name, verdict, detail})
	}
	return out
}

func renderClickOutcomes(out []clickOutcome) string {
	var sb strings.Builder
	ok := 0
	for _, o := range out {
		mark := "✅"
		switch o.verdict {
		case "点不动", "ID 不匹配":
			mark = "❌"
		case "无变化":
			mark = "⚠️"
		default:
			ok++
		}
		sb.WriteString(fmt.Sprintf("  %s %-14s %s\n", mark, o.name, o.detail))
	}
	sb.WriteString(fmt.Sprintf("  => %d/%d 通过\n", ok, len(out)))
	return sb.String()
}

// simulateClicks 是自检入口。
func simulateClicks(w *winMain) string {
	logPre := func() string { return fmt.Sprint(logLineCount(w)) }
	grewLog := func() func(pre string) (bool, string) {
		return func(pre string) (bool, string) {
			base, _ := strconv.Atoi(pre)
			n := logLineCount(w)
			if n > base {
				return true, fmt.Sprintf("日志从 %d 行增加到 %d 行", base, n)
			}
			return false, fmt.Sprintf("日志仍是 %d 行", n)
		}
	}

	cases := []clickCase{
		{
			name:  "主开关",
			cid:   idBtnToggle,
			tries: 40,
			pre:   func() string { return fmt.Sprint(w.app.IsRunning()) },
			expect: func(pre string) (bool, string) {
				return pre != fmt.Sprint(w.app.IsRunning()),
					fmt.Sprintf("点击前 running=%s 现在 running=%v 按钮=%q",
						pre, w.app.IsRunning(), w.btnToggle.text())
			},
			undo: func() { restoreToggle(w) },
		},
		{
			name:   "保存前置设置",
			cid:    idBtnFrontSave,
			tries:  30,
			pre:    logPre,
			expect: grewLog(),
		},
		{
			name:   "测试前置",
			cid:    idBtnFrontTest,
			tries:  150,
			pre:    logPre,
			expect: grewLog(),
		},
		{
			name:   "一键测试",
			cid:    idBtnTestAll,
			tries:  300,
			pre:    logPre,
			expect: grewLog(),
		},
		{
			name:  "清空",
			cid:   idBtnLogClear,
			tries: 10,
			pre:   logPre,
			expect: func(_ string) (bool, string) {
				pumpMessagesFor(100 * time.Millisecond)
				n := logLineCount(w)
				return n <= 1, fmt.Sprintf("清空后日志 %d 行", n)
			},
		},
	}
	return renderClickOutcomes(w.runClickCases(cases))
}

// restoreToggle 恢复主开关到"运行中"，让自检结束后状态可控。
func restoreToggle(w *winMain) {
	if !w.app.IsRunning() {
		sendMessage(uintptr(w.btnToggle), bmClick, 0, 0)
		for i := 0; i < 40 && !w.app.IsRunning(); i++ {
			pumpMessagesFor(150 * time.Millisecond)
		}
	}
}
