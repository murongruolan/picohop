# 生成 / 更新仓库根的 THIRD_PARTY_NOTICES.md。
#
# 用法：
#   .\scripts\gen_notices.ps1
#
# 【为什么要有个脚本，而不是手写一份声明】这份文件必须反映**真实的依赖集合**：
# 加一个依赖、换一个版本，声明就可能过期。手写的清单迟早与现实分叉。
#
# 【两个关键实现细节，改之前先读】
#   1. 模块集合用 `go list -deps ./desktop ./cmd/picohop` —— 即**真正链进二进制**的模块，
#      不是 `go mod graph` 那种全量依赖图（那里面有一大堆只用于构建/测试、根本不进产物的
#      模块，把它们写进声明是噪音）。
#   2. 许可证分类**必须大小写敏感**（-cmatch）。GPLv3 正文第 13 节提到
#      "GNU Affero General Public License"（混合大小写），用默认的大小写不敏感匹配会把
#      **所有 GPLv3 误判成 AGPL** —— 这个 bug 真发生过，连本项目自己的 LICENSE 都被误判。
#
# 【去重只影响"贴几份正文"，不影响贴出来的内容】同一许可证各家版权行不同、标题写法
# （"MIT License" / "The MIT License (MIT)" / 无标题）与折行也不同。所以：
#   - 去重用的 key 是"去掉版权行/标题行/装饰线并压缩空白"之后的正文；
#   - 真正贴进文件的是**原文**（最长的那份可能含标题，故注明"取自其中一个组件"）。
#
# 分类遇到无法识别的许可文本会直接抛错，绝不放行。

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
. "$PSScriptRoot\env.ps1" | Out-Null

function Classify-Text([string]$t) {
    if ($t -cmatch 'GNU AFFERO GENERAL PUBLIC LICENSE') { return 'AGPL-3.0' }
    if ($t -cmatch 'GNU LESSER GENERAL PUBLIC LICENSE') { return 'LGPL-3.0' }
    if ($t -cmatch 'GNU GENERAL PUBLIC LICENSE') {
        if ($t -cmatch 'Version 3, 29 June 2007') { return 'GPL-3.0' }
        if ($t -cmatch 'Version 2, June 1991') { return 'GPL-2.0' }
        return 'GPL'
    }
    if ($t -cmatch 'either version 3 of the License, or' -and $t -cmatch 'This program is free software') { return 'GPL-3.0-or-later' }
    if ($t -cmatch 'Mozilla Public License') { return 'MPL-2.0' }
    if ($t -cmatch 'Eclipse Public License') { return 'EPL' }
    if ($t -cmatch 'Apache License' -and $t -cmatch 'Version 2\.0') { return 'Apache-2.0' }
    if ($t -cmatch 'BSD 3-Clause License') { return 'BSD-3-Clause' }
    if ($t -cmatch 'BSD 2-Clause License') { return 'BSD-2-Clause' }
    if ($t -cmatch 'Redistribution and use in source and binary forms') {
        if ($t -cmatch 'Neither the name') { return 'BSD-3-Clause' }
        return 'BSD-2-Clause'
    }
    if ($t -cmatch 'Permission is hereby granted, free of charge') { return 'MIT' }
    if ($t -cmatch 'Permission to use, copy, modify, and/or distribute this software') { return 'ISC' }
    if ($t -cmatch 'Permission to use, copy, modify, and distribute this software for any') { return 'ISC' }
    if ($t -cmatch 'CC0 1\.0') { return 'CC0-1.0' }
    if ($t -cmatch 'unencumbered software released into the public domain') { return 'Unlicense' }
    return 'UNKNOWN'
}

# 严格程度排序：声明里按这个顺序排，copyleft 排在最前面（先看最要紧的）
$rank = @{ 'AGPL-3.0' = 0; 'GPL-3.0' = 1; 'GPL-3.0-or-later' = 1; 'GPL-2.0' = 2; 'GPL' = 2
           'LGPL-3.0' = 3; 'EPL' = 4; 'MPL-2.0' = 5; 'UNKNOWN' = 6; 'Apache-2.0' = 7
           'BSD-3-Clause' = 8; 'BSD-2-Clause' = 9; 'MIT' = 10; 'ISC' = 11; 'CC0-1.0' = 12; 'Unlicense' = 13 }
# 这些许可要求"在分发物里复现版权声明"，所以必须逐条抄进表格
$permissive = @('MIT', 'BSD-3-Clause', 'BSD-2-Clause', 'ISC', 'CC0-1.0', 'Unlicense')

