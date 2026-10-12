//go:build windows

package main

import (
	"testing"

	"chainproxy/internal/app"
	"chainproxy/internal/config"
)

// TestResetSettingsKeepsBottomPanelLayout 是"恢复默认设置把界面布局偏好也抹掉"
// 这条路的回归测试。
//
// 【为什么值得单独一条】底部栏的展开 / 页签 / 高度存在 Settings 里，但它不是
// "设置项"，而是"上次看到哪儿"。「恢复默认设置」以前是拿 Default().Settings
// 整个盖上去 —— 用户点一次按钮，展开状态与页签就永久回到出厂值，
// 现象和他的报障一模一样："偏好不落盘"。
//
// 反向验证：把 desktop/api.go 里带过去那三行注释掉，本测试立刻红
// （open=false view="" height=0）。
//
// 顺带守住另一半：真正的行为设置（这里用 PortStart 代表）仍必须恢复成默认值 ——
// 别为了保住布局偏好把「恢复默认设置」弄成"什么都不恢复"。
func TestResetSettingsKeepsBottomPanelLayout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHAINPROXY_DATA_DIR", dir)

	inst, err := app.New(config.ConfigPath())
	if err != nil {
		t.Fatalf("创建应用失败：%v", err)
	}
	t.Cleanup(inst.Close)

	s := inst.Settings()
	s.PortStart = 23456
	s.BottomPanelOpen = true
	s.BottomPanelView = "sites"
	s.BottomPanelHeight = 333
	if err := inst.SetSettings(s); err != nil {
		t.Fatalf("改设置失败：%v", err)
	}

	api := &API{app: inst}
	if _, err := api.ResetSettings(); err != nil {
		t.Fatalf("恢复默认设置失败：%v", err)
	}

	got := inst.Settings()
	if !got.BottomPanelOpen || got.BottomPanelView != "sites" || got.BottomPanelHeight != 333 {
		t.Fatalf("恢复默认设置把界面布局偏好一起清了：open=%v view=%q height=%d（期望 true/sites/333）",
			got.BottomPanelOpen, got.BottomPanelView, got.BottomPanelHeight)
	}
	if want := config.Default().Settings.PortStart; got.PortStart != want {
		t.Fatalf("行为设置应当恢复默认：PortStart=%d，期望 %d", got.PortStart, want)
	}

	// 还要确认它真的落到了磁盘：只改内存的话，重启后偏好照样丢。
	onDisk, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatalf("重新读取配置失败：%v", err)
	}
	if !onDisk.Settings.BottomPanelOpen || onDisk.Settings.BottomPanelView != "sites" ||
		onDisk.Settings.BottomPanelHeight != 333 {
		t.Fatalf("磁盘上的布局偏好被清掉了：open=%v view=%q height=%d",
			onDisk.Settings.BottomPanelOpen, onDisk.Settings.BottomPanelView, onDisk.Settings.BottomPanelHeight)
	}
	if onDisk.Settings.PortStart != config.Default().Settings.PortStart {
		t.Fatalf("磁盘上的行为设置没有恢复默认：PortStart=%d", onDisk.Settings.PortStart)
	}
}
