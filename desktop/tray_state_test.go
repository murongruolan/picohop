package main

import (
	"bytes"
	"runtime"
	"testing"
)

// TestTrayIconForHasAllModes 守住"三种状态各有一份可用、且互不相同"的图标。
//
// 【为什么值得单测】//go:embed 的路径写错、或生成脚本忘了跑，**编译不会报错**
// （文件在就嵌进去了，内容是空的也不报），只有用户会看到托盘变成空白方块。
// 所以这里按格式校验：Windows 的托盘链路认 ICO（魔数 + 条目数），其余平台认 PNG。
// 另外三种状态的字节必须真的不同 —— 否则"切状态"在界面上毫无反馈。
func TestTrayIconForHasAllModes(t *testing.T) {
	modes := []trayMode{trayModeNormal, trayModeGlobal, trayModeTun}
	seen := map[string]trayMode{}
	for _, m := range modes {
		icon := trayIconFor(m)
		name := trayModeName(m)
		if len(icon) == 0 {
			t.Fatalf("%s：图标是空的（embed 路径错了，或没跑 scripts\\gen_icons.ps1）", name)
		}
		if runtime.GOOS == "windows" {
			if len(icon) < 6 {
				t.Fatalf("%s：ICO 太短（%d 字节）", name, len(icon))
			}
			if !bytes.Equal(icon[:4], []byte{0x00, 0x00, 0x01, 0x00}) {
				t.Fatalf("%s：不是 ICO 魔数（前 4 字节 % x）", name, icon[:4])
			}
			entries := int(icon[4]) | int(icon[5])<<8
			if entries < 5 {
				t.Fatalf("%s：ICO 只有 %d 个尺寸，托盘在高 DPI 下会糊", name, entries)
			}
		} else {
			if len(icon) < 8 || !bytes.Equal(icon[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}) {
				t.Fatalf("%s：不是 PNG 魔数", name)
			}
		}
		if prev, dup := seen[string(icon)]; dup {
			t.Fatalf("%s 与 %s 的图标字节完全相同：换状态等于没换", name, trayModeName(prev))
		}
		seen[string(icon)] = m
	}
}

// TestWindowIconEmbedded 守住窗口图标那份 embed。
//
// Windows 上它目前只是备用（窗口/任务栏图标来自 exe 的 RT_GROUP_ICON 资源，见
// tray_icons_windows.go），但 Linux 的窗口图标走的就是它，所以同样要盯着。
func TestWindowIconEmbedded(t *testing.T) {
	if len(windowIcon) == 0 {
		t.Fatal("windowIcon 是空的：embed 路径错了，或没跑 scripts\\gen_icons.ps1")
	}
	if runtime.GOOS != "windows" {
		if !bytes.Equal(windowIcon[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}) {
			t.Fatal("非 Windows 的 windowIcon 应当是 PNG（LinuxWindow.Icon 收 PNG）")
		}
	}
}

// TestTrayPlanFirstPushAlwaysPushes 守住那个真发生过的 bug：首次设置必须真的推。
//
// 【那个 bug 长什么样】登记托盘时若拿"结构体零值"当当前状态，而零值就是 trayModeNormal，
// 于是"设成普通"被判成没变化 → 永远不调 SetIcon → 托盘一直显示系统默认图标。
// 所以这里明确断言：从"未设置"到任何状态都必须推。
func TestTrayPlanFirstPushAlwaysPushes(t *testing.T) {
	for _, want := range []trayMode{trayModeNormal, trayModeGlobal, trayModeTun} {
		if _, push := trayPlan(trayModeUnknown, want); !push {
			t.Fatalf("从未设置切到「%s」必须推图标", trayModeName(want))
		}
	}
	// 同状态重复设置不该重复推（避免每次都打一次进程外的 Shell 调用）
	if _, push := trayPlan(trayModeNormal, trayModeNormal); push {
		t.Fatal("同状态重复设置不该重复推")
	}
	if _, push := trayPlan(trayModeGlobal, trayModeNormal); !push {
		t.Fatal("状态变化必须推")
	}
	if next, _ := trayPlan(trayModeUnknown, trayModeNormal); next != trayModeNormal {
		t.Fatalf("推完之后当前状态应当是目标状态，实际 %d", next)
	}
}

// TestTrayModeName 日志里要能看出当时是哪个状态（排查"图标怎么没变"全靠它）。
func TestTrayModeName(t *testing.T) {
	for m, want := range map[trayMode]string{
		trayModeUnknown: "未设置",
		trayModeNormal:  "普通",
		trayModeGlobal:  "全局代理",
		trayModeTun:     "TUN（预留）",
	} {
		if got := trayModeName(m); got != want {
			t.Fatalf("trayModeName(%d) = %q，期望 %q", m, got, want)
		}
	}
	// 【零值是"未设置"而不是"普通"，这是有意的】登记托盘时先把它标成"未设置"，
	// 第一次设置才会真的推给系统（见 TestTrayPlanFirstPushAlwaysPushes）。
	if trayMode(0) != trayModeNormal || trayModeUnknown >= 0 {
		t.Fatal("trayMode 的取值约定变了：未知档必须是负数、0 仍是普通")
	}
}

// TestSetTrayModeWithoutTray 托盘没建起来时（比如系统不允许）也不能崩。
//
// 【为什么要测】托盘是在 app.Run() 之前建的，那时的失败只有日志能记录；
// 而全局代理模式那条路径**照样**会走到换图标这一步 —— 这里必须静默降级，
// 不能因为"没有托盘"就把系统代理的操作带崩（那是整机断网级别的事）。
func TestSetTrayModeWithoutTray(t *testing.T) {
	a := &API{}
	a.setTrayMode(trayModeGlobal) // 不该 panic
	a.trayMu.Lock()
	got := a.trayMode
	a.trayMu.Unlock()
	if got != trayModeGlobal {
		t.Fatalf("状态没记住：%d", got)
	}
	a.setTrayMode(trayModeGlobal) // 重复设置同状态也不该 panic
	a.setTrayMode(trayModeNormal)
	a.trayMu.Lock()
	got = a.trayMode
	a.trayMu.Unlock()
	if got != trayModeNormal {
		t.Fatalf("状态没切回来：%d", got)
	}
}

// TestRefreshTrayModeWithoutBackend 后端还没起来时按"普通"处理（不能崩、不能误判成全局）。
func TestRefreshTrayModeWithoutBackend(t *testing.T) {
	a := &API{}
	a.refreshTrayMode()
	a.trayMu.Lock()
	got := a.trayMode
	a.trayMu.Unlock()
	if got != trayModeNormal {
		t.Fatalf("没有后端时应为普通状态，实际 %d", got)
	}
}
