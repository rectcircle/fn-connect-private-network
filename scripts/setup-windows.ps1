#Requires -Version 5.1
[CmdletBinding()]
param([string]$ProjectRoot, [switch]$SkipSmoke, [switch]$IncludeServerTools, [switch]$IncludeReleaseTools)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$timer = [Diagnostics.Stopwatch]::StartNew()
if (-not $ProjectRoot) { $ProjectRoot = Split-Path -Parent $PSScriptRoot }
$ProjectRoot = [IO.Path]::GetFullPath($ProjectRoot)
$devRoot = Join-Path $ProjectRoot '.dev'
$downloads = Join-Path $devRoot 'downloads'
$logs = Join-Path $devRoot 'logs'
$null = New-Item -ItemType Directory -Force -Path $downloads, $logs
$logFile = Join-Path $logs ('setup-windows-{0}-{1}.log' -f (Get-Date -Format 'yyyyMMdd-HHmmss'), $PID)
$utf8 = New-Object Text.UTF8Encoding($false)

function Write-SetupLog([string]$Level, [string]$Message) {
    $line = '[setup-windows] [累计耗时={0:N1}秒] {1}: {2}' -f $timer.Elapsed.TotalSeconds, $Level, $Message
    Write-Host $line
    [IO.File]::AppendAllText($logFile, $line + [Environment]::NewLine, $utf8)
}

function Invoke-Stage([string]$Name, [scriptblock]$Action) {
    $stageTimer = [Diagnostics.Stopwatch]::StartNew()
    Write-SetupLog '检查' $Name
    try { & $Action }
    finally { Write-SetupLog '阶段耗时' ('{0}: {1:N1}秒' -f $Name, $stageTimer.Elapsed.TotalSeconds) }
}

function Invoke-Tool([string]$File, [string[]]$ToolArgs) {
    if (-not (Test-Path -LiteralPath $File -PathType Leaf)) { throw "工具不存在：$File" }
    # Windows PowerShell 5.1 将 native stderr 包装为 ErrorRecord；以退出码判断失败。
    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        & $File @ToolArgs 2>&1 | ForEach-Object { Write-SetupLog '输出' "$_" }
        $code = $LASTEXITCODE
    }
    finally { $ErrorActionPreference = $previousPreference }
    if ($code -ne 0) { throw "命令失败 ($code): $File $($ToolArgs -join ' ')" }
}

function Get-CheckedArchive([string]$Url, [string]$Name, [string]$Hash, [string]$Algorithm = 'SHA256') {
    $archive = Join-Path $downloads $Name
    if ((Test-Path -LiteralPath $archive) -and (Get-FileHash -LiteralPath $archive -Algorithm $Algorithm).Hash -eq $Hash) {
        Write-SetupLog '跳过' "下载缓存已校验：$Name"
        return $archive
    }
    $partial = "$archive.partial"
    Write-SetupLog '下载' $Url
    Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $partial
    if ((Get-FileHash -LiteralPath $partial -Algorithm $Algorithm).Hash -ne $Hash) {
        throw "下载校验失败，保留现场：$partial"
    }
    Move-Item -LiteralPath $partial -Destination $archive -Force
    return $archive
}

function Install-Archive([string]$Archive, [string]$Destination, [string]$InnerDirectory = '') {
    # 解压到唯一暂存目录；最终重命名为提交点。失败不留下冒充已安装的目录。
    if (Test-Path -LiteralPath $Destination) { throw "安装目录存在但验证未通过，拒绝覆盖：$Destination" }
    $parent = Split-Path -Parent $Destination
    $null = New-Item -ItemType Directory -Force -Path $parent
    $staging = Join-Path $parent ('.setup-' + [guid]::NewGuid().ToString('N'))
    Expand-Archive -LiteralPath $Archive -DestinationPath $staging
    $source = $staging
    if ($InnerDirectory) { $source = Join-Path $staging $InnerDirectory }
    Move-Item -LiteralPath $source -Destination $Destination
    Write-SetupLog '安装' $Destination
}

function Add-UserPath([string]$Directory) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @($userPath -split ';' | Where-Object { $_ })
    if ($entries -notcontains $Directory) {
        [Environment]::SetEnvironmentVariable('Path', (($entries + $Directory) -join ';'), 'User')
        Write-SetupLog '配置' "用户 PATH 添加：$Directory"
    }
    if (($env:Path -split ';') -notcontains $Directory) { $env:Path = "$Directory;$env:Path" }
}

