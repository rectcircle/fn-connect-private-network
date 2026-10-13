#Requires -Version 5.1
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$tokens = $null; $errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot '../setup-windows.ps1'), [ref]$tokens, [ref]$errors)
if ($errors.Count) { throw ($errors | Out-String) }
$function = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Install-WingetPackage' }, $true)
Invoke-Expression $function.Extent.Text
function Write-SetupLog($Level, $Message) {}
function Find-Tool($Name, $Candidates) { return 'Fake-Winget' }
function Fake-Winget {
    $script:received = @($args)
    $global:LASTEXITCODE = $script:exitCode
}
$savedPath = $env:Path
try {
    foreach ($code in @(0, 3010, 42)) {
        $script:restartRequired = $false; $script:exitCode = $code
        $failed = $false
        try { Install-WingetPackage 'Example.Tools' '--wait --norestart' } catch { $failed = $true }
        if ($failed -ne ($code -eq 42)) { throw "Unexpected installer result: $code" }
        if ($script:restartRequired -ne ($code -eq 3010)) { throw "Unexpected restart flag: $code" }
        $overrideIndex = [Array]::IndexOf($script:received, '--override')
        if ($overrideIndex -lt 0 -or $script:received[$overrideIndex + 1] -ne '--wait --norestart') { throw 'Installer override must remain a single argument.' }
        Write-Host "PASS installer exit $code"
    }
} finally { $env:Path = $savedPath }
