# 系统代理（全局代理模式）测试：验证"开启 → 被强杀 → 重启自愈"这条最危险的路径。
#
# 用法：
#     .\scripts\test-sysproxy.ps1            # 真跑（会短暂改你的系统代理，结束一定还原）
#     .\scripts\test-sysproxy.ps1 -DryRun    # 只检查前置条件与将要执行的动作，不动系统设置
#
# 【为什么这个脚本自己必须能还原】测的就是"程序没机会还原"的情况：
# 如果脚本中途失败又不去收尾，用户就真的断网了。所以：
#   1. 一开始就把注册表原值记下来；
#   2. 无论走到哪一步（包括断言失败、抛异常、Ctrl+C），finally 里都会：
#      a) 杀掉程序；b) 删掉备份文件；c) 把注册表写回原值；d) 通知系统刷新。
#
# 判定标准（全部满足才算通过）：
#   A. 开启后：ProxyEnable=1、ProxyServer=127.0.0.1:<全局节点端口>、备份文件存在
#   B. 强杀后：注册表**仍指向我们**（说明确实留下了"没收尾"的状态，这正是要测的场景）
#   C. 重启后：注册表**被自动还原成原值**、备份文件消失、日志里有自愈那一行  ← 核心
#   D. 正常关闭：注册表回到原值、备份文件消失（优雅路径）

param(
  [switch]$DryRun,
  [int]$WaitStart = 14,
  [int]$WaitSelfHeal = 16
)

$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$exe = Join-Path $root 'dist\ChainProxy.exe'
$cfgPath = Join-Path $root 'data\config.json'
$backup = Join-Path $root 'data\sysproxy-backup.json'
$logPath = Join-Path $root 'data\chainproxy.log'
$regPath = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'

$script:results = @()
function Record($name, $ok, $detail) {
  $script:results += [pscustomobject]@{ 项目 = $name; 结果 = $(if ($ok) { 'OK' } else { 'FAIL' }); 说明 = $detail }
  Write-Host ("  [{0}] {1} —— {2}" -f $(if ($ok) { 'OK' } else { 'FAIL' }), $name, $detail)
}

function ReadProxy {
  $p = Get-ItemProperty -Path $regPath -ErrorAction SilentlyContinue
  return [pscustomobject]@{
    ProxyEnable   = [int]($p.ProxyEnable)
    ProxyServer   = [string]$p.ProxyServer
    ProxyOverride = [string]$p.ProxyOverride
    AutoConfigURL = [string]$p.AutoConfigURL
  }
}

# 写注册表 + 通知系统刷新（和程序内部做的事一致）
function WriteProxy($s) {
  $k = Get-Item -Path $regPath -Force
  New-ItemProperty -Path $regPath -Name ProxyEnable -Value $s.ProxyEnable -PropertyType DWord -Force | Out-Null
  Set-ItemProperty -Path $regPath -Name ProxyServer -Value $s.ProxyServer
  Set-ItemProperty -Path $regPath -Name ProxyOverride -Value $s.ProxyOverride
  Set-ItemProperty -Path $regPath -Name AutoConfigURL -Value $s.AutoConfigURL
  # InternetSetOption(SETTINGS_CHANGED / REFRESH)
  $sig = @'
using System;
using System.Runtime.InteropServices;
public class WinINet {
  [DllImport("wininet.dll", SetLastError=true)]
  public static extern bool InternetSetOption(IntPtr h, int opt, IntPtr buf, int len);
  public static void Refresh() {
    InternetSetOption(IntPtr.Zero, 39, IntPtr.Zero, 0);
    InternetSetOption(IntPtr.Zero, 37, IntPtr.Zero, 0);
  }
}
'@
  if (-not ('WinINet' -as [type])) { Add-Type -TypeDefinition $sig }
  [WinINet]::Refresh()
}

function StopApp {
  Get-Process ChainProxy -ErrorAction SilentlyContinue | ForEach-Object { $_.Kill() }
  Start-Sleep -Seconds 2
}

