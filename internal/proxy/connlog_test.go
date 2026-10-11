package proxy

import (
	"sync/atomic"
	"testing"
)

// fakeCloser 只记"有没有被关过"。
type fakeCloser struct{ closed atomic.Int64 }

func (f *fakeCloser) Close() error {
	f.closed.Add(1)
	return nil
}

// TestCloseConnections 锁死"关闭全部"真的会去关底层的连接。
//
// 这个按钮如果只是把列表清空、不去关连接，用户会以为关掉了 ——
// 所以这里验的是"底层被关了几次"，不是"列表变没变"。
func TestCloseConnections(t *testing.T) {
	h := &Handler{Name: "n1"}
	// 清掉可能残留的历史登记，避免测试相互影响
	CloseConnections(nil)

	c1, c2 := &fakeCloser{}, &fakeCloser{}
	id1, _ := h.trackConn("socks5", "a.com:443", nil, c1, c2)
	c3 := &fakeCloser{}
	h.trackConn("http", "b.com:443", nil, c3)

	if n := ActiveConnCount(); n != 2 {
		t.Fatalf("应当登记 2 条连接，实际 %d", n)
	}

	// 只关其中一条
	if n := CloseConnections([]uint64{id1}); n != 1 {
		t.Fatalf("应当关掉 1 条，实际 %d", n)
	}
	if got := c1.closed.Load(); got != 1 {
		t.Fatalf("id1 的客户端连接应当被关 1 次，实际 %d", got)
	}
	if got := c3.closed.Load(); got != 0 {
		t.Fatalf("不该碰到 id2 的连接，实际被关 %d 次", got)
	}

	// 再全关
	if n := CloseConnections(nil); n != 1 {
		t.Fatalf("剩余应当只有 1 条，实际 %d", n)
	}
	if got := c3.closed.Load(); got != 1 {
		t.Fatalf("id2 的连接应当被关 1 次，实际 %d", got)
	}
}

// TestConnEventPhases 锁死事件的两个阶段都发出来了，且 close 带上字节与时长。
func TestConnEventPhases(t *testing.T) {
	var events []ConnEvent
	SetConnSink(func(ev ConnEvent) { events = append(events, ev) })
	t.Cleanup(func() { SetConnSink(nil) })

	h := &Handler{Name: "n1"}
	_, finish := h.trackConn("socks5", "a.com:443", nil)
	finish(10, 20, nil)

	if len(events) != 2 {
		t.Fatalf("应当收到 open + close 两条事件，实际 %d", len(events))
	}
	if events[0].Phase != ConnPhaseOpen || events[0].Target != "a.com:443" {
		t.Fatalf("open 事件不对：%+v", events[0])
	}
	if events[1].Phase != ConnPhaseClose || events[1].Up != 10 || events[1].Down != 20 {
		t.Fatalf("close 事件应当带上字节数：%+v", events[1])
	}
}
