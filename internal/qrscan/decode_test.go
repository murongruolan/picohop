package qrscan

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"
	"testing"

	"github.com/skip2/go-qrcode"
)

// testQRText 用一条真实的分享链接：既有长 URL 的量级，也顺带验证中文/特殊字符
// （# 片段）能原样读回。
const testQRText = "ss://YWVzLTEyOC1nY206cGFzc3dvcmQ@1.2.3.4:8388#PicoHop节点"

// mustQRImage 用 go-qrcode 生成一张二维码图片。
func mustQRImage(t *testing.T, size int) image.Image {
	t.Helper()
	data, err := qrcode.Encode(testQRText, qrcode.Medium, size)
	if err != nil {
		t.Fatalf("生成测试用二维码失败: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("解析生成的 PNG 失败: %v", err)
	}
	return img
}

// resizeNearest 是测试里的最近邻缩放（可放大也可缩小）。
// 不复用 scaleNearest：那个只做整数倍放大，和「任意目标尺寸」不是一回事。
func resizeNearest(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := b.Min.Y + y*b.Dy()/h
		for x := 0; x < w; x++ {
			sx := b.Min.X + x*b.Dx()/w
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

// TestDecodeGeneratedQR 覆盖「原图直接识别」。
func TestDecodeGeneratedQR(t *testing.T) {
	for _, size := range []int{160, 256, 500, 800} {
		t.Run(fmt.Sprintf("生成%dpx", size), func(t *testing.T) {
			img := mustQRImage(t, size)
			got, err := Decode(img)
			if err != nil {
				t.Fatalf("Decode(%v) 失败: %v", img.Bounds(), err)
			}
			if got != testQRText {
				t.Fatalf("内容不一致:\n得到 %q\n期望 %q", got, testQRText)
			}
		})
	}
}

// TestDecodeResizedQR 覆盖「同一张二维码被缩放成不同尺寸」，这是手机截图/缩略图的典型情况。
func TestDecodeResizedQR(t *testing.T) {
	src := mustQRImage(t, 600)
	for _, tc := range []struct{ w, h int }{
		{120, 120},
		{200, 200},
		{500, 500},
		{1024, 1024},
		{300, 260}, // 轻微非等比，模拟被拉伸过
	} {
		t.Run(fmt.Sprintf("%dx%d", tc.w, tc.h), func(t *testing.T) {
			img := resizeNearest(src, tc.w, tc.h)
			got, err := Decode(img)
			if err != nil {
				t.Fatalf("Decode(%dx%d) 失败: %v", tc.w, tc.h, err)
			}
			if got != testQRText {
				t.Fatalf("内容不一致:\n得到 %q\n期望 %q", got, testQRText)
			}
		})
	}
}

// TestDecodeQRInLargerScene 模拟真实场景：屏幕截图里二维码只占一小块，
// 四周是别的内容（这里用灰底代替）。
func TestDecodeQRInLargerScene(t *testing.T) {
	qr := resizeNearest(mustQRImage(t, 300), 150, 150)
	scene := image.NewRGBA(image.Rect(0, 0, 640, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 640; x++ {
			// 带一点渐变，故意不用纯色：局部自适应二值化在这种背景上容易失败。
			v := uint8(0x70 + (x*32)/640)
			scene.Set(x, y, color.RGBA{v, v, v, 0xFF})
		}
	}
	// 把二维码贴到 (240, 120)，四周留出大片背景。
	for y := 0; y < 150; y++ {
		for x := 0; x < 150; x++ {
			scene.Set(240+x, 120+y, qr.At(x, y))
		}
	}

	got, err := Decode(scene)
	if err != nil {
		t.Fatalf("Decode(大图里的小二维码) 失败: %v", err)
	}
	if got != testQRText {
		t.Fatalf("内容不一致:\n得到 %q\n期望 %q", got, testQRText)
	}
}

// TestDecodePNG 覆盖剪贴板路径：PNG 字节 → 文本。
func TestDecodePNG(t *testing.T) {
	data, err := qrcode.Encode(testQRText, qrcode.Medium, 256)
	if err != nil {
		t.Fatalf("生成测试用二维码失败: %v", err)
	}
	got, err := DecodePNG(data)
	if err != nil {
		t.Fatalf("DecodePNG 失败: %v", err)
	}
	if got != testQRText {
		t.Fatalf("内容不一致:\n得到 %q\n期望 %q", got, testQRText)
	}
}

// TestDecodePNGInvalid 保证坏输入返回错误而不是 panic。
func TestDecodePNGInvalid(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"空数据", nil},
		{"非 PNG", []byte("这不是一张 PNG 图片")},
		{"PNG 头被截断", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodePNG(tc.data); err == nil {
				t.Fatal("期望返回错误，实际成功了")
			}
		})
	}
}

// TestDecodeNonQRImage 纯白 / 纯噪声都不能识别出二维码，而且必须返回 error 而不是 panic。
func TestDecodeNonQRImage(t *testing.T) {
	t.Run("纯白", func(t *testing.T) {
		white := image.NewRGBA(image.Rect(0, 0, 256, 256))
		for i := range white.Pix {
			white.Pix[i] = 0xFF
		}
		if got, err := Decode(white); err == nil {
			t.Fatalf("纯白图不应该识别出内容，却得到 %q", got)
		} else if !strings.Contains(err.Error(), "已尝试") {
			t.Fatalf("错误信息里应说明尝试了几种预处理，实际: %v", err)
		}
	})

	t.Run("纯噪声", func(t *testing.T) {
		noise := image.NewRGBA(image.Rect(0, 0, 200, 200))
		rnd := rand.New(rand.NewSource(20261011))
		for i := 0; i < len(noise.Pix); i += 4 {
			noise.Pix[i] = uint8(rnd.Intn(256))
			noise.Pix[i+1] = uint8(rnd.Intn(256))
			noise.Pix[i+2] = uint8(rnd.Intn(256))
			noise.Pix[i+3] = 0xFF
		}
		if got, err := Decode(noise); err == nil {
			t.Fatalf("噪声图不应该识别出内容，却得到 %q", got)
		}
	})

	t.Run("尺寸非法", func(t *testing.T) {
		empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
		if _, err := Decode(empty); err == nil {
			t.Fatal("空尺寸图片应当返回错误")
		}
		var nilImg image.Image
		if _, err := Decode(nilImg); err == nil {
			t.Fatal("nil 图片应当返回错误")
		}
	})
}

// TestScaleNearest 验证放大是「整数倍 + 像素不插值」，这是靠放大提高识别率的前提。
func TestScaleNearest(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.RGBA{1, 2, 3, 0xFF})
	src.Set(1, 0, color.RGBA{4, 5, 6, 0xFF})
	src.Set(0, 1, color.RGBA{7, 8, 9, 0xFF})
	src.Set(1, 1, color.RGBA{10, 11, 12, 0xFF})

	dst := scaleNearest(src, 3)
	if got, want := dst.Bounds(), image.Rect(0, 0, 6, 6); got != want {
		t.Fatalf("放大后尺寸 = %v，期望 %v", got, want)
	}
	// 最近邻意味着每个 3x3 块内颜色完全一致。
	for _, tc := range []struct {
		x, y int
		want color.RGBA
	}{
		{0, 0, color.RGBA{1, 2, 3, 0xFF}},
		{2, 2, color.RGBA{1, 2, 3, 0xFF}},
		{3, 0, color.RGBA{4, 5, 6, 0xFF}},
		{5, 5, color.RGBA{10, 11, 12, 0xFF}},
	} {
		if got := dst.RGBAAt(tc.x, tc.y); got != tc.want {
			t.Fatalf("(%d,%d) = %v，期望 %v", tc.x, tc.y, got, tc.want)
		}
	}
}

