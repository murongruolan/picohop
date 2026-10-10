# ---------------------------------------------------------------------------
# 读取 ChainProxy 窗口里各控件的文本与列表内容。
#
#   .\scripts\dump_list.ps1 [进程名]
#
# 所有跨进程读取都走 SendMessageTimeout + 非托管缓冲区，
# 这样即使界面卡死，脚本也会在 2 秒后返回而不是把自己挂住。
# ---------------------------------------------------------------------------
param([string]$ProcName = 'chainproxy')

$ErrorActionPreference = 'Stop'

Add-Type @"
using System; using System.Text; using System.Runtime.InteropServices;
public class Win {
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr l);
  [DllImport("user32.dll")] public static extern bool EnumChildWindows(IntPtr h, EnumProc cb, IntPtr l);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassNameW(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern int GetDlgCtrlID(IntPtr h);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr SendMessageTimeoutW(IntPtr h, uint m, IntPtr w, IntPtr l, uint fl, uint to, out IntPtr res);

  public delegate bool EnumProc(IntPtr h, IntPtr l);
  public static string Cls(IntPtr h){ var sb=new StringBuilder(256); GetClassNameW(h,sb,256); return sb.ToString(); }

  public static string GetText(IntPtr h){
    IntPtr buf = Marshal.AllocHGlobal(4096*2);
    try {
      for (int i=0;i<4096;i++) Marshal.WriteInt16(buf, i*2, 0);
      IntPtr res;
      SendMessageTimeoutW(h, 0x000D, (IntPtr)4096, buf, 2, 2000, out res);
      return Marshal.PtrToStringUni(buf);
    } finally { Marshal.FreeHGlobal(buf); }
  }

  public static long MsgLong(IntPtr h, uint m, IntPtr w){
    IntPtr res; SendMessageTimeoutW(h, m, w, IntPtr.Zero, 2, 2000, out res); return res.ToInt64();
  }
}
"@

$p = Get-Process -Name $ProcName -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $p) { Write-Output "进程 $ProcName 未运行"; exit 1 }

$script:main = [IntPtr]::Zero
$cb = [Win+EnumProc]{
  param($h, $l)
  $pp = 0; [void][Win]::GetWindowThreadProcessId($h, [ref]$pp)
  if ($pp -eq $p.Id -and [Win]::Cls($h) -eq 'ChainProxyMainWindow') { $script:main = $h; return $false }
  return $true
}
[void][Win]::EnumWindows($cb, [IntPtr]::Zero)
if ($script:main -eq [IntPtr]::Zero) { Write-Output "未找到主窗口"; exit 1 }
Write-Output "主窗口 hwnd=$($script:main)  响应=$($p.Responding)"

$script:kids = New-Object System.Collections.ArrayList
$cb2 = [Win+EnumProc]{ param($h,$l) [void]$script:kids.Add($h); return $true }
[void][Win]::EnumChildWindows($script:main, $cb2, [IntPtr]::Zero)

$names = @{
  65538='头部状态'; 65539='头部圆点'; 65546='主开关'; 65547='一键测试';
  131074='前置类型'; 131075='前置地址'; 131076='前置端口'; 131077='前置用户'; 131078='前置密码'; 131079='前置超时';
  131094='前置测试结果'; 196610='住宅列表'; 262146='日志'; 327681='底部状态';
}
foreach ($h in $script:kids) {
  $id = [Win]::GetDlgCtrlID($h)
  if ($names.ContainsKey($id)) {
    $t = [Win]::GetText($h)
    if ($null -eq $t) { $t = '' }
    if ($t.Length -gt 90) { $t = $t.Substring(0, 90) + '…' }
    Write-Output ("  {0,-12} [{1,-14}] {2}" -f $names[$id], [Win]::Cls($h), ($t -replace "`r`n", ' | '))
  }
}

$list = $script:kids | Where-Object { [Win]::GetDlgCtrlID($_) -eq 196610 } | Select-Object -First 1
if ($list) { Write-Output ""; Write-Output "住宅列表行数 = $([Win]::MsgLong($list, 0x1004, [IntPtr]::Zero))" }