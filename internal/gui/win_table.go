//go:build windows

package gui

import (
	"fmt"
	"strings"

	"chainproxy/internal/app"
)

// ---------------------------------------------------------------------------
// 住宅代理列表：表格渲染
//
// 为什么不用 SysListView32？
// ---------------------------------------------------------------------------
// ListView 需要跨进程边界传递 LVITEM 结构体（mask/iItem/iSubItem/pszText…），
// 这条通路对结构体内存布局、GC 与 Windows 消息时序都很敏感：
// 实测中 LVCOLUMN 的 mask 一旦误开 LVCF_IMAGE 就会让控件卡死，
// 而 LVM_SETITEMTEXT 的返回值与文本落地都不稳定。
//
// 由于本项目只需要"看得清、能选中、不崩"，这里改用
// **等宽字体的只读多行 EDIT** 自己画表格：
//   - 零结构体编组，行为完全确定；
//   - 中文按双宽计算列宽，对齐不会错位；
//   - 选中行用 "▶" 标记，↑/↓ 键切换；
//   - 状态用 ● / ○ / ⚠ 区分（真正的颜色提示在顶部状态栏与每行前缀里）。
//
// 详见 docs/COMMIT_HISTORY.md 的 003 条目。

// 列定义：标题与宽度（显示宽度，中文算 2）。
type tableCol struct {
	title string
	width int
}

var upstreamCols = []tableCol{
	{"", 2},     // 选择标记
	{"状态", 4},   // ● / ○ / ⚠
	{"名称", 22},  //
	{"类型", 10},  //
	{"本地端口", 9}, //
	{"住宅服务器", 40},
	{"流量 ↑/↓", 22},
	{"状态说明", 46},
}

// displayWidth 计算字符串在等宽字体下的显示宽度（东亚全角算 2）。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWide(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // 韩文字母
		r >= 0x2E80 && r <= 0x303E,                         // CJK 部首、标点
		r >= 0x3041 && r <= 0x33FF,                         // 假名、注音、CJK 兼容
		r >= 0x3400 && r <= 0x4DBF,                         // CJK 扩展 A
		r >= 0x4E00 && r <= 0x9FFF,                         // CJK 统一表意文字
		r >= 0xA000 && r <= 0xA4CF,                         // 彝文
		r >= 0xAC00 && r <= 0xD7A3,                         // 韩文音节
		r >= 0xF900 && r <= 0xFAFF,                         // CJK 兼容表意文字
		r >= 0xFF00 && r <= 0xFF60,                         // 全角 ASCII
		r >= 0xFFE0 && r <= 0xFFE6,                         // 全角符号
		r == 0x3000,                                        // 表意空格
		r == 0x2022, r == 0x25CF, r == 0x25CB, r == 0x26A0: // • ● ○ ⚠
		return true
	}
	return false
}

// clip 按显示宽度截断字符串。
func clip(s string, w int) string {
	if displayWidth(s) <= w {
		return s
	}
	acc, out := 0, make([]rune, 0, len(s))
	for _, r := range s {
		rw := 1
		if isWide(r) {
			rw = 2
		}
		if acc+rw > w-1 {
			out = append(out, '…')
			break
		}
		out = append(out, r)
		acc += rw
	}
	return string(out)
}

// padRight 把字符串补齐到指定显示宽度。
func padRight(s string, w int) string {
	s = clip(s, w)
	return s + strings.Repeat(" ", max(0, w-displayWidth(s)))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// renderTableHeader 生成表头行。
func renderTableHeader() string {
	var sb strings.Builder
	for _, c := range upstreamCols {
		if c.title == "" {
			sb.WriteString(strings.Repeat(" ", c.width))
			continue
		}
		sb.WriteString(padRight(c.title, c.width))
		sb.WriteString(" ")
	}
	return sb.String()
}

// renderUpstreamTable 把状态行渲染成等宽表格，selected 为当前选中行下标（-1 表示无）。
func renderUpstreamTable(rows []app.StatusRow, selected int) string {
	var sb strings.Builder
	if len(rows) == 0 {
		sb.WriteString("  还没有添加住宅代理。点击右上角「+ 添加住宅代理」开始。\r\n")
		sb.WriteString("  典型做法：前置填主流客户端的 127.0.0.1:10808，住宅代理填你的代理商给的 地址:端口:账号:密码。\r\n")
		return sb.String()
	}
	for i, r := range rows {
		mark := "  "
		if i == selected {
			mark = "▶ "
		}
		dot := "○"
		switch r.State {
		case app.StateRunning:
			dot = "●"
		case app.StateError:
			dot = "⚠"
		}

		name := r.Name
		traffic := "—"
		if r.State == app.StateRunning {
			traffic = humanBytes(r.UpBytes) + " / " + humanBytes(r.DownBytes)
		}
		detail := r.Detail
		if r.State == app.StateOff {
			detail = "已禁用（可在列表中启用）"
		}

		cells := []string{mark, dot, name, r.Type, fmt.Sprint(r.ListenPort), r.Server, traffic, detail}
		for ci, c := range upstreamCols {
			sb.WriteString(padRight(cells[ci], c.width))
			sb.WriteString(" ")
		}
		sb.WriteString("\r\n")
	}
	return sb.String()
}