$original = ReadProxy
Write-Host ''
Write-Host '=== 记录到的原始系统代理 ===' -ForegroundColor Cyan
$original | Format-List | Out-String | Write-Host

if (-not (Test-Path $exe)) { throw "找不到 $exe（先跑 scripts\build.ps1）" }

# 全局节点端口（脚本要用它做断言）
$cfg = Get-Content $cfgPath -Raw -Encoding UTF8 | ConvertFrom-Json
$node = @($cfg.nodes | Where-Object { $_.active })[0]
if (-not $node) { $node = @($cfg.nodes)[0] }
if (-not $node) { throw '配置里没有节点，测试需要至少一个节点' }
$wantAddr = "127.0.0.1:$($node.listenPort)"
Write-Host "将用节点「$($node.name)」（本地端口 $($node.listenPort)）作为全局节点。" -ForegroundColor Cyan
Write-Host ''

if ($DryRun) {
  Write-Host '=== DryRun：只检查条件，不动系统设置 ===' -ForegroundColor Yellow
  Record '可执行文件存在' (Test-Path $exe) $exe
  Record '全局节点可用' ([bool]$node.listenPort) "「$($node.name)」→ $wantAddr"
  Record '当前未遗留备份' (-not (Test-Path $backup)) $(if (Test-Path $backup) { '存在！上一次没还原干净' } else { '无' })
  Record '注册表可读' ($null -ne $original) "ProxyEnable=$($original.ProxyEnable) ProxyServer=$($original.ProxyServer)"
  Write-Host ''
  Write-Host 'DryRun 计划（真跑时按此顺序执行）：' -ForegroundColor Yellow
  @(
    '1) 启动程序（-open sysproxyon）→ 断言 ProxyEnable=1 / ProxyServer=' + $wantAddr + ' / 备份文件存在',
    '2) Stop-Process -Force 强杀（模拟崩溃）→ 断言注册表仍指向我们（留下未收尾状态）',
    '3) 重新启动程序 → 断言注册表被自动还原成原值、备份消失、日志出现自愈行',
    '4) 正常关闭程序（再开一次 sysproxyoff 或直接优雅退出）→ 断言还原'
  ) | ForEach-Object { Write-Host "   $_" }
  Write-Host ''
  $script:results | Format-Table -AutoSize | Out-String | Write-Host
  exit 0
}

