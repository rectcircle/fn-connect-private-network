# 开发、构建与发布

本页统一维护环境准备、各端开发与构建、检查、打包、发布签名及官网部署。命令默认从仓库根目录运行，另有注明除外。
平台支持与选择依据见 [平台决策](platform-support.md)，实现边界见 [架构](architecture.md)。正式发布须遵守 [版本契约](versioning-and-compatibility.md)，实际验证结果和未验证项只在 [验证记录](validation.md) 维护。

按任务查阅：[macOS / fnOS](#macos-与-fnos-构建安装包) · [Windows](#windows-开发与构建) · [Linux](#linux-构建与部署) · [NixOS](#nix-flake-与-nixos) · [Android](#android-开发与构建) · [测试](#测试) · [资源同步](#中英文资源) · [发布准备](#产品版本与正式发布准备) · [RPM 签名](#rpm-签名与密钥维护) · [发布与镜像](#github-发布与-homebrew) · [官网](#官网开发与部署)

## 环境准备

共享核心需要 Go 1.27，资源同步使用 Python 3；前端开发使用 Node/npm。macOS 构建需要 Xcode/Swift/AppKit 工具链；Linux UI 需要对应基线中的 GTK/WebKit 开发依赖；Windows 和 Android 的固定工具链见各自章节。

## 本地运行

```bash
go run ./cmd/fncpn version
```

需要在本机长期使用 CLI 时安装到 `$GOBIN`：

```bash
go install ./cmd/fncpn
```

<a id="构建安装包"></a>

## macOS 与 fnOS 构建安装包

```bash
./scripts/build-all.sh
./scripts/build-macos-pkg.sh                 # 默认 Swift/AppKit
./scripts/build-macos-pkg.sh --ui wails      # Wails v2
./scripts/build-fpk.sh
```

原生 macOS 与 fnOS 产物写入 `dist/`；Wails macOS PKG 默认写入 `dist/wails-v2/`，
可用 `DIST_DIR` 指定其他输出目录。打包中间文件使用系统临时目录。
macOS 两种 UI 共用应用标识、固定安装路径和安装维护流程，默认发布仍使用原生 UI。
只构建 App 可使用 `build-native-macos.sh` 或 `build-wails-macos.sh`，详见
[Wails 构建](#macos-wails-ui)。

FPK 构建要求通过 `FNPACK` 或 `PATH` 显式提供 `fnpack`，不依赖本地 PoC 文件。
首次初始化默认使用 `10.253.203.0/24` 和 `54789/UDP`；冲突时
服务端保持管理页面可用，由管理员修改后重试。

- `fncpn-<version>-macos-arm64-unsigned.pkg`：Apple Silicon macOS 客户端。
- `fncpn-<version>-macos-x64-unsigned.pkg`：Intel macOS 客户端。
- `fncpn-<version>-fnos-x64.fpk`：x86_64 fnOS 服务端。
- `fncpn-<version>-fnos-arm64.fpk`：ARM64 fnOS 服务端。

### fnOS 服务监管与旧版升级

从 1.1.4 起，FPK 生命周期脚本生成 `fncpn-server.service` 和
`fncpn-server-privileged.service`。前者以 `fncpn:fncpn` 运行且不授予 capabilities；
后者以 `root:fncpn` 运行，仅保留 `CAP_NET_ADMIN`，负责内核 WireGuard、转发和 nftables。
两者启用 `NoNewPrivileges`、`ProtectSystem=strict`、`ProtectHome`、`PrivateTmp`，
仅开放各自状态、日志和运行目录的写入。安装目录与二进制仍归 root，业务用户不可写。
这仍是含 root 网络组件的应用，不声明为完全非 root。

应用中心负责启动意图：不 enable systemd 单元；显式停止先停止两个受监管服务，
再检查旧 PID 进程并执行恢复 journal 清理。正常卸载停止并移除单元，不删除身份和配置。
服务失败后由 systemd 重启；业务进程继续通过既有周期检查重新提交完整网络计划，
不因网络服务短暂重启而重建身份。

旧正式版直接升级沿用 `control`、`privileged` 数据目录、私钥及 IPC 路径。
启动前停止旧 PID 管理的进程；即使安装器已经替换二进制，也按原路径、UID 和角色
识别 `/proc/<pid>/exe` 的 ` (deleted)` 进程，确认退出后才启动新服务。
PID 管理的识别与清理代码作为正式版本升级路径保留。远程协议、本地 IPC 版本与数据 schema 不变。

`python3 scripts/fnos_systemd_test.py` 检查生命周期顺序、故障退出与历史 tag 的目录契约。
`scripts/test-fnos-systemd-linux.sh` 只允许在隔离的 GitHub Actions Linux VM 以 root 运行：
它在独立网络 namespace 中运行 v1.1.3 原始二进制和启动脚本，注册设备，再覆盖升级，
校验配置/设备/密钥哈希、实际 UID/capabilities、IPC 权限、网络服务崩溃恢复及重复停止。
该检查接入 Checks 与草稿发布前置门禁；不在用户 NAS 上运行。
CI 检查不替代 fnOS x86/ARM 的真实 FPK 安装、升级、直连和中继验收。

### 图标生成

macOS 打包自动生成图标；需要手动更新时运行 `swift scripts/generate-icons.swift packaging/assets`，生成资源随源码提交。其他平台使用已提交的生成资源，构建时不依赖 Swift。

### macOS Wails UI

依赖 Go（与根模块一致）、Node/npm、Xcode Command Line Tools、macOS 13+。

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
# 从仓库根目录构建当前架构 App
scripts/build-wails-macos.sh
# 统一安装包入口：默认 native，显式选择 Wails
scripts/build-macos-pkg.sh --ui wails
# 等价的便捷入口
scripts/build-wails-macos.sh --pkg
# Intel 构建
ARCH=x64 scripts/build-wails-macos.sh --pkg
```

App：`dist/macos-ui/wails/<架构>/FnCPN.app`。
原生 App 可通过 `scripts/build-native-macos.sh` 构建，输出到
`dist/macos-ui/native/<架构>/FnCPN.app`。两个构建器均支持 `--output /绝对路径/FnCPN.app`，
输出必须不存在；默认开发输出允许重复构建。`ui/build/bin/` 是 Wails CLI 中间产物。
安装包：`dist/wails-v2/fncpn-<产品版本>-macos-<架构>-unsigned.pkg`。
产物使用本地 ad-hoc 签名，尚未公证。
两种 UI 共用应用标识 `cn.rectcircle.fncpn`、URL scheme 和产品版本；PKG 都安装至
`/Applications/FnCPN.app`，用于相互替换，不能作为两个独立产品并行安装。
`build-macos-pkg.sh` 不带参数等价于 `--ui native`，默认输出目录仍为 `dist/`。
Wails 默认 PKG 目录为 `dist/wails-v2/`；显式设置 `DIST_DIR` 可覆盖两者的输出目录。

直接运行 App 需要当前用户可访问的特权服务已安装；它会启动同源码构建并随 App
携带的普通用户 daemon。首次安装应使用完整 PKG。PKG 使用现有原生安装器，安装到
`/Applications/FnCPN.app` 并配套更新 Core 与特权组件，使用相同用户数据及卸载入口。
不要同时运行原生和 Wails 两个 UI：两者共享 socket、身份及连接意图。
开发 App 发现已有 daemon 时只复用它，退出不终止别的 UI 启动的服务；若版本不匹配，
现有 IPC client 会明确拒绝通信，不放宽产品版本或 IPC 校验。

只开发前端或调试绑定时可在 `ui/` 运行 `wails dev`，先通过完整 App/安装器准备 daemon。

## Windows 开发与构建

### 开发环境

在仓库根目录执行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/setup-windows.ps1
```

setup 检查并准备 Git、Python、Go >= 1.27、PowerShell 7、.NET 10 SDK、CMake >= 3.27、MSVC 2022 x64/ARM64 工具链、Windows SDK、WiX 7.0.0 UI/Util 扩展、Wintun 0.14.1，以及 WebView2 原生 SDK、共享 Runtime 和安装引导器。
.NET SDK 仅用于 WiX/NuGet，客户端不依赖 .NET 桌面运行时。Node 和 GitHub CLI 分别通过 `-IncludeServerTools`、`-IncludeReleaseTools` 选择安装。

默认 setup 编译双架构 UI、Go 和测试 MSI，并运行本机可执行架构的 CTest；`-SkipSmoke` 跳过编译，但仍下载 Go 依赖和还原 WebView2 SDK。x64 主机不运行 ARM64 程序，ARM64 主机上的 x64 执行属于仿真。
setup 不安装 FnCPN、不创建网卡、不登录 GitHub、不自动重启。系统前置组件安装可能请求 UAC；`ExecutionPolicy Bypass` 仅作用于本次进程。

| 内容 | 位置 |
|---|---|
| 新安装的 .NET SDK | `%LOCALAPPDATA%\Microsoft\dotnet` |
| 新安装的 Go / PowerShell / GitHub CLI | `%LOCALAPPDATA%\Programs` |
| WiX | `.dev/tools/wix` |
| Wintun 与许可证 | `.dev/dependencies/wintun/0.14.1` |
| WebView2 原生 NuGet SDK | `.dev/native-ui-nuget` |
| WebView2 安装引导器 | `.dev/dependencies/webview2` |
| 下载缓存、setup 日志与临时源码 | `.dev/downloads`、`.dev/logs`、`.dev/smoke` |
| setup 编译验证产物 | `dist/smoke` |

已安装的可用工具优先复用。下载校验哈希或官方签名；异常目录不会直接覆盖。
通用工具目录加入用户 PATH，项目 `.dev` 不加入 PATH；设置 `DOTNET_ROOT`，不设置 `GOROOT` 或改变 `GOPATH`。已有终端/IDE 需重新打开以刷新持久环境。已有 fnm/nvm 的 Node 配置保留，不将临时目录写入用户 PATH。

默认 Go 代理不可达时，setup 先验证 `goproxy.cn`，再验证 `goproxy.io`，通过 `go env -w` 保存可用代理；已有自定义 `GOPROXY` 保留。恢复默认使用 `go env -u GOPROXY`，显式环境变量需另行调整。代理不可达时停止，不关闭 checksum database 校验。

### 构建和检查

日常使用三个入口，内部辅助脚本位于 `scripts/windows/`，由这些入口统一调用。

```powershell
pwsh -NoProfile -File scripts/build-windows.ps1
pwsh -NoProfile -File scripts/check-windows.ps1
```

build 默认按 ARM64、x64 构建，可指定 `-Architecture arm64` 或 `x64`，并通过 `-OutputRoot dist/<目录>` 选择输出位置。
默认产物位于 `dist/windows-native/<架构>/`，包含未签名 MSI、解包 payload 和 `build-manifest.json`。清单记录产品版本、commit、dirty 状态、源码指纹、架构、构建时间及 MSI SHA256；构建拒绝覆盖输出目录中的未登记文件。

check 验证版本/本地化生成资源、setup 回归、Go test/vet、安装维护协议历史/未来版本互通、原生 UI 与 MSI。它使用已有候选，不自动重建或安装；只检查源码用 `-SkipPackage`，单架构用 `-Architecture`，自定义产物目录用 `-PackageRoot`。

原生 UI 源码位于 `platform/windows/`，独立编译示例：

```powershell
cmake -S platform/windows -B .dev/windows-ui-arm64 -G "Visual Studio 17 2022" -A ARM64 -DWEBVIEW2_SDK_ROOT="$PWD/.dev/native-ui-nuget/microsoft.web.webview2/1.0.4258.31"
cmake --build .dev/windows-ui-arm64 --config Release
ctest --test-dir .dev/windows-ui-arm64 -C Release --output-on-failure
```

首次执行先运行 setup。构建 x64 时替换构建目录并使用 `-A x64`。UI 的中英文资源位于 `platform/windows/messages.json`，执行 `python scripts/sync-native-localizations.py` 更新 `Localization.h`。
维护协议测试需要完整 Git 历史（含 v1.0.7–v1.0.10 tag），
在 `.dev/maintenance-compatibility/` 构建隔离进程并保存 commit/哈希清单；不会安装软件或操作用户 daemon。
测试还把业务 IPC 改为 99、产品版本改为 9.0.0，验证新旧维护客户端双向退出和拒绝行为。

自动测试使用隔离窗口和管道，不操作已安装 daemon 或用户连接，覆盖 IPC 分片、字段校验、导航边界、授权取消、过期结果、退出资源回收、DPI/布局和 MSI 元数据。

### 安装验证与日志

MSI 成套更新 UI、Go 核心和驱动依赖；缺少共享 WebView2 Runtime 时需要联网安装。正常升级与卸载保留身份、配置、DPAPI 凭据和日志；验证安装、升级、卸载时须检查数据保留及网络清理。

显式清理发起卸载用户的数据（架构按实际包替换）：

```powershell
$version = (Get-Content internal/version/VERSION -Raw).Trim()
msiexec.exe /x "./fncpn-$version-windows-arm64-unsigned.msi" PURGE_CURRENT_USER_DATA=1
```

仅清理该用户的 `%LOCALAPPDATA%\FnCPN`，不清理其他用户、Service recovery journal 或共享 Runtime。
MSI 日志在安装用户 `%TEMP%\MSI*.LOG`，实际路径由 `MsiLogFileLocation` 记录；系统维护日志在 `%ProgramData%\FnCPN\Service\Logs\installer-*.log`。排查耗时可比较 Action 起止时间，进度条满格不代表事务完成。

Windows CI 使用 `windows-2022` 与 `Visual Studio 17 2022`，调整工具链时同步 setup 与 CI。EXE/MSI 当前未签名；Wintun 和 WebView2 引导器保留官方签名。WiX 7 [OSMF 条款](https://docs.firegiant.com/wix/osmf/) 由构建命令 `-acceptEula wix7` 接受。公开发布统一按下文流程执行。

## Linux 构建与部署

平台范围与选择依据见 [平台支持](platform-support.md#linux)，安装脚本命令见 [README](../README.md#linux自动安装)。Core 可交叉构建；UI 必须在 [targets.json](../packaging/linux/targets.json) 指定的对应架构基线环境构建。

```sh
TARGET=deb ARCH=arm64 scripts/build-linux-core.sh
# 在对应基线环境内构建 UI；TARGET 也可为 rpm 或 archlinux
TARGET=deb ARCH=arm64 scripts/build-linux-ui.sh
python3 scripts/check-linux-packages.py dist/linux/deb/arm64
```

输出在 `dist/linux/<format>/<arch>/`，包含 Core/UI 包、构建清单和校验和。先校验可信来源、SHA256 和包签名，再成套安装同版本组件：

```sh
sudo apt install ./fncpn-core-*.deb ./fncpn-ui-*.deb
# Fedora；openSUSE 改用 zypper install，RPM 公钥验证见下文
sudo dnf install ./fncpn-core-*.rpm ./fncpn-ui-*.rpm
# Arch 先完整更新系统，再安装双包
sudo pacman -Syu
sudo pacman -U ./fncpn-core-*.pkg.tar.zst ./fncpn-ui-*.pkg.tar.zst
# CLI 首次启用；UI 可通过图形管理员授权完成
fncpn-linux-setup
```

无头环境只安装 Core。安装脚本不自动创建 NAS 连接或启用 UI 登录自启；Core/UI 需要成套升级，不自动降级。安装 resolved 依赖后须核对宿主原有 DHCP DNS 与 stub 集成。

| 数据 | 默认路径 |
| --- | --- |
| 用户配置 | `${XDG_CONFIG_HOME:-~/.config}/fncpn/config.json` |
| 用户状态与日志 | `${XDG_STATE_HOME:-~/.local/state}/fncpn/` |
| 用户 IPC | `${XDG_RUNTIME_DIR:-/run/user/<uid>}/fncpn/client.sock` |
| 特权 IPC | `/run/fncpn/<uid>/privileged.sock` |
| 特权恢复日志 | `/var/lib/fncpn/client/<uid>/` |
| 特权运行日志 | `/var/log/fncpn/client-privileged-<uid>.log` |

XDG 覆盖值须为绝对路径。卸载保留身份、配置及连接意图，正常停止清理自有网络资源。

## Nix Flake 与 NixOS

Linux 目标为 `x86_64-linux`、`aarch64-linux`。Flake 提供 `core`、`ui`、`frontend` 包、
`core`/`ui` app、overlay、开发 shell 和 `nixosModules.default`。
默认包为无头 Core；UI 使用 Wails v2、原生 HTML/CSS/JavaScript。
依赖由 `flake.lock` 锁定；Go vendor hash 和 npm lockfile 固定依赖内容。
不额外发布 Nix Release 二进制包。首次构建通常在本地编译项目；有受信缓存时可复用缓存，
目前不配置项目自己的二进制缓存。

```sh
# 仓库内（新文件须纳入 Git，Git Flake 不包含未跟踪文件）
nix build .#core
nix build .#ui
nix develop
nix flake check
```

单独 `nix run .#ui` 或安装包不会向宿主注册 root 服务、用户服务或 polkit 策略。
NixOS 使用下方模块；其他 Linux 使用其原生安装包进行宿主集成。
NixOS 模块不使用 deb/RPM 的命令式安装维护脚本，也不写 `/usr/bin`。

### NixOS 配置

把此仓库作为已提交、可访问的 Flake input（选择明确 tag 或 commit），然后：

```nix
{
  imports = [ inputs.fncpn.nixosModules.default ];
  users.users.alice = { isNormalUser = true; uid = 1000; };
  services.fncpn = {
    enable = true;
    users = [ "alice" ];
    ui.enable = true; # 无头机器设为 false
    dns.enable = true;
  };
}
```

`users` 必须明确列出已有普通用户及稳定 UID（至少 1000）；模块只配置这些 UID 的特权实例。
模块安装匹配版本的 Core/UI、polkit 策略和 systemd 用户服务，系统启动时启用各用户独立的
root 实例，登录时启动用户 daemon。UI 自启仍由用户设置；注销后保留用户服务可自行配置 linger。
UI 授权入口只启动已声明实例，不再执行 `systemctl enable`。

默认启用 systemd-resolved。应用模块前应检查宿主 DNS 配置；分域 DNS 仍要求
`/etc/resolv.conf` 使用 `127.0.0.53` stub。`dns.enable = false` 不更改宿主 resolver，
未满足 stub 条件时明确显示 DNS 降级。模块不修改物理网络、转发或防火墙。

数据路径与普通 Linux 包相同，详见 [Linux 构建与部署](#linux-构建与部署)。删除模块配置不会删除
身份、配置和连接意图。NixOS generation 回滚程序并不回滚持久化数据，跨 schema 降级仍须按
[版本契约](versioning-and-compatibility.md) 恢复配套备份，不能假定旧程序读取新数据。
自定义 `package`/`ui.package` 时需使用同版本、同发布 commit 的成套组件。

### 维护和验证

`check-nix.yml` 在每次普通 push/PR 中执行 Flake 求值和两个原生 Linux 架构的 Core/UI
`goModules` 固定哈希检查，随后强制重建依赖输出，防止已缓存的固定输出掩盖本地替换模块
源码变化。草稿构建再执行完整 Core/UI、模块检查和虚拟显示中的真实前端启动。
Release workflow 的 `nix-dependencies` 是所有平台构建的前置门槛，未通过时不启动各平台打包。
这些检查不代替 NixOS 实机的 polkit、systemd、resolved、桌面和网络验收。
Nix 开发依赖只进入 CI，不在 Debian 验证虚拟机安装 Nix。

更新 Go 依赖、根模块源码或嵌入的版本文件都可能改变 UI vendor 哈希：UI 使用
`replace => ..`，vendoring 会复制项目代码。不要只在 go.mod/go.sum 改动时更新。
在有 Linux Nix builder 的环境（或配置了对应远程 builder）执行：

```sh
# 从 flake.lock 锁定的同一个 goModules derivation 计算并更新，再校验
scripts/update-nix-go-hashes.sh --system x86_64-linux
# 提交/创建发布 tag 前检查，不写文件
scripts/update-nix-go-hashes.sh --check --system x86_64-linux
```

更新脚本不再使用宿主 Go 或另一套手写的源复制规则；临时替换目标 derivation 的期望哈希，
只接受该依赖产物明确返回的实际哈希，拒绝网络等无关错误。两个哈希一起更新，真实构建校验
失败会恢复原文件。`--check` 永不修改哈希或 flake.lock，失败必须修正并提交后才能通过 CI。
CI 自身不自动接受新哈希或提交修改。更新 flake.lock、Go、Node 或图形依赖后仍须完整双架构验收。

## HarmonyOS 开发、构建与签名

工程位于 `apps/harmonyos/`，原生 ArkUI + N-API + 共享 Go 客户端。支持手机（含折叠屏）、
平板、PC/2in1，最低 API20，仅构建 ARM64；智慧屏暂不支持。普通产品使用系统 VPN，
模拟器产品使用真实授权和应用内隧道。二者不能混作系统 VPN 验收。

### macOS 命令行环境

无需 DevEco Studio。通过[华为下载中心](https://developer.huawei.com/consumer/cn/download/)登录下载
macOS ARM64 Command Line Tools ZIP；下载地址需要鉴权，脚本不代替登录、不保存 token/Cookie。
版本与官方 SHA256、社区 Go commit 固定在 `apps/harmonyos/toolchains.json`。

```sh
bash scripts/setup-harmonyos-macos.sh --cli-archive ~/Downloads/commandline-tools-mac-arm64-26.0.0.851.zip
# 已安装时复用 CLI 与固定社区 Go；不再需要 --with-go
bash scripts/setup-harmonyos-macos.sh
python3 scripts/harmonyos/setup_macos.py --check
bash scripts/check-harmonyos.sh
```

依赖安装到通用目录 `~/.local/opt/harmonyos/command-line-tools/<version>` 和
`~/.local/opt/harmonyos/go/<version>-<commit>`，缓存为 `~/.cache/harmonyos/downloads/`。
其他项目可复用；不替换主机 Go/Node，不修改 shell 启动文件或全局 GOOS/CC/GOROOT。
脚本对 ZIP 执行摘要及路径/符号链接安全校验，以临时目录完成安装后提交；不同版本并存，
已有安装登记不匹配时拒绝覆盖。安装日志在 `.dev/logs/`，环境报告在 `.dev/harmonyos/environment.json`。
工具准备成功不等于设备验收。

### 应用构建与发布签名

公开分发只提供 release 构建模式的**未签名**系统 VPN 包：

```sh
bash scripts/build-harmonyos.sh --release
bash scripts/package-harmonyos.sh --release
```

输出 `dist/harmonyos/fncpn-<版本>-harmonyos-arm64-release-unsigned.hap` 和
`build-manifest-release.json`，清单记录版本、源码 commit/dirty、CLI/Go、构建模式和未签名状态。
打包校验版本、设备声明、系统 VPN Extension、ARM64 原生库、许可证和构建摘要。
release 构建模式不等于发布证书签名；未签名 HAP 不能直接安装。
用户可使用自己的开发者账号，通过 HapSign 的 Test Profile 链路尝试自行签名，操作与验证边界见根 README。
应用功能已通过云真机调试验证；维护者暂无华为实体设备，跨账号同包名自行签名安装尚未验证。

维护者本地真机验证仍可选择签名，凭据保存在仓库外：

```sh
export HARMONYOS_KEYSTORE="$HOME/.harmonyos/signing/release.p12"
export HARMONYOS_KEY_ALIAS=release
export HARMONYOS_APP_CERT="$HOME/.harmonyos/signing/release.cer"
export HARMONYOS_PROFILE="$HOME/.harmonyos/signing/profiles/fncpn-release.p7b"
bash scripts/package-harmonyos.sh --release --sign
```

该命令交互读取密码并执行官方 `verify-app`；签名结果只用于本地验证，不进入公开或镜像清单。
不要提交私钥、密码、证书或 Profile。密钥库和 CSR 可使用 `keytool` 生成；CSR 包含
申请者 DN（CN/O/OU/L/ST/C），私钥保留在密钥库中。签名材料与安装设备、用途须匹配。
HAP 是模块安装包；APP 可包含多个模块，不能直接互换扩展名。

### HarmonyOS CI、官网与夸克分发

官网提供 HarmonyOS 中英下载页签；显式 HarmonyOS/OpenHarmony UA 优先于 Android 兼容标记，
未暴露系统标记的浏览器仍可手动选择。HAP 安装和覆盖升级说明维护在根 README。
当前未上架 AppGallery，下载入口指向对应公开 Release，不把草稿描述为公众可安装版本。

`build-harmonyos.yml` 支持手动触发或由统一 Release 调用，需要带 `harmonyos-26` 标签的
macOS ARM64 自托管 runner。开发者先从官方页面授权下载 CLI，再用环境脚本导入固定版本；
工作流不保存下载 token、不重新分发 SDK，也不需要证书、Profile、签名密码或钥匙串权限。

统一 Release 等待鸿蒙 job，收集同一 commit 的未签名 HAP 与构建清单。公开和夸克镜像均要求
1.1.6+ 的未签名普通系统 VPN release 产品，拒绝 signed、dirty、模拟器、debug、版本/构建号或
SHA256 不一致的候选，并检查 HAP 签名块，防止把签名产物误标成未签名。
HAP 内包含第三方声明与固定社区 Go LICENSE。夸克镜像沿用公开 Release 原始字节与哈希，
不签名、不改包，不镜像私钥、证书、Profile、签名包或模拟器包。

当前工作流接入不等于 runner 已配置或线上发布已通过。运行前先完成 Nix 等既有门槛，
再按干净 commit 构建草稿。公开和夸克上传仍由既有人工发布入口触发。

### 模拟器与独立能力探针

首次安装镜像前查看并明确接受厂商许可；脚本不默认接受。

```sh
emulator="$HOME/.local/opt/harmonyos/command-line-tools/26.0.0.851/emulator/Emulator"
"$emulator" -license
"$emulator" -imageList -deviceType phone
"$emulator" -install -deviceType phone -osVersion 'HarmonyOS 7.0.0(26.0.0)'
"$emulator" -create HarmonyOS_Development -deviceType phone -osVersion 'HarmonyOS 7.0.0(26.0.0)'
"$emulator" -start HarmonyOS_Development
bash scripts/build-harmonyos.sh --application-tunnel
bash scripts/package-harmonyos.sh --application-tunnel
```

安装 `dist/harmonyos/fncpn-<版本>-harmonyos-arm64-application-tunnel-unsigned.hap`；使用官方
`hdc list targets` 确认设备，再通过 `hdc -t <target> install <hap>` 安装。允许未签名安装的模拟器
不代表真机同样允许。应用标题显示“应用内调试”，真实登录、挑战、中继与 WireGuard 均复用
共享 Manager，只将显式使用 netstack 的本 App 请求送入隧道，不接管浏览器或其他应用。
该产品使用私有目录 `application-tunnel/`，与普通模式隔离配置和日志；两种产品应用 ID 相同，
覆盖安装不主动清空用户数据。返回系统 VPN 模式须重新安装默认产品。

独立探针源码在 `apps/harmonyos/diagnostics/`，工程生成到 `.dev/harmonyos/hap-smoke/`，
包名 `cn.rectcircle.fncpn.harmonysmoke`，不混入正式应用。

```sh
bash scripts/build-harmonyos-smoke.sh
python3 scripts/harmonyos/run_smoke.py --device <hdc-target> --https https://example.com --wss wss://echo.websocket.org
# 设备先安装诊断 HAP；具备系统 VPN 授权组件时才能执行
python3 scripts/harmonyos/run_vpn_probe.py --device <hdc-target> --cycles 2
```

runtime 探针测试异步 N-API、外部线程、goroutine/定时器/GC 与 HTTPS/WSS，活动 Go runtime
不可 dlclose。VPN 探针仅接管 `198.18.0.1/32`，本地地址 `198.18.0.2/32`、MTU1400，
通过 UDP53535 回显核对 TUN 与 protect；测试网络不得使用该诊断地址。
系统 raw fd 与 Go dup fd 分别持有，先停读/关闭副本，再释放系统资源。
结果分别在 `.dev/harmonyos/runtime/` 和 `vpn-probe/`，缺少授权组件记录 blocked，
不能解释为授权被拒绝或绕过系统授权。外部 echo 服务不可用时可替换为受控测试服务。

## iOS 开发环境准备

需要 macOS、Xcode 27.1、Go 1.27.1；iPhone/iPad 共用 `ios/FnCPN.xcodeproj`。
初始化脚本检查工具链并准备匹配的模拟器 Runtime，支持重跑；日志保存在 `.dev/logs/`。

```sh
bash scripts/setup-ios-macos.sh
# 仅准备编译环境，不下载 Runtime
bash scripts/setup-ios-macos.sh --skip-runtime
python3 -m unittest discover -s scripts/ios -p '*_test.py'
```

### iOS/iPadOS 开发构建与模拟器

```sh
bash scripts/build-ios.sh
bash scripts/build-ios.sh --install <SIMULATOR_UDID>
bash scripts/build-ios.sh --device
```

开发产物在 `.dev/ios/DerivedData/Build/Products/Debug-<SDK>/FnCPN.app`，包含
`PacketTunnel.appex`。真机开发产物未签名，不可直接安装。
修改工程结构应编辑 `scripts/ios/project.py`；生成器会覆盖 Xcode 工程。

模拟器可登录并通过菜单的应用内网络调试执行 HTTP(S) GET 或 `dns:完整私网域名`，
请求经过同一 Go 核心与用户态 netstack。配置、日志和 Keychain 独立于真机。
Safari、其他 App 和系统 DNS 不经过应用内隧道，系统 VPN、锁屏和切网需要真机验证。

### iOS 个人设备本地重签

完整安装教程：[中文](ios-installation.zh-CN.md) / [English](ios-installation.en.md)。
项目不持有签名私钥，不把签名材料放入源码、CI 或 Release 资产。

### iOS Release IPA

```sh
# 干净 checkout，不签名、不安装、不上传
bash scripts/package-ios.sh
# 尚未提交的本地 review 构建；证据明确标为 dirty，不接受为发布候选
bash scripts/package-ios.sh --allow-dirty --out .dev/ios/review-package
```

输出 `dist/ios/fncpn-<version>-ios-arm64-unsigned.ipa` 及 `build-manifest.json`。
使用 Xcode 27.1、Release/device ARM64，检查 App/扩展版本、无签名/描述文件、设备 Mach-O、
Packet Tunnel 声明和许可证。Go 链接器生成的临时 ad-hoc 签名也会移除。
`ios/BUILD_NUMBER` 是独立、递增的 iOS 构建编号；发布新候选前按需递增，两 target 必须相同。

`build-ios.yml` 使用 Xcode 27 runner，与桌面 Xcode 16.4 job 隔离；无须 Apple 证书 Secret。
Release workflow 下载同一 commit 的 iOS 资产，再纳入统一 manifest、SHA256 和草稿核验。
公开草稿前仍需对最终 IPA 重签、安装及连接验证。发布前检查表见 [验收](validation.md)。

### 移动端构建覆盖

Fyne 固定 2.8.2，覆盖仅写入 `.dev/` 本地副本，不修改 Go module 缓存；升级依赖时重新验证
UIScene、键盘、Core Text 及渲染匹配点。iOS 普通文字使用 Core Text/Core Graphics，保留
Fyne 符号字体，不内嵌完整 CJK 字库。Android 共用有界解析字体缓存及登录按钮触摸策略。

```sh
(cd mobile && go test -tags ci ./... && go test -race -tags ci ./... && go vet -tags ci ./...)
python3 scripts/test_fyne_font_cache.py
python3 scripts/test_fyne_keyboard_tap.py
python3 scripts/ios_release_test.py
```

普通 iPhone/iPad 与 Android 共用登录、键盘导航、横屏左文案/右表单、Toast、后台 loading、
同源导航和诊断 ZIP。iPhone Duo 的专用图标栏仅对 `iPhone19,4`、iOS 27.1 起启用，跟随
相机侧切换位置；展开菜单不挤压正文，登录返回恢复进入前的展开状态。其他设备保持共享布局。
Duo 展开姿态仅暴露整体避让框时，使用实测 `47⅚ pt` 中心距作为该型号回退，不外推到新机型。

日志区分 client 与 privileged，App 通过 Provider 消息分页同步脱敏快照；打开日志使用系统
文件选择与 Quick Look，导出共用 ZIP 结构。主应用 NAS IP 按钮交给默认浏览器，管理后台为
独立临时网页会话；同源主页面留在窗口，用户点击的外部公共域名链接交给浏览器。

## Android 开发与构建

Android 源码位于独立 `mobile` module；纯 Go 开发不需要 Android 工具链。完整构建与专项真机结果见 [验证记录](validation.md)，能力边界见 [平台决策](platform-support.md#android)。

桌面开发可设置 `FNCPN_CLIENT_SOCKET`，复用已安装且运行的桌面 daemon；
不会由本界面自动安装或启动服务。未设置时明确提示不可用，不显示模拟连接。

```sh
cd mobile
go run .
go test -tags ci,no_emoji ./...
go test -race -tags ci,no_emoji ./...
go vet -tags ci,no_emoji ./...
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build ./internal/androidvpn
```

以上命令在 `mobile/` 执行；`ci` 使用无显示驱动。测试覆盖真实授权注册表的 OTP 续接、宿主生命周期、两份 netstack 之间
的 DNS UDP/TCP 收发、后缀隔离、DNS 计划回滚、fd 替换和 socket 保护失败清理。
纯 Go 交叉编译不能验证 Java、JNI/C、Android 链接和系统行为。

### Android 构建与签名

macOS 可以显式运行初始化脚本，复用已有匹配工具，仅安装缺失组件：

```sh
bash scripts/setup-android-macos.sh
# 初始化完成后重新打开终端，或 source ~/.zshrc（Bash 使用 ~/.bashrc）
```

脚本参考 Windows 初始化入口：分阶段计时、日志保存在 `.dev/logs/`，校验后的下载缓存
保存在 `.dev/downloads/`，中断后可重跑。Go 固定 1.27.1、Gradle 固定 8.13；
缺失时安装到用户级 `~/.local/opt/`，不替换其他版本。已有 JDK 17、Python 3 优先复用，
缺失时使用主机已有 Homebrew 安装；没有 Homebrew 时明确报错，不自动安装包管理器。
SDK 使用 `ANDROID_HOME`（其次 `ANDROID_SDK_ROOT`），默认 `~/Library/Android/sdk`；
SDK manager 复用已安装的匹配 SDK / NDK 包。不安装 Android Studio、模拟器或系统镜像，
Java、Go、Gradle、Android 的环境变量和各自 PATH 条目直接写入 `~/.zshrc`（设置了
`ZDOTDIR` 时写入对应目录）及 `~/.bashrc`，同时写入 Bash 实际使用的登录启动文件
（依次选择已有 `.bash_profile`、`.bash_login`、`.profile`；均不存在时创建 `.bash_profile`）。
每项依赖使用独立标记区块，重跑更新区块且不重复添加 PATH；保留其他配置，变更前在原目录备份，
不生成单独的 env 文件。不修改系统代理或 Go 代理配置。建议预留 10–15 GB 磁盘空间。

默认交互接受 SDK 许可证，下载 Go module 后执行双架构 Debug APK 构建验证。
`--accept-licenses` 显式接受许可证并免交互；`--skip-smoke` 跳过 APK 构建验证，
依然安装工具并下载 Go module。首次 Gradle 构建会联网下载 AGP 等依赖。
初始化安装是显式操作，与下面只使用已有工具链的打包入口分开。

以下入口要求主机已经配备 Android 工具链；脚本不自动安装。

- 固定 Go 1.27.1、Fyne 2.8.2、JDK 17、Gradle 8.13、AGP 8.13.2。
- SDK platform 36、build tools 35.0.0；NDK 固定 `28.2.13676358`。
- `ANDROID_HOME` / `ANDROID_NDK_HOME` 指向已有 SDK / NDK。
- `scripts/android/prepare.py` 拒绝缺失或不匹配的 Go/NDK，不安装工具链，不运行 sdkmanager。
- 未添加 Gradle wrapper，使用已有 Gradle，离线模式缺少插件缓存时直接失败。

```sh
cd mobile/android
gradle --offline :app:assembleDebug
```

以上 Gradle 命令从仓库根目录进入 `mobile/android/`；后续 Gradle 和许可更新命令也在该目录执行。

Gradle 从固定 Fyne module 复制原始 Java driver 到构建目录，不维护 Fyne 分叉或修改已打包 APK。
构建分别输出 `app-arm64-v8a-debug.apk`、`app-x86_64-debug.apk`，每个 APK 仅含对应架构的单库，
不生成同时携带两套库的通用 APK。启用 16 KB 页对齐，记录产品版本、commit、工具链和 ABI。
依赖许可见 `mobile/THIRD_PARTY_NOTICES.md`，随 APK 打包。
可运行 `python3 ../../scripts/android/notices.py` 按 Android 包图更新许可，不需要 SDK/NDK。

Android versionName 读取根 `internal/version/VERSION`，versionCode 通过 `-PfncpnBuild=递增整数` 指定。
新增平台复用既有协议、无新增 server 要求时按 PATCH 发布；发布须完成版本与真机验收。
所有构建（含 Debug、开发版与 Release）均使用同一 FnCPN 正式证书，不再回退到 Debug keystore。
CI 从 `FNCPN_KEYSTORE`、`FNCPN_STORE_PASSWORD`、`FNCPN_KEY_ALIAS`、`FNCPN_KEY_PASSWORD` 读取；
本地默认读取下述仓库外凭据文件，可通过 `FNCPN_SIGNING_CREDENTIALS` 指定其他文件。
环境变量优先；缺少签名配置时构建直接失败，不提交或打印凭据。
FnCPN 正式签名使用应用独立 JKS：本地约定存放于 `~/.android/keystores/fncpn-release.jks`，
alias 为 `fncpn-release`，证书主体 `CN=rectcircle,C=CN`，RSA 3072 位，有效期 10000 天。
证书 SHA-256 指纹为
`BB:AA:0F:80:8A:64:9A:CA:7B:F7:5A:49:20:2A:A9:2A:C5:73:F5:70:35:09:E8:A1:70:D4:EC:77:16:A9:35:79`。
本地凭据位于同目录 `fncpn-release.credentials.json`，目录权限 0700、文件权限 0600；
密钥库和密码须另外加密备份，不进入源码或日志。
仓库 Actions Secrets 已配置 `FNCPN_KEYSTORE_BASE64`、`FNCPN_STORE_PASSWORD`、
`FNCPN_KEY_ALIAS`、`FNCPN_KEY_PASSWORD`。正式签名 job 需将前者解码到 runner 临时文件，
将该绝对路径传给 `FNCPN_KEYSTORE`，其余 Secret 作为同名环境变量传入，完成后删除临时密钥库。
分支推送构建具有调试能力、使用正式证书签名的 APK；Release workflow 通过复用 Android workflow
构建非 debuggable 的正式包，生产包名为 `cn.rectcircle.fncpn`，两份 APK 合入发布清单。
正式 versionCode 采用 tag commit 的 `git rev-list --count HEAD`，同一候选双 ABI 相同，后续发布单调增加。
通过版本与真机验收后可在干净源码树运行：

```sh
gradle --offline :app:bundleRelease -PfncpnBuild=递增整数
```

完整构建与实机专项按 [验证记录](validation.md) 记录；通过构建不等于所有后台、切网和兼容性验收完成。
方案结论与待验收范围见 [Android 支持](platform-support.md#android)。
`mobile/FyneApp.toml` 的生成版本 0.0.0 仅为开发占位，不作为正式发布版本，发布时统一注入产品版本。

### Android 界面与包体积

Android/iOS 共用 Fyne 页面、登录输入法 Next/Go、焦点避让与横屏双栏；最后一个字段在空间
允许时同时露出提交按钮。菜单进入登录页后返回原页面，只有进入前已展开才恢复展开；
登录失败保留表单和错误，状态更新不能替代授权成功。Duo 的系统侧栏是限定型号的呈现适配。

Toast 与信息/确认弹窗由共享 UI 实现，Toast 位于画布中下方。Android 通知、文件入口及
快捷设置标题使用英文/中文资源。日志事件、文件名与诊断字段不随系统语言变化。
管理后台先显示可取消 loading，再初始化原生 WebView，失败或超时可重试；单窗口会话
关闭后清理网页存储，用户点击公共外域链接交给默认浏览器，同源主页面保留在后台窗口。

菜单在启动阶段创建并预热 GL 文字/图标纹理，字体解析使用有界只读缓存，Face 按作用域
独立；主题切换刷新缓存颜色。动画只移动持久面板，不逐帧重排文字。Android 使用系统字体，
不打包完整 CJK 字库；控件普通输入使用系统键盘，登录提交按钮在确认点击后才取消焦点。

显式退出先成功断开并保存暂停意图，再清理 Go 宿主、停止 Service、移除任务并结束主进程。
返回桌面和普通后台切换不结束进程；键盘任务在 Activity 销毁或退出后失效。
系统强制停止后的恢复及厂商后台限制见 [支持边界](platform-support.md#android)。

日志通过只读 DocumentsProvider 交给系统文件界面，失败回退到文件选择器；导出使用共享
脱敏 ZIP。后台 Activity 在专用 `:admin` 进程，不复用 Fyne 宿主的 WebView/生命周期。

可调试包可通过启动参数 `--ez fncpn.uiProfile true` 采集一次 45 秒菜单 CPU profile 与
阶段事件，写入 App 缓存；正常启动不采样，非 debuggable 包忽略该参数。
诊断文件用于比较冷启动首次菜单与后续点击，不采集输入内容，不纳入发布资产。

### Android 本地真机调试与日志

CI 和本地 Gradle 统一使用固定 FnCPN 正式证书，后续同包名可相互覆盖（versionCode 不得倒退）。
历史使用旧 Debug 密钥的开发包不能直接覆盖；不要自动卸载或清除数据，需要先安排数据保留/重新登录。
本地可运行 `gradle :app:assembleDebug :app:assembleDebugAndroidTest -PfncpnBuild=递增整数 -PfncpnDev`，
安装独立包 `cn.rectcircle.fncpn.dev`（桌面名称“FnCPN 开发版”），保留已安装的 CI 包与数据。
普通 Debug 包名仍是 `cn.rectcircle.fncpn.debug`，正式包名不变；三种包使用同一受保护的固定发布密钥。

```sh
adb install -r app/build/outputs/apk/debug/app-arm64-v8a-debug.apk
adb install -r app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk
adb shell am instrument -w cn.rectcircle.fncpn.dev.test/cn.rectcircle.fncpn.SmokeInstrumentation
adb exec-out run-as cn.rectcircle.fncpn.dev cat files/logs/client.log > client.log
adb exec-out run-as cn.rectcircle.fncpn.dev cat files/logs/android.log > android.log
```

真机 instrumentation 使用本机 HTTP 页面检查管理 WebView 加载及系统栏/返回栏避让，不需要 NAS 账号。
真实 NAS 直连和后台功能仍须另外验收。IPv6 前置检查使用受保护且绑定所选物理网络的 UDP6 socket，
允许通过上游转换访问公网的 ULA 源地址；路由检查通过后仍须实际 WireGuard 握手。

Go 日志 `files/logs/client.log` 每份 2 MiB、保留两份备份；Java 系统事件日志
`files/logs/android.log` 每份约 512 KiB、保留一份备份，同时写入 `logcat -s FnCPN`。
Java 日志不记录 RPC 参数、地址、凭据和异常消息。设置页可导出诊断与近期日志 ZIP，
使用字段白名单排除身份、端点及自由错误文本；每份原始日志最多读取末尾 256 KiB。
分享仅临时授予 ZIP 的读取权限，最多保留八份，缓存 24 小时后失效并在下次导出清理；不依赖 Release 的 `run-as`。
原始 Go 文件供本机开发诊断，不能视作对外匿名报告。

### Android CI（Debug 构建）

[Android workflow](../.github/workflows/build-android.yml) 在任意分支推送、PR 和手动触发时运行。
提交需要推送到 GitHub 才会触发，不要求本机安装 Android 工具链。
推送和手动构建必须配置上述四项签名 Secrets；PR 只运行 Go/Python 检查，不执行持有正式私钥的构建。

流程先运行共享连接宿主和移动 module 的竞态测试、静态检查及 APK 校验脚本回归；
通过后，在 Ubuntu runner 配置上文固定版本的 Go、JDK、Gradle、SDK 和 NDK，下载 Go/Gradle
依赖并执行 `assembleDebug`。CI 首次构建允许在线下载依赖，不使用本地已有缓存场景的 `--offline`。
SDK/NDK 的安装步骤只存在于 workflow；本机构建脚本仍拒绝自动安装。

`scripts/android/verify_apk.py` 检查 Debug 包名、产品版本、run number 对应的 versionCode、
源码 commit、每份 APK 的唯一目标 ABI、JNI/Fyne 入口、16 KB ELF/ZIP 对齐及固定 FnCPN 证书指纹。
只有校验通过才上传 `fncpn-android-debug-<run>-<attempt>`，内含两份单架构 APK、SHA256SUMS 和每份 APK 的 `.verification.json`。
`fncpn-android-logs-<run>-<attempt>` 保留构建/校验日志和 Gradle 报告，失败时也尝试上传；产物保留 14 天。

普通分支流程使用正式证书签名，在 runner 临时目录还原密钥，任务结束清理；不创建 GitHub Release，
不公开或覆盖正式产物，版本与 server 的准入契约不变。构建成功也不等于已完成安装、
Service 冷启动或真实网络验收；首轮云端运行结果仍须单独记录。

## 测试

```bash
go test -race ./...
go vet ./...
node --test internal/server/web/index.test.cjs
```


### 网络恢复检查

根 module 的常规测试覆盖恢复逻辑；修改协调器、选路或取消行为时，可在 macOS 定向执行：

```sh
go test -race ./internal/client ./internal/ipc ./internal/platform/darwin
```

重点检查重复通知与连续切网、手动请求合并、断开取消和迟到结果、无通知重试、保留健康连接、多网卡选路、阶段预算及跨进程取消。权限相关改动还需覆盖等待、允许、超时、跳过和暂停保护。具体参数由源码及确定性测试维护；系统实测范围见 [验证记录](validation.md#网络恢复验收边界)。

### Wails UI 检查

```sh
(cd ui && MACOSX_DEPLOYMENT_TARGET=13.0 go test -race ./...)
(cd ui/frontend && npm ci && npm test && npm run build)
bash -n scripts/build-native-macos.sh scripts/build-wails-macos.sh scripts/build-macos-pkg.sh scripts/macos-app-common.sh
```

自动测试覆盖真实 IPC 编解码、多因子错误、组件版本不匹配、管理后台地址边界，以及
与原生一致的状态/权限呈现。完整安装器同时执行根项目测试和已有包结构检查。
真实 NAS 的用户名密码/访问码/OTP 登录、自启登录会话、网络切换恢复和实际升级/卸载，
需要在对应环境继续实机验收；构建通过不替代这些结论。

### 诊断与安全回归

修改错误分类、日志、鉴权或恢复语义时，按以下测试定位覆盖范围；常规命令见本节开头，平台依赖测试应在对应环境执行。测试替身不能代替实际握手、NAS 转发或系统安装验收。

- `internal/client/error_chain_test.go`：真实 Unix IPC → 服务端 HTTP → 客户端 →
  第二次 IPC，验证原因、回滚失败和请求 ID 不丢失。
- `internal/client/errors_test.go`：HTTP/WSS 200 `invalid token`、401、403、404、410、
  429、5xx、HTML、敏感正文、坏 JSON、读取失败、发现业务码和最新中继事件。
- `internal/client/gateway_recovery_test.go`：应用入口 404 的分类边界、结构化永久错误
  优先；真实 HTTP/WebSocket 模拟断线、404/502 和恢复，验证配置监听自动恢复、
  管理器不清理现有网络、不更换设备身份、中继恢复后数据报可往返。特权网络为
  测试替身，不据此声称已验证实机 WireGuard 握手或 NAS 转发。
- `internal/client/manager_logging_test.go`：网关 200 鉴权失败进入 `AUTH_REQUIRED`，
  客户端状态和日志均保留真实 HTTP 状态与原因。
- `internal/client/observability_test.go`、`internal/server/observability_test.go`：
  授权和连接 INFO、请求上下文、跨重新授权/重启的设备幂等、同名不同密钥、
  正常轮询和维护不刷日志，以及凭据不泄漏。
- `internal/privileged/observability_test.go`、`internal/logging/context_test.go`：
  身份文件创建/覆盖/删除事件与请求日志作用域；凭据读取和 Cookie 刷新不刷日志。
- `internal/server/devices_test.go`：RX 超时、计数重置和未知状态保护、实时删除校验、
  权限、失败回滚、并发删除、状态日志及同名记录隔离。
- `internal/server/web/iframe.test.cjs`：跨源受限 iframe 中的真实 Chrome 回归，
  验证确认/取消/Esc、确认期间重新上线、后台拒绝、删除失败保留行和窄屏布局。
- `internal/client/relay_test.go`、`internal/server/relay_test.go`：中继建立、正常结束、
  断线重拨失败与恢复的日志，不包含 URL 查询、Cookie 或报文内容。
- `internal/client/native_session_test.go`、`internal/client/adminproxy_test.go`：
  CLI/Web token 隔离、Web 登录失败不影响建链、本地 bootstrap nonce、路径限制和
  浏览器 Cookie 不得覆盖代理注入凭据。
- `internal/server/relay_test.go`：网关改写 Host、Origin 缺失/不同均可升级，
  无网关身份仍拒绝，容量限制和二进制转发保留。
- `internal/model/error_details_test.go`、`internal/logging/rotate_test.go`：
  错误链、联合错误、脱敏、字段保留和系统错误分类。
- 授权 Service、原生 AppKit 登录页与管理页测试验证错误展示、失败授权记录，
  以及后台错误不会覆盖尚未提交的表单错误。
- `internal/platform/linux/firewall_docker_linux_test.go`：在独立 Linux 网络命名空间中
  验证真实转发、NAT、Docker 默认 DROP、iptables-nft 兼容性、规则更新和清理，
  不修改宿主机现有网络规则。

## 中英文资源

开发与测试需要 Python 3（仅使用标准库，无新增运行时依赖）。
统一文案源为 `localization/messages.json`：键使用中文源文案，英文值为对应翻译；
动态参数使用 `{0}`、`{1}`，不要拼接需要翻译的句子。更新后运行：

```bash
python3 scripts/sync-localizations.py
python3 scripts/sync-localizations.py --check
node --test internal/server/web/index.test.cjs
```

脚本生成 macOS `.lproj/Localizable.strings`、HarmonyOS `Messages.ets` 和管理页内嵌词典，并检查缺失翻译和参数一致性。
生成文件需一起提交；macOS 打包会复制两种语言资源。`go test ./...` 包含资源同步检查及
macOS 中英文/回退运行测试；`FNCPN_LAYOUT_CHECK=1 go test ./packaging -count=1` 检查两种语言的窗口布局。

## 产品版本与正式发布准备

唯一产品版本源为 `internal/version/VERSION`；修改后运行
`python3 scripts/sync-version.py` 同步 fnOS manifest、macOS plist、Wails、HarmonyOS 元数据/界面版本及生成的 iOS 工程版本。
Go CLI 默认构建也会嵌入这个版本，不再报告 `dev`。
远程兼容性只检查 MAJOR 相同且 client MINOR 不大于 server MINOR，PATCH 不参与。
稳定的 `/version` 入口、HTTP/relay 响应版本和带身份校验的局域网探测支持升级后重新检查。
开发期已使用该判定器进行验证，正式兼容性承诺从 `1.0.0` 生效。

普通开发构建允许重建本地产物。正式构建使用 `RELEASE=1 ./scripts/build-all.sh`，
要求干净 checkout、显式版本与完整候选 commit，且不存在对应版本 tag；拒绝覆盖本地同名产物。重建草稿应使用新的 `DIST_DIR`。
构建生成仅包含当前发布产物的 `release-<version>.json` 和 `SHA256SUMS-<version>`。
发布时应归档安装包、元数据、协议与数据样本，后续兼容验收使用这些历史基线。
正式产物需完成安装与基础连接确认；已有验证及暂缓范围见 [验证记录](validation.md)。

## RPM 签名与密钥维护

FnCPN 的 Core/UI RPM 使用项目专用 OpenPGP v4 RSA-4096 密钥和 SHA256 签名。
不需要购买证书或付费签名服务。Debian、Arch、macOS 和 Windows 包不使用此 RPM 签名。

### 公钥与首次安装

公钥：[RPM-GPG-KEY-fncpn](../packaging/linux/keys/RPM-GPG-KEY-fncpn)。完整指纹：

```text
B2AA E111 998B BE82 DECC 9F10 45BB E575 8B86 1A6A
```

自动安装脚本从所选版本 tag 读取公钥，先核对脚本固定的公钥 SHA256，再在只包含该
公钥的临时 RPM 数据库中验证所有待安装包。全部通过后才将公钥导入系统，保持原生
包管理器签名策略；Fedora 的本地包安装显式启用 `localpkg_gpgcheck=True`。
旧的未签名草稿 RPM 不满足此条件，应改用签名后的草稿资产。

手动安装时，从可信来源下载公钥，并核对完整指纹后执行：

```sh
# 有 GnuPG 的环境可先查看完整指纹，与上方记录比对
# 不要仅比较短 key ID
gpg --show-keys --with-fingerprint RPM-GPG-KEY-fncpn
sudo rpmkeys --import RPM-GPG-KEY-fncpn
rpmkeys --checksig --verbose ./fncpn-core-*.rpm ./fncpn-ui-*.rpm
# 然后使用 dnf 或 zypper 成套安装同版本 Core/UI
```

必须同时有有效签名和有效内容摘要；只有 `digest: OK` 不能证明包已签名。
公钥和 RPM 可公开分发，签名私钥与口令不进入仓库、日志或发布资产。

### 草稿构建

`release.yml` 将 `RPM_SIGNING_PRIVATE_KEY`、`RPM_SIGNING_PASSPHRASE` 两个 Actions
Secrets 传递给 Linux 构建流程。仅 RPM 发布构建的签名步骤接收它们；普通 PR、其他格式
和未开启发布模式的手动构建不会导入私钥。缺少 Secret、指纹不符或验签失败时直接失败。

`rpm_signing.py` 使用独立临时 GnuPG 目录，通过 stdin 导入私钥，签名后关闭临时 agent
并删除目录。口令只通过权限受限的临时文件传给 GnuPG，不放在命令行参数中。
上传目录是 `dist/linux/`，不包含签名临时目录。

流程顺序为：构建 Core/UI → 签名 RPM → 隔离公钥验签 → 生成构建清单和 SHA256 →
在 Fedora/openSUSE 安装同一份签名包 → 统一发布清单。
兼容性测试不再允许未签名本地 RPM，证据包含 `rpm-signature` 和公钥指纹；发布清单
拒绝缺少签名证据或使用其他公钥的候选。发布后不得重新签名覆盖旧包。

`rpm_signing_test.py` 覆盖摘要成功但未签名、签名/摘要失败、缺少 Secret，并在 RPM
签名 CI 中使用实际包验证正常签名、移除签名、篡改内容和错误公钥。

### 本地密钥与恢复

维护者的密钥保留在默认 `~/.gnupg/`，私钥位于 `private-keys-v1.d/`，吊销证书位于
`openpgp-revocs.d/`。现有维护环境的随机口令保存在 `~/.gnupg/fncpn-rpm-passphrase`，
文件权限为 `0600`；不要将该目录或口令文件加入项目、同步日志或公开分享。
用 `gpg --list-secret-keys --with-fingerprint` 查看密钥及到期时间。

GitHub Secrets 是 CI 使用副本，网页/API 不提供原值下载。必要时可另外编写仅手动触发、
经过审核的恢复 workflow，将 Secret 用维护者临时提供的公钥加密后输出密文；不能输出
明文私钥。当前没有创建恢复 workflow，也没有将私钥备份到仓库或 Release。

密钥续期或轮换前，更新公开公钥、指纹文件、安装脚本固定的公钥哈希和对应 Secrets，
同步说明旧包的信任方式并重新验证。不要删除仍用于验收历史包的公钥。
失去私钥和 Secret 副本后无法从公钥重建私钥；泄露时应使用吊销证书并更换密钥。

参考：[RPM 签名](https://rpm.org/docs/4.20.x/man/rpmsign.8)、
[RPM 公钥管理](https://rpm.org/docs/4.20.x/man/rpmkeys.8)、
[GnuPG 密钥管理](https://gnupg.org/documentation/manuals/gnupg/OpenPGP-Key-Management.html)。

## GitHub 发布与 Homebrew

流水线固定 Go 1.27.1、Xcode 16.4 / 对应 SDK、Node 22.14.0 和 fnpack 1.2.3（下载验证 SHA256），使用 macOS 15 Apple Silicon runner。临时目录按构建随机隔离。
本地首发环境可能不同，发布清单记录实际工具版本；两端同一 commit 不等于构建字节可复现。

1. 更新 `internal/version/VERSION`（纯数字 `MAJOR.MINOR.PATCH`）、同步模板、更新 CHANGELOG，提交代码。
2. 推送候选 commit 后运行 `gh workflow run release.yml --ref main -f version=1.1.6 -f commit="$(git rev-parse HEAD)"`（替换版本号）。流水线固定完整 SHA 检查、构建并上传草稿，不创建 tag、不自动公开。
3. 草稿出错可修复、提交新 commit 后按新 SHA 重建及替换；版本 tag 在草稿期间必须不存在。Review 使用 `https://github.com/rectcircle/fn-connect-private-network/compare/v<上一正式版本>...<候选完整SHA>`。公开后兼容修复应升 PATCH。
4. 候选验收后，推荐运行 `python3 scripts/quark-release.py dispatch v1.0.10`（替换为待发布版本），先检查本地夸克授权并通过 `gh secret set` 同步 Secret，再触发公开发布。仅需 GitHub 发布时仍可运行 `gh workflow run release.yml --ref main -f version=1.0.10 -f commit=<草稿清单中的完整SHA> -f publish=true`，但必须预先准备夸克 Secret，否则后续镜像 job 会失败。独立的 publish job 下载原草稿产物复核并公开，不依赖构建 job，不安装编译工具、不重建或重新上传资产；也可使用本地已验收的原始产物运行 `python3 scripts/publish-release.py dist --publish`，避免将另一批未经确认的构建视为已验收。
5. 草稿及其资产均按 Release ID 操作，更新草稿时显式保留 `tag_name`，支持尚无 Git tag 的草稿；下载入口使用 `python3 scripts/download-release.py v<版本> dist`。Release 说明从 CHANGELOG 对应版本条目提取；尚未登记版本时使用 Unreleased，公开前应完成版本条目。发布脚本下载远端所有资产逐字节复核后，在候选 commit 创建版本 tag，再公开；如创建 tag 后公开失败，重试仅允许复用相同 commit 的 tag；已公开版本只允许校验一致的幂等重试，不覆盖。
6. `python3 scripts/update-cask.py <version>` 仅从公开稳定版发布清单生成 `Casks/fncpn.rb`；脚本拒绝非数字版本、草稿和 GitHub prerelease；公开任务自动提交并推送 Cask 更新。提交并推送生成文件。

PKG Cask 安装使用系统 installer，卸载先运行项目脚本做网络清理，再清理 package receipt；默认保留用户配置与凭据。
不配置自动 zap/purge，防止误删其他用户数据。Homebrew 统一标识符为 `fncpn`，只跟随稳定版。
历史正式协议及数据基线用于后续兼容验证，归档要求见 [版本契约](versioning-and-compatibility.md)。

Android 从 1.1.4 起提供 `fncpn-<version>-android-{arm64-v8a,x86_64}.apk`，均使用固定项目证书签名。
iOS 从 1.1.5 起提供 `fncpn-<version>-ios-arm64-unsigned.ipa`，独立 Xcode 27.1 job 记录同 commit 的结构核验与未签名证据；汇总、公开及夸克镜像都要求该资产。
候选构建同时启动 Android Release 构建，macOS 汇总 job 下载同 commit 的 APK 和验签证据，
校验非调试包、签名指纹、版本、ABI 与 SHA256 后纳入统一清单；公开与夸克镜像同样要求双 ABI APK。

macOS、Windows、fnOS 安装包命名使用小写 `fncpn-<version>-<os>-<arch>[-unsigned].<extension>`，系统为 `macos`、`windows`、`fnos`，架构为 `arm64`、`x64`。fnOS manifest 内的 `x86`/`arm` 平台标识保持不变，仅对外文件名统一。`release-<version>.json` 与 `SHA256SUMS-<version>` 为发布清单和校验和，沿用现有名称。Linux 使用 `fncpn-{core,ui}-<version>-linux-{amd64,arm64}.{deb,rpm,pkg.tar.zst}`，
Arch 仅 amd64；两个组件必须成套更新，Nix 使用 Flake 源码构建。
已公开安装包不改名，Homebrew 生成器支持历史文件名。

macOS 发布在 Apple Silicon runner 上构建 arm64 和 x64 两套 PKG，Swift 使用 `arm64`/`x86_64` target，Go 使用 `arm64`/`amd64` 并指定 cgo clang 架构。打包前校验 App 和 helper 的 Mach-O 架构。Intel 原生运行验证仍需 Intel Mac，不以交叉构建代替实机验收。

### 夸克网盘国内镜像

GitHub Release 是发布源。公开任务完成后，Homebrew 更新和夸克镜像作为两个独立 job 执行；
镜像失败不会撤回 GitHub Release 或阻断 Homebrew 更新，Actions 会明确报错。
`mirror-quark.yml` 也接收 GitHub 页面公开事件，并支持手动补传；通过 `GITHUB_TOKEN`
公开不会触发另一个工作流，因此 `release.yml` 显式调用可复用镜像工作流。

#### 从新克隆仓库开始

发布者需要仓库写权限、更新 Actions Secret 的权限、可运行 Actions 的仓库环境，以及目标夸克账号的人工授权。
仓库包含流程和固定目录 FID，不包含登录凭据；单独克隆源码不能直接访问维护者网盘。
准备 Python 3.11+、Node（建议与 CI 相同的 22.14.0）、Git 和 GitHub CLI，先执行 `gh auth login`。
以下命令均从仓库根目录执行，示例版本须替换为本次发布版本。

首次安装夸克 CLI（已有固定版本安装时跳过）：

```bash
python3 - <<'INSTALL_QUARK'
import importlib.util
from pathlib import Path
import shutil
import tempfile

spec = importlib.util.spec_from_file_location("quark_release", "scripts/quark-release.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
# 用仓库内的固定 URL、SHA256 和版本自检，只安装 CLI 及 hash worker。
with tempfile.TemporaryDirectory(prefix="fncpn-quark-install-") as temporary:
    entry = module.install_cli(Path(temporary) / "runtime")
    destination = module.DEFAULT_CLI.parent
    destination.mkdir(parents=True, exist_ok=False)
    for name in ("quark-drive.cjs", "hash-worker.cjs"):
        shutil.copy2(entry.parent / name, destination / name)
print(module.DEFAULT_CLI)
INSTALL_QUARK
```

首次授权或授权失效后：

```bash
CODEX_CI=1 node "$HOME/.local/share/quark-cli/1.0.24/quark-drive.cjs" login
CODEX_CI=1 node "$HOME/.local/share/quark-cli/1.0.24/quark-drive.cjs" get-user-info
```

`CODEX_CI=1` 用于让普通终端与 runner 使用相同的 Codex 授权通道，并不要求安装 Codex Skill。
浏览器中完成授权后，以 `get-user-info` 成功作为登录完成依据；仅网页显示成功不代表 CLI 已保存凭据。
如果登录等待超时，完成页面授权后再运行 `login` 检查；页面提供授权码时可本地使用
`login --token <授权码>` 完成交换，不将此授权码当成 `QUARK_AUTH`。

#### 发布与重试

提交候选完整 SHA 构建后先等待草稿构建和验收通过，再运行下方发布入口。
完成后使用 `gh run watch <运行ID> --exit-status` 确认公开、Homebrew 更新和夸克镜像三个 job 均成功。

本地发布入口（Python 3.11+、Node 16+、已登录的 `gh`）：

```bash
# 草稿构建和验收完成后：检查本地授权、更新 Secret，然后公开并镜像。
python3 scripts/quark-release.py dispatch v1.0.10

# 已公开版本的镜像失败后：更新授权并只重试镜像，不重建或替换 GitHub 资产。
python3 scripts/quark-release.py dispatch v1.0.10 --mode mirror
```

#### Token 获取与传递

登录成功后，CLI 自动把 access token、refresh token、账号和设备标识保存到
`~/.local/share/quark-cli/codex/config.json`；无需手动复制或打印 token。
`login --token` 接收的是一次性授权码，不是用于发布的长期 token。

`dispatch` 会检查本地授权，从配置中提取当前账号的最小授权状态，通过标准输入调用
`gh secret set QUARK_AUTH`，再触发公开发布。不要把 token 放进 workflow input、仓库或日志。
默认 CLI 路径为 `~/.local/share/quark-cli/1.0.24/quark-drive.cjs`，
可用 `--cli` 和 `--auth-config` 指定配套路径；失效时本地重新登录，再执行 `dispatch`。
同一时间只从一个本地会话发起发布或补传，避免并发更新同一个 Secret。

runner 只提取固定版本 CLI 的运行文件并校验压缩包 SHA256，不安装 Skill；授权配置写入临时目录并在结束后清理。
若 runner 刷新令牌后使本地旧凭据失效，下次发布须重新登录，不承诺永久免授权。

更多 CLI 用法参考 [夸克官方 Skill 仓库](https://github.com/quark-clouddrive/quarkclouddrive_offical)，
仅作参考，本项目不要求安装 Skill。

镜像流程：

1. 拒绝草稿及 prerelease，从指定公开 tag 下载全部资产；校验清单版本、tag commit 和该版本的完整平台安装包清单（1.1.2 起包括 10 个 Linux 包）、
   `SHA256SUMS` 和 GitHub 提供的全部资产 SHA256，不构建或重新打包。
2. 上传到未分享的 `/releases/fncpn-staging/v<版本>/`。重试时回读已有文件，只补传缺失文件；
   同名不同内容、重复名称或额外文件立即失败，不覆盖或删除远端内容。
3. 全部文件从夸克回读验证 SHA256 后，移动版本目录到 `/releases/fncpn/v<版本>/`，已有永久分享自动包含新目录。
   已公开镜像只允许完整校验的幂等重试，不做原地补写或替换。
4. 所有版本共享同一个镜像并发组，避免同时操作同一账号。失败时保留未分享的暂存目录供重试；
   若发现内容冲突，先排查原因，不用自动覆盖绕过错误。

目标目录 FID 固定在 `scripts/quark-release.py`，对应专用发布账号的 `releases` 与 `fncpn`；
切换账号或重建目录时必须同时调整 FID，不能只替换授权。
官网中文下载入口使用永久分享，提取码为公开下载用途，不是登录凭据；英文页面仍使用 GitHub。

已在 1.0.10 验证本地授权导出、Ubuntu runner 恢复授权、全部 8 个文件上传与 SHA256 回读、
校验后移动至公开目录，以及 Homebrew 并行更新。运行链接和未覆盖范围见
[历史发布结论](validation.md#历史发布结论)。
该次通过不代表凭据长期续期、不同账号或全部平台安装专项已验证。

## 官网开发与部署

源码位于 `website/`，使用原生 HTML / CSS / JavaScript，无 npm 依赖或构建步骤。使用 Netlify + Cloudflare 部署；网站部署与产品 tag、Release、Homebrew 更新独立，不改变产品版本或通信契约。

### 本地预览

```sh
python3 -m http.server 4174 --bind 127.0.0.1 --directory website
```

打开 <http://127.0.0.1:4174>，下载区可直接访问 <http://127.0.0.1:4174/#download>。
平台识别的无依赖测试：`node --test website/download-platform.test.mjs`。

### Netlify

导入此仓库并选择生产分支，使用根目录 `netlify.toml`：

| 设置 | 值 |
| --- | --- |
| Base directory | 留空，仓库根目录 |
| Build command | 留空 |
| Publish directory | `website` |

配置包含 CSP、禁止嵌入、权限限制等响应头，资源每次再验证缓存。`website/404.html` 用于未知路径，不使用 SPA 通配回写。CSP 只允许本站资源，新增外部资源时需要同步检查配置。

### 自定义域名与 Cloudflare

生产域名为 `https://fncpn.rectcircle.cn`。首页 canonical、分享元数据、sitemap 和 robots.txt 已统一使用该地址。

1. 取得 Netlify 的实际生产地址，在 Domain management 中绑定 `fncpn.rectcircle.cn`并设为主域名，不迁移到 Netlify DNS。
2. 在现有 Cloudflare 区域添加 `fncpn` 子域名的 CNAME，目标使用实际 `<site>.netlify.app` 主机名，不包含协议或路径。检查同名记录用途，避免冲突。
3. 初始使用 DNS only（灰云），完成 Netlify 验证及自定义域名 HTTPS 证书签发。
4. 确认源站证书有效后开启 Proxied（橙云），SSL/TLS 使用 Full (strict)。如区域有其他站点，可针对该主机名配置规则。
5. 上线后检查 HTTPS、资源加载、404 状态、下载链接、路径切换和命令复制。若代理影响域名验证或证书续期，暂时切回灰云完成验证。

客户端官网菜单、服务端管理页、fnOS 应用描述、安装器元数据与 Homebrew 主页均使用此官网；发布版本以 GitHub 为发布源，中文下载区另提供夸克网盘永久分享；问题反馈使用 GitHub 对应入口。请在交付这些客户端/服务端更新前确认官网已可访问。

### 维护

- 下载区同时维护 Mac、Windows、Linux 和 Nix 入口；Linux 默认指向 latest 公开版，草稿需要 token，不能让网页自动选择草稿。GNOME 托盘、DNS 前提和包格式矩阵链接到对应文档。
- 内容以根目录 README 和版本兼容文档为依据。平台支持、签名和验证范围变化时同步更新页面。
- 下载链接进入 GitHub Releases；中文页面在 GitHub 或 Homebrew 访问较慢时另提供夸克网盘入口，用户按版本、平台及架构选包，避免网页硬编码产品版本导致过时。
- 后续通过 Netlify Git 部署发布网页修改，无需创建产品版本 tag。
- 本地预览通过不等于生产部署成功；推送后应确认 Netlify 部署与生产页面内容。本文不记录或推断当前 Cloudflare 配置。

参考：[Netlify 配置](https://docs.netlify.com/build/configure-builds/file-based-configuration/)、[外部 DNS](https://docs.netlify.com/manage/domains/configure-domains/configure-external-dns/)、[HTTPS](https://docs.netlify.com/manage/domains/secure-domains-with-https/https-ssl/)、[Cloudflare Full (strict)](https://developers.cloudflare.com/ssl/origin-configuration/ssl-modes/full-strict/)。
