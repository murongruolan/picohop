//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"chainproxy/internal/app"
	"chainproxy/internal/config"
)

// TestSystemProxyNeedsRestore 是审查 #5（「恢复默认设置」→ 退出跳过系统代理还原 →
// 整机断网）的回归测试。
//
// 【为什么钉这四个组合】判据的每一半都在防一类事故：
//   - 开关被清掉但备份还在 → 必须还原（这就是 #5 的断网场景）；
//   - 开关开着但备份没了（被人删了/写失败）→ 也要还原（fail-closed）；
//   - 两者都没有 → **绝不能**动注册表，否则会清掉用户自己配的系统代理。
//
// 测试只用 CHAINPROXY_DATA_DIR + 真实的备份文件，不碰真注册表。
func TestSystemProxyNeedsRestore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHAINPROXY_DATA_DIR", dir)

	backup := sysProxySnapshotPath()
	if filepath.Dir(backup) != config.DataDir() {
		t.Fatalf("备份文件不在数据目录里：%s（DataDir=%s）", backup, config.DataDir())
	}

	writeBackup := func() {
		t.Helper()
		if err := os.WriteFile(backup, []byte(`{"proxyEnable":1,"proxyServer":"127.0.0.1:1080"}`), 0o600); err != nil {
			t.Fatalf("写备份失败：%v", err)
		}
	}
	removeBackup := func() {
		t.Helper()
		if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
			t.Fatalf("删备份失败：%v", err)
		}
	}

	cases := []struct {
		name       string
		flagOn     bool
		withBackup bool
		want       bool
	}{
		{"没开过全局代理也没备份：不许动用户的系统代理", false, false, false},
		{"开关开着但备份丢了：仍要还原（fail-closed）", true, false, true},
		{"「恢复默认设置」把开关清了、备份还在：必须还原（审查 #5）", false, true, true},
		{"开关开着且备份还在：正常退出要还原", true, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			removeBackup()
			if c.withBackup {
				writeBackup()
			}
			if got := systemProxyNeedsRestore(c.flagOn); got != c.want {
				t.Fatalf("systemProxyNeedsRestore(开关=%v, 备份=%v) = %v，期望 %v",
					c.flagOn, c.withBackup, got, c.want)
			}
		})
	}
}

// TestResetSettingsKeepsWorking 保证上面那条新增的守卫没有把"恢复默认"本身弄坏：
// 没开过全局代理（无备份）时不该去碰注册表，设置照常恢复成默认值。
func TestResetSettingsKeepsWorking(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHAINPROXY_DATA_DIR", dir)

	inst, err := app.New(config.ConfigPath())
	if err != nil {
		t.Fatalf("创建应用失败：%v", err)
	}
	t.Cleanup(inst.Close)

	s := inst.Settings()
	s.PortStart = 23456
	if err := inst.SetSettings(s); err != nil {
		t.Fatalf("改设置失败：%v", err)
	}

	api := &API{app: inst}
	got, err := api.ResetSettings()
	if err != nil {
		t.Fatalf("恢复默认设置失败：%v", err)
	}
	want := config.Default().Settings.PortStart
	if got.PortStart != want {
		t.Fatalf("返回值没有恢复默认：PortStart=%d，期望 %d", got.PortStart, want)
	}
	if cur := inst.Settings().PortStart; cur != want {
		t.Fatalf("落到配置里的没有恢复默认：PortStart=%d，期望 %d", cur, want)
	}
}
