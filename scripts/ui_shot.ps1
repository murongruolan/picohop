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
# 现在改用程序自己的 -open 启动参数让界面主动打开面板。
#
# 【-open 不再是"零副作用"——所以这里必须隔离数据目录】
#
# 自测入口为了截图会真的去切底部栏视图、点保存按钮，而底部栏的展开/页签/高度
# 是**用户的持久偏好**（写在 config.json 里）。早先这个脚本直接用仓库里的
# data\，于是"截一张图"就等于改用户的偏好：实测 -open globaltag 把
# bottomPanelOpen 写成了 false（用户下次启动发现面板是收起的），
# -open netpanel 还把空闲断开从 300 改成了 240。
#
# 现在：把真实 data\config.json **复制**一份到隔离目录，用 CHAINPROXY_DATA_DIR
# 启动程序 —— 自测再怎么点都只写在副本上；跑完还会核对真实配置的哈希，
# 一旦被改就报错（而不是默默把用户状态换掉）。纪律见 docs/VERIFY_DISCIPLINE.md。
# ---------------------------------------------------------------------------
param(
    [string]$Open = "",
    [string]$Out = "",
    [switch]$List,
    [switch]$NoBuild,          # 已经构建过就跳过构建，省时间
    [int]$WaitSeconds = 6,
    [string]$DataDir = ""      # 默认为 .cache\uishot-data（隔离目录），传值可覆盖
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')

$Root = Split-Path -Parent $PSScriptRoot
$Exe  = Join-Path $Root 'desktop\build\bin\ChainProxy.exe'

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
    # 与 scripts\build.ps1 走同一条编译路径：v3 起不用 wails build（那条链是
    # Taskfile 驱动的，得额外装 task 执行器），直接"前端 npm run build → go build"。
    # production 标签不能漏，v3 用它区分 production/dev 两套实现文件；
    # 引擎构建标签（with_utls / with_quic）随 sing-box 一起删了，不用再带。
    Push-Location $Root
    & npm --prefix (Join-Path $Root 'desktop\frontend') run build
    if ($LASTEXITCODE -ne 0) { Pop-Location; throw "前端构建失败" }
    # Vite 会清空 dist，占位文件要补回来，否则 //go:embed all:frontend/dist 找不到文件
    New-Item -ItemType File -Force (Join-Path $Root 'desktop\frontend\dist\.keep') | Out-Null
    New-Item -ItemType Directory -Force -Path (Split-Path $Exe -Parent) | Out-Null
    & go build -tags "production" -trimpath -buildvcs=false `
        -ldflags "-s -w -H windowsgui" -o $Exe ./desktop
    if ($LASTEXITCODE -ne 0) { Pop-Location; throw "图形界面编译失败" }
    Pop-Location
}

# ---------------------------------------------------------------------------
# 2. 只留一个实例
# ---------------------------------------------------------------------------
Get-Process ChainProxy -ErrorAction SilentlyContinue | ForEach-Object { $_.Kill() | Out-Null }
Start-Sleep -Milliseconds 1200

# ---------------------------------------------------------------------------
# 2.5 数据目录隔离（见文件头的说明）
#
# $env: 是**进程级**的，脚本跑完不能留着 —— 否则用户接着双击 dist\ChainProxy.exe
# 会莫名其妙用上截图用的那份配置。所以先记下原值，退出前一定还原。
# ---------------------------------------------------------------------------
$prevDataDir = $env:CHAINPROXY_DATA_DIR
$realCfg = Join-Path $Root 'data\config.json'
$realHashBefore = if (Test-Path $realCfg) { (Get-FileHash $realCfg -Algorithm SHA256).Hash } else { '' }

if (-not $DataDir) { $DataDir = Join-Path $Root '.cache\uishot-data' }
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
if (Test-Path $realCfg) {
    # 复制一份（含用户节点，只落在这个 gitignore 掉的 .cache 目录里）；
    # 这样截图里看到的仍然是他真实的节点列表，而不是一份空配置。
    Copy-Item $realCfg (Join-Path $DataDir 'config.json') -Force
}
Write-Host "[ui_shot] 数据目录（隔离）：$DataDir"
$env:CHAINPROXY_DATA_DIR = $DataDir

try {
    # -----------------------------------------------------------------------
    # 3. 启动（必要时带 -open）
    # -----------------------------------------------------------------------
    $sp = @{ FilePath = $Exe; WorkingDirectory = $Root }
    if ($Open) { $sp.ArgumentList = @('-open', $Open) }
    Start-Process @sp | Out-Null
    Start-Sleep -Seconds $WaitSeconds

    # -----------------------------------------------------------------------
    # 4. 截图
    # -----------------------------------------------------------------------
    Set-Location $Root
    & go run ./cmd/uishot -title ChainProxy -out $Out -wait 15
    if ($LASTEXITCODE -ne 0) { throw "截图失败" }

    Write-Host ""
    Write-Host "[ui_shot] 完成：$Out"
    Write-Host "          可用的 -Open 取值：addnode / edit / settings / import / logs / share / portonly / contextmenu / notify / columns / enterkey / resize / dialogdrag"
} finally {
    # -----------------------------------------------------------------------
    # 5. 还原环境变量 + 核对用户的真实配置没被动过
    # -----------------------------------------------------------------------
    if ($null -eq $prevDataDir) { Remove-Item Env:\CHAINPROXY_DATA_DIR -ErrorAction SilentlyContinue }
    else { $env:CHAINPROXY_DATA_DIR = $prevDataDir }

    $realHashAfter = if (Test-Path $realCfg) { (Get-FileHash $realCfg -Algorithm SHA256).Hash } else { '' }
    if ($realHashBefore -ne $realHashAfter) {
        throw ("用户的真实配置被改动了：$realCfg`n" +
               "  跑之前：$realHashBefore`n" +
               "  跑之后：$realHashAfter`n" +
               "截图脚本必须只写 $DataDir。若是别的 ChainProxy 实例（或人手）在这期间写过，请先关掉它再跑。")
    }
    Write-Host "[ui_shot] 用户的真实配置未被改动（sha256 前后一致）"
}
