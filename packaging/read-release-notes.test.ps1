$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$root = Join-Path ([IO.Path]::GetTempPath()) "atm-release-notes-$([Guid]::NewGuid().ToString('N'))"
$remote = Join-Path $root 'remote.git'
$producer = Join-Path $root 'producer'
$runner = Join-Path $root 'runner'
$notesFile = Join-Path $root 'notes.md'
$output = Join-Path $root 'output.md'
$readNotes = Join-Path $PSScriptRoot 'read-release-notes.ps1'
$notes = @(
    '# 简短标题更少超长失败'
    '## 新增'
    '- **更新说明**：显示中文，并保留 `#`。'
    '## 修复'
    '- **回归修复**：保证标签说明与安装包版本一致。'
) -join "`n"

function Assert-Rejected([string]$tag, [string]$expectedCommit, [string]$reason) {
    $failure = $null
    try {
        & $readNotes -Tag $tag -ExpectedCommit $expectedCommit -OutputPath $output | Out-Null
    } catch {
        $failure = $_.Exception.Message
    }
    if (-not $failure -or -not $failure.Contains($reason) -or (Test-Path -LiteralPath $output)) {
        throw "$tag 未按预期失败（$reason）。实际异常：$failure"
    }
}

New-Item -ItemType Directory -Path $root | Out-Null
try {
    git init --bare --initial-branch=main $remote
    git init --initial-branch=main $producer
    git -C $producer config user.name 'Release notes fixture'
    git -C $producer config user.email 'test@example.invalid'
    git -C $producer -c commit.gpgSign=false commit --allow-empty -m 'first commit'
    $oldCommit = git -C $producer rev-parse HEAD
    git -C $producer -c commit.gpgSign=false commit --allow-empty -m 'English checkout commit'
    $commit = git -C $producer rev-parse HEAD
    [IO.File]::WriteAllText($notesFile, "$notes`n")
    git -C $producer -c tag.gpgSign=false tag -a v1.0.0 --cleanup=verbatim -F $notesFile
    $tagObject = git -C $producer rev-parse refs/tags/v1.0.0
    git -C $producer -c tag.gpgSign=false tag -a v1.0.1 --cleanup=verbatim -F $notesFile $oldCommit
    git -C $producer -c tag.gpgSign=false tag v1.0.2
    git -C $producer -c tag.gpgSign=false tag -a v1.0.3 --cleanup=verbatim -m '# 缺少说明分类'
    git -C $producer -c tag.gpgSign=false tag -a v1.0.4 -m 'English commit log'
    git -C $producer -c tag.gpgSign=false tag -a v1.0.5 --cleanup=verbatim -m "# 中文主题`n## 未知分类"
    git -C $producer push $remote main --tags
    git clone --no-tags --depth=1 ([uri]::new($remote).AbsoluteUri) $runner
    git -C $runner fetch origin "+${commit}:refs/tags/v1.0.0"
    Push-Location $runner
    try {
        $result = & $readNotes -Tag v1.0.0 -ExpectedCommit $commit -OutputPath $output -ExpectedTagObject $tagObject
        if ($result -cne $tagObject -or [IO.File]::ReadAllText($output) -cne $notes -or
            (git cat-file -t refs/tags/v1.0.0) -cne 'commit') {
            throw '必须从远端取得原中文正文，并保持本地 checkout 标签原状。'
        }
        Remove-Item -LiteralPath $output
        Assert-Rejected v1.0.1 $commit '预期构建提交'
        Assert-Rejected v1.0.2 $commit 'annotated tag'
        Assert-Rejected v1.0.3 $commit '按规定分类'
        Assert-Rejected v1.0.4 $commit '中文版本主题标题'
        Assert-Rejected v1.0.5 $commit '未知、重复、错序或空'
        if (git for-each-ref refs/atm-release/notes/) { throw '临时 ref 未清理。' }
    } finally {
        Pop-Location
    }
    Write-Output 'Remote annotated release note tests passed.'
} finally {
    $resolved = (Resolve-Path -LiteralPath $root).Path
    if ($resolved -cne [IO.Path]::GetFullPath($root)) { throw "意外的测试目录：$resolved" }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
