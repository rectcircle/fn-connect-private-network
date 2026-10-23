# Fyne 移动界面

独立 Go module，使用 Fyne UI、Java VpnService 和共享 Go 连接宿主。Android 系统适配位于 `internal/androidruntime`、`internal/androidvpn`，Gradle 工程位于 `android/`。

- [开发、测试、Android 构建与签名](../docs/development.md#android-开发与构建)
- [提交后的签名开发包 CI](../docs/development.md#android-cidebug-构建)
- [方案结论与待验收范围](../docs/platform-support.md#android)

早期 Android CI 与后续本地构建已通过；1.1.4 草稿新增正式 Release APK，云端与最终产物仍待验收。专项实机结果与尚待验收项见[验证记录](../docs/validation.md)。
