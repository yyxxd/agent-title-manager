[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$NodeDistribution,
    [string]$MakeNsis = 'makensis.exe',
    [string]$Version,
    [string]$UpdatePublicKey = '',
    [switch]$NoInstaller
)

$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
if (-not $PSBoundParameters.ContainsKey('Version')) {
    $Version = (Get-Content -LiteralPath (Join-Path $root 'internal\desktop\version.txt') -Raw).Trim()
}
if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw 'Version must use X.Y.Z.' }
$node = (Resolve-Path -LiteralPath $NodeDistribution).Path
foreach ($name in @('node.exe', 'LICENSE')) {
    if (-not (Test-Path -LiteralPath (Join-Path $node $name) -PathType Leaf)) {
        throw "The official Node Windows x64 distribution must contain $name."
    }
}

function Invoke-Checked {
    param([string]$Program, [string[]]$Arguments)
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Program exited with code $LASTEXITCODE." }
}

Push-Location $root
try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    $stage = Join-Path $root "out\windows-x64-$Version-$([guid]::NewGuid().ToString('N').Substring(0, 8))"
    New-Item -ItemType Directory -Path (Join-Path $stage 'plugins') -Force | Out-Null
    Invoke-Checked 'go' @('run', './packaging/windows-icon')
    Invoke-Checked 'pnpm' @('--dir', 'frontend', 'install', '--frozen-lockfile')
    Invoke-Checked 'pnpm' @('--dir', 'frontend', 'build')
    Invoke-Checked 'go' @('test', './...')
    $desktopLinkFlags = "-s -w -H windowsgui -X github.com/yyxxd/agent-title-manager/internal/desktop.buildVersion=$Version -X github.com/yyxxd/agent-title-manager/internal/desktop.updatePublicKey=$UpdatePublicKey"
    Invoke-Checked 'go' @('build', '-trimpath', '-tags', 'production', '-ldflags', $desktopLinkFlags, '-o', (Join-Path $stage 'AgentTitleManager.exe'), './cmd/atm')
    Invoke-Checked 'go' @('build', '-trimpath', '-ldflags', '-s -w', '-o', (Join-Path $stage 'atm-bridge.exe'), './cmd/atm-bridge')
    # Keep the helper trust root in internal/update, where atm-update calls it.
    $helperLinkFlags = "-s -w -X github.com/yyxxd/agent-title-manager/internal/update.updatePublicKey=$UpdatePublicKey"
    Invoke-Checked 'go' @('build', '-trimpath', '-ldflags', $helperLinkFlags, '-o', (Join-Path $stage 'atm-update.exe'), './cmd/atm-update')
    foreach ($id in @('codex', 'claude', 'pi', 'antigravity', 'example')) {
        Copy-Item -LiteralPath "plugins\$id" -Destination "$stage\plugins\$id" -Recurse
    }
    Copy-Item -LiteralPath "$node\node.exe" -Destination "$stage\plugins\node.exe"
    Copy-Item -LiteralPath "$node\LICENSE" -Destination "$stage\plugins\NODE-LICENSE.txt"
    Copy-Item -LiteralPath 'LICENSE' -Destination "$stage\LICENSE.txt"
    Copy-Item -LiteralPath 'internal\engine\LICENSE.naming' -Destination "$stage\OIL-CODEX-TITLE-LICENSE.txt"
    Copy-Item -LiteralPath 'plugins\THIRD_PARTY_NOTICES.md' -Destination "$stage\plugins\THIRD_PARTY_NOTICES.md"
    if (-not $NoInstaller) {
        $installer = Join-Path $root "out\AgentTitleManager-$Version-windows-x64-setup.exe"
        Invoke-Checked $MakeNsis @('/INPUTCHARSET', 'UTF8', "/DPAYLOAD=$stage", "/DOUTPUT=$installer", "/DVERSION=$Version", 'packaging\installer.nsi')
        Get-FileHash -Algorithm SHA256 -LiteralPath $installer
    }
    Write-Output "Windows bundle: $stage"
} finally {
    Pop-Location
}
