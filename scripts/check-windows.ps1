#Requires -Version 7.0
[CmdletBinding()]
param([ValidateSet('all', 'arm64', 'x64')][string]$Architecture = 'all', [string]$PackageRoot, [switch]$SkipPackage)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
function Invoke-Check([string]$File, [string[]]$Arguments) {
    & $File @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Check failed: $File" }
}
Push-Location $root
try {
    Invoke-Check 'python' @('scripts/sync-version.py', '--check')
    Invoke-Check 'python' @('scripts/sync-localizations.py', '--check')
    Invoke-Check 'python' @('scripts/sync-native-localizations.py', '--check')
    & "$PSScriptRoot/windows/check-localization.ps1"
    & "$PSScriptRoot/windows/test-setup-proxy.ps1"
    & "$PSScriptRoot/windows/test-setup-tools.ps1"
    Invoke-Check 'go' @('test', './...')
    Invoke-Check 'go' @('vet', './...')
    & "$PSScriptRoot/windows/test-maintenance-compatibility.ps1"
    if (-not $SkipPackage) {
        $hostArch = $env:PROCESSOR_ARCHITEW6432
        if (-not $hostArch) { $hostArch = $env:PROCESSOR_ARCHITECTURE }
        $targets = if ($Architecture -eq 'all') { @('arm64', 'x64') } else { @($Architecture) }
        foreach ($target in $targets) {
            if ($target -eq 'arm64' -and $hostArch -ne 'ARM64') {
                & "$PSScriptRoot/windows/test-msi-platform.ps1" -Architecture $target -PackageRoot $PackageRoot
                Write-Host 'ARM64 MSI conditions checked; UI execution requires an ARM64 host.'
            } else {
                & "$PSScriptRoot/windows/test-ui-smoke.ps1" -Architecture $target -PackageRoot $PackageRoot
            }
        }
    }
    Write-Host 'Windows checks passed.'
} finally { Pop-Location }
