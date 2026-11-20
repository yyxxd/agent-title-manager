[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidatePattern('\Av(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\z')][string]$Tag,
    [Parameter(Mandatory)][ValidatePattern('\A[0-9a-f]{40}\z')][string]$ExpectedCommit,
    [Parameter(Mandatory)][string]$OutputPath,
    [ValidatePattern('\A[0-9a-f]{40}\z')][string]$ExpectedTagObject
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$originalEncoding = [Console]::OutputEncoding
$tagRef = "refs/atm-release/notes/$([Guid]::NewGuid().ToString('N'))"

try {
    [Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
    # checkout 可能已把本地同名标签设成提交。仅从 origin 读取标签对象。
    git fetch --no-tags --no-write-fetch-head origin "refs/tags/${Tag}:$tagRef"
    if ((git cat-file -t $tagRef) -cne 'tag') { throw "$Tag 必须是 annotated tag。" }
    $tagObject = git rev-parse --verify $tagRef
    if ($ExpectedTagObject -and $tagObject -cne $ExpectedTagObject) {
        throw "$Tag 的对象与预期不一致。"
    }

    $tagData = (git cat-file tag $tagRef) -join "`n"
    $headers, $notes = $tagData -csplit "`n`n", 2
    if (-not "$headers`n".StartsWith("object $ExpectedCommit`ntype commit`ntag $Tag`n", [StringComparison]::Ordinal)) {
        throw "$Tag 必须直接指向预期构建提交 $ExpectedCommit。"
    }
    $signature = ((git for-each-ref --format='%(contents:signature)' $tagRef) -join "`n").TrimEnd("`n")
    $notes = ([string]$notes).TrimEnd("`n")
    if ($signature -and $notes.EndsWith($signature, [StringComparison]::Ordinal)) {
        $notes = $notes.Substring(0, $notes.Length - $signature.Length).TrimEnd("`n")
    }

    $title, $lines = $notes -csplit "`n", 2
    if ($title -cnotmatch '^# \S.*$' -or $title -cnotmatch '\p{IsCJKUnifiedIdeographs}') {
        throw "$Tag 缺少中文版本主题标题（# 主题）。"
    }
    $categories = @('重要变更', '新增', '修复', '优化')
    $previousCategory = -1
    $hasEntry = $false
    foreach ($line in ($lines -csplit "`n")) {
        if ($line -cmatch '^## (.+)$') {
            $category = [Array]::IndexOf($categories, $Matches[1])
            if ($category -le $previousCategory -or ($previousCategory -ge 0 -and -not $hasEntry)) {
                throw "$Tag 包含未知、重复、错序或空的说明分类。"
            }
            $previousCategory = $category
            $hasEntry = $false
        } elseif ($previousCategory -ge 0 -and $line -cmatch '^- \*\*\S.*?\*\*：\S.*$' -and $line -cmatch '\p{IsCJKUnifiedIdeographs}') {
            $hasEntry = $true
        } else {
            throw "$Tag 的说明必须按规定分类，条目使用 - **要点**：说明。"
        }
    }
    if (-not $hasEntry) { throw "$Tag 缺少带条目的说明分类。" }

    [IO.File]::WriteAllText($OutputPath, $notes, [Text.UTF8Encoding]::new($false))
    $tagObject
} finally {
    [Console]::OutputEncoding = $originalEncoding
    git update-ref -d $tagRef
}
