#Requires -Version 7.0
[CmdletBinding()]
param([ValidateSet('arm64', 'x64')][string]$Architecture = 'arm64')

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Read-ExactPipeBytes([System.IO.Stream]$Stream, [int]$Count) {
    $buffer = [byte[]]::new($Count)
    $offset = 0
    while ($offset -lt $Count) {
        $readTask = $Stream.ReadAsync($buffer, $offset, $Count - $offset)
        if (-not $readTask.Wait(10000)) { throw 'Timed out reading a complete frame from the Go client daemon.' }
        $read = $readTask.Result
        if ($read -le 0) { throw 'The Go client daemon closed its pipe before returning a complete frame.' }
        $offset += $read
    }
    return ,$buffer
}

function Invoke-ClientPipeRequest([string]$Method, [hashtable]$Parameters) {
    $pipeName = 'FnCPN.Client.v1.' + [Diagnostics.Process]::GetCurrentProcess().SessionId
    $pipe = $null
    $connectDeadline = [DateTime]::UtcNow.AddSeconds(20)
    while ([DateTime]::UtcNow -lt $connectDeadline) {
        $candidate = [IO.Pipes.NamedPipeClientStream]::new('.', $pipeName, [IO.Pipes.PipeDirection]::InOut)
        try {
            $candidate.Connect(1000)
            $pipe = $candidate
            break
        }
        catch [TimeoutException] {
            $candidate.Dispose()
            Start-Sleep -Milliseconds 200
        }
    }
    if ($null -eq $pipe) { throw "Timed out connecting to the Go client pipe '$pipeName'." }
    try {
        $request = @{
            version = 4
            id = [guid]::NewGuid().ToString('N')
            method = $Method
            params = $Parameters
        }
        $requestId = $request.id
        $payload = [Text.Encoding]::UTF8.GetBytes(($request | ConvertTo-Json -Compress -Depth 8))
        if ($payload.Length -eq 0 -or $payload.Length -gt 262144) { throw 'The Go client request frame size is invalid.' }
        $header = [byte[]]@(
            (($payload.Length -shr 24) -band 255),
            (($payload.Length -shr 16) -band 255),
            (($payload.Length -shr 8) -band 255),
            ($payload.Length -band 255))
        $pipe.Write($header, 0, $header.Length)
        $pipe.Write($payload, 0, $payload.Length)
        $pipe.Flush()
        $responseHeader = Read-ExactPipeBytes $pipe 4
        $responseLength = ([int]$responseHeader[0] -shl 24) -bor ([int]$responseHeader[1] -shl 16) -bor
            ([int]$responseHeader[2] -shl 8) -bor [int]$responseHeader[3]
        if ($responseLength -le 0 -or $responseLength -gt 262144) { throw 'The Go client response frame size is invalid.' }
        $responsePayload = Read-ExactPipeBytes $pipe $responseLength
        $response = ConvertFrom-Json -InputObject ([Text.Encoding]::UTF8.GetString($responsePayload)) -AsHashtable
        if ($response.version -ne 4 -or $response.id -ne $requestId -or [string]::IsNullOrWhiteSpace([string]$response.productVersion)) {
            throw 'The Go client returned a mismatched or incompatible IPC response.'
        }
        return $response
    }
    finally { $pipe.Dispose() }
}

$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$uiPath = Join-Path $projectRoot "dist/windows/$Architecture/payload/ui/FnCPN.exe"
if (-not (Test-Path -LiteralPath $uiPath -PathType Leaf)) { throw "UI executable is missing: $uiPath; build this architecture first." }
$clientExePath = Join-Path (Split-Path -Parent (Split-Path -Parent $uiPath)) 'fncpn.exe'
if (-not (Test-Path -LiteralPath $clientExePath -PathType Leaf)) { throw "Go client executable is missing: $clientExePath; build this architecture first." }
$currentSessionId = [Diagnostics.Process]::GetCurrentProcess().SessionId
$existingDaemons = @(Get-CimInstance Win32_Process | Where-Object {
    $_.Name -ieq 'fncpn.exe' -and $_.SessionId -eq $currentSessionId -and $_.CommandLine -match '\bclient\s+daemon\b'
})
if ($existingDaemons.Count -gt 0) { throw 'A FnCPN Go client daemon is already running in this session; refusing to attach to or stop an existing daemon.' }
$existingUi = @(Get-CimInstance Win32_Process | Where-Object {
    $_.ExecutablePath -ieq $uiPath -and $_.SessionId -eq $currentSessionId
})
if ($existingUi.Count -gt 0) { throw 'FnCPN UI is already running in this session; refusing to attach to or stop an existing process.' }

$isolatedRoot = Join-Path $env:TEMP ('FnCPN-ui-smoke-' + [guid]::NewGuid().ToString('N'))
$isolatedLocalAppData = Join-Path $isolatedRoot 'LocalAppData'
New-Item -ItemType Directory -Path $isolatedLocalAppData -Force | Out-Null
$startupLogDirectory = Join-Path $isolatedLocalAppData 'FnCPN/Logs'
New-Item -ItemType Directory -Path $startupLogDirectory -Force | Out-Null
$startupLogPath = Join-Path $startupLogDirectory 'ui-startup.log'
$startupLogRotationMarker = 'fncpn-startup-rotation-smoke:'
[IO.File]::WriteAllText(
    $startupLogPath,
    $startupLogRotationMarker + ('x' * (1MB + 1)),
    [Text.UTF8Encoding]::new($false))
