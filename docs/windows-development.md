# Windows 命令行开发环境

Windows UI 使用原生 C++ Win32，构建、检查和发布统一走原生链路。

原生 UI 可独立编译，以当前 Windows SDK 的 MSVC 工具链生成 ARM64 或 x64：

```powershell
cmake -S platform/windows -B .dev/windows-ui-arm64 -G "Visual Studio 17 2022" -A ARM64
cmake --build .dev/windows-ui-arm64 --config Release
cmake -S platform/windows -B .dev/windows-ui-x64 -G "Visual Studio 17 2022" -A x64
cmake --build .dev/windows-ui-x64 --config Release
```

这只构建独立原生 UI，不生成 MSI。首次构建前执行 `dotnet restore platform/windows/WebView2SDK.csproj`，还原固定版本原生 SDK；Evergreen Runtime 不会复制进 UI 或安装包。

当前候选安装准入已放宽为 Windows 10 22H2（build 19045）及以上的 x64/ARM64
客户端系统；Windows Server 和 32 位系统不在范围内。安装条件已由 Windows Installer
回归验证，但 Windows 10 的实际登录、Service、Wintun、WebView2 和升级仍待实机验收。
.NET SDK 仅用于构建工具（WiX 与 NuGet restore），原生 UI 和 MSI payload 不携带或依赖 .NET 桌面运行时。安装准入不等于完整实机验收。

在仓库根目录运行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/setup-windows.ps1
```

`Bypass` 仅作用于本次进程，不修改用户/机器执行策略。脚本支持 Windows ARM64 和 x64，
无需管理员权限即可安装通用工具；仅当缺少 Windows SDK 等系统前置组件时，WinGet
可能请求 UAC。不安装 FnCPN Service，不创建网卡，不登录 GitHub，不自动重启。

脚本按顺序检查实际工具，再安装缺失项。已安装的可用 .NET 10 SDK、Go >= 1.27、
独立 PowerShell 保留；GitHub CLI 在指定 `-IncludeReleaseTools` 时准备；新安装的版本与下载哈希固定在脚本中。
Git/Python、CMake >= 3.27、MSVC 2022 x64/ARM64、Windows SDK 和 WebView2 已存在则跳过，缺少时通过 WinGet 安装。
Node 仅在指定 `-IncludeServerTools`（服务端网页测试）时准备；默认 Windows 构建不需要 Node 或 GitHub CLI。
WiX 及其 UI/Util 扩展必须为项目指定的 7.0.0，Wintun 为官方已签名 0.14.1。
下载先校验，解压到暂存目录后重命名提交；异常目录不覆盖，修复原因后可重跑。

| 内容 | 位置 |
|---|---|
| .NET SDK | `%LOCALAPPDATA%\Microsoft\dotnet` |
| Go | `%LOCALAPPDATA%\Programs\Go` |
| GitHub CLI | `%LOCALAPPDATA%\Programs\GitHub CLI` |
| PowerShell | `%LOCALAPPDATA%\Programs\PowerShell\7` |
| WiX | `.dev/tools/wix` |
| Wintun 与原始许可证 | `.dev/dependencies/wintun/0.14.1` |
| WebView2 Evergreen Bootstrapper | `.dev/dependencies/webview2` |
| 下载缓存、日志、临时源码 | `.dev/downloads`、`.dev/logs`、`.dev/smoke` |
| 验证构建产物 | `dist/smoke` |

仅通用工具目录加入用户 PATH；项目 `.dev` 不进入 PATH。设置用户 `DOTNET_ROOT`，
不设置 `GOROOT`、不改变已有 `GOPATH`。交叉构建临时使用的 `GOOS/GOARCH/CGO_ENABLED`
执行后恢复。脚本同步当前环境并验证干净新进程，不依赖系统重启或重新打开终端才能重跑。
已有终端/IDE 请重新打开以获得新 PATH；缺失系统组件的安装器如报告重启要求，脚本会提示。

Go 安装后自动探测默认模块代理：请求仓库当前 nftables、netlink、genetlink 的
`.mod` 文件，每个请求超时 15 秒，不依赖本地模块缓存。默认代理不可用时，先验证
`https://goproxy.cn`（中国大陆常用镜像），再尝试 `https://goproxy.io`；使用首个
可用镜像，通过 `go env -w` 保存对应的 `GOPROXY=<镜像地址>,direct`，
并同步当前进程的显式 GOPROXY；所有代理均失败则停止且不修改配置。
已有自定义 GOPROXY 保留。随后执行 `go mod download` 下载完整依赖（包含 Linux
专属模块），保留 Go 的 go.sum / checksum database 校验设置；`-SkipSmoke` 也执行此步骤。
保存的代理对后续 Go 命令生效，可用 `go env -u GOPROXY` 恢复默认；显式环境变量
优先于 Go 保存的配置，如设置过 GOPROXY 环境变量，还需自行调整或移除。
探测分支回归可运行 `powershell.exe -NoProfile -File scripts/windows/test-setup-proxy.ps1`。

