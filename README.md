# FN Connect Private Network

从 Mac 连接 fnOS NAS 和已启用的家庭局域网。客户端自动选择局域网直达、IPv6 直连或 FN Connect 中继，无需手动配置密钥与路由。

当前为 `1.0.0-rc.1` 发布候选版，尚未完成正式发布验收。中继基础访问、LAN 转发和部分恢复场景已有实机记录；网络与配置已获用户实机确认，常规安装与多次覆盖升级已验证；卸载及故障注入专项未执行，详见 [验收状态](docs/release-acceptance.md)。

## 使用条件

- Apple Silicon Mac，macOS 13 或更新版本。
- fnOS NAS，已启用 FN Connect；服务端提供 x86_64 和 ARM64 安装包，ARM 实机验收尚未完成。
- 有效的 fnOS 用户账号。普通用户可以连接，安装应用与管理网络设置需要管理员。
- 当前原生登录不支持 fnOS 双重认证（2FA）。

当前 macOS App 与 helper 使用 ad-hoc 签名；PKG 未签名、未公证，安装时可能需要在系统设置中明确批准。FN Connect 中继速度取决于上游服务和网络条件。

RC 安装包见 [GitHub Releases](https://github.com/rectcircle/fn-connect-private-network/releases)。

RC 仅从 GitHub Releases 手动下载安装包，后续 RC 使用 PKG 覆盖安装；fnOS 通过应用中心更新 FPK。
覆盖安装保留当前受支持格式的身份与配置，旧 `0.x` 历史格式不承诺迁移。

Homebrew 仅分发稳定版，统一标识符为 `fncpn`；首个正式版发布后提供安装命令：

```bash
brew tap rectcircle/fn-connect-private-network https://github.com/rectcircle/fn-connect-private-network
brew install --cask rectcircle/fn-connect-private-network/fncpn
```

当前尚无稳定版 Cask。后续 RC 发布不会更新它，稳定版用户不会通过它收到 RC。
测试 RC 时直接覆盖安装 PKG；请勿用 `brew reinstall` 获取 RC，它会安装 Cask 指向的稳定版。
若已通过旧 `fncpn-rc` Cask 安装，请先通过 brew 卸载（默认保留用户配置与凭据），再手动安装 RC。

## 安装与首次连接

安装包按版本命名：

| 设备 | 安装包 |
| --- | --- |
| Apple Silicon Mac | `FnCPN-<version>-arm64-unsigned.pkg` |
| x86_64 fnOS | `fncpn-<version>-x86.fpk` |
| ARM64 fnOS | `fncpn-<version>-arm.fpk` |

1. 在 fnOS 应用中心安装对应的 FPK，打开 FnCPN，确认服务状态正常。
2. 在 Mac 安装 PKG，打开「FnCPN」。
3. 输入 FN Connect ID、fnOS 用户名和密码，完成登录。密码不会持久化保存。
4. 等待客户端自动注册设备并连接，在详情中查看 NAS 地址和当前连接方式。

默认私有网络为 `10.253.203.0/24`，NAS 地址为 `10.253.203.1`。管理员修改网段后，以界面显示为准。首次安装若提示网段或端口冲突，请在 fnOS 管理页修改配置并重试。

## 日常使用

- **局域网直达**：使用 NAS 的局域网地址访问。
- **IPv6 直连 / FN Connect 中继**：使用详情中的 NAS 私有网络地址，或已启用的远端 LAN 地址访问。
- **断开**：暂停自动连接；再次点击连接后恢复。网络变化和重启不会取消用户的暂停意图。
- **重新登录**：会话无法自动恢复时，重新输入密码；保留有效设备身份和配置。
- **退出登录**：断开并清除登录凭据，保留设备身份。
- **忘记此服务端**：清除本机配置、密钥和凭据，之后需要重新设置。

本地与远端 LAN 网段重叠时，不添加冲突的 LAN 路由，仅保留 NAS 私有网络地址访问，并显示提示。管理页的「删除离线设备」用于清理记录，不代表安全撤销账号或设备的访问权限。

客户端和 fnOS 管理页支持简体中文与英文，跟随系统或浏览器首选语言；修改语言后重启 App 或刷新页面。

## 连接失败时

先检查 FN Connect 是否可用、NAS 上 FnCPN 是否运行，再查看客户端错误提示。需要登录时重新登录；权限不足时检查账号权限；版本不兼容时按提示升级对应端。可使用客户端的诊断功能获取脱敏信息，排障方法见 [诊断指南](docs/diagnostics.md)。

安装后也可通过终端查询和控制连接：

```bash
fncpn status
fncpn connect
fncpn disconnect
fncpn retry
fncpn diagnose --json
```

## 更新与卸载

更新前查看 [变更记录](CHANGELOG.md)。正式兼容性承诺从 `1.0.0` 开始：两端 MAJOR 必须相同，客户端 MINOR 不能高于服务端，PATCH 不影响连接准入。`0.x` 测试版不承诺历史数据迁移；不支持的旧格式需先停止服务、清理网络并备份，再主动处理，更新不会自动清空用户数据。

fnOS 通过应用中心停止或卸载 FnCPN。Mac 可运行随包安装的卸载脚本，默认保留用户配置、用户日志和凭据，删除特权服务日志（`/var/log/fncpn`）：

```bash
sudo /Library/PrivilegedHelperTools/cn.rectcircle.fncpn/uninstall.sh
```

如需同时删除当前用户的数据（无法撤销），使用：

```bash
sudo /Library/PrivilegedHelperTools/cn.rectcircle.fncpn/uninstall.sh \
  --purge-user-data "$(id -u)"
```

## 项目资料与许可证

开发、协议研究、设计与发布资料见 [文档索引](docs/README.md)。源码采用 [MIT 许可证](LICENSE)；第三方依赖许可证见 [第三方声明](THIRD_PARTY_NOTICES.md)。fnOS 与 FN Connect 商标归各自权利人，本项目与其无隶属或背书关系。
