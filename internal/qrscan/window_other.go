//go:build !windows

package qrscan

import (
	"errors"
	"image"
)

// CaptureWindow 在非 Windows 平台不可用。
func CaptureWindow(hwnd uintptr) (*image.RGBA, error) {
	return nil, errors.New("qrscan: 窗口截图仅支持 Windows")
}

// LooksBlank 与平台无关（纯像素判断）。
func LooksBlank(img *image.RGBA) bool {
	if img == nil {
		return true
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return true
	}
	stepX := maxInt(1, b.Dx()/32)
	stepY := maxInt(1, b.Dy()/32)
	var first [4]byte
	firstSet := false
	differing, total := 0, 0
	for y := b.Min.Y; y < b.Max.Y; y += stepY {
		for x := b.Min.X; x < b.Max.X; x += stepX {
			i := img.PixOffset(x, y)
			px := [4]byte{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}
			total++
			if !firstSet {
				first = px
				firstSet = true
				continue
			}
			if absInt(int(px[0])-int(first[0])) > 2 ||
				absInt(int(px[1])-int(first[1])) > 2 ||
				absInt(int(px[2])-int(first[2])) > 2 {
				differing++
			}
		}
	}
	if total == 0 {
		return true
	}
	return differing*100 < total*2
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
