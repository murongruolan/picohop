package proxy

import (
	"net"
	"testing"
	"time"
)

// ------------------------------------------------------------------
// 审查 #16：UDP 会话的"回程地址"（handler.go 里的 clientUDP）跨 goroutine 交接
// ------------------------------------------------------------------

// holdUDPQuotaUntilSettled 与 udp_associate_test.go 的 holdUDPSessionQuotaForTest 类似，
// 但收尾时**先等会话把名额真的归还**，再把账本拉回原值。
//
// 【为什么不能直接照抄 holdUDPSessionQuotaForTest】它的收尾是一句
// udpSessionCount.Store(base)，而 handler 的 releaseUDPSession 是在会话 goroutine 的
// defer 里**异步**发生的（顺序还是 finish -> up.Close -> pc.Close -> release）。
// Store 抢在 release 前面的话，账本先被拉到 base、随后又被 release 减一，变成 base-1，
// 也就是"名额凭空多出来一个"。同一个进程里重跑（go test -count=2/3）时，
// 后面 TestUDPAssociateOverCapIsRefusedFailClosed 的"名额已满必须拒绝"就会漏判 ——
// 这不是纸上推演：本用例的第一版正是这么把 -count=3 跑红的
// （"名额已满时不该再接受新的 UDP ASSOCIATE"）。
func holdUDPQuotaUntilSettled(t *testing.T) int64 {
	t.Helper()
	base := udpSessionCount.Load()
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for udpSessionCount.Load() > base && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		udpSessionCount.Store(base)
	})
	return base
}

// finishUDPQuotaForTest 主动断开控制连接并等名额归位：正常路径下让测试自己就把
// 进程级账本收拾干净（不依赖 cleanup 的时序），顺带断言 acquire / release 配过对。
func finishUDPQuotaForTest(t *testing.T, uc *udpClient, base int64) {
	t.Helper()
	_ = uc.tcp.Close()
	waitUDPSessionQuota(t, base, 2*time.Second)
}

// TestUDPAssociateReturnPathReachesClientSourceAddr 锁死审查 #16：
// 「客户端 -> 上游」的 goroutine 把客户端来源地址交给「上游 -> 客户端」的 goroutine，
// 必须经过**同步原语**（现在用 atomic.Pointer），而不是裸变量。
//
// 【原来的样子】写入方在 sync.Once 里写 clientUDP，读取方在另一个 goroutine 里
// 直接读它。sync.Once 只在**调用过 Do 的** goroutine 之间建立先后关系
// （它保证的是"只执行一次"，不是"对谁都可见"）；读取方从来没调用过 Do，
// 所以这一读一写之间**没有任何 happens-before 边**：既是 data race
// （Go 内存模型下属 UB），也让读取方可以一直读到 nil —— 那种情况下回程包被全部
// 丢掉，表现为"客户端发得出去、永远收不到回包"，且只在弱内存序机器或激进优化下
// 才现形，本机自测根本看不出来。
//
// 【为什么这条用例刻意不从 ch.sent 取走数据报】假出口是 channel 实现的，
// 从 ch.sent 取一次就在"写入方 -> 测试 -> 读取方"之间**人为**造出一条
// happens-before 链（channel 收发是同步点），data race 会被这条链掩盖 ——
// 实测：带 waitSent 的写法跑 -race 一声不吭。所以这里只发不取，
// 两个 goroutine 之间除被测代码自己的同步外没有任何顺序保证，-race 才如实报出问题。
//
// 【为什么用重试】客户端的第一份数据报与上游的第一份回包谁先到没有保证；
// 回包先到时"回程地址未知"，丢掉它是**正确行为**（SOCKS5 UDP ASSOCIATE 本来就是靠
// 客户端的第一份数据报定回程）。所以这里重发重投，直到回程真的到达客户端。
func TestUDPAssociateReturnPathReachesClientSourceAddr(t *testing.T) {
	// 名额是进程级的：中途 Fatalf 也要把它拉回原值，避免污染后面的用例。
	base := holdUDPQuotaUntilSettled(t)
	CloseConnections(nil)
	t.Cleanup(func() { CloseConnections(nil) })

	ch := newFakeUDPChannel()
	h := newUDPAssocHandler(&udpAssocUpstream{ch: ch}, time.Minute)

	rep, uc := associateUDP(t, serveUDPTestClient(t, h))
	if rep != repSuccess {
		t.Fatalf("UDP ASSOCIATE 应当成功，实际应答码 0x%02x（%s）", rep, ReplyText(rep))
	}

	// 每轮"发一份 + 投一份回包"，最多 3s。刻意不取 ch.sent（见上）。
	deadline := time.Now().Add(3 * time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		uc.send(t, "8.8.8.8", 53, "ping")
		ch.inject("8.8.8.8", 53, "pong")

		buf := make([]byte, 64*1024)
		_ = uc.sock.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, _, err := uc.sock.ReadFromUDP(buf)
		if err != nil {
			continue // 回程地址还没发布（或回包先到被丢弃），重发重投
		}
		host, port, payload, perr := parseUDPHeader(buf[:n])
		if perr != nil {
			t.Fatalf("拆回程 UDP 帧失败：%v", perr)
		}
		if host != "8.8.8.8" || port != 53 || string(payload) != "pong" {
			t.Fatalf("客户端收到的回程数据不对：%s:%d %q", host, port, payload)
		}
		finishUDPQuotaForTest(t, uc, base)
		return
	}
	t.Fatal("3s 内一份回程数据报都没送到客户端 —— 客户端来源地址没有被发布给下行 goroutine")
}

