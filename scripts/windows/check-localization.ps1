#Requires -Version 7.0
[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$catalog = Join-Path $projectRoot 'platform/windows/Localization.h'
$source = Join-Path $projectRoot 'platform/windows/main.cpp'
$catalogText = Get-Content -LiteralPath $catalog -Raw
$sourceText = Get-Content -LiteralPath $source -Raw
$catalogKeys = (Get-Content -LiteralPath (Join-Path $projectRoot 'platform/windows/messages.json') -Raw | ConvertFrom-Json -AsHashtable).Keys
$duplicates = $catalogKeys | Group-Object | Where-Object Count -gt 1
if ($duplicates) { throw "Duplicate native UI localization keys: $($duplicates.Name -join ', ')" }
$missing = [Collections.Generic.List[string]]::new()
foreach ($match in [regex]::Matches($sourceText, '\bL\("([^"\r\n]*)"\)')) {
    $key = $match.Groups[1].Value
    if ($catalogKeys -notcontains $key) { $missing.Add("main.cpp: $key") }
}
foreach ($field in @('FN Connect ID', 'fnOS 用户名', 'fnOS 密码')) {
    if ($catalogKeys -notcontains $field -or $sourceText -notmatch [regex]::Escape($field)) {
        $missing.Add("native UI accessible input label: $field")
    }
}
if ($missing.Count -gt 0) { throw "Missing English localization entries:`n$($missing -join "`n")" }
Write-Host "Native Windows localization coverage passed: $($catalogKeys.Count) English entries; all L() source strings are mapped."