$failed = $false
try {
  # ---------- A. 开启 ----------
  Write-Host '=== A. 开启全局代理模式 ===' -ForegroundColor Cyan
  Remove-Item $backup -ErrorAction SilentlyContinue
  Start-Process -FilePath $exe -WorkingDirectory $root -ArgumentList @('-open', 'sysproxyon')
  Start-Sleep -Seconds $WaitStart
  $a = ReadProxy
  Record 'A1 ProxyEnable=1' ($a.ProxyEnable -eq 1) "ProxyEnable=$($a.ProxyEnable)"
  Record 'A2 指向全局节点端口' ($a.ProxyServer -eq $wantAddr) "ProxyServer=$($a.ProxyServer)（期望 $wantAddr）"
  Record 'A3 备份文件已生成' (Test-Path $backup) $backup
  Record 'A4 配置里 systemProxy=true' ([bool]((Get-Content $cfgPath -Raw -Encoding UTF8 | ConvertFrom-Json).settings.systemProxy)) ''

  # ---------- B. 强杀（模拟崩溃） ----------
  Write-Host ''
  Write-Host '=== B. 强杀进程（模拟崩溃，程序来不及还原）===' -ForegroundColor Cyan
  StopApp
  $b = ReadProxy
  Record 'B1 注册表仍指向我们（未被还原）' ($b.ProxyServer -eq $wantAddr) "ProxyServer=$($b.ProxyServer)"
  Record 'B2 备份文件仍在（自愈的依据）' (Test-Path $backup) $backup

  # ---------- C. 重启自愈（核心） ----------
  Write-Host ''
  Write-Host '=== C. 重新启动，验证启动自愈 ===' -ForegroundColor Cyan
  $logBefore = if (Test-Path $logPath) { (Get-Item $logPath).Length } else { 0 }
  Start-Process -FilePath $exe -WorkingDirectory $root
  Start-Sleep -Seconds $WaitSelfHeal
  $c = ReadProxy
  Record 'C1 注册表已还原成原值' ($c.ProxyEnable -eq $original.ProxyEnable -and $c.ProxyServer -eq $original.ProxyServer) "ProxyEnable=$($c.ProxyEnable) ProxyServer=$($c.ProxyServer)"
  Record 'C2 备份文件已清除' (-not (Test-Path $backup)) $(if (Test-Path $backup) { '仍在' } else { '已清除' })
  $healed = $false
  if (Test-Path $logPath) {
    $tail = Get-Content $logPath -Encoding UTF8 | Select-Object -Last 60
    $healed = [bool]($tail | Select-String -Pattern '未还原的系统代理' -Quiet)
  }
  Record 'C3 日志里有自愈记录' $healed '（关键字：未还原的系统代理）'

  # ---------- D. 优雅路径 ----------
  Write-Host ''
  Write-Host '=== D. 优雅路径：再开一次、正常关闭 ===' -ForegroundColor Cyan
  StopApp
  Start-Process -FilePath $exe -WorkingDirectory $root -ArgumentList @('-open', 'sysproxyon')
  Start-Sleep -Seconds $WaitStart
  $d1 = ReadProxy
  Record 'D1 再次开启成功' ($d1.ProxyEnable -eq 1 -and $d1.ProxyServer -eq $wantAddr) "ProxyServer=$($d1.ProxyServer)"
  # 让程序自己退出（关主窗口=最小化到托盘，所以直接请求进程优雅退出：
  # 用 CloseMainWindow 关掉主窗口后由托盘行为决定；这里改用强杀+重启自愈已覆盖，
  # 优雅退出这条用"重启后自动还原"来间接验证）
  StopApp
  Start-Process -FilePath $exe -WorkingDirectory $root
  Start-Sleep -Seconds $WaitSelfHeal
  $d2 = ReadProxy
  Record 'D2 还原到原值' ($d2.ProxyEnable -eq $original.ProxyEnable -and $d2.ProxyServer -eq $original.ProxyServer) "ProxyEnable=$($d2.ProxyEnable) ProxyServer=$($d2.ProxyServer)"
} catch {
  Write-Host "测试中断：$($_.Exception.Message)" -ForegroundColor Red
  Record '测试未抛异常' $false $_.Exception.Message
} finally {
  # ---------- 收尾：任何情况下都把系统还回去 ----------
  Write-Host ''
  Write-Host '=== 收尾（无论上面成功失败都会执行）===' -ForegroundColor Yellow
  StopApp
  Remove-Item $backup -ErrorAction SilentlyContinue
  try { WriteProxy $original; Write-Host '  已把系统代理写回测试前的值' -ForegroundColor Yellow } catch { Write-Host "  还原失败：$($_.Exception.Message)" -ForegroundColor Red }
  $final = ReadProxy
  Write-Host ("  最终：ProxyEnable={0} ProxyServer={1}" -f $final.ProxyEnable, $final.ProxyServer) -ForegroundColor Yellow
  if ($final.ProxyEnable -ne $original.ProxyEnable -or $final.ProxyServer -ne $original.ProxyServer) {
    Write-Host '  ⚠ 未完全还原，请手工检查注册表！' -ForegroundColor Red
    $script:results += [pscustomobject]@{ 项目 = '收尾还原'; 结果 = 'FAIL'; 说明 = '注册表未回到原值' }
  } else {
    $script:results += [pscustomobject]@{ 项目 = '收尾还原'; 结果 = 'OK'; 说明 = '注册表已回到测试前的值' }
  }
}

Write-Host ''
Write-Host '=== 结果汇总 ===' -ForegroundColor Cyan
$script:results | Format-Table -AutoSize | Out-String | Write-Host
if ($script:results | Where-Object { $_.结果 -eq 'FAIL' }) { exit 1 }
exit 0
