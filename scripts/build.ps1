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
#   dist\ChainProxy.exe        图形界面（Wails + Vue）—— 平时双击这个
#   dist\chainproxy-cli.exe    命令行（排障、自动化测试）
#
# 【为什么命令行版不能叫 chainproxy.exe】
# NTFS 文件名**不区分大小写**，`ChainProxy.exe` 与 `chainproxy.exe` 是同一个文件。
# 早先就是这么命名的，结果图形界面那一步把命令行版直接覆盖掉了 ——
# 表现是"想用命令行却弹出界面"，而且 PE 子系统变成了 GUI，
# 在控制台里跑它既不输出也不退出（挂住）。
#
# 【关于构建标签】
# 命令行版与图形界面版都**不再需要任何引擎标签**：内嵌的 sing-box 已摘除
# （它才需要 with_utls / with_quic 来开 Reality(uTLS) 与 Hysteria2(QUIC)），
# 现在唯一的内核是 mihomo，它自带这些能力。标签只保留 v3 的 production
# （见下面图形界面那段：它区分 production/dev 两套实现文件）。
#
# 所有 Go / npm 缓存都在项目目录内（见 scripts\env.ps1），不在外部留下任何文件。
# ---------------------------------------------------------------------------
param(
    [string]$Version = "1.0.0",
    [switch]$Cli,
    [switch]$Gui,
    [switch]$SkipFrontend
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')

$Root   = Split-Path -Parent $PSScriptRoot
$OutDir = Join-Path $Root 'dist'
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$buildCli = -not $Gui
$buildGui = -not $Cli

Set-Location $Root

# ---------------------------------------------------------------------------
# 命令行版
# ---------------------------------------------------------------------------
if ($buildCli) {
    $ldflags = "-s -w -X main.Version=$Version"
    $cliName = 'chainproxy-cli.exe'
    Write-Host "[ChainProxy] 编译命令行版（version=$Version）……"
    go build -trimpath -ldflags $ldflags -o (Join-Path $OutDir $cliName) ./cmd/chainproxy
    if ($LASTEXITCODE -ne 0) { throw "命令行版编译失败" }
    $size = (Get-Item (Join-Path $OutDir $cliName)).Length
    Write-Host "[ChainProxy] 完成：dist\$cliName  ($([math]::Round($size/1MB,2)) MB)"
}

# ---------------------------------------------------------------------------
# 图形界面版
#
# 【v3 起不再用 wails build】
# `wails build` 是一条 Taskfile 驱动的流水线（根 Taskfile.yml → build/windows/
# Taskfile.yml），需要额外装 `task` 这个 Taskfile 执行器，而它做的事情本质上就是：
#   前端 npm run build → go build -tags production -ldflags "-H windowsgui"
# 我们直接调这条命令，好处是：不依赖 wails3 CLI 与 task、构建过程可见可控、
# 和命令行版走完全一样的编译路径。
#
# **production 标签不能忘**：v3 用它区分 production/dev 两套实现文件
# （roles_production.go / roles_dev.go 等），漏了会构建出 dev 行为。
# ---------------------------------------------------------------------------
if ($buildGui) {
    $desktop = Join-Path $Root 'desktop'

    if (-not $SkipFrontend) {
        Write-Host "[ChainProxy] 构建前端……"
        Push-Location (Join-Path $desktop 'frontend')
        if (-not (Test-Path 'node_modules')) {
            Write-Host "[ChainProxy] 安装前端依赖……"
            npm install --no-audit --no-fund
            if ($LASTEXITCODE -ne 0) { Pop-Location; throw "npm install 失败" }
        }
        npm run build
        if ($LASTEXITCODE -ne 0) { Pop-Location; throw "前端构建失败" }
        # Vite 每次构建都会清空 dist，占位文件要补回来，
        # 否则下次 go build ./... / go test ./... 会因为 //go:embed 找不到文件而失败。
        New-Item -ItemType File -Force (Join-Path $desktop 'frontend\dist\.keep') | Out-Null
        Pop-Location
    }

    Write-Host "[ChainProxy] 编译图形界面（version=$Version, tags=production）……"
    $guiLdflags = "-s -w -H windowsgui -X main.Version=$Version"
    go build -tags "production" -trimpath -buildvcs=false `
        -ldflags $guiLdflags -o (Join-Path $OutDir 'ChainProxy.exe') ./desktop
    if ($LASTEXITCODE -ne 0) { throw "图形界面编译失败" }

    $size = (Get-Item (Join-Path $OutDir 'ChainProxy.exe')).Length
    Write-Host "[ChainProxy] 完成：dist\ChainProxy.exe  ($([math]::Round($size/1MB,2)) MB)"
}

Set-Location $Root
Write-Host ""
Write-Host "[ChainProxy] 全部完成。产物在 dist\ 内。"
Write-Host "  图形界面： dist\ChainProxy.exe      （双击运行）"
Write-Host "  命令行：   dist\chainproxy-cli.exe  （ls / run / test / fetch / import / express）"
