//go:build windows

package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestEnableSystemProxyWritesAddressBeforeEnable 是审查 #30（「系统代理注册表非原子」）
// 的回归测试。
//
// 【缺陷原样】enableSystemProxy 原先第一步就写 ProxyEnable=1，最后才写 ProxyServer。
// 注册表写入**不是原子的**：中间任何一步失败（被安全软件拦、键被别的程序占住、句柄失效），
// 用户就被留在"开关开着、地址还是旧的/空的"这个状态 —— 浏览器把流量发给一个没人监听的
// 地址，等于断网，而界面那一步是**报错**的，用户以为没生效。
//
// 【怎么测】真实注册表上没法稳定制造失败，而且写它会弄脏用户自己配的系统代理，
// 所以把"写一个值"（sysProxyWrite）换成假的 writer，逐个位置注入失败，检查两件事：
//  1. 正常路径的写入顺序必须是 地址 → 绕过表 → 清 PAC → 开关；
//  2. 任何一步失败时 ProxyEnable 都**没有被写过**（开关是最后一个动作）。
//
// 备份文件落在 t.TempDir() 里，整条用例不碰真实 data\、也不碰真实注册表。
func TestEnableSystemProxyWritesAddressBeforeEnable(t *testing.T) {
	const addr = "127.0.0.1:10810"

	all := []string{"ProxyServer", "ProxyOverride", "AutoConfigURL", "ProxyEnable"}
	cases := []struct {
		name      string
		failAt    int      // 第几次写失败（从 0 数；-1 表示都不失败）
		wantWrote []string // 失败点之前**成功**写入的名字
	}{
		{"全部成功：地址先写、开关最后", -1, all},
		{"第一步（地址）就失败：开关不能被动过", 0, nil},
		{"第二步（绕过表）失败：开关不能被动过", 1, all[:1]},
		{"第三步（清 PAC）失败：开关不能被动过", 2, all[:2]},
		{"第四步（开关本身）失败：这是唯一允许写开关的位置", 3, all[:3]},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("PICOHOP_DATA_DIR", t.TempDir())

			orig := sysProxyWrite
			t.Cleanup(func() { sysProxyWrite = orig })

			var (
				wrote  []string
				values = map[string]any{}
			)
			sysProxyWrite = func(name string, value any) error {
				if len(wrote) == c.failAt {
					return fmt.Errorf("注入的写入失败：%s", name)
				}
				wrote = append(wrote, name)
				values[name] = value
				return nil
			}

			err := enableSystemProxy(addr)
			if c.failAt < 0 && err != nil {
				t.Fatalf("没有注入失败，enableSystemProxy 却报错：%v", err)
			}
			if c.failAt >= 0 && err == nil {
				t.Fatalf("第 %d 次写入注入了失败，enableSystemProxy 却返回成功", c.failAt+1)
			}

			if got, want := strings.Join(wrote, ","), strings.Join(c.wantWrote, ","); got != want {
				t.Fatalf("写入顺序 = [%s]，期望 [%s]", got, want)
			}
			// 这是本用例的核心断言：没走到"开开关"那一步，就绝不能把 ProxyEnable 写进去。
			if c.failAt >= 0 && c.failAt < len(all)-1 && slices.Contains(wrote, "ProxyEnable") {
				t.Fatalf("中途失败（第 %d 次）却已经写了 ProxyEnable：%v", c.failAt+1, wrote)
			}

			// 顺手钉住写进去的**值**：地址是本地口（不是空串）、PAC 被清掉、开关是 1。
			if v, ok := values["ProxyServer"]; ok && v != addr {
				t.Fatalf("ProxyServer 写成了 %v，期望 %q", v, addr)
			}
			if v, ok := values["AutoConfigURL"]; ok && v != "" {
				t.Fatalf("AutoConfigURL 是 %v，期望空串（清掉 PAC）", v)
			}
			if v, ok := values["ProxyEnable"]; ok && v != uint32(1) {
				t.Fatalf("ProxyEnable 是 %v，期望 uint32(1)", v)
			}
		})
	}
}
