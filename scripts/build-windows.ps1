#Requires -Version 7.0
[CmdletBinding()]
param([ValidateSet('all', 'arm64', 'x64')][string]$Architecture = 'all')

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$version = (Get-Content (Join-Path $projectRoot 'internal/version/VERSION') -Raw).Trim()
# Windows Installer requires a numeric version; retain RC identity in binaries and filenames.
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
$packageProject = Join-Path $projectRoot 'platform/windows/FnCPN.Windows/FnCPN.Windows.csproj'
$architectures = if ($Architecture -eq 'all') { @('arm64', 'x64') } else { @($Architecture) }

function Invoke-Checked([string]$File, [string[]]$Arguments) {
    & $File @Arguments
    if ($LASTEXITCODE -ne 0) { throw "命令失败 ($LASTEXITCODE): $File $($Arguments -join ' ')" }
}

function Escape-Xml([string]$Value) { [Security.SecurityElement]::Escape($Value) }

function Assert-WindowsDpiAwarenessManifest([string]$ManifestPath) {
    [xml]$manifest = Get-Content -LiteralPath $ManifestPath -Raw
    $dpiAware = $manifest.SelectSingleNode("//*[local-name()='dpiAware' and namespace-uri()='http://schemas.microsoft.com/SMI/2005/WindowsSettings']")
    $dpiAwareness = $manifest.SelectSingleNode("//*[local-name()='dpiAwareness' and namespace-uri()='http://schemas.microsoft.com/SMI/2016/WindowsSettings']")
    if ($null -eq $dpiAware -or $dpiAware.InnerText -ne 'true/pm' -or
        $null -eq $dpiAwareness -or $dpiAwareness.InnerText -ne 'PerMonitorV2') {
        throw 'WPF application manifest must declare Per-Monitor V2 DPI awareness with a legacy per-monitor fallback'
    }
    Write-Host '[windows-build] WPF manifest PerMonitorV2 DPI awareness validated'
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

function Assert-WindowsFnIdNormalization([string]$AssemblyPath, [string]$ProductVersion, [string]$AssetDirectory) {
    # Load bytes so the WPF assembly does not stay file-locked through MSI packaging/stage cleanup.
    $assembly = [Reflection.Assembly]::Load([IO.File]::ReadAllBytes($AssemblyPath))
    $versionAttribute = $assembly.GetCustomAttributesData() | Where-Object { $_.AttributeType -eq [Reflection.AssemblyInformationalVersionAttribute] } | Select-Object -First 1
    if ($null -eq $versionAttribute -or $versionAttribute.ConstructorArguments.Count -ne 1) {
        throw 'WPF product informational version is missing'
    }
    $embeddedVersion = ([string]$versionAttribute.ConstructorArguments[0].Value).Split('+', 2)[0]
    if ($embeddedVersion -ne $ProductVersion) {
        throw "WPF product version '$embeddedVersion' does not match '$ProductVersion'"
    }
    Write-Host "[windows-build] WPF product version passed: $embeddedVersion"
    $appType = $assembly.GetType('FnCPN.Windows.App', $true)
    $instanceMutexName = $appType.GetMethod('InstanceMutexName', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $instanceMutexName) { throw 'WPF per-session single-instance identity was not found' }
    $sameSession = $instanceMutexName.Invoke($null, [object[]]@('S-1-5-21-test', 4))
    $sameSessionAgain = $instanceMutexName.Invoke($null, [object[]]@('S-1-5-21-test', 4))
    $otherSession = $instanceMutexName.Invoke($null, [object[]]@('S-1-5-21-test', 5))
    $otherUser = $instanceMutexName.Invoke($null, [object[]]@('S-1-5-21-other', 4))
    if ($sameSession -ne $sameSessionAgain -or $sameSession -eq $otherSession -or $sameSession -eq $otherUser) {
        throw 'WPF single-instance mutex identity must be stable per user and session'
    }
    Write-Host '[windows-build] per-user/session single-instance identity cases passed: 4'

    $activationPeerSessionMatches = $appType.GetMethod('ActivationPeerSessionMatches', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $activationPeerSessionMatches) { throw 'WPF activation pipe session check was not found' }
    $sameActivationSession = $activationPeerSessionMatches.Invoke($null, [object[]]@([uint32]4, [uint32]4))
    $differentActivationSession = $activationPeerSessionMatches.Invoke($null, [object[]]@([uint32]4, [uint32]5))
    if (-not [bool]$sameActivationSession -or [bool]$differentActivationSession) {
        throw 'WPF activation pipe must reject clients outside the owning Windows session'
    }
    Write-Host '[windows-build] activation pipe client session cases passed: 2'

    $ipcType = $assembly.GetType('FnCPN.Windows.IPC', $true)
    $timeoutForMethod = $ipcType.GetMethod('TimeoutForMethod', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $timeoutForMethod) { throw 'WPF IPC method timeout policy was not found' }
    foreach ($case in @(
        [pscustomobject]@{ Method = 'authorize-native'; Seconds = 310 }
        [pscustomobject]@{ Method = 'watch-client-status'; Seconds = 310 }
        [pscustomobject]@{ Method = 'connect'; Seconds = 45 }
        [pscustomobject]@{ Method = 'retry'; Seconds = 45 }
        [pscustomobject]@{ Method = 'diagnose'; Seconds = 30 }
    )) {
        $actual = $timeoutForMethod.Invoke($null, [object[]]@($case.Method))
        if ($actual.TotalSeconds -ne $case.Seconds) {
            throw "IPC timeout mismatch for '$($case.Method)': got $($actual.TotalSeconds), expected $($case.Seconds)"
        }
    }
    Write-Host '[windows-build] method-specific IPC deadline cases passed: 5'

    $validateResponseEnvelope = $ipcType.GetMethod('ValidateResponseEnvelope', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $validateResponseEnvelope) { throw 'WPF IPC response envelope validator was not found' }
    $responseCases = @(
        [pscustomobject]@{ Name = 'valid success with unknown fields'; Json = '{"productVersion":"' + $ProductVersion + '","version":4,"id":"request","ok":true,"result":{"future":true},"futureField":17}'; Valid = $true }
        [pscustomobject]@{ Name = 'valid typed failure'; Json = '{"productVersion":"' + $ProductVersion + '","version":4,"id":"request","ok":false,"error":{"code":"UNAVAILABLE","message":"offline"}}'; Valid = $true }
        [pscustomobject]@{ Name = 'product version mismatch'; Json = '{"productVersion":"99.0.0","version":4,"id":"request","ok":true}'; Valid = $false }
        [pscustomobject]@{ Name = 'duplicate response field'; Json = '{"productVersion":"' + $ProductVersion + '","version":4,"id":"request","id":"request","ok":true}'; Valid = $false }
        [pscustomobject]@{ Name = 'missing failure details'; Json = '{"productVersion":"' + $ProductVersion + '","version":4,"id":"request","ok":false}'; Valid = $false }
        [pscustomobject]@{ Name = 'failure with result'; Json = '{"productVersion":"' + $ProductVersion + '","version":4,"id":"request","ok":false,"result":null,"error":{"code":"UNAVAILABLE","message":"offline"}}'; Valid = $false }
        [pscustomobject]@{ Name = 'failure with unknown code'; Json = '{"productVersion":"' + $ProductVersion + '","version":4,"id":"request","ok":false,"error":{"code":"FUTURE_CODE","message":"unsupported"}}'; Valid = $false }
        [pscustomobject]@{ Name = 'failure with duplicate error field'; Json = '{"productVersion":"' + $ProductVersion + '","version":4,"id":"request","ok":false,"error":{"code":"UNAVAILABLE","code":"INTERNAL","message":"offline"}}'; Valid = $false }
        [pscustomobject]@{ Name = 'success with error'; Json = '{"productVersion":"' + $ProductVersion + '","version":4,"id":"request","ok":true,"error":{"code":"INTERNAL","message":"bad"}}'; Valid = $false }
        [pscustomobject]@{ Name = 'mismatched request ID'; Json = '{"productVersion":"' + $ProductVersion + '","version":4,"id":"other","ok":true}'; Valid = $false }
    )
    foreach ($case in $responseCases) {
        $document = [Text.Json.JsonDocument]::Parse($case.Json)
        try {
            $threw = $false
            try { $validateResponseEnvelope.Invoke($null, [object[]]@($document.RootElement, 'request')) | Out-Null }
            catch { $threw = $true }
            if ($case.Valid -and $threw) { throw "Valid IPC response was rejected: $($case.Name)" }
            if (-not $case.Valid -and -not $threw) { throw "Invalid IPC response was accepted: $($case.Name)" }
        }
        finally { $document.Dispose() }
    }
    Write-Host "[windows-build] strict WPF IPC response envelope cases passed: $($responseCases.Count)"

    $windowType = $assembly.GetType('FnCPN.Windows.MainWindow', $true)
    $method = $windowType.GetMethod('NormalizeFnId', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $method) { throw 'WPF FN ID normalization method was not found' }
    $cases = @(
        [pscustomobject]@{ Input = 'HOME-NAS'; Expected = 'home-nas' }
        [pscustomobject]@{ Input = 'https://fnos.net/home-nas?from=share'; Expected = 'home-nas' }
        [pscustomobject]@{ Input = 'https://home-nas.fnos.net/path'; Expected = 'home-nas' }
        [pscustomobject]@{ Input = 'home-nas.fnos.net'; Expected = 'home-nas' }
        [pscustomobject]@{ Input = 'https://fnos.net/foo%2Fbar'; Expected = $null }
        [pscustomobject]@{ Input = '-invalid'; Expected = $null }
        [pscustomobject]@{ Input = 'other.example.com'; Expected = $null }
    )
    foreach ($case in $cases) {
        $actual = $method.Invoke($null, [object[]]@($case.Input))
        if (-not [object]::Equals($actual, $case.Expected)) {
            throw "FN ID normalization mismatch for '$($case.Input)': got '$actual', expected '$($case.Expected)'"
        }
    }
    Write-Host "[windows-build] FN ID normalization cases passed: $($cases.Count)"

    $parseAuthorizationUri = $windowType.GetMethod('TryParseAuthorizationUri', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $parseAuthorizationUri) { throw 'WPF authorization URI parser was not found' }
    $activationCases = @(
        [pscustomobject]@{ Input = 'fncpn://authorize/HOME-NAS?request=req-1'; Valid = $true; FnId = 'home-nas'; Request = 'req-1' }
        [pscustomobject]@{ Input = 'fncpn://authorize/home-nas?request=first&request=second'; Valid = $true; FnId = 'home-nas'; Request = 'first' }
        [pscustomobject]@{ Input = 'fncpn://authorize/home-nas'; Valid = $false; FnId = ''; Request = '' }
        [pscustomobject]@{ Input = 'fncpn://authorize/-invalid?request=req-1'; Valid = $false; FnId = ''; Request = '' }
        [pscustomobject]@{ Input = 'fncpn://other/home-nas?request=req-1'; Valid = $false; FnId = ''; Request = '' }
        [pscustomobject]@{ Input = 'https://authorize/home-nas?request=req-1'; Valid = $false; FnId = ''; Request = '' }
    )
    foreach ($case in $activationCases) {
        $arguments = [object[]]@($case.Input, '', '')
        $actual = $parseAuthorizationUri.Invoke($null, $arguments)
        if ([bool]$actual -ne $case.Valid -or $arguments[1] -ne $case.FnId -or $arguments[2] -ne $case.Request) {
            throw "Authorization URI parser mismatch for '$($case.Input)': valid='$actual', FN ID='$($arguments[1])', request='$($arguments[2])'"
        }
    }
    Write-Host "[windows-build] authorization URI validation cases passed: $($activationCases.Count)"

    $replacedAuthorizationRequest = $windowType.GetMethod('ReplacedAuthorizationRequest', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $replacedAuthorizationRequest) { throw 'WPF authorization transaction replacement classifier was not found' }
    $replacementCases = @(
        [pscustomobject]@{ Current = 'old-request'; Incoming = 'new-request'; Expected = 'old-request' }
        [pscustomobject]@{ Current = 'same-request'; Incoming = 'same-request'; Expected = $null }
        [pscustomobject]@{ Current = $null; Incoming = 'new-request'; Expected = $null }
    )
    foreach ($case in $replacementCases) {
        $actual = $replacedAuthorizationRequest.Invoke($null, [object[]]@($case.Current, $case.Incoming))
        if (-not [object]::Equals($actual, $case.Expected)) {
            throw "Authorization replacement mismatch for '$($case.Current)' -> '$($case.Incoming)'"
        }
    }
    Write-Host "[windows-build] authorization replacement cases passed: $($replacementCases.Count)"

    $failureCode = $windowType.GetMethod('FailureCode', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $failureCode) { throw 'WPF IPC failure classifier was not found' }
    foreach ($case in @(
        [pscustomobject]@{ Json = '{"code":"AUTH_REQUIRED","message":"expired"}'; Expected = 'AUTH_REQUIRED' }
        [pscustomobject]@{ Json = '{"code":"UNAVAILABLE","message":"offline"}'; Expected = 'UNAVAILABLE' }
        [pscustomobject]@{ Json = '{"message":"missing code"}'; Expected = $null }
    )) {
        $document = [System.Text.Json.JsonDocument]::Parse($case.Json)
        try {
            $actual = $failureCode.Invoke($null, [object[]]@($document.RootElement))
            if (-not [object]::Equals($actual, $case.Expected)) {
                throw "IPC failure classification mismatch: got '$actual', expected '$($case.Expected)'"
            }
        }
        finally { $document.Dispose() }
    }
    Write-Host '[windows-build] IPC failure classification cases passed: 3'

    $adminProxyUrlValidator = $windowType.GetMethod('IsValidAdminProxyUrl', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $adminProxyUrlValidator) { throw 'WPF admin proxy URL validator was not found' }
    $adminProxyToken = '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
    $adminProxyCases = @(
        [pscustomobject]@{ Url = "http://127.0.0.1:49152/app/fncpn?fncpn_proxy=$adminProxyToken"; Expected = $true }
        [pscustomobject]@{ Url = "https://127.0.0.1:49152/app/fncpn?fncpn_proxy=$adminProxyToken"; Expected = $false }
        [pscustomobject]@{ Url = "http://192.0.2.1:49152/app/fncpn?fncpn_proxy=$adminProxyToken"; Expected = $false }
        [pscustomobject]@{ Url = "http://localhost:49152/app/fncpn?fncpn_proxy=$adminProxyToken"; Expected = $false }
        [pscustomobject]@{ Url = "http://127.0.0.1:49152/elsewhere?fncpn_proxy=$adminProxyToken"; Expected = $false }
        [pscustomobject]@{ Url = 'http://127.0.0.1:49152/app/fncpn?fncpn_proxy=short'; Expected = $false }
        [pscustomobject]@{ Url = ('http://127.0.0.1:49152/app/fncpn?fncpn_proxy=' + ('z' * 64)); Expected = $false }
        [pscustomobject]@{ Url = "http://127.0.0.1:49152/app/fncpn?fncpn_proxy=$adminProxyToken&extra=1"; Expected = $false }
        [pscustomobject]@{ Url = "http://user@127.0.0.1:49152/app/fncpn?fncpn_proxy=$adminProxyToken"; Expected = $false }
        [pscustomobject]@{ Url = "http://127.0.0.1:49152/app/fncpn?fncpn_proxy=$adminProxyToken#external"; Expected = $false }
    )
    foreach ($case in $adminProxyCases) {
        $actual = [bool]$adminProxyUrlValidator.Invoke($null, [object[]]@($case.Url))
        if ($actual -ne $case.Expected) { throw "Admin proxy URL validation mismatch for '$($case.Url)': got '$actual'" }
    }
    Write-Host "[windows-build] admin proxy URL validation cases passed: $($adminProxyCases.Count)"

    $adminNavigationValidator = $windowType.GetMethod('IsAllowedAdminNavigation', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $adminNavigationValidator) { throw 'WPF admin navigation boundary validator was not found' }
    $adminOrigin = 'http://127.0.0.1:49152'
    $adminNavigationCases = @(
        [pscustomobject]@{ Url = 'http://127.0.0.1:49152/app/fncpn'; Expected = $true }
        [pscustomobject]@{ Url = 'http://127.0.0.1:49152/app/fncpn/devices?tab=networks'; Expected = $true }
        [pscustomobject]@{ Url = 'http://127.0.0.1:49152/'; Expected = $false }
        [pscustomobject]@{ Url = 'http://127.0.0.1:49152/app/fncpn-evil'; Expected = $false }
        [pscustomobject]@{ Url = 'http://127.0.0.1:49152/app/fncpn/%2e%2e/admin'; Expected = $false }
        [pscustomobject]@{ Url = 'http://127.0.0.1:49153/app/fncpn'; Expected = $false }
        [pscustomobject]@{ Url = 'http://localhost:49152/app/fncpn'; Expected = $false }
        [pscustomobject]@{ Url = 'http://127.0.0.2:49152/app/fncpn'; Expected = $false }
        [pscustomobject]@{ Url = 'http://192.0.2.1:49152/app/fncpn'; Expected = $false }
        [pscustomobject]@{ Url = 'https://127.0.0.1:49152/app/fncpn'; Expected = $false }
        [pscustomobject]@{ Url = 'http://user@127.0.0.1:49152/app/fncpn'; Expected = $false }
    )
    foreach ($case in $adminNavigationCases) {
        $actual = [bool]$adminNavigationValidator.Invoke($null, [object[]]@($case.Url, $adminOrigin))
        if ($actual -ne $case.Expected) { throw "Admin navigation boundary mismatch for '$($case.Url)': got '$actual'" }
    }
    Write-Host "[windows-build] admin navigation boundary cases passed: $($adminNavigationCases.Count)"

    $externalAdminLinkSanitizer = $windowType.GetMethod('SanitizeExternalAdminLink', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $externalAdminLinkSanitizer) { throw 'WPF external admin link sanitizer was not found' }
    foreach ($url in @(
        'https://example.com/help?fncpn_proxy=secret&next=1'
        'https://example.com/help?FNCPN_PROXY=secret&next=1'
        'javascript:alert(1)'
        'file:///C:/Windows/win.ini'
        'https://user@example.com/help'
    )) {
        $actual = $externalAdminLinkSanitizer.Invoke($null, [object[]]@($url))
        if ($url.StartsWith('https://example.com/', [StringComparison]::Ordinal)) {
            $sanitizedUri = [Uri]$actual
            $sanitizedQuery = [System.Web.HttpUtility]::ParseQueryString($sanitizedUri.Query)
            if ($sanitizedUri.Host -ne 'example.com' -or $sanitizedQuery['fncpn_proxy'] -or $sanitizedQuery['next'] -ne '1') {
                throw "External admin link sanitizer retained the local token or removed expected query data: $actual"
            }
        }
        elseif ($null -ne $actual) { throw "Unsafe external admin link was accepted: $url -> $actual" }
    }
    Write-Host '[windows-build] external admin link sanitization cases passed: 5'

    $localAppDataResolver = $windowType.GetMethod('GetLocalAppDataDirectory', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $localAppDataResolver) { throw 'WPF LocalAppData path resolver was not found' }
    $previousLocalAppData = [Environment]::GetEnvironmentVariable('LOCALAPPDATA', 'Process')
    try {
        $testLocalAppData = Join-Path $env:TEMP 'FnCPN-localappdata-contract'
        [Environment]::SetEnvironmentVariable('LOCALAPPDATA', $testLocalAppData, 'Process')
        $actualLocalAppData = $localAppDataResolver.Invoke($null, @())
        if ($actualLocalAppData -ne $testLocalAppData) { throw "WPF LocalAppData resolver ignored LOCALAPPDATA: '$actualLocalAppData'" }
        [Environment]::SetEnvironmentVariable('LOCALAPPDATA', ' ', 'Process')
        $actualFallbackPath = $localAppDataResolver.Invoke($null, @())
        $expectedFallbackPath = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)
        if ($actualFallbackPath -ne $expectedFallbackPath) { throw "WPF LocalAppData fallback mismatch: '$actualFallbackPath'" }
    }
    finally { [Environment]::SetEnvironmentVariable('LOCALAPPDATA', $previousLocalAppData, 'Process') }
    Write-Host '[windows-build] LocalAppData resolver cases passed: environment override and shell-folder fallback'

    $formatException = $windowType.GetMethod('FormatException', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $formatException) { throw 'WPF exception presentation mapper was not found' }
    $previousCulture = [Globalization.CultureInfo]::CurrentUICulture
    try {
        foreach ($case in @(
            [pscustomobject]@{ Culture = 'zh-Hans'; Expected = '操作超时，请重试。' }
            [pscustomobject]@{ Culture = 'en-US'; Expected = 'Operation timed out. Try again.' }
        )) {
            [Globalization.CultureInfo]::CurrentUICulture = [Globalization.CultureInfo]::GetCultureInfo($case.Culture)
            $actual = $formatException.Invoke($null, [object[]]@([TimeoutException]::new('method timed out')))
            if ($actual -ne $case.Expected) { throw "Timeout message mismatch for $($case.Culture): '$actual'" }
        }
    }
    finally { [Globalization.CultureInfo]::CurrentUICulture = $previousCulture }
    Write-Host '[windows-build] timeout error localization cases passed: 2'

    $needsLogin = $windowType.GetMethod('NeedsLogin', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $needsLogin) { throw 'WPF authentication-state classifier was not found' }
    $remoteErrorType = $assembly.GetType('FnCPN.Windows.RemoteError', $true)
    $statusType = $assembly.GetType('FnCPN.Windows.ClientStatus', $true)
    $errorConstructor = $remoteErrorType.GetConstructors() | Sort-Object { $_.GetParameters().Count } -Descending | Select-Object -First 1
    $statusConstructor = $statusType.GetConstructors() | Sort-Object { $_.GetParameters().Count } -Descending | Select-Object -First 1
    $cases = @(
        [pscustomobject]@{ State = 'AUTH_REQUIRED'; Code = $null; Operation = $null; HttpStatus = $null; Expected = $true }
        [pscustomobject]@{ State = 'ERROR'; Code = 'DEVICE_REVOKED'; Operation = 'authorization'; HttpStatus = 403; Expected = $true }
        [pscustomobject]@{ State = 'ERROR'; Code = 'PERMISSION_DENIED'; Operation = 'authorization.begin'; HttpStatus = 403; Expected = $true }
        [pscustomobject]@{ State = 'ERROR'; Code = 'PERMISSION_DENIED'; Operation = 'credentials.read'; HttpStatus = 403; Expected = $false }
        [pscustomobject]@{ State = 'ERROR'; Code = 'UNAVAILABLE'; Operation = 'admin-proxy'; HttpStatus = 503; Expected = $false }
    )
    foreach ($case in $cases) {
        $remoteError = $null
        if ($case.Code) {
            $remoteError = $errorConstructor.Invoke([object[]]@(
                $case.Code, 'test', $null, $case.Operation, $null, $case.HttpStatus, $null, $null, $null, $null
            ))
        }
        $status = $statusConstructor.Invoke([object[]]@(
            $case.State, $null, $null, 0, $null, $remoteError, $false, $null
        ))
        $actual = $needsLogin.Invoke($null, [object[]]@($status))
        if ([bool]$actual -ne $case.Expected) {
            throw "Authentication state mismatch for $($case.Code)/$($case.Operation): got '$actual', expected '$($case.Expected)'"
        }
    }
    Write-Host "[windows-build] authentication state cases passed: $($cases.Count)"

    $continueRemoteFromLogin = $windowType.GetMethod('ShouldContinueRemoteFromLogin', [Reflection.BindingFlags]'NonPublic,Static')
    $loginPrimaryLabel = $windowType.GetMethod('LoginPrimaryLabel', [Reflection.BindingFlags]'NonPublic,Static')
    $appStringsType = $assembly.GetType('FnCPN.Windows.AppStrings', $true)
    $getLocalizedString = $appStringsType.GetMethod('Get', [Reflection.BindingFlags]'Public,Static')
    if ($null -eq $continueRemoteFromLogin -or $null -eq $loginPrimaryLabel -or $null -eq $getLocalizedString) {
        throw 'WPF local-network continuation mapper or localization accessor was not found'
    }
    $loginPrimaryCases = @(
        [pscustomobject]@{ Status = $null; Expected = '登录并连接' }
        [pscustomobject]@{ Status = $statusConstructor.Invoke([object[]]@('AUTH_REQUIRED', $null, $null, 0, $null, $null, $false, $null)); Expected = '登录并连接' }
        [pscustomobject]@{ Status = $statusConstructor.Invoke([object[]]@('AUTH_REQUIRED', $null, $null, 0, $null, $null, $true, $null)); Expected = '继续远程连接' }
    )
    foreach ($case in $loginPrimaryCases) {
        $shouldContinue = $continueRemoteFromLogin.Invoke($null, [object[]]@($case.Status))
        $actual = $loginPrimaryLabel.Invoke($null, [object[]]@($case.Status))
        $expected = $getLocalizedString.Invoke($null, [object[]]@($case.Expected))
        if ([bool]$shouldContinue -ne ($case.Expected -eq '继续远程连接') -or $actual -ne $expected) {
            throw "Login primary action mismatch for local-network waiting=$($case.Status.LocalNetworkWaiting): got '$actual', expected '$expected'"
        }
    }
    Write-Host "[windows-build] login primary action cases passed: $($loginPrimaryCases.Count)"

    $fillMissingAuthorizationUsername = $windowType.GetMethod('FillMissingAuthorizationUsername', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $fillMissingAuthorizationUsername) { throw 'WPF authorization username prefill mapper was not found' }
    $authorizationUsernameCases = @(
        [pscustomobject]@{ Current = ''; Saved = 'nas-user'; Expected = 'nas-user' }
        [pscustomobject]@{ Current = 'typed-user'; Saved = 'nas-user'; Expected = 'typed-user' }
        [pscustomobject]@{ Current = ''; Saved = $null; Expected = '' }
    )
    foreach ($case in $authorizationUsernameCases) {
        $actual = $fillMissingAuthorizationUsername.Invoke($null, [object[]]@($case.Current, $case.Saved))
        if ($actual -ne $case.Expected) { throw "Authorization username prefill mismatch: got '$actual', expected '$($case.Expected)'" }
    }
    Write-Host "[windows-build] authorization username prefill cases passed: $($authorizationUsernameCases.Count)"

    $shouldShowCachedLoginStatus = $windowType.GetMethod('ShouldShowCachedLoginStatus', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $shouldShowCachedLoginStatus) { throw 'WPF cached login-status guard was not found' }
    foreach ($case in @(
        [pscustomobject]@{ Configured = $true; Entered = 'ui-details-smoke'; Saved = 'ui-details-smoke'; Expected = $true }
        [pscustomobject]@{ Configured = $false; Entered = ''; Saved = $null; Expected = $false }
        [pscustomobject]@{ Configured = $true; Entered = 'different-server'; Saved = 'ui-details-smoke'; Expected = $false }
    )) {
        $actual = $shouldShowCachedLoginStatus.Invoke($null, [object[]]@($case.Configured, $case.Entered, $case.Saved))
        if ([bool]$actual -ne $case.Expected) { throw "Cached login-status scope mismatch for configured=$($case.Configured), entered='$($case.Entered)', saved='$($case.Saved)'" }
    }
    Write-Host '[windows-build] cached login errors are scoped to the saved device: 3'

    $shouldShowLoginPanel = $windowType.GetMethod('ShouldShowLoginPanel', [Reflection.BindingFlags]'NonPublic,Static')
    $serviceNotification = $windowType.GetMethod('ShouldNotifyServiceUnavailable', [Reflection.BindingFlags]'NonPublic,Static')
    foreach ($case in @(
        @{ Args = @('', $false, $false); Expected = $false },
        @{ Args = @('device', $true, $false); Expected = $false },
        @{ Args = @('device', $false, $false); Expected = $true },
        @{ Args = @('device', $false, $true); Expected = $false }
    )) {
        if ($serviceNotification.Invoke($null, $case.Args) -ne $case.Expected) { throw 'Service notification gating failed' }
    }
    Write-Host '[windows-build] first-use/foreground/background service notification cases passed: 4'
    $shouldClearOperationError = $windowType.GetMethod('ShouldClearOperationError', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $shouldShowLoginPanel -or $null -eq $shouldClearOperationError) {
        throw 'WPF diagnostic refresh view-state mapper was not found'
    }
    $loginPanelCases = @(
        [pscustomobject]@{ Configured = $false; Requested = $false; Expected = $true }
        [pscustomobject]@{ Configured = $false; Requested = $null; Expected = $true }
        [pscustomobject]@{ Configured = $true; Requested = $null; Expected = $false }
        [pscustomobject]@{ Configured = $true; Requested = $true; Expected = $true }
        [pscustomobject]@{ Configured = $true; Requested = $false; Expected = $false }
    )
    foreach ($case in $loginPanelCases) {
        $actual = $shouldShowLoginPanel.Invoke($null, [object[]]@($case.Configured, $case.Requested))
        if ([bool]$actual -ne $case.Expected) { throw "WPF login panel state mismatch for configured=$($case.Configured), requested=$($case.Requested)" }
    }
    $healthyStatus = $statusConstructor.Invoke([object[]]@('RELAY', $null, $null, 0, $null, $null, $false, $null))
    $pausedStatus = $statusConstructor.Invoke([object[]]@('PAUSED', $null, $null, 0, $null, $null, $false, $null))
    $failedStatus = $statusConstructor.Invoke([object[]]@(
        'LOCAL', $null, $null, 0, $null,
        $errorConstructor.Invoke([object[]]@('UNAVAILABLE', 'test', $null, $null, $null, $null, $null, $null, $null, $null)),
        $false, $null
    ))
    foreach ($case in @(
        [pscustomobject]@{ Status = $healthyStatus; ShowingLogin = $false; Expected = $true }
        [pscustomobject]@{ Status = $healthyStatus; ShowingLogin = $true; Expected = $false }
        [pscustomobject]@{ Status = $pausedStatus; ShowingLogin = $false; Expected = $false }
        [pscustomobject]@{ Status = $failedStatus; ShowingLogin = $false; Expected = $false }
    )) {
        $actual = $shouldClearOperationError.Invoke($null, [object[]]@($case.Status, $case.ShowingLogin))
        if ([bool]$actual -ne $case.Expected) { throw "WPF operation error retention mismatch for $($case.Status.State)/login=$($case.ShowingLogin)" }
    }
    Write-Host "[windows-build] diagnostics view-state cases passed: $($loginPanelCases.Count + 4)"

    $buildDiagnosticsJson = $windowType.GetMethod('BuildDiagnosticsJson', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $buildDiagnosticsJson) { throw 'WPF diagnostic JSON serializer was not found' }
    $operationError = @{ code = 'UNAVAILABLE'; message = 'daemon startup failed' }
    $emptySnapshotJson = $buildDiagnosticsJson.Invoke($null, [object[]]@($null, $operationError))
    $emptySnapshot = [Text.Json.JsonDocument]::Parse($emptySnapshotJson)
    try {
        $errorElement = $emptySnapshot.RootElement.GetProperty('operationError')
        if ($errorElement.GetProperty('code').GetString() -ne 'UNAVAILABLE' -or
            $errorElement.GetProperty('message').GetString() -ne 'daemon startup failed') {
            throw 'WPF diagnostic JSON dropped an operation error when no daemon snapshot exists'
        }
    }
    finally { $emptySnapshot.Dispose() }
    $emptySnapshotJson = $buildDiagnosticsJson.Invoke($null, [object[]]@($null, $null))
    if ($emptySnapshotJson -ne '{}') { throw "Empty WPF diagnostic JSON changed unexpectedly: '$emptySnapshotJson'" }
    Write-Host '[windows-build] diagnostic JSON preserves operation failures without a daemon snapshot: 2'

    $trayIconVariant = $windowType.GetMethod('TrayIconVariant', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $trayIconVariant) { throw 'WPF tray status icon mapper was not found' }
    foreach ($case in @(
        [pscustomobject]@{ State = 'LOCAL'; NeedsLogin = $false; Expected = 'connected' }
        [pscustomobject]@{ State = 'DIRECT'; NeedsLogin = $false; Expected = 'connected' }
        [pscustomobject]@{ State = 'RELAY'; NeedsLogin = $false; Expected = 'connected' }
        [pscustomobject]@{ State = 'AUTHORIZING'; NeedsLogin = $false; Expected = 'working' }
        [pscustomobject]@{ State = 'PROBING'; NeedsLogin = $false; Expected = 'working' }
        [pscustomobject]@{ State = 'RECONNECTING'; NeedsLogin = $false; Expected = 'working' }
        [pscustomobject]@{ State = 'ERROR'; NeedsLogin = $false; Expected = 'attention' }
        [pscustomobject]@{ State = 'DAEMON_UNAVAILABLE'; NeedsLogin = $false; Expected = 'attention' }
        [pscustomobject]@{ State = 'AUTH_REQUIRED'; NeedsLogin = $false; Expected = 'attention' }
        [pscustomobject]@{ State = 'UNCONFIGURED'; NeedsLogin = $false; Expected = 'offline' }
        [pscustomobject]@{ State = 'ERROR'; NeedsLogin = $true; Expected = 'attention' }
    )) {
        $actual = $trayIconVariant.Invoke($null, [object[]]@($case.State, $case.NeedsLogin))
        if ($actual -ne $case.Expected) { throw "Tray icon variant mismatch for $($case.State)/needsLogin=$($case.NeedsLogin): '$actual'" }
    }
    Write-Host '[windows-build] tray icon state mapping cases passed: 11'
    $trayState = $windowType.GetMethod('TrayState', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $trayState) { throw 'WPF tray operation-state mapper was not found' }
    foreach ($case in @(
        [pscustomobject]@{ State = 'LOCAL'; NeedsLogin = $false; Busy = $true; Pending = $null; Expected = 'PROBING' }
        [pscustomobject]@{ State = 'AUTH_REQUIRED'; NeedsLogin = $true; Busy = $true; Pending = $null; Expected = 'AUTH_REQUIRED' }
        [pscustomobject]@{ State = 'PAUSED'; NeedsLogin = $false; Busy = $false; Pending = $false; Expected = 'AUTH_REQUIRED' }
        [pscustomobject]@{ State = 'AUTHORIZING'; NeedsLogin = $false; Busy = $true; Pending = $false; Expected = 'AUTH_REQUIRED' }
        [pscustomobject]@{ State = 'PAUSED'; NeedsLogin = $false; Busy = $false; Pending = $true; Expected = 'AUTHORIZING' }
        [pscustomobject]@{ State = 'RELAY'; NeedsLogin = $false; Busy = $false; Pending = $null; Expected = 'RELAY' }
    )) {
        $actual = $trayState.Invoke($null, [object[]]@($case.State, $case.NeedsLogin, $case.Busy, $case.Pending))
        if ($actual -ne $case.Expected) { throw "Tray operation-state mismatch for $($case.State): '$actual'" }
    }
    Write-Host '[windows-build] tray operation state cases passed: 6'
    $loadTrayIcon = $windowType.GetMethod('LoadTrayIcon', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $loadTrayIcon) { throw 'WPF tray status icon renderer was not found' }
    foreach ($variant in @('connected', 'offline', 'working', 'attention')) {
        $icon = $loadTrayIcon.Invoke($null, [object[]]@($variant, $AssetDirectory))
        if ($null -eq $icon -or $icon.Width -ne 32 -or $icon.Height -ne 32) {
            if ($null -ne $icon) { $icon.Dispose() }
            throw "Tray icon renderer returned an invalid icon for $variant"
        }
        $icon.Dispose()
    }
    Write-Host '[windows-build] tray icon rendering cases passed: 4'
    $loadHeaderIcon = $windowType.GetMethod('LoadHeaderIcon', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $loadHeaderIcon) { throw 'WPF header icon loader was not found' }
    $headerIcon = $loadHeaderIcon.Invoke($null, [object[]]@($AssetDirectory))
    if ($null -eq $headerIcon -or $headerIcon.PixelWidth -lt 108 -or $headerIcon.PixelHeight -lt 108 -or -not $headerIcon.IsFrozen) {
        throw 'Header icon must retain enough source pixels for 36 DIP at 300% DPI'
    }
    Write-Host "[windows-build] high-resolution header icon validated: $($headerIcon.PixelWidth)x$($headerIcon.PixelHeight)"

    $stateLabel = $windowType.GetMethod('StateLabel', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $stateLabel) { throw 'WPF client-status presentation mapper was not found' }
    $previousCulture = [Globalization.CultureInfo]::CurrentUICulture
    try {
        foreach ($case in @(
            [pscustomobject]@{ Culture = 'zh-Hans'; State = 'DAEMON_UNAVAILABLE'; Expected = '客户端服务不可用' }
            [pscustomobject]@{ Culture = 'en-US'; State = 'DAEMON_UNAVAILABLE'; Expected = 'Client service unavailable' }
            [pscustomobject]@{ Culture = 'zh-Hans'; State = 'SERVER_UNAVAILABLE'; Expected = '未找到 FnCPN 服务' }
            [pscustomobject]@{ Culture = 'en-US'; State = 'SERVER_UNAVAILABLE'; Expected = 'FnCPN server not found' }
        )) {
            [Globalization.CultureInfo]::CurrentUICulture = [Globalization.CultureInfo]::GetCultureInfo($case.Culture)
            $actual = $stateLabel.Invoke($null, [object[]]@($case.State))
            if ($actual -ne $case.Expected) { throw "Client/server unavailable state mismatch for $($case.Culture)/$($case.State): '$actual'" }
        }
    }
    finally { [Globalization.CultureInfo]::CurrentUICulture = $previousCulture }
    Write-Host '[windows-build] client/server unavailable state localization cases passed: 4'

    $presentationStateLabel = $windowType.GetMethod('PresentationStateLabel', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $presentationStateLabel) { throw 'WPF client authentication-state presentation mapper was not found' }
    $previousCulture = [Globalization.CultureInfo]::CurrentUICulture
    try {
        foreach ($case in @(
            [pscustomobject]@{ Culture = 'zh-Hans'; State = 'ERROR'; NeedsLogin = $true; Expected = '需要重新登录' }
            [pscustomobject]@{ Culture = 'en-US'; State = 'ERROR'; NeedsLogin = $true; Expected = 'Sign in required' }
            [pscustomobject]@{ Culture = 'zh-Hans'; State = 'RELAY'; NeedsLogin = $false; Expected = 'FN Connect 中继' }
            [pscustomobject]@{ Culture = 'en-US'; State = 'RELAY'; NeedsLogin = $false; Expected = 'FN Connect relay' }
            [pscustomobject]@{ Culture = 'zh-Hans'; State = 'ERROR'; NeedsLogin = $false; Expected = '连接失败' }
            [pscustomobject]@{ Culture = 'en-US'; State = 'ERROR'; NeedsLogin = $false; Expected = 'Connection failed' }
        )) {
            [Globalization.CultureInfo]::CurrentUICulture = [Globalization.CultureInfo]::GetCultureInfo($case.Culture)
            $actual = $presentationStateLabel.Invoke($null, [object[]]@($case.State, $case.NeedsLogin))
            if ($actual -ne $case.Expected) { throw "Authentication presentation state mismatch for $($case.Culture)/$($case.State)/needsLogin=$($case.NeedsLogin): '$actual'" }
        }
    }
    finally { [Globalization.CultureInfo]::CurrentUICulture = $previousCulture }
    Write-Host '[windows-build] authentication and error presentation cases passed: 6'

    $connectionStatusLabel = $windowType.GetMethod('ConnectionStatusLabel', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $connectionStatusLabel) { throw 'WPF connection status label mapper was not found' }
    $statusType = $assembly.GetType('FnCPN.Windows.ClientStatus', $true)
    $statusConstructor = $statusType.GetConstructors([Reflection.BindingFlags]'Instance,Public,NonPublic') |
        Where-Object { $_.GetParameters().Count -eq 8 } | Select-Object -First 1
    if ($null -eq $statusConstructor) { throw 'WPF client status constructor shape was not found' }
    $showDirectFailure = $windowType.GetMethod('ShouldShowDirectFailure', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $showDirectFailure) { throw 'WPF direct failure presentation mapper was not found' }
    foreach ($state in @('LOCAL', 'DIRECT', 'RELAY', 'ERROR', 'PROBING', 'RECONNECTING')) {
        $status = $statusConstructor.Invoke([object[]]@($state, $null, $null, 0, $null, $null, $false, $null))
        $expected = $state -notin @('LOCAL', 'DIRECT', 'RELAY')
        if ($showDirectFailure.Invoke($null, [object[]]@($status)) -ne $expected) {
            throw "IPv6 fallback error presentation mismatch for $state"
        }
    }
    Write-Host '[windows-build] healthy connection hides fallback failure cases passed: 6'
    $connectionStatusCases = @(
        [pscustomobject]@{ Culture = 'zh-Hans'; Status = $statusConstructor.Invoke([object[]]@('AUTH_REQUIRED', $null, $null, 0, $null, $null, $true, $null)); NeedsLogin = $true; LoginInFlight = $false; Expected = '正在等待局域网访问权限' }
        [pscustomobject]@{ Culture = 'en-US'; Status = $statusConstructor.Invoke([object[]]@('AUTH_REQUIRED', $null, $null, 0, $null, $null, $true, $null)); NeedsLogin = $true; LoginInFlight = $false; Expected = 'Waiting for local network access' }
        [pscustomobject]@{ Culture = 'zh-Hans'; Status = $statusConstructor.Invoke([object[]]@('AUTHORIZING', $null, $null, 0, $null, $null, $false, $null)); NeedsLogin = $false; LoginInFlight = $true; Expected = '正在登录' }
        [pscustomobject]@{ Culture = 'en-US'; Status = $statusConstructor.Invoke([object[]]@('AUTHORIZING', $null, $null, 0, $null, $null, $false, $null)); NeedsLogin = $false; LoginInFlight = $true; Expected = 'Signing in' }
    )
    $previousCulture = [Globalization.CultureInfo]::CurrentUICulture
    try {
        foreach ($case in $connectionStatusCases) {
            [Globalization.CultureInfo]::CurrentUICulture = [Globalization.CultureInfo]::GetCultureInfo($case.Culture)
            $actual = $connectionStatusLabel.Invoke($null, [object[]]@($case.Status, $case.NeedsLogin, $case.LoginInFlight))
            if ($actual -ne $case.Expected) { throw "Connection status title mismatch for $($case.Culture): '$actual', expected '$($case.Expected)'" }
        }
    }
    finally { [Globalization.CultureInfo]::CurrentUICulture = $previousCulture }
    Write-Host '[windows-build] waiting/login-in-flight connection status labels passed: 4'

    $remoteErrorType = $assembly.GetType('FnCPN.Windows.RemoteError', $true)
    if ($null -eq $remoteErrorType) { throw 'WPF remote-error type was not found' }
    $describeFailure = $windowType.GetMethods([Reflection.BindingFlags]'NonPublic,Static') |
        Where-Object { $_.Name -eq 'DescribeFailure' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType -eq $remoteErrorType } |
        Select-Object -First 1
    if ($null -eq $describeFailure) { throw 'WPF remote-error presentation mapper was not found' }
    $remoteErrorConstructor = $remoteErrorType.GetConstructors([Reflection.BindingFlags]'Instance,Public,NonPublic') |
        Where-Object { $_.GetParameters().Count -eq 10 } | Select-Object -First 1
    if ($null -eq $remoteErrorConstructor) { throw 'WPF remote-error constructor shape was not found' }
    $currentFailure = $remoteErrorConstructor.Invoke([object[]]@('UNAVAILABLE', 'service unavailable', $null, 'diagnose.privileged', $null, $null, $null, $null, $null, $null))
    foreach ($status in @(
        $statusConstructor.Invoke([object[]]@('RELAY', $null, $null, 0, $null, $currentFailure, $false, $null))
        $statusConstructor.Invoke([object[]]@('LOCAL', $null, $null, 0, $null, $null, $true, $null))
    )) {
        if (-not $showDirectFailure.Invoke($null, [object[]]@($status))) {
            throw 'Current error or local network waiting must retain direct failure diagnostics'
        }
    }
    $loginFailureMessage = $windowType.GetMethod('LoginFailureMessage', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $loginFailureMessage) { throw 'WPF login failure message mapper was not found' }
    $loginFailureCases = @(
        [pscustomobject]@{ Culture = 'zh-Hans'; Code = 'AUTH_REQUIRED'; Operation = 'native_session.authenticate'; RemoteCode = $null; Expected = '用户名或密码无效，请重试' }
        [pscustomobject]@{ Culture = 'en-US'; Code = 'AUTH_REQUIRED'; Operation = 'authorization.resume'; RemoteCode = $null; Expected = 'Your session has expired. Enter your password again.' }
        [pscustomobject]@{ Culture = 'zh-Hans'; Code = 'PERMISSION_DENIED'; Operation = 'authorization.create'; RemoteCode = $null; Expected = '该账号权限不足，无法完成授权' }
        [pscustomobject]@{ Culture = 'en-US'; Code = 'UNAVAILABLE'; Operation = 'authorization.create'; RemoteCode = $null; Expected = 'Cannot connect to fnOS. Check your network and try again.' }
        [pscustomobject]@{ Culture = 'zh-Hans'; Code = 'TIMEOUT'; Operation = 'authorization.create'; RemoteCode = $null; Expected = '暂时无法连接 fnOS，请检查网络后重试' }
        [pscustomobject]@{ Culture = 'en-US'; Code = 'FAILED_PRECONDITION'; Operation = 'authorization.create'; RemoteCode = 'TWO_FACTOR_REQUIRED'; Expected = 'This account requires two-factor authentication, which is not supported yet.' }
    )
    $previousCulture = [Globalization.CultureInfo]::CurrentUICulture
    try {
        foreach ($case in $loginFailureCases) {
            [Globalization.CultureInfo]::CurrentUICulture = [Globalization.CultureInfo]::GetCultureInfo($case.Culture)
            $failure = $remoteErrorConstructor.Invoke([object[]]@($case.Code, 'ignored summary', $null, $case.Operation, $case.RemoteCode, $null, $null, $null, $null, $null))
            $actual = $loginFailureMessage.Invoke($null, [object[]]@($failure))
            if ($actual -ne $case.Expected) { throw "Login failure message mismatch for $($case.Culture)/$($case.Code): '$actual', expected '$($case.Expected)'" }
        }
    }
    finally { [Globalization.CultureInfo]::CurrentUICulture = $previousCulture }
    Write-Host "[windows-build] macOS-equivalent login failure messages passed: $($loginFailureCases.Count)"
    $loginFailureForException = $windowType.GetMethod('LoginFailureForException', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $loginFailureForException) { throw 'WPF structured login exception mapper was not found' }
    $authorizationFailureType = $assembly.GetType('FnCPN.Windows.MainWindow+AuthorizationFailureException', $true)
    $authorizationFailureConstructor = $authorizationFailureType.GetConstructors([Reflection.BindingFlags]'Instance,Public,NonPublic') |
        Where-Object { $_.GetParameters().Count -eq 1 } | Select-Object -First 1
    if ($null -eq $authorizationFailureConstructor) { throw 'WPF authorization-wait failure constructor was not found' }
    $failedWait = $remoteErrorConstructor.Invoke([object[]]@('PERMISSION_DENIED', 'denied', $null, 'authorization.create', $null, 403, $null, $null, $null, $null))
    $authorizationFailure = $authorizationFailureConstructor.Invoke([object[]]@($failedWait))
    $previousCulture = [Globalization.CultureInfo]::CurrentUICulture
    try {
        [Globalization.CultureInfo]::CurrentUICulture = [Globalization.CultureInfo]::GetCultureInfo('zh-Hans')
        $actual = $loginFailureForException.Invoke($null, [object[]]@($authorizationFailure))
        if ($actual -ne '该账号权限不足，无法完成授权') { throw "Structured authorization-wait failure was not mapped to the login message: '$actual'" }
        $authorizationFailureWithoutDetails = $authorizationFailureConstructor.Invoke([object[]]@($null))
        $actual = $loginFailureForException.Invoke($null, [object[]]@($authorizationFailureWithoutDetails))
        if ($actual -ne '授权没有完成') { throw "Empty authorization-wait failure was not mapped to the localized fallback: '$actual'" }
    }
    finally { [Globalization.CultureInfo]::CurrentUICulture = $previousCulture }
    $ipcExceptionType = $assembly.GetType('FnCPN.Windows.IPCException', $true)
    $ipcExceptionConstructor = $ipcExceptionType.GetConstructors([Reflection.BindingFlags]'Instance,Public,NonPublic') |
        Where-Object { $_.GetParameters().Count -eq 2 } | Select-Object -First 1
    if ($null -eq $ipcExceptionConstructor) { throw 'WPF IPC exception constructor was not found' }
    $ipcFailureDocument = [Text.Json.JsonDocument]::Parse('{"code":"UNAVAILABLE","message":"offline","operation":"authorization.create"}')
    try {
        $ipcFailure = $ipcExceptionConstructor.Invoke([object[]]@('offline', $ipcFailureDocument.RootElement))
        [Globalization.CultureInfo]::CurrentUICulture = [Globalization.CultureInfo]::GetCultureInfo('en-US')
        $actual = $loginFailureForException.Invoke($null, [object[]]@($ipcFailure))
        if ($actual -ne 'Cannot connect to fnOS. Check your network and try again.') {
            throw "IPC login failure was not mapped to the fnOS connectivity message: '$actual'"
        }
    }
    finally { $ipcFailureDocument.Dispose(); [Globalization.CultureInfo]::CurrentUICulture = $previousCulture }
    Write-Host '[windows-build] structured IPC and authorization-wait login failures preserve specific messages and fallback: 3'
    $loginStatusMessage = $windowType.GetMethod('LoginStatusMessage', [Reflection.BindingFlags]'NonPublic,Static')
    if ($null -eq $loginStatusMessage) { throw 'WPF login status message mapper was not found' }
    $expiredFailure = $remoteErrorConstructor.Invoke([object[]]@('AUTH_REQUIRED', 'expired', $null, 'authorization.resume', $null, $null, $null, $null, $null, $null))
    $loginStatusCases = @(
        [pscustomobject]@{ Status = $statusConstructor.Invoke([object[]]@('AUTH_REQUIRED', $null, $null, 0, $null, $expiredFailure, $false, $null)); Expected = '登录已失效，请重新输入密码' }
        [pscustomobject]@{ Status = $statusConstructor.Invoke([object[]]@('AUTH_REQUIRED', $null, $null, 0, $null, $null, $true, $null)); Expected = '正在等待局域网访问权限' }
        [pscustomobject]@{ Status = $null; Expected = '' }
    )
    $previousCulture = [Globalization.CultureInfo]::CurrentUICulture
    try {
        [Globalization.CultureInfo]::CurrentUICulture = [Globalization.CultureInfo]::GetCultureInfo('zh-Hans')
        foreach ($case in $loginStatusCases) {
            $actual = $loginStatusMessage.Invoke($null, [object[]]@($case.Status))
            if ($actual -ne $case.Expected) { throw "Login status message mismatch: '$actual', expected '$($case.Expected)'" }
        }
    }
    finally { [Globalization.CultureInfo]::CurrentUICulture = $previousCulture }
    Write-Host '[windows-build] login view reflects cached auth/local network status: 3'

    $previousCulture = [Globalization.CultureInfo]::CurrentUICulture
    try {
        foreach ($case in @(
            [pscustomobject]@{ Culture = 'zh-Hans'; Code = 'SERVER_UNAVAILABLE'; Prefix = '未找到 FnCPN 服务，请在 fnOS 安装或启动 FnCPN 后重新连接。' }
            [pscustomobject]@{ Culture = 'en-US'; Code = 'SERVER_UNAVAILABLE'; Prefix = 'FnCPN server was not found. Install or start FnCPN on fnOS, then connect again.' }
            [pscustomobject]@{ Culture = 'zh-Hans'; Code = 'UNAVAILABLE'; Prefix = '服务暂不可用，请稍后重试。' }
            [pscustomobject]@{ Culture = 'en-US'; Code = 'UNAVAILABLE'; Prefix = 'Service unavailable. Try again later.' }
        )) {
            [Globalization.CultureInfo]::CurrentUICulture = [Globalization.CultureInfo]::GetCultureInfo($case.Culture)
            $errorValue = $remoteErrorConstructor.Invoke([object[]]@($case.Code, '', $null, $null, $null, $null, $null, $null, $null, $null))
            $actual = $describeFailure.Invoke($null, [object[]]@($errorValue))
            if (-not $actual.StartsWith($case.Prefix, [StringComparison]::Ordinal)) {
                throw "Remote failure localization mismatch for $($case.Culture)/$($case.Code): '$actual'"
            }
        }
    }
    finally { [Globalization.CultureInfo]::CurrentUICulture = $previousCulture }
    Write-Host '[windows-build] server/local service error distinction cases passed: 4'

    $stringsType = $assembly.GetType('FnCPN.Windows.AppStrings', $true)
    $getString = $stringsType.GetMethod('Get', [Reflection.BindingFlags]'Public,Static')
    if ($null -eq $getString) { throw 'Shared WPF localization lookup was not found' }
    $previousCulture = [Globalization.CultureInfo]::CurrentUICulture
    try {
        [Globalization.CultureInfo]::CurrentUICulture = [Globalization.CultureInfo]::GetCultureInfo('en-US')
        foreach ($case in @(
            [pscustomobject]@{ Key = '继续远程连接'; Expected = 'Continue with remote connection' }
            [pscustomobject]@{ Key = '返回连接详情'; Expected = 'Back to connection details' }
            [pscustomobject]@{ Key = 'FnCPN 已在运行，但无法将请求转交给现有窗口。请检查应用状态后重试。'; Expected = 'FnCPN is already running but could not receive the request. Check the application and try again.' }
        )) {
            $actual = $getString.Invoke($null, [object[]]@($case.Key))
            if ($actual -ne $case.Expected) { throw "Shared English translation mismatch for '$($case.Key)': '$actual'" }
        }
        [Globalization.CultureInfo]::CurrentUICulture = [Globalization.CultureInfo]::GetCultureInfo('zh-Hans')
        $fallback = $getString.Invoke($null, [object[]]@('继续远程连接'))
        if ($fallback -ne '继续远程连接') { throw "Shared Chinese fallback mismatch: '$fallback'" }
        $activationFallback = $getString.Invoke($null, [object[]]@('FnCPN 已在运行，但无法将请求转交给现有窗口。请检查应用状态后重试。'))
        if ($activationFallback -ne 'FnCPN 已在运行，但无法将请求转交给现有窗口。请检查应用状态后重试。') { throw "Shared Chinese activation message mismatch: '$activationFallback'" }
    }
    finally { [Globalization.CultureInfo]::CurrentUICulture = $previousCulture }
    Write-Host '[windows-build] shared Chinese/English localization cases passed: 5'
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

function Assert-MsiUpgradeMetadata([string]$Path, [string]$Architecture, [string]$ProductVersion) {
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
        foreach ($dialog in @('WelcomeEulaDlg', 'VerifyReadyDlg')) {
            if (-not $shieldDialogs.ContainsKey($dialog)) { throw "MSI missing elevation shield in $dialog" }
        }
        $view.Close()
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
            if ($record.StringData(3) -ne 'GoClientBinary' -or $record.StringData(4) -ne 'internal-stop-daemons') {
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
            if ($record.StringData(3) -ne 'GoClientBinary' -or $record.StringData(4) -ne 'internal-cleanup --role client') {
                throw 'CleanupNetwork must invoke only the embedded Go network cleanup command'
            }
        }
        if (-not $cleanupActionFound) { throw 'CleanupNetwork custom action is missing' }

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
            'fncpn\shell\open\command|' = '"[INSTALLFOLDER]ui\FnCPN.exe" "%1"'
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
$appManifest = Join-Path $projectRoot 'platform/windows/FnCPN.Windows/app.manifest'
Assert-WindowsDpiAwarenessManifest $appManifest
$manifestTool = Find-WindowsManifestTool
$webView2Signature = Get-AuthenticodeSignature -LiteralPath $webView2Bootstrapper
if ($webView2Signature.Status -ne 'Valid' -or $webView2Signature.SignerCertificate.Subject -notlike '*Microsoft Corporation*') { throw "WebView2 Bootstrapper Authenticode 签名无效：$($webView2Signature.Status)" }

foreach ($architecture in $architectures) {
    $timer = [Diagnostics.Stopwatch]::StartNew()
    $goArch = if ($architecture -eq 'arm64') { 'arm64' } else { 'amd64' }
    $wixArchShort = if ($architecture -eq 'arm64') { 'A64' } else { 'X64' }
    $runtime = if ($architecture -eq 'arm64') { 'win-arm64' } else { 'win-x64' }
    $target = Join-Path $projectRoot "dist/windows/$architecture"
    $stageRoot = Join-Path $projectRoot '.dev/windows-stage'
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
    Invoke-Checked 'dotnet' @('publish', $packageProject, '-c', 'Release', '-r', $runtime, '--self-contained', 'true', '-o', $uiOutput, "-p:InformationalVersion=$version+$sourceRevision")
    $publishedExecutable = Join-Path $uiOutput 'FnCPN.exe'
    $embeddedManifest = Join-Path $stage "FnCPN-$architecture.manifest.xml"
    Invoke-Checked $manifestTool @("-inputresource:$publishedExecutable;#1", "-out:$embeddedManifest")
    Assert-WindowsDpiAwarenessManifest $embeddedManifest
    $sourceIcon = Join-Path $projectRoot 'packaging/assets/AppIcon.ico'
    $publishedIcon = Join-Path $uiOutput 'FnCPN.ico'
    if (-not (Test-Path -LiteralPath $publishedIcon -PathType Leaf) -or (Get-FileHash -LiteralPath $publishedIcon -Algorithm SHA256).Hash -ne (Get-FileHash -LiteralPath $sourceIcon -Algorithm SHA256).Hash) {
        throw "Published UI icon is missing or differs from the product icon: $publishedIcon"
    }
    foreach ($variant in @('connected', 'offline', 'working', 'attention')) {
        $sourceStatusIcon = Join-Path $projectRoot "packaging/assets/Status-$variant.png"
        $publishedStatusIcon = Join-Path $uiOutput "Status-$variant.png"
        if (-not (Test-Path -LiteralPath $publishedStatusIcon -PathType Leaf) -or (Get-FileHash -LiteralPath $publishedStatusIcon -Algorithm SHA256).Hash -ne (Get-FileHash -LiteralPath $sourceStatusIcon -Algorithm SHA256).Hash) {
            throw "Published $variant tray status icon is missing or differs from its shared source asset: $publishedStatusIcon"
        }
    }
    Add-Type -AssemblyName System.Drawing
    $embeddedIcon = [Drawing.Icon]::ExtractAssociatedIcon((Join-Path $uiOutput 'FnCPN.exe'))
    if ($null -eq $embeddedIcon) { throw "Published WPF executable has no embedded application icon: $(Join-Path $uiOutput 'FnCPN.exe')" }
    $embeddedIcon.Dispose()
    $hostArchitecture = [Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString()
    $canExecutePublishedUI = ($runtime -eq 'win-arm64' -and $hostArchitecture -eq 'Arm64') -or
        ($runtime -eq 'win-x64' -and $hostArchitecture -eq 'X64')
    if ($canExecutePublishedUI) {
        Assert-WindowsFnIdNormalization (Join-Path $uiOutput 'FnCPN.dll') $version $uiOutput
    }
    else {
        Write-Host "[windows-build] [$architecture] runtime behavior checks skipped: host process is $hostArchitecture"
    }

    $goOutput = Join-Path $payload 'fncpn.exe'
    $previous = @{}
    foreach ($key in @('GOOS', 'GOARCH', 'CGO_ENABLED')) { $previous[$key] = [Environment]::GetEnvironmentVariable($key, 'Process') }
    try {
        $env:GOOS = 'windows'; $env:GOARCH = $goArch; $env:CGO_ENABLED = '0'
        Invoke-Checked 'go' @('build', '-trimpath', '-ldflags', "-X github.com/rectcircle/fn-connect-private-network/internal/version.Revision=$sourceRevision", '-o', $goOutput, './cmd/fncpn')
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
    Assert-PeArchitecture (Join-Path $payload 'fncpn.exe') $expectedMachine
    Assert-PeArchitecture (Join-Path $payload 'wintun.dll') $expectedMachine
    Assert-PeArchitecture (Join-Path $payload 'ui/FnCPN.exe') $expectedMachine
    Write-Host "[windows-build] [$architecture] Go, WPF, and Wintun PE architectures validated"

    $references = [System.Collections.Generic.List[string]]::new()
    $payloadDirectory = New-DirectoryXml $payload '' $references
    # One upgrade family prevents ARM64-emulated x64 and native ARM64 installs
    # from competing for the same SCM service and named-pipe identities.
    $serviceUpgrade = '{9E9B33D6-B243-4AA7-A054-B9F3E28A80A4}'
    $wxs = [Text.StringBuilder]::new()
    [void]$wxs.AppendLine('<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs" xmlns:util="http://wixtoolset.org/schemas/v4/wxs/util" xmlns:ui="http://wixtoolset.org/schemas/v4/wxs/ui">')
    [void]$wxs.AppendLine("<Package Name=`"FnCPN`" Manufacturer=`"FnCPN`" Version=`"$(Escape-Xml $installerVersion)`" UpgradeCode=`"$serviceUpgrade`" Scope=`"perMachine`" InstallerVersion=`"500`" Compressed=`"yes`">")
    [void]$wxs.AppendLine('<ui:WixUI Id="WixUI_Minimal" /><WixVariable Id="WixUILicenseRtf" Value="$(var.LicenseRtf)" />')
    [void]$wxs.AppendLine('<Property Id="MSIDEPLOYMENTCOMPLIANT" Value="1" />')
    # HKLM's 32-bit view is SOFTWARE\WOW6432Node on both supported architectures.
    # HKCU\Software is shared; search the initiating user's runtime before elevation.
    [void]$wxs.AppendLine('<Property Id="WEBVIEW2_MACHINE_VERSION" Secure="yes"><RegistrySearch Id="WebView2MachineSearch" Root="HKLM" Key="SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" Name="pv" Type="raw" Bitness="always32" /></Property>')
    [void]$wxs.AppendLine('<Property Id="WEBVIEW2_USER_VERSION" Secure="yes"><RegistrySearch Id="WebView2UserSearch" Root="HKCU" Key="SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" Name="pv" Type="raw" Bitness="always64" /></Property>')
    [void]$wxs.AppendLine('<UI><ProgressText Action="InstallWebView2Runtime" Message="Installing Microsoft WebView2 Runtime (internet connection required)..." /><ProgressText Action="StopAllUserDaemons" Message="Stopping FnCPN background processes..." /><ProgressText Action="CleanupNetwork" Message="Removing FnCPN tunnel addresses and routes..." /></UI>')
    [void]$wxs.AppendLine('<Property Id="WINDOWSBUILDNUMBER" Secure="yes"><RegistrySearch Id="WindowsBuildNumberSearch" Root="HKLM" Key="SOFTWARE\Microsoft\Windows NT\CurrentVersion" Name="CurrentBuildNumber" Type="raw" Bitness="always64" /></Property>')
    [void]$wxs.AppendLine('<Launch Condition="Installed OR (VersionNT64 AND MsiNTProductType = 1 AND WINDOWSBUILDNUMBER &gt;= 19045)" Message="FnCPN requires 64-bit Windows 10 22H2 or later (build 19045 or later)." />')
    [void]$wxs.AppendLine('<MajorUpgrade AllowSameVersionUpgrades="yes" Schedule="afterInstallInitialize" DowngradeErrorMessage="A newer FnCPN version is already installed." />')
    [void]$wxs.AppendLine('<util:CloseApplication Id="CloseFnCPNUI" Target="FnCPN.exe" ElevatedEndSessionMessage="yes" Timeout="30" RebootPrompt="no" />')
    [void]$wxs.AppendLine('<Property Id="PURGE_CURRENT_USER_DATA" Secure="yes" />')
    [void]$wxs.AppendLine('<MediaTemplate EmbedCab="yes" />')
    [void]$wxs.AppendLine('<Binary Id="WebView2SetupBinary" SourceFile="$(var.PayloadDir)\WebView2Setup.exe" />')
    [void]$wxs.AppendLine('<Binary Id="GoClientBinary" SourceFile="$(var.PayloadDir)\fncpn.exe" />')
    [void]$wxs.AppendLine('<CustomAction Id="InstallWebView2Runtime" BinaryRef="WebView2SetupBinary" ExeCommand="/silent /install" Execute="deferred" Impersonate="no" Return="check" />')
    [void]$wxs.AppendLine('<CustomAction Id="PurgeCurrentUserData" FileRef="GoClient" ExeCommand="internal-purge-user --stop-daemon" Execute="deferred" Impersonate="yes" Return="check" />')
    [void]$wxs.AppendLine('<CustomAction Id="StopAllUserDaemons" BinaryRef="GoClientBinary" ExeCommand="internal-stop-daemons" Execute="deferred" Impersonate="no" Return="check" />')
    [void]$wxs.AppendLine('<CustomAction Id="CleanupNetwork" BinaryRef="GoClientBinary" ExeCommand="internal-cleanup --role client" Execute="deferred" Impersonate="no" Return="check" />')
    [void]$wxs.AppendLine("<InstallExecuteSequence><Custom Action=`"InstallWebView2Runtime`" Before=`"InstallServices`" Condition=`"$(Escape-Xml $webView2InstallCondition)`" /><Custom Action=`"override Wix4CloseApplications_$wixArchShort`" Before=`"StopAllUserDaemons`" Condition=`"WIX_UPGRADE_DETECTED`" /><Custom Action=`"StopAllUserDaemons`" Before=`"InstallFiles`" Condition=`"WIX_UPGRADE_DETECTED`" /><Custom Action=`"CleanupNetwork`" Before=`"RemoveFiles`" Condition=`"REMOVE~=&quot;ALL&quot;`" /><Custom Action=`"PurgeCurrentUserData`" Before=`"RemoveFiles`" Condition=`"REMOVE~=&quot;ALL&quot; AND PURGE_CURRENT_USER_DATA=&quot;1&quot; AND NOT UPGRADINGPRODUCTCODE`" /></InstallExecuteSequence>")
    [void]$wxs.AppendLine('<StandardDirectory Id="ProgramFiles64Folder"><Directory Id="INSTALLFOLDER" Name="FnCPN-' + $architecture + '">')
    [void]$wxs.AppendLine('<Component Id="ClientService" Guid="*"><File Id="GoClient" Source="$(var.PayloadDir)\fncpn.exe" KeyPath="yes" />')
    [void]$wxs.AppendLine('<ServiceInstall Id="FnCPNServiceInstall" Name="FnCPNClient" DisplayName="FnCPN Network Service" Description="Configures the FnCPN WireGuard tunnel." Type="ownProcess" Start="auto" Account="LocalSystem" ErrorControl="normal" Arguments="client privileged-daemon"><util:ServiceConfig FirstFailureActionType="restart" SecondFailureActionType="restart" ThirdFailureActionType="restart" RestartServiceDelayInSeconds="5" ResetPeriodInDays="1"/></ServiceInstall>')
    [void]$wxs.AppendLine('<ServiceControl Id="FnCPNServiceControl" Name="FnCPNClient" Start="install" Stop="both" Remove="uninstall" Wait="yes" /></Component>')
    [void]$wxs.Append($payloadDirectory)
    [void]$wxs.AppendLine('<Component Id="ProtocolRegistration" Directory="INSTALLFOLDER" Guid="*"><RegistryValue Root="HKCR" Key="fncpn" Type="string" Value="URL:FnCPN authorization protocol" KeyPath="yes"/><RegistryValue Root="HKCR" Key="fncpn" Name="URL Protocol" Type="string" Value=""/><RegistryValue Root="HKCR" Key="fncpn\shell\open\command" Type="string" Value="&quot;[INSTALLFOLDER]ui\FnCPN.exe&quot; &quot;%1&quot;"/></Component>')
    [void]$wxs.AppendLine('<Component Id="CliPath" Directory="INSTALLFOLDER" Guid="*"><Environment Id="FnCPNPath" Name="PATH" Value="[INSTALLFOLDER]" Permanent="no" Part="last" Action="set" System="yes"/><RegistryValue Root="HKLM" Key="Software\FnCPN" Name="InstallPath" Type="string" Value="[INSTALLFOLDER]" KeyPath="yes"/><RegistryValue Root="HKLM" Key="Software\FnCPN" Name="ProductCode" Type="string" Value="[ProductCode]"/></Component>')
    [void]$wxs.AppendLine('<Component Id="StartMenu" Directory="INSTALLFOLDER" Guid="*"><Shortcut Id="FnCPNShortcut" Name="FnCPN" Target="[INSTALLFOLDER]ui\FnCPN.exe" WorkingDirectory="INSTALLFOLDER" Directory="ProgramMenuFolder"/><Shortcut Id="FnCPNDesktopShortcut" Name="FnCPN" Target="[INSTALLFOLDER]ui\FnCPN.exe" WorkingDirectory="INSTALLFOLDER" Directory="DesktopFolder"/><RegistryValue Root="HKLM" Key="Software\FnCPN" Name="ShortcutInstalled" Type="integer" Value="1" KeyPath="yes"/><RemoveFolder Id="RemoveFnCPNShortcut" Directory="ProgramMenuFolder" On="uninstall"/></Component>')
    [void]$wxs.AppendLine('</Directory></StandardDirectory>')
    [void]$wxs.AppendLine('<Feature Id="Main" Title="FnCPN" Level="1"><ComponentRef Id="ClientService"/><ComponentRef Id="ProtocolRegistration"/><ComponentRef Id="CliPath"/><ComponentRef Id="StartMenu"/>')
    foreach ($reference in $references) { [void]$wxs.AppendLine("<ComponentRef Id=`"$reference`" />") }
    [void]$wxs.AppendLine('</Feature></Package></Wix>')
    $source = Join-Path $stage 'Package.wxs'
    [IO.File]::WriteAllText($source, $wxs.ToString(), [Text.UTF8Encoding]::new($false))

    $licenseRtf = Join-Path $stage 'License.rtf'
    $licenseText = (Get-Content (Join-Path $projectRoot 'LICENSE') -Raw).Replace('\', '\\').Replace('{', '\{').Replace('}', '\}').Replace("`r", '').Replace("`n", '\par ' )
    [IO.File]::WriteAllText($licenseRtf, ('{\rtf1\ansi ' + $licenseText + '}'), [Text.Encoding]::ASCII)
    $msi = Join-Path $stage "FnCPN-$version-$architecture-unsigned.msi"
    Invoke-Checked $wix @('-acceptEula', 'wix7', 'build', $source, '-arch', $architecture, '-ext', $wixUtilExtension, '-ext', $wixUiExtension, '-d', "LicenseRtf=$licenseRtf", '-d', "PayloadDir=$payload", '-o', $msi, '-intermediatefolder', (Join-Path $stage 'wix-obj'))
    Assert-MsiUpgradeMetadata $msi $architecture $installerVersion
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
    Remove-Item -LiteralPath $stage -Recurse -Force
    & (Join-Path $PSScriptRoot 'test-windows-msi-platform.ps1') -Architecture $architecture
    Write-Host "[windows-build] [$architecture] 完成，阶段耗时 $($timer.Elapsed.ToString())；产物：$(Join-Path $target (Split-Path -Leaf $msi))"
}

Write-Host "[windows-build] 全部完成。产物在 dist/windows/<architecture>/；均为未签名本地候选，不会上传或创建 Release。"
