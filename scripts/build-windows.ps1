#Requires -Version 7.0
[CmdletBinding()]
param(
    [ValidateSet('all', 'arm64', 'x64')][string]$Architecture = 'all',
    [string]$OutputRoot = 'dist/windows-native'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$packageRoot = [IO.Path]::GetFullPath((Join-Path $projectRoot $OutputRoot))
$allowedOutputRoot = [IO.Path]::GetFullPath((Join-Path $projectRoot 'dist')) + [IO.Path]::DirectorySeparatorChar
if (-not $packageRoot.StartsWith($allowedOutputRoot, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'OutputRoot must be a subdirectory of the workspace dist directory'
}
$version = (Get-Content (Join-Path $projectRoot 'internal/version/VERSION') -Raw).Trim()
# Windows Installer uses the same numeric product version.
$installerVersion = $version.Split('-')[0]
# Microsoft documents pv > 0.0.0.0 as evidence of an installed Evergreen Runtime.
$webView2InstallCondition = 'NOT Installed AND NOT ((WEBVIEW2_MACHINE_VERSION AND WEBVIEW2_MACHINE_VERSION > "0.0.0.0") OR (WEBVIEW2_USER_VERSION AND WEBVIEW2_USER_VERSION > "0.0.0.0"))'
$revision = (& git -C $projectRoot rev-parse HEAD).Trim()
$sourceStatus = (& git -C $projectRoot status --porcelain --untracked-files=all | Out-String).Trim()
# Development documentation and CI definitions do not change package inputs.
# Both still contribute to workingTreeDirty.
$fingerprintPathspec = @('.', ':(exclude)docs/windows-development.md', ':(exclude).github/workflows/**')
$fingerprintStatus = (& git -C $projectRoot status --porcelain --untracked-files=all -- @fingerprintPathspec | Out-String).Trim()
$sourceDiff = (& git -C $projectRoot diff --binary HEAD -- @fingerprintPathspec | Out-String)
$untrackedFiles = @(& git -C $projectRoot ls-files --others --exclude-standard -- @fingerprintPathspec)
$fingerprintText = [Text.StringBuilder]::new()
[void]$fingerprintText.AppendLine($fingerprintStatus)
[void]$fingerprintText.AppendLine($sourceDiff)
foreach ($relativePath in $untrackedFiles | Sort-Object) {
    $fullPath = Join-Path $projectRoot $relativePath
    if (Test-Path -LiteralPath $fullPath -PathType Leaf) {
        [void]$fingerprintText.Append($relativePath).Append('|').Append((Get-FileHash -LiteralPath $fullPath -Algorithm SHA256).Hash).AppendLine()
    }
}
$fingerprintBytes = [Text.Encoding]::UTF8.GetBytes($fingerprintText.ToString())
$sourceFingerprint = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($fingerprintBytes)).ToLowerInvariant()
$sourceDirty = -not [string]::IsNullOrWhiteSpace($sourceStatus)
$sourceRevision = if ($sourceDirty) { "$revision-dirty.$($sourceFingerprint.Substring(0, 12))" } else { $revision }
$wix = Join-Path $projectRoot '.dev/tools/wix/wix.exe'
$wixUtilExtension = Join-Path $projectRoot '.dev/tools/wix/.wix/extensions/WixToolset.Util.wixext/7.0.0/wixext7/WixToolset.Util.wixext.dll'
$wixUiExtension = Join-Path $projectRoot '.dev/tools/wix/.wix/extensions/WixToolset.UI.wixext/7.0.0/wixext7/WixToolset.UI.wixext.dll'
$wintunRoot = Join-Path $projectRoot '.dev/dependencies/wintun/0.14.1'
$wintunLicense = Join-Path $wintunRoot 'LICENSE.txt'
$webView2Bootstrapper = Join-Path $projectRoot '.dev/dependencies/webview2/MicrosoftEdgeWebView2Setup.exe'
$nativeUiSource = Join-Path $projectRoot 'platform/windows'
$nativeUiSdkProject = Join-Path $nativeUiSource 'WebView2SDK.csproj'
$nativeUiNuget = Join-Path $projectRoot '.dev/native-ui-nuget'
$nativeUiSdkRoot = Join-Path $nativeUiNuget 'microsoft.web.webview2/1.0.4258.31'
$architectures = if ($Architecture -eq 'all') { @('arm64', 'x64') } else { @($Architecture) }

function Invoke-Checked([string]$File, [string[]]$Arguments) {
    & $File @Arguments
    if ($LASTEXITCODE -ne 0) { throw "命令失败 ($LASTEXITCODE): $File $($Arguments -join ' ')" }
}

function Hide-CompletionNavigation([string]$Path, [string]$Culture = 'en-US') {
    $installer = New-Object -ComObject WindowsInstaller.Installer
    $database = $installer.OpenDatabase($Path, 1)
    try {
        $chinese = $Culture -eq 'zh-CN'
        $text = if ($chinese) { '部分文件仍被以下程序占用。关闭它们后即可继续安装。若保留程序运行，安装后可能需要重启电脑。' } else { 'These applications are still using files needed by setup. Close them to continue. Keeping them running may require restarting Windows after installation.' }
        $description = if ($chinese) { '请关闭占用文件的程序，以便继续更新。' } else { 'Close applications using these files to continue updating.' }
        foreach ($item in @(@('Text',$text),@('Description',$description))) {
            $update = $database.OpenView('UPDATE `Control` SET `Text` = '''+$item[1]+''' WHERE `Dialog_` = ''MsiRMFilesInUse'' AND `Control` = '''+$item[0]+'''')
            [void]$update.Execute(); [void]$update.Close(); [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($update)
        }
        $close = if ($chinese) { '关闭占用程序并继续安装（安装后可能重新打开程序）' } else { 'Close applications and continue (they may reopen after installation)' }
        $keep = if ($chinese) { '保留程序运行，安装后重启电脑' } else { 'Keep applications running and restart Windows after installation' }
        foreach ($item in @(@('UseRM',$close),@('DontUseRM',$keep))) {
            $update = $database.OpenView('UPDATE `RadioButton` SET `Text` = '''+$item[1]+''' WHERE `Property` = ''WixUIRMOption'' AND `Value` = '''+$item[0]+'''')
            [void]$update.Execute(); [void]$update.Close(); [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($update)
        }
        $view = $database.OpenView('SELECT `Control`, `Attributes` FROM `Control` WHERE `Dialog_` = ''ExitDialog''')
        [void]$view.Execute()
        $changes = @{}
        while ($record = $view.Fetch()) {
            if ($record.StringData(1) -in @('Back', 'Cancel')) { $changes[$record.StringData(1)] = $record.IntegerData(2) -band (-bnot 3) }
            [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($record)
        }
        [void]$view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        # Keep the checkbox background small; its label is rendered by a transparent Text control.
        $update = $database.OpenView('UPDATE `Control` SET `Width` = 220, `Height` = 17 WHERE `Dialog_` = ''ExitDialog'' AND `Control` = ''OptionalCheckBox''')
        [void]$update.Execute(); [void]$update.Close(); [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($update)
        foreach ($control in $changes.Keys) {
            $update = $database.OpenView('UPDATE `Control` SET `Attributes` = ' + $changes[$control] + ' WHERE `Dialog_` = ''ExitDialog'' AND `Control` = ''' + $control + '''')
            [void]$update.Execute(); [void]$update.Close()
            [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($update)
        }
        [void]$database.Commit()
    } finally {
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($database)
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($installer)
    }
}

function Escape-Xml([string]$Value) { [Security.SecurityElement]::Escape($Value) }

function Assert-WindowsDpiAwarenessManifest([string]$ManifestPath) {
    [xml]$manifest = Get-Content -LiteralPath $ManifestPath -Raw
    $dpiAware = $manifest.SelectSingleNode("//*[local-name()='dpiAware' and namespace-uri()='http://schemas.microsoft.com/SMI/2005/WindowsSettings']")
    $dpiAwareness = $manifest.SelectSingleNode("//*[local-name()='dpiAwareness' and namespace-uri()='http://schemas.microsoft.com/SMI/2016/WindowsSettings']")
    if ($null -eq $dpiAware -or $dpiAware.InnerText -ne 'true/pm' -or
        $null -eq $dpiAwareness -or $dpiAwareness.InnerText -ne 'PerMonitorV2') {
        throw 'Native Windows application manifest must declare Per-Monitor V2 DPI awareness with a legacy per-monitor fallback'
    }
    Write-Host '[windows-build] Native UI manifest PerMonitorV2 DPI awareness validated'
}

function Find-WindowsManifestTool {
    $kitsRoot = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits/10/bin'
    $toolArchitecture = if ([Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture -eq [Runtime.InteropServices.Architecture]::Arm64) { 'arm64' } else { 'x64' }
    $candidates = foreach ($kit in Get-ChildItem -LiteralPath $kitsRoot -Directory -ErrorAction SilentlyContinue) {
        $version = $null
        if (-not [version]::TryParse($kit.Name, [ref]$version)) { continue }
        $path = Join-Path $kit.FullName "$toolArchitecture/mt.exe"
        if (Test-Path -LiteralPath $path -PathType Leaf) { [pscustomobject]@{ Version = $version; Path = $path } }
    }
    $selected = $candidates | Sort-Object Version -Descending | Select-Object -First 1
    if ($null -eq $selected) { throw "Windows SDK mt.exe ($toolArchitecture) is required to verify the published application manifest" }
    return $selected.Path
}

function Assert-PeArchitecture([string]$Path, [uint16]$ExpectedMachine) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw "PE payload is missing: $Path" }
    $stream = $null
    $reader = $null
    try {
        $stream = [IO.File]::OpenRead($Path)
        $reader = [IO.BinaryReader]::new($stream)
        if ($reader.ReadUInt16() -ne 0x5a4d) { throw "Invalid DOS signature: $Path" }
        $stream.Position = 0x3c
        $peOffset = $reader.ReadInt32()
        if ($peOffset -lt 64 -or $peOffset -gt ($stream.Length - 6)) { throw "Invalid PE header offset: $Path" }
        $stream.Position = $peOffset
        if ($reader.ReadUInt32() -ne 0x00004550) { throw "Invalid PE signature: $Path" }
        $machine = $reader.ReadUInt16()
        if ($machine -ne $ExpectedMachine) {
            throw ("PE architecture mismatch for {0}: expected 0x{1:X4}, got 0x{2:X4}" -f $Path, $ExpectedMachine, $machine)
        }
    }
    finally {
        if ($reader) { $reader.Dispose() }
        elseif ($stream) { $stream.Dispose() }
    }
}

function Publish-WindowsBuildDirectory([string]$Target, [string]$Next, [string]$Architecture) {
    $backup = "$Target.previous.$PID.$([guid]::NewGuid().ToString('N'))"
    $failed = "$Target.failed.$PID.$([guid]::NewGuid().ToString('N'))"
    $hadTarget = Test-Path -LiteralPath $Target
    $movedPrevious = $false
    try {
        if ($hadTarget) {
            Move-Item -LiteralPath $Target -Destination $backup
            $movedPrevious = $true
        }
        Move-Item -LiteralPath $Next -Destination $Target
    }
    catch {
        $publishError = $_
        if ($movedPrevious -and (Test-Path -LiteralPath $backup)) {
            try {
                if (Test-Path -LiteralPath $Target) {
                    Move-Item -LiteralPath $Target -Destination $failed
                    Write-Warning "[$Architecture] 新候选切换失败，保留失败目录以供诊断：$failed"
                }
                Move-Item -LiteralPath $backup -Destination $Target
                $movedPrevious = $false
                Write-Host "[windows-build] [$Architecture] 已恢复上一版候选目录"
            }
            catch {
                throw "[$Architecture] 发布候选失败：$($publishError.Exception.Message)；恢复旧候选也失败：$($_.Exception.Message)。旧候选保留在 $backup"
            }
        }
        throw $publishError
    }

    if ($movedPrevious -and (Test-Path -LiteralPath $backup)) {
        try {
            Remove-Item -LiteralPath $backup -Recurse -Force
        }
        catch {
            Write-Warning "[$Architecture] 新候选已发布，但旧候选备份无法清理：$backup ($($_.Exception.Message))"
        }
    }
}

function New-ComponentId([string]$Value) {
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $hash = [Convert]::ToHexString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($Value))).Substring(0, 16) }
    finally { $sha.Dispose() }
    return "C_$hash"
}

function New-DirectoryXml([string]$Payload, [string]$RelativeDirectory, [System.Collections.Generic.List[string]]$References) {
    $directoryPath = if ($RelativeDirectory) { Join-Path $Payload $RelativeDirectory } else { $Payload }
    $content = [Text.StringBuilder]::new()
    if ($RelativeDirectory) {
        $directoryId = New-ComponentId "dir:$RelativeDirectory"
        $name = Split-Path -Leaf $RelativeDirectory
        [void]$content.AppendLine("<Directory Id=`"$directoryId`" Name=`"$(Escape-Xml $name)`">")
    }
    foreach ($file in Get-ChildItem -LiteralPath $directoryPath -File | Sort-Object Name) {
        $relative = if ($RelativeDirectory) { Join-Path $RelativeDirectory $file.Name } else { $file.Name }
        if ($relative -ieq 'fncpn.exe' -or $relative -ieq 'WebView2Setup.exe') { continue }
        $id = New-ComponentId "file:$relative"
        $source = '$(var.PayloadDir)\' + ($relative -replace '/', '\')
        [void]$content.AppendLine("<Component Id=`"$id`" Guid=`"*`"><File Id=`"F_$($id.Substring(2))`" Source=`"$(Escape-Xml $source)`" KeyPath=`"yes`" /></Component>")
        $References.Add($id)
    }
    foreach ($child in Get-ChildItem -LiteralPath $directoryPath -Directory | Sort-Object Name) {
        $childRelative = if ($RelativeDirectory) { Join-Path $RelativeDirectory $child.Name } else { $child.Name }
        [void]$content.Append((New-DirectoryXml $Payload $childRelative $References))
    }
    if ($RelativeDirectory) { [void]$content.AppendLine('</Directory>') }
    return $content.ToString()
}

function Assert-MsiUpgradeMetadata([string]$Path, [string]$Architecture, [string]$ProductVersion, [string]$InstallerHelperPath) {
    $installer = $null
    $database = $null
    $view = $null
    try {
        $installer = New-Object -ComObject WindowsInstaller.Installer
        $database = $installer.OpenDatabase($Path, 0)
        $tick = [char]96
        $sequenceQuery = 'SELECT ' + $tick + 'Action' + $tick + ', ' + $tick + 'Condition' + $tick + ', ' + $tick + 'Sequence' + $tick + ' FROM ' + $tick + 'InstallExecuteSequence' + $tick
        $view = $database.OpenView($sequenceQuery)
        $view.Execute()
        $sequence = @{}
        $conditions = @{}
        while ($record = $view.Fetch()) {
            $action = $record.StringData(1)
            $sequence[$action] = $record.IntegerData(3)
            $conditions[$action] = $record.StringData(2)
        }

        $closeAction = if ($Architecture -eq 'arm64') { 'Wix4CloseApplications_A64' } else { 'Wix4CloseApplications_X64' }
        $requiredActions = @('InstallInitialize', 'RemoveExistingProducts', $closeAction, 'StopAllUserDaemons', 'InstallFiles', 'InstallServices', 'StartServices', 'InstallFinalize')
        $missing = @($requiredActions | Where-Object { -not $sequence.ContainsKey($_) })
        if ($missing.Count -gt 0) { throw "MSI upgrade sequence is missing: $($missing -join ', ')" }
        for ($index = 1; $index -lt $requiredActions.Count; $index++) {
            $previous = $requiredActions[$index - 1]
            $current = $requiredActions[$index]
            if ($sequence[$previous] -ge $sequence[$current]) {
                throw "Unsafe MSI action order: $previous=$($sequence[$previous]), $current=$($sequence[$current])"
            }
        }
        # ICE63: early RemoveExistingProducts must precede every script-generating action.
        if ($sequence.RemoveExistingProducts -ne $sequence.InstallInitialize + 1) {
            throw 'RemoveExistingProducts must immediately follow InstallInitialize without deferred actions between them'
        }
        foreach ($action in @($closeAction, 'StopAllUserDaemons')) {
            if ($conditions[$action] -notmatch 'WIX_UPGRADE_DETECTED') {
                throw "MSI action $action is not restricted to major upgrades"
            }
        }

        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT ' + $tick + 'Property' + $tick + ', ' + $tick + 'Value' + $tick + ' FROM ' + $tick + 'Property' + $tick)
        $view.Execute()
        $properties = @{}
        while ($record = $view.Fetch()) { $properties[$record.StringData(1)] = $record.StringData(2) }
        if ($properties.ProductVersion -ne $ProductVersion) {
            throw "MSI ProductVersion '$($properties.ProductVersion)' does not match '$ProductVersion'"
        }
        if ($properties.MsiLogging -ne 'voicewarmup!') { throw 'MSI must enable verbose, line-flushed installation logging' }
        if ($properties.WIXUI_EXITDIALOGOPTIONALCHECKBOX -ne '1' -or
            [string]::IsNullOrWhiteSpace($properties.WIXUI_EXITDIALOGOPTIONALCHECKBOXTEXT)) {
            throw 'Completion dialog must offer launching the installed UI, checked by default'
        }
        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        if ($properties.ALLUSERS -ne '1' -or $properties.MSIDEPLOYMENTCOMPLIANT -ne '1' -or $properties.ARPNOMODIFY -ne '1') {
            throw 'MSI must require machine elevation, declare UAC compliance, and expose direct uninstall in Settings'
        }
        if (($database.SummaryInformation(0).Property(15) -band 8) -ne 0) { throw 'MSI must not suppress elevation' }
        $view = $database.OpenView('SELECT `ActionProperty`,`Attributes` FROM `Upgrade`')
        $view.Execute()
        $sameVersionReplacement = $false
        while ($record = $view.Fetch()) {
            if ($record.StringData(1) -eq 'WIX_UPGRADE_DETECTED' -and ($record.IntegerData(2) -band 512) -ne 0) { $sameVersionReplacement = $true }
        }
        if (-not $sameVersionReplacement) { throw 'MSI must replace same numeric version candidates rather than install duplicates' }
        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT `Dialog_`,`Attributes` FROM `Control`')
        $view.Execute()
        $shieldDialogs = @{}
        while ($record = $view.Fetch()) {
            if (($record.IntegerData(2) -band 8388608) -ne 0) { $shieldDialogs[$record.StringData(1)] = $true }
        }
        foreach ($dialog in @('VerifyReadyDlg')) {
            if (-not $shieldDialogs.ContainsKey($dialog)) { throw "MSI missing elevation shield in $dialog" }
        }
        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT `Dialog` FROM `Dialog`')
        $view.Execute()
        $wizardDialogs = @()
        while ($record = $view.Fetch()) { $wizardDialogs += $record.StringData(1) }
        foreach ($dialog in @('WelcomeDlg', 'LicenseAgreementDlg', 'InstallDirDlg', 'VerifyReadyDlg', 'ProgressDlg', 'ExitDialog')) {
            if ($wizardDialogs -notcontains $dialog) { throw "MSI installation wizard is missing $dialog" }
        }
        if ($wizardDialogs -contains 'WelcomeEulaDlg') { throw 'MSI must use the full installation wizard rather than the combined minimal welcome/license page' }
        if ($properties.WIXUI_INSTALLDIR -ne 'INSTALLFOLDER') { throw 'Installation wizard must configure the product installation directory' }
        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT `Control`, `Attributes` FROM `Control` WHERE `Dialog_` = ''ExitDialog''')
        [void]$view.Execute()
        while ($record = $view.Fetch()) {
            if ($record.StringData(1) -in @('Back', 'Cancel') -and ($record.IntegerData(2) -band 3) -ne 0) {
                throw 'Completed installation must hide and disable Back and Cancel'
            }
        }
        [void]$view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        Write-Host "[windows-build] [$Architecture] UAC UI, direct uninstall, and same-version replacement validated"
        $view = $database.OpenView('SELECT ' + $tick + 'Condition' + $tick + ', ' + $tick + 'Description' + $tick + ' FROM ' + $tick + 'LaunchCondition' + $tick)
        $view.Execute()
        $windows11ConditionFound = $false
        while ($record = $view.Fetch()) {
            $condition = $record.StringData(1)
            if ($condition -match 'Installed\s+OR' -and
                $condition -match '\(VersionNT64\s+AND' -and
                $condition -match 'MsiNTProductType\s*=\s*1' -and
                $condition -match 'WINDOWSBUILDNUMBER\s*>?=\s*19045' -and
                $record.StringData(2) -match 'Windows 10 22H2') {
                $windows11ConditionFound = $true
                break
            }
        }
        if (-not $windows11ConditionFound) {
            throw 'MSI must allow maintenance but require 64-bit client Windows 10 22H2 build 19045 or later for installation'
        }

        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT ' + $tick + 'Property' + $tick + ', ' + $tick + 'Signature_' + $tick + ' FROM ' + $tick + 'AppSearch' + $tick)
        $view.Execute()
        $buildSearchFound = $false
        while ($record = $view.Fetch()) {
            if ($record.StringData(1) -eq 'WINDOWSBUILDNUMBER' -and $record.StringData(2) -eq 'WindowsBuildNumberSearch') {
                $buildSearchFound = $true
                break
            }
        }
        if (-not $buildSearchFound) { throw 'MSI must search the Windows build registry value before evaluating launch conditions' }

        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT ' + $tick + 'Signature_' + $tick + ', ' + $tick + 'Root' + $tick + ', ' + $tick + 'Key' + $tick + ', ' + $tick + 'Name' + $tick + ' FROM ' + $tick + 'RegLocator' + $tick)
        $view.Execute()
        $buildLocatorFound = $false
        while ($record = $view.Fetch()) {
            if ($record.StringData(1) -eq 'WindowsBuildNumberSearch' -and $record.IntegerData(2) -eq 2 -and
                $record.StringData(3) -eq 'SOFTWARE\Microsoft\Windows NT\CurrentVersion' -and
                $record.StringData(4) -eq 'CurrentBuildNumber') {
                $buildLocatorFound = $true
                break
            }
        }
        if (-not $buildLocatorFound) { throw 'MSI Windows build search must read HKLM CurrentBuildNumber' }
        Write-Host "[windows-build] [$Architecture] Windows 10 22H2 or later client OS launch condition and build search validated"

        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT ' + $tick + 'Property' + $tick + ', ' + $tick + 'Value' + $tick + ' FROM ' + $tick + 'Property' + $tick)
        $view.Execute()
        $properties = @{}
        while ($record = $view.Fetch()) { $properties[$record.StringData(1)] = $record.StringData(2) }
        $secureProperties = @($properties.SecureCustomProperties -split ';')
        if ($secureProperties -notcontains 'WIX_UPGRADE_DETECTED') {
            throw 'WIX_UPGRADE_DETECTED is missing from SecureCustomProperties'
        }
        if ($secureProperties -notcontains 'PURGE_CURRENT_USER_DATA') {
            throw 'PURGE_CURRENT_USER_DATA is missing from SecureCustomProperties'
        }

        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT ' + $tick + 'Action' + $tick + ', ' + $tick + 'Type' + $tick + ', ' + $tick + 'Source' + $tick + ', ' + $tick + 'Target' + $tick + ' FROM ' + $tick + 'CustomAction' + $tick)
        $view.Execute()
        $stopActionFound = $false
        while ($record = $view.Fetch()) {
            if ($record.StringData(1) -ne 'StopAllUserDaemons') { continue }
            $stopActionFound = $true
            $actionType = $record.IntegerData(2)
            if (($actionType -band 1024) -eq 0 -or ($actionType -band 2048) -eq 0) {
                throw "StopAllUserDaemons must be deferred and non-impersonating (CustomAction type $actionType)"
            }
            if ($record.StringData(3) -ne 'InstallerHelperBinary' -or $record.StringData(4) -ne 'stop-daemons') {
                throw 'StopAllUserDaemons must invoke only the embedded Go maintenance command'
            }
        }
        if (-not $stopActionFound) { throw 'StopAllUserDaemons custom action is missing' }

        $cleanupActionFound = $false
        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT ' + $tick + 'Action' + $tick + ', ' + $tick + 'Type' + $tick + ', ' + $tick + 'Source' + $tick + ', ' + $tick + 'Target' + $tick + ' FROM ' + $tick + 'CustomAction' + $tick)
        $view.Execute()
        while ($record = $view.Fetch()) {
            if ($record.StringData(1) -ne 'CleanupNetwork') { continue }
            $cleanupActionFound = $true
            $actionType = $record.IntegerData(2)
            if (($actionType -band 1024) -eq 0 -or ($actionType -band 2048) -eq 0) {
                throw "CleanupNetwork must be deferred and non-impersonating (CustomAction type $actionType)"
            }
            if ($record.StringData(3) -ne 'InstallerHelperBinary' -or $record.StringData(4) -ne 'cleanup-network') {
                throw 'CleanupNetwork must invoke only the embedded Go network cleanup command'
            }
        }
        if (-not $cleanupActionFound) { throw 'CleanupNetwork custom action is missing' }
        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT `Name`, `Data` FROM `_Streams`')
        $view.Execute()
        $helperFound = $false
        while ($record = $view.Fetch()) {
            if ($record.StringData(1) -eq 'Binary.GoClientBinary') { throw 'MSI must not embed a second full Go client' }
            if ($record.StringData(1) -eq 'Binary.InstallerHelperBinary') {
                $helperFound = $true
                if ($record.DataSize(2) -ne (Get-Item -LiteralPath $InstallerHelperPath).Length) { throw 'Embedded installer helper size mismatch' }
            }
        }
        if (-not $helperFound) { throw 'MSI installer helper stream is missing' }


        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView($sequenceQuery)
        $view.Execute()
        $sequence = @{}
        $conditions = @{}
        while ($record = $view.Fetch()) {
            $action = $record.StringData(1)
            $sequence[$action] = $record.IntegerData(3)
            $conditions[$action] = $record.StringData(2)
        }
        foreach ($action in @('CleanupNetwork')) {
            if (-not $sequence.ContainsKey($action)) { throw "MSI sequence is missing $action" }
            if (-not $sequence.ContainsKey('DeleteServices') -or $sequence[$action] -le $sequence.DeleteServices -or $sequence[$action] -ge $sequence.RemoveFiles) {
                throw "CleanupNetwork must run after DeleteServices and before RemoveFiles (cleanup=$($sequence[$action]), services=$($sequence.DeleteServices), files=$($sequence.RemoveFiles))"
            }
            if ($conditions[$action] -notmatch 'REMOVE.*ALL') { throw 'CleanupNetwork must run only for full uninstall' }
        }

        $purgeAction = 'PurgeCurrentUserData'
        if (-not $sequence.ContainsKey($purgeAction) -or $sequence[$purgeAction] -ge $sequence.RemoveFiles) {
            throw 'PurgeCurrentUserData must run before RemoveFiles'
        }
        if ($conditions[$purgeAction] -notmatch 'REMOVE.*ALL' -or
            $conditions[$purgeAction] -notmatch 'PURGE_CURRENT_USER_DATA.*1' -or
            $conditions[$purgeAction] -notmatch 'NOT\s+UPGRADINGPRODUCTCODE') {
            throw 'PurgeCurrentUserData must require explicit full uninstall and must not run during a major upgrade'
        }

        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT ' + $tick + 'Action' + $tick + ', ' + $tick + 'Type' + $tick + ', ' + $tick + 'Source' + $tick + ', ' + $tick + 'Target' + $tick + ' FROM ' + $tick + 'CustomAction' + $tick)
        $view.Execute()
        $purgeActionFound = $false
        while ($record = $view.Fetch()) {
            if ($record.StringData(1) -ne $purgeAction) { continue }
            $purgeActionFound = $true
            $actionType = $record.IntegerData(2)
            if (($actionType -band 1024) -eq 0 -or ($actionType -band 2048) -ne 0) {
                throw "PurgeCurrentUserData must be deferred and impersonate the uninstalling user (CustomAction type $actionType)"
            }
            if ($record.StringData(3) -ne 'GoClient' -or $record.StringData(4) -ne 'internal-purge-user --stop-daemon') {
                throw 'PurgeCurrentUserData must invoke only the fixed per-user purge command'
            }
        }
        if (-not $purgeActionFound) { throw 'PurgeCurrentUserData custom action is missing' }

        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT ' + $tick + 'Name' + $tick + ', ' + $tick + 'StartName' + $tick + ', ' + $tick + 'Arguments' + $tick + ' FROM ' + $tick + 'ServiceInstall' + $tick)
        $view.Execute()
        $serviceFound = $false
        while ($record = $view.Fetch()) {
            if ($record.StringData(1) -ne 'FnCPNClient') { continue }
            $serviceFound = $true
            if ($record.StringData(2) -ne 'LocalSystem' -or $record.StringData(3) -ne 'client privileged-daemon') {
                throw 'FnCPNClient must run the restricted client privileged-daemon command as LocalSystem'
            }
        }
        if (-not $serviceFound) { throw 'FnCPNClient ServiceInstall row is missing' }

        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT ' + $tick + 'Key' + $tick + ', ' + $tick + 'Name' + $tick + ', ' + $tick + 'Value' + $tick + ' FROM ' + $tick + 'Registry' + $tick)
        $view.Execute()
        $productCodeFound = $false
        while ($record = $view.Fetch()) {
            if ($record.StringData(1) -eq 'Software\FnCPN' -and $record.StringData(2) -eq 'ProductCode' -and $record.StringData(3) -eq '[ProductCode]') {
                $productCodeFound = $true
                break
            }
        }
        if (-not $productCodeFound) { throw 'MSI must store its ProductCode in HKLM Software\FnCPN for explicit repair' }

        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT ' + $tick + 'Root' + $tick + ', ' + $tick + 'Key' + $tick + ', ' + $tick + 'Name' + $tick + ', ' + $tick + 'Value' + $tick + ', ' + $tick + 'Component_' + $tick + ' FROM ' + $tick + 'Registry' + $tick)
        $view.Execute()
        $protocolValues = @{}
        while ($record = $view.Fetch()) {
            if ($record.IntegerData(1) -ne 0 -or $record.StringData(5) -ne 'ProtocolRegistration') { continue }
            $registryKey = $record.StringData(2)
            $registryName = $record.StringData(3)
            $protocolValues["$registryKey|$registryName"] = $record.StringData(4)
        }
        $expectedProtocolValues = @{
            'fncpn|' = 'URL:FnCPN authorization protocol'
            'fncpn|URL Protocol' = ''
            'fncpn\shell\open\command|' = '"[INSTALLFOLDER]ui\FnCPN.UI.exe" "%1"'
        }
        foreach ($registryValue in $expectedProtocolValues.GetEnumerator()) {
            if (-not $protocolValues.ContainsKey($registryValue.Key) -or $protocolValues[$registryValue.Key] -ne $registryValue.Value) {
                throw "MSI fncpn URL protocol registration is missing or invalid: $($registryValue.Key)"
            }
        }

        $view.Close()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $view = $database.OpenView('SELECT ' + $tick + 'Environment' + $tick + ', ' + $tick + 'Name' + $tick + ', ' + $tick + 'Value' + $tick + ', ' + $tick + 'Component_' + $tick + ' FROM ' + $tick + 'Environment' + $tick)
        $view.Execute()
        $cliPathFound = $false
        while ($record = $view.Fetch()) {
            if ($record.StringData(1) -ne 'FnCPNPath') { continue }
            $cliPathFound = $true
            if ($record.StringData(2) -ne '=-*PATH' -or $record.StringData(3) -ne '[~];[INSTALLFOLDER]' -or $record.StringData(4) -ne 'CliPath') {
                throw 'MSI must append INSTALLFOLDER to the machine PATH and remove it with the CliPath component'
            }
        }
        if (-not $cliPathFound) { throw 'MSI FnCPN machine PATH entry is missing' }
        Write-Host "[windows-build] [$Architecture] MSI sequence/security metadata validated: close=$($sequence[$closeAction]), stop=$($sequence.StopAllUserDaemons), remove=$($sequence.RemoveExistingProducts), files=$($sequence.InstallFiles)"
        Write-Host "[windows-build] [$Architecture] fncpn URL protocol and machine PATH metadata validated"
    }
    finally {
        if ($view) { try { $view.Close() } catch {}; [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view) }
        if ($database) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($database) }
        if ($installer) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($installer) }
    }
}

if (-not (Test-Path -LiteralPath $wix -PathType Leaf)) { throw "WiX 7 缺失：$wix。先运行 scripts/setup-windows.ps1。" }
if (-not (Test-Path -LiteralPath $wixUiExtension -PathType Leaf)) { throw "WiX UI Extension 缺失：$wixUiExtension。先运行 scripts/setup-windows.ps1。" }
if (-not (Test-Path -LiteralPath $wixUtilExtension -PathType Leaf)) { throw "WiX Util Extension 缺失：$wixUtilExtension。先运行 scripts/setup-windows.ps1。" }
if (-not (Test-Path -LiteralPath $wintunLicense -PathType Leaf)) { throw "Wintun 0.14.1 缺失：$wintunRoot。先运行 scripts/setup-windows.ps1。" }
if (-not (Test-Path -LiteralPath (Join-Path $projectRoot 'packaging/assets/AppIcon.ico') -PathType Leaf)) { throw 'Windows application icon is missing: packaging/assets/AppIcon.ico' }
if (-not (Test-Path -LiteralPath $webView2Bootstrapper -PathType Leaf)) { throw "WebView2 官方 Evergreen Bootstrapper 缺失：$webView2Bootstrapper。先运行 scripts/setup-windows.ps1。" }
$appManifest = Join-Path $nativeUiSource 'app.manifest'
Assert-WindowsDpiAwarenessManifest $appManifest
$manifestTool = Find-WindowsManifestTool
$null = New-Item -ItemType Directory -Force -Path $nativeUiNuget
Invoke-Checked 'dotnet' @('restore', $nativeUiSdkProject, "-p:RestorePackagesPath=$nativeUiNuget")
if (-not (Test-Path -LiteralPath (Join-Path $nativeUiSdkRoot 'build/native/include/WebView2.h') -PathType Leaf)) { throw 'Restored WebView2 native SDK is incomplete' }
$webView2Signature = Get-AuthenticodeSignature -LiteralPath $webView2Bootstrapper
if ($webView2Signature.Status -ne 'Valid' -or $webView2Signature.SignerCertificate.Subject -notlike '*Microsoft Corporation*') { throw "WebView2 Bootstrapper Authenticode 签名无效：$($webView2Signature.Status)" }

foreach ($architecture in $architectures) {
    $timer = [Diagnostics.Stopwatch]::StartNew()
    $goArch = if ($architecture -eq 'arm64') { 'arm64' } else { 'amd64' }
    $wixArchShort = if ($architecture -eq 'arm64') { 'A64' } else { 'X64' }
    $target = Join-Path $packageRoot $architecture
    $stageRoot = Join-Path $projectRoot '.dev/windows-native-stage'
    $stage = Join-Path $stageRoot ("{0}-{1}-{2}" -f $architecture, $PID, [guid]::NewGuid().ToString('N'))
    $payload = Join-Path $stage 'payload'
    $existingManifest = Join-Path $target 'build-manifest.json'
    if ((Test-Path -LiteralPath $target) -and @(Get-ChildItem -LiteralPath $target -Force).Count -gt 0 -and -not (Test-Path -LiteralPath $existingManifest)) {
        throw "拒绝覆盖没有本脚本 build-manifest.json 的既有目录：$target"
    }
    if (Test-Path -LiteralPath $existingManifest) {
        $previousManifest = Get-Content -LiteralPath $existingManifest -Raw | ConvertFrom-Json
        $managed = @($previousManifest.managedPaths) + @('build-manifest.json')
        foreach ($entry in Get-ChildItem -LiteralPath $target -Force) {
            if ($managed -notcontains $entry.Name) { throw "拒绝删除构建目录中的未登记文件：$($entry.FullName)" }
        }
    }
    $null = New-Item -ItemType Directory -Force -Path $stageRoot
    $null = New-Item -ItemType Directory -Force -Path $payload
    Write-Host "[windows-build] [$architecture] 开始版本 $version，commit $revision，工作区 dirty=$sourceDirty，源指纹 $sourceFingerprint"

    $uiOutput = Join-Path $payload 'ui'
    $null = New-Item -ItemType Directory -Force -Path $uiOutput
    $nativeBuild = Join-Path $projectRoot ".dev/windows-ui-$architecture"
    $cmakeArch = if ($architecture -eq 'arm64') { 'ARM64' } else { 'x64' }
    Invoke-Checked 'cmake' @('-S', $nativeUiSource, '-B', $nativeBuild, '-G', 'Visual Studio 17 2022', '-A', $cmakeArch, "-DWEBVIEW2_SDK_ROOT=$nativeUiSdkRoot")
    Invoke-Checked 'cmake' @('--build', $nativeBuild, '--config', 'Release')
    $hostArch = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
    if ($architecture -eq $hostArch -or ($hostArch -eq 'arm64' -and $architecture -eq 'x64')) {
        Invoke-Checked 'ctest' @('--test-dir', $nativeBuild, '-C', 'Release', '--output-on-failure')
    }
    Copy-Item -LiteralPath (Join-Path $nativeBuild 'Release/FnCPN.UI.exe') -Destination (Join-Path $uiOutput 'FnCPN.UI.exe')
    $publishedExecutable = Join-Path $uiOutput 'FnCPN.UI.exe'
    if ((Get-Item -LiteralPath $publishedExecutable).VersionInfo.ProductVersion -ne $version) {
        throw "Native UI version resource does not match product version $version"
    }
    $embeddedManifest = Join-Path $stage "FnCPN-$architecture.manifest.xml"
    Invoke-Checked $manifestTool @("-inputresource:$publishedExecutable;#1", "-out:$embeddedManifest")
    Assert-WindowsDpiAwarenessManifest $embeddedManifest
    $sourceIcon = Join-Path $projectRoot 'packaging/assets/AppIcon.ico'
    Copy-Item -LiteralPath $sourceIcon -Destination (Join-Path $uiOutput 'FnCPN.ico')
    $publishedIcon = Join-Path $uiOutput 'FnCPN.ico'
    if (-not (Test-Path -LiteralPath $publishedIcon -PathType Leaf) -or (Get-FileHash -LiteralPath $publishedIcon -Algorithm SHA256).Hash -ne (Get-FileHash -LiteralPath $sourceIcon -Algorithm SHA256).Hash) {
        throw "Published UI icon is missing or differs from the product icon: $publishedIcon"
    }
    Add-Type -AssemblyName System.Drawing
    $embeddedIcon = [Drawing.Icon]::ExtractAssociatedIcon((Join-Path $uiOutput 'FnCPN.UI.exe'))
    if ($null -eq $embeddedIcon) { throw "Native UI executable has no embedded application icon: $(Join-Path $uiOutput 'FnCPN.UI.exe')" }
    $embeddedIcon.Dispose()
    $hostArchitecture = [Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString()
    Write-Host "[windows-build] [$architecture] native UI built with the $cmakeArch toolchain (host process: $hostArchitecture)"

    $goOutput = Join-Path $payload 'fncpn.exe'
    $installerHelper = Join-Path $stage 'fncpn-installer.exe'
    $previous = @{}
    foreach ($key in @('GOOS', 'GOARCH', 'CGO_ENABLED')) { $previous[$key] = [Environment]::GetEnvironmentVariable($key, 'Process') }
    try {
        $env:GOOS = 'windows'; $env:GOARCH = $goArch; $env:CGO_ENABLED = '0'
        Invoke-Checked 'go' @('build', '-trimpath', '-ldflags', "-s -w -X github.com/rectcircle/fn-connect-private-network/internal/version.Revision=$sourceRevision", '-o', $goOutput, './cmd/fncpn')
        Invoke-Checked 'go' @('build', '-trimpath', '-ldflags', "-s -w -X github.com/rectcircle/fn-connect-private-network/internal/version.Revision=$sourceRevision", '-o', $installerHelper, './cmd/fncpn-windows-installer')
    }
    finally { foreach ($key in $previous.Keys) { [Environment]::SetEnvironmentVariable($key, $previous[$key], 'Process') } }

    $wintunDll = Join-Path $wintunRoot "bin/$goArch/wintun.dll"
    if (-not (Test-Path -LiteralPath $wintunDll -PathType Leaf)) { throw "缺少同架构 Wintun DLL：$wintunDll" }
    $signature = Get-AuthenticodeSignature -LiteralPath $wintunDll
    if ($signature.Status -ne 'Valid') { throw "Wintun Authenticode 签名无效：$($signature.Status)" }
    Copy-Item -LiteralPath $wintunDll -Destination (Join-Path $payload 'wintun.dll')
    Copy-Item -LiteralPath $wintunLicense -Destination (Join-Path $payload 'WINTUN-LICENSE.txt')
    Copy-Item -LiteralPath $webView2Bootstrapper -Destination (Join-Path $payload 'WebView2Setup.exe')
    $expectedMachine = if ($architecture -eq 'arm64') { [uint16]0xAA64 } else { [uint16]0x8664 }
    Assert-PeArchitecture $installerHelper $expectedMachine
    Assert-PeArchitecture (Join-Path $payload 'fncpn.exe') $expectedMachine
    Assert-PeArchitecture (Join-Path $payload 'wintun.dll') $expectedMachine
    Assert-PeArchitecture (Join-Path $payload 'ui/FnCPN.UI.exe') $expectedMachine
    Write-Host "[windows-build] [$architecture] Go, native UI, and Wintun PE architectures validated"

    $references = [System.Collections.Generic.List[string]]::new()
    $payloadDirectory = New-DirectoryXml $payload '' $references
    # One upgrade family prevents ARM64-emulated x64 and native ARM64 installs
    # from competing for the same SCM service and named-pipe identities.
    $serviceUpgrade = '{9E9B33D6-B243-4AA7-A054-B9F3E28A80A4}'
    $localizedProductCode = [guid]::NewGuid().ToString('B').ToUpperInvariant()
    $wxs = [Text.StringBuilder]::new()
    [void]$wxs.AppendLine('<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs" xmlns:util="http://wixtoolset.org/schemas/v4/wxs/util" xmlns:ui="http://wixtoolset.org/schemas/v4/wxs/ui">')
    [void]$wxs.AppendLine("<Package Name=`"FnCPN`" Manufacturer=`"rectcircle`" Version=`"$(Escape-Xml $installerVersion)`" UpgradeCode=`"$serviceUpgrade`" Scope=`"perMachine`" ProductCode=`"$localizedProductCode`" Language=`"!(loc.ProductLanguage)`" Codepage=`"65001`" InstallerVersion=`"500`" Compressed=`"yes`">")
    [void]$wxs.AppendLine('<SummaryInformation Codepage="1252" />')
    [void]$wxs.AppendLine('<ui:WixUI Id="WixUI_InstallDir" InstallDirectory="INSTALLFOLDER" /><WixVariable Id="WixUILicenseRtf" Value="$(var.LicenseRtf)" /><UIRef Id="WixUI_ErrorProgressText" />')
    [void]$wxs.AppendLine('<Property Id="MSIDEPLOYMENTCOMPLIANT" Value="1" />')
    [void]$wxs.AppendLine('<Property Id="MsiLogging" Value="voicewarmup!" />')
    [void]$wxs.AppendLine('<Property Id="WIXUI_EXITDIALOGOPTIONALCHECKBOXTEXT" Value="!(loc.LaunchApplication)" /><Property Id="WIXUI_EXITDIALOGOPTIONALCHECKBOX" Value="1" />')
    [void]$wxs.AppendLine('<CustomAction Id="SetLaunchFnCPNTarget" Property="WixShellExecTarget" Value="[INSTALLFOLDER]ui\FnCPN.UI.exe" />')
    [void]$wxs.AppendLine('<CustomAction Id="LaunchFnCPN" BinaryRef="Wix4UtilCA_$(sys.BUILDARCHSHORT)" DllEntry="WixShellExec" Execute="immediate" Impersonate="yes" Return="ignore" />')
    [void]$wxs.AppendLine('<UI><Publish Dialog="ExitDialog" Control="Finish" Event="DoAction" Value="SetLaunchFnCPNTarget" Order="1" Condition="WIXUI_EXITDIALOGOPTIONALCHECKBOX = 1 AND NOT Installed AND NOT REMOVE~=&quot;ALL&quot;" /><Publish Dialog="ExitDialog" Control="Finish" Event="DoAction" Value="LaunchFnCPN" Order="2" Condition="WIXUI_EXITDIALOGOPTIONALCHECKBOX = 1 AND NOT Installed AND NOT REMOVE~=&quot;ALL&quot;" /></UI>')
    [void]$wxs.AppendLine('<Property Id="ARPCONTACT" Value="rectcircle" />')
    [void]$wxs.AppendLine('<Property Id="ARPHELPLINK" Value="https://github.com/rectcircle/fn-connect-private-network/issues" />')
    [void]$wxs.AppendLine('<Property Id="ARPURLINFOABOUT" Value="https://github.com/rectcircle/fn-connect-private-network" />')
    [void]$wxs.AppendLine('<Property Id="ARPURLUPDATEINFO" Value="https://github.com/rectcircle/fn-connect-private-network/releases" />')
    [void]$wxs.AppendLine('<Property Id="ARPCOMMENTS" Value="Contact: rectcircle96@gmail.com" />')
    # HKLM's 32-bit view is SOFTWARE\WOW6432Node on both supported architectures.
    # HKCU\Software is shared; search the initiating user's runtime before elevation.
    [void]$wxs.AppendLine('<Property Id="WEBVIEW2_MACHINE_VERSION" Secure="yes"><RegistrySearch Id="WebView2MachineSearch" Root="HKLM" Key="SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" Name="pv" Type="raw" Bitness="always32" /></Property>')
    [void]$wxs.AppendLine('<Property Id="WEBVIEW2_USER_VERSION" Secure="yes"><RegistrySearch Id="WebView2UserSearch" Root="HKCU" Key="SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" Name="pv" Type="raw" Bitness="always64" /></Property>')
    [void]$wxs.AppendLine('<UI><ProgressText Action="InstallWebView2Runtime" Message="!(loc.InstallWebView2)" /><ProgressText Action="StopAllUserDaemons" Message="!(loc.StopDaemons)" /><ProgressText Action="CleanupNetwork" Message="!(loc.CleanupNetwork)" /></UI>')
    [void]$wxs.AppendLine('<Property Id="WINDOWSBUILDNUMBER" Secure="yes"><RegistrySearch Id="WindowsBuildNumberSearch" Root="HKLM" Key="SOFTWARE\Microsoft\Windows NT\CurrentVersion" Name="CurrentBuildNumber" Type="raw" Bitness="always64" /></Property>')
    [void]$wxs.AppendLine('<Launch Condition="Installed OR (VersionNT64 AND MsiNTProductType = 1 AND WINDOWSBUILDNUMBER &gt;= 19045)" Message="!(loc.WindowsRequirement)" />')
    [void]$wxs.AppendLine('<MajorUpgrade IgnoreLanguage="yes" AllowSameVersionUpgrades="yes" Schedule="afterInstallInitialize" DowngradeErrorMessage="!(loc.NewerVersionInstalled)" />')
    [void]$wxs.AppendLine('<util:CloseApplication Id="CloseFnCPNUI" Target="FnCPN.UI.exe" EndSessionMessage="yes" ElevatedEndSessionMessage="yes" Timeout="30" RebootPrompt="no" />')
    [void]$wxs.AppendLine('<Property Id="PURGE_CURRENT_USER_DATA" Secure="yes" />')
    [void]$wxs.AppendLine('<MediaTemplate EmbedCab="yes" CompressionLevel="high" />')
    [void]$wxs.AppendLine('<Binary Id="WebView2SetupBinary" SourceFile="$(var.PayloadDir)\WebView2Setup.exe" />')
    [void]$wxs.AppendLine('<Binary Id="InstallerHelperBinary" SourceFile="$(var.InstallerHelper)" />')
    [void]$wxs.AppendLine('<CustomAction Id="InstallWebView2Runtime" BinaryRef="WebView2SetupBinary" ExeCommand="/silent /install" Execute="deferred" Impersonate="no" Return="check" />')
    [void]$wxs.AppendLine('<CustomAction Id="PurgeCurrentUserData" FileRef="GoClient" ExeCommand="internal-purge-user --stop-daemon" Execute="deferred" Impersonate="yes" Return="check" />')
    [void]$wxs.AppendLine('<CustomAction Id="StopAllUserDaemons" BinaryRef="InstallerHelperBinary" ExeCommand="stop-daemons" Execute="deferred" Impersonate="no" Return="check" />')
    [void]$wxs.AppendLine('<CustomAction Id="StopCurrentUserDaemon" BinaryRef="InstallerHelperBinary" ExeCommand="stop-current-daemon" Execute="immediate" Impersonate="yes" Return="check" /><InstallExecuteSequence><Custom Action="StopCurrentUserDaemon" Before="InstallValidate" Condition="WIX_UPGRADE_DETECTED" /></InstallExecuteSequence>')
    [void]$wxs.AppendLine('<CustomAction Id="CleanupNetwork" BinaryRef="InstallerHelperBinary" ExeCommand="cleanup-network" Execute="deferred" Impersonate="no" Return="check" />')
    [void]$wxs.AppendLine("<InstallExecuteSequence><Custom Action=`"InstallWebView2Runtime`" Before=`"InstallServices`" Condition=`"$(Escape-Xml $webView2InstallCondition)`" /><Custom Action=`"override Wix4CloseApplications_$wixArchShort`" Before=`"StopAllUserDaemons`" Condition=`"WIX_UPGRADE_DETECTED`" /><Custom Action=`"StopAllUserDaemons`" Before=`"InstallFiles`" Condition=`"WIX_UPGRADE_DETECTED`" /><Custom Action=`"CleanupNetwork`" Before=`"RemoveFiles`" Condition=`"REMOVE~=&quot;ALL&quot;`" /><Custom Action=`"PurgeCurrentUserData`" Before=`"RemoveFiles`" Condition=`"REMOVE~=&quot;ALL&quot; AND PURGE_CURRENT_USER_DATA=&quot;1&quot; AND NOT UPGRADINGPRODUCTCODE`" /></InstallExecuteSequence>")
    [void]$wxs.AppendLine('<StandardDirectory Id="ProgramFiles64Folder"><Directory Id="INSTALLFOLDER" Name="FnCPN-' + $architecture + '">')
    [void]$wxs.AppendLine('<Component Id="ClientService" Guid="*"><File Id="GoClient" Source="$(var.PayloadDir)\fncpn.exe" KeyPath="yes" />')
    [void]$wxs.AppendLine('<ServiceInstall Id="FnCPNServiceInstall" Name="FnCPNClient" DisplayName="!(loc.ServiceName)" Description="!(loc.ServiceDescription)" Type="ownProcess" Start="auto" Account="LocalSystem" ErrorControl="normal" Arguments="client privileged-daemon"><util:ServiceConfig FirstFailureActionType="restart" SecondFailureActionType="restart" ThirdFailureActionType="restart" RestartServiceDelayInSeconds="5" ResetPeriodInDays="1"/></ServiceInstall>')
    [void]$wxs.AppendLine('<ServiceControl Id="FnCPNServiceControl" Name="FnCPNClient" Start="install" Stop="both" Remove="uninstall" Wait="yes" /></Component>')
    [void]$wxs.Append($payloadDirectory)
    [void]$wxs.AppendLine('<Component Id="ProtocolRegistration" Directory="INSTALLFOLDER" Guid="*"><RegistryValue Root="HKCR" Key="fncpn" Type="string" Value="URL:FnCPN authorization protocol" KeyPath="yes"/><RegistryValue Root="HKCR" Key="fncpn" Name="URL Protocol" Type="string" Value=""/><RegistryValue Root="HKCR" Key="fncpn\shell\open\command" Type="string" Value="&quot;[INSTALLFOLDER]ui\FnCPN.UI.exe&quot; &quot;%1&quot;"/></Component>')
    [void]$wxs.AppendLine('<Component Id="CliPath" Directory="INSTALLFOLDER" Guid="*"><Environment Id="FnCPNPath" Name="PATH" Value="[INSTALLFOLDER]" Permanent="no" Part="last" Action="set" System="yes"/><RegistryValue Root="HKLM" Key="Software\FnCPN" Name="InstallPath" Type="string" Value="[INSTALLFOLDER]" KeyPath="yes"/><RegistryValue Root="HKLM" Key="Software\FnCPN" Name="ProductCode" Type="string" Value="[ProductCode]"/></Component>')
    [void]$wxs.AppendLine('<Component Id="StartMenu" Directory="INSTALLFOLDER" Guid="*"><Shortcut Id="FnCPNShortcut" Name="FnCPN" Target="[INSTALLFOLDER]ui\FnCPN.UI.exe" WorkingDirectory="INSTALLFOLDER" Directory="ProgramMenuFolder"/><Shortcut Id="FnCPNDesktopShortcut" Name="FnCPN" Target="[INSTALLFOLDER]ui\FnCPN.UI.exe" WorkingDirectory="INSTALLFOLDER" Directory="DesktopFolder"/><RegistryValue Root="HKLM" Key="Software\FnCPN" Name="ShortcutInstalled" Type="integer" Value="1" KeyPath="yes"/><RemoveFolder Id="RemoveFnCPNShortcut" Directory="ProgramMenuFolder" On="uninstall"/></Component>')
    [void]$wxs.AppendLine('</Directory></StandardDirectory>')
    [void]$wxs.AppendLine('<Feature Id="Main" Title="FnCPN" Level="1"><ComponentRef Id="ClientService"/><ComponentRef Id="ProtocolRegistration"/><ComponentRef Id="CliPath"/><ComponentRef Id="StartMenu"/>')
    foreach ($reference in $references) { [void]$wxs.AppendLine("<ComponentRef Id=`"$reference`" />") }
    [void]$wxs.AppendLine('</Feature>')
    $brandIcon = [Drawing.Image]::FromFile((Join-Path $projectRoot 'packaging/assets/AppIcon.iconset/icon_512x512@2x.png'))
    foreach ($asset in @(@('Dialog',493,312),@('Banner',493,58))) {
        # MSI bitmap controls scale to their authored bounds. Supply 3x artwork
        # rather than enlarging the default 32px frame selected from the ICO.
        $bitmap = [Drawing.Bitmap]::new($asset[1]*3,$asset[2]*3)
        $graphics = [Drawing.Graphics]::FromImage($bitmap)
        $graphics.InterpolationMode = [Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $graphics.PixelOffsetMode = [Drawing.Drawing2D.PixelOffsetMode]::HighQuality
        $graphics.ScaleTransform(3,3)
        $graphics.Clear([Drawing.Color]::White)
        if ($asset[0] -eq 'Dialog') {
            $brush = [Drawing.SolidBrush]::new([Drawing.Color]::FromArgb(240,246,255))
            $graphics.FillRectangle($brush,0,0,164,312); $brush.Dispose()
            $graphics.DrawImage($brandIcon,[Drawing.Rectangle]::new(46,46,72,72))
        } else { $graphics.DrawImage($brandIcon,[Drawing.Rectangle]::new(438,9,40,40)) }
        $bitmapPath = Join-Path $stage ($asset[0]+'.bmp')
        $bitmap.Save($bitmapPath,[Drawing.Imaging.ImageFormat]::Bmp)
        $graphics.Dispose(); $bitmap.Dispose()
        [void]$wxs.AppendLine('<WixVariable Id="WixUI'+$asset[0]+'Bmp" Value="'+(Escape-Xml $bitmapPath)+'" />')
    }
    $brandIcon.Dispose()
    [void]$wxs.AppendLine('</Package></Wix>')
    $source = Join-Path $stage 'Package.wxs'
    [IO.File]::WriteAllText($source, $wxs.ToString(), [Text.UTF8Encoding]::new($false))

    $licenseRtf = Join-Path $stage 'License.rtf'
    $licenseText = (Get-Content (Join-Path $projectRoot 'LICENSE') -Raw).Replace('\', '\\').Replace('{', '\{').Replace('}', '\}').Replace("`r", '').Replace("`n", '\par ' )
    [IO.File]::WriteAllText($licenseRtf, ('{\rtf1\ansi ' + $licenseText + '}'), [Text.Encoding]::ASCII)
    $msi = Join-Path $stage "fncpn-$version-windows-$architecture-unsigned.msi"
    Invoke-Checked $wix @('-acceptEula', 'wix7', 'build', $source, '-culture', 'en-US', '-loc', (Join-Path $projectRoot 'packaging/windows/installer.en-US.wxl'), '-arch', $architecture, '-ext', $wixUtilExtension, '-ext', $wixUiExtension, '-d', "LicenseRtf=$licenseRtf", '-d', "PayloadDir=$payload", '-d', "InstallerHelper=$installerHelper", '-o', $msi, '-intermediatefolder', (Join-Path $stage 'wix-obj'))
    $chineseMsi = Join-Path $stage 'FnCPN.zh-CN.msi'
    Invoke-Checked $wix @('-acceptEula', 'wix7', 'build', $source, '-culture', 'zh-CN', '-loc', (Join-Path $projectRoot 'packaging/windows/installer.zh-CN.wxl'), '-arch', $architecture, '-ext', $wixUtilExtension, '-ext', $wixUiExtension, '-d', "LicenseRtf=$licenseRtf", '-d', "PayloadDir=$payload", '-d', "InstallerHelper=$installerHelper", '-o', $chineseMsi, '-intermediatefolder', (Join-Path $stage 'wix-obj-zh'))
    $chineseTransform = Join-Path $stage '2052.mst'
    Hide-CompletionNavigation $msi
    Hide-CompletionNavigation $chineseMsi 'zh-CN'
    Invoke-Checked $wix @('-acceptEula', 'wix7', 'msi', 'transform', '-t', 'language', $msi, $chineseMsi, '-o', $chineseTransform)
    & (Join-Path $PSScriptRoot 'windows/embed-installer-language.ps1') -MsiPath $msi -TransformPath $chineseTransform -ChineseMsiPath $chineseMsi

    Assert-MsiUpgradeMetadata $msi $architecture $installerVersion $installerHelper
    $recoveryValidationSource = Join-Path $stage 'RecoveryValidation.wxs'
    Invoke-Checked $wix @('-acceptEula', 'wix7', 'msi', 'decompile', $msi, '-o', $recoveryValidationSource)
    $recoveryValidation = Get-Content -LiteralPath $recoveryValidationSource -Raw
    $recoveryTable = [regex]::Match($recoveryValidation, '(?s)<CustomTable Id="Wix4ServiceConfig">.*?</CustomTable>')
    if (-not $recoveryTable.Success) { throw "[$architecture] MSI is missing the WiX Util ServiceConfig failure-actions table" }
    foreach ($expected in @(
            '<Data Column="ServiceName" Value="FnCPNClient" />',
            '<Data Column="Component_" Value="ClientService" />',
            '<Data Column="FirstFailureActionType" Value="restart" />',
            '<Data Column="SecondFailureActionType" Value="restart" />',
            '<Data Column="ThirdFailureActionType" Value="restart" />',
            '<Data Column="ResetPeriodInDays" Value="1" />',
            '<Data Column="RestartServiceDelayInSeconds" Value="5" />'
        )) {
        if (-not $recoveryTable.Value.Contains($expected)) { throw "[$architecture] MSI ServiceConfig recovery policy is missing: $expected" }
    }
    if ($recoveryValidation -notmatch '<Custom Action="Wix4SchedServiceConfig_[^"]+"[^>]*After="InstallServices"') {
        throw "[$architecture] MSI ServiceConfig must be applied after installing and before starting FnCPNClient"
    }
    Write-Host "[windows-build] [$architecture] SCM recovery policy validated: restart x3, 5s install default, 1-day reset"
    $manifest = [ordered]@{
        productVersion = $version; installerVersion = $installerVersion; commit = $revision; workingTreeDirty = $sourceDirty; sourceFingerprint = $sourceFingerprint; architecture = $architecture
        installerLanguages = @('en-US', 'zh-CN')
        installerHelperBytes = (Get-Item -LiteralPath $installerHelper).Length; goDebugInfo = $false
        uiTechnology = 'Win32'; uiRuntime = 'static MSVC'; webViewRuntime = 'shared Evergreen'
        builtAtUtc = [DateTime]::UtcNow.ToString('o'); signed = $false
        managedPaths = @((Split-Path -Leaf $msi), 'payload')
        artifacts = @([ordered]@{ path = (Split-Path -Leaf $msi); sha256 = (Get-FileHash -LiteralPath $msi -Algorithm SHA256).Hash })
    }
    $manifestPath = Join-Path $stage 'build-manifest.json'
    [IO.File]::WriteAllText($manifestPath, ($manifest | ConvertTo-Json -Depth 5), [Text.UTF8Encoding]::new($false))
    $next = "$target.next.$PID.$([guid]::NewGuid().ToString('N'))"
    try {
        $null = New-Item -ItemType Directory -Force -Path $next
        Copy-Item -LiteralPath $payload -Destination (Join-Path $next 'payload') -Recurse
        Copy-Item -LiteralPath $msi -Destination $next
        Copy-Item -LiteralPath $manifestPath -Destination $next
        Publish-WindowsBuildDirectory $target $next $architecture
    }
    finally {
        if (Test-Path -LiteralPath $next) {
            try { Remove-Item -LiteralPath $next -Recurse -Force }
            catch { Write-Warning "[$architecture] 本次构建的临时输出无法清理，保留供诊断：$next ($($_.Exception.Message))" }
        }
    }
    $resolvedStage = [IO.Path]::GetFullPath($stage)
    if (-not $resolvedStage.StartsWith([IO.Path]::GetFullPath($stageRoot) + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Temporary stage path is outside the expected workspace staging directory'
    }
    try { Remove-Item -LiteralPath $resolvedStage -Recurse -Force }
    catch { Write-Warning "[$architecture] 构建已完成；暂存文件仍被占用，保留供诊断：$resolvedStage" }
    & (Join-Path $PSScriptRoot 'windows/test-msi-platform.ps1') -Architecture $architecture -PackageRoot $packageRoot
    Write-Host "[windows-build] [$architecture] 完成，阶段耗时 $($timer.Elapsed.ToString())；产物：$(Join-Path $target (Split-Path -Leaf $msi))"
}

Write-Host "[windows-build] 全部完成。产物在 $packageRoot/<architecture>/；均为未签名本地候选，不会上传或创建 Release。"
