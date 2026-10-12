package app

import (
	"encoding/json"
	"testing"

	"github.com/murongruolan/picohop/internal/config"
)

// 审查 #19 的回归测试：测试失败不得把上一次测出来的有效值写成 0。
//
// 缺陷原貌（internal/app/test.go 的 runOneTest → updateTest）：
//
//	// TCP 失败
//	write(true, 0, 0, "")
//	// 真连接 / 测速 / 出口 IP 失败
//	write(false, 0, 0, "")
//
// 失败路径和成功路径共用同一条写回，于是 updateTest 里的
// `LatencyMs = latencyMs` / `RealMs = latencyMs` / `SpeedMBps = speed` 会把
// **上一次测出来的数据直接抹成 0**：
//   - 数据没了：界面上「延迟/速度」列变成空，用户只能重测，而重测又要再连一次；
//   - 状态也丢了：0 与"从没测过"完全同形，界面无从区分"没测过 / 测过但失败 /
//     上次成功值（已过期）"这三种中间态。
//
// 修法：数值只在真的测出来时才覆盖（`measured.ok`），失败只置该族的失败标记
// 与"最近一次测试"的结论；数值自己的测量时间（LatencyAt / SpeedAt）留着，
// 界面据此把旧值显示成"过期"。

// testNode 造一个干净的节点配置。
func testNode(id string) config.Node {
	return config.Node{
		ID: id, Name: id, Protocol: config.ProtocolSOCKS5,
		Server: "example.com", Port: 443,
	}
}

// nodeOf 取出某个节点的配置副本（测试里的读侧统一走它）。
func nodeOf(t *testing.T, a *App, id string) config.Node {
	t.Helper()
	n, ok := a.GetNode(id)
	if !ok {
		t.Fatalf("节点 %s 不存在", id)
	}
	return n
}

// TestFailedLatencyTestKeepsLastValue 是缺陷的核心：
// 一次成功的 TCP 测试之后再测一次失败，旧延迟必须留下来、并被标成"过期"。
func TestFailedLatencyTestKeepsLastValue(t *testing.T) {
	a := newTestApp(t, []config.Node{testNode("n1")}, config.Front{})

	a.updateTest("n1", TestTCP, false, measured{ok: true, ms: 312})
	before := nodeOf(t, a, "n1")
	if before.LatencyMs != 312 || before.LatencyAt.IsZero() || before.LatencyFailed {
		t.Fatalf("前置条件不成立：应当测出 312ms 且标记为有效，实际 %+v", before)
	}

	// 用户又测了一次：这次连不上。失败只是"这次的结论"，不是"上次的数值作废"。
	a.updateTest("n1", TestTCP, true, measured{})

	got := nodeOf(t, a, "n1")
	if got.LatencyMs != 312 {
		t.Fatalf("失败不得抹掉上次测出来的延迟：期望 312，实际 %d", got.LatencyMs)
	}
	if !got.LatencyAt.Equal(before.LatencyAt) {
		t.Errorf("没测出数值就不该刷新测量时间：%v → %v", before.LatencyAt, got.LatencyAt)
	}
	if !got.LatencyFailed {
		t.Error("最近一次延迟测试没测出数值，应当标记为过期")
	}
	// 失败结论本身照旧要更新（「移除测试无效项」靠它，审查 #8）
	if !got.TestFailed || got.TestedAt.IsZero() {
		t.Errorf("失败结论必须照常写回：testFailed=%v testedAt=%v", got.TestFailed, got.TestedAt)
	}
	if got.LatencyKind != "tcp" {
		t.Errorf("没测出数值时不该改 LatencyKind（否则界面会改显示另一列的空值）：%q", got.LatencyKind)
	}
}

// TestFailedSpeedTestKeepsLastValue 同上，针对速度列。
func TestFailedSpeedTestKeepsLastValue(t *testing.T) {
	a := newTestApp(t, []config.Node{testNode("n1")}, config.Front{})

	a.updateTest("n1", TestSpeed, false, measured{ok: true, speed: 12.5})
	a.updateTest("n1", TestSpeed, false, measured{})

	got := nodeOf(t, a, "n1")
	if got.SpeedMBps != 12.5 {
		t.Fatalf("测速失败不得抹掉上次测出来的速度：期望 12.5，实际 %v", got.SpeedMBps)
	}
	if got.SpeedAt.IsZero() {
		t.Error("上次成功值的测量时间应当留着，界面要靠它提示“上次成功于 xx 前”")
	}
	if !got.SpeedFailed {
		t.Error("最近一次测速没测出数值，应当标记为过期")
	}
}

