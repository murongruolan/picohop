﻿# 一键验证 WireGuard 出口：先查前置 UDP -> 启动服务 -> 经本地端口取出口 IP -> 报告
#
# 用法：
#   .\scripts\test_wg.ps1
#   .\scripts\test_wg.ps1 -Config <路径> -TestUrl https://ifconfig.me/ip
#
# 依次报告：
#   [0] 前置代理的 UDP 能力        <- "前置 -> WG" 能否成立的先决条件
#   [1] 本机直连的出口 IP          （对照）
#   [2] 经前置的出口 IP            （对照）
#   [3] 经 WireGuard 隧道的出口 IP + SOCKS5 / HTTP 两种客户端

param(
    [string]$Config,
    [string]$TestUrl = 'https://ifconfig.me/ip',
    [int]$WaitSec = 25
)

# 注意：默认值不能写成 (Join-Path ...)，Windows PowerShell 5.1 的 param() 解析器
# 不接受在参数默认值里调用命令，会报 "The assignment expression is not valid"。
# 所以在函数体里解析，5.1 与 7 都能用。
if (-not $Config) { $Config = Join-Path $PSScriptRoot '..\dist\data\config.json' }

$ErrorActionPreference = 'Continue'
$root = Resolve-Path (Join-Path $PSScriptRoot '..')
$console = Join-Path $root 'dist\picohop-console.exe'
$dataDir = Split-Path $Config -Parent

function FetchIp([string[]]$proxyArgs) {
    $a = @('-s', '--max-time', '25')
    if ($proxyArgs -and $proxyArgs.Count -gt 0) { $a += $proxyArgs }
    else { $a += '--noproxy'; $a += '*' }
    $a += $TestUrl
    $out = & curl.exe @a 2>&1
    $v = ($out | Out-String).Trim()
    if (-not $v) { return '(失败)' }
    return ($v -split "`n")[0].Trim()
}

# 通过 SOCKS5 的 UDP ASSOCIATE 向 8.8.8.8:53 发一个 DNS 查询；
# 能收到应答才说明前置**真的**能转发 UDP。
# 注意：很多节点会"接受 UDP ASSOCIATE 但转发不了数据"，
# 所以只看关联成功是不够的，必须真的收发一次。
function Test-FrontUdp([string]$HostName, [int]$Port) {
    $tcp = $null
    try {
        $tcp = New-Object System.Net.Sockets.TcpClient
        $tcp.Connect($HostName, $Port)
        $st = $tcp.GetStream()

        $st.Write([byte[]]@(5, 1, 0), 0, 3)          # greeting: v5, 无认证
        $r = New-Object byte[] 2
        $st.Read($r, 0, 2) | Out-Null
        if ($r[0] -ne 5) { return @{ Ok = $false; Detail = '目标不是 SOCKS5 代理' } }

        $st.Write([byte[]]@(5, 3, 0, 1, 0, 0, 0, 0, 0, 0), 0, 10)   # UDP ASSOCIATE
        $h = New-Object byte[] 4
        $st.Read($h, 0, 4) | Out-Null
        if ($h[1] -ne 0) { return @{ Ok = $false; Detail = "UDP ASSOCIATE 被拒绝（rep=$($h[1])）" } }

        $relayIp = ''
        switch ($h[3]) {
            1 { $b = New-Object byte[] 4;  $st.Read($b, 0, 4)  | Out-Null; $relayIp = ($b -join '.') }
            4 { $b = New-Object byte[] 16; $st.Read($b, 0, 16) | Out-Null; $relayIp = (New-Object System.Net.IPAddress(, $b)).ToString() }
            default { return @{ Ok = $false; Detail = '不支持的地址类型' } }
        }
        $pb = New-Object byte[] 2
        $st.Read($pb, 0, 2) | Out-Null
        $relayPort = $pb[0] * 256 + $pb[1]
        if ($relayIp -eq '0.0.0.0' -or $relayIp -eq '::') { $relayIp = $HostName }

        # 最小 DNS 查询，按 SOCKS5 UDP 头封装（目标用 1.1.1.1：经实测它在直连与经前置两种情况下都可达；8.8.8.8 在部分线路上被挡，会误判）
        $q = [byte[]]@(0xAB, 0xCD, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
                       7, 101, 120, 97, 109, 112, 108, 101, 3, 99, 111, 109, 0, 0x00, 0x01, 0x00, 0x01)
        $pkt = [byte[]]@(0, 0, 0, 1, 1, 1, 1, 1, 0, 53) + $q

        $u = New-Object System.Net.Sockets.UdpClient
        $u.Client.ReceiveTimeout = 6000
        $u.Send($pkt, $pkt.Length, $relayIp, $relayPort) | Out-Null
        $ep = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        try {
            $resp = $u.Receive([ref]$ep)
            return @{ Ok = $true; Detail = "向 1.1.1.1:53 收到 $($resp.Length) 字节应答" }
        } catch {
            return @{ Ok = $false; Detail = '已发出 UDP 但收不到任何回包（节点很可能禁用了 UDP）' }
        }
    } catch {
        return @{ Ok = $false; Detail = "无法连接前置：$($_.Exception.Message)" }
    } finally {
        if ($tcp) { $tcp.Close() }
    }
}

Write-Host "===== WireGuard 出口验证 =====" -ForegroundColor Cyan
Write-Host "配置   : $Config"
Write-Host "测试URL: $TestUrl"
Write-Host ""

try { $j = Get-Content $Config -Raw -Encoding UTF8 | ConvertFrom-Json }
catch { Write-Host "配置读取失败：$_" -ForegroundColor Red; exit 1 }

