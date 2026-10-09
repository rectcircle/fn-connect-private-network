# FN Connect Private Network

从 Mac 连接 fnOS NAS 和家庭局域网。FnCPN 自动选择局域网直达、IPv6 直连或 FN Connect 中继，无需手动配置密钥与路由。

## 使用条件

- Apple Silicon Mac，macOS 13 或更新版本。
- 已启用 FN Connect 的 fnOS NAS，支持 x86_64 和 ARM64。
- fnOS 用户账号。普通用户可以连接；安装软件和修改服务端网络设置需要管理员权限。

原生登录不支持双重认证（2FA）。

## 安装

先安装 NAS 服务端，再安装 Mac 客户端。安装包从 [GitHub Releases](https://github.com/rectcircle/fn-connect-private-network/releases) 下载：

| 设备 | 选择的文件 |
| --- | --- |
| x86_64 NAS | `fncpn-<version>-x86.fpk` |
| ARM64 NAS | `fncpn-<version>-arm.fpk` |
| Apple Silicon Mac | `FnCPN-<version>-arm64-unsigned.pkg` |

### NAS

在 fnOS 应用中心手动安装对应的 FPK，打开 FnCPN，确认服务正常运行。

### Mac：手动安装

维护者没有 Apple 开发者账号，因此 App/helper 使用 ad-hoc 签名，PKG 未签名、未公证。
如果信任本仓库及发布产物，可以通过命令行移除下载隔离标记后安装。

例如，安装下载到「下载」目录的包（将文件名替换为实际版本）：

```bash
/usr/bin/xattr -d com.apple.quarantine "$HOME/Downloads/FnCPN-<version>-arm64-unsigned.pkg"
open "$HOME/Downloads/FnCPN-<version>-arm64-unsigned.pkg"
```

按安装器提示输入管理员密码。安装完成后打开 App：

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

如果不信任预编译安装包，请先审查源码，再从选定的发布 tag 自行构建。macOS 构建需要 Apple Silicon Mac、Go 1.27、Python 3 和 Xcode/Swift 工具链。

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

1. 在 Mac 打开 FnCPN，输入 FN Connect ID、fnOS 用户名和密码。
2. 点击「授权并连接」，等待设备自动注册并建立连接。密码不会持久化保存。
3. 在连接详情中查看 NAS 地址，用该地址访问 NAS；也可访问服务端启用的 LAN 网段。

默认 NAS 私有地址为 `10.253.203.1`。管理员修改网段后，以连接详情显示的地址为准。
如果提示网段或端口冲突，在 fnOS 的 FnCPN 管理页修改网络设置后重试。

## 日常使用

- **访问 NAS**：局域网直达时使用 NAS 局域网地址；远程连接时使用详情中的 NAS 私有地址或已启用的 LAN 地址。
- **断开与重连**：断开会暂停自动连接；再次点击连接即可恢复。网络变化或重启不会取消暂停意图。
- **重新登录**：会话无法自动恢复时重新输入密码，保留设备身份与配置。
- **退出登录**：断开并清除登录凭据，保留设备身份。
- **忘记此服务端**：清除本机配置、密钥和凭据，之后需要重新设置。

本地与远端 LAN 网段重叠时，FnCPN 会提示冲突并保留 NAS 私有地址访问。中继速度取决于上游服务和网络条件。
界面支持简体中文与英文，跟随系统或浏览器首选语言。

连接失败时，先检查 FN Connect 是否可用、NAS 上 FnCPN 是否运行，再按 App 提示处理。排障与脱敏诊断见 [诊断指南](docs/diagnostics.md)。

也可以使用命令行：

```bash
fncpn status
fncpn connect
fncpn disconnect
fncpn retry
fncpn diagnose --json
```

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

## 项目资料与许可证

开发与协议资料见 [文档索引](docs/README.md)，实测范围见 [验证记录](docs/validation.md)。源码采用 [MIT 许可证](LICENSE)，依赖许可证见 [第三方声明](THIRD_PARTY_NOTICES.md)。fnOS 与 FN Connect 商标归各自权利人，本项目与其无隶属或背书关系。
