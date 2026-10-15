# 从一张母图生成三平台的应用图标与三种底色的托盘图标。
#
# 用法：
#   .\scripts\gen_icons.ps1                  用 desktop\build\icon-master.png
#   .\scripts\gen_icons.ps1 -Source icon.png 用指定的母图（例如你刚导出的那份）
#   .\scripts\gen_icons.ps1 -Preview         额外在 .cache\ 下出一张核对用的预览图
#   .\scripts\gen_icons.ps1 -Syso            顺带用 rsrc 重生成 exe 的图标资源（需要 rsrc）
#
# 产出（全部入库，供三平台构建与运行期使用）：
#   desktop\build\appicon.png                     1024²   Wails 认的应用图标
#   desktop\build\windows\icon.ico                16…256  exe / 窗口 / 任务栏（多尺寸）
#   desktop\build\darwin\icons.icns               16…1024 macOS 应用图标（Wails 认的文件名）
#   desktop\build\linux\appicon.png               512²    Linux 图标源
#
# 【配色规则（使用者定）】除托盘之外的地方**一律用白底那一版**（窗口图标、任务栏、
# exe、macOS/Linux 打包图标）；托盘是唯一会随模式换底色的地方：白底（平时）、
# #3E1F08（全局代理模式）、#240046（预留 TUN）。白底那一版带一条极细灰边
# （#C9CDD4，宽度随尺寸等比），否则它在浅色任务栏上会与背景融在一起。
#   desktop\build\linux\icons\hicolor\<N>x<N>\apps\com.murongruolan.picohop.png
#   desktop\build\icons\tray\tray-<state>-<N>.png normal|global|tun × 16/22/32/44/64
#   desktop\build\icons\tray\tray-<state>.ico     同上，Windows 托盘用（多尺寸）
#   desktop\rsrc_windows_amd64.syso               -Syso 时：exe 的文件图标（资源节）
#
# 【为什么是 PowerShell + System.Drawing，而不是 Python/Pillow 或 ImageMagick】
# 本项目所有脚本都是 PowerShell，构建机上一定有；Pillow / ImageMagick 都要额外装，
# 为了生成几个图标给使用者加一条环境要求不值当。System.Drawing 在 Windows 上自带，
# PNG 编解码、缩放重采样、alpha 合成、圆角路径它都有。
#
# 【为什么 .ico 里放的是 PNG 压缩的条目】
# ICO 的每个条目可以是 BMP(DIB) 也可以是 PNG。老工具偏爱 BMP，是因为 XP 时代的
# shell 不认 PNG；Vista 以后都认。我们随 Wails 生成的那份 icon.ico 就是 PNG 条目
# （6 个尺寸加起来 21 KB —— 若是 32bpp BMP，光 256² 就 256 KB），而且它在托盘里
# 一直正常工作。所以这里同样用 PNG 条目：文件小、实现简单、现代 Windows 无差别。
#
# 【托盘三种底色是干什么的】
#   normal  白底                     —— 平时
#   global  #3E1F08（深棕）           —— 全局代理模式开启后（系统代理指向全局节点）
#   tun     #240046（深紫）           —— 预留：以后做 TUN 模式时用
# 深色底是为了让亮青色的标记跳出来；白底那份在浅色任务栏上会与背景贴近，
# 预览图里把两种底色都画出来了，好不好看由使用者定。

[CmdletBinding()]
param(
    [string]$Source = '',
    [switch]$Preview,
    [switch]$Syso,
    # 要生成 .syso 的架构。默认只出构建目标 amd64；要发给 arm64 用户时加 -Arches amd64,arm64。
    [string[]]$Arches = @('amd64')
)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
# 与其它脚本一致：先加载环境（GOPATH 等），这样找得到 go install 装出来的 rsrc。
. "$PSScriptRoot\env.ps1" | Out-Null

if (-not $Source) {
    $Source = Join-Path $root 'desktop\build\icon-master.png'
    if (-not (Test-Path $Source)) { $Source = Join-Path $root 'icon.png' }
}
if (-not (Test-Path $Source)) { throw "找不到母图：$Source" }
$Source = (Resolve-Path $Source).Path

