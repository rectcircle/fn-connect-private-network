# FnCPN Wails v2 客户端（macOS / Linux）

本目录使用官方 **Wails v2.17.0 CLI** 的 vanilla 模板创建：

```sh
# 在已有失败目录清理前，实际执行于空的 ui/wails-v2，再移至 ui/
wails init -n fncpn-ui -t vanilla -d ui/wails-v2
```

前端只有原生 HTML、CSS、JavaScript。Vite 仅用于打包，不引入 React、Vue、Svelte
或任何组件框架。Wails 自动生成的 JS/类型绑定位于 `frontend/wailsjs/`。

## 目录

```text
ui/
  main.go                 Wails 窗口、菜单、单实例和 URL scheme
  app.go                  现有 Go IPC client 的白名单桥接、daemon 生命周期
  native_darwin.go/.m      macOS 菜单栏、独立 WKWebView、自启、通知
  native_linux.go/.c       GTK/Ayatana 托盘、独立 WebKit 窗口、XDG 自启
  lifecycle_*.go          各平台 Core 服务启动与日志路径
  app_test.go             真实 Unix socket 帧的桥接与版本校验测试
  frontend/index.html     原生页面与表单
  frontend/src/           状态呈现、交互、CSS、原生应用的英文文案
  frontend/wailsjs/       Wails 生成的桥接代码
  build/darwin/           CLI 生成并定制的 macOS bundle 模板
  build/bin/              忽略 Git 的构建产物
  go.mod                  独立 UI module，replace .. 复用同一源码的 Core
  wails.json              Wails v2 项目配置
```

## 构建

依赖 Go（与根模块一致）、Node/npm、Xcode Command Line Tools、macOS 13+。

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
# 从仓库根目录构建当前架构 App
scripts/build-wails-macos.sh
# 统一安装包入口：默认 native，显式选择 Wails
scripts/build-macos-pkg.sh --ui wails
# 等价的便捷入口
scripts/build-wails-macos.sh --pkg
# Intel 构建
ARCH=x64 scripts/build-wails-macos.sh --pkg
```

App：`dist/macos-ui/wails/<架构>/FnCPN.app`。
原生 App 可通过 `scripts/build-native-macos.sh` 构建，输出到
`dist/macos-ui/native/<架构>/FnCPN.app`。两个构建器均支持 `--output /绝对路径/FnCPN.app`，
输出必须不存在；默认开发输出允许重复构建。`ui/build/bin/` 是 Wails CLI 中间产物。
安装包：`dist/wails-v2/fncpn-<产品版本>-macos-<架构>-unsigned.pkg`。
产物使用本地 ad-hoc 签名，尚未公证。
两种 UI 共用应用标识 `cn.rectcircle.fncpn`、URL scheme 和产品版本；PKG 都安装至
`/Applications/FnCPN.app`，用于相互替换，不能作为两个独立产品并行安装。
`build-macos-pkg.sh` 不带参数等价于 `--ui native`，默认输出目录仍为 `dist/`。
Wails 默认 PKG 目录为 `dist/wails-v2/`；显式设置 `DIST_DIR` 可覆盖两者的输出目录。

直接运行 App 需要当前用户可访问的特权服务已安装；它会启动同源码构建并随 App
携带的普通用户 daemon。首次安装应使用完整 PKG。PKG 使用现有原生安装器，安装到
`/Applications/FnCPN.app` 并配套更新 Core 与特权组件，使用相同用户数据及卸载入口。
不要同时运行原生和 Wails 两个 UI：两者共享 socket、身份及连接意图。
开发 App 发现已有 daemon 时只复用它，退出不终止别的 UI 启动的服务；若版本不匹配，
现有 IPC client 会明确拒绝通信，不放宽产品版本或 IPC 校验。

只开发前端或调试绑定时可在 `ui/` 运行 `wails dev`，先通过完整 App/安装器准备 daemon。

## 原生功能对照

- 原生登录：FN Connect ID/链接规范化、访问码探测、用户名密码、OTP、信任设备、
  多因子挑战复用授权 request ID、取消授权、更换 NAS、重新登录、返回连接详情。
- 连接/断开/重试、等待局域网权限时继续远程连接；事件长轮询和定期诊断更新。
- 显示连接路径、隧道地址、接口/MTU、握手、DNS、特权服务、IPv6、NAS IP 和诊断。
- 复制 DNS/诊断、打开 NAS、日志、官网；系统语言选择简体中文或英文。
- 菜单栏状态图标与连接动作、关闭窗口留驻、SMAppService 登录自启、单实例、
  `fncpn://authorize/...` 授权唤起、特权服务不可用通知。
- 管理后台通过现有 `admin-proxy` IPC 在独立 WKWebView 打开。仅接受 daemon 返回的
  loopback HTTP 地址；远程内容不加载 Wails bindings。
- macOS 自己启动的 daemon 在退出时正常 shutdown/bootout，保留自动连接意图；
  Linux 的明确“退出”会等待用户 Core 服务停止、清理连接后关闭 UI，同样保留连接意图。
  停止失败时保留 UI 并显示错误；关闭窗口只隐藏窗口，不停止连接。显式“断开”会
  清除自动连接意图。托盘“打开”恢复最小化窗口并通过原生窗口 API 请求前台激活。

