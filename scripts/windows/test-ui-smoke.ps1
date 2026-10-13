#Requires -Version 7.0
[CmdletBinding()]
param([ValidateSet('arm64', 'x64')][string]$Architecture = 'arm64', [string]$PackageRoot)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$build = Join-Path $root ".dev/windows-ui-$Architecture"
if (-not $PackageRoot) { $PackageRoot = Join-Path $root 'dist/windows-native' }
$PackageRoot = [IO.Path]::GetFullPath($PackageRoot)
$payload = Join-Path $PackageRoot "$Architecture/payload"
& ctest --test-dir $build -C Release --output-on-failure
if ($LASTEXITCODE) { throw 'Native protocol and window regressions failed.' }
$ui = Join-Path $payload 'ui/FnCPN.UI.exe'
if (-not (Test-Path -LiteralPath $ui)) { throw "Build the native package first: $ui" }
$forbidden = @(Get-ChildItem -LiteralPath (Join-Path $payload 'ui') -Recurse -File | Where-Object {
    $_.Name -match '^(coreclr|clrjit|hostfxr|hostpolicy|PresentationFramework|System\.Windows\.Forms)' -or
    $_.Name -match '\.(deps|runtimeconfig)\.json$'
})
if ($forbidden.Count) { throw "Native UI contains managed runtime payload: $($forbidden.Name -join ', ')" }
$version = (Get-Content (Join-Path $root 'internal/version/VERSION') -Raw).Trim()
if ((Get-Item -LiteralPath $ui).VersionInfo.ProductVersion -ne $version) { throw 'Native UI product version mismatch.' }
& (Join-Path $PSScriptRoot 'test-msi-platform.ps1') -Architecture $Architecture -PackageRoot $PackageRoot
$size = (Get-ChildItem -LiteralPath $payload -File -Recurse | Measure-Object Length -Sum).Sum
Write-Host "Native UI smoke passed ($Architecture); payload $size bytes. No installed daemon or user data was changed."
