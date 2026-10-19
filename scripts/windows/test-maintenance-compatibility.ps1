#Requires -Version 7.0
[CmdletBinding()]
param()
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
# Unique workspace-owned directory; retained with exact fixture provenance.
$stage = Join-Path $root ('.dev/maintenance-compatibility/' + [guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Force -Path $stage
$fixtureSource = Get-Content -LiteralPath (Join-Path $root 'internal/ipc/testdata/maintenance/main.go.txt') -Raw
$fixtures = [Collections.Generic.List[object]]::new()
function Build-Fixture([string]$Name, [string]$Source, [bool]$Maintenance, [string]$Revision) {
    $cmdDir = Join-Path $Source 'cmd/maintenance-compat-fixture'
    $null = New-Item -ItemType Directory -Force -Path $cmdDir
    $sourceText = $fixtureSource
    if ($Maintenance) { $sourceText = $sourceText.Replace('// MAINTENANCE_HOOK', 'server.PrepareMaintenanceShutdown = prepare') }
    [IO.File]::WriteAllText((Join-Path $cmdDir 'main.go'), $sourceText)
    $binary = Join-Path $stage "$Name.exe"
    Push-Location $Source
    try {
        & go build -o $binary ./cmd/maintenance-compat-fixture
        if ($LASTEXITCODE) { throw "Fixture build failed: $Name" }
    } finally { Pop-Location }
    $fixtures.Add([ordered]@{ name = $Name; path = $binary; revision = $Revision; sha256 = (Get-FileHash -LiteralPath $binary).Hash })
}
Push-Location $root
$previousFixtures = $env:FNCPN_MAINTENANCE_FIXTURES
try {
    foreach ($tag in @('v1.0.7', 'v1.0.8', 'v1.0.9', 'v1.0.10')) {
        $revision = (& git rev-parse --verify "$tag^{commit}").Trim()
        if ($LASTEXITCODE) { throw "Required historical tag missing: $tag; fetch repository tags before validation." }
        $archive = Join-Path $stage "$tag.zip"
        & git archive --format=zip -o $archive $revision
        if ($LASTEXITCODE) { throw "Archive failed: $tag" }
        $source = Join-Path $stage $tag
        Expand-Archive -LiteralPath $archive -DestinationPath $source
        Build-Fixture $tag $source $false $revision
    }
    # The pre-maintenance IPC 5 candidate differs from v1.0.10 IPC only in
    # ProtocolVersion (verified against 10f5512). Recreate that fixed adapter
    # without depending on an unpublished feature-branch commit staying reachable.
    $legacy5 = Join-Path $stage 'legacy-ipc5-source'
    Expand-Archive -LiteralPath (Join-Path $stage 'v1.0.10.zip') -DestinationPath $legacy5
    $legacyProtocolPath = Join-Path $legacy5 'internal/ipc/protocol.go'
    $legacyProtocol = Get-Content -LiteralPath $legacyProtocolPath -Raw
    if ($legacyProtocol -notmatch 'const ProtocolVersion = 4') { throw 'Historical IPC 4 source changed' }
    [IO.File]::WriteAllText($legacyProtocolPath, ($legacyProtocol -replace 'const ProtocolVersion = 4', 'const ProtocolVersion = 5'))
    [IO.File]::WriteAllText((Join-Path $legacy5 'internal/version/VERSION'), '1.1.0')
    Build-Fixture 'legacy-ipc5' $legacy5 $false 'v1.0.10 IPC with version 5; matches pre-maintenance 10f5512 IPC'
    $future = Join-Path $stage 'future-source'
    foreach ($file in @(& git ls-files --cached --others --exclude-standard -- internal go.mod go.sum)) {
        $target = Join-Path $future $file
        $null = New-Item -ItemType Directory -Force -Path (Split-Path -Parent $target)
        Copy-Item -LiteralPath (Join-Path $root $file) -Destination $target
    }
    Build-Fixture 'current-maintenance' $future $true 'working-tree'
    $protocolPath = Join-Path $future 'internal/ipc/protocol.go'
    $protocol = Get-Content -LiteralPath $protocolPath -Raw
    if ($protocol -notmatch 'const ProtocolVersion = [0-9]+') { throw 'Cannot construct future business IPC fixture' }
    [IO.File]::WriteAllText($protocolPath, ($protocol -replace 'const ProtocolVersion = [0-9]+', 'const ProtocolVersion = 99'))
    [IO.File]::WriteAllText((Join-Path $future 'internal/version/VERSION'), '9.0.0')
    Build-Fixture 'future-ipc-99' $future $true 'working-tree with business IPC 99 and product 9.0.0'
    $manifest = Join-Path $stage 'fixtures.json'
    [IO.File]::WriteAllText($manifest, (ConvertTo-Json -InputObject @($fixtures.ToArray()) -Depth 4))
    $env:FNCPN_MAINTENANCE_FIXTURES = $manifest
    foreach ($callerSource in @($root, $future)) {
        Push-Location $callerSource
        try {
            & go test ./internal/ipc -run '^TestMaintenanceHistoricalProcesses$' -count=1 -v
            if ($LASTEXITCODE) { throw "Maintenance compatibility failed ($callerSource); fixtures retained at $stage" }
        } finally { Pop-Location }
    }
    Write-Host "Maintenance historical/future compatibility passed; provenance: $manifest"
} finally {
    $env:FNCPN_MAINTENANCE_FIXTURES = $previousFixtures
    Pop-Location
}
