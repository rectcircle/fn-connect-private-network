# 脚本入口

Windows 日常开发只需要三个入口：

| 脚本 | 用途 |
|---|---|
| `setup-windows.ps1` | 准备工具和依赖；默认编译验收，`-SkipSmoke` 只跳过编译；Node/GitHub CLI 分别通过 `-IncludeServerTools` / `-IncludeReleaseTools` 选择 |
| `build-windows.ps1` | 构建 ARM64/x64 原生 UI、Go 和 MSI；支持 `-Architecture`、`-OutputRoot` |
| `check-windows.ps1` | Go test/vet、生成资源、setup 回归及已有候选检查；支持 `-SkipPackage`、`-Architecture`、`-PackageRoot` |

需要 PowerShell 7 的入口声明了 `#Requires`；setup 可从 Windows PowerShell 5.1 启动。
详细依赖、产物目录和验证范围见 [Windows 开发](../docs/windows-development.md)。
`windows/` 是实现与测试辅助，由上述入口调用。迁移期间的 `*-native.ps1` 名称已统一，旧 WPF 实现没有保留兼容包装。

其他脚本服务于不同平台和发布流程：

| 组 | 脚本 | 用途 |
|---|---|---|
| macOS/fnOS 构建 | `build-all.sh`、`build-macos-pkg.sh`、`build-fpk.sh`、`install-fnpack.sh` | 平台产物与 fnpack 环境 |
| 共享检查 | `check-release.sh`、`test-fnos-state-permissions.sh` | 跨平台/服务端验收 |
| 生成资源 | `sync-version.py`、`sync-localizations.py`、`sync-native-localizations.py`、`generate-icons.swift` | 统一版本、翻译与产品图标 |
| 发布 | `release-version.sh`、`release-manifest.py`、`publish-release.py`、`update-cask.py` | 版本、资产清单、发布和 Homebrew 更新 |
| 发布回归 | `release_manifest_test.py`、`publish_release_test.py`、`update_cask_test.py` | 发布操作的自动验证 |
| 设计预览 | `preview-icon-concepts.swift`、`preview-private-network-icon.swift` | 图标方案预览，不参与 Windows setup |

不应将共享或其他平台脚本当成 Windows 迁移残留删除。新增 Windows 内部辅助放到 `windows/`，优先接入统一入口，不再增加平行的常用入口。