# ---------------------------------------------------------------- 基础工具
function New-Argb([string]$hex) {
    $h = $hex.TrimStart('#')
    [System.Drawing.Color]::FromArgb(255,
        [Convert]::ToInt32($h.Substring(0, 2), 16),
        [Convert]::ToInt32($h.Substring(2, 2), 16),
        [Convert]::ToInt32($h.Substring(4, 2), 16))
}

# 用 LockBits 一次性读像素：GetPixel 在 1250² 上要跑几分钟，这样是毫秒级。
function Get-AlphaBounds([System.Drawing.Bitmap]$bmp, [int]$threshold = 8) {
    $rect = New-Object System.Drawing.Rectangle 0, 0, $bmp.Width, $bmp.Height
    $data = $bmp.LockBits($rect, [System.Drawing.Imaging.ImageLockMode]::ReadOnly,
        [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    try {
        $bytes = New-Object byte[] ($data.Stride * $bmp.Height)
        [System.Runtime.InteropServices.Marshal]::Copy($data.Scan0, $bytes, 0, $bytes.Length)
        $minX = $bmp.Width; $minY = $bmp.Height; $maxX = -1; $maxY = -1
        for ($y = 0; $y -lt $bmp.Height; $y++) {
            $row = $y * $data.Stride
            for ($x = 0; $x -lt $bmp.Width; $x++) {
                if ($bytes[$row + $x * 4 + 3] -ge $threshold) {
                    if ($x -lt $minX) { $minX = $x }
                    if ($x -gt $maxX) { $maxX = $x }
                    if ($y -lt $minY) { $minY = $y }
                    if ($y -gt $maxY) { $maxY = $y }
                }
            }
        }
    } finally { $bmp.UnlockBits($data) }
    if ($maxX -lt 0) { throw '母图整幅都是透明的' }
    New-Object System.Drawing.Rectangle $minX, $minY, ($maxX - $minX + 1), ($maxY - $minY + 1)
}

function New-Canvas([int]$w, [int]$h) {
    $b = New-Object System.Drawing.Bitmap $w, $h, ([System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $g = [System.Drawing.Graphics]::FromImage($b)
    $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
    $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $g.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
    @{ Bitmap = $b; Graphics = $g }
}

function Set-HighQuality([System.Drawing.Graphics]$g) {
    $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
    $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $g.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
}

function Save-Png([System.Drawing.Bitmap]$bmp, [string]$path) {
    $dir = Split-Path -Parent $path
    if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force $dir | Out-Null }
    $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)
}

function Get-PngBytes([System.Drawing.Bitmap]$bmp) {
    $ms = New-Object System.IO.MemoryStream
    $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
    [byte[]]$bytes = $ms.ToArray()
    $ms.Dispose()
    # 【必须用逗号包一层】PowerShell 会把函数返回的数组"拆包"成管道元素；不包的话调用方
    # 拿到的是 Object[]，BinaryWriter.Write 会只写出一个字节 —— 真踩过：.ico 只有 159 字节。
    , $bytes
}

# 「应用图标」（窗口 / 任务栏 / exe / macOS / Linux 打包）：
# **一律用白底那一版** —— 使用者明确要求除托盘之外的地方只出现白底，所以这里与
# tray-normal 同一套外观（白底圆角 + 极细灰边），只是标记占比更小、留白更多。
# 托盘是唯一会随模式换底色的地方（见 tray_state.go 的说明）。
function New-AppIcon([System.Drawing.Bitmap]$art, [System.Drawing.Rectangle]$bounds, [int]$size) {
    New-TileIcon $art $bounds $size (New-Argb '#FFFFFF') 0.72 (New-Argb $BORDER) (Get-BorderWidth $size)
}

# 描边的灰与宽度。
# 【为什么宽度要随尺寸等比】它是一条"极细边"：在托盘 16 px 上是 1 px 实线，在 1024 px 的
# 应用图标上按同一比例放大（16 px）——这样把大图缩到 16 px 看时，边的粗细和托盘那份一致。
$BORDER = '#C9CDD4'
function Get-BorderWidth([int]$size) { [Math]::Max(1, [Math]::Round($size * 0.016)) }

# 圆角底块图标：底色块（可选极细描边）+ 居中的标记。
# 应用图标与三种托盘底色都走它，区别只有底色、标记占比、要不要描边。
function New-TileIcon([System.Drawing.Bitmap]$art, [System.Drawing.Rectangle]$bounds, [int]$size,
    [System.Drawing.Color]$bg, [double]$artScale,
    [System.Drawing.Color]$border = [System.Drawing.Color]::Empty, [int]$borderWidth = 0) {
    $c = New-Canvas $size $size
    $g = $c.Graphics
    $r = [Math]::Max(2, [Math]::Round($size * 0.22))
    $path = New-Object System.Drawing.Drawing2D.GraphicsPath
    $d = $r * 2
    $path.AddArc(0, 0, $d, $d, 180, 90)
    $path.AddArc($size - $d, 0, $d, $d, 270, 90)
    $path.AddArc($size - $d, $size - $d, $d, $d, 0, 90)
    $path.AddArc(0, $size - $d, $d, $d, 90, 90)
    $path.CloseFigure()
    $brush = New-Object System.Drawing.SolidBrush $bg
    $g.FillPath($brush, $path)
    $brush.Dispose(); $path.Dispose()

    if ($borderWidth -gt 0) {
        # 描边画在**内缩**一圈的路径上，这样 1 px 的边也完整落在图标内、不会被裁掉半条
        $inset = $borderWidth / 2.0
        $s2 = $size - $borderWidth
        $r2 = [Math]::Max(1.0, $r - $inset)
        $d2 = $r2 * 2
        $bp = New-Object System.Drawing.Drawing2D.GraphicsPath
        $bp.AddArc($inset, $inset, $d2, $d2, 180, 90)
        $bp.AddArc($inset + $s2 - $d2, $inset, $d2, $d2, 270, 90)
        $bp.AddArc($inset + $s2 - $d2, $inset + $s2 - $d2, $d2, $d2, 0, 90)
        $bp.AddArc($inset, $inset + $s2 - $d2, $d2, $d2, 90, 90)
        $bp.CloseFigure()
        $pen = New-Object System.Drawing.Pen $border, $borderWidth
        $g.DrawPath($pen, $bp)
        $pen.Dispose(); $bp.Dispose()
    }

    $w = [Math]::Max(1, [Math]::Round($size * $artScale))
    $h = [Math]::Max(1, [Math]::Round($w * $bounds.Height / $bounds.Width))
    $x = [Math]::Round(($size - $w) / 2.0)
    $y = [Math]::Round(($size - $h) / 2.0)
    $dest = New-Object System.Drawing.Rectangle $x, $y, $w, $h
    $g.DrawImage($art, $dest, $bounds, [System.Drawing.GraphicsUnit]::Pixel)
    $g.Dispose()
    $c.Bitmap
}

# 「托盘图标」：三种底色。**只有白底加那条极细灰边** —— 深棕/深紫底在深浅任务栏上
# 都看得见，白底在浅色任务栏上会和背景融掉（预览图里能看出来），所以描一条边把它框出来。
function New-TrayIcon([System.Drawing.Bitmap]$art, [System.Drawing.Rectangle]$bounds, [int]$size, [System.Drawing.Color]$bg) {
    $artScale = if ($size -le 22) { 0.90 } else { 0.76 }
    $isWhite = ($bg.R -eq 255 -and $bg.G -eq 255 -and $bg.B -eq 255)
    if ($isWhite) {
        return New-TileIcon $art $bounds $size $bg $artScale (New-Argb $BORDER) (Get-BorderWidth $size)
    }
    New-TileIcon $art $bounds $size $bg $artScale
}

# 多尺寸 .ico：自己拼容器（System.Drawing 的 Icon.Save 只能存单一尺寸）
function Save-Ico([string]$path, [hashtable[]]$entries) {
    # entries: @{ Size = 16; Png = [byte[]] }
    $dir = Split-Path -Parent $path
    if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force $dir | Out-Null }
    $fs = [System.IO.File]::Create($path)
    $bw = New-Object System.IO.BinaryWriter $fs
    try {
        $bw.Write([uint16]0); $bw.Write([uint16]1); $bw.Write([uint16]$entries.Count)
        $offset = 6 + 16 * $entries.Count
        foreach ($e in $entries) {
            $s = [int]$e.Size
            $bw.Write([byte]$(if ($s -ge 256) { 0 } else { $s }))
            $bw.Write([byte]$(if ($s -ge 256) { 0 } else { $s }))
            $bw.Write([byte]0); $bw.Write([byte]0)
            $bw.Write([uint16]1); $bw.Write([uint16]32)
            $bw.Write([uint32]$e.Png.Length); $bw.Write([uint32]$offset)
            $offset += $e.Png.Length
        }
        foreach ($e in $entries) { [byte[]]$png = $e.Png; $bw.Write($png) }
    } finally { $bw.Dispose(); $fs.Dispose() }
}

# .icns：同样自己拼（ic07…ic14，PNG 载荷是合法的）
function Save-Icns([string]$path, [hashtable[]]$entries) {
    $dir = Split-Path -Parent $path
    if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force $dir | Out-Null }
    $chunks = New-Object System.Collections.Generic.List[byte]
    foreach ($e in $entries) {
        [byte[]]$type = [Text.Encoding]::ASCII.GetBytes($e.Type)
        [byte[]]$len = [BitConverter]::GetBytes([uint32]($e.Png.Length + 8))
        [byte[]]$png = $e.Png
        if ([BitConverter]::IsLittleEndian) { [Array]::Reverse($len) }
        $chunks.AddRange($type); $chunks.AddRange($len); $chunks.AddRange($png)
    }
    $fs = [System.IO.File]::Create($path)
    $bw = New-Object System.IO.BinaryWriter $fs
    try {
        [byte[]]$magic = [Text.Encoding]::ASCII.GetBytes('icns')
        $bw.Write($magic)
        [byte[]]$total = [BitConverter]::GetBytes([uint32]($chunks.Count + 8))
        if ([BitConverter]::IsLittleEndian) { [Array]::Reverse($total) }
        $bw.Write($total)
        [byte[]]$all = $chunks.ToArray()
        $bw.Write($all)
    } finally { $bw.Dispose(); $fs.Dispose() }
}

