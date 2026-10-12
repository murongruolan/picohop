package ports

import (
	"net"
	"strconv"
	"testing"
)

// freeBase 找一个"从它开始连续 n 个端口都真正空闲"的起始端口。
//
// 【为什么必须动态找，不能写死 10810】
// 这几个测试依赖真实的端口占用情况。早先直接断言"空占用表时 Next() 返回 10810"，
// 结果开发机上正跑着 ChainProxy（占着 10810/10811）时测试就红了 ——
// 那种失败与代码毫无关系，却极易被误判成回归。真踩过一次。
func freeBase(t *testing.T, from, n int) int {
	t.Helper()
	for p := from; p < from+3000; p++ {
		ok := true
		for i := 0; i < n; i++ {
			ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p+i)))
			if err != nil {
				ok = false
				break
			}
			_ = ln.Close()
		}
		if ok {
			return p
		}
	}
	t.Fatalf("从 %d 起找不到 %d 个连续空闲端口", from, n)
	return 0
}

// TestNextFollowsMaxPlusOne 锁死"默认端口 = 本项目内所有 10800+ 端口最大值 + 1"。
func TestNextFollowsMaxPlusOne(t *testing.T) {
	base := freeBase(t, BaselineFloor+100, 2)

	a := New()
	a.Host = "127.0.0.1"
	a.Start = base

	// 没有端口时从起始值开始
	if got := a.Next(); got != base {
		t.Fatalf("空占用表时应返回起始值 %d，实际 %d", base, got)
	}

	a.MarkProject(base, "n1")
	if got := a.Next(); got != base+1 {
		t.Fatalf("最大值 %d => 应返回 %d，实际 %d", base, base+1, got)
	}

	// 低于 10800 的端口不参与取最大值，否则一个 8080 会把结果拉低
	a.MarkProject(8080, "low")
	if got := a.Next(); got != base+1 {
		t.Fatalf("8080 不应参与取最大值，应仍返回 %d，实际 %d", base+1, got)
	}

	// 只有低端口时，回到起始值
	b := New()
	b.Host = "127.0.0.1"
	b.Start = base
	b.MarkProject(8080, "low")
	if got := b.Next(); got != base {
		t.Fatalf("只有低端口时应返回 %d，实际 %d", base, got)
	}
}

// TestNextSkipsBusyPort 顺延跳过真正被占用的端口。
func TestNextSkipsBusyPort(t *testing.T) {
	base := freeBase(t, BaselineFloor+100, 4)

	// 用另一个监听把 base+1 占住
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(base+1)))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	a := New()
	a.Host = "127.0.0.1"
	a.Start = base
	a.MarkProject(base, "n1")

	// max = base → 顺延候选 base+1（被占）→ 应当继续顺延到 base+2
	if got := a.Next(); got != base+2 {
		t.Fatalf("应跳过被占用的 %d 返回 %d，实际 %d", base+1, base+2, got)
	}
}

