//go:build !windows

package config

import "os"

// restrictDataDir 在非 Windows 上退化成 0700。
//
// 这个缺陷本身是 NTFS 特有的（从盘符根目录继承来一条 Authenticated Users 的
// 可继承 ACE），POSIX 权限位没有"继承"这回事，0700 就是"只有属主能进"，
// 与本包在 Windows 上收紧 DACL 的目标一致。
func restrictDataDir(dir string) error {
	return os.Chmod(dir, 0o700)
}
