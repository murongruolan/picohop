# ---------------------------------------------------------------------------
# ChainProxy 构建脚本
#
#   .\scripts\build.ps1            构建 dist\chainproxy.exe
#   .\scripts\build.ps1 -Version 1.0.0
#
# 所有 Go 缓存都在项目目录内（见 scripts\env.ps1），不在外部留下任何文件。
# ---------------------------------------------------------------------------
param(
    [string]$Version = "1.0.0",
    [switch]$Console
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')

$Root   = Split-Path -Parent $PSScriptRoot
$OutDir = Join-Path $Root 'dist'
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

Set-Location $Root

$ldflags = "-s -w -X main.Version=$Version"
Write-Host "[ChainProxy] 编译中（version=$Version）……"

if ($Console) {
    # 带控制台窗口，方便 CLI 子命令调试
    go build -trimpath -ldflags $ldflags -o (Join-Path $OutDir 'chainproxy.exe') ./cmd/chainproxy
} else {
    # -H=windowsgui：双击不弹黑框（GUI 模式）；CLI 子命令仍可用
    go build -trimpath -ldflags "-s -w -H=windowsgui -X main.Version=$Version" -o (Join-Path $OutDir 'chainproxy.exe') ./cmd/chainproxy
}

if ($LASTEXITCODE -ne 0) { throw "编译失败" }

$size = (Get-Item (Join-Path $OutDir 'chainproxy.exe')).Length
Write-Host "[ChainProxy] 完成：dist\chainproxy.exe  ($([math]::Round($size/1MB,2)) MB)"