$wg = $j.upstreams | Where-Object { $_.type -eq 'wireguard' } | Select-Object -First 1
if (-not $wg) { Write-Host "配置里没有 wireguard 类型的链路" -ForegroundColor Red; exit 1 }

$localPort = $wg.listenPort
$effectiveFront = $j.front
if (($wg.PSObject.Properties.Name -contains 'frontOverride') -and $wg.frontOverride) {
    $effectiveFront = $wg.frontOverride
}
$useFront = ($effectiveFront.type -eq 'socks5' -or $effectiveFront.type -eq 'http')

Write-Host "链路      : $($wg.name)  本地端口 $localPort"
Write-Host "端点      : $($wg.wireguard.endpointHost):$($wg.wireguard.endpointPort)"
Write-Host "隧道IP    : $($wg.wireguard.address)   AllowedIPs=$($wg.wireguard.allowedIPs)"
if ($useFront) {
    Write-Host "该链路前置: $($effectiveFront.type) $($effectiveFront.host):$($effectiveFront.port)"
} else {
    Write-Host "该链路前置: 直连（不使用前置代理）"
}
Write-Host ""

# ---- 私钥检查 ----
$pk = "$($wg.wireguard.privateKey)".Trim()
if ($pk -like '*<<<*' -or $pk -like 'PASTE_YOUR_FULL_PRIVATE_KEY*') {
    Write-Host "X 私钥还没填（当前是占位符）。" -ForegroundColor Red
    Write-Host "  私钥固定 44 个字符、以 = 结尾。可用下面命令生成，避免界面复制不全："
    Write-Host "    .\dist\picohop-console.exe wgkey" -ForegroundColor Yellow
    exit 1
}
if ($pk.Length -ne 44 -or -not $pk.EndsWith('=')) {
    Write-Host "X 私钥看着不完整：长度 $($pk.Length)（应为 44），结尾 '$($pk[$pk.Length-1])'（应为 =）" -ForegroundColor Red
    exit 1
}

# ---- [0] 前置 UDP 能力 ----
if ($useFront) {
    Write-Host "[0] 检查前置 UDP 能力 ..." -ForegroundColor Cyan
    $probe = Test-FrontUdp -HostName $effectiveFront.host -Port $effectiveFront.port
    if ($probe.Ok) {
        Write-Host "    OK  $($probe.Detail)" -ForegroundColor Green
    } else {
        Write-Host "    X   $($probe.Detail)" -ForegroundColor Red
        Write-Host "        WireGuard 完全依赖 UDP 实际流动，这一步不通，握手必然超时。" -ForegroundColor Yellow
        Write-Host "        请换一个支持 UDP 的节点，或把该链路改成直连。" -ForegroundColor Yellow
    }
    Write-Host ""
}

Write-Host "[1] 本机直连出口          : $(FetchIp @())"
if ($useFront) {
    $fp = "$($effectiveFront.host):$($effectiveFront.port)"
    Write-Host "[2] 经前置($fp) 出口  : $(FetchIp @('--socks5-hostname', $fp))"
}
Write-Host ""

# ---- 启动服务 ----
Write-Host "[3] 启动 PicoHop ..." -ForegroundColor Cyan
Get-Process picohop-console -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 500

$logFile = Join-Path $env:TEMP ("picohop-wgtest-{0}.log" -f (Get-Random))
$env:PICOHOP_DATA_DIR = $dataDir
$proc = Start-Process -FilePath $console -ArgumentList @('run', '-config', $Config) `
    -PassThru -RedirectStandardOutput $logFile -RedirectStandardError ($logFile + '.err')

$deadline = (Get-Date).AddSeconds($WaitSec)
$ready = $false
while ((Get-Date) -lt $deadline) {
    Start-Sleep -Milliseconds 500
    if ($proc.HasExited) { break }
    if (Get-NetTCPConnection -LocalPort $localPort -State Listen -ErrorAction SilentlyContinue) { $ready = $true; break }
}

if ($proc.HasExited) {
    Write-Host "X 进程已退出，日志：" -ForegroundColor Red
    Get-Content $logFile -Encoding UTF8 -ErrorAction SilentlyContinue | Select-Object -Last 10
    Get-Content ($logFile + '.err') -Encoding UTF8 -ErrorAction SilentlyContinue | Select-Object -Last 6
    exit 1
}
Write-Host "    端口 $localPort $(if ($ready) { '已监听' } else { '尚未监听（继续尝试）' })"
Start-Sleep -Seconds 3

Write-Host ""
Write-Host "===== 结果 =====" -ForegroundColor Cyan
$lp = "$localPort"
Write-Host "  经隧道 SOCKS5 127.0.0.1:$lp : $(FetchIp @('--socks5-hostname', "127.0.0.1:$lp"))" -ForegroundColor Green
Write-Host "  经隧道 HTTP   127.0.0.1:$lp : $(FetchIp @('-x', "http://127.0.0.1:$lp"))" -ForegroundColor Green

Write-Host ""
Write-Host "===== 隧道状态（来自日志）=====" -ForegroundColor Cyan
Get-Content $logFile -Encoding UTF8 -ErrorAction SilentlyContinue |
    Select-String -Pattern '隧道已建立|last_handshake_time_sec|tx_bytes|rx_bytes|allowed_ip|preshared_key|握手超时|失败|专属前置' |
    Select-Object -First 16 | ForEach-Object { Write-Host ("  " + $_.Line.Trim()) }

Write-Host ""
Write-Host "===== 清理 =====" -ForegroundColor Cyan
Get-Process -Id $proc.Id -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 800
Write-Host ("  端口已释放: {0}" -f ((Get-NetTCPConnection -LocalPort $localPort -State Listen -ErrorAction SilentlyContinue).Count -eq 0))
Write-Host "  日志: $logFile"
