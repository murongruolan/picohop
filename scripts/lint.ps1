# ---------------------------------------------------------------------------
# ChainProxy 静态检查
#
#   .\scripts\lint.ps1
#
# 做四件事：
#   1. gofmt：所有 Go 代码必须格式化过
#   2. go vet：带正确的构建标签做静态检查
#   3. 配置校验：用当前配置跑一遍 validate
#   4. 引擎自检：确认构建标签齐全（缺了 Reality / Hysteria2 会在运行期才炸）
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

# 与 scripts\build.ps1 保持一致：缺了这两个标签，Reality / Hysteria2 运行时会失败
$Tags = "with_utls,with_quic"

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
Write-Host "[lint] 2/4 go vet（tags=$Tags）……"
& go vet -tags $Tags -unsafeptr=false ./... 2>&1
if ($LASTEXITCODE -ne 0) { $failed++ } else { Write-Host "  OK" }

# ---------------------------------------------------------------------------
Write-Host "[lint] 3/4 配置校验 ……"
& go run -tags $Tags ./cmd/chainproxy validate 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "  配置有问题（见上面的输出）"
    $failed++
} else {
    Write-Host "  OK"
}

# ---------------------------------------------------------------------------
Write-Host "[lint] 4/4 内嵌引擎构建标签自检 ……"
& go run -tags $Tags ./cmd/chainproxy engines 2>&1
if ($LASTEXITCODE -ne 0) { $failed++ } else { Write-Host "  OK" }

# ---------------------------------------------------------------------------
if ($failed -gt 0) {
    Write-Host ""
    Write-Host "[lint] 有 $failed 项没通过" -ForegroundColor Red
    exit 1
}
Write-Host ""
Write-Host "[lint] 全部通过" -ForegroundColor Green
