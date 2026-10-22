# Wails 客户端

本目录是独立 Go module，使用 Wails v2 与原生 HTML/CSS/JavaScript，服务于 macOS 可选 UI 和 Linux UI。通过现有 IPC 调用 Go 核心，平台窗口、托盘和管理 WebView 适配位于 `native_*` 文件，前端位于 `frontend/`。

- [macOS 构建与开发](../docs/development.md#macos-wails-ui)
- [Linux 构建与部署](../docs/development.md#linux-构建与部署)
- [测试检查](../docs/development.md#测试)
- [平台决策](../docs/platform-support.md)与[验证记录](../docs/validation.md)