# ---------------------------------------------------------------- 开始生成
$art = New-Object System.Drawing.Bitmap $Source
$art = $art.Clone((New-Object System.Drawing.Rectangle 0, 0, $art.Width, $art.Height),
    [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
$bounds = Get-AlphaBounds $art
$size0 = $art.Width
Write-Host "[图标] 母图 $Source"
Write-Host "[图标] 画布 $($art.Width)x$($art.Height)｜不透明内容 $($bounds.Width)x$($bounds.Height) @ ($($bounds.X),$($bounds.Y))"

$APP_ICO = @(16, 20, 24, 32, 40, 48, 64, 128, 256)
$LINUX = @(16, 24, 32, 48, 64, 128, 256, 512)

# 1) 各平台应用图标
$app1024 = New-AppIcon $art $bounds 1024
Save-Png $app1024 (Join-Path $root 'desktop\build\appicon.png')
$app1024.Dispose()

$icoEntries = @()
foreach ($s in $APP_ICO) {
    $b = New-AppIcon $art $bounds $s
    $icoEntries += @{ Size = $s; Png = (Get-PngBytes $b) }
    $b.Dispose()
}
Save-Ico (Join-Path $root 'desktop\build\windows\icon.ico') $icoEntries
Write-Host "[图标] desktop\build\windows\icon.ico（$($APP_ICO -join '/')，共 $([math]::Round((Get-Item 'desktop\build\windows\icon.ico').Length/1KB,1)) KB）"

$icnsMap = @(
    @{ Type = 'ic11'; Size = 32 }, @{ Type = 'ic12'; Size = 64 },
    @{ Type = 'ic07'; Size = 128 }, @{ Type = 'ic13'; Size = 256 },
    @{ Type = 'ic08'; Size = 256 }, @{ Type = 'ic14'; Size = 512 },
    @{ Type = 'ic09'; Size = 512 }, @{ Type = 'ic10'; Size = 1024 }
)
$icnsEntries = @()
foreach ($m in $icnsMap) {
    $b = New-AppIcon $art $bounds $m.Size
    $icnsEntries += @{ Type = $m.Type; Png = (Get-PngBytes $b) }
    $b.Dispose()
}
Save-Icns (Join-Path $root 'desktop\build\darwin\icons.icns') $icnsEntries
Write-Host "[图标] desktop\build\darwin\icons.icns（8 个尺寸，$([math]::Round((Get-Item 'desktop\build\darwin\icons.icns').Length/1KB,1)) KB）"

$linux512 = New-AppIcon $art $bounds 512
Save-Png $linux512 (Join-Path $root 'desktop\build\linux\appicon.png')
$linux512.Dispose()
foreach ($s in $LINUX) {
    $b = New-AppIcon $art $bounds $s
    Save-Png $b (Join-Path $root "desktop\build\linux\icons\hicolor\${s}x${s}\apps\com.murongruolan.picohop.png")
    $b.Dispose()
}
Write-Host "[图标] desktop\build\linux\（appicon.png + hicolor $($LINUX -join '/')）"

# 2) 托盘三种底色
$states = [ordered]@{
    normal = @{ Hex = '#FFFFFF'; Label = '白底（平时）' }
    global = @{ Hex = '#3E1F08'; Label = '深棕 #3E1F08（全局代理模式）' }
    tun    = @{ Hex = '#240046'; Label = '深紫 #240046（预留：TUN）' }
}
$traySizes = @(16, 22, 32, 44, 64)
$trayIcoSizes = @(16, 20, 24, 32, 40, 48, 64)
$trayDir = Join-Path $root 'desktop\build\icons\tray'
foreach ($k in $states.Keys) {
    $bg = New-Argb $states[$k].Hex
    foreach ($s in $traySizes) {
        $b = New-TrayIcon $art $bounds $s $bg
        Save-Png $b (Join-Path $trayDir "tray-$k-$s.png")
        $b.Dispose()
    }
    $ents = @()
    foreach ($s in $trayIcoSizes) {
        $b = New-TrayIcon $art $bounds $s $bg
        $ents += @{ Size = $s; Png = (Get-PngBytes $b) }
        $b.Dispose()
    }
    Save-Ico (Join-Path $trayDir "tray-$k.ico") $ents
    Write-Host "[托盘] tray-$k：PNG $($traySizes -join '/') + ICO $($trayIcoSizes -join '/')  $($states[$k].Hex)"
}

# 3) 核对用预览图（只在 -Preview 时出，落在 .cache\ 下，不入库）
if ($Preview) {
    $W = 1100; $H = 690
    $sheet = New-Canvas $W $H
    $g = $sheet.Graphics
    $g.Clear([System.Drawing.Color]::FromArgb(255, 246, 247, 249))
    $font = New-Object System.Drawing.Font 'Segoe UI', 11
    $small = New-Object System.Drawing.Font 'Segoe UI', 8.5
    $ink = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 30, 30, 30))
    $inkLight = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 230, 230, 230))

    # --- 应用图标 ---
    $g.DrawString('应用图标（Windows icon.ico / macOS icons.icns / Linux PNG）', $font, $ink, 24, 12)
    $x = 24
    foreach ($s in @(256, 128, 64, 48, 32, 24, 16)) {
        $b = New-AppIcon $art $bounds $s
        $g.DrawImage($b, $x, 42, $s, $s)
        $g.DrawString("$s", $small, $ink, $x, (42 + $s + 3))
        $x += $s + 22
        $b.Dispose()
    }

    # --- 托盘图标：每种底色一行，左边浅色任务栏、右边深色任务栏，末尾附 96px 细节 ---
    $g.DrawString('托盘图标：每种底色各一行（左）浅色任务栏背景 /（右）深色任务栏背景，末尾是 96 px 细节', $font, $ink, 24, 322)
    $sizes = @(16, 20, 22, 24, 32, 48, 64)
    $rowY = 352
    foreach ($k in $states.Keys) {
        $bgc = New-Argb $states[$k].Hex
        $g.DrawString($states[$k].Label, $small, $ink, 24, ($rowY + 40))
        # 浅色条
        $sb = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 243, 243, 243))
        $g.FillRectangle($sb, 132, $rowY, 400, 100); $sb.Dispose()
        $tx = 142
        foreach ($s in $sizes) {
            $b = New-TrayIcon $art $bounds $s $bgc
            $g.DrawImage($b, $tx, ($rowY + (100 - $s) / 2), $s, $s)
            $tx += $s + 10
            $b.Dispose()
        }
        $b = New-TrayIcon $art $bounds 96 $bgc
        $g.DrawImage($b, ($tx + 4), ($rowY + 2), 96, 96); $b.Dispose()
        # 深色条
        $sb = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 31, 31, 31))
        $g.FillRectangle($sb, 560, $rowY, 400, 100); $sb.Dispose()
        $tx = 570
        foreach ($s in $sizes) {
            $b = New-TrayIcon $art $bounds $s $bgc
            $g.DrawImage($b, $tx, ($rowY + (100 - $s) / 2), $s, $s)
            $tx += $s + 10
            $b.Dispose()
        }
        $g.DrawString('（各尺寸均为实际像素）', $small, $inkLight, 966, ($rowY + 44))
        $rowY += 108
    }
    $font.Dispose(); $small.Dispose(); $ink.Dispose(); $inkLight.Dispose(); $g.Dispose()
    $prevDir = Join-Path $root '.cache'
    if (-not (Test-Path $prevDir)) { New-Item -ItemType Directory -Force $prevDir | Out-Null }
    Save-Png $sheet.Bitmap (Join-Path $prevDir 'icon-preview.png')
    $sheet.Bitmap.Dispose()
    Write-Host "[图标] 预览图 .cache\icon-preview.png"
}

