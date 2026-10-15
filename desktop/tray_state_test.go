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

// TestTrayModeName 日志里要能看出当时是哪个状态（排查"图标怎么没变"全靠它）。
func TestTrayModeName(t *testing.T) {
	for m, want := range map[trayMode]string{
		trayModeNormal: "普通",
		trayModeGlobal: "全局代理",
		trayModeTun:    "TUN（预留）",
	} {
		if got := trayModeName(m); got != want {
			t.Fatalf("trayModeName(%d) = %q，期望 %q", m, got, want)
		}
	}
	// 零值必须是"普通"：API 结构体的零值就是它，构造漏设时会落到这一档。
	if trayMode(0) != trayModeNormal {
		t.Fatal("trayMode 的零值不是 trayModeNormal")
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