// TestToGrayOpaque 保证灰度化后 alpha 一律是 255：
// 半透明输入如果保留 alpha，gozxing 取色时会按预乘值处理，二维码边界会被抹掉。
func TestToGrayOpaque(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 1))
	src.Set(0, 0, color.RGBA{0, 0, 0, 0})     // 全透明
	src.Set(1, 0, color.RGBA{255, 0, 0, 128}) // 半透明
	src.Set(2, 0, color.RGBA{0, 255, 0, 255}) // 不透明
	src.Set(3, 0, color.RGBA{0, 0, 255, 200}) // 半透明
	gray := toGray(src)
	for x := 0; x < 4; x++ {
		r, g, b, a := gray.At(x, 0).RGBA()
		if a != 0xFFFF {
			t.Fatalf("(%d,0) alpha = %d，期望 65535", x, a)
		}
		if r != g || g != b {
			t.Fatalf("(%d,0) 不是灰度: %d,%d,%d", x, r, g, b)
		}
	}
}

// boundsOnlyImage 只提供 Bounds，用来在不真分配大图的前提下测轮次裁剪。
type boundsOnlyImage struct{ image.Rectangle }

func (b boundsOnlyImage) ColorModel() color.Model { return color.RGBAModel }
func (b boundsOnlyImage) Bounds() image.Rectangle { return b.Rectangle }
func (b boundsOnlyImage) At(x, y int) color.Color { return color.RGBA{} }

// TestBuildAttemptsSkipsHugeScales 保证超大截图不会去分配放大的缓冲
// （5000x5000 放大 2 倍 = 1 亿像素，光 RGBA 缓冲就是 400MB）。
func TestBuildAttemptsSkipsHugeScales(t *testing.T) {
	attempts := buildAttempts(boundsOnlyImage{image.Rect(0, 0, 5000, 5000)})
	if len(attempts) != 1 || attempts[0].name != "原图" {
		names := make([]string, 0, len(attempts))
		for _, a := range attempts {
			names = append(names, a.name)
		}
		t.Fatalf("超大图应当只保留原图一轮，实际 %v", names)
	}

	// 普通 4K 整屏要保留全部轮次，别把上限卡得太死把正常场景也砍掉。
	if n := len(buildAttempts(boundsOnlyImage{image.Rect(0, 0, 3840, 2160)})); n < 4 {
		t.Fatalf("4K 截图应保留多轮尝试，实际只有 %d 轮", n)
	}
}

// TestBuildAttemptsOrder 固定住「先便宜后昂贵」的尝试顺序与轮数，
// 免得以后有人把放大挪到最前面（整屏截图会白算几张大图）。
func TestBuildAttemptsOrder(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	attempts := buildAttempts(img)
	if len(attempts) < 4 {
		t.Fatalf("尝试轮数 = %d，期望至少 4 轮", len(attempts))
	}
	if attempts[0].name != "原图" {
		t.Fatalf("第一轮应是原图，实际 %q", attempts[0].name)
	}
	for i, a := range attempts {
		if a.pixels > maxScalePixels {
			t.Fatalf("第 %d 轮 %q 的像素量 %d 超过上限 %d", i, a.name, a.pixels, maxScalePixels)
		}
	}
}
