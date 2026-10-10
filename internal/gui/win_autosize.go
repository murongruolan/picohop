//go:build windows

package gui

import (
	"strings"
)

// ---------------------------------------------------------------------------
// 按实测文字宽度自动排版
//
// 上一版界面所有宽度都是"拍脑袋估"的，中文渲染宽度比预期大得多，
// 造成 23 处文字溢出、8 个控件掉到客户区之外。
// 这里改成：**先用 GDI 量出真实宽度，再决定控件宽度和位置**，
// 排版不再依赖任何估算。
// ---------------------------------------------------------------------------

// dpiHeight 是 layout 内部用的 DPI 换算（layout 不持有 dpi 字段）。

// measure 用给定字体量一段文字的像素宽度。
func (l layout) measure(font uintptr, s string) int32 {
	m := newFontMetrics(font)
	defer m.close()
	return int32(m.textWidth(s))
}

// autoW 返回一个既不小于 min、又能装下 text 的控件宽度。
func (l layout) autoW(font uintptr, text string, minW int32, padW int32) int32 {
	w := l.measure(font, text) + padW
	if w < minW {
		return minW
	}
	return w
}

func (l layout) labelAuto(text string, cid id, x, y int32) (ctrl, int32) {
	w := l.measure(l.fRegular, text) + 10
	c := l.make(control{Class: "STATIC", Text: text, Style: wsChild | wsVisible | ssLeft, ID: cid}, x, y, w, l.s(18))
	return c, w
}

// labelRightAuto 右对齐的自适应标签（文字贴着输入框左边）。
func (l layout) labelRightAuto(text string, cid id, right int32, y int32) (ctrl, int32) {
	w := l.measure(l.fRegular, text) + 10
	c := l.make(control{Class: "STATIC", Text: text, Style: wsChild | wsVisible | ssRight, ID: cid}, right-w, y, w, l.s(18))
	return c, w
}

// buttonAuto 造一个宽度刚好装下文字的按钮。
// minW 是最小宽度；pad 预留按钮自身的边框与焦点虚线。
func (l layout) buttonAuto(text string, cid id, x, y, minW, h int32) (ctrl, int32) {
	w := l.autoW(l.fRegular, text, minW, 24)
	c := l.make(control{
		Class: "BUTTON", Text: text,
		Style: wsChild | wsVisible | wsTabStop | bsPushButton,
		ID:    cid,
	}, x, y, w, h)
	return c, w
}

// defButtonAuto 同 buttonAuto，但是默认按钮（回车即触发）。
func (l layout) defButtonAuto(text string, cid id, x, y, minW, h int32) (ctrl, int32) {
	w := l.autoW(l.fRegular, text, minW, 24)
	c := l.make(control{
		Class: "BUTTON", Text: text,
		Style: wsChild | wsVisible | wsTabStop | bsDefPushButton,
		ID:    cid,
	}, x, y, w, h)
	return c, w
}

// fitGroupTitle 截短分组标题，让它不超过给定宽度。
// 返回截短后的文本（必要时补省略号）。
func (l layout) fitGroupTitle(text string, maxW int32) string {
	if l.measure(l.fRegular, text)+8 <= maxW {
		return text
	}
	r := []rune(text)
	for len(r) > 4 {
		r = r[:len(r)-1]
		cand := strings.TrimRight(string(r), " —-·（）()") + "…"
		if l.measure(l.fRegular, cand)+8 <= maxW {
			return cand
		}
	}
	return "…"
}

// ellipsize 把文本截到不超过 maxW 像素宽（供状态栏等单行控件使用）。
func (l layout) ellipsize(text string, maxW int32) string {
	if l.measure(l.fRegular, text) <= maxW {
		return text
	}
	r := []rune(text)
	for len(r) > 1 {
		r = r[:len(r)-1]
		if l.measure(l.fRegular, strings.TrimRight(string(r), "…")+"…") <= maxW {
			return strings.TrimRight(string(r), "…") + "…"
		}
	}
	return ""
}