# 版权行只在文件**开头连续的版权块**里取。为什么不能全文正则匹配：BSD 正文里有一句
# "…the above copyright notice, this list of conditions…"，全文匹配会把它当成版权行
# 抄进表格（第一版就这么错过一次）。
function Get-CopyLines([string]$t) {
    $lines = @($t -split "`r?`n")
    $out = @()
    $started = $false
    $limit = [Math]::Min($lines.Count, 20)
    for ($i = 0; $i -lt $limit; $i++) {
        $l = $lines[$i]
        if ($l -match '(?i)^\s*(copyright|\(c\)|©)') {
            $started = $true
            $clean = ($l -replace '\s+', ' ').Trim()
            if ($clean -and ($out -notcontains $clean)) { $out += $clean }
            continue
        }
        if ($started) { break }
    }
    return $out
}

# 有些项目（zx2c4 的两个）的 LICENSE 里根本没有版权行，版权写在源码头上 —— 回退去那里找
function Get-CopyFromHeaders([string]$dir) {
    $files = @(Get-ChildItem -LiteralPath $dir -Recurse -Filter *.go -File -ErrorAction SilentlyContinue |
        Select-Object -First 40)
    foreach ($f in $files) {
        foreach ($l in @(Get-Content -LiteralPath $f.FullName -TotalCount 15 -ErrorAction SilentlyContinue)) {
            if ($l -match '(?i)copyright') {
                $clean = ($l -replace '^\s*(//+|/\*+|\*+)\s*', '' -replace '\*+/\s*$', '' -replace '\s+', ' ').Trim()
                if ($clean) { return $clean }
            }
        }
    }
    return ''
}

function Get-ModInfo([string]$dir) {
    $files = @()
    if ($dir -and (Test-Path -LiteralPath $dir)) {
        $files = @(Get-ChildItem -LiteralPath $dir -File -ErrorAction SilentlyContinue |
            Where-Object { $_.Name -match '^(LICEN[CS]E|COPYING|UNLICENSE|NOTICE)' })
    }
    $kinds = @(); $copy = @(); $texts = @{}; $notices = @()
    foreach ($f in $files) {
        if ($f.Name -match 'NOTICE') { $notices += (Get-Content -LiteralPath $f.FullName -Raw); continue }
        $t = Get-Content -LiteralPath $f.FullName -Raw -ErrorAction SilentlyContinue
        if (-not $t) { continue }
        $k = Classify-Text $t
        if ($k -eq 'UNKNOWN') { throw "无法识别的许可证文本：$dir\$($f.Name)" }
        if ($kinds -notcontains $k) { $kinds += $k }
        $body = (($t -split "`r?`n" | Where-Object {
            $_ -notmatch '(?i)^\s*(copyright|\(c\)|©)' -and
            $_ -notmatch '(?i)^\s*(the\s+)?(mit|bsd|isc|apache|mozilla|mpl|gnu|unlicense|cc0)\b.*licen[cs]e' -and
            $_ -notmatch '(?i)^\s*all rights reserved\.?\s*$' -and
            $_ -notmatch '^\s*[=\-*]{3,}\s*$'
        }) -join ' ') -replace '\s+', ' '
        $bh = [BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash(
            [Text.Encoding]::UTF8.GetBytes($body.Trim()))).Replace('-', '')
        if (-not $texts.ContainsKey($k)) { $texts[$k] = @{} }
        if (-not $texts[$k].ContainsKey($bh)) { $texts[$k][$bh] = $t }
        if ($k -in $permissive) {
            foreach ($c in (Get-CopyLines $t)) { if ($copy -notcontains $c) { $copy += $c } }
        }
    }
    if (($kinds | Where-Object { $_ -in $permissive }).Count -gt 0 -and $copy.Count -eq 0) {
        $fb = Get-CopyFromHeaders $dir
        if ($fb) { Write-Host "    [版权回退到源码头] $dir"; $copy += $fb }
    }
    return @{ Kinds = @($kinds | Sort-Object { $rank[$_] }); Copy = $copy; Texts = $texts; Notices = $notices }
}

# ---------------------------------------------------------------- 收集
$linkedPaths = @(& go list -deps -f '{{if .Module}}{{.Module.Path}}{{end}}' ./desktop ./cmd/picohop 2>$null |
    Where-Object { $_ } | Sort-Object -Unique)
$modTable = @{}
foreach ($line in (& go list -m -f '{{.Path}}|{{.Version}}|{{.Dir}}' all 2>$null)) {
    $p = $line -split '\|', 3
    if ($p.Count -ge 3) { $modTable[$p[0]] = @{ Version = $p[1]; Dir = $p[2] } }
}

