//go:build windows

package gui

// 界面配色。刻意保持"浅色 + 一个重点色"，避免深浅色主题在不同系统上失配。
var (
	colText     = rgb(0x1F, 0x23, 0x28)
	colMuted    = rgb(0x6B, 0x72, 0x80)
	colAccent   = rgb(0x1F, 0x6F, 0xEB)
	colRunning  = rgb(0x16, 0xA3, 0x4A)
	colStopped  = rgb(0x8A, 0x8F, 0x98)
	colError    = rgb(0xD9, 0x2D, 0x2D)
	colDisabled = rgb(0xA8, 0xAE, 0xB8)
	colHeaderBg = rgb(0xF2, 0xF5, 0xF9)
	colPanelBg  = rgb(0xF7, 0xF9, 0xFB)
	colLine     = rgb(0xD8, 0xDE, 0xE6)
	colWhite    = rgb(0xFF, 0xFF, 0xFF)
)

// uiFont 是一组界面字体句柄，由 initFonts 创建、窗口销毁时释放。
type uiFont struct {
	regular uintptr
	bold    uintptr
	mono    uintptr
	tiny    uintptr
	title   uintptr
}

// initFonts 创建界面字体。
//
// 注意：这里**不再**用 "where segoeui.ttf" 探测字体是否存在。
// 原因：那会在启动路径上 fork 一个 cmd 子进程，在受作业对象/管道限制的环境里
// 会偶发卡住（实测导致整个界面永远不显示、窗口假死、消息循环不启动）。
// CreateFontW 对缺失字体会静默替换，统一用 "Segoe UI" 即可。
func initFonts(dpi int32) *uiFont {
	scale := func(v int32) int32 {
		if dpi <= 0 {
			return v
		}
		return int32(float64(v) * float64(dpi) / 96.0)
	}
	face := "Segoe UI"
	mono := "Consolas"
	return &uiFont{
		regular: createFont(-scale(16), 0, 0, 0, 400, 0, 0, 0, "Segoe UI", face),
		bold:    createFont(-scale(16), 0, 0, 0, 700, 0, 0, 0, "Segoe UI", face),
		mono:    createFont(-scale(14), 0, 0, 0, 400, 0, 0, 0, mono, mono),
		tiny:    createFont(-scale(14), 0, 0, 0, 400, 0, 0, 0, "Segoe UI", face),
		title:   createFont(-scale(21), 0, 0, 0, 700, 0, 0, 0, "Segoe UI", face),
	}
}
func (f *uiFont) destroy() {
	for _, h := range []uintptr{f.regular, f.bold, f.mono, f.tiny, f.title} {
		if h != 0 {
			deleteObject(h)
		}
	}
}

// scaleDPI 把按 96 DPI 设计的长度换算到当前 DPI。
func scaleDPI(v int32, dpi int32) int32 {
	if dpi <= 0 {
		return v
	}
	return int32(float64(v) * float64(dpi) / 96.0)
}

// dpix 批量换算。
func dpix(dpi int32, vs ...int32) []int32 {
	out := make([]int32, len(vs))
	for i, v := range vs {
		out[i] = scaleDPI(v, dpi)
	}
	return out
}

// humanBytes 把字节数格式化成人类可读形式。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return itoa64(n) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB"}
	if exp >= len(units) {
		exp = len(units) - 1
	}
	return formatFloat(float64(n)/float64(div)) + " " + units[exp]
}

func formatFloat(f float64) string {
	s := formatFloatPrec(f, 1)
	return s
}

func formatFloatPrec(f float64, prec int) string {
	// 简单四舍五入到指定小数位，避免引入 fmt 的开销其实无所谓，这里保持简单。
	pow := 1.0
	for i := 0; i < prec; i++ {
		pow *= 10
	}
	v := f * pow
	if v >= 0 {
		v = float64(int64(v + 0.5))
	} else {
		v = float64(int64(v - 0.5))
	}
	neg := v < 0
	if neg {
		v = -v
	}
	ip := int64(v) / int64(pow)
	fp := int64(v) % int64(pow)
	s := itoa64(ip)
	if prec > 0 {
		s += "."
		fs := itoa64(fp)
		for len(fs) < prec {
			fs = "0" + fs
		}
		s += fs
	}
	if neg {
		s = "-" + s
	}
	return s
}