// TestFailureIsPerFamily 锁死"按族记失败标记"：
// 一次测速失败不该把延迟也标成过期，反之亦然 —— 否则用户测个速，延迟列就变灰。
func TestFailureIsPerFamily(t *testing.T) {
	a := newTestApp(t, []config.Node{testNode("n1")}, config.Front{})

	a.updateTest("n1", TestTCP, false, measured{ok: true, ms: 80})
	a.updateTest("n1", TestSpeed, false, measured{ok: true, speed: 9.5})

	// 测速失败：延迟一族不受影响
	a.updateTest("n1", TestSpeed, false, measured{})
	got := nodeOf(t, a, "n1")
	if got.LatencyFailed {
		t.Error("测速失败不该把延迟标记成过期")
	}
	if got.SpeedFailed != true || got.SpeedMBps != 9.5 {
		t.Errorf("速度应当保留上次的值并标记过期：%v / %v", got.SpeedMBps, got.SpeedFailed)
	}

	// 延迟失败：速度一族不受影响
	a.updateTest("n1", TestReal, false, measured{})
	got = nodeOf(t, a, "n1")
	if !got.LatencyFailed {
		t.Error("真连接测试没测出数值，延迟一族应当标记为过期")
	}
	if got.SpeedFailed != true || got.SpeedMBps != 9.5 {
		t.Errorf("延迟失败不该动速度列：%v / %v", got.SpeedMBps, got.SpeedFailed)
	}
}

// TestSuccessClearsFailedAndRefreshesValue 反向钉死：恢复之后必须回到"有效"，
// 否则一个已经恢复的节点会永远显示成过期值。
func TestSuccessClearsFailedAndRefreshesValue(t *testing.T) {
	a := newTestApp(t, []config.Node{testNode("n1")}, config.Front{})

	a.updateTest("n1", TestTCP, false, measured{ok: true, ms: 300})
	a.updateTest("n1", TestTCP, true, measured{})
	if !nodeOf(t, a, "n1").LatencyFailed {
		t.Fatal("前置条件不成立：应当先进入过期态")
	}

	a.updateTest("n1", TestReal, false, measured{ok: true, ms: 120})

	got := nodeOf(t, a, "n1")
	if got.LatencyFailed {
		t.Error("测通了就该回到有效态")
	}
	if got.RealMs != 120 || got.LatencyKind != "real" {
		t.Errorf("真连接结果应当写进 RealMs 并成为显示值：%+v", got)
	}
	if got.LatencyAt.IsZero() {
		t.Error("测出数值就必须刷新测量时间")
	}
	if got.TestFailed {
		t.Error("成功的测试必须把上一次的失败结论清掉")
	}
}

// TestZeroMsSuccessIsNotAFailure 锁死"0 与没测出数值是两件事"。
//
// 成功但不足 1ms 的测试（本机/局域网节点）数值就是 0。判据一旦写成
// `ms == 0 即失败`，这些节点会被判成"过期/失败"—— 那正是审查 #8 的老坑
// （拿 latencyMs == 0 反推失败），所以这次显式区分，不能从 0 反推。
func TestZeroMsSuccessIsNotAFailure(t *testing.T) {
	a := newTestApp(t, []config.Node{testNode("n1")}, config.Front{})

	// 先失败，再"成功但 0ms"
	a.updateTest("n1", TestTCP, true, measured{})
	a.updateTest("n1", TestTCP, false, measured{ok: true, ms: 0})

	got := nodeOf(t, a, "n1")
	if got.LatencyFailed {
		t.Error("0ms 的成功也是成功，不该标记成失败/过期")
	}
	if got.LatencyAt.IsZero() {
		t.Error("0ms 也是一次测量，应当记下测量时间")
	}
}

// TestNeverMeasuredIsNotFailed 锁死第三态：从没测过的节点不是"失败"。
//
// 界面靠"无值 + 未失败"显示 "—"、"无值 + 失败"显示"失败"、
// "有值 + 失败"显示变灰的旧值。若把没测过也标成失败，刚导入的节点会一片"失败"。
func TestNeverMeasuredIsNotFailed(t *testing.T) {
	a := newTestApp(t, []config.Node{testNode("n1")}, config.Front{})

	got := nodeOf(t, a, "n1")
	if got.LatencyFailed || got.SpeedFailed {
		t.Error("从没测过不等于失败")
	}
	if !got.LatencyAt.IsZero() || !got.SpeedAt.IsZero() {
		t.Error("从没测过就不该有测量时间")
	}

	// 视图要把这三样原样带出去，否则界面无从区分三态
	v := a.Nodes()
	if len(v) != 1 {
		t.Fatalf("应当有 1 行，实际 %d", len(v))
	}
	if v[0].LatencyFailed || v[0].SpeedFailed || !v[0].LatencyAt.IsZero() || !v[0].SpeedAt.IsZero() {
		t.Errorf("NodeView 没带出测试状态：%+v", v[0])
	}
}