$launchTime = [DateTime]::Now
[void][Reflection.Assembly]::Load('UIAutomationClient')
[void][Reflection.Assembly]::Load('UIAutomationTypes')
if (-not ('FnCPNUiSmokeNative' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class FnCPNUiSmokeNative {
    private delegate bool EnumWindowsCallback(IntPtr handle, IntPtr parameter);
    [DllImport("user32.dll")]
    private static extern bool EnumWindows(EnumWindowsCallback callback, IntPtr parameter);
    [DllImport("user32.dll")]
    private static extern uint GetWindowThreadProcessId(IntPtr handle, out uint processId);
    [DllImport("user32.dll")]
    public static extern bool IsWindowVisible(IntPtr handle);
    [DllImport("user32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static extern bool PostMessage(IntPtr handle, uint message, IntPtr wParam, IntPtr lParam);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern uint RegisterWindowMessage(string message);
    [DllImport("user32.dll")]
    public static extern void keybd_event(byte virtualKey, byte scanCode, uint flags, UIntPtr extraInfo);
    public static IntPtr FindTopLevelWindow(int targetProcessId) {
        IntPtr result = IntPtr.Zero;
        EnumWindows((handle, _) => {
            uint processId;
            GetWindowThreadProcessId(handle, out processId);
            if (processId == (uint)targetProcessId) { result = handle; return false; }
            return true;
        }, IntPtr.Zero);
        return result;
    }
    public static IntPtr FindVisibleTopLevelWindow(int targetProcessId) {
        IntPtr result = IntPtr.Zero;
        EnumWindows((handle, _) => {
            uint processId;
            GetWindowThreadProcessId(handle, out processId);
            if (processId == (uint)targetProcessId && IsWindowVisible(handle)) { result = handle; return false; }
            return true;
        }, IntPtr.Zero);
        return result;
    }
}
'@
}
function Find-FnCPNTrayElement {
    $root = [Windows.Automation.AutomationElement]::RootElement
    $matches = $root.FindAll([Windows.Automation.TreeScope]::Descendants, [Windows.Automation.Condition]::TrueCondition)
    for ($index = 0; $index -lt $matches.Count; $index++) {
        $candidate = $matches.Item($index)
        if ($candidate.Current.Name -match 'FnCPN' -and $candidate.Current.ClassName -match 'Tray|Notify|SystemTray' -and
            $candidate.Current.ProcessId -ne $process.Id -and
            $candidate.Current.ControlType -in @([Windows.Automation.ControlType]::Button, [Windows.Automation.ControlType]::ListItem)) {
            return $candidate
        }
    }
    return $null
}
function Find-FnCPNTrayOverflowButton {
    $root = [Windows.Automation.AutomationElement]::RootElement
    foreach ($label in @('显示隐藏的图标', 'Show hidden icons')) {
        $matches = $root.FindAll([Windows.Automation.TreeScope]::Descendants,
            [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, $label))
        for ($index = 0; $index -lt $matches.Count; $index++) {
            $candidate = $matches.Item($index)
            if ($candidate.Current.ControlType -eq [Windows.Automation.ControlType]::Button) { return $candidate }
        }
    }
    return $null
}
function Wait-ForAccessibleName {
    param(
        [Windows.Automation.AutomationElement]$Element,
        [string]$Pattern,
        [int]$TimeoutMilliseconds = 2000
    )
    $deadline = [DateTime]::UtcNow.AddMilliseconds($TimeoutMilliseconds)
    $actual = ''
    do {
        try { $actual = $Element.Current.Name }
        catch { $actual = '' }
        if ($actual -match $Pattern) { return $actual }
        Start-Sleep -Milliseconds 25
    } while ([DateTime]::UtcNow -lt $deadline)
    return $actual
}
function Send-SmokeKey([byte]$VirtualKey) {
    [FnCPNUiSmokeNative]::keybd_event($VirtualKey, 0, 0, [UIntPtr]::Zero)
    [FnCPNUiSmokeNative]::keybd_event($VirtualKey, 0, 2, [UIntPtr]::Zero)
}
function Wait-ForFocusedAutomationId([string]$AutomationId, [int]$TimeoutMilliseconds = 2000) {
    $deadline = [DateTime]::UtcNow.AddMilliseconds($TimeoutMilliseconds)
    do {
        try {
            $focused = [Windows.Automation.AutomationElement]::FocusedElement
            if ($null -ne $focused -and $focused.Current.AutomationId -eq $AutomationId) { return }
        }
        catch { }
        Start-Sleep -Milliseconds 25
    } while ([DateTime]::UtcNow -lt $deadline)
    $focusedId = try { [Windows.Automation.AutomationElement]::FocusedElement.Current.AutomationId } catch { '<unavailable>' }
    throw "Expected keyboard focus on '$AutomationId', got '$focusedId'."
}
$process = $null
$activationProcess = $null
$cliAuthorizeProcess = $null
$protocolRegistrationCreated = $false
$protocolKeyPath = 'Software\Classes\fncpn'
$passed = $false
$runKeyPath = 'Software\Microsoft\Windows\CurrentVersion\Run'
$runValueName = 'FnCPN'
$autoStartOriginalExists = $false
$autoStartOriginalValue = $null
$autoStartOriginalKind = [Microsoft.Win32.RegistryValueKind]::String
$autoStartChanged = $false
$runKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($runKeyPath, $false)
if ($null -ne $runKey) {
    try {
        $autoStartOriginalExists = $runKey.GetValueNames() -contains $runValueName
        if ($autoStartOriginalExists) {
            $autoStartOriginalValue = $runKey.GetValue($runValueName, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
            $autoStartOriginalKind = $runKey.GetValueKind($runValueName)
        }
    }
    finally { $runKey.Dispose() }
}
try {
    $start = [Diagnostics.ProcessStartInfo]::new($uiPath)
    $start.WorkingDirectory = Split-Path -Parent $uiPath
    $start.UseShellExecute = $false
    $start.Environment['LOCALAPPDATA'] = $isolatedLocalAppData
    $process = [Diagnostics.Process]::Start($start)
    $deadline = [DateTime]::UtcNow.AddSeconds(20)
    $window = $null
    while ([DateTime]::UtcNow -lt $deadline) {
        $process.Refresh()
        if ($process.HasExited) { throw "FnCPN exited during startup with code $($process.ExitCode)" }
        $root = [Windows.Automation.AutomationElement]::RootElement
        $condition = [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ProcessIdProperty, $process.Id)
        $windows = $root.FindAll([Windows.Automation.TreeScope]::Children, $condition)
        if ($windows.Count -gt 0) { $window = $windows.Item(0); break }
        Start-Sleep -Milliseconds 250
    }
    if ($null -eq $window) { throw 'Timed out waiting for the WPF window in the UI Automation tree.' }

    $trayElement = Find-FnCPNTrayElement
    $overflowButton = $null
    if ($null -eq $trayElement) {
        $root = [Windows.Automation.AutomationElement]::RootElement
        foreach ($label in @('显示隐藏的图标', 'Show hidden icons')) {
            $namedElements = $root.FindAll([Windows.Automation.TreeScope]::Descendants,
                [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, $label))
            for ($index = 0; $index -lt $namedElements.Count; $index++) {
                $candidate = $namedElements.Item($index)
                if ($candidate.Current.ControlType -eq [Windows.Automation.ControlType]::Button) {
                    $overflowButton = $candidate
                    break
                }
            }
            if ($null -ne $overflowButton) { break }
        }
        if ($null -ne $overflowButton) {
            $overflowButton.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
            Start-Sleep -Milliseconds 350
            $trayElement = Find-FnCPNTrayElement
            $overflowButton.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
        }
    }
    if ($null -eq $trayElement) {
        $traySummary = @()
        $trayRoot = [Windows.Automation.AutomationElement]::RootElement.FindAll(
            [Windows.Automation.TreeScope]::Descendants, [Windows.Automation.Condition]::TrueCondition)
        for ($index = 0; $index -lt $trayRoot.Count; $index++) {
            $element = $trayRoot.Item($index)
            if ($element.Current.ClassName -match 'Tray|Notify|SystemTray') {
                $traySummary += "$($element.Current.Name) [$($element.Current.ClassName)]"
            }
        }
        throw "The Windows notification area did not expose the running FnCPN NotifyIcon; tray elements: $($traySummary -join '; ')"
    }
    Write-Host "NotifyIcon presentation passed: Windows accessibility exposes FnCPN in the notification area (host PID $($trayElement.Current.ProcessId), class '$($trayElement.Current.ClassName)')."
    $taskbarCreatedMessage = [FnCPNUiSmokeNative]::RegisterWindowMessage('TaskbarCreated')
    $mainWindowHandle = [FnCPNUiSmokeNative]::FindTopLevelWindow($process.Id)
    if ($taskbarCreatedMessage -eq 0 -or $mainWindowHandle -eq [IntPtr]::Zero) {
        throw 'Could not resolve the WPF window or register the Explorer TaskbarCreated message.'
    }
    if (-not [FnCPNUiSmokeNative]::PostMessage($mainWindowHandle, $taskbarCreatedMessage, [IntPtr]::Zero, [IntPtr]::Zero)) {
        throw "Could not send the TaskbarCreated recovery message (Win32 $([Runtime.InteropServices.Marshal]::GetLastWin32Error()))."
    }
    $trayRecoveryDeadline = [DateTime]::UtcNow.AddSeconds(3)
    $recoveredTrayElement = $null
    do {
        Start-Sleep -Milliseconds 100
        $recoveredTrayElement = Find-FnCPNTrayElement
    } while ($null -eq $recoveredTrayElement -and [DateTime]::UtcNow -lt $trayRecoveryDeadline)
    if ($null -eq $recoveredTrayElement) {
        # Explorer may re-register a just-restored icon into its hidden-icons
        # flyout. Inspect that UIA subtree too before treating it as lost.
        $recoveryOverflowButton = Find-FnCPNTrayOverflowButton
        if ($null -ne $recoveryOverflowButton) {
            $recoveryOverflowButton.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
            try {
                $overflowRecoveryDeadline = [DateTime]::UtcNow.AddSeconds(2)
                do {
                    $recoveredTrayElement = Find-FnCPNTrayElement
                    if ($null -eq $recoveredTrayElement) { Start-Sleep -Milliseconds 100 }
                } while ($null -eq $recoveredTrayElement -and [DateTime]::UtcNow -lt $overflowRecoveryDeadline)
            }
            finally { $recoveryOverflowButton.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke() }
        }
    }
    if ($null -eq $recoveredTrayElement) { throw 'NotifyIcon was not exposed after the WPF window received TaskbarCreated.' }
    Write-Host 'NotifyIcon Explorer-recovery path passed: simulated TaskbarCreated caused the icon to remain/reappear in the Shell UIA tree.'
    $recoveredTrayElement.SetFocus()
    Start-Sleep -Milliseconds 150
    [FnCPNUiSmokeNative]::keybd_event(0x10, 0, 0, [UIntPtr]::Zero) # Shift down
    [FnCPNUiSmokeNative]::keybd_event(0x79, 0, 0, [UIntPtr]::Zero) # F10 down
    [FnCPNUiSmokeNative]::keybd_event(0x79, 0, 2, [UIntPtr]::Zero) # F10 up
    [FnCPNUiSmokeNative]::keybd_event(0x10, 0, 2, [UIntPtr]::Zero) # Shift up
    Start-Sleep -Milliseconds 150
    $trayMenuItem = $null
    foreach ($label in @('退出', 'Exit')) {
        $trayMenuItem = [Windows.Automation.AutomationElement]::RootElement.FindFirst(
            [Windows.Automation.TreeScope]::Descendants,
            [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, $label))
        if ($null -ne $trayMenuItem -and -not $trayMenuItem.Current.IsOffscreen) { break }
        $trayMenuItem = $null
    }
    if ($null -eq $trayMenuItem) {
        [FnCPNUiSmokeNative]::keybd_event(0x1B, 0, 0, [UIntPtr]::Zero) # Escape
        [FnCPNUiSmokeNative]::keybd_event(0x1B, 0, 2, [UIntPtr]::Zero)
        throw 'Shift+F10 on the accessible FnCPN notification icon did not expose the tray menu Exit item.'
    }
    $autoStartMenuItem = $null
    foreach ($label in @('登录 Windows 后启动 FnCPN', 'Start FnCPN when I sign in to Windows')) {
        $autoStartMenuItem = [Windows.Automation.AutomationElement]::RootElement.FindFirst(
            [Windows.Automation.TreeScope]::Descendants,
            [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, $label))
        if ($null -ne $autoStartMenuItem -and -not $autoStartMenuItem.Current.IsOffscreen) { break }
        $autoStartMenuItem = $null
    }
    if ($null -eq $autoStartMenuItem) {
        [FnCPNUiSmokeNative]::keybd_event(0x1B, 0, 0, [UIntPtr]::Zero)
        [FnCPNUiSmokeNative]::keybd_event(0x1B, 0, 2, [UIntPtr]::Zero)
        throw 'The tray context menu did not expose its per-user autostart item.'
    }
    $autoStartChanged = $true
    try { $autoStartMenuItem.GetCurrentPattern([Windows.Automation.TogglePattern]::Pattern).Toggle() }
    catch { $autoStartMenuItem.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke() }
    $runKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($runKeyPath, $false)
    $enabledAfterMenuToggle = $null -ne $runKey -and $runKey.GetValueNames() -contains $runValueName
    if ($null -ne $runKey) { $runKey.Dispose() }
    if ($enabledAfterMenuToggle -eq $autoStartOriginalExists) {
        throw 'Selecting the tray autostart item did not toggle the current-user Run registration.'
    }
    Write-Host "NotifyIcon menu passed: Shift+F10 exposed '$($trayMenuItem.Current.Name)' and its autostart command toggled HKCU Run."
    $edits = $window.FindAll([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ControlTypeProperty, [Windows.Automation.ControlType]::Edit))
    $fnId = $null
    $username = $null
    foreach ($edit in $edits) {
        if ($edit.Current.Name -eq 'FN Connect ID') { $fnId = $edit }
        if ($edit.Current.Name -eq 'fnOS 用户名' -or $edit.Current.Name -eq 'fnOS username') { $username = $edit }
    }
    if ($null -eq $fnId -or $null -eq $username) { throw 'Could not locate the named FN ID and username inputs in the accessibility tree.' }
    $valuePatternId = [Windows.Automation.ValuePattern]::Pattern
    $fnIdValue = $fnId.GetCurrentPattern($valuePatternId)
    $usernameValue = $username.GetCurrentPattern($valuePatternId)
    $login = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, '登录并连接'))
    if ($null -eq $login) { $login = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, 'Sign in and connect')) }
    if ($null -eq $login) { throw 'Could not locate the login button in the accessibility tree.' }
    $loginInvoke = $login.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern)
    $errorElement = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'LoginError'))
    if ($null -eq $errorElement) { throw 'Login form did not expose its accessible validation element.' }

    $fnId.SetFocus()
    Wait-ForFocusedAutomationId 'FnIdBox'
    Send-SmokeKey 0x09 # VK_TAB
    Wait-ForFocusedAutomationId 'UsernameBox'
    Send-SmokeKey 0x09 # VK_TAB
    Wait-ForFocusedAutomationId 'PasswordBox'
    Write-Host 'Keyboard accessibility passed: Tab moves from FN Connect ID to username and password in form order.'

    $fnIdValue.SetValue('https://example.invalid/not-a-fnos-share')
    $usernameValue.SetValue('')
    $loginInvoke.Invoke()
    $errorText = Wait-ForAccessibleName $errorElement 'FN Connect ID 格式不正确|Invalid FN Connect ID'
    if ($errorText -notmatch 'FN Connect ID 格式不正确|Invalid FN Connect ID') {
        throw "Expected accessible invalid-ID validation, got: '$errorText'"
    }
    Write-Host "UI validation passed: $errorText"

    $fnIdValue.SetValue('https://fnos.net/Home-Nas')
    $loginInvoke.Invoke()
    $errorText = Wait-ForAccessibleName $errorElement '请输入 fnOS 用户名|Enter your fnOS username'
    if ($errorText -notmatch '请输入 fnOS 用户名|Enter your fnOS username') {
        throw "Expected accessible username validation, got: '$errorText'"
    }
    Write-Host "UI validation passed: $errorText"

    $usernameValue.SetValue('codex-ui-smoke-user')
    $loginInvoke.Invoke()
    $errorText = Wait-ForAccessibleName $errorElement '请输入 fnOS 密码|Enter your fnOS password'
    if ($errorText -notmatch '请输入 fnOS 密码|Enter your fnOS password') {
        throw "Expected accessible password validation, got: '$errorText'"
    }
    Write-Host "UI validation passed: $errorText"

    $previousLocalAppData = $env:LOCALAPPDATA
    try {
        $env:LOCALAPPDATA = $isolatedLocalAppData
        $statusOutput = & $clientExePath status --json 2>&1
        $statusExitCode = $LASTEXITCODE
    }
    finally { $env:LOCALAPPDATA = $previousLocalAppData }
    if ($statusExitCode -ne 0) { throw "The isolated Go CLI could not connect to or start its client daemon: $($statusOutput -join [Environment]::NewLine)" }
    Write-Host 'CLI validation passed: status --json started or reached the isolated daemon.'

    # A same-user process can connect to the activation pipe and stall before
    # sending a frame. Its per-request timeout must not stop the app-wide listener.
    $activationPipeName = 'FnCPN.UI.Activation.' + $currentSessionId
    $stalledActivation = [IO.Pipes.NamedPipeClientStream]::new('.', $activationPipeName, [IO.Pipes.PipeDirection]::Out)
    try {
        $stalledActivation.Connect(5000)
        Start-Sleep -Milliseconds 5500
    }
    finally { $stalledActivation.Dispose() }

    $activationStart = [Diagnostics.ProcessStartInfo]::new($uiPath)
    $activationStart.WorkingDirectory = Split-Path -Parent $uiPath
    $activationStart.UseShellExecute = $false
    $beginResponse = Invoke-ClientPipeRequest 'authorization-begin' @{ fnId = 'activation-smoke' }
    if (-not $beginResponse.ok) { throw "Could not create a Go authorization transaction: $($beginResponse.error.message)" }
    $authorizationRequestId = [string]$beginResponse.result.requestId
    if ([string]::IsNullOrWhiteSpace($authorizationRequestId)) { throw 'The Go daemon returned an empty authorization request ID.' }
    $activationStart.Arguments = "fncpn://authorize/activation-smoke?request=$authorizationRequestId"
    $activationStart.Environment['LOCALAPPDATA'] = $isolatedLocalAppData
    $activationProcess = [Diagnostics.Process]::Start($activationStart)
    if (-not $activationProcess.WaitForExit(7000)) { throw 'The second UI instance did not exit after forwarding its authorization URI.' }
    if ($activationProcess.ExitCode -ne 0) { throw "The second UI instance exited with code $($activationProcess.ExitCode) while forwarding authorization." }
    $activationDeadline = [DateTime]::UtcNow.AddSeconds(5)
    $forwardedFnId = $null
    do {
        $activationFnId = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
            [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'FnIdBox'))
        if ($null -ne $activationFnId) {
            $forwardedFnId = $activationFnId.GetCurrentPattern($valuePatternId).Current.Value
            if ($forwardedFnId -eq 'activation-smoke') { break }
        }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $activationDeadline)
    if ($forwardedFnId -ne 'activation-smoke') { throw "The running UI did not receive the authorization FN ID; got '$forwardedFnId'." }
    Write-Host 'UI activation passed: the listener recovered from a stalled same-user pipe client and forwarded the next fncpn://authorize request.'

    $cancelLogin = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, '取消'))
    if ($null -eq $cancelLogin) { $cancelLogin = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, 'Cancel')) }
    if ($null -eq $cancelLogin) { throw 'External authorization did not expose an accessible Cancel button.' }
    $cancelLogin.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    $cancelDeadline = [DateTime]::UtcNow.AddSeconds(5)
    $cancelText = ''
    do {
        $cancelText = $errorElement.Current.Name
        if ($cancelText -match '登录已取消|Sign-in was canceled') { break }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $cancelDeadline)
    if ($cancelText -notmatch '登录已取消|Sign-in was canceled') { throw "External authorization cancellation was not shown accessibly; got '$cancelText'." }
    $waitResponse = Invoke-ClientPipeRequest 'authorization-wait' @{ requestId = $authorizationRequestId }
    if (-not $waitResponse.ok -or $waitResponse.result.state -ne 'canceled') {
        throw "The Go authorization transaction was not canceled; response: $(ConvertTo-Json $waitResponse -Compress -Depth 8)"
    }
    Write-Host "UI cancellation passed: $cancelText"

    # Exercise the real Windows ShellExecute URL association without installing the MSI.
    # Refuse to overwrite an existing per-user registration and remove this temporary one in finally.
    if (Test-Path -LiteralPath "HKCU:\$protocolKeyPath") {
        throw 'A per-user fncpn URL protocol registration already exists; refusing to modify it for the smoke test.'
    }
    $protocolKey = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($protocolKeyPath)
    try {
        $protocolKey.SetValue('', 'URL:FnCPN authorization protocol', [Microsoft.Win32.RegistryValueKind]::String)
        $protocolKey.SetValue('URL Protocol', '', [Microsoft.Win32.RegistryValueKind]::String)
        $protocolCommand = $protocolKey.CreateSubKey('shell\open\command')
        try { $protocolCommand.SetValue('', '"' + $uiPath + '" "%1"', [Microsoft.Win32.RegistryValueKind]::String) }
        finally { $protocolCommand.Dispose() }
    }
    finally { $protocolKey.Dispose() }
    $protocolRegistrationCreated = $true

    $cliStart = [Diagnostics.ProcessStartInfo]::new($clientExePath)
    $cliStart.WorkingDirectory = Split-Path -Parent $clientExePath
    $cliStart.UseShellExecute = $false
    $cliStart.RedirectStandardOutput = $true
    $cliStart.RedirectStandardError = $true
    $cliStart.ArgumentList.Add('authorize')
    $cliStart.ArgumentList.Add('activation-cli-smoke')
    $cliStart.Environment['LOCALAPPDATA'] = $isolatedLocalAppData
    $cliAuthorizeProcess = [Diagnostics.Process]::Start($cliStart)
    if ($null -eq $cliAuthorizeProcess) { throw 'Could not start the CLI authorization process.' }
    $cliStdoutTask = $cliAuthorizeProcess.StandardOutput.ReadToEndAsync()
    $cliStderrTask = $cliAuthorizeProcess.StandardError.ReadToEndAsync()

    $cliActivationDeadline = [DateTime]::UtcNow.AddSeconds(20)
    $cliForwardedFnId = $null
    do {
        $cliFnIdElement = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
            [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'FnIdBox'))
        if ($null -ne $cliFnIdElement) {
            $cliForwardedFnId = $cliFnIdElement.GetCurrentPattern($valuePatternId).Current.Value
            if ($cliForwardedFnId -eq 'activation-cli-smoke') { break }
        }
        $cliAuthorizeProcess.Refresh()
        if ($cliAuthorizeProcess.HasExited) {
            throw "CLI authorize exited before opening its UI (code $($cliAuthorizeProcess.ExitCode)): $($cliStderrTask.Result)"
        }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $cliActivationDeadline)
    if ($cliForwardedFnId -ne 'activation-cli-smoke') { throw "ShellExecute did not deliver the CLI FN ID to the running WPF window; got '$cliForwardedFnId'." }
    $cliCancelButton = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, '取消'))
    if ($null -eq $cliCancelButton) { $cliCancelButton = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, 'Cancel')) }
    if ($null -eq $cliCancelButton) { throw 'CLI authorization activation did not expose an accessible Cancel button.' }
    $cliCancelButton.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    if (-not $cliAuthorizeProcess.WaitForExit(15000)) { throw 'CLI authorize did not finish after the UI canceled its transaction.' }
    $cliStderr = $cliStderrTask.Result
    $cliAuthorizeProcess.Refresh()
    if ($cliAuthorizeProcess.ExitCode -ne 130 -or $cliStderr -notmatch 'CANCELED') {
        throw "CLI authorize did not report the canceled transaction (exit $($cliAuthorizeProcess.ExitCode)): $cliStderr"
    }
    Write-Host 'CLI ShellExecute validation passed: HKCU URL association launched/activated WPF, and CLI received the canceled terminal result.'

    $exitButton = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, '退出'))
    if ($null -eq $exitButton) { $exitButton = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, 'Quit')) }
    if ($null -eq $exitButton) { throw 'Could not locate the exit button in the accessibility tree.' }
    $exitButton.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    if (-not $process.WaitForExit(10000)) { throw 'The UI process did not exit after invoking Exit.' }
    $daemonDeadline = [DateTime]::UtcNow.AddSeconds(10)
    $remainingDaemons = @()
    do {
        $remainingDaemons = @(Get-CimInstance Win32_Process | Where-Object {
            $_.ExecutablePath -eq $clientExePath -and $_.CommandLine -match '\bclient\s+daemon\b' -and
            $_.CreationDate -ge $launchTime.AddSeconds(-2)
        })
        if ($remainingDaemons.Count -eq 0) { break }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $daemonDeadline)
    if ($remainingDaemons.Count -gt 0) {
        throw "The isolated Go client daemon did not exit after the UI quit (PID $($remainingDaemons.ProcessId -join ', '))."
    }

    # Start from a synthetic, non-secret config to exercise the full details
    # view even when no fnOS account or installed privileged Service is present.
    $configDirectory = Join-Path $isolatedLocalAppData 'FnCPN'
    New-Item -ItemType Directory -Path $configDirectory -Force | Out-Null
    $configPath = Join-Path $configDirectory 'config.json'
    [IO.File]::WriteAllText($configPath, '{"version":2,"fnId":"ui-details-smoke","configuration":{},"autoConnect":false}', [Text.UTF8Encoding]::new($false))
    $process.Dispose()
    $process = $null
    $detailsStart = [Diagnostics.ProcessStartInfo]::new($uiPath)
    $detailsStart.WorkingDirectory = Split-Path -Parent $uiPath
    $detailsStart.UseShellExecute = $false
    $detailsStart.Environment['LOCALAPPDATA'] = $isolatedLocalAppData
    $process = [Diagnostics.Process]::Start($detailsStart)
    if ($null -eq $process) { throw 'Could not start WPF for the details-view smoke test.' }
    $detailsDeadline = [DateTime]::UtcNow.AddSeconds(20)
    $window = $null
    $identityElement = $null
    do {
        $process.Refresh()
        if ($process.HasExited) { throw "WPF exited during details-view startup with code $($process.ExitCode)." }
        $root = [Windows.Automation.AutomationElement]::RootElement
        $condition = [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ProcessIdProperty, $process.Id)
        $windows = $root.FindAll([Windows.Automation.TreeScope]::Children, $condition)
        if ($windows.Count -gt 0) {
            $window = $windows.Item(0)
            $identityElement = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
                [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'IdentityText'))
            if ($null -ne $identityElement -and $identityElement.Current.Name -match 'ui-details-smoke') { break }
        }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $detailsDeadline)
    if ($null -eq $identityElement -or $identityElement.Current.Name -notmatch 'ui-details-smoke') {
        $visibleText = @()
        if ($null -ne $window) {
            $textCondition = [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ControlTypeProperty, [Windows.Automation.ControlType]::Text)
            $textElements = $window.FindAll([Windows.Automation.TreeScope]::Descendants, $textCondition)
            for ($index = 0; $index -lt $textElements.Count; $index++) {
                $text = $textElements.Item($index).Current.Name
                if (-not [string]::IsNullOrWhiteSpace($text)) { $visibleText += $text }
            }
        }
        $startupLogPath = Join-Path $isolatedLocalAppData 'FnCPN/Logs/ui-startup.log'
        $startupLog = if (Test-Path -LiteralPath $startupLogPath) { Get-Content -LiteralPath $startupLogPath -Raw } else { '<missing>' }
        throw "The configured synthetic profile did not open the WPF details view. Visible text: $($visibleText -join ' | '). Startup log: $startupLog"
    }
    $trayStateBaseline = Invoke-ClientPipeRequest 'watch-client-status' @{ after = 0 }
    if (-not $trayStateBaseline.ok -or -not $trayStateBaseline.result.changed) {
        throw 'Could not establish the isolated daemon status-watch baseline for tray state validation.'
    }
    $trayElement = Find-FnCPNTrayElement
    if ($null -eq $trayElement) {
        $overflowButton = Find-FnCPNTrayOverflowButton
        if ($null -ne $overflowButton) {
            $overflowButton.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
            Start-Sleep -Milliseconds 250
            $trayElement = Find-FnCPNTrayElement
        }
    }
    if ($null -eq $trayElement) { throw 'Could not locate the FnCPN tray icon before testing its configured-profile connection action.' }
    $trayElement.SetFocus()
    Start-Sleep -Milliseconds 100
    [FnCPNUiSmokeNative]::keybd_event(0x10, 0, 0, [UIntPtr]::Zero)
    [FnCPNUiSmokeNative]::keybd_event(0x79, 0, 0, [UIntPtr]::Zero)
    [FnCPNUiSmokeNative]::keybd_event(0x79, 0, 2, [UIntPtr]::Zero)
    [FnCPNUiSmokeNative]::keybd_event(0x10, 0, 2, [UIntPtr]::Zero)
    Start-Sleep -Milliseconds 250
    $connectMenuItems = [Windows.Automation.AutomationElement]::RootElement.FindAll(
        [Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, '连接'))
    $visibleConnectMenuItems = @()
    for ($index = 0; $index -lt $connectMenuItems.Count; $index++) {
        $item = $connectMenuItems.Item($index)
        if ($item.Current.ControlType -eq [Windows.Automation.ControlType]::MenuItem -and -not $item.Current.IsOffscreen) {
            $visibleConnectMenuItems += $item
        }
    }
    if ($visibleConnectMenuItems.Count -ne 1 -or -not $visibleConnectMenuItems[0].Current.IsEnabled) {
        [FnCPNUiSmokeNative]::keybd_event(0x1B, 0, 0, [UIntPtr]::Zero)
        [FnCPNUiSmokeNative]::keybd_event(0x1B, 0, 2, [UIntPtr]::Zero)
        throw "Configured-profile tray connection action is missing or disabled (visible count=$($visibleConnectMenuItems.Count))."
    }
    $visibleConnectMenuItems[0].GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    $statusGeneration = [UInt64]$trayStateBaseline.result.generation
    $authRequiredWatch = $null
    $lastObservedState = [string]$trayStateBaseline.result.status.state
    $connectStatusDeadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
        $statusWatch = Invoke-ClientPipeRequest 'watch-client-status' @{ after = $statusGeneration }
        if (-not $statusWatch.ok) { throw "Tray Connect action status watch failed: $($statusWatch.error | ConvertTo-Json -Compress -Depth 8)" }
        if ($statusWatch.result.changed) {
            $statusGeneration = [UInt64]$statusWatch.result.generation
            $lastObservedState = [string]$statusWatch.result.status.state
            if ($lastObservedState -eq 'AUTH_REQUIRED') { $authRequiredWatch = $statusWatch; break }
        }
        Start-Sleep -Milliseconds 50
    } while ([DateTime]::UtcNow -lt $connectStatusDeadline)
    if ($null -eq $authRequiredWatch) {
        throw "Selecting the configured-profile tray Connect action did not publish AUTH_REQUIRED within 15 seconds; last state was '$lastObservedState'."
    }
    Write-Host 'Configured-profile tray action passed: selecting Connect drove the Go daemon to AUTH_REQUIRED.'
    $primaryAction = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'PrimaryButton'))
    $authUiDeadline = [DateTime]::UtcNow.AddSeconds(5)
    do {
        if ($null -ne $primaryAction -and $primaryAction.Current.Name -eq '重新登录') { break }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $authUiDeadline)
    if ($null -eq $primaryAction -or $primaryAction.Current.Name -ne '重新登录') {
        $actualPrimaryLabel = if ($null -ne $primaryAction) { $primaryAction.Current.Name } else { '<missing>' }
        throw "The WPF details action did not reflect the AUTH_REQUIRED status; actual label='$actualPrimaryLabel'."
    }
    $stateLabel = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'StateText'))
    $stateLabelDeadline = [DateTime]::UtcNow.AddSeconds(5)
    do {
        if ($null -ne $stateLabel -and $stateLabel.Current.Name -eq '需要重新登录') { break }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $stateLabelDeadline)
    if ($null -eq $stateLabel -or $stateLabel.Current.Name -ne '需要重新登录') {
        $actualStateLabel = if ($null -ne $stateLabel) { $stateLabel.Current.Name } else { '<missing>' }
        throw "The WPF status presentation did not reflect the authentication-required state; actual label='$actualStateLabel'."
    }
    Start-Sleep -Milliseconds 150
    $trayElement = Find-FnCPNTrayElement
    if ($null -eq $trayElement) {
        $root = [Windows.Automation.AutomationElement]::RootElement
        $shellElements = $root.FindAll([Windows.Automation.TreeScope]::Descendants, [Windows.Automation.Condition]::TrueCondition)
        $overflowButton = $null
        for ($index = 0; $index -lt $shellElements.Count; $index++) {
            $candidate = $shellElements.Item($index)
            if ($candidate.Current.ControlType -eq [Windows.Automation.ControlType]::Button -and
                $candidate.Current.Name -match '(?i)hidden|隐藏|overflow') {
                $overflowButton = $candidate
                break
            }
        }
        if ($null -ne $overflowButton) {
            $overflowButton.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
            Start-Sleep -Milliseconds 250
            $trayElement = Find-FnCPNTrayElement
        }
    }
    if ($null -eq $trayElement) {
        $traySummary = @()
        $shellElements = [Windows.Automation.AutomationElement]::RootElement.FindAll(
            [Windows.Automation.TreeScope]::Descendants, [Windows.Automation.Condition]::TrueCondition)
        for ($index = 0; $index -lt $shellElements.Count; $index++) {
            $candidate = $shellElements.Item($index)
            if ($candidate.Current.ClassName -match 'Tray|Notify|SystemTray|Overflow' -or $candidate.Current.Name -match '(?i)FnCPN|hidden|隐藏|overflow') {
                $traySummary += "$($candidate.Current.Name) [$($candidate.Current.ClassName)] $($candidate.Current.ControlType)"
            }
        }
        throw "Could not locate the active details-process NotifyIcon for dynamic menu validation. Shell items: $($traySummary -join '; ')"
    }
    $visibleReloginItems = @()
    $visibleRetryItems = @()
    $visibleMenuNames = @()
    for ($menuAttempt = 0; $menuAttempt -lt 4; $menuAttempt++) {
        $trayElement.SetFocus()
        Start-Sleep -Milliseconds 100
        [FnCPNUiSmokeNative]::keybd_event(0x10, 0, 0, [UIntPtr]::Zero)
        [FnCPNUiSmokeNative]::keybd_event(0x79, 0, 0, [UIntPtr]::Zero)
        [FnCPNUiSmokeNative]::keybd_event(0x79, 0, 2, [UIntPtr]::Zero)
        [FnCPNUiSmokeNative]::keybd_event(0x10, 0, 2, [UIntPtr]::Zero)
        $menuDeadline = [DateTime]::UtcNow.AddMilliseconds(750)
        do {
            $reloginItems = [Windows.Automation.AutomationElement]::RootElement.FindAll(
                [Windows.Automation.TreeScope]::Descendants,
                [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, '重新登录'))
            $retryItems = [Windows.Automation.AutomationElement]::RootElement.FindAll(
                [Windows.Automation.TreeScope]::Descendants,
                [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, '重试连接'))
            $visibleReloginItems = @()
            $visibleRetryItems = @()
            for ($index = 0; $index -lt $reloginItems.Count; $index++) {
                $item = $reloginItems.Item($index)
                if ($item.Current.ControlType -eq [Windows.Automation.ControlType]::MenuItem -and -not $item.Current.IsOffscreen) {
                    $visibleReloginItems += $item
                }
            }
            for ($index = 0; $index -lt $retryItems.Count; $index++) {
                $item = $retryItems.Item($index)
                if ($item.Current.ControlType -eq [Windows.Automation.ControlType]::MenuItem -and -not $item.Current.IsOffscreen) {
                    $visibleRetryItems += $item
                }
            }
            if ($visibleReloginItems.Count -gt 0 -or $visibleRetryItems.Count -gt 0) { break }
            Start-Sleep -Milliseconds 50
        } while ([DateTime]::UtcNow -lt $menuDeadline)
        if ($visibleReloginItems.Count -gt 0 -or $visibleRetryItems.Count -gt 0) { break }
        $allMenuItems = [Windows.Automation.AutomationElement]::RootElement.FindAll(
            [Windows.Automation.TreeScope]::Descendants,
            [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ControlTypeProperty, [Windows.Automation.ControlType]::MenuItem))
        $visibleMenuNames = @()
        for ($index = 0; $index -lt $allMenuItems.Count; $index++) {
            $item = $allMenuItems.Item($index)
            if (-not $item.Current.IsOffscreen) { $visibleMenuNames += $item.Current.Name }
        }
        [FnCPNUiSmokeNative]::keybd_event(0x1B, 0, 0, [UIntPtr]::Zero)
        [FnCPNUiSmokeNative]::keybd_event(0x1B, 0, 2, [UIntPtr]::Zero)
        Start-Sleep -Milliseconds 200
    }
    if ($visibleReloginItems.Count -ne 1 -or -not $visibleReloginItems[0].Current.IsEnabled -or $visibleRetryItems.Count -ne 0) {
        [FnCPNUiSmokeNative]::keybd_event(0x1B, 0, 0, [UIntPtr]::Zero)
        [FnCPNUiSmokeNative]::keybd_event(0x1B, 0, 2, [UIntPtr]::Zero)
        throw "AUTH_REQUIRED tray menu state is inconsistent after four open attempts: visible relogin=$($visibleReloginItems.Count), retry=$($visibleRetryItems.Count), menu items=[$($visibleMenuNames -join ', ')]."
    }
    $visibleReloginItems[0].GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    $fnIdEdit = $null
    $loginUiDeadline = [DateTime]::UtcNow.AddSeconds(5)
    do {
        $fnIdEdit = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
            [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'FnIdBox'))
        if ($null -ne $fnIdEdit -and -not $fnIdEdit.Current.IsOffscreen) { break }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $loginUiDeadline)
    if ($null -eq $fnIdEdit -or $fnIdEdit.Current.IsOffscreen -or
        $fnIdEdit.GetCurrentPattern([Windows.Automation.ValuePattern]::Pattern).Current.Value -ne 'ui-details-smoke') {
        throw 'Selecting tray relogin did not open the login panel with the existing FN ID prefilled.'
    }
    $loginFailureElement = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'LoginError'))
    if ($null -eq $loginFailureElement -or
        $loginFailureElement.Current.Name -notmatch '(登录已失效|用户名或密码无效|session has expired|username or password)') {
        $actualLoginFailure = if ($null -eq $loginFailureElement) { '<missing>' } else { $loginFailureElement.Current.Name }
        throw "AUTH_REQUIRED relogin did not explain the cached authentication failure: '$actualLoginFailure'."
    }
    $returnToDetails = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, '返回连接详情'))
    if ($null -eq $returnToDetails) { $returnToDetails = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, 'Return to connection details')) }
    if ($null -eq $returnToDetails) { throw 'Tray relogin did not provide a return-to-details action.' }
    $returnToDetails.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    $identityDeadline = [DateTime]::UtcNow.AddSeconds(5)
    do {
        $identityElement = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
            [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'IdentityText'))
        if ($null -ne $identityElement -and -not $identityElement.Current.IsOffscreen) { break }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $identityDeadline)
    if ($null -eq $identityElement -or $identityElement.Current.Name -notmatch 'ui-details-smoke') {
        throw 'Returning after tray relogin did not restore the synthetic device details.'
    }
    Write-Host 'NotifyIcon dynamic state passed: AUTH_REQUIRED exposes enabled relogin, hides retry, and opens the prefilled login panel with a localized authentication explanation.'
    $networkElement = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'NetworkText'))
    $handshakeElement = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'HandshakeText'))
    $diagnosticsElement = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'DiagnosticsText'))
    if ($null -eq $networkElement -or $networkElement.Current.Name -match '(?i)网络指纹|fingerprint') {
        throw 'The details view exposes the raw network fingerprint in its physical-network summary.'
    }
    if ($null -eq $handshakeElement -or $handshakeElement.Current.Name -match '(?i)MTU') {
        throw 'The details view does not present handshake age separately from interface MTU.'
    }
    if ($null -eq $diagnosticsElement -or $diagnosticsElement.Current.Name -notmatch '"networkFingerprint"\s*:') {
        throw 'The full network fingerprint is missing from the copyable diagnostics JSON.'
    }
    foreach ($automationId in @('PrimaryButton', 'ChangeServerButton')) {
        $element = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
            [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, $automationId))
        if ($null -eq $element -or $element.Current.IsOffscreen) { throw "Details view action '$automationId' is missing or off-screen." }
    }
    $autoStartElement = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'AutoStartBox'))
    if ($null -eq $autoStartElement) { throw 'Details view is missing the accessible Windows sign-in autostart control.' }
    $autoStartToggle = $autoStartElement.GetCurrentPattern([Windows.Automation.TogglePattern]::Pattern)
    $runKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($runKeyPath, $false)
    $autoStartWasEnabled = $null -ne $runKey -and $runKey.GetValueNames() -contains $runValueName
    if ($null -ne $runKey) { $runKey.Dispose() }
    if (($autoStartToggle.Current.ToggleState -eq [Windows.Automation.ToggleState]::On) -ne $autoStartWasEnabled) {
        throw 'Autostart checkbox state does not match the current-user Run registration.'
    }
    $autoStartChanged = $true
    if ($autoStartWasEnabled) { $autoStartToggle.Toggle() }
    $runKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($runKeyPath, $false)
    $disabledValuePresent = $null -ne $runKey -and $runKey.GetValueNames() -contains $runValueName
    if ($null -ne $runKey) { $runKey.Dispose() }
    if ($disabledValuePresent -or $autoStartToggle.Current.ToggleState -ne [Windows.Automation.ToggleState]::Off) {
        throw 'Disabling autostart did not remove the per-user Run registration.'
    }
    $autoStartToggle.Toggle()
    $runKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($runKeyPath, $false)
    $enabledValue = if ($null -ne $runKey) { $runKey.GetValue($runValueName, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) } else { $null }
    if ($null -ne $runKey) { $runKey.Dispose() }
    $expectedRunValue = '"' + $uiPath + '" --background'
    if ($enabledValue -ne $expectedRunValue -or $autoStartToggle.Current.ToggleState -ne [Windows.Automation.ToggleState]::On) {
        throw "Enabling autostart did not register the current WPF executable with --background. Expected '$expectedRunValue', got '$enabledValue'."
    }
    Write-Host 'Autostart validation passed: checkbox state and per-user Run registration toggle together with --background.'
    if (-not (Test-Path -LiteralPath $startupLogPath)) { throw 'WPF startup diagnostics were not written under the isolated LOCALAPPDATA profile.' }
    $startupLogContent = Get-Content -LiteralPath $startupLogPath -Raw
    if ($startupLogContent -notmatch [regex]::Escape("Resolved client executable: $clientExePath; exists=True.")) {
        throw "WPF startup diagnostics did not use the isolated LOCALAPPDATA profile or expected Go client path: $startupLogContent"
    }
    $startupLogBackupPath = $startupLogPath + '.1'
    if (-not (Test-Path -LiteralPath $startupLogBackupPath)) { throw 'WPF startup log did not rotate the oversized existing log to its single backup.' }
    $startupLogBackup = [IO.File]::ReadAllText($startupLogBackupPath)
    if (-not $startupLogBackup.StartsWith($startupLogRotationMarker, [StringComparison]::Ordinal)) {
        throw 'WPF startup log rotation did not preserve the previous log in the backup.'
    }
    if ((Get-Item -LiteralPath $startupLogPath).Length -gt 1MB) { throw 'WPF startup log exceeded its 1 MiB size bound after rotation.' }
    Write-Host 'User data path and rotation validation passed: isolated LOCALAPPDATA, correct Go path, 1 MiB limit, and one preserved backup.'
    $changeServerButton = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'ChangeServerButton'))
    $changeServerButton.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    $fnIdEdit = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'FnIdBox'))
    $usernameEdit = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'UsernameBox'))
    if ($null -eq $fnIdEdit -or $null -eq $usernameEdit -or
        $fnIdEdit.GetCurrentPattern([Windows.Automation.ValuePattern]::Pattern).Current.Value -ne '' -or
        $usernameEdit.GetCurrentPattern([Windows.Automation.ValuePattern]::Pattern).Current.Value -ne '') {
        throw 'Switch server did not clear the current login form.'
    }
    $watchBaseline = Invoke-ClientPipeRequest 'watch-client-status' @{ after = 0 }
    if (-not $watchBaseline.ok -or -not $watchBaseline.result.changed) {
        throw 'Could not establish the isolated daemon status-watch baseline.'
    }
    $disconnectResult = Invoke-ClientPipeRequest 'disconnect' @{}
    $watchChanged = Invoke-ClientPipeRequest 'watch-client-status' @{ after = [UInt64]$watchBaseline.result.generation }
    if (-not $watchChanged.ok -or -not $watchChanged.result.changed -or $watchChanged.result.status.state -ne 'PAUSED') {
        throw 'The isolated daemon did not publish the expected disconnect status-watch update.'
    }
    Start-Sleep -Milliseconds 500
    if ($fnIdEdit.Current.IsOffscreen -or $usernameEdit.Current.IsOffscreen) {
        throw 'A background daemon status refresh displaced the user from the switch-server login form.'
    }
    Write-Host 'Diagnostic refresh view-state passed: a real Go disconnect status update preserved the active login form.'
    $returnDetailsButton = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, '返回连接详情'))
    if ($null -eq $returnDetailsButton) { $returnDetailsButton = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, 'Return to connection details')) }
    if ($null -eq $returnDetailsButton) { throw 'Switch server did not provide a return-to-details action for the existing device.' }
    $returnDetailsButton.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    $identityDeadline = [DateTime]::UtcNow.AddSeconds(5)
    do {
        $identityElement = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
            [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'IdentityText'))
        if ($null -ne $identityElement -and $identityElement.Current.Name -match 'ui-details-smoke') { break }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $identityDeadline)
    if ($null -eq $identityElement -or $identityElement.Current.Name -notmatch 'ui-details-smoke') {
        throw 'Returning from switch server forgot or replaced the existing daemon identity.'
    }
    Write-Host 'Switch server validation passed: form values clear, returning restores the existing daemon identity without forgetting it.'

    # Closing the window must leave the tray application and Go daemon alive;
    # a normal second launch should restore that same instance.
    $detailsHandle = [IntPtr]$window.Current.NativeWindowHandle
    if ($detailsHandle -eq [IntPtr]::Zero -or -not [FnCPNUiSmokeNative]::PostMessage($detailsHandle, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)) {
        throw 'Could not request a normal WPF window close for the tray-residency smoke test.'
    }
    $hiddenDeadline = [DateTime]::UtcNow.AddSeconds(5)
    do {
        $process.Refresh()
        if ($process.HasExited) { throw 'Closing the main window exited the tray application.' }
        if (-not [FnCPNUiSmokeNative]::IsWindowVisible($detailsHandle)) { break }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $hiddenDeadline)
    if ([FnCPNUiSmokeNative]::IsWindowVisible($detailsHandle)) { throw 'Closing the main window did not hide it to the tray.' }
    $residentDaemon = Invoke-ClientPipeRequest 'status' @{}
    if (-not $residentDaemon.ok) { throw 'The Go daemon stopped when the main window was closed to the tray.' }

    $trayRestoreStart = [Diagnostics.ProcessStartInfo]::new($uiPath)
    $trayRestoreStart.WorkingDirectory = Split-Path -Parent $uiPath
    $trayRestoreStart.UseShellExecute = $false
    $trayRestoreStart.Environment['LOCALAPPDATA'] = $isolatedLocalAppData
    $trayRestoreProcess = [Diagnostics.Process]::Start($trayRestoreStart)
    if ($null -eq $trayRestoreProcess -or -not $trayRestoreProcess.WaitForExit(7000)) {
        throw 'A second launch did not finish forwarding activation to the tray-resident instance.'
    }
    if ($trayRestoreProcess.ExitCode -ne 0) { throw "Tray-resident activation exited with code $($trayRestoreProcess.ExitCode)." }
    $trayRestoreProcess.Dispose()
    $trayRestoreDeadline = [DateTime]::UtcNow.AddSeconds(5)
    $window = $null
    do {
        $restoredHandle = [FnCPNUiSmokeNative]::FindVisibleTopLevelWindow($process.Id)
        if ($restoredHandle -ne [IntPtr]::Zero) {
            $window = [Windows.Automation.AutomationElement]::FromHandle($restoredHandle)
            if ($null -ne $window) { break }
        }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $trayRestoreDeadline)
    if ($null -eq $window) { throw 'The tray-resident main window did not return after a second launch.' }
    $restoredIdentity = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'IdentityText'))
    if ($null -eq $restoredIdentity -or $restoredIdentity.Current.Name -notmatch 'ui-details-smoke') {
        throw 'Tray activation did not restore the existing client identity.'
    }
    Write-Host 'Window-close tray validation passed: close hid WPF while daemon stayed alive, and a second launch restored the same identity.'

    Write-Host 'Details view passed: WPF bootstrapped the isolated Go daemon without CLI prewarming; concise physical network summary, separate handshake/MTU fields, full diagnostic fingerprint, and primary/server-switch actions are visible.'
    $exitButton = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, '退出'))
    if ($null -eq $exitButton) { $exitButton = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, 'Quit')) }
    if ($null -eq $exitButton) { throw 'Could not locate the exit button for the details-view smoke test.' }
    $exitButton.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    if (-not $process.WaitForExit(10000)) { throw 'WPF did not exit after the details-view smoke test.' }
    $process.Dispose()
    $process = $null
    $daemonDeadline = [DateTime]::UtcNow.AddSeconds(10)
    $remainingDaemons = @()
    do {
        $remainingDaemons = @(Get-CimInstance Win32_Process | Where-Object {
            $_.ExecutablePath -eq $clientExePath -and $_.CommandLine -match '\bclient\s+daemon\b' -and
            $_.CreationDate -ge $launchTime.AddSeconds(-2)
        })
        if ($remainingDaemons.Count -eq 0) { break }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $daemonDeadline)
    if ($remainingDaemons.Count -gt 0) {
        throw "The details-view client daemon did not exit after the UI quit (PID $($remainingDaemons.ProcessId -join ', '))."
    }

    # A configured profile launched through HKCU Run must remain resident but hidden;
    # launching the app again without --background should activate that instance.
    $backgroundStart = [Diagnostics.ProcessStartInfo]::new($uiPath)
    $backgroundStart.WorkingDirectory = Split-Path -Parent $uiPath
    $backgroundStart.UseShellExecute = $false
    $backgroundStart.ArgumentList.Add('--background')
    $backgroundStart.Environment['LOCALAPPDATA'] = $isolatedLocalAppData
    $process = [Diagnostics.Process]::Start($backgroundStart)
    if ($null -eq $process) { throw 'Could not start WPF with --background for the configured-profile test.' }
    $backgroundDeadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
        $process.Refresh()
        if ($process.HasExited) { throw "Configured background WPF exited with code $($process.ExitCode)." }
        $windowHandle = [FnCPNUiSmokeNative]::FindTopLevelWindow($process.Id)
        if ($windowHandle -ne [IntPtr]::Zero) { break }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $backgroundDeadline)
    if ($windowHandle -eq [IntPtr]::Zero) { throw 'Configured background WPF did not create its main window.' }
    Start-Sleep -Milliseconds 500
    if ([FnCPNUiSmokeNative]::IsWindowVisible($windowHandle)) {
        throw 'Configured --background startup left the WPF window visible instead of hiding it to the tray.'
    }
    Write-Host 'Background startup validation passed: a configured profile stays resident with its window hidden.'

    $showStart = [Diagnostics.ProcessStartInfo]::new($uiPath)
    $showStart.WorkingDirectory = Split-Path -Parent $uiPath
    $showStart.UseShellExecute = $false
    $showStart.Environment['LOCALAPPDATA'] = $isolatedLocalAppData
    $activationProcess = [Diagnostics.Process]::Start($showStart)
    if ($null -eq $activationProcess -or -not $activationProcess.WaitForExit(7000)) {
        throw 'A normal second launch did not activate the configured background instance.'
    }
    $windowDeadline = [DateTime]::UtcNow.AddSeconds(5)
    $window = $null
    do {
        $visibleHandle = [FnCPNUiSmokeNative]::FindVisibleTopLevelWindow($process.Id)
        if ($visibleHandle -ne [IntPtr]::Zero) {
            $windowHandle = $visibleHandle
            $window = [Windows.Automation.AutomationElement]::FromHandle($windowHandle)
            if ($null -ne $window) { break }
        }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $windowDeadline)
    if ($null -eq $window) {
        $visibleAfterActivation = [FnCPNUiSmokeNative]::IsWindowVisible($windowHandle)
        $rootChildren = [Windows.Automation.AutomationElement]::RootElement.FindAll([Windows.Automation.TreeScope]::Children, [Windows.Automation.Condition]::TrueCondition)
        $rootSummary = @()
        for ($index = 0; $index -lt $rootChildren.Count; $index++) {
            $element = $rootChildren.Item($index)
            if ($element.Current.ProcessId -eq $process.Id) { $rootSummary += "$($element.Current.Name):$($element.Current.NativeWindowHandle)" }
        }
        $activationLogPath = Join-Path $isolatedLocalAppData 'FnCPN/Logs/ui-startup.log'
        $activationLog = if (Test-Path -LiteralPath $activationLogPath) { Get-Content -LiteralPath $activationLogPath -Raw } else { '<missing>' }
        throw "Normal second launch did not restore the configured background window (visible=$visibleAfterActivation, handle=$windowHandle, processExited=$($process.HasExited), UIA=$($rootSummary -join ',')). Startup log: $activationLog"
    }
    $exitButton = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, '退出'))
    if ($null -eq $exitButton) { $exitButton = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, 'Quit')) }
    if ($null -eq $exitButton) { throw 'Could not locate the restored background window Exit action.' }
    $exitButton.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    if (-not $process.WaitForExit(10000)) { throw 'Restored configured background WPF did not exit normally.' }
    $process.Dispose()
    $process = $null
    $activationProcess.Dispose()
    $activationProcess = $null
    $daemonDeadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        $remainingDaemons = @(Get-CimInstance Win32_Process | Where-Object {
            $_.ExecutablePath -eq $clientExePath -and $_.CommandLine -match '\bclient\s+daemon\b' -and
            $_.CreationDate -ge $launchTime.AddSeconds(-2)
        })
        if ($remainingDaemons.Count -eq 0) { break }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $daemonDeadline)
    if ($remainingDaemons.Count -gt 0) { throw 'Configured background WPF did not stop its daemon after normal exit.' }

    # With no saved identity, --background must show the first-use login UI.
    $firstUseLocalAppData = Join-Path $isolatedRoot 'FirstUseLocalAppData'
    New-Item -ItemType Directory -Path $firstUseLocalAppData -Force | Out-Null
    $firstUseStart = [Diagnostics.ProcessStartInfo]::new($uiPath)
    $firstUseStart.WorkingDirectory = Split-Path -Parent $uiPath
    $firstUseStart.UseShellExecute = $false
    $firstUseStart.ArgumentList.Add('--background')
    $firstUseStart.Environment['LOCALAPPDATA'] = $firstUseLocalAppData
    $process = [Diagnostics.Process]::Start($firstUseStart)
    if ($null -eq $process) { throw 'Could not start first-use WPF with --background.' }
    $firstUseDeadline = [DateTime]::UtcNow.AddSeconds(15)
    $window = $null
    do {
        $process.Refresh()
        if ($process.HasExited) { throw "First-use background WPF exited with code $($process.ExitCode)." }
        $condition = [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ProcessIdProperty, $process.Id)
        $window = [Windows.Automation.AutomationElement]::RootElement.FindFirst([Windows.Automation.TreeScope]::Children, $condition)
        if ($null -ne $window) { break }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $firstUseDeadline)
    $firstUseVisibleDeadline = [DateTime]::UtcNow.AddSeconds(15)
    $firstUseHandle = [IntPtr]::Zero
    do {
        $firstUseHandle = [FnCPNUiSmokeNative]::FindVisibleTopLevelWindow($process.Id)
        if ($firstUseHandle -ne [IntPtr]::Zero) { break }
        $process.Refresh()
        if ($process.HasExited) { break }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $firstUseVisibleDeadline)
    if ($null -eq $window -or $firstUseHandle -eq [IntPtr]::Zero) {
        $firstUseLogPath = Join-Path $firstUseLocalAppData 'FnCPN/Logs/ui-startup.log'
        $firstUseLog = if (Test-Path -LiteralPath $firstUseLogPath) { Get-Content -LiteralPath $firstUseLogPath -Raw } else { '<missing>' }
        throw "First-use --background startup did not show the login window (window=$($null -ne $window), handle=$firstUseHandle, processExited=$($process.HasExited)). Startup log: $firstUseLog"
    }
    $loginErrorElement = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty, 'LoginError'))
    if ($null -eq $loginErrorElement) { throw 'First-use background login window is missing its accessible login feedback.' }
    $firstUseExit = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, '退出'))
    if ($null -eq $firstUseExit) { $firstUseExit = $window.FindFirst([Windows.Automation.TreeScope]::Descendants,
        [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, 'Quit')) }
    if ($null -eq $firstUseExit) { throw 'First-use background login window has no accessible Exit action.' }
    $firstUseExit.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    if (-not $process.WaitForExit(10000)) { throw 'First-use background WPF did not exit normally.' }
    $process.Dispose()
    $process = $null
    Write-Host 'First-use background validation passed: an unconfigured profile shows the login window.'
    $passed = $true
    Write-Host "UI smoke passed for ${Architecture}: login validation, URI activation/cancellation, CLI ShellExecute, WPF daemon bootstrap, details diagnostics/actions, switch-server identity, background startup, and normal exit."
}
finally {
    if ($null -ne $process) {
        $process.Refresh()
        if (-not $process.HasExited) { Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue }
        $process.Dispose()
    }
    if ($null -ne $activationProcess) {
        $activationProcess.Refresh()
        if (-not $activationProcess.HasExited) { Stop-Process -Id $activationProcess.Id -Force -ErrorAction SilentlyContinue }
        $activationProcess.Dispose()
    }
    if ($null -ne $cliAuthorizeProcess) {
        $cliAuthorizeProcess.Refresh()
        if (-not $cliAuthorizeProcess.HasExited) { Stop-Process -Id $cliAuthorizeProcess.Id -Force -ErrorAction SilentlyContinue }
        $cliAuthorizeProcess.Dispose()
    }
    if ($protocolRegistrationCreated) {
        Remove-Item -LiteralPath "HKCU:\$protocolKeyPath" -Recurse -Force -ErrorAction SilentlyContinue
    }
    if ($autoStartChanged) {
        $runKey = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($runKeyPath)
        try {
            if ($autoStartOriginalExists) { $runKey.SetValue($runValueName, $autoStartOriginalValue, $autoStartOriginalKind) }
            else { $runKey.DeleteValue($runValueName, $false) }
        }
        finally { $runKey.Dispose() }
    }
    $leftoverDaemons = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue | Where-Object {
        $_.ExecutablePath -eq $clientExePath -and $_.CommandLine -match '\bclient\s+daemon\b' -and
        $_.CreationDate -ge $launchTime.AddSeconds(-2)
    })
    foreach ($daemon in $leftoverDaemons) { Stop-Process -Id $daemon.ProcessId -Force -ErrorAction SilentlyContinue }
    $resolvedRoot = [IO.Path]::GetFullPath($isolatedRoot)
    $tempRoot = [IO.Path]::GetFullPath($env:TEMP).TrimEnd('\') + '\'
    if ($resolvedRoot.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -and $resolvedRoot -like '*FnCPN-ui-smoke-*') {
        Remove-Item -LiteralPath $resolvedRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
if (-not $passed) { exit 1 }