macOS 专用桥接仅承接系统能力，界面和业务交互仍由 Wails v2 + vanilla 前端实现。
Linux 的服务、自启和托盘适配已在 Debian 13 arm64 安装验证；各包族原生构建和
多发行版前端启动由草稿 CI 验证。此 UI 不替代现有 Windows 原生客户端。

## 验证

```sh
(cd ui && MACOSX_DEPLOYMENT_TARGET=13.0 go test -race ./...)
(cd ui/frontend && npm ci && npm test && npm run build)
bash -n scripts/build-native-macos.sh scripts/build-wails-macos.sh scripts/build-macos-pkg.sh scripts/macos-app-common.sh
```

自动测试覆盖真实 IPC 编解码、多因子错误、组件版本不匹配、管理后台地址边界，以及
与原生一致的状态/权限呈现。完整安装器同时执行根项目测试和已有包结构检查。
真实 NAS 的用户名密码/访问码/OTP 登录、自启登录会话、网络切换恢复和实际升级/卸载，
需要在对应环境继续实机验收；构建通过不替代这些结论。

本次本机验收（2026-10-21，Apple Silicon）：App 启动后已读取实际 daemon 的局域网连接、
DNS、特权服务及 NAS 地址，验证了更换 NAS/返回详情和窗口关闭留驻、正常退出。
管理后台入口收到现有 fnOS 会话过期错误，验证了转入重新登录且连接保持；尚未完成
有效管理会话下的页面加载验收。未执行实际安装或卸载，也未修改登录自启设置。

## macOS 阶段的实验残留清理记录

上一阶段曾将以下失败实验变更恢复到当时 HEAD（Debian 13 本阶段已重新实现相关文件）：根 `go.mod` / `go.sum`、`.github/workflows/release.yml`、
`internal/command/daemon_unix.go`、`internal/command/defaults_unix.go`、
`internal/platform/engine_linux.go`。删除未纳入 Git 且无法编译的 Linux 客户端草稿：
`client_engine.go`、`device_linux.go`、`dns_linux.go`、`firewall_linux.go`。
这些草稿包含与既有 Linux server 类型重复的定义；发布流程引用不存在的 Linux 构建脚本，
同时遗漏 shell 的 `fi`；macOS daemon 草稿使用错误的 LaunchDaemon 路径。
恢复后已重新验证 Linux CLI 可以交叉编译。

上一阶段 `ui/` 之外保留的改动：`scripts/build-wails-macos.sh`、
`build-macos-pkg.sh` 的 Wails 构建入口（现已改为正式 `--ui` 选项）、`sync-version.py` 的 UI 产品版本同步，
以及 `docs/linux-support.md` 方案文档。Wails 依赖只在 UI module 中管理。

界面按原生参考图的 494 × 668 pt 窗口校准，正文内边距 28 pt、诊断区 80 pt，
普通按钮和工具按钮使用原生应用同名 SF Symbols。保留系统窗口控制按钮，
Wails 自定义标题区域负责居中标题和窗口拖动；SDK 27 的系统窗口控制仍由 macOS 绘制。
[Apple 关于新系统外观的说明](https://developer.apple.com/documentation/technologyoverviews/adopting-liquid-glass)。

## Linux

Linux 使用同一套原生 HTML/CSS/JavaScript 和 IPC 桥接，原生适配使用 GTK3、
WebKitGTK 4.1 与 Ayatana AppIndicator。Core 独立于 UI 进程运行；关闭窗口隐藏到托盘，明确“退出”会停止用户 Core 并清理连接。
管理后台使用独立、不含 Go 绑定的 WebKit 窗口。首次启动使用 pkexec 授权对应用户的特权服务。

构建在 `packaging/linux/targets.json` 对应发行版与架构环境中运行：
`TARGET=deb ARCH=amd64 scripts/build-linux-ui.sh`，先构建同目标、同版本 Core。
macOS 可交叉编译 Core，不能直接链接 Linux 图形依赖。
CI、deb/RPM/Arch 双包、系统路径、运行依赖和验证边界见 [Linux 支持](../docs/linux-support.md)。
已完成 Linux UI 的原生 CI 编译、21 组目标容器的前端启动检查及 Debian 13 arm64
草稿 deb 安装验证；真实桌面与用户测试范围见 [验证记录](../docs/validation.md)。
主页操作错误统一显示在诊断详情中，异常诊断使用红色；登录错误保留在登录表单。

### Linux 托盘窗口激活

GNOME Wayland 会为托盘点击提供一次性激活令牌。旧版 GTK AppIndicator 不处理
`ProvideXdgActivationToken`，且其 GtkMenuItem 回调不保留 D-Bus 菜单点击时间戳。
`activation_linux.c` 在 AppIndicator 共用的会话总线上接收这一标准方法，并观察
DBusMenu 的原始点击时间戳。实际置前仍由 GTK 主线程完成：Wayland 将令牌交给
GDK 后调用 present；X11 使用启动通知与原始时间戳。令牌最多保留五秒且只消费一次。
不会设置永久置顶，也不修改桌面的焦点策略。

`scripts/test-linux-activation.sh` 用独立 D-Bus 会话测试实际消息接收、时间戳传递、
错误对象路径及一次性消费；完整 Linux 构建在各包族基线中运行该测试。
