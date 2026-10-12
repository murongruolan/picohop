# ---------------------------------------------------------------------------
# ChainProxy 静态检查
#
#   .\scripts\lint.ps1
#
# 做四件事：
#   1. gofmt：所有 Go 代码必须格式化过
#   2. go vet：静态检查
#   3. 配置校验：用当前配置跑一遍 validate
#   4. 内核自检：确认内嵌 mihomo 真的注册上了（没有第二个内核，也没有构建标签要查）
#
# 【关于 -unsafeptr=false】
# cmd\uishot 里需要用 syscall 把 Windows 传进来的 uintptr 还原成结构体指针
# （GetWindowRect / GetClientRect 这类 API 直接填结构体），这是纯 syscall
# 直调 Win32 的固有写法，vet 的 unsafeptr 检查必然误报，所以关掉它。
# 但只关这一项，其它 vet 检查一律保留。
# ---------------------------------------------------------------------------
$ErrorActionPreference = 'Continue'
. (Join-Path $PSScriptRoot 'env.ps1')

$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

# 不带任何引擎构建标签：with_utls / with_quic 是内嵌 sing-box 的，已随它摘除；
# 现在唯一的内核 mihomo 自带 Reality(uTLS) 与 Hysteria2(QUIC) 能力。
# 与 scripts\build.ps1、scripts\check.ps1 保持一致。

$failed = 0

# ---------------------------------------------------------------------------
Write-Host "[lint] 1/4 gofmt ……"
$unformatted = & gofmt -l cmd internal desktop 2>&1
if ($unformatted) {
    Write-Host "  以下文件没有格式化（跑 gofmt -w 修正）："
    $unformatted | ForEach-Object { Write-Host "    $_" }
    $failed++
} else {
    Write-Host "  OK"
}

# ---------------------------------------------------------------------------
Write-Host "[lint] 2/4 go vet ……"
& go vet -unsafeptr=false ./... 2>&1
if ($LASTEXITCODE -ne 0) { $failed++ } else { Write-Host "  OK" }

# ---------------------------------------------------------------------------
Write-Host "[lint] 3/4 配置校验 ……"
& go run ./cmd/chainproxy validate 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "  配置有问题（见上面的输出）"
    $failed++
} else {
    Write-Host "  OK"
}

# ---------------------------------------------------------------------------
Write-Host "[lint] 4/4 内嵌内核自检 ……"
& go run ./cmd/chainproxy engines 2>&1
if ($LASTEXITCODE -ne 0) { $failed++ } else { Write-Host "  OK" }

# ---------------------------------------------------------------------------
if ($failed -gt 0) {
    Write-Host ""
    Write-Host "[lint] 有 $failed 项没通过" -ForegroundColor Red
    exit 1
}
Write-Host ""
Write-Host "[lint] 全部通过" -ForegroundColor Green
