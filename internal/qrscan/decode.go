// Package qrscan 提供「从屏幕上圈选一块矩形 → 识别其中的二维码」所需的底层能力：
// Windows 下基于 GDI 的纯 syscall 截图，以及基于 gozxing 的跨平台二维码识别。
//
// 这里刻意不依赖 cgo / x/image：ChainProxy 要保证「单个 exe、零外部依赖」，
// 而 CGO_ENABLED=0 也决定了任何需要 C 编译器的截图库都用不了。
//
// 本包只负责「截图 + 识别」；全屏圈选窗口属于上层 UI 的工作。
package qrscan

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// maxScalePixels 是单轮放大后允许的最大像素数。
//
// 用户很可能把整块 4K 屏都圈进来（约 830 万像素），再放大 3 倍就是 7500 万像素、
// 光 RGBA 缓冲就要 300MB，多轮叠加足以把内存打满；超过上限的轮次直接不参与尝试。
const maxScalePixels = 64 << 20

// tryHarderHints 让 ZXing 的检测器多试几种定位方式。
// 故意只用在放大/二值化这些「已经失败过一次」的轮次上：图够清楚时第一轮就命中了，
// 不必为它付 TRY_HARDER 的时间开销。
var tryHarderHints = map[gozxing.DecodeHintType]interface{}{
	gozxing.DecodeHintType_TRY_HARDER: true,
}

// decodeAttempt 描述一轮识别尝试。
//
// build 写成闭包而不是直接持有一张图：整屏放大 2/3 倍的缓冲非常大，
// 只有真正轮到这一轮时才值得分配，用完即可回收。
type decodeAttempt struct {
	name   string
	hints  map[gozxing.DecodeHintType]interface{}
	build  func() image.Image
	pixels int // build 结果的像素总量，构造阶段用于判断是否超限
}

// Decode 识别图片里的二维码，返回其中的文本。
//
// 屏幕截图 / 手机截图里的二维码往往只有几十像素宽，而 ZXing 的检测器要求每个模块
// 至少有若干像素才认得出，所以这里做多轮预处理重试：
//
//  1. 原图（默认参数，最快）
//  2. 灰度 + 最近邻放大 2 倍
//  3. 灰度 + 最近邻放大 3 倍
//  4. 几档全局固定阈值二值化 + 放大 2 倍
//     （默认的 HybridBinarizer 是局部自适应阈值，遇到灰底/渐变背景容易糊成一片）
//
// 任一轮成功立即返回；全部失败才报错，错误里会说明一共试了几种预处理。
func Decode(img image.Image) (string, error) {
	if img == nil {
		return "", errors.New("qrscan: 图片为空")
	}
	if b := img.Bounds(); b.Dx() <= 0 || b.Dy() <= 0 {
		return "", fmt.Errorf("qrscan: 图片尺寸非法 %v", b)
	}

	attempts := buildAttempts(img)
	var (
		lastErr  error
		notFound int
	)
	for _, a := range attempts {
		text, err := decodeOne(a.build(), a.hints)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if isNotFound(err) {
			// 统计一下有几轮是「压根没找到条码」，方便排查是图太小还是图不对。
			notFound++
		}
	}
	if lastErr == nil {
		// 兜底：正常情况下 attempts 非空且最后一轮一定有错误。
		return "", errors.New("qrscan: 没有可用的识别方案")
	}
	return "", fmt.Errorf("qrscan: 未识别出二维码，已尝试 %d 种预处理（其中 %d 次报告未找到条码），最后错误: %w",
		len(attempts), notFound, lastErr)
}

// DecodePNG 从 PNG 字节识别二维码，用于剪贴板里的图片。
func DecodePNG(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("qrscan: PNG 数据为空")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("qrscan: PNG 解码失败: %w", err)
	}
	return Decode(img)
}

