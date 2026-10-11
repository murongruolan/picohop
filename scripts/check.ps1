# 提交前检查：跑不绿就不许提交。
#
# 【为什么要有这个脚本】这一轮我**带着红的测试提交了一次**。两个原因：
#   1. 改了函数签名后只跑了 go build —— 而 go build **不编译测试文件**；
#   2. 提交那步把 `go test` 的输出管道给了 Select-String，
#      而 PowerShell 里管道的退出码取的是**最后一个命令**的 —— 失败被吞掉了。
# 所以这里：先跑测试、**先取退出码**、再打印，任一环节失败就退出非 0。
$ErrorActionPreference = 'Stop'
. "$PSScriptRoot\env.ps1" | Out-Null
Set-Location (Split-Path $PSScriptRoot -Parent)

$failed = @()

Write-Host '[check] gofmt …'
$fmt = & gofmt -l cmd internal desktop
if ($LASTEXITCODE -ne 0) { $failed += 'gofmt 执行失败' }
if ($fmt) { $failed += "gofmt 未格式化: $($fmt -join ', ')" }

Write-Host '[check] go vet …'
& go vet -tags "with_utls,with_quic" -unsafeptr=false ./... 2>&1 | Out-Host
if ($LASTEXITCODE -ne 0) { $failed += 'go vet 失败' }

Write-Host '[check] go test（全部包）…'
& go test -tags "with_utls,with_quic" -count=1 ./... 2>&1 | Out-Host
if ($LASTEXITCODE -ne 0) { $failed += 'go test 未全绿' }

if ($failed.Count) {
  Write-Host ''
  Write-Host '[check] ❌ 未通过：' -ForegroundColor Red
  $failed | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
  exit 1
}
Write-Host ''
Write-Host '[check] ✅ 全部通过（gofmt / go vet / go test）' -ForegroundColor Green