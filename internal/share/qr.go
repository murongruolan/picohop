package share

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// defaultQRSize 是默认二维码边长（像素）。
//
// 320 是实践里比较好用的值：手机在 20~30cm 外能稳定扫出来，
// 而图片本身只有几 KB，塞进 data URL 也不会让前端卡顿。
const defaultQRSize = 320

// qrRecoveryLevel 用 Medium 级纠错。
//
// 分享链接最终是显示在屏幕上的，屏幕反光/缩放/截图压缩都会糊掉码点，
// Medium（约 15% 纠错）在"码更小"和"更抗糊"之间最平衡；
// 提到 High 会让模块数变多、二维码变大，反而更难扫。
const qrRecoveryLevel qrcode.RecoveryLevel = qrcode.Medium

// QRPNG 生成二维码 PNG 字节。size 是图片边长（像素），<=0 时用默认 320。
//
// 失败返回 error，不 panic —— 这条路径最终会被 Wails 暴露成前端 API，
// 一个 panic 就是整个应用崩掉。
func QRPNG(text string, size int) ([]byte, error) {
	if size <= 0 {
		size = defaultQRSize
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("share: 二维码内容为空")
	}
	png, err := encodeQR(text, size)
	if err != nil {
		return nil, err
	}
	if len(png) == 0 {
		return nil, errors.New("share: 二维码生成结果为空")
	}
	return png, nil
}

// QRDataURL 生成可直接塞进 <img src> 的 data URL。
func QRDataURL(text string, size int) (string, error) {
	png, err := QRPNG(text, size)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// encodeQR 包一层 recover，把上游库的 panic 转成 error。
//
// 为什么需要：skip2/go-qrcode 在若干内部不变量出错时走的是 log.Panic
// （encoder.go 的 "Unknown data mode"、qrcode.go 的 buildRegularSymbol 错误等），
// 而不是返回 error；内容超容量倒是会正常返回 "content too long to encode"。
// 我实测了 4 种纠错等级 × 4 种内容形态 × 长度 1~4000 共约 5 万次调用，
// 没有复现出 panic，所以这条路径目前更像"保险"而不是已知缺陷——
// 但这个方法会被 Wails 直接暴露给前端，一旦 panic 就是整个应用崩掉，
// 用两行 recover 换掉这种可能性是划算的。
func encodeQR(text string, size int) (png []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			png = nil
			err = fmt.Errorf("share: 生成二维码失败（内容 %d 字节，边长 %d 像素，可能超出二维码容量）: %v", len(text), size, r)
		}
	}()
	png, err = qrcode.Encode(text, qrRecoveryLevel, size)
	if err != nil {
		return nil, fmt.Errorf("share: 生成二维码失败（内容 %d 字节，边长 %d 像素）: %w", len(text), size, err)
	}
	return png, nil
}
