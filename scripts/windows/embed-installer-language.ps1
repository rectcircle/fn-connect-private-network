#Requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$MsiPath,
    [Parameter(Mandatory)][string]$TransformPath,
    [Parameter(Mandatory)][string]$ChineseMsiPath
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$installer = New-Object -ComObject WindowsInstaller.Installer
$base = $null; $translated = $null; $view = $null; $record = $null; $summary = $null
function Read-MsiProperty($Database, [string]$Name) {
    $propertyView = $Database.OpenView("SELECT ``Value`` FROM ``Property`` WHERE ``Property`` = '$Name'")
    [void]$propertyView.Execute()
    $propertyRecord = $propertyView.Fetch()
    try { return $propertyRecord.StringData(1) }
    finally {
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($propertyRecord)
        [void]$propertyView.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($propertyView)
    }
}
function Read-MsiRows($Database, [string]$Query) {
    $rowView = $Database.OpenView($Query)
    [void]$rowView.Execute()
    $rows = [Collections.Generic.List[string]]::new()
    try {
        while ($row = $rowView.Fetch()) {
            $values = for ($field = 1; $field -le $row.FieldCount(); $field++) { $row.StringData($field) }
            $rows.Add(($values | ConvertTo-Json -Compress))
            [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($row)
        }
        return ($rows | Sort-Object | ConvertTo-Json -Compress)
    }
    finally {
        [void]$rowView.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($rowView)
    }
}
try {
    $base = $installer.OpenDatabase([IO.Path]::GetFullPath($MsiPath), 1)
    $translated = $installer.OpenDatabase([IO.Path]::GetFullPath($ChineseMsiPath), 0)
    foreach ($property in @('ProductCode', 'ProductVersion', 'UpgradeCode')) {
        if ((Read-MsiProperty $base $property) -ne (Read-MsiProperty $translated $property)) {
            throw "Language transform must preserve $property"
        }
    }
    if ((Read-MsiProperty $base 'ProductLanguage') -ne '1033' -or (Read-MsiProperty $translated 'ProductLanguage') -ne '2052') {
        throw 'Expected English base and Simplified Chinese localized MSI'
    }
    foreach ($table in @('CustomAction', 'InstallExecuteSequence', 'InstallUISequence', 'Component', 'File', 'Directory', 'Registry', 'ServiceControl', 'Upgrade')) {
        $query = 'SELECT * FROM `' + $table + '`'
        if ((Read-MsiRows $base $query) -ne (Read-MsiRows $translated $query)) { throw "Language transform changes installation semantics in $table" }
    }
    foreach ($localized in @(@{ Database=$base; Pattern='Next'; Culture='English' }, @{ Database=$translated; Pattern='下一步'; Culture='Chinese' })) {
        $labels = Read-MsiRows $localized.Database 'SELECT `Text` FROM `Control`'
        if ($labels -notmatch $localized.Pattern) { throw "Missing $($localized.Culture) wizard button translations" }
        $progress = Read-MsiRows $localized.Database 'SELECT `Description` FROM `ActionText`'
        $expected = if ($localized.Culture -eq 'Chinese') { '正在停止 FnCPN' } else { 'Stopping FnCPN' }
        if ($progress -notmatch $expected) { throw "Missing $($localized.Culture) FnCPN progress translations" }
    }
    $languageRows = Read-MsiRows $base 'SELECT `Language` FROM `Upgrade`'
    if ($languageRows -match '1033|2052') { throw 'Upgrades must not be restricted to the old installation language' }
    # Numeric language storage names let Windows Installer select the embedded
    # language transform when opening this package. Payload is included once.
    $record = $installer.CreateRecord(2)
    $record.StringData(1) = '2052'
    $record.SetStream(2, [IO.Path]::GetFullPath($TransformPath))
    $view = $base.OpenView('INSERT INTO `_Storages` (`Name`, `Data`) VALUES (?, ?)')
    $view.Execute($record)
    $view.Close()
    $summary = $base.SummaryInformation(1)
    $platform = $summary.Property(7).Split(';')[0]
    $summary.Property(7) = "$platform;1033,2052"
    $summary.Persist()
    $base.Commit()
    Write-Host '[windows-build] Embedded Chinese language transform; English fallback; product identity preserved'
}
finally {
    foreach ($item in @($summary, $record, $view, $translated, $base, $installer)) {
        if ($null -ne $item) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($item) }
    }
}
