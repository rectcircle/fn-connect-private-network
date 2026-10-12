# Windows 命令行开发环境

下一 Windows 版本的 UI 迁移方案见 [Windows UI 后续优化](windows-ui-roadmap.md)。
本文的构建步骤对应当前 WPF 实现。

当前候选安装准入已放宽为 Windows 10 22H2（build 19045）及以上的 x64/ARM64
客户端系统；Windows Server 和 32 位系统不在范围内。安装条件已由 Windows Installer
回归验证，但 Windows 10 的实际登录、Service、Wintun、WebView2 和升级仍待实机验收。
当前 .NET 10 依赖的厂商 Windows 10 支持范围与本项目安装准入不同，不能将放行安装
等同于所有 Windows 10 版本已获得厂商支持或完成产品验收。

在仓库根目录运行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/setup-windows.ps1
```

`Bypass` 仅作用于本次进程，不修改用户/机器执行策略。脚本支持 Windows ARM64 和 x64，
无需管理员权限即可安装通用工具；仅当缺少 Windows SDK 等系统前置组件时，WinGet
可能请求 UAC。不安装 FnCPN Service，不创建网卡，不登录 GitHub，不自动重启。

脚本按顺序检查实际工具，再安装缺失项。已安装的可用 .NET 10 SDK、Go >= 1.27、
GitHub CLI 和独立 PowerShell 保留；新安装的版本与下载哈希固定在脚本中。
Git/Python/Node 和 Windows SDK、WebView2 已存在则跳过，缺少时通过 WinGet 安装。
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
探测分支回归可运行 `powershell.exe -NoProfile -File scripts/test-setup-windows-proxy.ps1`。

默认验证包含 WPF + NotifyIcon + WebView2 双架构自包含发布、Go 双架构编译和 WiX
双架构 MSI 构建，但不安装验证 MSI。可用 `-SkipSmoke` 跳过编译验收，仅准备工具。
验证程序不是产品版本，不修改 FnCPN 版本、业务数据或协议。
最终产品的驱动、网络和升级实机验收另行执行；Windows 发布计划为 GitHub Releases，
无 WinGet 应用分发、无受信证书要求。

## 构建 Windows 产品候选

准备完成后，在仓库根目录运行：

```powershell
./scripts/build-windows.ps1
```

脚本按 ARM64、x64 顺序发布自包含 WPF UI 到 `ui/` 子目录，再构建根目录 Go 单一 CLI/daemon/Service，
加入同架构、官方 Authenticode 有效的 Wintun DLL 与许可证，最后生成两个 WiX MSI。分开放置 UI 与 Go
是为了避开 Windows 文件名大小写不敏感导致的 `FnCPN.exe`/`fncpn.exe` 覆盖。
每个架构也可单独构建：

```powershell
./scripts/build-windows.ps1 -Architecture arm64
./scripts/build-windows.ps1 -Architecture x64
```

产物在 `dist/windows/arm64/` 与 `dist/windows/x64/`，包括未签名 MSI、解包 payload 和
`build-manifest.json`。清单记录源 commit、dirty 状态、源码指纹、架构、UTC 构建时间和 MSI SHA256。
RC 的程序版本和安装包文件名保留 `-rc.N`，MSI ProductVersion 使用核心数字版本，
清单另记 `installerVersion`。同一核心版本的候选不能依靠 MSI 数字版本区分升级顺序；
安装器支持相同核心版本候选覆盖替换，但不能阻止 RC 编号逆序安装；请按发布顺序安装。
公开 RC 不得覆盖原产物，修复需递增 RC 编号。
构建会安全拒绝覆盖目录中未登记的文件。当前 Windows 开发机可交叉构建两种架构，但安装、Service、
Wintun 路由与升级验收仍需对应 Windows 实机；初始 GitHub 手动下载版本允许未签名，
公开 RC 必须使用同一发布提交的干净构建，不直接上传本地 dirty 候选。
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

1. 整理并审阅源码、CI、文档和版本资源，提交全部发布输入。当前准备版本为 `1.0.4`；
   确认该编号尚未公开，公开后只能递增 RC/PATCH。正式版必须重新构建并检查版本元数据，不能给 RC 包改名。
2. 在准备提交上运行 Windows Go 测试/vet、本地化与版本同步检查、代理设置回归和双架构
   构建。macOS/fnOS 的测试与发布检查由相应 runner 执行；Windows 无法替代这些平台验收。
3. 从发布提交创建对应 tag；Release workflow 先构建 draft。Windows runner 安装固定 WiX
   UI/Util 扩展与官方依赖，验证 tag/版本、Go 和资源，再构建两种 MSI；macOS runner
   执行共享与平台检查，构建 PKG/FPK，并生成全平台清单。
4. 检查 draft 的五个安装包（macOS PKG、两种 fnOS FPK、两种 Windows MSI）、发布 JSON
   与 SHA256SUMS。下载并核验实际资产；对 CI 生成的 ARM64 MSI 补一次安装/登录/连接抽验。
   本地开发 MSI 的验证不等同于 hosted runner 产物验收。
5. 发布说明如实披露 Windows 11 ARM64 已验收、原生 x64/Windows 10 与异常场景待验证、
   未签名、缺 Runtime 时需联网，以及当前 CLI 授权会唤起 UI。RC 不更新 Homebrew Cask，正式版公开后由发布流水线更新 Cask。
6. 完成 draft 验证后，使用同 tag 的 Release workflow 并设置 `publish=true` 公开；公开
   阶段复验并发布原 draft 字节，不用重建产物替换已经验收的 draft。

发布必须满足 [版本与兼容性](versioning-and-compatibility.md)。实际 hosted CI、历史版本
兼容验证与发布资产验收未完成时，应在发布说明中明确尚未验证的范围。

WiX 7 具有 Open Source Maintenance Fee 条款，使用者应核对官方适用条件：
https://docs.firegiant.com/wix/osmf/ 。构建验证通过 `-acceptEula wix7` 接受该 EULA，
不创建全局接受文件，不购买订阅或凭据。


源码指纹排除本开发环境说明文档（`docs/windows-development.md`）及 `.github/workflows/` 下的 CI 定义；这些文件不改变安装包构建输入。manifest 的 dirty 状态仍包含这些文件的修改。
