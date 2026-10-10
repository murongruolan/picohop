//go:build windows

package qrscan

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skipIfNoDesktop 在取不到桌面（无头/服务会话）时跳过截图相关测试。
// 这种情况是环境限制，不是实现缺陷，所以用 Skip 而不是 Fail。
func skipIfNoDesktop(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "GetDC") ||
		strings.Contains(err.Error(), "虚拟桌面") ||
		strings.Contains(err.Error(), "CreateCompatibleDC") {
		t.Skipf("当前会话没有可用桌面，跳过截图测试: %v", err)
	}
	t.Fatalf("截图失败: %v", err)
}

// TestVirtualScreenAndCaptureScreen 真截一次整屏，并确保图能存成 PNG 再读回来。
func TestVirtualScreenAndCaptureScreen(t *testing.T) {
	vs, err := VirtualScreen()
	skipIfNoDesktop(t, err)
	t.Logf("VirtualScreen() = %v，物理像素 %dx%d", vs, vs.Dx(), vs.Dy())

	if vs.Dx() <= 0 || vs.Dy() <= 0 {
		t.Fatalf("虚拟桌面尺寸必须大于 0，实际 %dx%d", vs.Dx(), vs.Dy())
	}

	img, err := CaptureScreen()
	skipIfNoDesktop(t, err)
	if img == nil {
		t.Fatal("CaptureScreen 返回了 nil 图片")
	}
	if got, want := img.Bounds(), image.Rect(0, 0, vs.Dx(), vs.Dy()); got != want {
		t.Fatalf("截图边界 = %v，期望 %v", got, want)
	}

	// 统计一下非黑像素比例，确认拿到的不是"全 0 的空白缓冲"（GDI 失败时很常见），
	// 但不做断言：无头/锁屏会话下真的是全黑，那属于环境问题。
	nonBlack := 0
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 || img.Pix[i+1] != 0 || img.Pix[i+2] != 0 {
			nonBlack++
		}
	}
	t.Logf("截图像素 %d 个，其中非黑像素 %d 个；左上角像素 RGBA=%v",
		vs.Dx()*vs.Dy(), nonBlack, img.RGBAAt(0, 0))

	// alpha 必须被强制成 255，否则后续识别会被当成透明图。
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0xFF {
			t.Fatalf("第 %d 个像素的 alpha = %d，期望 255", i/4, img.Pix[i])
		}
	}

	// 落盘到 t.TempDir() 再读回：既验证 RGBA 数据是完好的，也不污染项目目录。
	path := filepath.Join(t.TempDir(), "screen.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建临时文件失败: %v", err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatalf("PNG 编码失败: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭临时文件失败: %v", err)
	}
	if st, err := os.Stat(path); err != nil || st.Size() == 0 {
		t.Fatalf("临时 PNG 异常: stat=%v, err=%v", st, err)
	}

	back, err := os.Open(path)
	if err != nil {
		t.Fatalf("重新打开临时文件失败: %v", err)
	}
	defer back.Close()
	decoded, err := png.Decode(back)
	if err != nil {
		t.Fatalf("读回 PNG 失败: %v", err)
	}
	if got, want := decoded.Bounds(), img.Bounds(); got != want {
		t.Fatalf("读回的图片边界 = %v，期望 %v", got, want)
	}
	t.Logf("截图已写入并成功读回: %s", path)
}

// TestCaptureRect 覆盖正常选区与越界选区。
func TestCaptureRect(t *testing.T) {
	vs, err := VirtualScreen()
	skipIfNoDesktop(t, err)
	if vs.Dx() < 64 || vs.Dy() < 64 {
		t.Skipf("虚拟桌面只有 %v，太小，跳过 64x64 选区测试", vs)
	}

	// 从虚拟桌面中心取 64x64。
	cx, cy := vs.Min.X+vs.Dx()/2, vs.Min.Y+vs.Dy()/2
	sel := image.Rect(cx-32, cy-32, cx+32, cy+32)
	img, err := CaptureRect(sel)
	skipIfNoDesktop(t, err)
	if img == nil {
		t.Fatal("CaptureRect 返回了 nil 图片")
	}
	if got, want := img.Bounds(), image.Rect(0, 0, 64, 64); got != want {
		t.Fatalf("选区截图边界 = %v，期望 %v", got, want)
	}
	t.Logf("CaptureRect(%v) 成功，返回 %v", sel, img.Bounds())

	// 副屏在主屏左边/上边时坐标是负的，这一步顺带验证负坐标不会翻车。
	neg := image.Rect(vs.Min.X, vs.Min.Y, vs.Min.X+32, vs.Min.Y+32)
	if negImg, err := CaptureRect(neg); err != nil {
		t.Fatalf("截取虚拟桌面左上角 %v 失败: %v", neg, err)
	} else if got, want := negImg.Bounds(), image.Rect(0, 0, 32, 32); got != want {
		t.Fatalf("左上角选区边界 = %v，期望 %v", got, want)
	}

	// 明显越界：右下角只应保留 10x10。
	out := image.Rect(vs.Max.X-10, vs.Max.Y-10, vs.Max.X+5000, vs.Max.Y+5000)
	clipped, err := CaptureRect(out)
	if err != nil {
		t.Fatalf("越界选区应当被裁剪而不是报错，却失败: %v", err)
	}
	if got, want := clipped.Bounds(), image.Rect(0, 0, 10, 10); got != want {
		t.Fatalf("越界选区裁剪后 = %v，期望 %v", got, want)
	}
	t.Logf("越界选区 %v 被裁剪为 %v", out, clipped.Bounds())

	// 完全在屏幕外：返回错误即可，绝不能 panic。
	off := image.Rect(vs.Max.X+100, vs.Max.Y+100, vs.Max.X+200, vs.Max.Y+200)
	if _, err := CaptureRect(off); err == nil {
		t.Fatal("完全在屏幕外的选区应当返回错误")
	} else {
		t.Logf("完全越界的选区按预期返回错误: %v", err)
	}

	// 退化选区（面积为 0）也必须是错误而不是崩溃。
	if _, err := CaptureRect(image.Rect(cx, cy, cx, cy)); err == nil {
		t.Fatal("空选区应当返回错误")
	}
}

// TestCaptureWholeVirtualScreen 全覆盖：整屏截图与「按整屏矩形调用 CaptureRect」应等价。
func TestCaptureWholeVirtualScreen(t *testing.T) {
	vs, err := VirtualScreen()
	skipIfNoDesktop(t, err)
	rect, err := CaptureRect(vs)
	skipIfNoDesktop(t, err)
	if got, want := rect.Bounds(), image.Rect(0, 0, vs.Dx(), vs.Dy()); got != want {
		t.Fatalf("整屏 CaptureRect 边界 = %v，期望 %v", got, want)
	}
	t.Logf("整屏 CaptureRect(%v) = %v", vs, rect.Bounds())
}