默认构建包含原生 Win32/WebView2 双架构 UI、Go 双架构编译和 WiX
双架构 MSI 构建，但不安装验证 MSI。可用 `-SkipSmoke` 跳过编译验收，仅准备工具及还原 WebView2 原生 SDK。
默认验收还会运行本机支持架构的 CTest；x64 主机只编译 ARM64 测试，不执行它。
验证程序不是产品版本，不修改 FnCPN 版本、业务数据或协议。
最终产品的驱动、网络和升级实机验收另行执行；Windows 发布计划为 GitHub Releases，
无 WinGet 应用分发、无受信证书要求。

## 构建 Windows 产品候选

准备完成后，在仓库根目录运行：

```powershell
./scripts/build-windows.ps1
```

脚本按 ARM64、x64 顺序构建原生 C++ UI 到 `ui/` 子目录，再构建根目录 Go 单一 CLI/daemon/Service，
加入同架构、官方 Authenticode 有效的 Wintun DLL 与许可证，最后生成两个 WiX MSI。分开放置 UI 与 Go
是为了避开 Windows 文件名大小写不敏感导致的 `FnCPN.UI.exe`/`fncpn.exe` 覆盖。
每个架构也可单独构建：

```powershell
./scripts/build-windows.ps1 -Architecture arm64
./scripts/build-windows.ps1 -Architecture x64
```

产物在 `dist/windows-native/arm64/` 与 `dist/windows-native/x64/`，包括未签名 MSI、解包 payload 和
`build-manifest.json`。清单记录源 commit、dirty 状态、源码指纹、架构、UTC 构建时间和 MSI SHA256。
产品版本使用 `MAJOR.MINOR.PATCH`，MSI ProductVersion 与产品版本一致。未公开本地候选或发布草稿可重建替换；公开版本及资产不可覆盖，修复递增 PATCH。
构建会安全拒绝覆盖目录中未登记的文件。当前 Windows 开发机可交叉构建两种架构，但安装、Service、
Wintun 路由与升级验收仍需对应 Windows 实机；初始 GitHub 手动下载版本允许未签名，
公开版本必须使用同一发布提交的干净构建，不直接上传本地 dirty 候选。
MSI 内嵌微软 Authenticode 签名的 Evergreen Bootstrapper；机器级首次安装时以静默方式调用它，
仅在需要时安装共享 WebView2 Runtime，并自动匹配设备架构。缺少 Runtime 时首次安装需要联网；
bootstrapper 安装失败会令 MSI 安装失败。共享 Runtime 不会随 FnCPN 卸载。

正常卸载保留用户配置、DPAPI 凭据和日志。只有显式传入清理属性才会删除发起卸载用户的
`%LOCALAPPDATA%\FnCPN`：

```powershell
msiexec.exe /x .\fncpn-1.0.4-windows-arm64-unsigned.msi PURGE_CURRENT_USER_DATA=1
```

该动作先请求停止当前用户的 Go daemon，再清理该用户的数据和日志；不会遍历或清理其他
Windows 用户 profile，也不会删除 `%ProgramData%` 下的 Service recovery journal 或共享
WebView2 Runtime。ARM64 普通安装、候选替换、卸载后 CLI/协议/PATH 清理与重装配置保留
已经实测通过；多用户、完整网络清理与异常安装事务仍待验收。

Go Windows Service 在启动时幂等设置 SCM 故障恢复：首次失败 5 秒后重启，后续失败按 30 秒、
60 秒重试，并对非崩溃失败启用恢复。安装器已实现跨会话 daemon 停止；多用户会话下的
停止行为与安装中断回滚仍需在隔离环境完成异常场景实机验收。

## 发布前置步骤

1. 整理并审阅源码、CI、文档和版本资源，提交全部发布输入。版本以 `internal/version/VERSION` 为准；
   确认该编号尚未公开，公开后只能递增 PATCH。公开前必须检查版本元数据和同提交产物。
2. 在准备提交上运行 Windows Go 测试/vet、本地化与版本同步检查、代理设置回归和双架构
   构建。macOS/fnOS 的测试与发布检查由相应 runner 执行；Windows 无法替代这些平台验收。
