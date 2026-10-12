//go:build !windows

package main

// 非 Windows 平台没有"系统代理"这套东西（macOS/Linux 各有各的做法），
// 这里给同名的空实现，让上层代码不用到处判断平台。
//
// enableSystemProxy 返回错误而不是静默成功：界面上点了"全局代理"却什么都没发生、
// 也不报错，是最难查的那种问题。

import "errors"

var errNoSystemProxy = errors.New("当前平台不支持自动配置系统代理")

func enableSystemProxy(string) error { return errNoSystemProxy }
func disableSystemProxy() error      { return nil }
func restoreSystemProxyIfNeeded() bool {
	return false
}

// systemProxyBackupExists 在非 Windows 上一律为假：这边根本没有"改过系统代理"
// 这回事，也就不会有备份文件（判据含义见 sysproxy_windows.go）。
func systemProxyBackupExists() bool { return false }
func systemProxyIsOurs(string) bool { return false }
