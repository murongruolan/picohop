# ---------------------------------------------------------------------------
# ChainProxy 静态检查
#
#   .\scripts\lint.ps1
#
# 说明：这里刻意关掉了 vet 的 unsafeptr 检查（-unsafeptr=false）。
#
# 原因：ChainProxy 的 GUI 是**纯 syscall 直调 Win32**（不引入 cgo，也不引入
# golang.org/x/sys/windows），因此必须把 Windows 传进来的 uintptr 形态的
# LPARAM 还原成结构体指针才能读 NMHDR / NMCUSTOMDRAW 等通知数据，
# 例如：
#
#     hdr := (*NMHDR)(unsafePtrAt(lParam, 0))
#
# 这是这个技术路线下**不可避免**且语义正确的用法：lParam 指向的是 Windows
# 内核/USER32 提供的内存（消息队列里），既不由 Go 堆管理、也不会被 GC 移动，
# 因此转换是安全的；而 vet 的 unsafeptr 规则无法区分"从 syscall 拿到的
# uintptr"和"从 Go 堆算出来的 uintptr"，只能一律报可疑。
#
# 其余所有 vet 检查（printf、copylocks、lostcancel 等）保持开启。
# ---------------------------------------------------------------------------
param(
    [switch]$Fix
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
Set-Location (Split-Path -Parent $PSScriptRoot)

Write-Host "[ChainProxy] gofmt ..."
$fmt = & gofmt -l . 2>&1
if ($fmt) {
    Write-Host "  以下文件格式不符合 gofmt："
    $fmt | ForEach-Object { Write-Host "    $_" -ForegroundColor Yellow }
    if ($Fix) {
        & gofmt -w . 2>&1 | Out-Null
        Write-Host "  已自动格式化。" -ForegroundColor Green
    } else {
        Write-Host "  提示：执行 .\scripts\lint.ps1 -Fix 自动修复" -ForegroundColor DarkGray
    }
} else {
    Write-Host "  全部符合 gofmt。" -ForegroundColor Green
}

Write-Host "`n[ChainProxy] go vet（unsafeptr 已按脚本头部说明关闭）..."
& go vet -unsafeptr=false ./...
if ($LASTEXITCODE -ne 0) { throw "go vet 未通过" }

Write-Host "`n[ChainProxy] go test ..."
& go test ./...
if ($LASTEXITCODE -ne 0) { throw "go test 未通过" }

Write-Host "`n[ChainProxy] 检查通过。" -ForegroundColor Green