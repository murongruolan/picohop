//go:build !windows

package qrscan

import (
	"errors"
	"image"
)

// ErrCancelled 表示用户取消了圈选（按 Esc、点右键、或选了空区域）。
//
// 这个变量在两个平台的文件里都必须存在（而不是只在 overlay_windows.go 里），
// 否则上层的 switch 判断 err == qrscan.ErrCancelled 在非 Windows 构建下
// 会直接编译不过。
var ErrCancelled = errors.New("已取消圈选")

// errOverlayUnsupported 与 screen_other.go 的 errScreenUnsupported 分开写，
// 因为这两个错误的触发原因不同（一个是"截不了屏"，一个是"没有圈选交互"），
// 上层可能需要区分展示。
var errOverlayUnsupported = errors.New("屏幕圈选仅支持 Windows")

// SelectRegion 在非 Windows 平台上总是失败。
//
// 圈选遮罩完全建立在 Win32 的 CreateWindowExW + 消息循环之上，
// 没有跨平台的等价物；这里只保证上层代码在所有平台都能编译过。
func SelectRegion() (image.Rectangle, error) {
	return image.Rectangle{}, errOverlayUnsupported
}

// SelectRegionHooked 与 SelectRegion 相同（非 Windows 平台上同样直接失败），
// 但会在遮罩窗口创建完成后回调 onReady。
//
// 这里保留同名同签名的桩，是为了让跨平台编译和测试代码（overlay_test.go
// 带 windows tag）在任何平台都不会因为缺少符号而失败。
func SelectRegionHooked(onReady func(hwnd uintptr)) (image.Rectangle, error) {
	return image.Rectangle{}, errOverlayUnsupported
}
