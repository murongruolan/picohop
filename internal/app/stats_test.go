package app

import (
	"testing"

	"github.com/murongruolan/picohop/internal/config"
)

// TestTrafficHistoryServesLastN 锁死"返回最近 N 个、按时间升序、且是副本"。
//
// 是副本这一条很重要：返回的切片会被界面直接拿去画图、甚至排序，
// 如果它引用的是内部缓冲，界面的一次改动就会污染后台正在写的环形缓冲。
func TestTrafficHistoryServesLastN(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})

	if got := a.TrafficHistory(10, ""); len(got) != 0 {
		t.Fatalf("全新实例应当是空的，实际 %d 个样本", len(got))
	}

	a.mu.Lock()
	for i := 1; i <= 5; i++ {
		a.pushTrafficLocked(TrafficSample{At: int64(i), Up: int64(i * 10), Down: int64(i * 100)})
	}
	a.mu.Unlock()

	got := a.TrafficHistory(3, "")
	if len(got) != 3 {
		t.Fatalf("应当返回最近 3 个，实际 %d 个", len(got))
	}
	if got[0].At != 3 || got[2].At != 5 {
		t.Fatalf("应当是最新的 3 个且按时间升序，实际 %+v", got)
	}

	// seconds 大于已有样本数：返回全部
	if all := a.TrafficHistory(99, ""); len(all) != 5 {
		t.Fatalf("seconds 超过样本数时应返回全部，实际 %d 个", len(all))
	}
	// seconds <= 0：返回全部
	if all := a.TrafficHistory(0, ""); len(all) != 5 {
		t.Fatalf("seconds=0 时应返回全部，实际 %d 个", len(all))
	}

	// 改返回值不能影响缓冲
	got[0].Up = -1
	if again := a.TrafficHistory(3, ""); again[0].Up == -1 {
		t.Fatal("返回的应当是副本，改动它不该影响内部缓冲")
	}
}

// TestPushTrafficTrimsToRetention 锁死裁剪算术。
//
// 这段代码只在连续跑满一小时之后才生效，靠等是等不到的，所以直接灌。
func TestPushTrafficTrimsToRetention(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})

	a.mu.Lock()
	total := trafficHistorySeconds + 10
	for i := 0; i < total; i++ {
		a.pushTrafficLocked(TrafficSample{At: int64(i)})
	}
	a.mu.Unlock()

	got := a.TrafficHistory(0, "")
	if len(got) != trafficHistorySeconds {
		t.Fatalf("应当裁到 %d 个，实际 %d 个", trafficHistorySeconds, len(got))
	}
	// 留下的应当是**最新的**那批：最后一个样本的 At 是 total-1
	if got[len(got)-1].At != int64(total-1) {
		t.Fatalf("保留的应当是最近的样本，最后一个 At=%d（期望 %d）", got[len(got)-1].At, total-1)
	}
	if got[0].At != int64(total-trafficHistorySeconds) {
		t.Fatalf("最旧的样本应当是 At=%d，实际 %d", total-trafficHistorySeconds, got[0].At)
	}
}

// TestTrafficHistoryPerNode 锁死"按节点取历史"：不同节点互不串台，
// 且各自与总量是各自独立的序列。
func TestTrafficHistoryPerNode(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})

	a.mu.Lock()
	a.pushTrafficLocked(TrafficSample{At: 1000, Up: 10, Down: 20})
	a.pushNodeTrafficLocked("n1", TrafficSample{At: 1000, Up: 1, Down: 2})
	a.pushNodeTrafficLocked("n2", TrafficSample{At: 1000, Up: 3, Down: 4})
	a.pushNodeTrafficLocked("n1", TrafficSample{At: 2000, Up: 5, Down: 6})
	a.mu.Unlock()

	if got := a.TrafficHistory(0, ""); len(got) != 1 || got[0].Up != 10 {
		t.Fatalf("总量应当只有 1 个样本，实际 %+v", got)
	}
	n1 := a.TrafficHistory(0, "n1")
	if len(n1) != 2 || n1[0].Up != 1 || n1[1].Up != 5 {
		t.Fatalf("n1 应当有 2 个样本且按时间升序，实际 %+v", n1)
	}
	n2 := a.TrafficHistory(0, "n2")
	if len(n2) != 1 || n2[0].Up != 3 {
		t.Fatalf("n2 应当是独立序列，实际 %+v", n2)
	}
	// 不存在的节点：空，而不是总量（串台就坏在这里）
	if got := a.TrafficHistory(0, "nope"); len(got) != 0 {
		t.Fatalf("不存在的节点应当返回空，实际 %+v", got)
	}
	// 只要最近 1 个
	if got := a.TrafficHistory(1, "n1"); len(got) != 1 || got[0].At != 2000 {
		t.Fatalf("seconds=1 应当只给最新的，实际 %+v", got)
	}
}