function Find-Tool([string]$Name, [string[]]$Candidates) {
    foreach ($candidate in $Candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
    }
    $command = Get-Command $Name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) { return $command.Path }
    return $null
}

function Install-WingetPackage([string]$Id, [string]$Override = '') {
    $winget = Find-Tool 'winget.exe' @()
    if (-not $winget) { throw '缺少 App Installer / WinGet，无法安装系统前置组件。' }
    Write-SetupLog '安装' "通过 WinGet 安装缺失组件：$Id；系统组件可能弹出 UAC。"
    $arguments = @('install', '--id', $Id, '--exact', '--source', 'winget', '--silent', '--disable-interactivity', '--accept-package-agreements', '--accept-source-agreements')
    if ($Override) { $arguments += @('--override', $Override) }
    & $winget @arguments 2>&1 |
        ForEach-Object { Write-SetupLog '输出' "$_" }
    $code = $LASTEXITCODE
    if ($code -eq 3010) {
        $script:restartRequired = $true
        Write-SetupLog '提示' "$Id 报告需要系统重启，仍在本次执行验证可用性。"
    }
    elseif ($code -ne 0) { throw "安装 $Id 失败：$code" }
    $env:Path = $env:Path + ';' + [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')
}

function Test-GoModuleProxy([string]$Proxy) {
    # 使用仓库实际的 Linux 专属依赖，而非首页；缓存不能掩盖网络不可达。
    $modules = @(Get-Content -LiteralPath (Join-Path $ProjectRoot 'go.mod') | Where-Object {
        $_ -match '^\s*(github.com/google/nftables|github.com/mdlayher/(genetlink|netlink))\s+v'
    })
    if ($modules.Count -ne 3) { throw 'Go 代理探测依赖列表已变化，请更新探测机制。' }
    foreach ($line in $modules) {
        $parts = $line.Trim() -split '\s+'
        $uri = "$Proxy/$($parts[0])/@v/$($parts[1]).mod"
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $uri -TimeoutSec 15
            if ($response.StatusCode -ne 200 -or $response.Content -notmatch '(?m)^module\s+') {
                throw '响应不是有效的 Go module 文件。'
            }
        }
        catch {
            Write-SetupLog '探测失败' "$uri：$($_.Exception.Message)"
            return $false
        }
    }
    return $true
}

function Initialize-GoModuleProxy {
    $proxy = & $script:go env GOPROXY
    if ($LASTEXITCODE -ne 0) { throw '无法读取 Go GOPROXY 配置。' }
    $proxy = "$proxy".Trim()
    if ($proxy -eq 'https://proxy.golang.org,direct') {
        if (Test-GoModuleProxy 'https://proxy.golang.org') {
            Write-SetupLog '已验证' '默认 Go 模块代理可用，保留配置。'
        }
        else {
            $selectedProxy = $null
            foreach ($candidate in @('https://goproxy.cn', 'https://goproxy.io')) {
                if (Test-GoModuleProxy $candidate) { $selectedProxy = "$candidate,direct"; break }
            }
            if (-not $selectedProxy) {
                throw '默认与备用 Go 模块代理均不可用；未修改配置，请检查网络后重跑。'
            }
            # 显式进程变量会覆盖 go env -w；同步当前进程供本次安装及子进程使用。
            Invoke-Tool $script:go @('env', '-w', "GOPROXY=$selectedProxy")
            if (Test-Path Env:GOPROXY) { $env:GOPROXY = $selectedProxy }
            Write-SetupLog '配置' "已保存 GOPROXY=$selectedProxy；可用 go env -u GOPROXY 恢复默认（若显式设置环境变量，还需移除该变量）。"
        }
    }
    else { Write-SetupLog '保留' '已有自定义 GOPROXY，使用其配置下载验证。' }
    Push-Location $ProjectRoot
    try { Invoke-Tool $script:go @('mod', 'download') }
    finally { Pop-Location }
}