$rows = @(); $kindCount = @{}; $variantText = @{}; $allNotices = @{}
foreach ($path in $linkedPaths) {
    if ($path -eq 'github.com/murongruolan/picohop') { continue }
    $dir = if ($modTable.ContainsKey($path)) { $modTable[$path].Dir } else { '' }
    $mi = Get-ModInfo $dir
    $kindsTxt = if ($mi.Kinds.Count) { $mi.Kinds -join ' + ' } else { 'NO-LICENSE-FILE' }
    if ($mi.Kinds.Count) { foreach ($k in $mi.Kinds) { $kindCount[$k] = 1 + ($kindCount[$k] -as [int]) } }
    else { $kindCount['NO-LICENSE-FILE'] = 1 + ($kindCount['NO-LICENSE-FILE'] -as [int]) }
    foreach ($k in $mi.Kinds) {
        if (-not $variantText.ContainsKey($k)) { $variantText[$k] = @{} }
        foreach ($kv in $mi.Texts[$k].GetEnumerator()) { $variantText[$k][$kv.Key] = $kv.Value }
    }
    foreach ($n in $mi.Notices) { $allNotices[$n] = $true }
    $rows += [pscustomobject]@{ Path = $path; Kind = $kindsTxt
        Remark = $(if ($mi.Copy.Count) { $mi.Copy -join '；' } else { '' })
        Sort = (($mi.Kinds | ForEach-Object { $rank[$_] }) + 99 | Measure-Object -Minimum).Minimum }
}
$rows = @($rows | Sort-Object Sort, Path)

