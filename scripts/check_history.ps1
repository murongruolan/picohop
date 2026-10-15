# 提交前检查：COMMIT_HISTORY.md 的索引有没有跟上 git log。
#
# 【为什么要有它】028 那次补齐发现：索引在 023 之后积了 99 个提交没登记，而发现它靠的是
# "碰巧有人去数" —— 数的时候还把 99 数成了 90。文档漂移不该靠人记着。
#
# 做法：取最近 N 个提交的 "hash + subject"，逐个在 COMMIT_HISTORY.md 里找；
# 两者**任一**出现就算登记过；都找不到就报出来并非 0 退出。
#
# 【为什么是"任一"，不是只比 subject】索引里两种登记方式并存：
#   - 024 之后的行写的是提交标题（subject）；
#   - 023a–023f 这种合并行只保留 hash（一节里归并了 99 条，写不下标题）。
# 只比 subject 会把已经登记过的合并提交误报成"缺登记"—— 这个脚本第一版就踩了。
$ErrorActionPreference = 'Stop'

$count = 5
if ($args.Count -ge 1) { $count = [int]$args[0] }

$root = Split-Path -Parent $PSScriptRoot
$histPath = Join-Path $root 'docs\COMMIT_HISTORY.md'
if (-not (Test-Path -LiteralPath $histPath)) { throw "找不到 $histPath" }

$lines = @(& git -C $root log --format='%h%x09%s' -n $count)
if ($lines.Count -eq 0) { throw 'git log 没有输出' }

$text = [IO.File]::ReadAllText($histPath, [Text.Encoding]::UTF8)
$missing = @()
foreach ($line in $lines) {
    $parts = $line -split "`t", 2
    $h = $parts[0]
    $s = if ($parts.Count -gt 1) { $parts[1] } else { '' }
    if (-not ($text.Contains($s) -or $text.Contains($h))) {
        $missing += "$h  $s"
    }
}

Write-Host "[history] 检查最近 $($lines.Count) 个提交"
if ($missing.Count -eq 0) {
    Write-Host '[history] OK 都在索引里'
    exit 0
}

Write-Host '[history] 以下提交没有登记到 docs/COMMIT_HISTORY.md：'
foreach ($m in $missing) { Write-Host "  - $m" }
Write-Host ''
Write-Host '按该文档开头的维护约定，每次提交都要在索引里加一行并写详细章节。'
exit 1