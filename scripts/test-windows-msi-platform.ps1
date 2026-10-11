#Requires -Version 5.1
param([ValidateSet('arm64', 'x64')][string]$Architecture = 'arm64')
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$manifest = Get-Content "$root/dist/windows/$Architecture/build-manifest.json" -Raw | ConvertFrom-Json
$path = Join-Path "$root/dist/windows/$Architecture" $manifest.artifacts[0].path
$installer = New-Object -ComObject WindowsInstaller.Installer
$session = $null; $view = $null; $database = $null
try {
    # Safe session prevents installation changes; only run read-only AppSearch.
    $session = $installer.OpenPackage($path, 1)
    $database = $installer.OpenDatabase($path, 0)
    $view = $database.OpenView('SELECT `Condition` FROM `LaunchCondition`')
    $view.Execute()
    $condition = $null
    while ($record = $view.Fetch()) {
        if ($record.StringData(1).Contains('WINDOWSBUILDNUMBER')) { $condition = $record.StringData(1); break }
    }
    if (-not $condition) { throw 'Missing OS launch condition' }
    if ($session.DoAction('AppSearch') -ne 1) { throw 'AppSearch failed' }
    $build = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion').CurrentBuildNumber
    if ($session.Property('WINDOWSBUILDNUMBER') -ne $build) { throw 'MSI did not read the actual OS build' }
    $view.Close()
    [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
    $view = $database.OpenView('SELECT `Condition` FROM `InstallExecuteSequence` WHERE `Action` = ''InstallWebView2Runtime''')
    $view.Execute()
    $runtimeCondition = $view.Fetch().StringData(1)
    if (-not $runtimeCondition.Contains('WEBVIEW2_MACHINE_VERSION') -or -not $runtimeCondition.Contains('WEBVIEW2_USER_VERSION')) { throw 'WebView2 installation is not conditional on runtime detection' }
    foreach ($runtimeCase in @(
        @{ Name='WebView2 missing'; Machine=''; User=''; Installed=''; Upgrade=''; Expected=1 },
        @{ Name='WebView2 machine installed'; Machine='149.0.0.1'; User=''; Installed=''; Upgrade=''; Expected=0 },
        @{ Name='WebView2 user installed'; Machine=''; User='149.0.0.1'; Installed=''; Upgrade=''; Expected=0 },
        @{ Name='WebView2 empty registration'; Machine='0.0.0.0'; User=''; Installed=''; Upgrade=''; Expected=1 },
        @{ Name='WebView2 both empty registrations'; Machine='0.0.0.0'; User='0.0.0.0'; Installed=''; Upgrade=''; Expected=1 },
        @{ Name='WebView2 upgrade already installed'; Machine='149.0.0.1'; User=''; Installed=''; Upgrade='1'; Expected=0 },
        @{ Name='WebView2 upgrade missing'; Machine=''; User=''; Installed=''; Upgrade='1'; Expected=1 },
        @{ Name='WebView2 uninstall'; Machine=''; User=''; Installed='1'; Upgrade=''; Expected=0 }
    )) {
        $session.Property('WEBVIEW2_MACHINE_VERSION') = $runtimeCase.Machine
        $session.Property('WEBVIEW2_USER_VERSION') = $runtimeCase.User
        $session.Property('Installed') = $runtimeCase.Installed
        $session.Property('WIX_UPGRADE_DETECTED') = $runtimeCase.Upgrade
        if ($session.EvaluateCondition($runtimeCondition) -ne $runtimeCase.Expected) { throw "Failed: $($runtimeCase.Name)" }
        Write-Host "PASS $Architecture $($runtimeCase.Name)"
    }
    $session.Property('Installed') = ''
    $session.Property('WIX_UPGRADE_DETECTED') = ''
    # Hosted Windows runners use Server, which the product intentionally rejects.
    $hostExpected = if ($session.Property('VersionNT64') -and
        $session.Property('MsiNTProductType') -eq '1' -and [int]$build -ge 19045) { 1 } else { 0 }
    if ($session.EvaluateCondition($condition) -ne $hostExpected) { throw 'Actual host launch condition mismatch' }
    Write-Host "PASS $Architecture actual host (expected $hostExpected)"
    foreach ($case in @(
        @{ Name='Windows 11 compatibility version'; Version='603'; Build='22000'; Product='1'; Installed=''; Expected=1 },
        @{ Name='Windows 11 current'; Version='1000'; Build='26200'; Product='1'; Installed=''; Expected=1 },
        @{ Name='Windows 10 22H2'; Version='603'; Build='19045'; Product='1'; Installed=''; Expected=1 },
        @{ Name='Windows 10 21H2 below minimum'; Version='603'; Build='19044'; Product='1'; Installed=''; Expected=0 },
        @{ Name='Windows 7'; Version='601'; Build='7601'; Product='1'; Installed=''; Expected=0 },
        @{ Name='32 bit'; Version=''; Build='26200'; Product='1'; Installed=''; Expected=0 },
        @{ Name='Server'; Version='1000'; Build='26200'; Product='3'; Installed=''; Expected=0 },
        @{ Name='Missing build'; Version='603'; Build=''; Product='1'; Installed=''; Expected=0 },
        @{ Name='Maintenance'; Version=''; Build=''; Product=''; Installed='1'; Expected=1 }
    )) {
        $session.Property('VersionNT64') = $case.Version
        $session.Property('WINDOWSBUILDNUMBER') = $case.Build
        $session.Property('MsiNTProductType') = $case.Product
        $session.Property('Installed') = $case.Installed
        if ($session.EvaluateCondition($condition) -ne $case.Expected) { throw "Failed: $($case.Name)" }
        Write-Host "PASS $Architecture $($case.Name)"
    }
}
finally {
    foreach ($comObject in @($view, $database, $session, $installer)) {
        if ($null -ne $comObject) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($comObject) }
    }
}
