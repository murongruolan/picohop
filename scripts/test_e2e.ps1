# ---------------------------------------------------------------------------
# ChainProxy 端到端测试脚本
#
#   .\scripts\test_e2e.ps1
#
# 流程：
#   1. 记录前置代理（主流客户端10808）的出口 IP 作为基准
#   2. 启动 chainproxy run（占用 10810 / 10811）
#   3. 用 curl 分别以 SOCKS5 / HTTP / 明文 HTTP 走本地端口访问公网 IP 查询服务
#   4. 断言：本地端口的出口 IP 必须与前置出口 IP 不同（这正是链式代理的意义）
#   5. 停止进程后确认端口已释放；重启后可立即重新绑定
#
# 注意：住宅 IP 是动态的，单次请求偶尔会失败，因此每个出口 IP 查询都重试若干次。
# 重试次数用完仍然失败才算真失败。
# ---------------------------------------------------------------------------
param(
    [int]$Port1 = 10810,
    [int]$Port2 = 10811,
    [int]$Retries = 3
)

$ErrorActionPreference = 'Continue'
. (Join-Path $PSScriptRoot 'env.ps1') | Out-Null

$Root = Split-Path -Parent $PSScriptRoot
$Exe  = Join-Path $Root 'dist\chainproxy-console.exe'
$env:CHAINPROXY_DATA_DIR = Join-Path $Root 'data'

if (-not (Test-Path $Exe)) { throw "找不到 $Exe，请先执行 .\scripts\build.ps1 -Console" }

function Wait-PortOpen([int]$Port, [int]$TimeoutSec = 15) {
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        if (Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue) { return $true }
        Start-Sleep -Milliseconds 300
    }
    return $false
}

function PortBusy([int]$Port) {
    return [bool](Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue)
}

# 住宅 IP 是动态的，单次请求偶尔失败；重试若干次后再判定。
function Get-ExitIP([string[]]$CurlArgs, [int]$Tries = 3) {
    $last = ''
    for ($i = 1; $i -le $Tries; $i++) {
        $r = (& curl.exe -s @CurlArgs)
        $last = "$r"
        if ($r -and ($last -match '^\d{1,3}(\.\d{1,3}){3}$')) { return $last }
        Start-Sleep -Seconds 1
    }
    return $last
}

$script:fail = 0
function Check($name, $cond, $detail) {
    if ($cond) { Write-Host "  [PASS] $name  $detail" -ForegroundColor Green }
    else       { Write-Host "  [FAIL] $name  $detail" -ForegroundColor Red; $script:fail++ }
}

function Start-Service {
    return Start-Process -FilePath $Exe -ArgumentList 'run' -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $Root 'data\e2e.out') `
        -RedirectStandardError  (Join-Path $Root 'data\e2e.err')
}

Write-Host "=== ChainProxy 端到端测试 ===" -ForegroundColor Cyan

Write-Host "`n[0] 前置代理基准出口 IP"
$frontIP = (& curl.exe -s --max-time 25 --socks5-hostname 127.0.0.1:10808 https://api.ipify.org)
Write-Host "    前置出口 IP = $frontIP"

Write-Host "`n[1] 启动服务"
$proc = Start-Service
Start-Sleep -Seconds 2
Check "端口 $Port1 已占用" (Wait-PortOpen $Port1) ""
Check "端口 $Port2 已占用" (Wait-PortOpen $Port2) ""

Write-Host "`n[2] SOCKS5 客户端经本地端口"
$s1 = Get-ExitIP @('-s', '--max-time', '30', '--socks5-hostname', "127.0.0.1:$Port1", 'https://api.ipify.org') $Retries
Write-Host "    socks5://127.0.0.1:$Port1 -> $s1"
Check "SOCKS5 链路出口 IP 不同于前置" ($s1 -and $s1 -ne $frontIP) "($frontIP -> $s1)"

Write-Host "`n[3] HTTP 代理客户端经本地端口（mixed 协议）"
$s2 = Get-ExitIP @('-s', '--max-time', '30', '-x', "http://127.0.0.1:$Port1", 'https://api.ipify.org') $Retries
Write-Host "    http://127.0.0.1:$Port1 -> $s2"
Check "HTTP 链路出口 IP 不同于前置" ($s2 -and $s2 -ne $frontIP) "($frontIP -> $s2)"

Write-Host "`n[4] 第二条链路并存"
$s3 = Get-ExitIP @('-s', '--max-time', '30', '--socks5-hostname', "127.0.0.1:$Port2", 'https://api.ipify.org') $Retries
Write-Host "    socks5://127.0.0.1:$Port2 -> $s3"
Check "第二条链路出口 IP 不同于前置" ($s3 -and $s3 -ne $frontIP) "($frontIP -> $s3)"

Write-Host "`n[5] 明文 HTTP 代理请求（非 CONNECT）"
$s4 = Get-ExitIP @('-s', '--max-time', '30', '-x', "http://127.0.0.1:$Port1", 'http://api.ipify.org') $Retries
Write-Host "    http://127.0.0.1:$Port1 (明文 http) -> $s4"
Check "明文 HTTP 请求经链路可达" ($s4 -match '^\d{1,3}(\.\d{1,3}){3}$') "($s4)"

Write-Host "`n[6] 停止服务并确认端口释放"
Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2
Check "端口 $Port1 已释放" (-not (PortBusy $Port1)) ""
Check "端口 $Port2 已释放" (-not (PortBusy $Port2)) ""

Write-Host "`n[7] 重启后端口可立即重新占用"
$proc2 = Start-Service
Start-Sleep -Seconds 2
Check "端口 $Port1 可重新占用" (Wait-PortOpen $Port1) ""
$s5 = Get-ExitIP @('-s', '--max-time', '30', '--socks5-hostname', "127.0.0.1:$Port1", 'https://api.ipify.org') $Retries
Check "重启后链路仍可用" ($s5 -match '^\d{1,3}(\.\d{1,3}){3}$') "($s5)"
Stop-Process -Id $proc2.Id -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1

Write-Host ""
if ($fail -eq 0) {
    Write-Host "=== 全部通过 ===" -ForegroundColor Green
} else {
    Write-Host "=== 失败 $fail 项 ===" -ForegroundColor Red
    Get-Content (Join-Path $Root 'data\e2e.out') -Encoding UTF8 -ErrorAction SilentlyContinue | Select-Object -Last 40
    Get-Content (Join-Path $Root 'data\e2e.err') -Encoding UTF8 -ErrorAction SilentlyContinue | Select-Object -Last 40
}
exit $fail