// TestNextNeverReturnsUnverifiedPort 锁死"扫不到空闲端口就返回 NoPort"。
//
// 【审查 #21 的回归锁】老实现有两条错误出口，都会把一个**没有验证过**的端口
// 交给调用方，最终被原样写进节点配置，用户看到的是"节点启动失败：端口绑定失败"：
//   - 候选顺延越过 65535（占用表里已经有 65535 时，cand = max+1 = 65536）：
//     循环条件 `cand <= 65535` 一次都不成立，函数把 65536 原样返回；
//   - 连扫 MaxScanAhead 次都还占着：循环结束时返回的是最后一个候选，
//     而它的 Check 结果恰恰是"不空闲"。
//
// 【为什么允许返回 0】Next() 只返回 int，没有 error 出口；0 不是合法端口
// （Check 直接判非法），任何写入路径都能一眼判出来并明确报错。
// 返回"猜一个"的端口比返回 0 更危险：它会让一个必然起不来的端口静默进配置。
func TestNextNeverReturnsUnverifiedPort(t *testing.T) {
	t.Run("候选越过 65535 时返回 NoPort", func(t *testing.T) {
		a := New()
		a.Host = "127.0.0.1"
		a.Start = 65535
		// 占用表里已经有 65535 → cand = 65536，一个必然 bind 失败的端口
		a.MarkProject(65535, "占满")

		if got := a.Next(); got != NoPort {
			t.Fatalf("候选 %d 已越过 65535，应当返回 NoPort(%d)，实际 %d",
				65536, NoPort, got)
		}
	})

	t.Run("扫到端口区间末尾仍占着时返回 NoPort", func(t *testing.T) {
		// 在区间末尾拿 6 个真实监听，逼近 65535 这条硬边界
		const n = 6
		base := 65536 - n
		lns := make([]net.Listener, 0, n)
		for p := base; p <= 65535; p++ {
			ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
			if err != nil {
				for _, l := range lns {
					_ = l.Close()
				}
				t.Skipf("本机 %d 已被占用，跳过：%v", p, err)
			}
			lns = append(lns, ln)
		}
		defer func() {
			for _, l := range lns {
				_ = l.Close()
			}
		}()

		a := New()
		a.Host = "127.0.0.1"
		a.Start = base

		if got := a.Next(); got != NoPort {
			t.Fatalf("%d-%d 全部被占用，应当返回 NoPort(%d)，实际 %d",
				base, 65535, NoPort, got)
		}
	})

	t.Run("扫描预算用尽时返回 NoPort", func(t *testing.T) {
		// 真实占住连续的 MaxScanAhead 个端口：占用表标记做不到这件事 ——
		// 候选起点就是"占用表里最大端口 + 1"，标记出来的端口永远落在候选之前。
		base := freeBase(t, BaselineFloor+100, MaxScanAhead+1)
		lns := make([]net.Listener, 0, MaxScanAhead)
		for i := 0; i < MaxScanAhead; i++ {
			ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(base+i)))
			if err != nil {
				for _, l := range lns {
					_ = l.Close()
				}
				t.Fatalf("占住 %d 失败：%v", base+i, err)
			}
			lns = append(lns, ln)
		}
		defer func() {
			for _, l := range lns {
				_ = l.Close()
			}
		}()

		a := New()
		a.Host = "127.0.0.1"
		a.Start = base

		// base+MaxScanAhead 其实空闲，但它已经超出扫描预算、从未被验证 ——
		// 老实现就是把这个"没验证过"的端口当结果返回的。
		if got := a.Next(); got != NoPort {
			t.Fatalf("连续 %d 个端口都占着，应当返回 NoPort(%d)，实际 %d（%d 从未被验证）",
				MaxScanAhead, NoPort, got, base+MaxScanAhead)
		}
	})
}

// TestAllocateForDoesNotRegisterNoPort 锁死"没分到端口就不许登记"。
//
// 登记 0 会让 ProjectPorts/UsedPorts 里多出一个"0 号端口"的幽灵节点，
// 界面上显示成本项目占用了端口 0。
func TestAllocateForDoesNotRegisterNoPort(t *testing.T) {
	a := New()
	a.Host = "127.0.0.1"
	a.Start = 65535
	a.MarkProject(65535, "占满")

	if got := a.AllocateFor("新节点"); got != NoPort {
		t.Fatalf("没有可用端口时应当返回 NoPort(%d)，实际 %d", NoPort, got)
	}
	got := a.ProjectPorts()
	if len(got) != 1 || got[0] != 65535 {
		t.Fatalf("失败时不应登记任何端口，实际登记了 %v", got)
	}
}

// TestCheckDetectsProjectAndSystem 覆盖占用检测的三条路径。
func TestCheckDetectsProjectAndSystem(t *testing.T) {
	a := New()
	a.Host = "127.0.0.1"
	a.MarkProject(10810, "已有节点")

	if st := a.Check(10810); st.Free || !st.InProject {
		t.Errorf("项目内端口应被识别为占用：%+v", st)
	}
	if st := a.Check(10810); st.Message == "" {
		t.Error("占用时应当给出提示文案")
	}

	// 系统占用
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port
	if st := a.Check(busy); st.Free || !st.Busy {
		t.Errorf("系统占用的端口应被识别：%+v", st)
	}

	// 非法端口
	if st := a.Check(0); st.Valid || st.Free {
		t.Errorf("端口 0 应当非法：%+v", st)
	}
	if st := a.Check(70000); st.Valid || st.Free {
		t.Errorf("端口 70000 应当非法：%+v", st)
	}
	if st := a.Check(80); st.Free {
		t.Errorf("系统保留端口 80 不应当被认作可用：%+v", st)
	}
}

