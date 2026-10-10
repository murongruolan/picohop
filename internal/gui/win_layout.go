//go:build windows

package gui

import (
	"fmt"
	"sort"
	"strings"
	"unsafe"
)

// ---------------------------------------------------------------------------
// 布局体检工具
//
// 目的：Win32 自绘界面最容易出的两类毛病，人眼看截图才发现得了，自动化里很难断言：
//   1. 文字比控件宽 —— 按钮/分组标题被截断或溢出；
//   2. 控件排到窗口客户区之外 —— 右边/下边看不见。
//
// 这里用 GDI 的 GetTextExtentPoint32W 精确量出每个控件的文字宽度，
// 与控件实际客户宽度比对，输出"溢出报告"；同时报告超出客户区的控件。
// 配合 CHAINPROXY_UI_DUMP 使用，是 GUI 改版后的必跑检查项。
// ---------------------------------------------------------------------------

// measureFont 为给定的字体句柄创建一个临时 DC / 字体组合。
type fontMetrics struct {
	dc     uintptr
	oldFn  uintptr
	broken bool
}

func newFontMetrics(font uintptr) *fontMetrics {
	hdc := getDCZero(0)
	if hdc == 0 {
		return &fontMetrics{broken: true}
	}
	old := selectObject(hdc, font)
	return &fontMetrics{dc: hdc, oldFn: old}
}

func (m *fontMetrics) textWidth(s string) int {
	if m == nil || m.broken {
		return 0
	}
	return getTextExtentPoint32(m.dc, s)
}

func (m *fontMetrics) close() {
	if m == nil || m.broken {
		return
	}
	selectObject(m.dc, m.oldFn)
	releaseDCZero(0, m.dc)
}

// layoutReport 是一次布局体检的结论。
type layoutReport struct {
	ClientW, ClientH int32
	OverflowControls []string // 超出客户区的控件
	OverflowText     []string // 文字比控件宽的控件
}

// checkLayout 遍历主窗口所有子控件，产出布局体检报告。
func checkLayout(hwnd uintptr) layoutReport {
	var rep layoutReport
	var cr RECT
	pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&cr)))
	rep.ClientW = cr.Right - cr.Left
	rep.ClientH = cr.Bottom - cr.Top

	// 控件坐标是相对客户的，GetWindowRect 是屏幕坐标，
	// 这里统一换算成客户区相对坐标再比较。
	var wr RECT
	pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
	offX := wr.Left - cr.Left
	offY := wr.Top - cr.Top

	fm := newFontMetrics(0)
	defer fm.close()

	for _, e := range collectChildWindows(hwnd) {
		if !e.vis {
			continue
		}
		x := e.x - offX
		y := e.y - offY

		// 超出客户区（左/上/右/下都要查）。
		// 之前只查了右/下，结果"标签被推到负坐标"这种错位一直没被发现。
		if x < 0 || y < 0 || x+e.w > rep.ClientW || y+e.h > rep.ClientH {
			dir := ""
			if x < 0 {
				dir += " 左边越界"
			}
			if y < 0 {
				dir += " 上边越界"
			}
			if x+e.w > rep.ClientW {
				dir += " 右边越界"
			}
			if y+e.h > rep.ClientH {
				dir += " 下边越界"
			}
			rep.OverflowControls = append(rep.OverflowControls,
				fmt.Sprintf("%s id=%d (%d,%d)+%dx%d 客户区=%dx%d%s",
					e.class, e.id, x, y, e.w, e.h, rep.ClientW, rep.ClientH, dir))
		}

		// 文字比控件宽（只看有文字的 STATIC/BUTTON）
		txt := strings.TrimSpace(strings.ReplaceAll(e.text, "\r", ""))
		if txt == "" {
			continue
		}
		switch e.class {
		case "Static", "Button":
		default:
			continue
		}

		var fr RECT
		pGetClientRect.Call(e.hwnd, uintptr(unsafe.Pointer(&fr)))
		availW := int(fr.Right - fr.Left)
		if availW <= 0 {
			continue
		}
		font := sendMessage(e.hwnd, 0x0031 /* WM_GETFONT */, 0, 0) //nolint
		m := fm
		if font != 0 {
			m = newFontMetrics(font)
		}
		need := m.textWidth(txt) + controlTextPad(e.class)
		if m != fm {
			m.close()
		}
		if need > availW {
			rep.OverflowText = append(rep.OverflowText,
				fmt.Sprintf("%-12s id=%-7d 控件宽=%-4d 文字需宽=%-4d 超出=%-3d 文本=%q",
					e.class, e.id, availW, need, need-availW, truncForReport(txt)))
		}
	}
	sort.Strings(rep.OverflowControls)
	sort.Strings(rep.OverflowText)
	return rep
}

// controlTextPad 是控件文字之外必须预留的边框/内边距。
// BUTTON 在 Windows 上左右各约 4~8 像素且带焦点虚线框，静态控件也要留一点余量。
func controlTextPad(class string) int {
	if class == "Button" {
		return 20
	}
	return 6
}

func truncForReport(s string) string {
	r := []rune(s)
	if len(r) <= 34 {
		return s
	}
	return string(r[:34]) + "…"
}

// render 把体检报告写成文本。
func (r layoutReport) render() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("客户区尺寸 = %d x %d\n", r.ClientW, r.ClientH))

	sb.WriteString(fmt.Sprintf("\n超出客户区的控件：%d 个\n", len(r.OverflowControls)))
	for _, s := range r.OverflowControls {
		sb.WriteString("  " + s + "\n")
	}

	sb.WriteString(fmt.Sprintf("\n文字宽度超过控件宽度：%d 个\n", len(r.OverflowText)))
	for _, s := range r.OverflowText {
		sb.WriteString("  " + s + "\n")
	}

	if len(r.OverflowControls) == 0 && len(r.OverflowText) == 0 {
		sb.WriteString("  ✅ 没有发现布局问题\n")
	}
	return sb.String()
}
