//go:build windows

package config

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// restrictDataDir 把数据目录的 DACL 换成"当前用户 + Administrators + SYSTEM"三条完全控制，
// 并**关闭继承**。
//
// 【为什么必须关继承（SDDL 里的 D:P）】data\ 建在仓库下、仓库又在盘符根下，
// 那条 `Authenticated Users:(I)(M)` 是从盘符根继承下来的可继承 ACE。
// 只要目录自己还开着继承，我们删掉它，系统下一次传播就会把它再加回来 ——
// 等于白改。所以这里用的是 protected DACL（SetNamedSecurityInfo 里同时传
// PROTECTED_DACL_SECURITY_INFORMATION）。
//
// 【为什么用 x/sys 设 DACL 而不是 exec icacls】icacls 的输出是本地化文本、
// 解析不可靠，还要多起一个进程；而 SetNamedSecurityInfo 在"自己就是属主"时
// 就允许改 DACL，**不需要管理员权限** —— 这是"非管理员也必须能跑"的关键。
//
// 【为什么是三主体】当前用户（进程 token 的 user SID）保证非管理员能用；
// Administrators 保证管理员排障/备份；SYSTEM 保证服务、备份、杀软等系统进程
// 不会因为读不到配置而报错。其余主体（Authenticated Users / Users / Everyone）
// 一律不给 —— 它们正是这个缺陷的来源。
func restrictDataDir(dir string) error {
	want, err := dataDirAllowed()
	if err != nil {
		return err
	}

	// 【快路径】已经是我们要的权限就直接返回。
	//
	// 设置 DACL 会让 NTFS 把可继承的 ACE 递归传播到已有子对象上（见 datadir.go），
	// 而 data\ 下面还有 WebView2 的用户数据目录（可能上万个文件）。
	// 每次启动都无脑重设一遍，等于每次启动都对着整棵树走一遍。
	if sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION); err == nil {
		if daclBody(sd.String()) == want {
			return nil
		}
	}

	sd, err := windows.SecurityDescriptorFromString("D:P" + want)
	if err != nil {
		return fmt.Errorf("构造数据目录 DACL 失败: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("从安全描述符里取 DACL 失败: %w", err)
	}
	// 注意：这一次调用会**连带把 ACE 传播到 data\ 下已经存在的子文件/子目录**
	// （实测：已有的 config.json 会从四条继承 ACE 变成我们那三条的 D:AI(...)），
	// 所以老配置不会留在"改权限之前"的宽松状态里。
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("设置数据目录权限失败: %w", err)
	}
	return nil
}

// dataDirAllowed 返回数据目录唯一允许的 ACE 列表（SDDL 片段，不含 "D:" 前缀）。
//
// OI/CI 让目录下**新建**的文件与子目录自动继承同一套权限：
// config.json、chainproxy.log、原子写用的 .tmp、sysproxy-backup.json 都靠它。
func dataDirAllowed() (string, error) {
	// GetCurrentProcessToken 拿到的是伪句柄，不需要（也不该）Close；
	// GetTokenUser 的内存是 Go 堆上的，SID 指针在本次调用内一直有效。
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("读取进程 token 的用户 SID 失败: %w", err)
	}
	if u.User.Sid == nil {
		return "", fmt.Errorf("进程 token 里没有用户 SID")
	}
	return fmt.Sprintf("(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;%s)", u.User.Sid.String()), nil
}

// daclBody 把 SDDL 里的 ACE 列表抠出来（去掉 "D:" 和 P/AI 这些标志位），
// 这样比较"权限对不对"时不受标志位写法的影响：差异只会导致多设一次，不会漏设。
func daclBody(sddl string) string {
	if i := strings.IndexByte(sddl, '('); i >= 0 {
		return sddl[i:]
	}
	return sddl
}
