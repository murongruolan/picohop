# 一键验证 WireGuard 出口：启动服务 -> 经本地端口取出口 IP -> 报告 -> 关掉
#
# 用法：
#   .\scripts\test_wg.ps1                    # 用 dist\data\config.json
#   .\scripts\test_wg.ps1 -Config <路径>
#   .\scripts\test_wg.ps1 -TestUrl https://ifconfig.me/ip
#
# 会依次报告：
#   1. 本机直连的出口 IP（对照）
#   2. 经前置代理 前置的出口 IP（对照）
#   3. 经 WireGuard 隧道（本地端口）的出口 IP  ← 这才是"住宅出口在哪"的答案
#   4. SOCKS5 与 HTTP 两种客户端都试一遍（验证 mixed 协议）

param(
    [string]$Config = (Join-Path $PSScriptRoot '..\dist\data\config.json'),
    [string]$TestUrl = 'https://ifconfig.me/ip',
    [int]$WaitSec = 25
)

$ErrorActionPreference = 'Continue'
$root = Resolve-Path (Join-Path $PSScriptRoot '..')
$console = Join-Path $root 'dist\chainproxy-console.exe'
$dataDir = Split-Path $Config -Parent

function FetchIp([string]$proxyArg) {
    $args = @('-s', '--max-time', '25')
    if ($proxyArg) { $args += $proxyArg }
    else { $args += '--noproxy'; $args += '*' }
    $args += $TestUrl
    $out = & curl.exe @args 2>&1
    $v = ($out | Out-String).Trim()
    if (-not $v) { return '(失败)' }
    return ($v -split "`n")[0].Trim()
}

Write-Host "===== WireGuard 出口验证 =====" -ForegroundColor Cyan
Write-Host "配置   : $Config"
Write-Host "测试URL: $TestUrl"
Write-Host ""

# 读配置，拿到本地端口与端点
try {
    $j = Get-Content $Config -Raw | ConvertFrom-Json
} catch {
    Write-Host "配置读取失败：$_" -ForegroundColor Red
    exit 1
}
$wg = $j.upstreams | Where-Object { $_.type -eq 'wireguard' } | Select-Object -First 1
if (-not $wg) { Write-Host "配置里没有 wireguard 类型的链路" -ForegroundColor Red; exit 1 }
$localPort = $wg.listenPort
Write-Host "链路   : $($wg.name)  本地端口 $localPort"
Write-Host "端点   : $($wg.wireguard.endpointHost):$($wg.wireguard.endpointPort)"
Write-Host "隧道IP : $($wg.wireguard.address)   AllowedIPs=$($wg.wireguard.allowedIPs)"
Write-Host ""

if ($wg.wireguard.privateKey -like '*<<<*') {
    Write-Host "❌ 私钥还没填。请编辑上面的配置文件，把 privateKey 换成你的私钥。" -ForegroundColor Red
    Write-Host "   （私钥必须由你自己填，程序不会去猜。它是 44 字符的 base64，以 = 结尾）"
    exit 1
}
if ($wg.wireguard.privateKey -match '\s') {
    Write-Host "⚠️  私钥里似乎有空格/换行，建议去掉。" -ForegroundColor Yellow
}

Write-Host "[1] 本机直连出口        : $(FetchIp '')"
Write-Host "[2] 经前置代理(10808)    : $(FetchIp '--socks5-hostname;127.0.0.1:10808')" -ErrorAction SilentlyContinue
Write-Host ""

Write-Host "[3] 启动 ChainProxy ..." -ForegroundColor Cyan
Get-Process chainproxy-console -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 500

$logFile = Join-Path $env:TEMP ("chainproxy-wgtest-{0}.log" -f (Get-Random))
$env:CHAINPROXY_DATA_DIR = $dataDir
$proc = Start-Process -FilePath $console -ArgumentList @('run', '-config', $Config) `
    -PassThru -RedirectStandardOutput $logFile -RedirectStandardError ($logFile + '.err')

# 等端口监听 + 隧道建立
$deadline = (Get-Date).AddSeconds($WaitSec)
$ready = $false
while ((Get-Date) -lt $deadline) {
    Start-Sleep -Milliseconds 500
    if ($proc.HasExited) { break }
    $listen = Get-NetTCPConnection -LocalPort $localPort -State Listen -ErrorAction SilentlyContinue
    if ($listen) { $ready = $true; break }
}

if ($proc.HasExited) {
    Write-Host "❌ 进程已退出，日志：" -ForegroundColor Red
    Get-Content $logFile -ErrorAction SilentlyContinue | Select-Object -Last 12
    Get-Content ($logFile + '.err') -ErrorAction SilentlyContinue | Select-Object -Last 6
    exit 1
}
if (-not $ready) { Write-Host "⚠️  端口 $localPort 还没监听，继续尝试……" -ForegroundColor Yellow }
else { Write-Host "    端口 $localPort 已监听" }

# 隧道建立还需要一点时间（握手 + keepalive）
Start-Sleep -Seconds 3

Write-Host ""
Write-Host "===== 结果 =====" -ForegroundColor Cyan
$viaSocks = FetchIp "--socks5-hostname;127.0.0.1:$localPort"
Write-Host ("  经隧道（SOCKS5 127.0.0.1:{0}） : {1}" -f $localPort, $viaSocks) -ForegroundColor Green
$viaHttp = FetchIp "-x;http://127.0.0.1:$localPort"
Write-Host ("  经隧道（HTTP   127.0.0.1:{0}） : {1}" -f $localPort, $viaHttp) -ForegroundColor Green

Write-Host ""
Write-Host "===== 隧道状态（来自日志）=====" -ForegroundColor Cyan
$log = Get-Content $logFile -ErrorAction SilentlyContinue
$log | Select-String -Pattern '隧道已建立|last_handshake_time_sec|tx_bytes|rx_bytes|allowed_ip|preshared_key|握手超时|失败' |
    Select-Object -First 16 | ForEach-Object { Write-Host ("  " + $_.Line.Trim()) }

Write-Host ""
Write-Host "===== 清理 =====" -ForegroundColor Cyan
Get-Process -Id $proc.Id -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 800
$left = (Get-NetTCPConnection -LocalPort $localPort -State Listen -ErrorAction SilentlyContinue).Count
Write-Host "  端口已释放: $($left -eq 0)"
Write-Host "  日志: $logFile"
