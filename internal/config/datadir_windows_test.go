//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// dacl 读出 path 的 DACL：返回 ACE 列表（SDDL 片段）与"是否已关闭继承"。
func readDACL(t *testing.T, path string) (body string, protected bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("读 %s 的 DACL 失败: %v", path, err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatalf("读 %s 的安全描述符控制位失败: %v", path, err)
	}
	return daclBody(sd.String()), ctrl&windows.SE_DACL_PROTECTED != 0
}

// setPermissiveACL 人为给目录装上"继承自盘符根目录"的那套权限
// （Authenticated Users / Users 完全控制），用来复现缺陷现场。
//
// 真实机器上这些 ACE 是从 F:\ 根继承来的，测试里**不能依赖盘符根长什么样**
// （别的机器上可能是 C:\、也可能是别人改过的），所以自己造一份等价的前置状态。
func setPermissiveACL(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;AU)(A;OICI;FA;;;BU)")
	if err != nil {
		t.Fatalf("构造宽松安全描述符失败: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("取 DACL 失败: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		t.Fatalf("设置宽松 DACL 失败: %v", err)
	}
}

// assertNoForeignAccess 断言 path 上不再有任何"别的本地用户"的授权。
//
// 只看这几条知名 SID 就够了：它们正是继承自盘符根目录、让同机任何用户都能读改
// 配置的那几条（AU=Authenticated Users、BU=Users、WD=Everyone、
// AN=Anonymous、SU=Service）。
func assertNoForeignAccess(t *testing.T, path string) {
	t.Helper()
	body, _ := readDACL(t, path)
	for _, sid := range []string{";;;AU)", ";;;BU)", ";;;WD)", ";;;AN)", ";;;SU)"} {
		if strings.Contains(body, sid) {
			t.Errorf("%s 仍然对 %s 开放: %s", path, sid, body)
		}
	}
}

// TestRestrictDataDirRemovesInheritedAuthenticatedUsers 是这个缺陷的回归测试。
//
// 【为什么权限这种东西也要写测试】改错或改回去**不会**有任何编译错误或运行期
// 报错：程序照跑，只是同机任何一个本地用户都能读到 config.json 里的全部凭据
// （密码、UUID、WireGuard 私钥）。只能用断言把它钉住。
//
// 覆盖三点：
//  1. 目录自己不再给 AU/BU 权限，并且**关掉了继承**（否则父目录的可继承 ACE
//     下一次传播就会把 Authenticated Users 加回来）；
//  2. 目录里**已经存在**的文件（config.json 就是老文件）一起被收掉；
//  3. 收紧之后**自己还能正常读写** —— 别把用户锁在自己的数据目录外面。
func TestRestrictDataDirRemovesInheritedAuthenticatedUsers(t *testing.T) {
	dir := t.TempDir()
	setPermissiveACL(t, dir)

	// 模拟"改权限之前就已经存在"的配置与日志。
	old := filepath.Join(dir, "config.json")
	if err := os.WriteFile(old, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, _ := readDACL(t, old); !strings.Contains(body, ";;;AU)") {
		t.Fatalf("前置条件不成立：新文件应当继承到 Authenticated Users，实际 %s", body)
	}

	want, err := dataDirAllowed()
	if err != nil {
		t.Fatal(err)
	}
	if err := restrictDataDir(dir); err != nil {
		t.Fatalf("收紧数据目录失败: %v", err)
	}

	body, protected := readDACL(t, dir)
	if body != want {
		t.Errorf("数据目录的 ACE 应当是 %s，实际 %s", want, body)
	}
	if !protected {
		t.Error("数据目录必须关闭继承（protected DACL），否则父目录的 Authenticated Users 会被继承回来")
	}
	assertNoForeignAccess(t, old)

	// 新建的文件/子目录必须继承同一套权限（config.json 的原子写、
	// picohop.log、webview 目录都靠这条）。
	fresh := filepath.Join(dir, "picohop.log")
	if err := os.WriteFile(fresh, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertNoForeignAccess(t, fresh)
	sub := filepath.Join(dir, "webview")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	assertNoForeignAccess(t, sub)

	// 收紧不能把自己锁在外面：写、改名、删都得照常能用。
	renamed := filepath.Join(dir, "picohop.log.1")
	if err := os.Rename(fresh, renamed); err != nil {
		t.Fatalf("收紧后无法改名（把自己锁在外面了）: %v", err)
	}
	if err := os.Remove(renamed); err != nil {
		t.Fatalf("收紧后无法删除（把自己锁在外面了）: %v", err)
	}
}

// TestRestrictDataDirIsIdempotent 确认重复调用结果不变、也不报错。
//
// 每次启动都会调一次，而设置 DACL 会连带把 ACE 传播到整个子树上
// （data\ 下面有 WebView2 的用户数据目录），所以实现里有"已经是目标权限就直接
// 返回"的快路径 —— 这里顺带锁住它不会把权限改成别的东西。
func TestRestrictDataDirIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := restrictDataDir(dir); err != nil {
		t.Fatal(err)
	}
	first, _ := readDACL(t, dir)
	if err := restrictDataDir(dir); err != nil {
		t.Fatalf("第二次收紧报错: %v", err)
	}
	if second, _ := readDACL(t, dir); second != first {
		t.Errorf("重复收紧后权限变了：%s -> %s", first, second)
	}
}

// TestEnsureDataDirCreatesAndTightens 覆盖入口函数：目录不存在时要建出来，
// 且在写任何文件之前就已经是收紧后的权限。
func TestEnsureDataDirCreatesAndTightens(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	t.Setenv("PICOHOP_DATA_DIR", dir)

	if err := EnsureDataDir(); err != nil {
		t.Fatalf("EnsureDataDir 失败: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("数据目录没有建出来: %v", err)
	}
	want, err := dataDirAllowed()
	if err != nil {
		t.Fatal(err)
	}
	if body, protected := readDACL(t, dir); body != want || !protected {
		t.Errorf("数据目录权限不对：body=%s protected=%v，期望 body=%s protected=true", body, protected, want)
	}
	if !strings.Contains(want, fmt.Sprintf(";;;%s)", currentUserSIDForTest(t))) {
		t.Errorf("允许列表里应当包含当前用户 SID：%s", want)
	}
}

// currentUserSIDForTest 返回当前进程 token 的用户 SID 字符串。
func currentUserSIDForTest(t *testing.T) string {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("取当前用户 SID 失败: %v", err)
	}
	return u.User.Sid.String()
}
