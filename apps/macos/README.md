# macos 客户端

Swift/AppKit 原生 UI 与 packaging/ 交付配置；可选 Wails 应用在 apps/shared/wails。

- [开发与构建](../../docs/development.md)
- [布局与依赖约束](../../docs/project-layout.md)
- [版本与升级契约](../../docs/versioning-and-compatibility.md)
- [发布验收](../../docs/validation.md)

## 链路设置与测速（1.2.0）

应用菜单 / 菜单栏的「设置…」（⌘,）和主窗口的设置按钮打开同一设置窗口。
网络链路页可拖动 IPv6、FN Connect 行排序并分别禁用，local 始终第一且不可禁用。
主窗口、设置和管理窗口打开或最小化时显示程序坞图标，全部关闭后隐藏图标，菜单栏服务继续运行。
链路选择使用保持展开的复选列表；开始或重新测试前确认短暂中断及结果影响因素。优先级说明悬停即显示。
测速使用当前 NAS IP 的私网 API，临时切换真实隧道，可能影响业务连接；完成或取消后恢复正常选路。

配套 CLI：

```bash
fncpn link list
fncpn link priority fn-connect ipv6
fncpn link disable ipv6
fncpn link enable ipv6
fncpn link reset
fncpn link speedtest --link ipv6
fncpn link speedtest --all --json  # NDJSON 实时进度及逐项结果
```

App 和 CLI 分别发起一次流式调用，关闭设置窗口或 CLI 按 Ctrl-C 会取消网络请求，并恢复正常选路。服务端没有独占测速会话。
结果中的上传速率来自 NAS 接收统计，下载来自 Mac 接收统计。接口及测量定义见 [接口契约](../../docs/interfaces.md)。
