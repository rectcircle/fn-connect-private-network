# Windows 开发与发布

Windows 客户端使用 C++ Win32 UI、Go daemon/Service 和应用内 WebView2 管理后台。UI 通过本地 IPC 调用 Go；认证、连接、凭据持久化、路由与特权 journal 由 Go 负责。
支持 Windows 10 22H2（build 19045）及以上的 x64/ARM64 客户端系统，不支持 Windows Server 或 32 位系统。

## 开发环境

在仓库根目录执行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/setup-windows.ps1
```

setup 检查并准备 Git、Python、Go >= 1.27、PowerShell 7、.NET 10 SDK、CMake >= 3.27、MSVC 2022 x64/ARM64 工具链、Windows SDK、WiX 7.0.0 UI/Util 扩展、Wintun 0.14.1，以及 WebView2 原生 SDK、共享 Runtime 和安装引导器。
.NET SDK 仅用于 WiX/NuGet，客户端不依赖 .NET 桌面运行时。Node 和 GitHub CLI 分别通过 `-IncludeServerTools`、`-IncludeReleaseTools` 选择安装。

默认 setup 编译双架构 UI、Go 和测试 MSI，并运行本机可执行架构的 CTest；`-SkipSmoke` 跳过编译，但仍下载 Go 依赖和还原 WebView2 SDK。x64 主机不运行 ARM64 程序，ARM64 主机上的 x64 执行属于仿真。
setup 不安装 FnCPN、不创建网卡、不登录 GitHub、不自动重启。系统前置组件安装可能请求 UAC；`ExecutionPolicy Bypass` 仅作用于本次进程。

| 内容 | 位置 |
|---|---|
| 新安装的 .NET SDK | `%LOCALAPPDATA%\Microsoft\dotnet` |
| 新安装的 Go / PowerShell / GitHub CLI | `%LOCALAPPDATA%\Programs` |
| WiX | `.dev/tools/wix` |
| Wintun 与许可证 | `.dev/dependencies/wintun/0.14.1` |
| WebView2 原生 NuGet SDK | `.dev/native-ui-nuget` |
| WebView2 安装引导器 | `.dev/dependencies/webview2` |
| 下载缓存、setup 日志与临时源码 | `.dev/downloads`、`.dev/logs`、`.dev/smoke` |
| setup 编译验证产物 | `dist/smoke` |

已安装的可用工具优先复用。下载校验哈希或官方签名；异常目录不会直接覆盖。
通用工具目录加入用户 PATH，项目 `.dev` 不加入 PATH；设置 `DOTNET_ROOT`，不设置 `GOROOT` 或改变 `GOPATH`。已有终端/IDE 需重新打开以刷新持久环境。已有 fnm/nvm 的 Node 配置保留，不将临时目录写入用户 PATH。

默认 Go 代理不可达时，setup 先验证 `goproxy.cn`，再验证 `goproxy.io`，通过 `go env -w` 保存可用代理；已有自定义 `GOPROXY` 保留。恢复默认使用 `go env -u GOPROXY`，显式环境变量需另行调整。代理不可达时停止，不关闭 checksum database 校验。

## 构建和检查

日常使用三个入口，内部辅助脚本位于 `scripts/windows/`，职责见 [脚本索引](../scripts/README.md)。

```powershell
pwsh -NoProfile -File scripts/build-windows.ps1
pwsh -NoProfile -File scripts/check-windows.ps1
```

build 默认按 ARM64、x64 构建，可指定 `-Architecture arm64` 或 `x64`，并通过 `-OutputRoot dist/<目录>` 选择输出位置。
默认产物位于 `dist/windows-native/<架构>/`，包含未签名 MSI、解包 payload 和 `build-manifest.json`。清单记录产品版本、commit、dirty 状态、源码指纹、架构、构建时间及 MSI SHA256；构建拒绝覆盖输出目录中的未登记文件。

check 验证版本/本地化生成资源、setup 回归、Go test/vet、原生 UI 与 MSI。它使用已有候选，不自动重建或安装；只检查源码用 `-SkipPackage`，单架构用 `-Architecture`，自定义产物目录用 `-PackageRoot`。

原生 UI 源码位于 `platform/windows/`，独立编译示例：

```powershell
cmake -S platform/windows -B .dev/windows-ui-arm64 -G "Visual Studio 17 2022" -A ARM64 -DWEBVIEW2_SDK_ROOT="$PWD/.dev/native-ui-nuget/microsoft.web.webview2/1.0.4258.31"
cmake --build .dev/windows-ui-arm64 --config Release
ctest --test-dir .dev/windows-ui-arm64 -C Release --output-on-failure
```

首次执行先运行 setup。构建 x64 时替换构建目录并使用 `-A x64`。UI 的中英文资源位于 `platform/windows/messages.json`，执行 `python scripts/sync-native-localizations.py` 更新 `Localization.h`。
自动测试使用隔离窗口和管道，不操作已安装 daemon 或用户连接，覆盖 IPC 分片、字段校验、导航边界、授权取消、过期结果、退出资源回收、DPI/布局和 MSI 元数据。

## 安装与运行

安装包包含 `ui/FnCPN.UI.exe`、单一 Go CLI/daemon/Service `fncpn.exe`、同架构官方签名的 Wintun DLL 和许可证。
Go 使用 `-s -w` 去除调试符号，仍保留 panic 堆栈所需函数名、文件和行号；CAB 使用 high 压缩。精简安装辅助程序只嵌入 MSI Binary 表，不安装到产品目录，不复制第二份完整 Go 客户端。

MSI 使用标准 `WixUI_InstallDir` 向导，允许选择安装目录。每个架构只有一个 MSI，英文基础包内嵌简体中文 transform `2052`，按系统显示语言自动选择中文或回退英文；语言切换不改变产品、组件或安装动作身份。完成页默认勾选立即打开 FnCPN，隐藏返回和取消按钮。

管理后台使用共享 Evergreen WebView2 Runtime。安装器检测已安装 Runtime，仅缺失时调用微软签名的引导器；首次安装缺失 Runtime 时需要联网，安装失败会中止 MSI。卸载 FnCPN 保留共享 Runtime。

升级前关闭 UI 和后台进程，组件成套更新。跨会话停止 daemon 和网络 journal 恢复以 LocalSystem 执行，先检查服务目录 owner/ACL/reparse；当前用户的关闭操作以该用户身份执行。辅助程序不读取凭据、不遍历删除其他用户数据。
Go Service 设置故障恢复：首次失败 5 秒后重启，后续按 30 秒、60 秒重试，也处理非崩溃失败。

正常升级和卸载保留用户身份、配置、DPAPI 凭据及日志。需要显式清理当前卸载用户的数据时：

```powershell
$version = (Get-Content internal/version/VERSION -Raw).Trim()
msiexec.exe /x "./fncpn-$version-windows-arm64-unsigned.msi" PURGE_CURRENT_USER_DATA=1
```

该操作只清理发起卸载用户的 `%LOCALAPPDATA%\FnCPN`，不会清理其他用户 profile、Service recovery journal 或共享 Runtime。
主窗口关闭后驻留托盘，显式退出结束程序；自启动为用户级设置。管理窗口按需创建，在主窗口前方打开，导航限制在本地代理范围，外链不携带代理 token。

## 安装日志

MSI 默认启用 `MsiLogging=voicewarmup!`，日志写入安装用户 `%TEMP%\MSI*.LOG`，实际路径由 `MsiLogFileLocation` 记录。
定位慢安装时比较各 Action 的开始和结束时间，检查 Restart Manager、旧版本移除、后台停止和服务启动；进度条满格不代表事务已完成。
LocalSystem 安装维护操作另外记录到 `%ProgramData%\FnCPN\Service\Logs\installer-*.log`，包含操作、时间、退出码和耗时，不记录授权信息。

## 草稿与公开发布

产品版本来自 `internal/version/VERSION`，使用 `MAJOR.MINOR.PATCH`；生成资源通过 `scripts/sync-version.py` 同步。
遵守 [版本与兼容性](versioning-and-compatibility.md)：同一发布的 macOS、fnOS、Windows 和 Go 来自同一 commit；未公开草稿可以重建替换，公开版本及产物不可覆盖，修复递增 PATCH。

1. 提交全部发布输入，运行检查，从干净发布提交创建对应 `v<版本>` tag。
2. tag 触发 Release workflow 创建草稿。Windows CI 固定使用 `windows-2022`，匹配 `Visual Studio 17 2022` generator；更换工具链须同步本地 setup 和 CI。
3. 验证草稿的六个安装包（两种 macOS PKG、两种 fnOS FPK、两种 Windows MSI）、发布 JSON 和 SHA256SUMS，核验下载资产及安装结果。
4. 使用同 tag 的 Release workflow、`publish=true` 公开已验收草稿；公开阶段复验原产物，不重建或重新上传，随后更新 Homebrew Cask。

Windows EXE/MSI 未签名，Wintun 和 WebView2 引导器保留官方签名。WiX 7 的 [OSMF 条款](https://docs.firegiant.com/wix/osmf/) 由构建命令 `-acceptEula wix7` 接受。
各版本的测试结果记录在 [验证记录](validation.md)，功能变更记录在 [CHANGELOG](../CHANGELOG.md)。