try {
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw '此脚本仅支持 Windows。' }
    if (-not (Test-Path (Join-Path $ProjectRoot 'go.mod'))) { throw 'ProjectRoot 必须指向 FnCPN 仓库。' }
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $arch = $env:PROCESSOR_ARCHITEW6432
    if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
    if ($arch -eq 'ARM64') { $hostArch = 'arm64'; $goArch = 'arm64' }
    elseif ($arch -eq 'AMD64') { $hostArch = 'x64'; $goArch = 'amd64' }
    else { throw "仅支持 ARM64/x64 Windows：$arch" }
    # Known Folder API，避免当前工具进程的 LOCALAPPDATA 沙箱重定向。
    $localRoot = [Environment]::GetFolderPath('LocalApplicationData')
    $programs = Join-Path $localRoot 'Programs'
    $script:restartRequired = $false
    Write-SetupLog '开始' "平台=$hostArch；项目=$ProjectRoot；日志=$logFile"

    Invoke-Stage 'Git、Python、CMake、MSVC 和 Windows SDK' {
        $tools = @(@('git.exe', '--version', 'Git.Git'), @('python.exe', '--version', 'Python.Python.3.13'))
        if ($IncludeServerTools) { $tools += ,@('node.exe', '--version', 'OpenJS.NodeJS.LTS') }
        foreach ($entry in $tools) {
            $tool = Find-Tool $entry[0] @()
            if (-not $tool) {
                Install-WingetPackage $entry[2]
                $tool = Find-Tool $entry[0] @()
                if (-not $tool) { throw "安装后仍无法找到 $($entry[0])。" }
            }
            Invoke-Tool $tool @($entry[1])
            if ($entry[0] -eq 'node.exe') { $script:node = $tool }
            Write-SetupLog '已安装' $tool
        }
        $script:cmake = Find-Tool 'cmake.exe' @()
        if (-not $script:cmake) { Install-WingetPackage 'Kitware.CMake'; $script:cmake = Find-Tool 'cmake.exe' @() }
        if (-not $script:cmake) { throw 'CMake is required for the native Windows UI.' }
        $cmakeVersion = & $script:cmake --version
        if ($LASTEXITCODE -ne 0 -or "$cmakeVersion" -notmatch 'cmake version (\d+\.\d+\.\d+)' -or [version]$Matches[1] -lt [version]'3.27.0') { throw 'CMake >= 3.27 is required; update CMake and rerun setup.' }
        Write-SetupLog '已安装' "CMake：$script:cmake"
        $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
        $compiler = if (Test-Path -LiteralPath $vswhere) { & $vswhere -latest -version '[17.0,18.0)' -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 Microsoft.VisualStudio.Component.VC.Tools.ARM64 -property installationPath }
        if (-not $compiler) {
            Install-WingetPackage 'Microsoft.VisualStudio.2022.BuildTools' '--wait --passive --norestart --add Microsoft.VisualStudio.Workload.VCTools --add Microsoft.VisualStudio.Component.VC.Tools.ARM64 --includeRecommended'
            $compiler = & $vswhere -latest -version '[17.0,18.0)' -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 Microsoft.VisualStudio.Component.VC.Tools.ARM64 -property installationPath
            if (-not $compiler) { throw 'Add the MSVC x64/x86 and ARM64 tools to Visual Studio 2022, then rerun setup.' }
        }
        Write-SetupLog '已安装' "MSVC x64/ARM64：$compiler"
        $kits = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\bin'
        $signTools = @(Get-ChildItem -LiteralPath $kits -Filter signtool.exe -Recurse -ErrorAction SilentlyContinue)
        if ($signTools.Count -eq 0) {
            Install-WingetPackage 'Microsoft.WindowsSDK.10.0.26100'
            $signTools = @(Get-ChildItem -LiteralPath $kits -Filter signtool.exe -Recurse -ErrorAction SilentlyContinue)
            if ($signTools.Count -eq 0) { throw '安装后仍未发现 Windows SDK SignTool。' }
        }
        Write-SetupLog '已安装' "Windows SDK：$kits"
    }

    Invoke-Stage '.NET SDK 10.0.401' {
        $dotnetDirectory = Join-Path $localRoot 'Microsoft\dotnet'
        $script:dotnet = Find-Tool 'dotnet.exe' @((Join-Path $dotnetDirectory 'dotnet.exe'), 'C:\Program Files\dotnet\dotnet.exe')
        $usable = $false
        if ($script:dotnet) {
            $sdks = @(& $script:dotnet --list-sdks)
            $usable = $LASTEXITCODE -eq 0 -and @($sdks | Where-Object { $_ -match '^10\.' }).Count -gt 0
        }
        if ($usable) { Write-SetupLog '已安装' "$script:dotnet：$($sdks -join ', ')" }
        else {
            $hashes = @{
                arm64 = '8272eaab6f06ad658b1976e19d88beed287a601f968b71d5c26b75d10587cf087665c599d2e136a911002f955c97f59aa8692581bbf6b8e7af5f82604c810256'
                x64 = '24b670ad3d923bfcf47df6c3b034152398b42f6dbc388e10d783aee1cfb5e5817d399fc0ae2a12cfa822a55e61d34830ccb15c50ef6efee437ab874bb7c79430'
            }
            $name = "dotnet-sdk-10.0.401-win-$hostArch.zip"
            $archive = Get-CheckedArchive "https://builds.dotnet.microsoft.com/dotnet/Sdk/10.0.401/$name" $name $hashes[$hostArch] 'SHA512'
            Install-Archive $archive $dotnetDirectory
            $script:dotnet = Join-Path $dotnetDirectory 'dotnet.exe'
        }
        $root = Split-Path -Parent $script:dotnet
        Add-UserPath $root
        [Environment]::SetEnvironmentVariable('DOTNET_ROOT', $root, 'User')
        $env:DOTNET_ROOT = $root
        Invoke-Tool $script:dotnet @('--info')
    }

    Invoke-Stage 'Go 1.27.2' {
        $goDirectory = Join-Path $programs 'Go'
        $script:go = Find-Tool 'go.exe' @((Join-Path $goDirectory 'bin\go.exe'), 'C:\Program Files\Go\bin\go.exe')
        $usable = $false
        if ($script:go) {
            $goVersion = & $script:go version
            $usable = $LASTEXITCODE -eq 0 -and $goVersion -match 'go1\.(\d+)' -and [int]$Matches[1] -ge 27
        }
        if ($usable) { Write-SetupLog '已安装' "$goVersion：$script:go" }
        else {
            $hashes = @{ arm64 = '5c4b294f23b558619ae8a4aa94d023e3bca03f52c8398bb41b9026002b0013de'; amd64 = '1314008898bd40df77af4b014f777f08873dbdfbcd3d92308728ee03304fe04f' }
            $name = "go1.27.2.windows-$goArch.zip"
            $archive = Get-CheckedArchive "https://go.dev/dl/$name" $name $hashes[$goArch]
            Install-Archive $archive $goDirectory 'go'
            $script:go = Join-Path $goDirectory 'bin\go.exe'
        }
        Add-UserPath (Split-Path -Parent $script:go)
        Invoke-Tool $script:go @('version')
    }

    Invoke-Stage 'Go 模块代理探测与完整依赖下载' { Initialize-GoModuleProxy }

    if ($IncludeReleaseTools) { Invoke-Stage 'GitHub CLI 2.102.0' {
        $ghDirectory = Join-Path $programs 'GitHub CLI'
        $script:gh = Find-Tool 'gh.exe' @((Join-Path $ghDirectory 'bin\gh.exe'), 'C:\Program Files\GitHub CLI\gh.exe')
        if ($script:gh) { Write-SetupLog '已安装' $script:gh }
        else {
            $hashes = @{ arm64 = '5dcf12aa8525eabd0c46ec414f323ab6cf65229fc2cd46543cc705001bbaf223'; amd64 = 'ae64e556ecc240b200f7eba60d550e4bb60d78e860e69dd88c449405b86067f4' }
            $name = "gh_2.102.0_windows_$goArch.zip"
            $archive = Get-CheckedArchive "https://github.com/cli/cli/releases/download/v2.102.0/$name" $name $hashes[$goArch]
            Install-Archive $archive $ghDirectory
            $script:gh = Join-Path $ghDirectory 'bin\gh.exe'
        }
        Invoke-Tool $script:gh @('--version')
        Add-UserPath (Split-Path -Parent $script:gh)
    }

    }

    Invoke-Stage '独立 PowerShell 7.6.6' {
        $psDirectory = Join-Path $programs 'PowerShell\7'
        $script:pwsh = Find-Tool 'pwsh.exe' @((Join-Path $psDirectory 'pwsh.exe'), 'C:\Program Files\PowerShell\7\pwsh.exe')
        # 不把 Codex 内置 runtime 当成用户安装，也不将其加入持久 PATH。
        if ($script:pwsh -and $script:pwsh -match '\\\.cache\\codex-runtimes\\') { $script:pwsh = $null }
        if ($script:pwsh) { Write-SetupLog '已安装' $script:pwsh }
        else {
            $hashes = @{ arm64 = 'bbde9dda31d148415eccb5fbe1638e6400a144187b006e5b3fd8ec2f39d781be'; x64 = '02fe458be20493fbdf43f61ea20610b811ee6c738ab1676c61b9cfcd1a33c860' }
            $name = "PowerShell-7.6.6-win-$hostArch.zip"
            $archive = Get-CheckedArchive "https://github.com/PowerShell/PowerShell/releases/download/v7.6.6/$name" $name $hashes[$hostArch]
            Install-Archive $archive $psDirectory
            $script:pwsh = Join-Path $psDirectory 'pwsh.exe'
        }
        Invoke-Tool $script:pwsh @('--version')
        Add-UserPath (Split-Path -Parent $script:pwsh)
    }

    Invoke-Stage 'WiX 7.0.0（项目内）' {
        $wixDirectory = Join-Path $devRoot 'tools\wix'
        $script:wix = Join-Path $wixDirectory 'wix.exe'
        if (Test-Path -LiteralPath $script:wix) {
            $version = & $script:wix --version
            if ($LASTEXITCODE -ne 0 -or $version -notmatch '^7\.0\.0') { throw '项目 WiX 版本不符合预期。' }
            Write-SetupLog '已安装' "WiX $version"
        }
        else { Invoke-Tool $script:dotnet @('tool', 'install', 'wix', '--version', '7.0.0', '--tool-path', $wixDirectory) }
        Invoke-Tool $script:wix @('--version')
        foreach ($extensionName in @('Util', 'UI')) {
            $extension = Join-Path $wixDirectory ".wix\extensions\WixToolset.$extensionName.wixext\7.0.0\wixext7\WixToolset.$extensionName.wixext.dll"
            if (Test-Path -LiteralPath $extension -PathType Leaf) {
                Write-SetupLog '已安装' "WiX $extensionName Extension 7.0.0：$extension"
            }
            else {
                Push-Location $wixDirectory
                try { Invoke-Tool $script:wix @('-acceptEula', 'wix7', 'extension', 'add', "WixToolset.$extensionName.wixext/7.0.0") }
                finally { Pop-Location }
            }
            if (-not (Test-Path -LiteralPath $extension -PathType Leaf)) { throw "WiX $extensionName Extension 安装后仍不存在。" }
        }
    }

    Invoke-Stage 'Wintun 0.14.1（ARM64 / AMD64）' {
        $wintunDirectory = Join-Path $devRoot 'dependencies\wintun\0.14.1'
        if (-not (Test-Path -LiteralPath $wintunDirectory)) {
            $archive = Get-CheckedArchive 'https://www.wintun.net/builds/wintun-0.14.1.zip' 'wintun-0.14.1.zip' '07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51'
            Install-Archive $archive $wintunDirectory 'wintun'
        }
        foreach ($architecture in @('arm64', 'amd64')) {
            $dll = Join-Path $wintunDirectory "bin\$architecture\wintun.dll"
            if (-not (Test-Path -LiteralPath $dll)) { throw "缺少 $dll" }
            $signature = Get-AuthenticodeSignature -LiteralPath $dll
            if ($signature.Status -ne 'Valid') { throw "Wintun 签名无效：$($signature.Status) / $dll" }
            Write-SetupLog '已安装' "已验证官方签名：$dll"
        }
    }

    Invoke-Stage 'WebView2 Runtime' {
        $clients = @(Get-ItemProperty 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\*', 'HKLM:\SOFTWARE\Microsoft\EdgeUpdate\Clients\*', 'HKCU:\Software\Microsoft\EdgeUpdate\Clients\*' -ErrorAction SilentlyContinue | Where-Object { $_.PSObject.Properties['name'] -and $_.name -eq 'Microsoft Edge WebView2 Runtime' })
        if ($clients.Count -eq 0) {
            Install-WingetPackage 'Microsoft.EdgeWebView2Runtime'
            $clients = @(Get-ItemProperty 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\*', 'HKLM:\SOFTWARE\Microsoft\EdgeUpdate\Clients\*', 'HKCU:\Software\Microsoft\EdgeUpdate\Clients\*' -ErrorAction SilentlyContinue | Where-Object { $_.PSObject.Properties['name'] -and $_.name -eq 'Microsoft Edge WebView2 Runtime' })
            if ($clients.Count -eq 0) { throw '安装后仍未发现 WebView2 Runtime。' }
        }
        Write-SetupLog '已安装' "WebView2 $($clients[0].pv)"
    }

    Invoke-Stage 'WebView2 Evergreen Bootstrapper（Windows MSI payload）' {
        $bootstrapperDirectory = Join-Path $devRoot 'dependencies\webview2'
        $bootstrapper = Join-Path $bootstrapperDirectory 'MicrosoftEdgeWebView2Setup.exe'
        $cachedBootstrapper = Join-Path $downloads 'MicrosoftEdgeWebView2Setup.exe'
        $validateBootstrapper = {
            param([string]$Path)
            if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return $false }
            $signature = Get-AuthenticodeSignature -LiteralPath $Path
            return $signature.Status -eq 'Valid' -and $signature.SignerCertificate.Subject -like '*Microsoft Corporation*'
        }
        if (Test-Path -LiteralPath $bootstrapper) {
            if (-not (& $validateBootstrapper $bootstrapper)) { throw "已存在的 WebView2 Bootstrapper 签名无效，拒绝覆盖：$bootstrapper" }
            Write-SetupLog '已安装' "已验证微软签名：$bootstrapper"
        }
        else {
            if (Test-Path -LiteralPath $cachedBootstrapper) {
                if (-not (& $validateBootstrapper $cachedBootstrapper)) { throw "缓存的 WebView2 Bootstrapper 签名无效，拒绝覆盖：$cachedBootstrapper" }
                Write-SetupLog '跳过' "复用已验证签名的下载缓存：$cachedBootstrapper"
            }
            else {
                $partial = "$cachedBootstrapper.partial"
                Invoke-WebRequest -UseBasicParsing -Uri 'https://go.microsoft.com/fwlink/p/?LinkId=2124703' -OutFile $partial
                if (-not (& $validateBootstrapper $partial)) { throw "下载的 WebView2 Bootstrapper 签名无效，保留现场：$partial" }
                Move-Item -LiteralPath $partial -Destination $cachedBootstrapper
            }
            $null = New-Item -ItemType Directory -Force -Path $bootstrapperDirectory
            Copy-Item -LiteralPath $cachedBootstrapper -Destination $bootstrapper
            if (-not (& $validateBootstrapper $bootstrapper)) { throw "已安装的 WebView2 Bootstrapper 签名校验失败：$bootstrapper" }
            Write-SetupLog '安装' "已验证微软签名并暂存：$bootstrapper"
        }
    }

    Invoke-Stage 'WebView2 原生 SDK 还原' {
        $nativeSource = Join-Path $ProjectRoot 'apps/windows'
        $nativePackages = Join-Path $ProjectRoot '.dev/native-ui-nuget'
        $nativeSdkRoot = Join-Path $nativePackages 'microsoft.web.webview2/1.0.4258.31'
        Invoke-Tool $script:dotnet @('restore', (Join-Path $nativeSource 'WebView2SDK.csproj'), "-p:RestorePackagesPath=$nativePackages")
        if (-not (Test-Path -LiteralPath (Join-Path $nativeSdkRoot 'build/native/include/WebView2.h'))) { throw 'WebView2 native SDK restore is incomplete.' }
    }

    if (-not $SkipSmoke) {
        Invoke-Stage 'ARM64/x64 原生 UI、Go 与 MSI 构建验证' {
            $smoke = Join-Path $devRoot 'smoke'
            $output = Join-Path $ProjectRoot 'dist\smoke'
            $null = New-Item -ItemType Directory -Force -Path $smoke, $output
            $nativeSource = Join-Path $ProjectRoot 'apps/windows'
            $nativePackages = Join-Path $ProjectRoot '.dev/native-ui-nuget'
            $nativeSdkRoot = Join-Path $nativePackages 'microsoft.web.webview2/1.0.4258.31'
            $goSource = Join-Path $smoke 'main.go'
            [IO.File]::WriteAllText($goSource, "package main`nfunc main() { println(`"FnCPN build smoke`") }`n", $utf8)
            $wixSource = Join-Path $smoke 'Package.wxs'
            [IO.File]::WriteAllText($wixSource, '<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs"><Package Name="FnCPN Build Smoke" Manufacturer="FnCPN" Version="1.0.0" UpgradeCode="9A82BC51-B608-4CC6-B383-388F20D0A573" Scope="perUser"><MediaTemplate EmbedCab="yes"/><StandardDirectory Id="LocalAppDataFolder"><Directory Id="INSTALLFOLDER" Name="FnCPNBuildSmoke"><Component Id="Smoke" Guid="*"><File Source="$(var.Payload)"/></Component></Directory></StandardDirectory><Feature Id="Main"><ComponentRef Id="Smoke"/></Feature></Package></Wix>', $utf8)
            $oldEnvironment = @{}
            foreach ($key in @('GOOS', 'GOARCH', 'CGO_ENABLED')) { $oldEnvironment[$key] = [Environment]::GetEnvironmentVariable($key, 'Process') }
            try {
                $env:GOOS = 'windows'; $env:CGO_ENABLED = '0'
                foreach ($target in @('arm64', 'x64')) {
                    $cmakeArch = if ($target -eq 'arm64') { 'ARM64' } else { 'x64' }
                    $nativeBuild = Join-Path $output "windows-ui-$target"
                    Invoke-Tool $script:cmake @('--fresh', '-S', $nativeSource, '-B', $nativeBuild, '-G', 'Visual Studio 17 2022', '-A', $cmakeArch, "-DWEBVIEW2_SDK_ROOT=$nativeSdkRoot")
                    Invoke-Tool $script:cmake @('--build', $nativeBuild, '--config', 'Release')
                    if ($target -eq $hostArch -or $hostArch -eq 'arm64') {
                        Invoke-Tool (Join-Path (Split-Path -Parent $script:cmake) 'ctest.exe') @('--test-dir', $nativeBuild, '-C', 'Release', '--output-on-failure')
                    } else { Write-SetupLog '跳过' 'x64 主机不能运行 ARM64 回归；已交叉编译，需 ARM64 主机执行。' }
                    Copy-Item -LiteralPath (Join-Path $nativeBuild 'Release/FnCPN.UI.exe') -Destination (Join-Path $output "FnCPN-$target.exe")
                    $env:GOARCH = $target
                    if ($target -eq 'x64') { $env:GOARCH = 'amd64' }
                    $exe = Join-Path $output "go-$target.exe"
                    Invoke-Tool $script:go @('build', '-o', $exe, $goSource)
                    Write-SetupLog '协议' 'WiX 构建使用 -acceptEula wix7，接受官方 OSMF EULA；不购买服务。'
                    Invoke-Tool $script:wix @('build', '-acceptEula', 'wix7', $wixSource, '-arch', $target, '-d', "Payload=$exe", '-o', (Join-Path $output "smoke-$target.msi"), '-intermediatefolder', (Join-Path $output "wix-$target"))
                }
                Invoke-Tool (Join-Path $output "go-$hostArch.exe") @()
            }
            finally { foreach ($key in $oldEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key, $oldEnvironment[$key], 'Process') } }
        }
    }
    Invoke-Stage '新进程环境验证' {
        $savedPath = $env:Path
        try {
            $env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')
            $verify = 'dotnet --version; if ($LASTEXITCODE) { exit 1 }; go version; if ($LASTEXITCODE) { exit 1 }; git --version; if ($LASTEXITCODE) { exit 1 }; python --version; if ($LASTEXITCODE) { exit 1 }; cmake --version; if ($LASTEXITCODE) { exit 1 }'
            if ($IncludeReleaseTools) { $verify += '; gh --version; if ($LASTEXITCODE) { exit 1 }' }
            # fnm/nvm may expose Node only in the current shell. Validate its selected
            # executable without persisting a temporary version-manager path.
            if ($IncludeServerTools) { $verify += "; & '" + $script:node.Replace("'", "''") + "' --version; if (`$LASTEXITCODE) { exit 1 }" }
            Invoke-Tool $script:pwsh @('-NoProfile', '-NonInteractive', '-Command', $verify)
        }
        finally { $env:Path = $savedPath }
    }
    Write-SetupLog '成功' '准备完成；已有终端/IDE 请重新打开以刷新 PATH。不修改执行策略，不安装应用服务或创建网卡。'
    if ($script:restartRequired) { Write-SetupLog '提示' '安装器报告需要系统重启；工具已在当前执行验证，本脚本不自动重启。' }
    else { Write-SetupLog '提示' '本次安装无需系统重启。' }
}
catch {
    Write-SetupLog '错误' $_.Exception.Message
    Write-SetupLog '错误' "执行已停止，修复后可以重跑。日志：$logFile"
    exit 1
}
