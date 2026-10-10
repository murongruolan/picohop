//go:build !windows

package qrscan

import (
	"errors"
	"image"
)

// 屏幕截图依赖 Win32 GDI，非 Windows 平台只保留识别能力（Decode / DecodePNG
// 在 decode.go 里，不带 build tag），截图接口一律返回错误，方便上层无差别编译。
var errScreenUnsupported = errors.New("屏幕截图仅支持 Windows")

// VirtualScreen 在非 Windows 平台上总是失败。
func VirtualScreen() (image.Rectangle, error) {
	return image.Rectangle{}, errScreenUnsupported
}

// CaptureScreen 在非 Windows 平台上总是失败。
func CaptureScreen() (*image.RGBA, error) {
	return nil, errScreenUnsupported
}

// CaptureRect 在非 Windows 平台上总是失败。
func CaptureRect(r image.Rectangle) (*image.RGBA, error) {
	return nil, errScreenUnsupported
}
