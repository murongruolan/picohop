package config

import (
	"testing"
	"time"
)

// TestSettingsIdleTimeout 锁死"秒 → 时长"的换算与两个边界。
//
// 【为什么专门测 0 和负数】存储形式是"0 表示没设过"，而下游中继里
// idle <= 0 的含义是**不设空闲超时**（永不超时）。一旦这里把 0 原样透传，
// 缺陷就从"15 秒就断"翻成"永远不断" —— 死连接再也不回收。负数同理由此拉回默认值。
func TestSettingsIdleTimeout(t *testing.T) {
	cases := []struct {
		sec  int
		want time.Duration
	}{
		{0, 300 * time.Second},   // 没设过 → 默认值
		{-1, 300 * time.Second},  // 非法值 → 默认值（fail-closed）
		{1, 1 * time.Second},     // 用户显式设的最小值
		{240, 240 * time.Second}, // 用户改过的值必须原样生效
	}
	for _, c := range cases {
		got := Settings{IdleTimeoutSec: c.sec}.IdleTimeout()
		if got != c.want {
			t.Errorf("IdleTimeoutSec=%d 应得 %v，实际 %v", c.sec, c.want, got)
		}
	}

	// 内置默认值就是 300 秒（界面「空闲自动断开」的初始值）
	if Default().Settings.IdleTimeout() != 300*time.Second {
		t.Errorf("内置默认空闲超时应为 300s，实际 %v", Default().Settings.IdleTimeout())
	}
	if DefaultIdleTimeoutSec != 300 {
		t.Errorf("默认秒数常量应为 300，实际 %d", DefaultIdleTimeoutSec)
	}
}

// TestNormalizeRepairsNegativeIdleTimeout 确认读盘时把非法值修回默认值，
// 同时又不能覆盖用户显式设过的合法值。
func TestNormalizeRepairsNegativeIdleTimeout(t *testing.T) {
	bad := Default()
	bad.Settings.IdleTimeoutSec = -5
	bad.normalize()
	if bad.Settings.IdleTimeoutSec != DefaultIdleTimeoutSec {
		t.Errorf("负数应被修回默认值 %d，实际 %d", DefaultIdleTimeoutSec, bad.Settings.IdleTimeoutSec)
	}

	ok := Default()
	ok.Settings.IdleTimeoutSec = 240
	ok.normalize()
	if ok.Settings.IdleTimeoutSec != 240 {
		t.Errorf("用户设的 240 不该被改，实际 %d", ok.Settings.IdleTimeoutSec)
	}
}
