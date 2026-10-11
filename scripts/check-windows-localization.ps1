#Requires -Version 7.0
[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$catalog = Join-Path $projectRoot 'platform/windows/FnCPN.Windows/AppStrings.Localization.g.cs'
$codeBehind = Join-Path $projectRoot 'platform/windows/FnCPN.Windows/MainWindow.xaml.cs'
$xamlPath = Join-Path $projectRoot 'platform/windows/FnCPN.Windows/MainWindow.xaml'

$catalogText = Get-Content -LiteralPath $catalog -Raw
$keys = [regex]::Matches($catalogText, '\["([^"\r\n]+)"\]\s*=') |
    ForEach-Object { $_.Groups[1].Value }
$duplicates = $keys | Group-Object | Where-Object Count -gt 1
if ($duplicates) {
    throw "Duplicate localization source keys: $($duplicates.Name -join ', ')"
}

$missing = [Collections.Generic.List[string]]::new()
$codeText = Get-Content -LiteralPath $codeBehind -Raw
foreach ($match in [regex]::Matches($codeText, 'L\("([^"\r\n]*)"\)')) {
    $key = $match.Groups[1].Value
    if ($keys -notcontains $key) { $missing.Add("MainWindow.xaml.cs: $key") }
}

[xml]$xaml = Get-Content -LiteralPath $xamlPath -Raw
foreach ($node in $xaml.SelectNodes('//*')) {
    foreach ($attribute in $node.Attributes) {
        if ($attribute.Name -notin @('Text', 'Content', 'AutomationProperties.Name')) { continue }
        if ($attribute.Value -match '[\p{IsCJKUnifiedIdeographs}]' -and $keys -notcontains $attribute.Value) {
            $missing.Add("MainWindow.xaml: $($attribute.Name)=$($attribute.Value)")
        }
    }
}

function Find-NamedElement([string]$Name) {
    return $xaml.SelectSingleNode("//*[@*[local-name()='Name' and .='$Name']]")
}

foreach ($name in @('FnIdBox', 'UsernameBox', 'PasswordBox')) {
    $element = Find-NamedElement $name
    if ($null -eq $element -or $null -eq $element.Attributes['AutomationProperties.Name']) {
        $missing.Add("MainWindow.xaml: $name must expose its accessible input name")
    }
}

foreach ($name in @('LoginError', 'StateText', 'IdentityText', 'PathText', 'AddressText', 'HandshakeText', 'DirectText', 'NetworkText', 'PrivilegeText', 'LanOverlapText', 'StatusError', 'DiagnosticErrorsText', 'DiagnosticsText')) {
    $element = Find-NamedElement $name
    if ($null -eq $element) {
        $missing.Add("MainWindow.xaml: dynamic text element $name is missing")
        continue
    }
    if ($null -ne $element.Attributes['AutomationProperties.Name']) {
        $missing.Add("MainWindow.xaml: dynamic text element $name must expose its current text, not a fixed accessible name")
    }
}

foreach ($name in @('LoginError', 'StatusError')) {
    $element = Find-NamedElement $name
    $setting = if ($null -eq $element) { $null } else { $element.Attributes['AutomationProperties.LiveSetting'] }
    if ($null -eq $setting -or $setting.Value -ne 'Assertive') {
        $missing.Add("MainWindow.xaml: $name must announce changes assertively")
    }
}
$statusElement = Find-NamedElement 'StateText'
$statusSetting = if ($null -eq $statusElement) { $null } else { $statusElement.Attributes['AutomationProperties.LiveSetting'] }
if ($null -eq $statusSetting -or $statusSetting.Value -ne 'Polite') {
    $missing.Add('MainWindow.xaml: StateText must announce status changes politely')
}

if ($missing.Count -gt 0) {
    throw "Missing English localization entries:`n$($missing -join "`n")"
}

Write-Host "Windows localization coverage passed: $($keys.Count) English entries; all code and XAML source strings are mapped."
