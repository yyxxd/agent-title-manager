[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateSet('Prepare', 'Publish')][string]$Phase,
    [Parameter(Mandatory)][string]$Directory
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$releaseApi = "repos/$env:GITHUB_REPOSITORY/releases"
$tag = 'v0.4.1'
$releaseId = 452563468
$installerName = 'AgentTitleManager-0.4.1-windows-x64-setup.exe'
$installerHash = '3ca08eab96dfb69472dea0594d037b712a7c1a393d06387a2648c02107339eb6'
$originalHash = 'eb7351c4ac86b133b973430f040cacdf14e14b768398045fc20d5b6441444561'
$manifestName = 'update-manifest.json'
$backupName = 'update-manifest.v0.4.1.original.json'
$candidateName = 'update-manifest.v0.4.1.candidate.json'
$notesPath = Join-Path $Directory 'notes.md'

function Get-RepairRelease {
    $release = gh api "$releaseApi/tags/$tag" | ConvertFrom-Json
    if ($release.id -ne $releaseId -or $release.tag_name -cne $tag -or
        $release.draft -or $release.prerelease -or $release.immutable) {
        throw '目标不是已知的可编辑 v0.4.1 正式发布。'
    }
    $installer = $release.assets | Where-Object name -CEQ $installerName
    if ($installer.id -ne 771431687 -or $installer.size -ne 29719985 -or
        $installer.state -cne 'uploaded' -or $installer.digest -cne "sha256:$installerHash") {
        throw '已发布的 v0.4.1 安装包元数据与预期不一致。'
    }
    return $release
}

function Get-OriginalAsset($release) {
    $original = $release.assets | Where-Object id -EQ 771431694
    if ($original.name -cnotin @($manifestName, $backupName) -or
        $original.state -cne 'uploaded' -or $original.digest -cne "sha256:$originalHash") {
        throw '找不到已知的 v0.4.1 原清单或其备份。'
    }
    return $original
}

function Get-Sha256([string]$path) {
    (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Rename-ReleaseAsset($asset, [string]$name) {
    gh api --method PATCH "$releaseApi/assets/$($asset.id)" --raw-field "name=$name" | Out-Null
}

if ($Phase -eq 'Prepare') { New-Item -ItemType Directory -Path $Directory -Force | Out-Null }
& "$PSScriptRoot/read-release-notes.ps1" -Tag $tag -ExpectedCommit 'd69ab6c0055f6d266b84e3b30c5c730063b8bb83' `
    -ExpectedTagObject '5681137e966b75f91d91b43cdcaa3905665129b3' -OutputPath $notesPath | Out-Null
$release = Get-RepairRelease
$original = Get-OriginalAsset $release

if ($Phase -eq 'Prepare') {
    gh release download $tag --repo $env:GITHUB_REPOSITORY --pattern $installerName --dir $Directory
    $originalPath = Join-Path $Directory 'original.json'
    gh release download $tag --repo $env:GITHUB_REPOSITORY --pattern $original.name --output $originalPath
    if ((Get-Sha256 (Join-Path $Directory $installerName)) -cne $installerHash -or
        (Get-Sha256 $originalPath) -cne $originalHash) {
        throw '下载的安装包或原清单与已知 SHA-256 不一致。'
    }
    return
}

$candidatePath = Join-Path $Directory $candidateName
$candidateDigest = "sha256:$(Get-Sha256 $candidatePath)"
$manifest = $release.assets | Where-Object name -CEQ $manifestName
if ($manifest.state -cne 'uploaded' -or $manifest.digest -cne $candidateDigest) {
    if ($manifest -and $manifest.id -ne $original.id) {
        throw '当前清单是未知版本，停止替换。'
    }
    $candidate = $release.assets | Where-Object name -CEQ $candidateName
    if ($candidate.state -ceq 'starter' -and $candidate.size -eq 0) {
        # GitHub 上传失败可能留下空资产。只清理候选名下的空资产。
        gh api --method DELETE "$releaseApi/assets/$($candidate.id)"
        $candidate = $null
    }
    if (-not $candidate) {
        gh release upload $tag --repo $env:GITHUB_REPOSITORY $candidatePath
        $release = Get-RepairRelease
        $candidate = $release.assets | Where-Object name -CEQ $candidateName
    }
    if ($candidate.state -cne 'uploaded' -or $candidate.digest -cne $candidateDigest) {
        throw '候选清单资产的 SHA-256 与签名输出不一致。'
    }
    try {
        if ($original.name -ceq $manifestName) { Rename-ReleaseAsset $original $backupName }
        Rename-ReleaseAsset $candidate $manifestName
    } catch {
        $failure = $_
        $release = Get-RepairRelease
        if ($manifestName -cnotin $release.assets.name) {
            Rename-ReleaseAsset (Get-OriginalAsset $release) $manifestName
        }
        throw $failure
    }
}

$release = Get-RepairRelease
$original = Get-OriginalAsset $release
$manifest = $release.assets | Where-Object name -CEQ $manifestName
if ($original.name -cne $backupName -or $manifest.state -cne 'uploaded' -or $manifest.digest -cne $candidateDigest) {
    throw '正式清单或原清单备份回读失败。'
}
$notes = [IO.File]::ReadAllText($notesPath)
if ($release.body -cne $notes) {
    gh api --method PATCH "$releaseApi/$releaseId" --field "body=@$notesPath" | Out-Null
}
if ((Get-RepairRelease).body -cne $notes) { throw 'GitHub Release 中文正文回读失败。' }
