# FN Connect Private Network

从 Mac、Windows、Linux、Android、iPhone、iPad 或鸿蒙设备连接 fnOS NAS 和家庭局域网。FnCPN 自动选择局域网直达、IPv6 直连或 FN Connect 中继，无需手动配置密钥与路由。

## 使用条件

- Apple Silicon 或 Intel Mac，macOS 13 或更新版本。
- Windows 客户端：Windows 10 22H2（build 19045）或更新的 ARM64/x64 系统，不支持 32 位或 Windows Server。Windows 10 和原生 x64 已由用户完成验收。
- Linux 客户端：Debian 12/13、Ubuntu 22.04/24.04/26.04/26.10、Fedora 43/44、openSUSE Leap 16.0/Tumbleweed（amd64/arm64），以及官方 Arch Linux（amd64）；需要 systemd。NixOS 使用 [Flake 模块](docs/development.md#nix-flake-与-nixos)，不支持 Alpine。
- HarmonyOS 原生客户端：API20+、ARM64，支持手机（含折叠屏）、平板、PC/二合一；提供未签名 HAP，需用户自行调试签名安装，智慧屏暂不支持。
- iOS/iPadOS 客户端：构建目标 iOS 15.0 起，ARM64；仅提供未签名 IPA，需具备 Packet Tunnel 权限的签名材料。安装教程：[中文](docs/ios-installation.zh-CN.md) / [English](docs/ios-installation.en.md)。
- Android 客户端：Android 8.0 / API 26 起，ARM64 与 x86_64；大多数手机选择 ARM64。
- 已启用 FN Connect 的 fnOS NAS，支持 x86_64 和 ARM64。
- fnOS 用户账号。普通用户可以连接；安装软件和修改服务端网络设置需要管理员权限。

从 1.1.1 起，macOS、Windows 客户端和 Go CLI 支持 FN Connect 访问码及 fnOS 两步验证（OTP），按登录提示填写即可。

FnCPN 在自身的 fnOS HTTPS/WSS 连接中使用系统信任及内置、指纹固定的官方 ISRG Root X1 根证书，覆盖登录、发现、接口请求、中继和管理代理。补充信任仅限 `fnos.net` 与有效 FN ID 对应的子域名，保持证书链、域名、有效期和用途校验；不安装系统证书，不影响其他应用。管理 WebView 通过本机 HTTP 代理访问 fnOS。外部浏览器使用自己的证书信任设置；此前版本已安装的系统证书不会自动移除。

## 安装

先安装 NAS 服务端，再安装对应客户端。安装包从 [GitHub Releases](https://github.com/rectcircle/fn-connect-private-network/releases) 下载：

| 设备 | 选择的文件 |
| --- | --- |
| x86_64 NAS | `fncpn-<version>-fnos-x64.fpk` |
| ARM64 NAS | `fncpn-<version>-fnos-arm64.fpk` |
| Apple Silicon Mac | `fncpn-<version>-macos-arm64-unsigned.pkg` |
| Intel Mac | `fncpn-<version>-macos-x64-unsigned.pkg` |
| Windows ARM64 | `fncpn-<version>-windows-arm64-unsigned.msi` |
| iPhone / iPad | `fncpn-<version>-ios-arm64-unsigned.ipa`（需自行签名） |
| Android ARM64 | `fncpn-<version>-android-arm64-v8a.apk` |
| HarmonyOS ARM64 | `fncpn-<version>-harmonyos-arm64-release-unsigned.hap`（需自行签名） |
| Android x86_64 | `fncpn-<version>-android-x86_64.apk` |
| Windows x64 | `fncpn-<version>-windows-x64-unsigned.msi` |
| Debian / Ubuntu | `fncpn-{core,ui}-<version>-linux-{amd64,arm64}.deb` |
| Fedora / openSUSE | `fncpn-{core,ui}-<version>-linux-{amd64,arm64}.rpm` |
| Arch Linux | `fncpn-{core,ui}-<version>-linux-amd64.pkg.tar.zst` |
| Nix / NixOS | [Flake 源码构建与模块](docs/development.md#nix-flake-与-nixos)，无独立 Release 包 |

### iPhone / iPad：签名安装

从 1.1.5 起提供未签名 IPA，不能直接安装。维护者没有 Apple Developer Program 付费订阅，
当前没有 App Store / TestFlight 分发。需要支持 Network Extension（Packet Tunnel）的
设备签名证书与描述文件；维护者已验证 DxSign 和 KravaSign 的材料。

签名及安装步骤：[中文教程](docs/ios-installation.zh-CN.md) / [English guide](docs/ios-installation.en.md)。

### Android：安装

Android APK 从 **1.1.4** 起提供；草稿构建完成后需登录有权限的 GitHub 账号下载，公开后可从 Releases 或官网下载区获取。

1. 普通 ARM64 手机选择 `fncpn-<version>-android-arm64-v8a.apk`，x86_64 设备选择对应 APK。
2. 允许浏览器或文件管理器“安装未知应用”，安装并打开 FnCPN。
3. 使用 FN Connect ID 和 fnOS 账号登录，按需填写访问码/OTP；连接时允许系统 VPN 授权。
4. 查看连接路径及 DNS 后缀，使用浏览器或其他 App 访问 NAS/已启用 LAN 网段。

正式包使用固定项目证书签名，包名 `cn.rectcircle.fncpn`；后续覆盖升级保留身份、配置与连接意图。
旧开发包 `.dev`/`.debug` 与正式包独立，数据不会自动导入，正式包需要登录一次。
Android 只允许一个当前 VPN，与其他 VPN 冲突；主页面返回进入后台，菜单“退出”会断开并暂停自动连接。
系统强制停止后需手动打开 App；不承诺开机自启、始终开启/锁定模式或厂商省电限制下永久在线。
诊断页可复制状态、通过系统文件管理器查看日志，菜单可导出经过过滤的诊断 ZIP。

### HarmonyOS：安装与升级

**鸿蒙版暂未上架华为应用市场。应用功能已通过华为云真机调试验证，包括登录和 VPN 连接。**
支持 API20+、ARM64 的手机（含折叠屏）、平板、PC/二合一；智慧屏暂不支持。

从 **1.1.7 起**提供普通系统 VPN 模式的未签名 HAP：
`fncpn-<version>-harmonyos-arm64-release-unsigned.hap`。对应版本公开后可从
[GitHub Releases](https://github.com/rectcircle/fn-connect-private-network/releases) 或官网夸克镜像下载，
并对照同版本 `SHA256SUMS-<version>` 校验。未签名包不能直接安装，不要选择 `application-tunnel` 模拟器包。

有意愿的用户可尝试自行调试签名安装：

1. 注册并实名认证华为开发者账号。
2. 手机开启开发者模式和 USB 调试，连接电脑，并确认调试授权。
3. 使用 [HapSign](https://github.com/guantw/HapSign) 选择未签名 HAP，按提示登录自己的华为账号，
   申请设备绑定的调试证书和 Profile，在本地签名并通过 HDC 安装。Windows 便携版内置所需工具，
   不必另装完整 DevEco Studio；其他平台要求请查看工具文档。
   [HAP Resigner](https://github.com/toads/hap-resigner) 可作为替代工具。
4. 安装后填写 FN Connect ID 和 fnOS 账号，按提示完成访问码／OTP 和系统 VPN 授权。
5. 更新时重新签名，并保持相同包名、签名身份及包含当前设备的有效 Profile；保留签名材料，
   不要先卸载。更换包名或签名身份不承诺保留原有数据。

**自行签名安装尚未验证**：维护者暂无华为实体设备，尚未验证普通用户使用其他开发者账号
自行签名安装的完整流程，尤其是跨账号为同一已注册包名申请调试 Profile 的兼容性。
上述工具使用 Test Profile 链路尝试申请，不要求用户先改包名，但不代表华为服务端一定允许。
用户可自行验证，并通过 Issue 反馈机型、系统版本、签名工具版本及脱敏后的错误信息；
请勿上传账号令牌、密码或私钥。这项限制针对自行签名安装流程，不代表应用功能未经测试。

构建与维护者本地签名见[开发文档](docs/development.md#harmonyos-开发构建与签名)。

普通退出后再次进入会按原连接意图恢复；显式断开会暂停自动连接。
日志与诊断入口见[诊断说明](docs/diagnostics.md#harmonyos-日志与导出)。

### Linux：自动安装

下载脚本后执行，默认从 GitHub **latest 公开正式版**获取安装包：

```bash
curl -fsSL https://raw.githubusercontent.com/rectcircle/fn-connect-private-network/main/scripts/install-linux.sh -o install-fncpn-linux.sh
bash install-fncpn-linux.sh
```

脚本检测发行版、架构及桌面环境：有桌面时安装同版本 Core+UI，无桌面时仅安装 Core；
通过 SSH 运行时，也会检查已安装的桌面会话文件。已有 UI 时默认将 Core/UI 成套更新。
可使用两个参数覆盖默认行为：

```bash
# 无头 / CLI：仅安装 Core
bash install-fncpn-linux.sh --type core

# 指定版本（也接受 v1.1.2），安装 Core+UI
bash install-fncpn-linux.sh --type all --version 1.1.2

# 指定版本，仅安装 Core
bash install-fncpn-linux.sh --type core --version 1.1.2
```

Linux 安装包从 **1.1.2** 开始提供；latest 获取最新公开正式版，不会自动改装草稿。
指定草稿或读取私有 Release 时，需要先通过环境变量 `GH_TOKEN`
（或 `GITHUB_TOKEN`）提供有读取权限的 GitHub token。

脚本校验 Release 的 SHA256，再使用 apt、dnf、zypper 或 pacman 安装并解析运行依赖；
必要时会请求 sudo，并补齐 curl、jq、校验工具及 CA 证书。Arch 会执行完整系统升级。
RPM 自动安装先核对固定公钥并验证项目签名，全部通过后导入系统信任，保持包管理器签名检查。
公钥指纹和手动安装步骤见 [RPM 签名](docs/development.md#rpm-签名与密钥维护)。
已有 UI 版本不匹配时，`--type core` 会要求改用 `--type all`；自动安装不支持降级。
macOS、Windows、不支持的发行版/架构和无 systemd 的环境会直接报错。

安装后，桌面用户从应用菜单启动 FnCPN；无头用户以需要连接 NAS 的普通用户执行：

```bash
fncpn-linux-setup
```

分域 DNS 需要 systemd-resolved 和 `127.0.0.53` stub，脚本不改写物理网络或
`/etc/resolv.conf`；发行版安装 resolved 依赖时可能调整系统 resolver，须确认原有 DHCP DNS 仍正常。
GNOME 顶栏图标需要 [AppIndicator 扩展](docs/troubleshooting.md#linux系统托盘--顶栏没有-fncpn-图标)。
关闭窗口隐藏到托盘；“退出”会清理连接并停止用户 Core 服务，保留配置与连接意图。
完整支持范围、手动安装和验证状态见 [Linux 支持](docs/platform-support.md#linux)。

### Windows：安装

按 Windows 系统架构选择 MSI，双击安装并批准管理员权限。FnCPN EXE/MSI 未签名，
系统可能提示未知发布者；仅安装信任的来源。缺少 WebView2 Runtime 时首次安装需要联网。
安装完成后从开始菜单或桌面打开 FnCPN。普通卸载与重装保留用户配置和凭据。
Windows 正式安装包从 1.0.4 开始提供；公开版本的兼容修复使用新的 PATCH，不覆盖原产物。验证范围见 [变更记录](CHANGELOG.md)。

### NAS

在 fnOS 应用中心手动安装对应的 FPK，打开 FnCPN，确认服务正常运行。

### Mac：手动安装

维护者没有 Apple 开发者账号，因此 App/helper 使用 ad-hoc 签名，PKG 未签名、未公证。
如果信任本仓库及发布产物，可以通过命令行移除下载隔离标记后安装。

例如，安装下载到「下载」目录的包（将文件名替换为实际版本）：

```bash
/usr/bin/xattr -d com.apple.quarantine "$HOME/Downloads/fncpn-<version>-macos-arm64-unsigned.pkg"
open "$HOME/Downloads/fncpn-<version>-macos-arm64-unsigned.pkg"
```

按安装器提示输入管理员密码。从 1.1.1 起，图形界面安装成功后会提供“稍后 / 启动 FnCPN”提示；也可从“应用程序”手动打开 App：

```bash
open /Applications/FnCPN.app
```

如果提示 `No such xattr: com.apple.quarantine`，表示该文件没有这个标记，无需重复删除。
也可以保留隔离标记，在系统拦截后通过「系统设置 → 隐私与安全性」批准打开。

### Mac：Homebrew 安装

公开稳定版提供 Cask：

```bash
brew tap rectcircle/fn-connect-private-network https://github.com/rectcircle/fn-connect-private-network
brew install --cask rectcircle/fn-connect-private-network/fncpn
open /Applications/FnCPN.app
```

Homebrew 校验下载包，然后通过 `sudo` 调用系统命令行安装器，可能要求管理员密码。
无需预先打开 PKG 或移除隔离标记；如果实际遇到 macOS 安全拦截，再按提示通过「隐私与安全性」处理。

维护者没有 Apple 开发者账号；Homebrew 不会补上开发者签名或公证。
手动安装中的 `xattr` 命令只移除指定 PKG 的下载隔离标记，不会全局关闭 Gatekeeper 或授予局域网权限。
只应在信任源码与发布产物时批准打开或移除标记。

### 自行编译

如果不信任预编译安装包，请先审查源码，再从选定的发布 tag 自行构建。macOS 构建需要 Mac、Go 1.27、Python 3 和 Xcode/Swift 工具链。

```bash
git clone https://github.com/rectcircle/fn-connect-private-network.git
cd fn-connect-private-network
git checkout "<选定的发布tag>"
go test ./...
./scripts/build-macos-pkg.sh
```

将 tag 占位符替换为要审查和构建的版本。生成的 PKG 位于 `dist/`，用它完成安装。
本地构建也使用 ad-hoc 签名，不获得 Apple 开发者认证；fnOS 构建与完整测试步骤见 [开发与构建](docs/development.md)。

## 首次连接

1. 打开 FnCPN，输入 FN Connect ID；如果 NAS 开启了访问码，按提示填写，再输入 fnOS 用户名和密码。
2. 点击「登录并连接」。若账号需要两步验证，会进入仅显示 OTP 和“信任此设备”的步骤；填写六位 OTP 后继续，也可点击“上一步”返回修改账号密码。登录成功后自动注册设备并建立连接，密码不会持久化保存。
3. 在连接详情中查看 NAS 地址，用该地址访问 NAS；也可访问服务端启用的 LAN 网段。

默认 NAS 私有地址为 `10.253.203.1`。管理员修改网段后，以连接详情显示的地址为准。
如果提示网段或端口冲突，在 fnOS 的 FnCPN 管理页修改网络设置后重试。

## 日常使用

- **访问 NAS**：局域网直达时使用 NAS 局域网地址；远程连接时使用详情中的 NAS 私有地址或已启用的 LAN 地址。
- **断开与重连**：断开会暂停自动连接；再次点击连接即可恢复。网络变化或重启不会取消暂停意图。
- **重新登录**：会话无法自动恢复时重新输入密码，保留设备身份与配置。
- **退出登录**：断开并清除登录凭据，保留隧道设备身份和配置；下次登录会生成新的 fnOS 登录设备标识。
- **忘记此服务端**：清除本机配置、密钥和凭据，之后需要重新设置。

本地与远端 LAN 网段重叠时，FnCPN 会提示冲突并保留 NAS 私有地址访问。中继速度取决于上游服务和网络条件。
界面支持简体中文与英文，跟随系统或浏览器首选语言。

连接失败时，先检查 FN Connect 是否可用、NAS 上 FnCPN 是否运行，再按 App 提示处理。常见限制、端口和防火墙要求见 [故障排查](docs/troubleshooting.md)；日志与脱敏诊断见 [诊断指南](docs/diagnostics.md)。

也可以使用命令行：

```bash
fncpn status
fncpn connect
fncpn disconnect
fncpn retry
fncpn diagnose --json
```

`fncpn authorize <fn-id>` 会在终端交互输入 fnOS 用户名和密码，并按需要提示访问码、OTP 和设备信任。密码输入时不回显，并只通过本机 IPC 临时发送给 client daemon；命令不会打开 FnCPN App。

## 更新与卸载

更新前查看 [变更记录](CHANGELOG.md)。手动安装的 Mac 客户端使用新 PKG 覆盖安装，NAS 在应用中心更新 FPK。正常更新保留受支持格式的身份与配置；版本不兼容时，按界面提示升级对应端。

通过 Homebrew 安装的客户端可直接升级：

```bash
brew update
brew upgrade --cask rectcircle/fn-connect-private-network/fncpn
fncpn version
```

卸载：

```bash
brew uninstall --cask rectcircle/fn-connect-private-network/fncpn
```

Homebrew 只更新稳定版；测试候选版时手动下载安装包。

手动安装的 Mac 客户端使用随包卸载脚本：

```bash
sudo /Library/PrivilegedHelperTools/cn.rectcircle.fncpn/uninstall.sh
```

卸载保留用户配置、用户日志与凭据，清理特权服务日志。若要同时删除该用户的数据，改用以下命令（无法撤销）：

```bash
sudo /Library/PrivilegedHelperTools/cn.rectcircle.fncpn/uninstall.sh \
  --purge-user-data "$(id -u)"
```

如果软件由 Homebrew 安装，彻底删除该用户数据并清理 Brew 记录时使用：

```bash
sudo /Library/PrivilegedHelperTools/cn.rectcircle.fncpn/uninstall.sh \
  --purge-user-data "$(id -u)" &&
brew uninstall --cask --force rectcircle/fn-connect-private-network/fncpn
```

项目脚本会删除自身，因此第二步用 `--force` 跳过已不存在的脚本。只在第一步成功后继续；正常卸载无需 `--force`。

NAS 服务端通过 fnOS 应用中心卸载。管理页的「删除离线设备」用于清理记录，不等同于安全撤销访问权限。

## 支持项目

如果 FnCPN 对你有帮助，欢迎自愿支持项目维护。赞助将用于开发、测试和基础设施费用；软件继续免费开源。

- [爱发电](https://afdian.com/a/rectcircle)
- [Ko-fi](https://ko-fi.com/rectcircle)

## 项目资料与许可证

维护者发版步骤见 [开发与发布](docs/development.md#github-发布与-homebrew)（含夸克 CLI 安装、授权、草稿公开及失败重试）。开发与协议资料见 [文档索引](docs/README.md)，实测范围见 [验证记录](docs/validation.md)。源码采用 [MIT 许可证](LICENSE)，依赖许可证见 [第三方声明](THIRD_PARTY_NOTICES.md)。fnOS 与 FN Connect 商标归各自权利人，本项目与其无隶属或背书关系。

## 作者与联系

作者：rectcircle · 邮箱：rectcircle96@gmail.com

项目官网：https://fncpn.rectcircle.cn

源码仓库：https://github.com/rectcircle/fn-connect-private-network
