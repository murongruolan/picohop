# ---------------------------------------------------------------------------
# ChainProxy GUI 自检脚本
#
#   .\scripts\test_ui.ps1
#
# 做什么：
#   1. 连续跑 N 次 UI 自检（CHAINPROXY_UI_DUMP），确认界面构建**稳定可重复**
#      （Win32 这类自绘图界面最容易出的问题就是"偶尔卡住"，必须重复验证）；
#   2. 断言自检报告里的关键内容：控件数量、前置字段已回填、表格有住宅代理行；
#   3. 打开编辑对话框做一次自检，确认对话框控件齐全；
#   4. 启动真实 GUI，确认窗口可见且响应正常，再干净退出。
# ---------------------------------------------------------------------------
param([int]$Repeat = 3)

$ErrorActionPreference = 'Continue'
. (Join-Path $PSScriptRoot 'env.ps1') | Out-Null

$Root = Split-Path -Parent $PSScriptRoot
$Exe = Join-Path $Root 'dist\chainproxy-console.exe'
$env:CHAINPROXY_DATA_DIR = Join-Path $Root 'data'
$dump = Join-Path $env:CHAINPROXY_DATA_DIR 'ui-dump.txt'
$env:CHAINPROXY_UI_DUMP = $dump

if (-not (Test-Path $Exe)) { throw "找不到 $Exe，请先执行 .\scripts\build.ps1 -Console" }

$fail = 0
function Check($name, $cond, $detail = '') {
    if ($cond) { Write-Host "  [PASS] $name  $detail" -ForegroundColor Green }
    else       { Write-Host "  [FAIL] $name  $detail" -ForegroundColor Red; $script:fail++ }
}

Write-Host "=== ChainProxy GUI 自检（重复 $Repeat 次） ===" -ForegroundColor Cyan

for ($i = 1; $i -le $Repeat; $i++) {
    Remove-Item $dump -ErrorAction SilentlyContinue
    $p = Start-Process -FilePath $Exe -PassThru
    $p.WaitForExit(30000) | Out-Null
    if (-not $p.HasExited) { $p.Kill(); Start-Sleep -Milliseconds 500 }
    if (-not (Test-Path $dump)) {
        Check "第 $i 次 UI 自检产出报告" $false "(进程超时或未写文件)"
        continue
    }
    $s = [System.Text.Encoding]::UTF8.GetString([System.IO.File]::ReadAllBytes($dump))

    $ctrlCount = 0
    if ($s -match '控件总数: (\d+)') { $ctrlCount = [int]$Matches[1] }
    Check "第 $i 次：窗口可见且控件齐全" ($ctrlCount -ge 30) "(控件 $ctrlCount 个)"

    Check "第 $i 次：前置地址已回填" ($s -match '127\.0\.0\.1' -and $s -match '10808') ""
    Check "第 $i 次：住宅代理表格已渲染" ($s -match '住宅代理列表（表格文本）' -and $s -match '10810') ""
    Check "第 $i 次：链路状态已就绪" ($s -match 'running' -or $s -match 'stopped') ""
}

Write-Host "`n[编辑对话框自检]"
Remove-Item $dump -ErrorAction SilentlyContinue
$env:CHAINPROXY_UI_DUMP_DIALOG = '1'
$p = Start-Process -FilePath $Exe -PassThru
$p.WaitForExit(30000) | Out-Null
if (-not $p.HasExited) { $p.Kill(); Start-Sleep -Milliseconds 500 }
if (Test-Path $dump) {
    $s = [System.Text.Encoding]::UTF8.GetString([System.IO.File]::ReadAllBytes($dump))
    Check "对话框自检报告已生成" ($s -match '编辑对话框自检报告') ""
    Check "对话框含名称/类型/本地端口字段" ($s -match '名称' -and $s -match '类型' -and $s -match '本地端口') ""
    Check "对话框含 WireGuard 分组" ($s -match 'WireGuard') ""
} else {
    Check "对话框自检报告已生成" $false ""
}
Remove-Item Env:\CHAINPROXY_UI_DUMP_DIALOG -ErrorAction SilentlyContinue

Write-Host "`n[真实 GUI 启动检查]"
Remove-Item Env:\CHAINPROXY_UI_DUMP -ErrorAction SilentlyContinue
$gui = Start-Process -FilePath (Join-Path $Root 'dist\chainproxy.exe') -PassThru
Start-Sleep -Seconds 5
$proc = Get-Process -Id $gui.Id -ErrorAction SilentlyContinue
if ($proc) {
    Check "GUI 进程存活" $true "(pid=$($proc.Id))"
    Check "GUI 响应正常" $proc.Responding ""
    $listeners = @(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | Where-Object { $_.LocalPort -eq 10810 -or $_.LocalPort -eq 10811 })
    Check "GUI 启动后自动占用端口" ($listeners.Count -ge 1) "($($listeners.Count) 个监听)"

    Write-Host "`n  [经 GUI 占用的端口实际连通测试]"
    $frontIP = (& curl.exe -s --max-time 25 --socks5-hostname 127.0.0.1:10808 https://api.ipify.org)
    $chainIP = (& curl.exe -s --max-time 30 --socks5-hostname 127.0.0.1:10810 https://api.ipify.org)
    Check "经 10810 的出口 IP 与前置不同" ($chainIP -and $chainIP -ne $frontIP) "(前置 $frontIP → 住宅 $chainIP)"

    Stop-Process -Id $gui.Id -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
    Check "GUI 退出后端口释放" (-not (Get-NetTCPConnection -State Listen -LocalPort 10810 -ErrorAction SilentlyContinue)) ""
} else {
    Check "GUI 进程存活" $false ""
}

Write-Host ""
if ($fail -eq 0) { Write-Host "=== GUI 自检全部通过 ===" -ForegroundColor Green }
else { Write-Host "=== 失败 $fail 项 ===" -ForegroundColor Red }
exit $fail