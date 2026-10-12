package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 这个文件锁死的是"底部栏的界面布局偏好"这三个字段：
//
//	BottomPanelOpen   展开 / 收起
//	BottomPanelView   停在哪个页签
//	BottomPanelHeight 拖出来的高度
//
// 【为什么值得单独一组测试】用户实报的是"我展开着关掉程序，下次进来却是收起的、
// 而且不在我上次看的那个页签"。这类"偏好没落盘"的现象里，配置层能出的错只有两种：
//  1. 字段根本没被存/被读（往返丢字段）；
//  2. 被"默认值"顶掉（normalize / Default 里顺手塞了一个界面默认值）。
//
// 这两种都在这里钉住。界面层的异步竞态与脚手架擦写见
// docs/VERIFY_DISCIPLINE.md 与 scripts/test_panel_prefs.ps1（端到端）。

// testPanelPrefs 是一组刻意**都不是默认值**的偏好：
// 默认是收起 / logs / 0，用它们才能区分"存住了"和"被默认值顶掉了"。
func testPanelPrefs() (open bool, view string, height int) {
	return true, "sites", 333
}

// TestBottomPanelPrefsRoundTrip 锁死布局偏好的落盘往返。
//
// 连存两次是刻意的：程序运行期会多次把整份配置写回磁盘（统计落盘、
// SaveSettings 都会走 Save），只要中间任何一步用 Default() 盖上，第二次读就变了。
func TestBottomPanelPrefsRoundTrip(t *testing.T) {
	open, view, height := testPanelPrefs()
	path := filepath.Join(t.TempDir(), "config.json")

	cfg := Default()
	cfg.Settings.BottomPanelOpen = open
	cfg.Settings.BottomPanelView = view
	cfg.Settings.BottomPanelHeight = height
	if err := cfg.Save(path); err != nil {
		t.Fatalf("保存配置失败：%v", err)
	}

	assertPanelPrefs := func(stage string, s Settings) {
		t.Helper()
		if s.BottomPanelOpen != open || s.BottomPanelView != view || s.BottomPanelHeight != height {
			t.Fatalf("%s：布局偏好被改掉了 —— 拿到 open=%v view=%q height=%d，期望 open=%v view=%q height=%d",
				stage, s.BottomPanelOpen, s.BottomPanelView, s.BottomPanelHeight, open, view, height)
		}
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("读取配置失败：%v", err)
	}
	assertPanelPrefs("首次 Load", got.Settings)

	// 第二次落盘（模拟运行期"把内存里的配置整体写回"）后再读
	if err := got.Save(path); err != nil {
		t.Fatalf("二次保存失败：%v", err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatalf("二次读取失败：%v", err)
	}
	assertPanelPrefs("二次 Load", again.Settings)
}

// TestBottomPanelPrefsNotInventedByDefaults 保证 Default() **不预置**布局偏好。
//
// 这是 config.go 里那条决定的机器可读版本：空 / 0 / false 一律表示"没设过"，
// 界面侧再补默认值（logs / 260 / 收起）。一旦 Default() 里塞上 "logs"，
// "没设过"和"用户真的选了日志"就再也分不开 —— 而「恢复默认设置」正是拿
// Default().Settings 整个盖上去的（desktop/api.go 的 ResetSettings 已单独处理布局偏好）。
func TestBottomPanelPrefsNotInventedByDefaults(t *testing.T) {
	s := Default().Settings
	if s.BottomPanelOpen || s.BottomPanelView != "" || s.BottomPanelHeight != 0 {
		t.Fatalf("Default() 不该带布局偏好，实际 open=%v view=%q height=%d",
			s.BottomPanelOpen, s.BottomPanelView, s.BottomPanelHeight)
	}
}

// TestBottomPanelPrefsSurvivePartialFile 保证"老配置 / 手写的配置只写了这几项"时
// normalize 不会顺手补成界面默认值。
//
// 反过来说：normalize 只许补**行为设置**的缺失值（端口、超时…），
// 布局偏好的缺失值属于界面，不该在这里被编出来。
func TestBottomPanelPrefsSurvivePartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"version":2,"settings":{"bottomPanelOpen":true,"bottomPanelView":"connections"}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("写测试配置失败：%v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if !cfg.Settings.BottomPanelOpen || cfg.Settings.BottomPanelView != "connections" {
		t.Fatalf("显式写下的布局偏好被 normalize 改掉了：open=%v view=%q",
			cfg.Settings.BottomPanelOpen, cfg.Settings.BottomPanelView)
	}
	// 高度没写 → 保持 0（"没设过"），不许在这里补 260
	if cfg.Settings.BottomPanelHeight != 0 {
		t.Fatalf("没写过的高度不该被补成 %d（0 = 没设过，由界面补默认值）", cfg.Settings.BottomPanelHeight)
	}
	// 同时确认行为设置的缺失值仍然照旧被补上（别为了这个改动弄坏了 normalize）
	if cfg.Settings.PortStart == 0 || cfg.Settings.IdleTimeoutSec == 0 {
		t.Fatalf("normalize 仍应补上行为设置的默认值：portStart=%d idleTimeoutSec=%d",
			cfg.Settings.PortStart, cfg.Settings.IdleTimeoutSec)
	}
}

// TestBottomPanelPrefsJSONFieldNames 钉死 JSON 字段名。
//
// 字段名是可迁移格式的一部分：改名字等于让所有老配置里的偏好静默失效
// （反序列化不报错、值直接丢），现象就是"我的展开状态又没了"。
func TestBottomPanelPrefsJSONFieldNames(t *testing.T) {
	data, err := json.Marshal(Default().Settings)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"bottomPanelOpen", "bottomPanelView", "bottomPanelHeight"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("设置里缺少 JSON 字段 %q（改名会让老配置里的偏好静默失效）", k)
		}
	}
}
