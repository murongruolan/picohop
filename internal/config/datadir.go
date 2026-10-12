package config

import (
	"fmt"
	"os"
)

// EnsureDataDir 创建数据目录，并把它收紧到"只有当前用户、Administrators、SYSTEM"。
//
// 【为什么要收紧】data\ 是**运行期目录**：config.json（节点的全部凭据 —— 密码、
// UUID、WireGuard 私钥）和 picohop.log 都在里面。它默认落在仓库目录下，而仓库
// 往往就在某个盘符根目录下面，于是继承了盘符根那条
//
//	NT AUTHORITY\Authenticated Users:(I)(M)
//
// 也就是说**同机任何一个本地用户都能读写这份配置**（改端口、读走全部节点凭据）。
// 最坑的是这条权限是"继承来的"：在资源管理器里看只写着"继承的"，
// 看起来像系统默认值，没人会去动它；实测见 datadir_windows_test.go 的回归测试。
//
// 【调用点】必须早于对数据目录的**任何一次写入**，否则那些文件会带着宽松的继承
// 权限落盘：
//   - internal/app 的 New()：命令行与界面都从这里进来，日志文件就在其后打开；
//   - desktop 的 main()：Wails 创建 WebView2 用户数据目录（data\webview）之前。
//
// 【为什么不在这里自己递归改子文件】Windows 上设置目录 DACL 时，系统会把带
// OI/CI 的 ACE **自动传播到已经存在的子对象**上（实测确认，连子文件上的
// Authenticated Users 一起收掉），所以这里只需要对目录调一次。
//
// 【返回错误为什么不致命】数据目录允许被指到别处（PICOHOP_DATA_DIR），
// 可能落在 FAT/exFAT 分区或网络盘上 —— 那里没有 NTFS ACL，收紧必然失败。
// 这种情况**继续启动**、但由调用方把警告明确打出来（stderr + 日志），
// 比让整个程序起不来更有用；但绝不能静默放过。
func EnsureDataDir() error {
	dir := DataDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建数据目录 %s 失败: %w", dir, err)
	}
	return restrictDataDir(dir)
}
