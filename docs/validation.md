# 验证记录

记录影响发布判断和技术选型的实测结论。日常测试与发布步骤见 [开发与构建](development.md)，
产品要求见 [产品需求](product.md)，升级保证见 [版本契约](versioning-and-compatibility.md)。
自动化通过、用户确认和带日志的实测分别记录；旧版证据不代表新版的所有专项通过。

## 1.1.4 Android 候选（2026-10-23；草稿准备，未公开）

- 使用官方 `fyne init` 生成的独立 UI module；Fyne UI 依赖共享 Go 核心，Android VpnService、
  Keystore、TUN、网络事件、通知及 WebView 由薄 Java 层适配，单应用进程、单 Go runtime。
- [早期 Android CI](https://github.com/rectcircle/fn-connect-private-network/actions/runs/39762676937)
  在 `df0378a` 成功；后续改动已进行本地完整 Java/JNI/Go 构建，不能将早期 CI 当作当前候选成功证据。
- PJZ110 真机先后安装开发 build 4–19，用户确认主要功能与交互基本可用；用户分别确认 IPv6
  直连和临时禁用 IPv6 后的 FN Connect 中继可用。正常 IPv6 检测已恢复，无临时降级残留。
- IPv6 使用受保护并绑定物理网络的 UDP6 路由预检，后续验证真实 WireGuard 握手；支持 ULA
  上联，不要求手机拥有全局 IPv6 地址。真实外网长期测试与完整历史二进制矩阵仍待专项验收。
- Chrome NAS 访问曾出现 ERR_TOO_MANY_RETRIES；CDP netlog 与系统 appops 显示 Chrome
  的局域网权限被拒绝，用户调整权限后确认解决。未将该故障归因于服务器重定向循环。
- 真实 WebView instrumentation 验证页面避让系统栏/应用栏，诊断 ContentProvider 可读且拒绝写入；
  菜单、登录、系统字体、诊断复制、后台导航和冷启动布局已有 Go 回归。00:05 的旧版 v7
  曾因触摸等待发生 ANR；通知入口随后改为单 Activity 复用，不能据此断言所有 ANR 已消除。
- APK 每个 ABI 独立发布，使用 `no_emoji,migrated_fynedo`、符号裁剪及 16 KB ELF/ZIP 对齐。
  开发包约 25.9/27.5 MiB；真实 16 KB 页设备、厂商省电、长期锁屏/切网、耗电/吞吐、
  Private DNS/DoH、系统 VPN 撤销与完整正式包升级仍需最终候选专项验证。
- Go 根模块与移动模块测试、竞态与 vet，构建/验签回归、workflow actionlint 已有本地通过记录；
  macOS 原 PNG 重新生成后哈希不变，移动 SVG/Android 矢量同源，渲染对照保留图形细节。
- build 20 双 ABI 与 instrumentation 包均匹配固定 FnCPN 正式签名；旧 Debug 密钥包被新校验器
  拒绝，无密钥配置时 Gradle 直接失败。密钥仅在仓库外或 Actions Secrets 中，源码仅存公钥指纹。
  手机保留旧 Debug 签名 build 19；正式包使用独立生产包名，需单独登录，未自动删除旧数据。
- 1.1.4 新增 Android 平台不增加 server 要求，按用户确认采用 PATCH；版本准入仍为同 MAJOR
  且 client.MINOR ≤ server.MINOR。此前公开的 1.1.3 不覆盖；当前各端以同 commit 构建 1.1.4。
- 新 Release workflow 将 Android 正式签名、非 debuggable APK 纳入统一清单及镜像，绑定版本、
  commit、versionCode、ABI 和证书指纹。本次仅触发草稿，不公开；云端结果与最终产物待后续审阅。

## 1.1.2 正式发布验收（2026-10-22）

- 最终发布 commit 为 `c3fd6b2e67243ff1cafde2d2ec3ab5e4c11d210d`，
  [草稿构建 39634507085](https://github.com/rectcircle/fn-connect-private-network/actions/runs/39634507085)
  成功，包含 21 组 Linux 发行版/架构验证、双架构 Nix 构建及 RPM 签名检查。
- 独立下载 18 个发布资产，16 个安装包 SHA256 与清单逐项匹配，清单 commit 与 tag 一致；
  4 个 RPM 在只含项目公钥的临时密钥库中验签通过。公开沿用这批产物，不重新构建。
- Debian 13 arm64 成套覆盖安装最终 Core/UI，配置哈希不变，`dpkg -V` 无差异，
  用户服务、特权服务、UI 和托盘启动。用户确认“验证无问题”，并确认兼容验收无问题后授权公开。
  兼容验收按用户确认记录，不补写未提供的逐项历史二进制组合、日志或额外平台实测。
- GitHub Release 已公开，Homebrew Cask 更新成功。首次夸克镜像因旧脚本仍要求恰好 6 个安装包而在上传前失败；
  镜像工具现按版本及 Linux 支持矩阵校验完整安装包清单，独立镜像任务重试不重建或覆盖 GitHub 资产。
- 历史候选记录及其他发行版真实桌面/内核、NixOS 主机集成的验证边界保留如下。

## 1.1.2 Linux 候选与 Debian 验证（2026-10-21）

以下为 2026-10-21 的历史候选记录；最终发布验收见上一节。macOS 默认原生 UI，
Linux 使用 Wails v2，不将历史候选的验证结果扩大为最终 commit 的新增实测。

- [草稿构建 39614395493](https://github.com/rectcircle/fn-connect-private-network/actions/runs/39614395493)
  对应 `2a28a23366347f8415a7286cca2b5e4efff6f313`，构建成功。5 组基线原生构建
  Core/UI；21 组目标验证包依赖、实际安装、ELF 所需版本符号、`ldd -r`、普通用户
  在 Xvfb + 会话 D-Bus 中启动真实 WebKit 前端、同版本重装和卸载。Nix 双架构构建、
  模块检查与前端启动通过。报告绑定同一 commit、版本和发布包 SHA256。
- Debian 13 arm64 从 GitHub 下载相同草稿的 Core/UI deb，校验发布清单与 SHA256 后
  成套覆盖安装，`dpkg -V` 无差异，配置文件哈希保持不变，用户及特权服务就绪。
  未向虚拟机安装开发环境。用户确认配置保留、IPv6 直连及临时禁用 IPv6 后的中继正常，
  随后恢复原 IPv6 设置。该确认不扩大为其他发行版的实机验收。
- 修复 DNS 虚拟接口触发物理网络快照变化、LAN 探测误经自身隧道、NetworkManager
  接管自有接口引发重复激活；增加主题着色托盘图标、退出等待 Core 清理，以及 GTK
  使用 Wayland 激活令牌/DBusMenu 点击时间戳请求窗口置前。
- 托盘激活消息处理以独立 GTestDBus 会话测试实际 C 实现，覆盖令牌接收、原始时间戳、
  对象路径过滤与一次性消费；该测试通过。真实桌面显示和焦点仍以用户实测为准。
- 用户曾报告关机等待；上一启动日志中 FnCPN 服务在约 0.4 秒内停止，90 秒等待来自
  Parallels `prlcc` scope。此记录不宣称解决所有关机等待，也未修改 Parallels 配置。

待验收边界：其他发行版的真实内核、SELinux/AppArmor 强制模式、GNOME/KDE/Wayland
桌面集成和 NixOS 主机部署；签名 RPM 的最终 CI 验证及用户兼容验收确认已补充于上一节。
容器共享 runner 内核，虚拟 X11 的启动检查不能证明各发行版桌面或内核行为。

## 1.1.1 认证兼容修复与候选验收（2026-10-20）

用户于 2026-10-20 确认“macos 和 windows 都测好了”，随后确认“cli 也测试了，没问题”。
本轮用户确认覆盖 macOS、Windows 图形客户端与 Go CLI 登录流程。按用户对本轮候选的整体确认记录，
不补写未提供的系统版本、设备型号、逐项日志或架构组合，不将该结论扩展为所有平台与历史版本互通验收。

- macOS、Windows 原生界面及 Go CLI 支持访问码、OTP 和设备信任；图形界面 OTP 为独立步骤，
  支持返回账号密码表单，并处理访问码异步探测后的焦点与 Tab 顺序。
- 访问码探测和验证携带 `mode=relay`；登录 WSS、远程 API、管理后台代理复用
  `os-access-code`。管理后台保留独立 Browser 设备身份和 Web token，并完成其 OTP 验证。
- macOS 安装包加入当前用户会话中的“稍后 / 启动 FnCPN”提示；不阻塞安装器，
  命令行或无人值守安装不弹窗。
- 候选源码提交为 `1fb9f90626d298f55c0fae90697bdf41b9e71561`，tag 为 `v1.1.1`。
  [常规 CI](https://github.com/rectcircle/fn-connect-private-network/actions/runs/39397395883)
  与[草稿构建](https://github.com/rectcircle/fn-connect-private-network/actions/runs/39397395527)
  均通过：Go 测试、race、vet、发布辅助脚本、Web 测试、Swift 类型检查、Windows 原生 UI
  回归及 ARM64/x64 安装包构建通过。本轮开发阶段另有中英文 AppKit 实际布局检查通过。
- 草稿包含 macOS、Windows、fnOS 的 ARM64/x64 六个安装包及清单、SHA256 文件；
  上传后回读验证通过，下载清单的 commit 与候选一致。草稿尚未公开；本次文档更新不等于正式发布。
- 产品版本为 1.1.1，不新增 FnCPN server 要求，不改变远程版本准入规则、本地 IPC 5 或
  持久化 schema；现有 1.1.x server 无需配套升级。仍不据此宣称已完成历史正式 client/server
  二进制互通矩阵或所有平台的实机升级验收。

## Windows 虚拟机 LOCAL DNS 修复（2026-10-19）

本机处于虚拟机 NAT 网段，NAS 私网探测成功并进入 LOCAL，但 DNS 地址检查额外要求目的地址
属于本机网卡直连前缀，导致可经网关访问的 NAS DNS API 被提前拒绝、查询返回 SERVFAIL。
LOCAL 现使用 NAS 已授权的 `AllowedIPs` 限制查询目标和回答，不再要求同一直连子网；
DIRECT/RELAY 的网段冲突过滤，以及暂停、切换 NAS、越界地址限制保留。

- client/dns 包测试及 vet 通过；增加 LOCAL 同网段 / 虚拟机 NAT 的完整查询与清理回归，
  并覆盖越界、公网、loopback、暂停以及 DIRECT/RELAY 网段冲突。
- 在隔离配置和隔离本机 DNS listener 中使用真实 NAS HTTP DNS API 验证，用户指定的
  NAS 域名经 UDP / TCP 均返回正确 LAN IPv4；未修改用户 NRPT、配置或运行中的 daemon。
- 修复前旧安装版本的系统解析为 SERVFAIL；修复后的当前 Windows ARM64 候选由用户确认“无问题”。
  此确认与上述隔离 UDP/TCP 自动化证据分别记录，不扩展为历史版本完整安装矩阵验收。

## Windows 安装维护协议修复（2026-10-19）

`codex/feature-lan-dns` 的本地 1.1.0 候选，未发布。修复 1.0.9 → 1.1.0 安装时
`StopCurrentUserDaemon` 因产品版本 / IPC 4→5 检查失败而返回 MSI 1722 的问题。
安装维护 v1 独立于业务 IPC；旧 IPC 4/5 的退出适配保留，不读取业务状态，不放宽正常业务版本检查。

- `scripts/check-windows.ps1 -SkipPackage`、全量 `go test ./...` 与 `go vet ./...` 通过。
- `scripts/windows/test-maintenance-compatibility.ps1` 使用 v1.0.7、v1.0.8、v1.0.9、v1.0.10
  原始 IPC 源码构建隔离进程；另构建等价旧 IPC 5 样本、当前维护 v1、业务 IPC 99 / 产品 9.0.0 样本。
  当前和模拟未来的维护客户端分别测试这 7 种进程的正常退出、拒绝退出、身份拒绝，共 42 项通过。
  IPC 5 样本基于 v1.0.10，只将 IPC 改为 5、产品改为 1.1.0；已核对其 IPC 源码与 `10f5512` 等价，
  不要求未发布分支提交长期可达。每次运行在 `.dev/maintenance-compatibility/` 保存源码 commit 和二进制哈希清单。
- 单元回归覆盖普通业务版本检查、非法维护方法、未知维护版本、缺失/错误响应、请求 ID 不匹配、
  拒绝关闭、身份校验、未收到确认不执行关闭，以及存活进程超时不能视为退出。
- 修复 Windows schema 2→3 配置迁移前未关闭读取句柄、导致原子替换失败的问题；
  原有身份、私钥、连接意图和精确回滚备份保留测试已通过。

这些是安装维护和迁移的自动化证据。历史测试使用原始 IPC 实现及隔离测试 handler，
不等同于每个历史 MSI 的完整实机升级；用户已确认当前 Windows ARM64 候选“无问题”，
历史完整安装矩阵及其他网络环境仍不属于此次验证范围。

## 1.1.0 DNS 候选与真机验收（2026-10-19）

当前候选来自 `v1.1.0` tag，GitHub Release 草稿尚未公开。新增 mDNS / LAN DNS 地址解析，
默认 `<FN Connect ID>.fncpn`，保留自定义后缀；安装/升级后随原连接意图自动应用。
新增服务端要求通过 MINOR 1.1 表达，不能通过关闭 DNS 放行 1.1 client → 1.0 server。

| 范围 | 自动化证据 |
| --- | --- |
| Go | `go test -race ./...`、`go vet ./...` 通过；后续解析/生命周期修改对 dns/client/server/privileged/command 包重跑竞态测试 |
| 解析 | 家庭 DNS 缺失/超时/不存在回退 mDNS；LAN 优先及故障回退、IPv4 A、AAAA 本地空答案且不查询 NAS、地址范围及路由冲突、后缀隔离；真实 loopback UDP/TCP 前端及 LAN DNS CNAME/负缓存/截断转 TCP 测试 |
| 系统计划 | 测试替身验证 LOCAL 自动应用、后缀更新、断开清理、journal 恢复、进程与 generation 隔离、不覆盖外部规则；不在开发主机安装真实 resolver/NRPT 规则 |
| 升级 | schema 1 server / schema 2 client 输入的备份和原子升级测试，验证设备、网络、私钥及连接意图保留；旧版本请求头与版本准入回归 |
| 页面及原生界面 | Web 测试 32 项通过，iframe 浏览器专项因未配置 Playwright 跳过；Swift 完整编译及 `FNCPN_LAYOUT_CHECK=1` 离屏布局测试通过；本地化、版本和 IPC 同步检查通过 |
| 跨平台构建 | Windows amd64/arm64、Linux amd64/arm64 的 Go CLI 编译通过；PR Checks 与 1.1.0 Release workflow 在 macOS / Windows runner 均通过，构建了 macOS、fnOS 和 Windows ARM64/x64 安装包 |

用户于 2026-10-19 确认当前 1.1.0 候选的 macOS 与 Windows 真机测试全部完成，包含实际连接和 DNS 功能验收；此前记录的 Windows ARM64 LOCAL DNS 回归是其中一项。此处按用户确认记录整体结论，不补写未提供的系统版本、设备型号、逐项日志或性能数据。

正式发布前仍需按版本契约使用实际已发布的同 MAJOR 历史 client/server 二进制完成互通矩阵；当前历史源码夹具和候选 Release 构建不能替代该项验收。
DNS 已统一为 NAS LAN IP 的 HTTP 54790，LOCAL 使用物理 LAN，DIRECT/RELAY 使用 WireGuard LAN 路由。
DNS 已移除新增签名、时间窗口和防重放；回归覆盖无密钥查询、重复请求成功、不依赖设备注册状态、请求参数及路由冲突；不使用 HTTPS 网关回退。
`/api/v1/probe` 与 `/probe` 共用证明逻辑；后者保留 v1.x.x 兼容，v2.x.x 将废弃。
管理页已拆分网络连接、域名访问与解析诊断，增加字段说明及折叠高级配置；已用本地模拟 API 在浏览器检查布局。
内建 `<NAS hostname>.<后缀>` 自动化覆盖 LAN 地址过滤、仅 IPv4 地址回答 和不调用上游；管理页只使用 server 本机读取的 FN ID 与管理端口，覆盖未知信息、强制 HTTPS、自定义后缀保留和忽略 URL 参数。
server 本机信息读取已在 fnOS 1.2.0800 实机以 `fncpn` 用户运行 Go 读取器冒烟测试通过，直接读取 Unix Socket 与管理端口配置；未替换运行中的服务或修改 NAS 设置。
Windows loopback 候选选择、候选耗尽、双传输部分绑定回滚和特权端参数校验已有自动化回归；
Windows 检测到组策略接管 NRPT 时报告 DNS 受限，不修改组策略或谎报就绪。

回滚恢复升级前程序与配套 `state.schema-1.backup.json`、`config.json.schema-2.backup`；
server schema 2 / client schema 3 不由旧程序直接读取，本地 IPC 升为 5，本地组件成套更新。

## 1.0.0-rc.2 验证汇总（2026-10-09）

`1.0.0-rc.2` 安装由用户确认通过。以下范围记录截至 2026-10-09 收到的验证证据。

| 范围 | 结论与依据 |
| --- | --- |
| 常规安装与升级 | 历史首次安装及多次小版本覆盖升级正常；rc.2 安装用户确认“验证没问题了”。 |
| 网络与配置 | 用户确认实机验证完成；有历史 RELAY、NAS/LAN 访问、Docker 转发和恢复记录。 |
| 局域网权限与中继回退 | 0.1.55 安装后用户确认局域网权限正常；临时关闭 Mac Wi-Fi IPv6 后中继回退验证通过。 |
| 自动化 | rc.2 发布流水线通过 Go test/race/vet、Web 测试、版本/本地化同步、发布脚本测试、Linux 双架构编译、Swift macOS 13 检查和真实 PKG 工具回归。网络引擎故障测试使用隔离环境或测试替身。 |
| 未执行专项 | 截至该次记录，rc.2 卸载/purge、跨 UID 数据保留、SIGKILL/journal 恢复、无控制台用户安装、独立全新安装矩阵、回滚、快速用户切换、凭据长期到期/撤销、设备删除并发与失败回滚、长时间运行及精确性能没有完整实机证据。 |
| 暂缓范围 | 权限负向、ARM fnOS 和最低 macOS 版本等扩展平台按用户决定暂缓，等待 issue 反馈；不标为已验证。 |
| 正式产物覆盖 | 此次记录不包含 1.0.0 正式产物验收；该次验证时没有历史 1.x 正式产物，未执行历史正式版本互通验证。 |

## RC 安装问题与回归

rc.1 的 `/var/log/install.log` 显示安装器将 App 重定位到工作区中的安装包展开副本，
postinstall 随后因 `/Applications/FnCPN.app` 不存在而失败。
rc.2 显式禁止 `BundleIsRelocatable`，校验生成包没有 relocate bundle 且 App 路径正确。
真实 PKG 测试覆盖正常构建及拒绝可重定位配置；用户安装回归确认通过。修复不删除身份或配置。

| 公开版本 | 源码 commit | 发布流水线 | 核验 |
| --- | --- | --- | --- |
| 1.0.0-rc.1 | `1d85e6795d2cc8fe188ae7d605bb33b8eda622c9` | `37816795546` | 构建及资产核验通过；随后实机发现安装重定位问题。 |
| 1.0.0-rc.2 | `ecc2a5c50d12da35a9361b79c117defcdfe6717c` | `37880665290` | 固定路径、版本、签名、SHA256、许可证和用户安装回归通过。 |

两轮均使用 Go 1.27.1、Xcode 16.4 / SDK 15.5 和 fnpack 1.2.3；各轮三种安装包来自同一提交，
清单记录源码和工具版本。App/helper 最低 macOS 为 13.0，使用 ad-hoc 签名；PKG 未签名、未公证。
远端资产逐字节复核后公开；产物检查不证明其他实机专项通过。

## 历史实机证据

主要环境：x86_64 fnOS 1.2.x、Apple Silicon macOS 27.0；NAS `192.168.71.2`，LAN
`192.168.71.0/24`，overlay `10.253.203.0/24`。接口编号只用于历史追溯。
“用户确认”不补写未提供的日志、耗时或性能数字。

| ID | 验证项 | 版本、结果与依据 |
| --- | --- | --- |
| V-01 | fnOS 全新安装与初始化 | 早期服务端 `0.1.3`：彻底清理后安装成功；普通/root daemon 分工、配置与密钥权限、LAN 识别、`fncpn0`、`ip_forward=1` 及监听检查通过。依据早期终端输出和用户确认。 |
| V-02 | Mac 安装与文件凭据 | 客户端 `0.1.11` 全新安装成功，双 healthcheck 通过；`/var/db/fncpn/credentials/<uid>/` 为 root 所有，目录 `0700`、凭据文件 `0600`。未读取凭据内容，不代表跨用户攻击场景已实机覆盖。 |
| V-03 | Mac 窗口、输入与授权入口 | 早期 `0.1.5` 至 `0.1.8` 修复后，用户分别确认 App 可见、输入框布局正常、直接出现中继登录页；取消、超时等异常分支不在此通过结论中。 |
| V-04 | 会话失效识别与重新授权 | `0.1.13` 日志将 HTTP 200、`invalid token` 明确报告为 `AUTH_REQUIRED`；用户重新授权后建立 RELAY 并完成 WireGuard 握手。不是仅凭授权窗口关闭判定连接成功。 |
| V-05 | 无公网 IPv6 时自动 RELAY | `0.1.13`、`0.1.14` 有完整输出，`0.1.17` 有用户确认；WSS 建立后 WireGuard 握手成功，`privilegedActive=true`、`privilegedDegraded=false`。LOCAL 探测未命中后自动选择中继，无需手选模式。 |
| V-06 | Overlay 与 NAS LAN 地址访问 | `0.1.13` 输出：`10.253.203.1`、`192.168.71.2` 的路由均走 `utun4`，两组 ping 各 4/4 回复、零丢包。 |
| V-07 | NAS 网页访问 | `curl --noproxy "*"` 返回 `HTTP=200 remote=192.168.71.2`；用户确认 `http://192.168.71.2:5666/` 可正常打开。未通过 FN Connect 域名绕过隧道。 |
| V-08 | Docker 环境下跨设备 LAN 转发 | 服务端修复并更新后，客户端 `0.1.13` 输出：网关 `192.168.71.1` 路由走 `utun4`，ping 4/4、零丢包、TTL 63；`0.1.14` 重连后再次 4/4。没有关闭 Docker 或改成全局 FORWARD ACCEPT。 |
| V-09 | 主动断开与路由清理 | `0.1.13` 输出：`fncpn disconnect` 返回 `PAUSED`，特权网络 inactive 且未 degraded；overlay 和 LAN 路由恢复为 `en0` 经 `10.211.55.1`，`utun4` 不存在。 |
| V-10 | 升级保留暂停意图 | `0.1.13` 暂停后覆盖安装 `0.1.14`，仍为 `PAUSED`、`privilegedActive=false`；启动日志保留原设备 ID 和 `auto_connect=false`，未自动建联。 |
| V-11 | 原凭据重连与 INFO | `0.1.14` 执行 connect，无需重新授权；设备 ID 不变，地址仍为 `10.253.203.3/32`，恢复 RELAY 且网关 4/4。日志有配置读取、探测、发现、选路、WSS、网络下发、握手及 connected 节点，共用同一 IPC 请求 ID；单次总耗时约 7.9 秒，不作为性能基准。 |
| V-12 | 离线旧设备删除 | 服务端 `0.1.16` 更新后，启动观察期先显示“状态未知”；用户等待状态变化并确认已完成离线旧设备删除。页面内确认交互可用，不是按同名自动合并设备。 |
| V-13 | 在线设备的页面删除保护 | 保持 `0.1.17` 客户端连接，用户确认管理页显示“在线”，该行删除按钮不可点击；未对在线设备实际提交删除请求。 |
| V-14 | 服务端停用后自动恢复 | 客户端更新为 `0.1.17` 后复测：先正常 RELAY，经 FN Connect 应用中心停用 FnCPN 20 秒再启用；120 秒观察中不手动重试、连接或授权，用户确认结果符合预期。未附该次完整日志，不记录具体丢包数或恢复秒数。 |
| V-15 | Mac 重启后自动连接 | `0.1.17` 保持连接意图，重启目标 Mac；登录桌面后不手动启动连接或授权，用户按诊断和网关探测步骤确认成功。不是只重开 App 窗口。 |
| V-16 | MTU 大包 | `0.1.17` 下对 `10.253.203.1` 和 `192.168.71.1` 分别执行 10 次 `ping -D -s 1252`，载荷加 IPv4/ICMP 头为 1280 字节；用户确认无丢包、符合预期。 |
| V-17 | 实际文件下载 | 用户按 NAS 内网地址进入文件管理下载文件，确认“下载正常”。实际文件大小、耗时和校验和未回传，只确认下载功能，不据此声称吞吐达标、哈希一致或长时间稳定性通过。 |
| V-18 | CLI/Web 会话隔离与管理后台 | `0.1.27` 实机 probe 证明 CLI 新短 token、`user.authToken`、`user.tokenLogin` 恢复及 Web 登录后 CLI token 继续有效；独立 Web token 可访问 FnCPN 网关。现场包中用户确认管理后台免登录进入 `/app/fncpn`，且上游仅携带 `mode + Web fnos-token` 时 `/app/fncpn/api/v1/admin/snapshot` 返回 200。该条记录不包含代码收敛后安装包的 smoke test 结果。 |
| V-19 | 恢复后 Cookie 切换 | 现场日志证明 `user.tokenLogin` 已成功但原 RemoteClient 继续使用旧内存 Cookie，配置重试仍返回 `invalid token`。修复后自动化测试要求第二个 RemoteClient 明确携带恢复后的短 token；该条记录未附修复后实机重启回归结果。 |

历史 Cookie 恢复机制及 CLI/Web 会话研究见 [fnOS 协议研究](fnos-protocols.md)；
各条证据只覆盖注明的版本与场景；网络与配置的用户确认不推导凭据长期失效专项通过。

## 底层方案实测（2026-09-28）

当时使用 fnOS PoC 0.4.1、macOS 0.1.0、外部 wg、端口 51820 和 overlay `10.203.0.0/24`。
PoC 参数仅用于理解该次实测；安装与配置应遵循 [用户指南](../README.md)。

| 技术判断 | 实测证据 |
| --- | --- |
| FN Connect 可承载二进制 WSS | 第三方应用经统一网关和 Unix Socket 完成 Upgrade、双向回显及约 31.6 MiB 传输，未发现内容校验或协议错误。 |
| fnOS Native 应用具备网络管理能力 | UID 0 下 WireGuard/TUN 创建、nftables 创建删除、IPv4/IPv6 forwarding 读取及测试资源清理通过。 |
| WireGuard 身份与地址池可持久化 | 创建 fncpn0，服务端私钥重启不变，设备分配地址并读取握手/RX/TX；地址复用与多设备模型有自动化测试，多台真实客户端同时在线当时未测。 |
| macOS IPv6 UDP 直连可行 | root helper + wireguard-go 建立 utun4，路由和握手通过；基础 ping 5/5，1200-byte payload 100/100，平均 RTT 64.183 ms。 |
| WireGuard over FN Connect WSS 可行 | 本地 UDP bridge 经 WSS 对接服务端；基础 ping 5/5，大包 100/100，平均 RTT 93.127 ms、最大 312.303 ms。仅该次样本，不作性能承诺。 |
| 运输层可独立恢复 | 停止 bridge 时 ping 超时，重新启动后不重建 utun，恢复 ping 5/5。 |
| 停止清理具备幂等性 | 停止后 overlay 路由恢复默认网关，重复 stop 成功。 |
| helper peer credential 边界可行 | 未知 exec 操作被拒绝；nobody 被拒绝；LOCAL_PEERCRED/LOCAL_PEERPID 返回实际连接 UID/PID。旧 CLI 对拒绝错误显示不准确，但 helper 已拒绝。 |

2026-10-02 用户在 fnOS 执行 Linux 隔离网络 v2 测试全部 PASS，覆盖转发/NAT、
Docker DROP、网段限制、幂等、事务失败及清理；正式 Docker LAN 转发另有 V-08 实机证据。
这些结论说明方案可行，不证明其他版本或未覆盖的故障与安全场景通过。


## 1.0.0 协议与数据基线

基线源码由 `v1.0.0` 固定，发布清单记录对应 commit；正式安装包与 SHA256 在同名 Release 归档。
协议样本和数据样本使用该 tag 中的可执行测试夹具，避免保存真实账号、Cookie、私钥或设备信息：

- 远程 bootstrap、版本识别与拒绝行为：`internal/server/version_test.go`、`internal/client/version_test.go`；HTTP/WSS 字段见 [接口契约](interfaces.md)。
- 服务端业务数据 schema 1：`internal/server/store.go` 与 `store_test.go`。
- 客户端配置 schema 2：`internal/client/storage.go` 与 `storage_test.go`，测试生成身份、配置和持久化样本。
- 本地 IPC 版本 4：`internal/ipc/protocol.go` 与 `protocol_test.go`；仅支持成套安装的本地组件。
- 网络运行状态和 cleanup journal 独立于业务数据，按该 tag 下 `internal/platform/darwin`、`internal/platform/linux` 的序列化实现与测试保留。

后续兼容验收应使用这些固定历史源码与正式产物，不以修改测试中的版本字符串代替历史实现。
rc.2 安装已获用户确认；1.0.0 的重新构建和产物检查单独记录，不推导正式包已在目标机器安装。


## 1.0.0 构建与发布核验（2026-10-09）

- 源码 commit：`478c4e6cb1076071e903c11823a59fa004f364de`，tag：`v1.0.0`；发布流水线 `37926567426` 通过。
- 三种正式安装包从同一提交重新构建，发布清单与 SHA256 匹配；GitHub Release 已公开为稳定版并设为 Latest。
- macOS PKG 版本、App 产品版本和 CLI 均为 `1.0.0`，build number 为 `23`；固定 App 路径、无 relocate bundle、ad-hoc 签名、macOS 13 deployment target 和许可证检查通过。
- fnOS 两种 FPK 的版本、平台、ELF CPU 架构与许可证检查通过。
- 正式包未在目标 Mac/fnOS 上重新安装；rc.2 的用户安装确认独立保留。本轮 CLI version smoke test 与产物核验不记为正式包实机连接通过。


## fnOS 1.0.0 卸载后重新安装故障（2026-10-09）

只读核对 NAS 日志：20:12:06 安装回调以 fncpn 用户打开 `logs/server/server.log` 时返回
`Permission denied`，安装失败；20:12:37 重试安装成功，20:12:47 服务启动成功。
两次的应用 UID 均为 978；失败时的目录权限已被重试替换，未确认具体是哪一级目录、ACL 或组权限阻止访问。

1.0.1 的安装回调改为 root 创建日志、设置明确属主与 0600 权限，不依赖安装阶段普通用户写入。
回归测试覆盖切换用户不可用、已有内容保留及权限收敛、日志属主设置失败和不安全日志路径拒绝。
该测试不等于 fnOS 实机重新安装通过；未在 NAS 修改文件、停服或重装。


## 1.0.1 构建与发布核验（2026-10-09）

- 源码 commit：`15bed1aaa5ba6532e89d51f86cd2e29148130b6c`，tag：`v1.0.1`；发布流水线 `37930012922` 与 main CI 通过。
- 三种安装包清单 commit 与 SHA256 核验通过；两个 FPK 内的安装脚本与该提交源码逐字节一致，版本、ELF 架构和许可证核验通过。
- macOS 产品/PKG 版本及 CLI 为 1.0.1，build number 为 25；固定路径、无 relocate bundle、ad-hoc 签名和 macOS 13 target 核验通过。
- 已公开为 Latest 稳定版，Homebrew Cask 跟随 1.0.1；未覆盖 1.0.0 产物。
- 未在 NAS 写文件、停服或重装；卸载后重装的 fnOS 实机回归仍未执行。本轮记录为自动化和产物核验通过。


## Homebrew 1.0.0 → 1.0.1 升级（2026-10-09）

用户回传完整终端输出：直接执行 `brew upgrade --cask rectcircle/fn-connect-private-network/fncpn`，
卸载脚本保留用户配置、日志与凭据，系统命令行 installer 安装成功，Homebrew 记录升级成功。
该次升级未要求预先打开 PKG 或移除隔离标记；不推导其他系统版本、首次安装或升级后连接已验证。


## Homebrew 1.0.1 卸载后重新安装（2026-10-09）

用户在卸载后直接使用 Homebrew 安装 1.0.1，确认“可以安装成功”；该次回归未要求预先打开 PKG 或处理隔离标记。
项目 purge 卸载脚本删除自身后，普通 `brew uninstall` 因脚本不存在失败；使用 `--force` 清理 Brew 记录后完成重新安装。
该记录为用户确认，未附安装日志；不推导所有用户数据均已逐项核验清空、连接功能或 fnOS 重装已验证。

## fnOS 1.0.1 启用故障与 1.0.2 回归（2026-10-09）

用户提供的平台日志记录两次启用失败：特权 daemon 已就绪，普通 server 打开
`logs/server/server.log.crash` 返回 `Permission denied`，随后 app.sock 健康检查失败。
日志未证明该文件的具体属主。1.0.1 仅初始化主日志，且升级回调和启用不执行安装初始化。

1.0.2 在安装及停止旧进程后的启动阶段统一准备两端主日志、崩溃日志及各五份轮转文件。
回归执行实际脚本函数，覆盖启动顺序、所有日志内容保留、权限收敛、逐文件属主调用、
不触及身份存储，以及目录、崩溃日志和轮转文件的符号链接拒绝。
测试以替身记录 chown，不等于目标 fnOS 上的实际属主和 ACL 验证；NAS 未执行写操作，
新包实机启用结果需单独记录。

## 1.0.2 构建与发布核验（2026-10-09）

- 源码 commit：`0fcb0a89f3a822e55ee17a8bd2d82386faee7b12`，tag：`v1.0.2`；发布流水线 `37933160822` 与 main CI `37933161162` 通过。
- 三种安装包版本、清单 commit、SHA256 核验通过；两个 FPK 的 cmd/main 与源码逐字节一致，ELF 架构及许可证核验通过。
- macOS PKG 和产品版本为 1.0.2，build number 为 27；CLI version、固定安装路径、无 relocate bundle 及 App 的 codesign 深度校验通过。
- 远端五份资产逐字节复核后公开为 Latest 稳定版，Homebrew Cask 更新至 1.0.2；未覆盖历史版本。
- 本轮未在 NAS 写文件、停服、升级或启用；产物核验和自动化通过不代表 fnOS 实机启用已通过。


## fnOS 1.0.2 启用后的状态权限故障（2026-10-09）

用户日志确认 1.0.2 已通过日志初始化，读取 control/state.json 返回 Permission denied。
namei 输出显示 control 归 fncpn 所有、0700，state.json 归 root:root 所有、0700；
fncpn UID 为 978。由此确认普通 server 无法读取该已有文件；文件为何变为 root 属主尚未确认。

修复候选在安装和停止旧进程后的启动阶段修复已存在的 control/state.json、probe.key 属主与权限，
保留全部内容，并分别保持 privileged/server.key、network-state.json 为 root 私有。
回归覆盖已存在文件的 0700 权限收敛、逐文件属主调用、内容保留及不安全路径拒绝；
chown 使用替身，未记为实际跨 UID 或 NAS 实机启用通过。实机启用结果需另行记录。

## 1.0.3 构建与发布核验（2026-10-09）

- 源码 commit：`0b74a7208c8d3436c101af2ef659839e6481c49a`，tag：`v1.0.3`；发布流水线 `37935906843` 与 main CI `37935907565` 通过。
- 两个 CI 流水线均执行真实权限回归：在 macOS runner 临时目录创建 root 所有、0700 的已有状态文件，确认 nobody 不可读；执行实际 prepare_state 后确认普通用户可读、内容保留、目录可写，特权密钥和目录仍不可访问。仅对 runner 的 nobody/root 组名做平台映射，不使用 chown 替身；不等同于 fnOS ACL 或目标 NAS 实机启用验证。
- 三种安装包版本、源码 commit 和 SHA256 核验通过，两个 FPK 的 cmd/main 与源码逐字节一致，ELF 架构及许可证核验通过。
- macOS 产品/PKG/CLI 版本为 1.0.3，build number 为 29；固定路径、无 relocate bundle 和 App 的 codesign 深度校验通过。
- 远端五份资产逐字节复核后公开为 Latest 稳定版，Homebrew Cask 更新至 1.0.3；历史公开版本未覆盖。
- 本轮未在 NAS 写文件、停服、升级或启用；新包实机启用结果需单独记录。

## 1.0.10 构建与夸克镜像核验（2026-10-15）

- 源码 commit：`ed73f7f83e531c2aa37ec6f963f8e68e4c79602c`，tag：`v1.0.10`。
- [草稿构建 38602567019](https://github.com/rectcircle/fn-connect-private-network/actions/runs/38602567019) 成功：Windows ARM64/x64 构建、macOS/fnOS 发布检查、原始资产上传及逐字节复核通过。
- 公开前独立下载草稿，验证全部 8 个资产 SHA256、清单及 tag commit；展开 macOS ARM64/x64 包确认架构，ARM64 内嵌 CLI 输出 `1.0.10`。未据此宣称完成真实安装、运行或历史数据迁移验收。
- [公开发布 38605607575](https://github.com/rectcircle/fn-connect-private-network/actions/runs/38605607575) 全部成功：本地 `dispatch` 检查授权，将当前账号最小配置通过标准输入同步为 `QUARK_AUTH` Secret，公开原始草稿后并行更新 Homebrew 与夸克。
- Ubuntu runner 使用固定版本 CLI（仅运行文件，无 Skill），成功恢复本地授权并上传全部 8 个文件到未分享的暂存目录；日志中 6 个安装包、发布清单、校验和全部逐项 `Verified:`，最终输出 `Verified mirror: /releases/fncpn/v1.0.10 (8 files)`。
- 校验完成后版本目录移动至 `/releases/fncpn/v1.0.10/`；通过原永久分享确认版本目录可见，目录内为完整 8 个文件。Homebrew 自动更新提交为 `e604993`。
- 本地新增 18 项镜像回归测试、完整 `check-release.sh` 和工作流 actionlint 静态检查通过。回归覆盖部分上传恢复、内容冲突、已公开镜像不可覆盖、校验前不公开、授权检查失败不传 Secret、凭据不进入 workflow input/错误输出及 CLI 压缩包校验。
- 异常保留：[主分支 Checks 38602543466](https://github.com/rectcircle/fn-connect-private-network/actions/runs/38602543466) 的 macOS job 显示 `check-release.sh` 步骤失败，但整体长期保持运行中，日志接口返回 404；取消及强制取消请求后仍未能获取失败日志。发布依据为同 commit 的 Release 校验成功、本地完整检查及独立草稿资产核验，不将该 Checks 记为通过，根因待排查。
- 未覆盖：令牌自然到期后的跨机器刷新及轮换、长期无人值守、切换账号/重建目录、该版本全平台真实安装与历史正式版本互通专项。此次变更仅为分发流程，不改变协议、IPC 或持久化 schema；不据此扩大已有产品兼容验证范围。

## 网络恢复验收边界

从原网络恢复设计文档归并的待验收范围；本次文档整理未执行测试或新增实机验证，历史结论仍以对应版本记录为准。

- 网络场景须覆盖 Wi-Fi/Ethernet 切换、睡眠唤醒、NAS 冷启动、弱网、全隧道 VPN 和多网卡路由，观察连接恢复、健康路径保留及暂停意图。
- macOS 权限须覆盖首次弹窗、拒绝、延迟允许、跳过等待及在系统设置中修改权限；unsigned/ad-hoc 构建升级后须复测系统身份变化的影响。
- 自动化覆盖不能替代上述系统验收；应用层预算不能证明所有环境下的实际连接耗时。测试入口见 [网络恢复检查](development.md#网络恢复检查)。