// buildAttempts 生成按「代价从低到高」排序的尝试列表。
func buildAttempts(img image.Image) []decodeAttempt {
	bounds := img.Bounds()
	base := bounds.Dx() * bounds.Dy()

	// 灰度图只算一次，几轮放大/二值化共用。
	var gray *image.RGBA
	grayOnce := func() *image.RGBA {
		if gray == nil {
			gray = toGray(img)
		}
		return gray
	}

	attempts := []decodeAttempt{{
		name:   "原图",
		build:  func() image.Image { return img },
		pixels: base,
	}}

	for _, factor := range []int{2, 3} {
		f := factor
		if base*f*f > maxScalePixels {
			// 放大后过大，跳过（例如整屏 4K × 3 倍）。
			continue
		}
		attempts = append(attempts, decodeAttempt{
			name:   fmt.Sprintf("灰度+放大%d倍", f),
			hints:  tryHarderHints,
			build:  func() image.Image { return scaleNearest(grayOnce(), f) },
			pixels: base * f * f,
		})
	}

	const binarizeScale = 2
	if base*binarizeScale*binarizeScale <= maxScalePixels {
		for _, threshold := range []uint8{96, 128, 160} {
			th := threshold
			attempts = append(attempts, decodeAttempt{
				name:   fmt.Sprintf("全局阈值%d二值化+放大%d倍", th, binarizeScale),
				hints:  tryHarderHints,
				build:  func() image.Image { return scaleNearest(binarize(grayOnce(), th), binarizeScale) },
				pixels: base * binarizeScale * binarizeScale,
			})
		}
	}

	return attempts
}

// decodeOne 跑一轮 gozxing 识别。
func decodeOne(img image.Image, hints map[gozxing.DecodeHintType]interface{}) (text string, err error) {
	// gozxing 是 ZXing 的机械移植，个别检测器分支在纯噪声图上会数组越界 panic。
	// 这是 GUI 的调用路径，宁可返回一个错误也不要把整个程序带崩。
	defer func() {
		if r := recover(); r != nil {
			text = ""
			err = fmt.Errorf("qrscan: 识别器内部 panic: %v", r)
		}
	}()

	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", err
	}
	result, err := qrcode.NewQRCodeReader().Decode(bmp, hints)
	if err != nil {
		return "", err
	}
	// 空内容的二维码对上层毫无意义（导入节点会拿到空串），当成本轮失败继续试。
	if result.GetText() == "" {
		return "", errors.New("qrscan: 识别到了二维码但内容为空")
	}
	return result.GetText(), nil
}

// isNotFound 判断错误是否为「没找到二维码」。
func isNotFound(err error) bool {
	var nf gozxing.NotFoundException
	return errors.As(err, &nf)
}

// toGray 把任意图片转成灰度，并且强制不透明。
//
// 必须强制 a=255：gozxing 取色时走的是 image.Image.At().RGBA()，
// 那是 **alpha 预乘** 后的值，半透明像素的 RGB 会偏小、全透明像素会直接变成白，
// 二维码边界会被抹掉；而 GDI 截图拿到的 alpha 又常常是 0。
func toGray(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			// 0.299R + 0.587G + 0.114B，整数近似并 +500 做四舍五入。
			lum := uint8((299*(r>>8) + 587*(g>>8) + 114*(bl>>8) + 500) / 1000)
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = lum, lum, lum, 0xFF
		}
	}
	return dst
}

// binarize 用固定阈值把灰度图压成纯黑白。
// 固定阈值和 gozxing 默认的局部自适应阈值是两种互补的失败模式，多试一档往往就过。
func binarize(gray *image.RGBA, threshold uint8) *image.RGBA {
	b := gray.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			i := gray.PixOffset(x, y)
			var v uint8
			if gray.Pix[i] >= threshold {
				v = 0xFF
			}
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = v, v, v, 0xFF
		}
	}
	return dst
}

// scaleNearest 用最近邻法把图放大 factor 倍。
//
// 手写而不是引 golang.org/x/image/draw：这里只是想把二维码的模块撑大，
// 最近邻不会引入插值模糊（插值反而会破坏黑白边界），也就没必要多一个依赖。
func scaleNearest(src *image.RGBA, factor int) *image.RGBA {
	if factor <= 1 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx()*factor, b.Dy()*factor
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	// 用 Stride 而不是 width*4：image.RGBA 的行间可能有额外填充。
	srcStride, dstStride := src.Stride, dst.Stride
	for y := 0; y < h; y++ {
		srcRow := src.Pix[y/factor*srcStride : y/factor*srcStride+b.Dx()*4]
		dstRow := dst.Pix[y*dstStride : y*dstStride+w*4]
		for x := 0; x < w; x++ {
			sx := x / factor * 4
			copy(dstRow[x*4:x*4+4], srcRow[sx:sx+4])
		}
	}
	return dst
}
