package tester

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
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

// TestRealPingReusesConnectionAndTakesMin 锁住"取多次最小值"的真实含义：
// 第 2 次起的样本必须**复用**同一条连接，而不是重新建链。
//
// 【为什么断言的是连接数，而不是只比快慢】本地回环上建一条 TCP 只要几十微秒，
// 光比时间分辨不出有没有复用。而"复用"正是我们与主流客户端同口径的全部理由：
// 它建一个 HttpClient、循环两次取最小值，第 2 次是在已经建好的隧道里跑的。
// 一旦有人把 DisableKeepAlives 改回 true，或把读 body 的写法改掉导致连接回不了
// 连接池，最小值就退化成冷连接，界面数字会比主流客户端高好几倍 —— 这个用例会红。
func TestRealPingReusesConnectionAndTakesMin(t *testing.T) {
	const coldDelay = 300 * time.Millisecond

	var (
		mu       sync.Mutex
		requests int
		dials    int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		first := requests == 1
		mu.Unlock()
		if first {
			time.Sleep(coldDelay) // 只在第一次请求上磨蹭：这就是"冷连接"的代价
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		atomic.AddInt32(&dials, 1)
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}

	best, samples, err := RealPing(context.Background(), dial, srv.URL, 2, 10*time.Second)
	if err != nil {
		t.Fatalf("RealPing: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("样本数 = %d, want 2", len(samples))
	}
	if got := atomic.LoadInt32(&dials); got != 1 {
		t.Fatalf("建立了 %d 条连接，want 1：第 2 次取样没有复用连接，最小值会退化成冷连接", got)
	}
	if samples[0] < coldDelay/2 {
		t.Fatalf("第 1 个样本 %v 远小于服务端冷延迟 %v，用例本身失效了", samples[0], coldDelay)
	}
	if best > coldDelay/2 {
		t.Fatalf("最优样本 %v 仍是冷连接的量级，第 2 次没走复用（samples=%v）", best, samples)
	}
}

// TestRealPingSingleAttemptIsCold 锁住"取样次数为 1 时量到的就是冷连接"：
// 把设置里的取样次数设成 1，用户看到的就是"第一次打开网页"的代价，不是热往返。
func TestRealPingSingleAttemptIsCold(t *testing.T) {
	const coldDelay = 200 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(coldDelay)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	best, samples, err := RealPing(context.Background(), nil, srv.URL, 1, 5*time.Second)
	if err != nil {
		t.Fatalf("RealPing: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("样本数 = %d, want 1", len(samples))
	}
	if best < coldDelay/2 {
		t.Fatalf("单次取样 %v 明显小于服务端延迟 %v，说明计时起点不对", best, coldDelay)
	}
}

// TestRealPingAllAttemptsFail 锁住"全部样本都失败才报错，且失败样本不进结果"。
func TestRealPingAllAttemptsFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	best, samples, err := RealPing(context.Background(), nil, srv.URL, 2, 5*time.Second)
	if err == nil {
		t.Fatalf("两次都返回 500，应当报错；得到 best=%v samples=%v", best, samples)
	}
	if len(samples) != 0 {
		t.Fatalf("失败的样本不该进结果：samples=%v", samples)
	}
}
