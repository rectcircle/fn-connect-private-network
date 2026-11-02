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

## 设置与网络链路测试（1.2.0）

主窗口的“设置”和菜单入口打开默认通用的本地设置页；支持版本/commit/下载入口、各 NAS 的链路排序与开关、
链路多选、测试前中断确认、实时方向动画与逐项指标，以及整轮完成后的结果表。局域网始终优先。
桌面自启复用平台实现，手机不显示；服务未启动时仅展示通用信息及连接提示，不因打开设置请求 VPN 授权。
关闭设置页面会取消该次测速并恢复正常选路。页面由客户端本机提供，测速仍使用 NAS IP 的私网 API。
协议及本地页面权限见 [接口契约](../../docs/interfaces.md)。
