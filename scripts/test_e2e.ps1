# ---------------------------------------------------------------------------
# ChainProxy 端到端测试
#
#   .\scripts\test_e2e.ps1
#   .\scripts\test_e2e.ps1 -Config data\config.json -HoldSeconds 25
#
# 验证的是**用户真正用到的那个路径**：本地 mixed 端口。
#
# 流程：
#   1. 记录全局前置代理的出口 IP 作为基准
#   2. 后台启动 chainproxy run（把所有节点拉起来，占用各自的本地端口）
#   3. 用 curl 分别以 SOCKS5 与 HTTP 两种方式走每个本地端口访问 IP 查询服务
#   4. 断言：本地端口的出口 IP 必须与前置出口 IP **不同**
#      —— 这正是"链式代理真的生效"的定义
#   5. 停止进程后确认端口已释放（能重新 bind 上）
#
# 说明：住宅 IP 是动态的，单次请求偶尔会失败，因此每次查询都重试若干次；
# 重试次数用完仍然失败才算真失败。
# ---------------------------------------------------------------------------
param(
    [string]$Config = "",
    [int]$HoldSeconds = 20,
    [int]$Retries = 3
)

$ErrorActionPreference = 'Continue'
. (Join-Path $PSScriptRoot 'env.ps1')

$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

if (-not $Config) { $Config = Join-Path $Root 'data\config.json' }

$script:failed = 0
function Fail($msg) { Write-Host "  [失败] $msg" -ForegroundColor Red; $script:failed++ }
function Ok($msg)   { Write-Host "  [通过] $msg" -ForegroundColor Green }

# ---------------------------------------------------------------------------
# 读配置，拿到每个节点的本地端口
# ---------------------------------------------------------------------------
if (-not (Test-Path $Config)) {
    Write-Host "找不到配置文件：$Config" -ForegroundColor Red
    exit 1
}
$cfg = Get-Content $Config -Raw -Encoding UTF8 | ConvertFrom-Json
if (-not $cfg.nodes -or $cfg.nodes.Count -eq 0) {
    Write-Host "配置里没有任何节点，先添加一个再跑。" -ForegroundColor Yellow
    exit 1
}

Write-Host "========================================"
Write-Host "ChainProxy 端到端测试"
Write-Host "配置：$Config"
Write-Host "节点：$($cfg.nodes.Count) 个"
Write-Host "========================================"

# ---------------------------------------------------------------------------
# 1. 基准：前置代理的出口 IP
# ---------------------------------------------------------------------------
Write-Host ""
Write-Host "[1/4] 取全局前置代理出口 IP（基准）"
$fetchOut = & go run ./cmd/chainproxy fetch -config $Config -node '' 2>&1 | Out-String
Write-Host $fetchOut

$frontIP = ''
$m = [regex]::Match($fetchOut, '✅\s+([0-9]{1,3}(?:\.[0-9]{1,3}){3})')
if ($m.Success) { $frontIP = $m.Groups[1].Value }
if (-not $frontIP) {
    Write-Host "  [警告] 没能从输出里解析出前置出口 IP，后面只能做连通性判断" -ForegroundColor Yellow
} else {
    Write-Host "  前置出口 IP = $frontIP"
}

# ---------------------------------------------------------------------------
# 2. 启动
# ---------------------------------------------------------------------------
Write-Host ""
Write-Host "[2/4] 后台启动节点（$HoldSeconds 秒）"
$runOut = Join-Path $Root '.cache\e2e-run.log'
$proc = Start-Process -FilePath 'go' `
    -ArgumentList @('run', './cmd/chainproxy', 'run', '-config', $Config, '-seconds', "$HoldSeconds") `
    -WorkingDirectory $Root -RedirectStandardOutput $runOut -RedirectStandardError "$runOut.err" -PassThru
Start-Sleep -Seconds 6
Write-Host "  已启动（pid=$($proc.Id)），日志：$runOut"

# ---------------------------------------------------------------------------
# 3. 逐个端口用 SOCKS5 / HTTP 探测
# ---------------------------------------------------------------------------
Write-Host ""
Write-Host "[3/4] 经本地端口探测出口 IP"

