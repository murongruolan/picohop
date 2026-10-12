package app

import (
	"testing"

	"github.com/murongruolan/picohop/internal/config"
	"github.com/murongruolan/picohop/internal/proxy"
)

func openEvent(id uint64, at int64, target string) proxy.ConnEvent {
	return proxy.ConnEvent{
		ID: id, Phase: proxy.ConnPhaseOpen, Node: "住宅代理1",
		Protocol: "socks5", Target: target, SrcPort: 50000 + int(id), At: at,
	}
}

// TestConnTableLifecycle 锁死"建立 → 活跃、结束 → 归档并带上字节与时长"。
func TestConnTableLifecycle(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})

	a.trackConnEvent(openEvent(1, 1000, "a.com:443"))
	a.trackConnEvent(openEvent(2, 2000, "b.com:443"))

	conns := a.Connections()
	if len(conns) != 2 {
		t.Fatalf("应当有 2 条活动连接，实际 %d", len(conns))
	}
	// 活动连接按建立时间倒序：后建的排前面
	if conns[0].ID != 2 || conns[0].State != "active" {
		t.Fatalf("活动连接应当按时间倒序且状态为 active，实际 %+v", conns[0])
	}
	// 活动连接的时长是按"到现在"算的，不该是 0
	if conns[0].Duration <= 0 {
		t.Fatalf("活动连接的时长应当实时计算，实际 %d", conns[0].Duration)
	}
	// 连接建立时也应当写进网络日志（连接流水）
	if logs, _ := a.NetworkLogs(0); len(logs) != 2 {
		t.Fatalf("建立连接时应当同时写网络日志，实际 %d 条", len(logs))
	}

	// 结束：字节与时长要落到记录里，并从活动表移到已关闭表
	a.trackConnEvent(proxy.ConnEvent{
		ID: 1, Phase: proxy.ConnPhaseClose, Node: "住宅代理1", Protocol: "socks5",
		Target: "a.com:443", SrcPort: 50001, At: 1000, Up: 111, Down: 222, Duration: 333,
	})
	conns = a.Connections()
	if len(conns) != 2 {
		t.Fatalf("结束之后仍然应当是 2 条（1 活动 + 1 已关闭），实际 %d", len(conns))
	}
	closed := conns[1]
	if closed.State != "closed" || closed.Up != 111 || closed.Down != 222 || closed.Duration != 333 {
		t.Fatalf("已关闭记录不对：%+v", closed)
	}
	// 结束后不该再写一行网络日志（否则一段连接会重复出现两遍）
	if logs, _ := a.NetworkLogs(0); len(logs) != 2 {
		t.Fatalf("结束时不该再写网络日志，实际 %d 条", len(logs))
	}
}

// TestConnTableUnknownClose 收到没见过的 ID 时不 panic、也不凭空造记录。
func TestConnTableUnknownClose(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})
	a.trackConnEvent(proxy.ConnEvent{ID: 99, Phase: proxy.ConnPhaseClose, Up: 1})
	if conns := a.Connections(); len(conns) != 0 {
		t.Fatalf("不该凭空造出记录，实际 %d", len(conns))
	}
}

// TestClosedConnTrim 已关闭列表超上限时裁掉最旧的。
func TestClosedConnTrim(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})
	total := closedConnCap + 5
	for i := 1; i <= total; i++ {
		id := uint64(i)
		a.trackConnEvent(openEvent(id, int64(i), "x.com:443"))
		a.trackConnEvent(proxy.ConnEvent{ID: id, Phase: proxy.ConnPhaseClose, Up: 1, Down: 2, Duration: 3})
	}
	conns := a.Connections()
	if len(conns) != closedConnCap {
		t.Fatalf("已关闭列表应当裁到 %d 条，实际 %d", closedConnCap, len(conns))
	}
	// 保留的应当是最新的那批
	if conns[0].ID != uint64(total) {
		t.Fatalf("最近关闭的应当排在最前，实际 ID=%d（期望 %d）", conns[0].ID, total)
	}
}
