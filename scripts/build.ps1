# ---------------------------------------------------------------------------
# ChainProxy 构建脚本
#
#   .\scripts\build.ps1                 构建图形界面 + 命令行两个可执行文件
#   .\scripts\build.ps1 -Cli            只构建命令行版（快）
#   .\scripts\build.ps1 -Gui            只构建图形界面版
#   .\scripts\build.ps1 -Version 1.2.3  指定版本号
#   .\scripts\build.ps1 -SkipFrontend   跳过前端 npm 构建（前端没改时省时间）
#
# 产物都在 dist\ 内：
#   dist\ChainProxy.exe   图形界面（Wails + Vue）
#   dist\chainproxy.exe   命令行（排障、自动化测试）
#
# 【关于构建标签】
# sing-box 把 Reality(uTLS) 与 Hysteria2(QUIC) 放在构建标签后面，
# **必须**带 with_utls,with_quic，否则运行时才会报
# "uTLS, which is required by reality is not included in this build"。
# 本脚本已经把标签写死，避免忘带。
#
# 所有 Go / npm 缓存都在项目目录内（见 scripts\env.ps1），不在外部留下任何文件。
# ---------------------------------------------------------------------------
param(
    [string]$Version = "1.0.0",
    [switch]$Cli,
    [switch]$Gui,
    [switch]$SkipFrontend,
    [switch]$Console
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')

$Root   = Split-Path -Parent $PSScriptRoot
$OutDir = Join-Path $Root 'dist'
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

# 两个都需要：with_utls 给 Reality，with_quic 给 Hysteria2
$Tags = "with_utls,with_quic"

$buildCli = -not $Gui
$buildGui = -not $Cli

Set-Location $Root

# ---------------------------------------------------------------------------
# 命令行版
# ---------------------------------------------------------------------------
if ($buildCli) {
    $ldflags = "-s -w -X main.Version=$Version"
    Write-Host "[ChainProxy] 编译命令行版（version=$Version, tags=$Tags）……"
    if ($Console) {
        # 带控制台窗口，方便看到实时日志
        go build -tags $Tags -trimpath -ldflags $ldflags -o (Join-Path $OutDir 'chainproxy.exe') ./cmd/chainproxy
    } else {
        go build -tags $Tags -trimpath -ldflags $ldflags -o (Join-Path $OutDir 'chainproxy.exe') ./cmd/chainproxy
    }
    if ($LASTEXITCODE -ne 0) { throw "命令行版编译失败" }
    $size = (Get-Item (Join-Path $OutDir 'chainproxy.exe')).Length
    Write-Host "[ChainProxy] 完成：dist\chainproxy.exe  ($([math]::Round($size/1MB,2)) MB)"
}

# ---------------------------------------------------------------------------
# 图形界面版（Wails 会自己调 npm 构建前端）
# ---------------------------------------------------------------------------
if ($buildGui) {
    $desktop = Join-Path $Root 'desktop'
    Set-Location $desktop

    if (-not $SkipFrontend) {
        Write-Host "[ChainProxy] 安装前端依赖……"
        Push-Location (Join-Path $desktop 'frontend')
        npm install --no-audit --no-fund
        if ($LASTEXITCODE -ne 0) { Pop-Location; throw "npm install 失败" }
        Pop-Location
    }

    $wailsArgs = @('build', '-tags', $Tags, '-platform', 'windows/amd64')
    if ($SkipFrontend) { $wailsArgs += '-s' }   # -s: 跳过前端构建
    $wailsArgs += @('-ldflags', "-s -w -X main.Version=$Version")

    Write-Host "[ChainProxy] 用 Wails 打包图形界面（version=$Version, tags=$Tags）……"
    & wails @wailsArgs
    if ($LASTEXITCODE -ne 0) { throw "Wails 打包失败" }

    # Vite 每次构建都会清空 dist，占位文件要补回来，
    # 否则下次 go build ./... / go test ./... 会因为 //go:embed 找不到文件而失败。
    $keep = Join-Path $desktop 'frontend\dist\.keep'
    if (-not (Test-Path $keep)) { New-Item -ItemType File -Force $keep | Out-Null }

    $built = Join-Path $desktop 'build\bin\ChainProxy.exe'
    if (-not (Test-Path $built)) { throw "没有找到打包产物：$built" }
    Copy-Item -Force $built (Join-Path $OutDir 'ChainProxy.exe')

    $size = (Get-Item (Join-Path $OutDir 'ChainProxy.exe')).Length
    Write-Host "[ChainProxy] 完成：dist\ChainProxy.exe  ($([math]::Round($size/1MB,2)) MB)"
}

Set-Location $Root
Write-Host ""
Write-Host "[ChainProxy] 全部完成。产物在 dist\ 内。"
Write-Host "  图形界面： dist\ChainProxy.exe"
Write-Host "  命令行：   dist\chainproxy.exe ls / run / test / fetch / import / express"
