# ---------------------------------------------------------------------------
# ChainProxy 界面截图（开发期自测）
#
#   .\scripts\ui_shot.ps1                  截主界面
#   .\scripts\ui_shot.ps1 -Open settings   启动时直接打开"设置"面板再截图
#   .\scripts\ui_shot.ps1 -List            列出当前可见窗口标题
#
# 为什么要有这个脚本：这是个桌面程序，界面排版对不对、对话框有没有错位，
# 读代码是看不出来的，必须真的看一眼。截图结果默认放在 .cache\shots\。
#
# 【为什么不用"模拟点击"来打开对话框】
# 做过，然后删了。真实鼠标注入（SetCursorPos + mouse_event）有一个致命问题：
# 一旦被测窗口失去前台焦点，点击就会落到**用户当时正在用的别的程序**上。
# 实测确实发生过一次（截图里出现了用户另一个程序的画面）。
#
# 现在改用程序自己的 -open 启动参数让界面主动打开面板，纯只读、零副作用。
# ---------------------------------------------------------------------------
param(
    [string]$Open = "",
    [string]$Out = "",
    [switch]$List,
    [switch]$NoBuild,          # 已经构建过就跳过构建，省时间
    [int]$WaitSeconds = 6
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')

$Root = Split-Path -Parent $PSScriptRoot
$Exe  = Join-Path $Root 'desktop\build\bin\ChainProxy.exe'
$Tag  = "with_utls,with_quic"

if (-not $Out) {
    $name = if ($Open) { $Open } else { 'main' }
    $Out = Join-Path $Root ".cache\shots\$name.png"
}

if ($List) {
    Set-Location $Root
    & go run ./cmd/uishot -list
    exit $LASTEXITCODE
}

# ---------------------------------------------------------------------------
# 1. 保证有可执行的程序
# ---------------------------------------------------------------------------
if (-not $NoBuild -or -not (Test-Path $Exe)) {
    Write-Host "[ui_shot] 构建图形界面 ……"
    Set-Location (Join-Path $Root 'desktop')
    & wails build -s -tags $Tag -platform windows/amd64 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "wails 构建失败" }
}

# ---------------------------------------------------------------------------
# 2. 只留一个实例
# ---------------------------------------------------------------------------
Get-Process ChainProxy -ErrorAction SilentlyContinue | ForEach-Object { $_.Kill() | Out-Null }
Start-Sleep -Milliseconds 1200

# ---------------------------------------------------------------------------
# 3. 启动（必要时带 -open）
# ---------------------------------------------------------------------------
$sp = @{ FilePath = $Exe; WorkingDirectory = $Root }
if ($Open) { $sp.ArgumentList = @('-open', $Open) }
Start-Process @sp | Out-Null
Start-Sleep -Seconds $WaitSeconds

# ---------------------------------------------------------------------------
# 4. 截图
# ---------------------------------------------------------------------------
Set-Location $Root
& go run ./cmd/uishot -title ChainProxy -out $Out -wait 15
if ($LASTEXITCODE -ne 0) { throw "截图失败" }

Write-Host ""
Write-Host "[ui_shot] 完成：$Out"
Write-Host "          可用的 -Open 取值：addnode / edit / settings / import / logs / share / portonly / contextmenu"