3. 从发布提交创建对应 tag；Release workflow 先构建 draft。Windows runner 安装固定 WiX
   UI/Util 扩展与官方依赖，验证 tag/版本、Go 和资源，再构建两种 MSI；macOS runner
   执行共享与平台检查，构建 PKG/FPK，并生成全平台清单。
4. 检查 draft 的五个安装包（macOS PKG、两种 fnOS FPK、两种 Windows MSI）、发布 JSON
   与 SHA256SUMS。下载并核验实际资产；对 CI 生成的 ARM64 MSI 补一次安装/登录/连接抽验。
   本地开发 MSI 的验证不等同于 hosted runner 产物验收。
5. 发布说明如实披露 Windows 11 ARM64 已验收、原生 x64/Windows 10 与异常场景待验证、
   未签名及缺 Runtime 时需联网。版本公开后由发布流水线更新 Homebrew Cask。
6. 完成 draft 验证后，使用同 tag 的 Release workflow 并设置 `publish=true` 公开；公开
   阶段复验并发布原 draft 字节，不用重建产物替换已经验收的 draft。

发布必须满足 [版本与兼容性](versioning-and-compatibility.md)。实际 hosted CI、历史版本
兼容验证与发布资产验收未完成时，应在发布说明中明确尚未验证的范围。

WiX 7 具有 Open Source Maintenance Fee 条款，使用者应核对官方适用条件：
https://docs.firegiant.com/wix/osmf/ 。构建验证通过 `-acceptEula wix7` 接受该 EULA，
不创建全局接受文件，不购买订阅或凭据。


源码指纹排除本开发环境说明文档（`docs/windows-development.md`）及 `.github/workflows/` 下的 CI 定义；这些文件不改变安装包构建输入。manifest 的 dirty 状态仍包含这些文件的修改。

## 原生 UI 开发与验证

Windows UI 源码位于 `platform/windows/`，使用 `setup-windows.ps1`、`build-windows.ps1` 和 `check-windows.ps1` 三个入口，
产物输出到 `dist/windows-native/`。旧 WPF 项目、专用构建与测试脚本已移除。
源码清理不修改产品身份、数据、IPC 或远程协议，不清空现有配置。

原生 UI 的中文/英文资源独立保存在 `platform/windows/messages.json`，
执行 `python scripts/sync-native-localizations.py` 更新 `Localization.h`。
构建器运行本机可执行架构的 CTest；ARM64 主机运行 x64 测试属于仿真验证。

```powershell
./scripts/windows/test-ui-smoke.ps1 -Architecture arm64
./scripts/windows/test-ui-smoke.ps1 -Architecture x64
```

测试包含真实分片 Named Pipe 收发、协议必需字段和未知字段、URI 准入、后台导航边界、
登录目标编辑保留、过期异步结果隔离、关闭驻留，以及 MSI 条件与无托管运行时 payload 检查。
测试窗口和管道与已安装的 daemon 隔离，不安装 MSI、不登录 NAS、不修改用户连接。
真实登录、Service/Wintun、断网恢复、多用户安装升级、回滚及卸载仍按发布验收执行。

## 原生 MSI 体积优化

原生发布链路对 Go 客户端与安装辅助程序使用 `-s -w`，不携带符号表和 DWARF。
Go 运行时仍保留 panic/Stack 所需的函数名、文件和行号；需要调试器时从对应源码构建调试程序。

安装前停止跨用户 session 的 daemon、卸载时恢复网络由独立
`cmd/fncpn-windows-installer` 执行，MSI 不再内嵌第二份完整客户端。
辅助程序只接受固定操作，要求 LocalSystem；共享服务目录的 owner/ACL/reparse 检查，
并使用现有 NetworkConfigurator 按稳定 GUID 恢复 journal 中登记的路由、地址和 MTU。
它无需启动 WireGuard runtime，不读取用户凭据或遍历清理用户数据。
正常卸载保留用户数据，显式当前用户清理仍使用原有的 impersonating 操作。

辅助程序仅嵌入 MSI Binary 表，不安装到产品目录。安装操作保持 deferred、
non-impersonating 和原有执行顺序；构建检查其架构、嵌入体积及完整客户端副本缺失。
CAB 使用 high 压缩。2026-10-13 本地候选实测 ARM64 约 9.0 MiB、x64 约 9.7 MiB，
分别低于优化前约 22.4 / 24.4 MiB。具体大小随源码和编译器变化。

