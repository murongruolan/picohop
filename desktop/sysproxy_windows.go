//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows/registry"

	"chainproxy/internal/config"
)

// Windows 系统代理（WinINET）的设置位置。
//
// 参考了主流客户端的实现（同类实现）：
// 它写的就是这个键 + ProxyEnable/ProxyServer/ProxyOverride/AutoConfigURL，
// 然后用 InternetSetOption 通知正在运行的程序立刻生效。
const proxyRegPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// 默认绕过列表：本地与内网不走代理。
//
// 不绕过的话，"访问本机服务"会绕一圈代理（还可能因为代理没起来直接失败），
// 而主流客户端也会写一个类似的默认值。
const proxyBypass = `<local>;localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.2*;172.30.*;172.31.*;192.168.*`

var (
	wininet                    = syscall.NewLazyDLL("wininet.dll")
	procInternetSetOptionW     = wininet.NewProc("InternetSetOptionW")
	internetOptionRefresh      = uintptr(37)
	internetOptionSettingsChgd = uintptr(39)
)

// sysProxySnapshot 是"改之前的样子"。
//
// 【为什么必须备份】改系统代理是**进程外**的状态：程序被强杀、崩溃、断电时，
// 没有机会把设置改回去 —— 那用户的整台机器就上不了网了（浏览器一直连一个死端口）。
// 所以：改动**之前**先落盘一份，启动时若发现这份文件还在，就说明上次没还原干净，
// 立刻自愈。这是主流客户端没做的一件事（它直接清零）。
type sysProxySnapshot struct {
	ProxyEnable   uint32 `json:"proxyEnable"`
	ProxyServer   string `json:"proxyServer"`
	ProxyOverride string `json:"proxyOverride"`
	AutoConfigURL string `json:"autoConfigURL"`
}

func sysProxySnapshotPath() string {
	return filepath.Join(config.DataDir(), "sysproxy-backup.json")
}

func readSysProxySnapshot() (sysProxySnapshot, error) {
	var s sysProxySnapshot
	s.ProxyEnable = 0xFFFFFFFF // 读不到就当作"没有设置过"
	k, err := registry.OpenKey(registry.CURRENT_USER, proxyRegPath, registry.QUERY_VALUE)
	if err != nil {
		return s, err
	}
	defer k.Close()
	if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
		s.ProxyEnable = uint32(v)
	}
	s.ProxyServer, _, _ = k.GetStringValue("ProxyServer")
	s.ProxyOverride, _, _ = k.GetStringValue("ProxyOverride")
	s.AutoConfigURL, _, _ = k.GetStringValue("AutoConfigURL")
	return s, nil
}

func writeSysProxySetting(name string, value any) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, proxyRegPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开注册表键失败（%s）: %w", proxyRegPath, err)
	}
	defer k.Close()
	switch v := value.(type) {
	case uint32:
		return k.SetDWordValue(name, v)
	case string:
		return k.SetStringValue(name, v)
	default:
		return fmt.Errorf("不支持的注册表值类型 %T", value)
	}
}

// notifySysProxyChanged 通知系统"网络设置变了"，让浏览器等程序立刻重新读取
// （只写注册表的话，已经跑着的程序可能要等下次启动才认）。
func notifySysProxyChanged() {
	_, _, _ = procInternetSetOptionW.Call(0, internetOptionSettingsChgd, 0, 0)
	_, _, _ = procInternetSetOptionW.Call(0, internetOptionRefresh, 0, 0)
}

// enableSystemProxy 把系统代理指向 addr（形如 "127.0.0.1:10810"）。
func enableSystemProxy(addr string) error {
	// 先备份。只在**还没有备份**时写：重复开启时不能把"已经是我们的地址"当成原始值，
	// 否则还原就成了把代理指向自己。
	if _, err := os.Stat(sysProxySnapshotPath()); os.IsNotExist(err) {
		snap, rerr := readSysProxySnapshot()
		if rerr != nil {
			// 读不到原值也不算致命（可能从来没设过），但要在日志里留痕
			snap = sysProxySnapshot{ProxyEnable: 0}
		}
		data, _ := json.MarshalIndent(snap, "", "  ")
		if werr := os.WriteFile(sysProxySnapshotPath(), data, 0o600); werr != nil {
			// 备份写不下去就**不要**改系统设置 —— 宁可不生效，也不能留下收不回的改动
			return fmt.Errorf("无法保存系统代理备份，已放弃修改系统设置: %w", werr)
		}
	}

	if err := writeSysProxySetting("ProxyEnable", uint32(1)); err != nil {
		return err
	}
	if err := writeSysProxySetting("ProxyServer", addr); err != nil {
		return err
	}
	if err := writeSysProxySetting("ProxyOverride", proxyBypass); err != nil {
		return err
	}
	// 清掉 PAC：它和"手动代理"同时存在时会优先 PAC，导致我们的地址被忽略
	if err := writeSysProxySetting("AutoConfigURL", ""); err != nil {
		return err
	}
	notifySysProxyChanged()
	return nil
}

// disableSystemProxy 还原系统代理。
//
// 有备份就按备份还原（连用户原本的代理设置一起还回去）；
// 没备份就清空（与主流客户端的行为一致：关掉 = 不用代理）。
func disableSystemProxy() error {
	snap, err := loadSysProxySnapshot()
	if err != nil || snap == nil {
		snap = &sysProxySnapshot{ProxyEnable: 0}
	}
	if err := writeSysProxySetting("ProxyEnable", snap.ProxyEnable); err != nil {
		return err
	}
	if err := writeSysProxySetting("ProxyServer", snap.ProxyServer); err != nil {
		return err
	}
	if err := writeSysProxySetting("ProxyOverride", snap.ProxyOverride); err != nil {
		return err
	}
	if err := writeSysProxySetting("AutoConfigURL", snap.AutoConfigURL); err != nil {
		return err
	}
	notifySysProxyChanged()
	_ = os.Remove(sysProxySnapshotPath())
	return nil
}

func loadSysProxySnapshot() (*sysProxySnapshot, error) {
	data, err := os.ReadFile(sysProxySnapshotPath())
	if err != nil {
		return nil, err
	}
	var s sysProxySnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// systemProxyBackupExists 返回"改注册表之前留下的那份备份还在不在"。
//
// 它存在的意义是当判据用：这份文件由 enableSystemProxy 在**动注册表之前**落盘、
// 由 disableSystemProxy 在还原**成功后**删除，所以"文件还在"就是"注册表确实被
// 我们改过、而且还没还原"唯一的物证 —— 比配置里那个开关可靠（开关会被整体覆盖
// Settings 的操作清成 false，见 api.go 里 systemProxyNeedsRestore 的注释）。
func systemProxyBackupExists() bool {
	_, err := os.Stat(sysProxySnapshotPath())
	return err == nil
}

// restoreSystemProxyIfNeeded 在启动时调用：如果上次没还原干净（崩溃/被强杀），自愈。
//
// 返回 true 表示确实做了一次还原（调用方应当写进日志/通知用户）。
func restoreSystemProxyIfNeeded() bool {
	if _, err := os.Stat(sysProxySnapshotPath()); err != nil {
		return false
	}
	if err := disableSystemProxy(); err != nil {
		return false
	}
	return true
}

// systemProxyIsOurs 判断当前系统代理是否指向我们（用于界面显示与实际对齐）。
func systemProxyIsOurs(addr string) bool {
	snap, err := readSysProxySnapshot()
	if err != nil {
		return false
	}
	return snap.ProxyEnable == 1 && snap.ProxyServer == addr
}
