# ---------------------------------------------------------------------------
# ChainProxy GUI 自检脚本
#
#   .\scripts\inspect_window.ps1 [进程名]
#
# 枚举目标进程的所有顶层窗口与子控件，打印 类名 / ID / 坐标 / 文本，
# 用来在没有截图的情况下验证 Win32 界面确实被正确创建与布局。
# ---------------------------------------------------------------------------
param([string]$ProcName = 'chainproxy')

$ErrorActionPreference = 'Stop'

Add-Type @"
using System;
using System.Text;
using System.Collections.Generic;
using System.Runtime.InteropServices;

public class W32 {
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr l);
    [DllImport("user32.dll")] public static extern bool EnumChildWindows(IntPtr h, EnumProc cb, IntPtr l);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowTextW(IntPtr h, StringBuilder s, int n);
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassNameW(IntPtr h, StringBuilder s, int n);
    [DllImport("user32.dll")] public static extern int GetDlgCtrlID(IntPtr h);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
    [DllImport("user32.dll")] public static extern bool IsWindowEnabled(IntPtr h);

    public delegate bool EnumProc(IntPtr h, IntPtr l);
    [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }

    public static string Text(IntPtr h) { var sb = new StringBuilder(512); GetWindowTextW(h, sb, 512); return sb.ToString(); }
    public static string Cls(IntPtr h)  { var sb = new StringBuilder(256); GetClassNameW(h, sb, 256); return sb.ToString(); }

    public static List<IntPtr> TopLevel(uint pid) {
        var list = new List<IntPtr>();
        EnumWindows((h, l) => { uint p; GetWindowThreadProcessId(h, out p); if (p == pid) list.Add(h); return true; }, IntPtr.Zero);
        return list;
    }
    public static List<IntPtr> Children(IntPtr parent) {
        var list = new List<IntPtr>();
        EnumChildWindows(parent, (h, l) => { list.Add(h); return true; }, IntPtr.Zero);
        return list;
    }
}
"@

$pids = @(Get-Process -Name $ProcName -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id)
if (-not $pids -or $pids.Count -eq 0) { Write-Output "没有找到进程 $ProcName"; exit 1 }

foreach ($pid_ in $pids) {
    Write-Output "=============================================="
    Write-Output "PID $pid_  ($ProcName)"
    $tops = [W32]::TopLevel([uint32]$pid_)
    if ($tops.Count -eq 0) { Write-Output "  没有顶层窗口（窗口未创建？）"; continue }
    foreach ($top in $tops) {
        $r = New-Object W32+RECT
        [void][W32]::GetWindowRect($top, [ref]$r)
        Write-Output "  顶层窗口: '$([W32]::Text($top))' class=$([W32]::Cls($top)) rect=($($r.L),$($r.T))-($($r.R),$($r.B)) visible=$([W32]::IsWindowVisible($top))"
        $kids = [W32]::Children($top)
        Write-Output "  子控件数: $($kids.Count)"
        foreach ($k in $kids) {
            [void][W32]::GetWindowRect($k, [ref]$r)
            $txt = [W32]::Text($k)
            if ($txt.Length -gt 46) { $txt = $txt.Substring(0,46) + '…' }
            $vis = if ([W32]::IsWindowVisible($k)) { 'V' } else { '-' }
            $en  = if ([W32]::IsWindowEnabled($k))  { 'E' } else { '-' }
            Write-Output ("    [{0}{1}] {2,-16} id={3,-7} ({4,4},{5,4}) {6,4}x{7,-4} '{8}'" -f $vis,$en,[W32]::Cls($k),[W32]::GetDlgCtrlID($k),$r.L,$r.T,($r.R-$r.L),($r.B-$r.T),$txt)
        }
    }
}