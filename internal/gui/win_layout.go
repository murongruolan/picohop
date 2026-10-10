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
	Overlaps         []string // 互相压住的控件
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
	rep.Overlaps = findOverlaps(hwnd, offX, offY)
	return rep
}

// box 是一个用于重叠检测的控件矩形。
type box struct {
	id    int32
	class string
	text  string
	x, y  int32
	w, h  int32
}

// findOverlaps 找出"互相压住"的控件。
//
// 为什么要单独做这件事：分组框（BS_GROUPBOX）**本来就应该**盖住里面的控件、
// 大面板里也本来就放着别的控件，所以不能无脑两两判重叠。
// 这里的做法是：先把"完全包含住另一个控件"的控件标记为容器并排除，
// 剩下的叶子控件之间才做两两相交检测。
// 之前"状态文字被右上角按钮盖住"就是这么发现的。
func findOverlaps(hwnd uintptr, offX, offY int32) []string {
	var boxes []box
	for _, e := range collectChildWindows(hwnd) {
		if !e.vis || e.w <= 0 || e.h <= 0 {
			continue
		}
		switch e.class {
		case "Button", "Static", "Edit", "ComboBox":
		default:
			continue
		}
		if e.class == "Button" && isGroupBox(e.hwnd) {
			continue // 分组框是容器，天然与子控件重叠
		}
		boxes = append(boxes, box{
			id: e.id, class: e.class, text: e.text,
			x: e.x - offX, y: e.y - offY, w: e.w, h: e.h,
		})
	}

	// 标记容器：完全包含另一个控件的，视为容器，本身不参与重叠判定
	isContainer := make([]bool, len(boxes))
	for i := range boxes {
		for j := range boxes {
			if i == j {
				continue
			}
			if contains(boxes[i], boxes[j]) {
				isContainer[i] = true
				break
			}
		}
	}

	var out []string
	for i := range boxes {
		if isContainer[i] {
			continue
		}
		for j := i + 1; j < len(boxes); j++ {
			if isContainer[j] {
				continue
			}
			if !intersects(boxes[i], boxes[j]) {
				continue
			}
			out = append(out, fmt.Sprintf("%s id=%d %q 与 %s id=%d %q 互相重叠（%d,%d %dx%d vs %d,%d %dx%d）",
				boxes[i].class, boxes[i].id, truncForReport(boxes[i].text),
				boxes[j].class, boxes[j].id, truncForReport(boxes[j].text),
				boxes[i].x, boxes[i].y, boxes[i].w, boxes[i].h,
				boxes[j].x, boxes[j].y, boxes[j].w, boxes[j].h))
		}
	}
	sort.Strings(out)
	return out
}

func contains(a, b box) bool {
	return b.x >= a.x && b.y >= a.y && b.x+b.w <= a.x+a.w && b.y+b.h <= a.y+a.h
}

func intersects(a, b box) bool {
	if a.x+a.w <= b.x || b.x+b.w <= a.x {
		return false
	}
	if a.y+a.h <= b.y || b.y+b.h <= a.y {
		return false
	}
	return true
}

// isGroupBox 判断按钮是不是 BS_GROUPBOX（风格位 0x0007，高 4 位为 7）。
func isGroupBox(h uintptr) bool {
	style := getWindowLongPtr(h, gwlStyle)
	return uint32(style)&0x0F == bsGroupBox
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

	sb.WriteString(fmt.Sprintf("\n互相重叠的控件：%d 对\n", len(r.Overlaps)))
	for _, s := range r.Overlaps {
		sb.WriteString("  " + s + "\n")
	}

	if len(r.OverflowControls) == 0 && len(r.OverflowText) == 0 && len(r.Overlaps) == 0 {
		sb.WriteString("  ✅ 没有发现布局问题\n")
	}
	return sb.String()
}
