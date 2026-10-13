#Requires -Version 5.1
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProjectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$tokens = $null; $parseErrors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot '../setup-windows.ps1'), [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count) { throw ($parseErrors | Out-String) }
$ast.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -in @('Test-GoModuleProxy', 'Initialize-GoModuleProxy') }, $true) |
    ForEach-Object { Invoke-Expression $_.Extent.Text }
function Write-SetupLog($Level, $Message) {}
function Invoke-WebRequest($Uri, $TimeoutSec, [switch]$UseBasicParsing) {
    $script:requests += $Uri
    if ($TimeoutSec -ne 15) { throw 'Missing bounded timeout' }
    if ($Uri.StartsWith('https://proxy.golang.org') -and -not $script:primaryWorks) { throw 'timeout' }
    if ($Uri.StartsWith('https://goproxy.cn') -and -not $script:chinaWorks) { throw 'timeout' }
    if ($Uri.StartsWith('https://goproxy.io') -and -not $script:fallbackWorks) { throw 'timeout' }
    return @{ StatusCode = 200; Content = "module example.com/probe`n" }
}
function Fake-Go {
    $global:LASTEXITCODE = 0
    return $script:configuredProxy
}
function Invoke-Tool($File, $ToolArgs) { $script:calls += ($ToolArgs -join ' ') }
$script:go = 'Fake-Go'
$savedProxy = [Environment]::GetEnvironmentVariable('GOPROXY', 'Process')
try {
    foreach ($scenario in @('default', 'china', 'fallback', 'unreachable', 'custom')) {
        $script:requests = @(); $script:calls = @()
        $script:configuredProxy = 'https://proxy.golang.org,direct'
        $env:GOPROXY = $script:configuredProxy
        $script:primaryWorks = $scenario -eq 'default'
        $script:chinaWorks = $scenario -eq 'china'
        $script:fallbackWorks = $scenario -ne 'unreachable'
        if ($scenario -eq 'custom') { $script:configuredProxy = 'https://company.example,direct'; $env:GOPROXY = $script:configuredProxy }
        $failed = $false
        try { Initialize-GoModuleProxy } catch { $failed = $true }
        if ($failed -ne ($scenario -eq 'unreachable')) { throw "Unexpected result: $scenario" }
        $writes = @($script:calls | Where-Object { $_ -like 'env -w*' })
        if ($writes.Count -ne [int]($scenario -in @('china', 'fallback'))) { throw "Unexpected configuration write: $scenario" }
        if ($scenario -eq 'china' -and ($env:GOPROXY -ne 'https://goproxy.cn,direct' -or @($script:requests | Where-Object { $_.StartsWith('https://goproxy.io') }).Count -ne 0)) { throw 'China proxy was not preferred' }
        if ($scenario -eq 'fallback' -and $env:GOPROXY -ne 'https://goproxy.io,direct') { throw 'Process override was not updated' }
        if ($scenario -eq 'custom' -and ($script:requests.Count -ne 0 -or $env:GOPROXY -ne $script:configuredProxy)) { throw 'Custom proxy changed' }
        if (($script:calls -contains 'mod download') -ne ($scenario -ne 'unreachable')) { throw "Unexpected download: $scenario" }
        if ($scenario -eq 'unreachable' -and $env:GOPROXY -ne $script:configuredProxy) { throw 'Failed probe changed configuration' }
        Write-Host "PASS $scenario"
    }
}
finally { [Environment]::SetEnvironmentVariable('GOPROXY', $savedProxy, 'Process') }