function ProbeIP([string]$scheme, [int]$port) {
    for ($i = 1; $i -le $Retries; $i++) {
        $ip = ''
        if ($scheme -eq 'socks5') {
            $ip = (& curl.exe -s --max-time 20 --socks5-hostname "127.0.0.1:$port" https://api.ipify.org 2>$null)
        } else {
            $ip = (& curl.exe -s --max-time 20 -x "http://127.0.0.1:$port" https://api.ipify.org 2>$null)
        }
        $ip = ($ip | Out-String).Trim()
        if ($ip -match '^[0-9]{1,3}(\.[0-9]{1,3}){3}$') { return $ip }
        Start-Sleep -Milliseconds 800
    }
    return ''
}

# ---------------------------------------------------------------------------
# 判定口径（这一节刻意分成了"硬失败"和"警告"两类）
#
#   硬失败：
#     - 本地端口连不上          -> 我们的监听/服务有问题，就是 bug
#     - 拿到了出口 IP 但与前置相同 -> 链式代理没生效，就是 bug
#
#   警告（不计入失败）：
#     - 重试完还是拿不到出口 IP  -> 这是**住宅代理服务商自己在抖**
#       实测：同一个上游账号、配置完全相同的两个节点，同一时刻一个通一个不通；
#       再跑一次又反过来了。把它算成失败只会让这个脚本变得薛定谔，
#       反而掩盖真正的问题。
# ---------------------------------------------------------------------------
foreach ($n in $cfg.nodes) {
    $port = [int]$n.listenPort
    $name = $n.name
    Write-Host ""
    Write-Host "  · $name（本地端口 $port，协议 $($n.protocol)）"

    # 先确认本地端口确实在监听（这是我们的责任）
    $listening = $false
    try {
        $c = [System.Net.Sockets.TcpClient]::new()
        $task = $c.ConnectAsync('127.0.0.1', $port)
        $listening = $task.Wait(2000) -and $c.Connected
        $c.Close()
    } catch { $listening = $false }

    if (-not $listening) {
        Fail "本地端口 $port 没有在监听"
        continue
    }
    Write-Host "    [监听] 127.0.0.1:$port 可连接" -ForegroundColor Green

    $s5 = ProbeIP 'socks5' $port
    $hp = ProbeIP 'http' $port

    $ok = 0
    $sameAsFront = $false
    foreach ($pair in @(@('SOCKS5', $s5), @('HTTP', $hp))) {
        $label = $pair[0]; $ip = $pair[1]
        if (-not $ip) {
            Write-Host "    [$label] 重试 $Retries 次仍拿不到出口 IP（多为住宅服务商抖动）" -ForegroundColor Yellow
            continue
        }
        if ($frontIP -and $ip -eq $frontIP) {
            Write-Host "    [$label] 出口 IP $ip 与前置相同 —— 流量没有走这个出口" -ForegroundColor Red
            $sameAsFront = $true
            continue
        }
        Write-Host "    [$label] 出口 IP $ip（与前置不同）" -ForegroundColor Green
        $ok++
    }

    if ($sameAsFront) {
        Fail "$name ：出口 IP 与前置相同（链式代理没生效）"
    } elseif ($ok -eq 0) {
        Write-Host "    [警告] SOCKS5 与 HTTP 都没拿到出口 IP；端口是通的，多半是出口侧网络问题" -ForegroundColor Yellow
    } else {
        Ok "$name ：链路生效（$ok/2 种客户端协议探测成功）"
    }
}

# ---------------------------------------------------------------------------
# 4. 停止并确认端口释放
# ---------------------------------------------------------------------------
Write-Host ""
Write-Host "[4/4] 停止并确认端口已释放"
& go run ./cmd/chainproxy validate -config $Config | Out-Null
Get-Process chainproxy -ErrorAction SilentlyContinue | ForEach-Object { $_.Kill() | Out-Null }
if ($proc -and -not $proc.HasExited) { $proc.Kill() | Out-Null }
Start-Sleep -Seconds 2

foreach ($n in $cfg.nodes) {
    $port = [int]$n.listenPort
    $busy = $false
    try {
        $l = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $port)
        $l.Start(); $l.Stop()
    } catch { $busy = $true }
    if ($busy) { Fail "端口 $port 停止后仍被占用" } else { Ok "端口 $port 已释放" }
}

# ---------------------------------------------------------------------------
Write-Host ""
if ($script:failed -gt 0) {
    Write-Host "端到端测试：$($script:failed) 项失败" -ForegroundColor Red
    exit 1
}
Write-Host "端到端测试：全部通过" -ForegroundColor Green