Go 全量测试包含安装操作的普通用户拒绝、额外参数拒绝、隔离 Named Pipe 的停止/拒绝/无 daemon，
以及 untrusted journal 在 recovery 前被拒绝且保持原内容。现有网络恢复测试继续验证 GUID、
接口 index 变化、失败时保留 journal 等行为。本次未实际安装或卸载当前机器的产品。

原生 MSI 使用 WiX 标准 `WixUI_InstallDir` 向导：欢迎、许可协议、安装目录、安装确认、
进度、完成。安装目录默认仍为 Program Files 下的 FnCPN 架构目录；允许在目录页修改。
安装确认页显示管理员授权盾牌，维修/卸载使用标准维护界面。

安装向导的国际化使用英文基础 MSI 和内嵌的简体中文语言 transform（storage `2052`）；
每个架构仍只发布一个 MSI、携带一份程序 payload。通过 msiexec/双击打开时，简体中文
显示语言自动使用中文，其余显示语言回退英文。标准向导、维护、错误与进度文本由 WiX
本地化库提供，FnCPN 自定义文本在 `packaging/windows/installer.en-US.wxl` 和
`installer.zh-CN.wxl` 维护。MIT 许可正文保留原文。

中英文构建共用同一个 ProductCode 和 UpgradeCode，跨安装语言允许升级；语言 transform
不修改安装动作、文件和组件身份。不要将两个独立生成 ProductCode 的 MSI 简单合并。
本地已实测中文系统自动加载 `2052`、显示中文欢迎/协议页；未执行实际安装。

原生主窗口按 macOS 的信息层级排列：产品图标/版本、设备名、连接状态、两列详情、
连接/重新登录/重新检测、诊断文本、底部工具栏。原生静态控件使用统一背景和分层字体，
状态仅在已建立连接时显示绿色；高对比度使用系统颜色。默认窗口大小和字体随显示器 DPI
缩放，窗口高度受工作区限制，仅在内容超出窗口时显示页面滚动条。

原生 MSI 默认启用 `MsiLogging=voicewarmup!`，逐行落盘至安装用户 `%TEMP%\MSI*.LOG`，
实际路径由 MSI 的 `MsiLogFileLocation` 记录。排查慢安装时比较各 Action 的开始/结束时间，
特别关注 Restart Manager、旧版本移除、后台停止和服务启动；进度条满格不代表事务已完成。
安装辅助程序另外写入 `%ProgramData%\FnCPN\Service\Logs\installer-*.log`，记录固定操作、
开始/结束时间、退出码和耗时，不记录授权信息。历史未开启日志的安装无法补出 Action 耗时。

## Windows 脚本整理

日常只需要三个入口（在仓库根目录执行）：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/setup-windows.ps1
pwsh -NoProfile -File scripts/build-windows.ps1
pwsh -NoProfile -File scripts/check-windows.ps1
```

`check-windows.ps1` 默认验证两个架构的已有候选，不自动安装或重建。只检查源码、Go 和 setup 回归可使用 `-SkipPackage`；指定单个架构用 `-Architecture x64` 或 `arm64`，自定义候选目录用 `-PackageRoot`。

| 决定 | 内容 | 原因 |
|---|---|---|
| 保留并统一名称 | setup / build / check 三个入口 | 原生 UI 已是唯一 Windows 实现，无需 `-native` 后缀或旧链路包装 |
| 保留为内部辅助 | `scripts/windows/` 的本地化检查、MSI 语言嵌入、MSI 平台条件、UI smoke、setup 回归 | 各自验证不同风险，由统一入口或 CI 调用 |
| 清理 | 旧 WPF setup/build/smoke、桌面运行时打包和旧路径引用 | 不再对应当前产品，不保留重复入口 |
| 新增 | 统一 check 入口、安装工具返回码回归 | 集中验证；3010 表示成功但需重启，其他失败必须阻断 |
| 保留依赖 | Go、Python、Git、PowerShell、.NET SDK、CMake、MSVC、Windows SDK、WiX、Wintun、WebView2 SDK/Runtime/bootstrapper | .NET SDK 仍供 WiX/NuGet 使用，不能随 WPF 一并移除 |
| 改为可选 | Node (`-IncludeServerTools`)、GitHub CLI (`-IncludeReleaseTools`) | 分别用于 fnOS 网页测试、发布管理，不是 Windows 本地构建必需 |

仓库其他脚本包含 macOS/fnOS 构建、跨平台版本/本地化、发布资产与 Homebrew 校验，不能因为 Windows 技术栈切换删除。按职责保留，Windows 专用辅助已移入子目录。
此整理不自动删除本机工具、下载缓存或用户配置，也不卸载共享 Runtime。
