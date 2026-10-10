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
$env:GOTOOLCHAIN        = 'local'   # 不自动下载新工具链，版本完全由本机 go 决定
$env:CHAINPROXY_ROOT = $ChainProxyRoot

foreach ($d in @($env:GOPATH, $env:GOMODCACHE, $env:GOCACHE, $env:GOTMPDIR)) {
    New-Item -ItemType Directory -Force -Path $d | Out-Null
}

Write-Host "[ChainProxy] GOPATH     = $env:GOPATH"
Write-Host "[ChainProxy] GOMODCACHE = $env:GOMODCACHE"
Write-Host "[ChainProxy] GOCACHE    = $env:GOCACHE"