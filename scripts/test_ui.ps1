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

    # 布局体检：控件必须全部落在客户区内，文字必须全部装得下控件。
    # 这两条是"界面错位 / 按钮文字超出"的直接防线。
    $overflowCtrl = 0
    if ($s -match '超出客户区的控件：(\d+) 个') { $overflowCtrl = [int]$Matches[1] }
    Check "第 $i 次：没有控件越出窗口" ($overflowCtrl -eq 0) "(越界 $overflowCtrl 个)"

    $overflowText = 0
    if ($s -match '文字宽度超过控件宽度：(\d+) 个') { $overflowText = [int]$Matches[1] }
    Check "第 $i 次：没有文字被控件截断" ($overflowText -eq 0) "(截断 $overflowText 个)"

    # 重叠检测：控件不能互相压住（例如状态文字被按钮盖住）
    $overlap = 0
    if ($s -match '互相重叠的控件：(\d+) 对') { $overlap = [int]$Matches[1] }
    Check "第 $i 次：没有控件互相重叠" ($overlap -eq 0) "(重叠 $overlap 对)"

    # 点击模拟：按钮必须真的产生可观测效果（防止"点了没反应"复发）
    $clickFail = ([regex]::Matches($s, '\[FAIL\]|❌')).Count
    $clickWarn = ([regex]::Matches($s, '⚠️')).Count
    Check "第 $i 次：所有按钮点击都有响应" ($clickFail -eq 0 -and $clickWarn -eq 0) "(❌ $clickFail / ⚠️ $clickWarn)"
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
    Check "对话框含预共享密钥字段" ($s -match '预共享密钥') ""

    # 下拉框必须真的返回"当前选中项"。
    # 曾经 CB_GETLBTEXT 传死下标 0，导致无论选什么都读回第一项，
    # 界面会把 http / wireguard 全部静默存成 socks5。
    Check "下拉框能正确读回 wireguard 选项" ($s -match '类型="wireguard"') ""

    # 两种形态都要过布局体检（只测 socks5 的话 WireGuard 那组控件是隐藏的，等于没测）
    $dReports = ([regex]::Matches($s, '编辑对话框自检报告')).Count
    Check "socks5 与 wireguard 两种对话框都做了自检" ($dReports -ge 2) "(共 $dReports 份)"

    # 对话框按钮必须真的有效。
    # 这里刻意在**第二次**打开的对话框上点按钮：
    # Win32 窗口类每进程只能注册一次，早先每次打开都重新注册，
    # 导致第二次起所有按钮消息被派发到已销毁的对象上（点了没反应）。
    $genOK = $s -match '✅ 「生成密钥」'
    $pubOK = $s -match '✅ 「本端公钥」与私钥一致'
    $copyOK = $s -match '✅ 「复制公钥」'
    Check "对话框「生成密钥」按钮有效" $genOK ""
    Check "对话框「本端公钥」随私钥正确更新" $pubOK ""
    Check "对话框「复制公钥」写入剪贴板" $copyOK ""

    # 类型切换：用户在下拉框里换类型时，下面那组输入框必须跟着变。
    # 这条路径以前从没被测到（自检一直是直接摆好类型），
    # 所以"切换后界面不变"这个真实 bug 溜了过去。
    $swWg = $s -match '✅ 切到 wireguard'
    $swSocks = $s -match '✅ 切回 socks5'
    Check "对话框切换类型时控件跟随显隐（→ wireguard）" $swWg ""
    Check "对话框切换类型时控件跟随显隐（→ socks5）" $swSocks ""

    # 对话框同样要过布局体检
    $dCtrl = 0
    if ($s -match '对话框[\s\S]*?超出客户区的控件：(\d+) 个') { $dCtrl = [int]$Matches[1] }
    Check "对话框没有控件越出窗口" ($dCtrl -eq 0) "(越界 $dCtrl 个)"
    $dText = 0
    if ($s -match '对话框[\s\S]*?文字宽度超过控件宽度：(\d+) 个') { $dText = [int]$Matches[1] }
    Check "对话框没有文字被截断" ($dText -eq 0) "(截断 $dText 个)"
    $dOverlap = 0
    if ($s -match '对话框[\s\S]*?互相重叠的控件：(\d+) 对') { $dOverlap = [int]$Matches[1] }
    Check "对话框没有控件互相重叠" ($dOverlap -eq 0) "(重叠 $dOverlap 对)"
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