// TestUDPAssociateReturnPathStaysOnFirstClientSourceAddr 锁死"回程地址只认第一份
// 客户端数据报"这一语义。
//
// 【为什么要单独锁死】#16 的修法把"只写一次"从 sync.Once 换成了
// atomic.Pointer.CompareAndSwap(nil, ...) —— 语义与旧实现一致（先到者定死）。
// 若后人图省事改成 clientUDP.Store(from)，语义就变成"最后一份说了算"：
// 任何能往中继口发包的人都能把这条会话的回程劫走。这条用例就是拦这个改法的。
//
// 这条用例**故意**用 waitSent 建立确定的先后顺序（先确认写入方处理了某份数据报，
// 再投回包），所以它测的是语义而不是竞态；竞态由上面那条用例负责。
func TestUDPAssociateReturnPathStaysOnFirstClientSourceAddr(t *testing.T) {
	// 名额是进程级的：中途 Fatalf 也要把它拉回原值，避免污染后面的用例。
	base := holdUDPQuotaUntilSettled(t)
	CloseConnections(nil)
	t.Cleanup(func() { CloseConnections(nil) })

	ch := newFakeUDPChannel()
	h := newUDPAssocHandler(&udpAssocUpstream{ch: ch}, time.Minute)

	rep, uc := associateUDP(t, serveUDPTestClient(t, h))
	if rep != repSuccess {
		t.Fatalf("UDP ASSOCIATE 应当成功，实际应答码 0x%02x（%s）", rep, ReplyText(rep))
	}

	// 第一份数据报来自 uc.sock，它就此定死回程地址。
	uc.send(t, "8.8.8.8", 53, "ping")
	dg := ch.waitSent(t, 2*time.Second)
	if string(dg.payload) != "ping" {
		t.Fatalf("上游收到的数据报不对：%s:%d %q", dg.host, dg.port, dg.payload)
	}
	ch.inject("8.8.8.8", 53, "pong")
	host, port, payload := uc.recv(t, 3*time.Second)
	if host != "8.8.8.8" || port != 53 || payload != "pong" {
		t.Fatalf("客户端收到的回程数据不对：%s:%d %q", host, port, payload)
	}

	// 换一个**源端口**（同一台机器上的"另一个人"）再发一份，然后投回包：
	// 回程必须仍在第一份数据报的地址上，不许改道。
	other, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("开第二个客户端 UDP 套接字失败：%v", err)
	}
	t.Cleanup(func() { _ = other.Close() })

	frame, err := appendUDPHeader(nil, "9.9.9.9", 53, []byte("from-other"))
	if err != nil {
		t.Fatalf("封装第二个客户端的数据报失败：%v", err)
	}
	if _, err := other.WriteToUDP(frame, uc.relay); err != nil {
		t.Fatalf("第二个客户端发数据报失败：%v", err)
	}
	if dg2 := ch.waitSent(t, 2*time.Second); string(dg2.payload) != "from-other" {
		t.Fatalf("上游应当先收到第二个客户端的数据报，实际 %q", dg2.payload)
	}

	ch.inject("9.9.9.9", 53, "pong-other")
	host, port, payload = uc.recv(t, 3*time.Second)
	if host != "9.9.9.9" || port != 53 || payload != "pong-other" {
		t.Fatalf("回程应当仍在第一份数据报的地址上，实际收到：%s:%d %q", host, port, payload)
	}

	// 反向确认：回程没有改道到第二个源端口。
	_ = other.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, _, err := other.ReadFromUDP(make([]byte, 2048)); err == nil {
		t.Fatalf("回程被改道到了第二个源端口（读到 %d 字节）—— 回程地址不许「最后一份说了算」", n)
	}

	finishUDPQuotaForTest(t, uc, base)
}
