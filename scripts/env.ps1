# ---------------------------------------------------------------------------
# ChainProxy 本地环境脚本
#
# 目的：把 Go 的所有"下载/缓存"位置强制重定向到本项目目录内，
#       绝不在 C:\Users\<user>\go、F:\DevCache 等外部目录留下任何东西。
#
# 用法（在任意 pwsh 会话中）：
#   . F:\chain_proxy\scripts\env.ps1
#
# 或者直接使用 scripts\build.ps1 / scripts\test.ps1，它们会自动加载本脚本。
# ---------------------------------------------------------------------------

$ChainProxyRoot = Split-Path -Parent $PSScriptRoot

$env:GOPATH          = Join-Path $ChainProxyRoot '.cache\gopath'
$env:GOMODCACHE      = Join-Path $ChainProxyRoot '.cache\gomod'
$env:GOCACHE         = Join-Path $ChainProxyRoot '.cache\gobuild'
$env:GOTMPDIR        = Join-Path $ChainProxyRoot '.cache\gotmp'
$env:GOPROXY         = 'https://goproxy.cn,https://proxy.golang.org,direct'
$env:GOFLAGS         = '-mod=mod'
$env:CGO_ENABLED     = '0'
$env:CHAINPROXY_ROOT = $ChainProxyRoot

# 工具链：本机 go 是 1.24.0，但 Wails v2.13 要求 >=1.25.0、sing-box v1.14 要求 >=1.25.5，
# 所以必须允许 Go 自动切换工具链（GOTOOLCHAIN=auto）。
#
# 关键点：GOMODCACHE 已被重定向到 <项目>\.cache\gomod，
# 因此自动下载的工具链也落在 **项目目录内**（.cache\gomod\golang.org\toolchain@...），
# 不会写进 C:\Users\...\go 或 F:\DevCache 等外部目录。
$env:GOTOOLCHAIN     = 'auto'

foreach ($d in @($env:GOPATH, $env:GOMODCACHE, $env:GOCACHE, $env:GOTMPDIR)) {
    New-Item -ItemType Directory -Force -Path $d | Out-Null
}

Write-Host "[ChainProxy] GOPATH     = $env:GOPATH"
Write-Host "[ChainProxy] GOMODCACHE = $env:GOMODCACHE"
Write-Host "[ChainProxy] GOCACHE    = $env:GOCACHE"