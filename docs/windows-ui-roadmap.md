# Windows 下一版本 UI 优化方向

决策日期：2026-10-10。状态：方案已确定，计划在下一个 Windows 版本实施；具体产品版本号待定。
本文记录后续开发方向，当前 WPF 实现与安装包尚未迁移。

## 已确定的方案

采用独立的 **C++ Win32 UI 程序**，登录、连接详情和托盘使用 Windows 原生标准控件；
管理后台继续在应用内嵌入 **WebView2**。UI 通过现有 Named Pipe IPC 与 Go daemon 通信，
Go daemon 继续通过已有受保护接口调用 Go Windows Service。

```text
C++ Win32 UI（普通用户独立进程）
  ├─ 原生登录、详情、诊断和托盘
  ├─ WebView2 管理后台
  └─ Named Pipe IPC → Go daemon → Go Windows Service
```

UI 与 Go 保持进程及协议边界：UI 不链接 Go 业务代码，不嵌入 Go runtime，不直接操作
隧道、路由、特权 journal 或凭据持久化。Go 保留认证、连接协调、网络、数据与诊断逻辑。
本次优化替换 Windows UI，保留既有功能和权限边界。

## 目的与取舍

当前 WPF/.NET 10 自包含方案同时携带 WPF 和用于托盘的 WinForms 运行环境。
当前 ARM64 候选 MSI 约 76.4 MiB（约 80 MB 十进制），payload 约 202.5 MiB；
这些是现有候选的测量基线，不是所有版本的固定体积。

新方案移除 .NET 桌面运行环境，减少安装包、安装占用和常驻 UI 的开销。
保留原生控件的系统外观，以维护简单表单和状态界面的成本为依据选择 Win32。
布局、DPI、消息循环和异步更新由 UI 实现负责；避免大量自绘控件。
具体体积、内存和启动时间收益必须实测，暂不承诺数值。

## WebView2 处理

管理后台保持应用内打开。UI 从现有 `admin-proxy` IPC 获取代理地址，继续执行地址校验、
同源导航限制、弹窗和外链处理、登录过期提示及返回详情操作。
Go 管理代理继续负责后台请求，不将认证与代理业务迁移到 C++。

使用共享 Evergreen WebView2 Runtime，不在 MSI 内携带固定版本浏览器本体。
安装器检测 Runtime 并按需安装；复用现有引导器方案，验证联网、无网和安装失败行为。
卸载 FnCPN 保留共享 Runtime。

原生 WebView2 集成的评估成本为中等，主要包括 COM 与异步初始化、窗口尺寸同步、
导航事件和销毁顺序。常规登录与详情页不依赖 WebView2，应验证管理视图按需初始化及资源释放。
原生 API 封装、构建工具和 C++ runtime 的部署方式在实施时确定，并纳入实际依赖清单。

## 系统支持目标

下一版本拟验收 Windows 10 22H2 x64，以及 Windows 11 x64/ARM64。
Windows 10 ARM64 单独评估，不作为当前已确定的支持承诺。
最终支持范围以 Go、Wintun、WebView2、所用 Windows API 和真实安装验收共同确定。
技术可运行与依赖厂商支持周期分别记录。

Windows 11 的现有安装限制是当前版本的策略，不能作为新 UI 的天然限制。
扩大支持前核对全部依赖和 API，并用 Windows Installer 实际评估准入条件：
兼容版本属性不用于推断 Windows 10/11，按真实系统 build 和架构验证；保留维护操作入口。

## 迁移范围与验收

| 范围 | 要求 |
|---|---|
| 登录和授权 | 保留字段校验、具体错误、授权取消、CLI 授权等待、切换目标及重新登录行为 |
| 连接与详情 | 保留连接/断开/重试、LOCAL/DIRECT/RELAY、权限等待、后台忙碌状态和诊断 |
| 托盘与生命周期 | 保留鼠标/键盘菜单、自启动、关闭窗口驻留、显式退出和 Explorer 重启后恢复 |
| 单实例与激活 | 保留用户/session 隔离、URI 转交、启动 daemon 收敛和调用方身份校验 |
| 管理后台 | 保留内嵌 WebView2、导航边界、弹窗/外链策略、认证失效及返回详情 |
| 系统集成 | 保留复制诊断、打开日志/NAS、服务修复和 UAC 取消处理 |
| 可访问性与布局 | 验证中英文长文本、100%/150%/200% DPI、跨屏缩放、Tab、Narrator 和高对比度 |
| 安装与升级 | 验证旧 WPF 到新 UI 的升级、进程退出、协议/PATH、自启动、修复、回滚与卸载 |

实施顺序：先完成原生登录/详情/托盘与 IPC 的最小可运行版本，再迁移内嵌管理后台，
最后切换 MSI payload、开发环境工具与 UI 自动化验收。不得仅凭新界面能启动就删除旧行为验证。
对照旧候选记录安装包大小、完整安装占用、首次/后续启动时间、托盘常驻内存和管理后台打开后的开销。
Windows 10 x64、Windows 11 原生 x64 和原生 ARM64 分别验收；仿真结果单独标注。

## 版本与数据约束

遵守 [版本管理与兼容性](versioning-and-compatibility.md)。UI 实现替换本身不要求升级 MAJOR，
最终版本增量按实际行为变化确定；Windows、macOS、fnOS 和 Go CLI 保持统一产品版本。
本地组件成套升级，IPC 和数据 schema 继续独立版本化。
保留身份、配置、凭据和连接意图；升级默认保留用户数据，回滚按配套程序及数据备份恢复。
远程版本准入规则保持不变，不因 UI 迁移要求另一端更新 PATCH。

## 技术参考

- [Win32 对话框与标准控件](https://learn.microsoft.com/en-us/windows/win32/dlgbox/about-dialog-boxes)
- [原生托盘示例](https://learn.microsoft.com/en-us/windows/win32/shell/samples-notificationicon)
- [Win32 WebView2 官方入门](https://learn.microsoft.com/en-us/microsoft-edge/webview2/gettingstarted)
- [高 DPI 开发](https://learn.microsoft.com/en-us/windows/win32/hidpi/high-dpi-desktop-application-development-on-windows)
- [UI Automation 标准控件代理](https://learn.microsoft.com/en-us/windows/win32/winauto/uiauto-clientsideprovider)
