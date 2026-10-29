# Windows 原生客户端

本目录包含 Win32/WebView2 原生 UI、CMake 工程、manifest、版本资源与本地化；
`packaging/` 包含 MSI 中英文语言资源。共享 Go 系统适配位于 `internal/platform/windows`，
安装维护实现位于 `internal/windowsinstall`，共享图标暂由 `assets/branding` 提供。

- [环境、构建与检查](../../docs/development.md#windows-开发与构建)
- [布局与依赖约束](../../docs/project-layout.md)
- [安装维护及版本契约](../../docs/versioning-and-compatibility.md)

自动化入口保持 `scripts/setup-windows.ps1`、`scripts/build-windows.ps1` 与
`scripts/check-windows.ps1`，辅助实现位于 `scripts/windows/`。
CMake 配置使用 `--fresh`，避免迁移前缓存绑定旧源码目录；开发与交付输出路径保持原状。
本地化修改 `messages.json` 后运行 `scripts/sync-native-localizations.py` 生成 `Localization.h`。