# ---------------------------------------------------------------- 组装
$L = [System.Collections.Generic.List[string]]::new()
$L.Add('# 第三方组件声明（THIRD_PARTY_NOTICES）')
$L.Add('')
$L.Add('> 本文件由 `.\scripts\gen_notices.ps1` **生成**，不要手改 —— 加了依赖或换了版本之后重跑它。')
$L.Add('')
$L.Add('本项目的**自有代码**以 [GPL-3.0](LICENSE) 发布（原因见 [README 的「许可证」一节](README.md#许可证)）。')
$L.Add('它静态链接了下列第三方组件 —— 这份文件就是为了满足它们的许可要求而存在：')
$L.Add('')
$L.Add('- **MIT / BSD / ISC**：必须在分发物里复现版权声明与许可全文 → 表格里的版权行 + 下文的许可全文；')
$L.Add('- **Apache-2.0**：同上；若组件带 NOTICE 文件，NOTICE 也必须一并复现 → 见「NOTICE 原文」；')
$L.Add('- **GPL 家族**：整个作品受其约束（所以本项目是 GPL-3.0），并须提供对应源码 → 见文末；')
$L.Add('- **LGPL-3.0 / MPL-2.0**：只约束该组件自身，不要求整个作品同许可 → 见文末说明。')
$L.Add('')
$L.Add('模块清单由 `go list -deps ./desktop ./cmd/picohop` 生成 —— 即**真正链进二进制**的那些，')
$L.Add('不是 `go.mod` 里的全量依赖图。精确版本钉在 `go.mod` / `go.sum` 里，此处不重复列出。')
$L.Add('')
$L.Add('## 一、组件清单')
$L.Add('')
$L.Add('| 组件 | 许可证 | 版权声明（按许可要求复现） |')
$L.Add('|---|---|---|')
foreach ($r in $rows) {
    $rk = if ($r.Remark) { $r.Remark } else { '—' }
    $L.Add("| ``$($r.Path)`` | $($r.Kind) | $rk |")
}
$L.Add('')
$L.Add('按类别统计（同时带多种许可的组件在各类里各计一次，所以合计大于组件数）：')
$L.Add('')
$L.Add('| 许可证 | 组件数 |')
$L.Add('|---|---|')
foreach ($kv in ($kindCount.GetEnumerator() | Sort-Object { $rank[$_.Key] })) {
    $L.Add("| $($kv.Key) | $($kv.Value) |")
}
$L.Add('')
$L.Add('## 二、许可全文')
$L.Add('')
$L.Add('同一许可证的正文在各组件间只有版权行与标题写法不同（版权行已在上表逐条列出），因此每种只贴一份。')
$L.Add('**GPL-3.0 不在此重复**：本项目的 [LICENSE](LICENSE) 就是它的全文；')
$L.Add('**GPL-3.0-or-later 也不重复**：它引用同一份 GPLv3 正文，只是允许升级到更高版本。')
$L.Add('')
$order = @('MIT', 'BSD-3-Clause', 'BSD-2-Clause', 'ISC', 'Apache-2.0', 'MPL-2.0', 'LGPL-3.0', 'GPL-2.0', 'CC0-1.0', 'Unlicense')
foreach ($k in $order) {
    if (-not $variantText.ContainsKey($k)) { continue }
    $variants = @($variantText[$k].Values | Sort-Object Length)
    $L.Add("### $k")
    $L.Add('')
    $L.Add('下面这份取自其中一个组件（因此开头那行版权属于它）；**所有组件的版权行都在上表里逐条列出**。')
    if ($variants.Count -gt 1) { $L.Add("（去重后，该许可证在本项目里有 $($variants.Count) 种不同正文，下面全部贴出。）") }
    $L.Add('')
    $shown = 0
    foreach ($v in $variants) {
        if ($shown -ge 4) { $L.Add("（另有 $($variants.Count - 4) 种更长的变体未贴出。）"); break }
        $L.Add('```text')
        foreach ($ln in ($v -split "`r?`n")) { $L.Add($ln.TrimEnd()) }
        $L.Add('```')
        $L.Add('')
        $shown++
    }
}
$L.Add('## 三、NOTICE 原文')
$L.Add('')
$L.Add('带 NOTICE 的组件（按 Apache-2.0 与 MPL-2.0 的要求必须一并复现）：')
$L.Add('')
foreach ($n in $allNotices.Keys) {
    $L.Add('```text')
    foreach ($ln in ($n -split "`r?`n")) { $L.Add($ln.TrimEnd()) }
    $L.Add('```')
    $L.Add('')
}
$L.Add('## 四、需要如实说明的两处')
$L.Add('')
$L.Add('1. **`github.com/rasky/go-lzo`（GPL-2.0）措辞含糊。** 它的 `LICENSE.gpl` 是 GPLv2 全文，')
$L.Add('   README 只写 "shares the same license (GPLv2)"，**没有"or later"字样**。严格按文本读是')
$L.Add('   GPL-2.0-only，而 GPL-2.0-only 与 GPL-3.0 不兼容。但同一份 README 说明它是 LZO1X')
$L.Add('   **原版源码**的直译、与原版同许可，而原版是 2.0-or-later —— 因此实际意图是')
$L.Add('   2.0-or-later，与本项目兼容。这个模糊点是从 mihomo 继承来的，不是本项目引入的。')
$L.Add('2. **`github.com/metacubex/chacha` 没有任何许可证文件**（其模块根目录只有 `.gitignore` /')
$L.Add('   `chacha.go` / `go.mod` / `go.sum`）。严格说这意味着没有授予再分发权利。它是 mihomo 的')
$L.Add('   依赖、随 mihomo 一起以 GPL 发布，实际风险很低，但如实记录在此。')
$L.Add('')
$L.Add('## 五、LGPL-3.0 与 MPL-2.0 的处理（为什么它们不改变本项目的许可）')
$L.Add('')
$L.Add('- **LGPL-3.0**（`easytier-go`）：LGPL 只要求**对该库自身的修改**继续以 LGPL 提供，并不要求')
$L.Add('  整个作品换许可。它的静态链接义务是"让接收者能够用修改过的版本重新链接"——本项目')
$L.Add('  提供全部源码与构建脚本，任何人都可以替换该依赖后重新构建，因此满足；')
$L.Add('  本项目**没有修改**该库的任何文件。')
$L.Add('- **MPL-2.0**（`mipstack` / `yamux` / `zerotier-go`）：MPL 是**文件级** copyleft，只约束那些')
$L.Add('  文件本身。本项目**没有修改**这些文件，因此只需保留其声明（本节与上表）。')
$L.Add('  另：MPL-2.0 与 GPL-3.0 兼容（其第 3.3 节的 Secondary License 机制）。')
$L.Add('')
$L.Add('## 六、对应源码与分发要求')
$L.Add('')
$L.Add('本项目及其全部依赖的源码都可得：本项目源码在仓库根目录；依赖的**精确版本**钉在')
$L.Add('`go.mod` / `go.sum`，按 [README 的构建一节](README.md#构建) 可重建出与发布产物一致的二进制。')
$L.Add('')
$L.Add('**发布二进制时，必须把 `LICENSE` 与本文件一并放进发布物**（GPL-3.0 要求"随附"许可文本，')
$L.Add('只放在仓库里对下载二进制的人不等于随附）。')
$L.Add('')

[IO.File]::WriteAllLines((Join-Path $root 'THIRD_PARTY_NOTICES.md'), $L, (New-Object Text.UTF8Encoding($false)))
$fi = Get-Item (Join-Path $root 'THIRD_PARTY_NOTICES.md')
Write-Host "OK  THIRD_PARTY_NOTICES.md"
Write-Host "组件 $($rows.Count) 个｜许可类别 $($kindCount.Count) 类｜NOTICE $($allNotices.Count) 份｜$([math]::Round($fi.Length/1KB,1)) KB｜$((Get-Content $fi.FullName).Count) 行"
