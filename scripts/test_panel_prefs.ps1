# ---------------------------------------------------------------------------
# 底部栏偏好（展开 / 页签 / 高度）跨重启持久化 —— 端到端验证
#
#   .\scripts\test_panel_prefs.ps1
#   .\scripts\test_panel_prefs.ps1 -Shot        # 顺便截一张"重启后应该长这样"的图
#
# 【这个脚本验的是什么】用户实报："展开之后关掉程序，下次进来却是收起的，
# 也不在我上次看的那个页签。" 也就是界面布局偏好没被记住。
#
# 它**不读代码**，只做三件事：写一份带非默认偏好的配置 → 真的启动程序 → 读回来比对：
#
#   1) 启动后不被"默认值反写"：设置是异步加载的，若界面在加载完之前就写回，
#      用户的偏好会被一份"只有面板三项、其余全是零值"的设置覆盖掉；
#   2) 关掉再开仍然记得：跨重启（含被强杀，程序来不及做任何抢救）；
#   3) 自测入口（-open <面板名>）不擦用户状态：这类入口为了截图会切底部栏视图，
#      以前它会把偏好写进配置 —— 实测 -open globaltag 能把 bottomPanelOpen 写成 false。
#
# 全程只在 CHAINPROXY_DATA_DIR 指向的**临时副本**上跑；跑完核对用户真实
# data\config.json 的 sha256 没变（纪律见 docs/VERIFY_DISCIPLINE.md）。
#
# 【为什么用文件内容做断言】偏好本来就落在 config.json 里，它是"程序认为用户
# 上次是什么状态"的唯一权威来源 —— 断言它比截图更硬。想看"界面真的照它渲染了"
# 就加 -Shot，截出来的图应当：面板是展开的、高亮在「网站测试」上。
# ---------------------------------------------------------------------------
param(
    [string]$Exe = "",           # 默认为 dist\ChainProxy.exe
    [int]$WaitSeconds = 10,      # 每次启动等多久再看（WebView2 冷启动偏慢）
    [switch]$Shot,               # 顺便截图
    [switch]$Force               # 允许杀掉已经在跑的 ChainProxy
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1') | Out-Null

$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root
if (-not $Exe) { $Exe = Join-Path $Root 'dist\ChainProxy.exe' }

# 断言用的那组偏好：**刻意都不是默认值**（默认是 收起 / logs / 260），
# 否则"存住了"和"被默认值顶掉了"看起来一模一样。
$wantOpen   = $true
$wantView   = 'sites'
$wantHeight = 333

$script:failed = 0
function Ok($m)   { Write-Host "  [通过] $m" -ForegroundColor Green }
function Fail($m) { Write-Host "  [失败] $m" -ForegroundColor Red; $script:failed++ }

function ReadPanelPrefs([string]$cfgPath) {
    $c = Get-Content $cfgPath -Raw -Encoding UTF8 | ConvertFrom-Json
    return [pscustomobject]@{
        Open   = [bool]$c.settings.bottomPanelOpen
        View   = [string]$c.settings.bottomPanelView
        Height = [int]$c.settings.bottomPanelHeight
    }
}

function AssertPanelPrefs([string]$stage, [string]$cfgPath) {
    $p = ReadPanelPrefs $cfgPath
    $got = "open=$($p.Open) view='$($p.View)' height=$($p.Height)"
    $expect = "open=$wantOpen view='$wantView' height=$wantHeight"
    if ($p.Open -eq $wantOpen -and $p.View -eq $wantView -and $p.Height -eq $wantHeight) {
        Ok "$stage：$got（与写入的一致）"
    } else {
        Fail "$stage：$got —— 期望 $expect"
    }
}

# ---------------------------------------------------------------------------
# 前置检查
# ---------------------------------------------------------------------------
if (-not (Test-Path $Exe)) {
    Write-Host "找不到 $Exe（先跑 scripts\build.ps1，或用 -Exe 指定）" -ForegroundColor Red
    exit 1
}

$running = @(Get-Process ChainProxy -ErrorAction SilentlyContinue)
if ($running.Count -and -not $Force) {
    Write-Host "已经有 ChainProxy 在跑（pid: $($running.Id -join ', ')）。" -ForegroundColor Yellow
    Write-Host "本脚本要独占一个实例；请先从托盘退出它，或加 -Force 让脚本杀掉它。" -ForegroundColor Yellow
    exit 1
}
if ($running.Count) { $running | ForEach-Object { $_.Kill() | Out-Null }; Start-Sleep -Seconds 2 }

# 用户的真实配置：全程只读，最后核对哈希
$realCfg = Join-Path $Root 'data\config.json'
$realHashBefore = if (Test-Path $realCfg) { (Get-FileHash $realCfg -Algorithm SHA256).Hash } else { '' }

# 隔离目录（gitignore 掉的 .cache 内）。webview\ 会一起建在这里，
# 不删是为了复用 WebView2 的 profile，跑得快些。
$dataDir = Join-Path $Root '.cache\panel-prefs-e2e\data'
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
$cfgPath = Join-Path $dataDir 'config.json'

# 造一份配置：没有任何真实节点/凭据（只有一个指向回环的占位节点，不联网），
# 全局快捷键设为 none，免得测试期间抢用户的热键。
$cfg = [ordered]@{
    version = 2
    front   = [ordered]@{ type = 'direct'; host = ''; port = 0; timeoutMs = 10000 }
    settings = [ordered]@{
        defaultListenHost      = '127.0.0.1'
        allowLan               = $false
        portStart              = 10810
        idleTimeoutSec         = 300
        autoActivateOnLaunch   = $false
        tcpTestTimeoutMs       = 5000
        testConcurrency        = 8
        realTestUrl            = 'http://cp.cloudflare.com/generate_204'
        speedTestUrl           = 'https://speed.cloudflare.com/__down?bytes=104857600'
        speedTestSeconds       = 8
        theme                  = 'system'
        notificationDurationMs = 4000
        logLines               = 500
        bottomPanelView        = $wantView
        bottomPanelHeight      = $wantHeight
        bottomPanelOpen        = $wantOpen
        shortcutToggleWindow   = 'none'
        shortcutToggleAll      = 'none'
    }
    nodes = @(
        [ordered]@{
            id = 'e2e0000000000000000000000000001'; name = '自测占位节点'; protocol = 'socks5'
            listenHost = '127.0.0.1'; listenPort = 10899; front = ''
            server = '127.0.0.1'; port = 18080; active = $false
            createdAt = '2026-01-01T00:00:00Z'
        }
    )
}

function Start-App([string]$DevOpen) {
    $env:CHAINPROXY_DATA_DIR = $dataDir
    $args = @()
    if ($DevOpen) { $args = @('-open', $DevOpen) }
    Start-Process -FilePath $Exe -WorkingDirectory $Root -ArgumentList $args | Out-Null
    Start-Sleep -Seconds $WaitSeconds
}

function Stop-App {
    Get-Process ChainProxy -ErrorAction SilentlyContinue | ForEach-Object { $_.Kill() | Out-Null }
    Start-Sleep -Seconds 2
}

# ---------------------------------------------------------------------------
Write-Host ''
Write-Host '========================================' -ForegroundColor Cyan
Write-Host '底部栏偏好跨重启持久化 · 端到端验证'
Write-Host "程序：$Exe"
Write-Host "数据目录（隔离）：$dataDir"
Write-Host '========================================' -ForegroundColor Cyan

$prevDataDir = $env:CHAINPROXY_DATA_DIR
try {
    # ---------- 1. 启动后不被默认值反写 ----------
    Write-Host ''
    Write-Host '[1/4] 启动后：写进去的偏好还在吗（不该被默认值反写）'
    $cfg | ConvertTo-Json -Depth 8 | Set-Content $cfgPath -Encoding UTF8
    AssertPanelPrefs '写入后（启动前）' $cfgPath
    Start-App ''
    AssertPanelPrefs '启动后' $cfgPath

    # ---------- 2. 关掉再开 ----------
    Write-Host ''
    Write-Host '[2/4] 强杀（模拟关掉程序）→ 重新启动：还是上次的状态吗'
    Stop-App
    AssertPanelPrefs '关掉之后' $cfgPath
    Start-App ''
    AssertPanelPrefs '重启之后' $cfgPath

    if ($Shot) {
        Write-Host '        截图（应当：面板展开、高亮在「网站测试」）…'
        # 变量别叫 $shot：PowerShell 变量名大小写不敏感，会撞上开关参数 $Shot
        # （撞上就会报"无法把 String 转成 SwitchParameter"，实测踩过）。
        $shotPath = Join-Path $Root '.cache\shots\panel-prefs-after-restart.png'
        & go run ./cmd/uishot -title ChainProxy -out $shotPath -wait 15 | Out-Host
        if ($LASTEXITCODE -ne 0) { Fail '截图失败' } else { Ok "截图：$shotPath" }
    }

    # ---------- 3. 自测入口不擦用户状态 ----------
    Write-Host ''
    Write-Host '[3/4] 用自测入口 -open bottompanel 再启动一次：它不该改写偏好'
    Stop-App
    # 这一条是**反向可验证**的：修之前，-open bottompanel 会把 bottomPanelView
    # 从 sites 改成 logs（它为了截图会真的去切视图，而切视图以前会写配置）。
    Start-App 'bottompanel'
    AssertPanelPrefs '自测入口跑过之后' $cfgPath
} finally {
    Stop-App
    if ($null -eq $prevDataDir) { Remove-Item Env:\CHAINPROXY_DATA_DIR -ErrorAction SilentlyContinue }
    else { $env:CHAINPROXY_DATA_DIR = $prevDataDir }

    # ---------- 4. 用户的真实配置一根汗毛都没动 ----------
    Write-Host ''
    Write-Host '[4/4] 用户的真实配置有没有被动过'
    $realHashAfter = if (Test-Path $realCfg) { (Get-FileHash $realCfg -Algorithm SHA256).Hash } else { '' }
    if (Test-Path $realCfg) {
        if ($realHashBefore -eq $realHashAfter) {
            Ok "data\config.json 未改动（sha256 $($realHashAfter.Substring(0,12))…）"
        } else {
            Fail "data\config.json 被改动了！`n         跑之前：$realHashBefore`n         跑之后：$realHashAfter"
        }
    } else {
        Write-Host '  （仓库里没有 data\config.json，跳过）' -ForegroundColor Yellow
    }
}

Write-Host ''
if ($script:failed -gt 0) {
    Write-Host "底部栏偏好验证：$($script:failed) 项失败" -ForegroundColor Red
    exit 1
}
Write-Host '底部栏偏好验证：全部通过' -ForegroundColor Green
exit 0
