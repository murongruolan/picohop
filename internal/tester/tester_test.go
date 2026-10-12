package tester

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// TestSpeedCountingStartsAtFirstByte 是审查 #20 的回归用例：测速的分母里
// **不许**包含建链 / TLS 握手 / 等响应头的时间。
//
// 【为什么这么构造】让服务端先故意磨蹭 headerDelay 才发响应头，之后才按固定
// 速率（每 chunkPause 一块）吐 body：
//   - 前半段延迟里客户端一个 body 字节都读不到，属于"零字节时间"；
//   - 后半段才是真正的传输窗口。
//
// 旧的实现把计时起点放在 client.Do 之前，分母 = headerDelay + 传输窗口，
// 于是这个 1.5 秒的建链成本把速度砍到真值的六成左右；口径对齐后应当在真值附近。
//
// 容差取 ±35%：Windows 上 time.Sleep 有十几毫秒的抖动，20 次累加会让实测窗口
// 偏长、比值偏低，但绝不会低到 0.6。
func TestSpeedCountingStartsAtFirstByte(t *testing.T) {
	const (
		headerDelay = 1500 * time.Millisecond // 响应头之前的"建链/首字节"成本
		chunks      = 20
		chunkPause  = 100 * time.Millisecond // 每块之间的间隔，总传输约 2s
		chunkSize   = 64 * 1024
	)

	wantBytes := int64(chunks * chunkSize)
	wantMBps := float64(wantBytes) / (1024 * 1024) / (float64(chunks) * chunkPause.Seconds())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// 这段磨蹭发生在响应头之前：客户端此时一个字节都收不到。
		time.Sleep(headerDelay)
		w.Header().Set("Content-Length", strconv.Itoa(chunks*chunkSize))
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush() // 让头立刻出去，后面的 body 才有可测的窗口
		}
		buf := make([]byte, chunkSize)
		for i := 0; i < chunks; i++ {
			if _, err := w.Write(buf); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			if i != chunks-1 {
				time.Sleep(chunkPause)
			}
		}
	}))
	defer srv.Close()

	// maxSeconds 给足，让循环以 EOF 结束（而不是被 deadline 掐断），
	// 这样分母就是"从头到尾把 body 收完"的真实传输耗时。
	got, total, err := Speed(context.Background(), nil, srv.URL, 8, 10*time.Second)
	if err != nil {
		t.Fatalf("Speed: %v", err)
	}
	if total != wantBytes {
		t.Fatalf("下载字节数 = %d, want %d", total, wantBytes)
	}

	ratio := got / wantMBps
	if ratio < 0.65 || ratio > 1.35 {
		t.Fatalf("速度 %.3f MB/s，真值约 %.3f MB/s（比值 %.2f）：分母把建链时间也算进去了",
			got, wantMBps, ratio)
	}
}