// TestRandomFreeInRange 确认随机端口落在合法范围且确实空闲。
func TestRandomFreeInRange(t *testing.T) {
	a := New()
	a.Host = "127.0.0.1"

	seen := map[int]bool{}
	for i := 0; i < 30; i++ {
		p, err := a.RandomFree()
		if err != nil {
			t.Fatalf("第 %d 次随机失败：%v", i, err)
		}
		if p <= 10000 {
			t.Fatalf("随机端口必须大于 10000，实际 %d", p)
		}
		if p > RandomMax {
			t.Fatalf("随机端口不应超过 %d，实际 %d", RandomMax, p)
		}
		if !a.Check(p).Free {
			t.Fatalf("随机出来的端口 %d 竟然不空闲", p)
		}
		seen[p] = true
	}
	if len(seen) < 5 {
		t.Errorf("30 次随机只出现 %d 个不同端口，随机性可疑", len(seen))
	}
}

// TestRandomFreeGivesUpAfterRetries 锁死"多次随机仍未随机到空闲端口"。
//
// 手法：把随机范围压到只剩几个端口，并把它们全部标记为占用，
// 于是每次随机都失败，必须在 MaxRandomRetries 次之后返回错误。
func TestRandomFreeGivesUpAfterRetries(t *testing.T) {
	a := New()
	a.Host = "127.0.0.1"
	a.RandMin, a.RandMax = 20000, 20002
	for p := 20000; p <= 20002; p++ {
		a.MarkProject(p, "占满")
	}

	_, err := a.RandomFree()
	if err == nil {
		t.Fatal("端口全部被占用时应当返回错误")
	}
	if err != ErrNoFreePort {
		t.Fatalf("应当返回 ErrNoFreePort，实际 %v", err)
	}
	if err.Error() != "多次随机仍未随机到空闲端口" {
		t.Fatalf("错误文案应当是界面上要显示的那句话，实际 %q", err.Error())
	}
}

// TestRandomRetryCountIsFive 确认重试上限就是 5 次。
func TestRandomRetryCountIsFive(t *testing.T) {
	if MaxRandomRetries != 5 {
		t.Fatalf("产品要求重试上限为 5 次，实际 %d", MaxRandomRetries)
	}
	a := New()
	if a.MaxRetries != 5 {
		t.Fatalf("New() 的默认重试上限应为 5，实际 %d", a.MaxRetries)
	}
}

// TestAllocateForRegisters 确认自动分配后端口被登记，下一次不会重复。
func TestAllocateForRegisters(t *testing.T) {
	base := freeBase(t, BaselineFloor+100, 2)

	a := New()
	a.Host = "127.0.0.1"
	a.Start = base

	p1 := a.AllocateFor("n1")
	p2 := a.AllocateFor("n2")
	if p1 == p2 {
		t.Fatalf("两次分配不应相同：%d / %d", p1, p2)
	}
	if p1 != base {
		t.Fatalf("第一次应分配起始值 %d，实际 %d", base, p1)
	}
	if p2 != p1+1 {
		t.Fatalf("第二次应分配 %d，实际 %d", p1+1, p2)
	}
	got := a.ProjectPorts()
	if len(got) != 2 || got[0] != p1 || got[1] != p2 {
		t.Fatalf("ProjectPorts 结果不对：%v", got)
	}
}

// TestHostPortFormat 只是一个防止 strconv 用法写错的哨兵测试。
func TestHostPortFormat(t *testing.T) {
	got := net.JoinHostPort("127.0.0.1", strconv.Itoa(10810))
	if got != "127.0.0.1:10810" {
		t.Fatalf("地址拼接错误：%s", got)
	}
}