// TestExitIPStillClearedOnFailure 说明一处**故意的不一致**：
// 出口 IP 失败时仍然清空，不做"上次成功值"。
//
// 理由：它是"当前的实际出口"，留一个过期的 IP 比没有更危险 —— 用户会按它判断
// 自己落在哪里。延迟/速度是性能数据，旧值仍然有参考价值，所以两者的处理不同。
func TestExitIPStillClearedOnFailure(t *testing.T) {
	a := newTestApp(t, []config.Node{testNode("n1")}, config.Front{})

	a.updateTest("n1", TestExit, false, measured{ok: true, exitIP: "203.0.113.7"})
	if got := a.ExitIPs()["n1"]; got != "203.0.113.7" {
		t.Fatalf("前置条件不成立：出口 IP 应当写回，实际 %q", got)
	}

	a.updateTest("n1", TestExit, false, measured{})
	if got, ok := a.ExitIPs()["n1"]; ok {
		t.Fatalf("探测失败必须清掉出口 IP（不能留一个过期的落地地址），实际 %q", got)
	}
}

// TestStaleStateSurvivesRoundTrip 锁死"这些状态经得起界面绕一圈"。
//
// 编辑节点时界面是把整行（NodeView 的 JSON）**原样**当作节点送回后端的
// （见 NodeDialog.vue：payload = 深拷贝的 form）。所以新字段的 json tag 必须与
// config.Node 对得上，否则用户改一次别名就把"上次成功值/过期标记"清空了 ——
// 而且清得无声无息。这里就用一次真实的 JSON 往返来钉死它。
func TestStaleStateSurvivesRoundTrip(t *testing.T) {
	a := newTestApp(t, []config.Node{testNode("n1")}, config.Front{})

	a.updateTest("n1", TestTCP, false, measured{ok: true, ms: 260})
	a.updateTest("n1", TestTCP, true, measured{})

	blob, err := json.Marshal(a.Nodes()[0])
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(blob, &raw); err != nil {
		t.Fatal(err)
	}
	// NodeView 里的 transport 是给人看的文本（"tcp+tls"），而配置里同名的
	// TransportOptions 是个结构体 —— 两者**同名不同型**，对话框自己会重建它
	// （见 NodeDialog.vue 里 `form.transport = Object.assign(blankForm().transport, ...)`）。
	// 这里照做，只留本缺陷新增的那几个字段走往返。
	delete(raw, "transport")
	blob, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var upd config.Node
	if err := json.Unmarshal(blob, &upd); err != nil {
		t.Fatal(err)
	}
	if upd.LatencyMs != 260 || !upd.LatencyFailed || upd.LatencyAt.IsZero() {
		t.Fatalf("视图里的测试状态没能原样解析回配置（json tag 对不上）：%+v", upd)
	}
	upd.Name = "改过别名"
	if _, err := a.UpdateNode(upd); err != nil {
		t.Fatal(err)
	}

	got := nodeOf(t, a, "n1")
	if got.Name != "改过别名" {
		t.Fatalf("改名没生效：%q", got.Name)
	}
	if got.LatencyMs != 260 || !got.LatencyFailed || got.LatencyAt.IsZero() {
		t.Errorf("编辑之后测试状态不该丢（检查 json tag 是否对齐）：%+v", got)
	}
}

// TestCloneDropsStaleTestState 确认克隆出来的节点是"没测过"，而不是继承原节点的
// 过期标记与测量时间。
func TestCloneDropsStaleTestState(t *testing.T) {
	a := newTestApp(t, []config.Node{testNode("n1")}, config.Front{})

	a.updateTest("n1", TestTCP, false, measured{ok: true, ms: 150})
	a.updateTest("n1", TestSpeed, false, measured{ok: true, speed: 3.5})
	a.updateTest("n1", TestTCP, true, measured{})

	dup, err := a.CloneNode("n1")
	if err != nil {
		t.Fatal(err)
	}
	if dup.LatencyMs != 0 || dup.RealMs != 0 || dup.SpeedMBps != 0 || dup.LatencyKind != "" {
		t.Errorf("克隆出来的节点不该带着原节点的数值：%+v", dup)
	}
	if dup.LatencyFailed || dup.SpeedFailed {
		t.Errorf("克隆出来的节点不该带着原节点的失败标记：%+v", dup)
	}
	if !dup.LatencyAt.IsZero() || !dup.SpeedAt.IsZero() {
		t.Errorf("克隆出来的节点不该带着原节点的测量时间：%+v", dup)
	}
}
