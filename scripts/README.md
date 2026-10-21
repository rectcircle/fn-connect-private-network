# 脚本入口

Windows 日常开发只需要三个入口：

| 脚本 | 用途 |
|---|---|
| `setup-windows.ps1` | 准备工具和依赖；默认编译验收，`-SkipSmoke` 只跳过编译；Node/GitHub CLI 分别通过 `-IncludeServerTools` / `-IncludeReleaseTools` 选择 |
| `build-windows.ps1` | 构建 ARM64/x64 原生 UI、Go 和 MSI；支持 `-Architecture`、`-OutputRoot` |
| `check-windows.ps1` | Go test/vet、生成资源、setup 回归、安装维护历史/未来兼容及已有候选检查；支持 `-SkipPackage`、`-Architecture`、`-PackageRoot` |

需要 PowerShell 7 的入口声明了 `#Requires`；setup 可从 Windows PowerShell 5.1 启动。
详细依赖、产物目录和验证范围见 [Windows 开发](../docs/windows-development.md)。
`windows/` 是实现与测试辅助，由上述入口调用。迁移期间的 `*-native.ps1` 名称已统一，旧 WPF 实现没有保留兼容包装。

其他脚本服务于不同平台和发布流程：

| 组 | 脚本 | 用途 |
|---|---|---|
| macOS UI | `build-native-macos.sh`、`build-wails-macos.sh` | 生成完整 App；`macos-app-common.sh` 为共享资源与签名实现，PKG 入口用 `--ui native|wails` 选择，默认 native |
| macOS/fnOS 构建 | `build-all.sh`、`build-macos-pkg.sh`、`build-fpk.sh`、`install-fnpack.sh` | 平台产物与 fnpack 环境 |
| Linux 安装 | `install-linux.sh` | 自动检测发行版/桌面，从 GitHub latest 或指定版本安装全部/Core；`install_linux_test.py` 为无安装副作用的回归测试 |
| Linux | `build-linux-core.sh`、`build-linux-ui.sh`、`test-debian13.sh` | Core 交叉构建、基线容器原生 UI 双包构建、多发行版共用包验证、远端预编译测试；见 [Linux 支持](../docs/linux-support.md) |
| RPM 签名 | `rpm_signing.py`、`rpm_signing_test.py` | 临时 CI 密钥签名、固定项目公钥验签、未签名/篡改/错误公钥回归；见 [密钥维护](../docs/rpm-signing.md) |
| Linux 草稿验证 | `linux-compatibility.py`、`linux_abi.py`、`linux-ui-smoke.py`、`test-linux-activation.sh` | 包事务、目标库版本符号、真实 WebKit 前端启动及托盘激活 D-Bus 测试；完整矩阵仅草稿构建执行 |
| 共享检查 | `check-release.sh`、`test-fnos-state-permissions.sh` | 跨平台/服务端验收 |
| 生成资源 | `sync-version.py`、`sync-localizations.py`、`sync-native-localizations.py`、`generate-icons.swift` | 统一版本、翻译与产品图标 |
| 发布 | `release-version.sh`、`release-manifest.py`、`publish-release.py`、`quark-release.py`、`update-cask.py` | 版本、资产清单、发布和 Homebrew 更新 |
| 发布回归 | `release_manifest_test.py`、`publish_release_test.py`、`quark_release_test.py`、`update_cask_test.py` | 发布操作的自动验证 |
| 设计预览 | `preview-icon-concepts.swift`、`preview-private-network-icon.swift` | 图标方案预览，不参与 Windows setup |

不应将共享或其他平台脚本当成 Windows 迁移残留删除。新增 Windows 内部辅助放到 `windows/`，优先接入统一入口，不再增加平行的常用入口。

Nix 源码构建、模块配置和依赖 hash 更新见 [Nix 支持](../docs/nix.md)。
