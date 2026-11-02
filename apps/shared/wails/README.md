# Wails 客户端

本目录是独立 Go module（保留 `github.com/rectcircle/fn-connect-private-network/ui` module path），使用 Wails v2 与原生 HTML/CSS/JavaScript，服务于 macOS 可选 UI 和 Linux UI。通过现有 IPC 调用 Go 核心，平台窗口、托盘和管理 WebView 适配位于 `native_*` 文件，前端位于 `frontend/`。

- [macOS 构建与开发](../../../docs/development.md#macos-wails-ui)
- [Linux 构建与部署](../../../docs/development.md#linux-构建与部署)
- [测试检查](../../../docs/development.md#测试)
- [平台决策](../../../docs/platform-support.md)与[验证记录](../../../docs/validation.md)

## 设置与网络链路测试（1.2.0）

主窗口的“设置”和菜单入口打开默认通用的本地设置页；支持版本/commit/下载入口、各 NAS 的链路排序与开关、
链路多选、测试前中断确认、实时方向动画与逐项指标，以及整轮完成后的结果表。局域网始终优先。
桌面自启复用平台实现，手机不显示；服务未启动时仅展示通用信息及连接提示，不因打开设置请求 VPN 授权。
关闭设置页面会取消该次测速并恢复正常选路。页面由客户端本机提供，测速仍使用 NAS IP 的私网 API。
协议及本地页面权限见 [接口契约](../../../docs/interfaces.md)。