# 4) 可选：exe 的文件图标（资源节 .syso）
#
# 【为什么必须有它】Windows 上 Wails v3 没有"运行期设置窗口图标"的 API
# （application.WindowsWindow 只有 DisableIcon，没有 Icon 字段；顶层 WebviewWindowOptions
# 也没有），标题栏/任务栏/资源管理器里那个图标**来自 exe 里的 RT_GROUP_ICON 资源**。
# 资源靠 Go 链接器自带的 .syso 机制塞进去：把 rsrc 生成的 .syso 放进包目录即可，
# 构建命令一个字都不用改（Go 会按 GOOS/GOARCH 自动挑 rsrc_windows_<arch>.syso）。
if ($Syso) {
    $rsrcPath = ''
    $cmd = Get-Command rsrc -ErrorAction SilentlyContinue
    if ($cmd) { $rsrcPath = $cmd.Source }
    if (-not $rsrcPath) {
        $local = Join-Path $env:GOPATH 'bin\rsrc.exe'
        if (Test-Path $local) { $rsrcPath = $local }
    }
    if (-not $rsrcPath) {
        Write-Host '[图标] 没找到 rsrc，跳过 .syso。装：go install github.com/akavel/rsrc@latest'
    } else {
        $ico = Join-Path $root 'desktop\build\windows\icon.ico'
        foreach ($arch in $Arches) {
            $out = Join-Path $root "desktop\rsrc_windows_$arch.syso"
            & $rsrcPath -ico $ico -arch $arch -o $out
            if ($LASTEXITCODE -ne 0 -or -not (Test-Path $out)) { throw "rsrc 生成失败（arch=$arch）" }
            Write-Host "[图标] desktop\rsrc_windows_$arch.syso  $([math]::Round((Get-Item $out).Length/1KB,1)) KB"
        }
    }
}

$art.Dispose()
Write-Host '[图标] 完成'
