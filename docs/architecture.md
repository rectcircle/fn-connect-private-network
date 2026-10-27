# 技术架构与核心决策

以源码为依据，产品行为见 [产品需求](product.md)，远程与本地契约见 [接口](interfaces.md)。
客户端共享 Go 连接核心，以平台适配层接入原生界面、IPC 和网络能力。macOS 与 Windows 在特权组件内嵌 wireguard-go，Linux 客户端与 fnOS 使用内核 WireGuard；不随包启动外部 wg/bridge 工具。

## 总体架构

```text
客户端（macOS、Windows、Linux；共享业务核心）
┌──────────────────────────────┐
│ 平台原生 UI / fncpn CLI       │
│ 展示、原生登录表单、用户命令  │
└──────────────┬───────────────┘
               │ 用户私有 IPC
┌──────────────▼────────────────┐
│ fncpn client daemon           │
│ 状态机、选路、配置、凭证、WSS  │
└──────────────┬────────────────┘
               │ 受限特权 IPC
┌──────────────▼────────────────┐
│ fncpn client privileged-daemon│
│ 系统特权；WireGuard、网络配置 │
└──────────────┬────────────────┘
               │ IPv6 UDP / FN Connect WSS
               ▼
fnOS
┌──────────────────────────────┐
│ fnOS 统一网关                 │
└──────────────┬───────────────┘
               │ Unix Socket
┌──────────────▼────────────────┐
│ fncpn server daemon           │
│ 普通权限；HTTP/WSS、设备与配置 │
└──────────────┬────────────────┘
               │ 受限特权 IPC
┌──────────────▼────────────────┐
│ fncpn server privileged-daemon│
│ root；WireGuard、转发、防火墙   │
└───────────────────────────────┘
```

客户端数据面由系统特权组件持有 WireGuard（macOS/Linux root / Windows LocalSystem）；RELAY 的 WSS 连接由普通 client daemon 持有，通过 loopback UDP 转发密文；LOCAL 不创建隧道。图中的进程关系不表示 root 进程发起 WSS。

### 共享核心与平台边界

客户端分为呈现控制、共享连接核心和系统适配三层。桌面图表示进程隔离方式，
不是移动端必须复制的部署结构。所有平台复用 `internal/client` 的状态机、授权、
配置同步、选路、DNS 业务及中继，`internal/clienthost` 提供嵌入式宿主；平台层实现窄接口。

```text
呈现与控制：桌面原生 UI / 移动 Fyne UI / CLI
                         ↓ 平台控制接口
共享连接核心：clienthost → Service / Manager
              授权、状态机、选路、配置同步、DNS、RelayBridge
                         ↓ 网络与系统窄接口
系统适配：生命周期、凭据、WireGuard/TUN、路由、DNS、网络事件
```

### 平台运行模型

| 平台 | 界面与控制 | 核心承载及权限边界 | 网络与凭据 |
|---|---|---|---|
| macOS | AppKit / WebKit，可选 Wails；CLI | 用户 LaunchAgent 与 root LaunchDaemon，Unix Socket 校验身份 | wireguard-go / utun；loopback DNS + resolver；Keychain |
| Windows | 原生 UI / WebView2；CLI | 用户 daemon 与 LocalSystem 服务，Named Pipe 校验身份 | wireguard-go / Wintun；NRPT；系统凭据 |
| Linux | Wails / GTK3 / WebKitGTK；CLI | systemd 用户服务与按 UID 隔离的 root 服务，Unix Socket | 内核 WireGuard / netlink；systemd-resolved；用户凭据存储 |
| Android | Fyne + Java/JNI | VpnService 在应用中持有 clienthost，单 Go runtime；无需 root 或桌面双 daemon | 系统 VPN/TUN、路由、DNS；Keystore |
| iOS / iPadOS 真机 | Fyne + 原生桥 | App 与 Packet Tunnel 独立进程；扩展独占活动 clienthost，通过 Provider 消息控制 | wireguard-go、packetFlow、系统网络设置；Keychain |
| iOS 模拟器 | 同一 Fyne UI及网络调试入口 | App 内唯一 clienthost；编译标签隔离，不启动系统 Packet Tunnel | 用户态 netstack；仅显式应用内 HTTP/DNS 请求入隧道 |

Linux 不修改物理接口、转发或主机防火墙。Android LOCAL 名称解析可以保留 DNS 用 TUN，
不运行 WireGuard。平台支持和实测边界见 [支持范围](platform-support.md) 与 [发布验收](validation.md)。

### iOS / iPadOS 承载关系

```text
App 进程：Fyne UI → iosui / iosClient → NETunnelProviderSession
                                      │ Provider 消息
Packet Tunnel 进程：Swift Provider → C bridge → clienthost / Service / Manager
                                                   ↓ iosvpn.Engine
                          packetFlow、网络设置、Keychain、wireguard-go
```

`mobile/ioscore` 编为单份 C archive；连接、配置和凭据由扩展单写，App 不持有第二个
活动 Manager。Swift 负责系统生命周期和系统回调，共享核心负责业务。DNS netstack
将匹配后缀的 UDP/TCP 请求转交共享 loopback DNS，平台不重新实现 DNS 业务。
App 与扩展均不依赖 App Groups；配置在扩展沙盒，日志通过受限 Provider 消息同步为 App 快照。

模拟器使用 `fncpn_ios_simulator`，复用相同核心与 Engine，配置及 Keychain 命名空间独立。
HTTP 通过 `Engine.DialApplication`、DNS 通过共享 listener 显式访问；Safari、WebView、
其他 App 与系统路由/DNS 不进入该调试隧道。它不代替真机系统 VPN 验收。

### 职责与扩展约束

UI 只展示状态和提交命令，不直接配置网络。业务核心通过接口使用系统能力，
不得依赖 Fyne、UIKit 或 Java。平台宿主负责进程、系统扩展及权限模型，不复制连接状态机。
桌面 privileged-daemon 只接受结构化网络计划，不处理浏览器输入或账号凭据；
server 普通进程承载 HTTP/WSS、设备与配置，root 进程负责 WireGuard、转发和防火墙。
新增平台实现这些边界即可复用核心，不要求使用桌面 IPC 或启动桌面 daemon 可执行文件。

## HarmonyOS 承载与多窗口

`apps/harmonyos/entry` 承载 ArkUI 页面、组件、窗口控制和系统适配；`apps/harmonyos/go`
提供共享库与私有 Unix socket，`internal/platform/harmonyos` 负责 TUN、WireGuard、分流 DNS
和物理网络绑定。UI → 异步 N-API → Go IPC 客户端 → 私有 `control.sock` → VPN 扩展中的
clienthost/Manager；UI 不启动第二个连接状态机。扩展通过 N-API 主线程回调调用系统 VPN、
网络事件和 AssetStore，避免同步阻塞 ArkTS 主线程。

数据面为系统 TUN → Go WireGuard → 经 protect 的物理 UDP socket → NAS；中继复用共享核心。
私有 DNS 由平台适配处理，控制面 HTTPS/WSS、直连和 DNS 上游不得回环进入 VPN。
原始系统 TUN fd 与 Go dup fd 独立管理；替换时先脱离并关闭 Go 副本，再销毁和重建系统接口，
失败按事务清理/回滚，避免重复 VPN 和泄漏描述符。身份配置在私有目录，凭据通过 AssetStore。
本地桥接版本为独立的 1，组件随 HAP 成套升级，不新增远程协议或 server 要求。

手机、折叠屏、平板、PC/2in1 共用组件与业务状态。840vp 起且正文宽度充足时默认嵌入256vp侧栏，
窄窗口使用抽屉；字体放大时回退，侧栏展开收起带动画并推动正文。登录内容达到640vp时可双栏，
连接/诊断按更高阈值排列；缩放、折展、分屏不重建连接。半折叠保守避让到上/左安全窗格。
手机保留 Android/iOS 的三行 slogan、登录表单、主次按钮和菜单结构；英文布局通过输入类型请求，
普通应用不能强制切换第三方输入法的中英文状态。登录失败留在表单，错误位于按钮上方。

PC/2in1 管理后台用内部 singleton `AdminAbility` 独立主窗口，加载共享本地代理 URL；
重复打开通过 onNewWant 更新窗口 LocalStorage，页面显式使用共享存储，关闭不影响 VPN。
账号/NAS 改变或授权失效会关闭旧管理窗口；不复制凭据、不直连 NAS 公网地址。
窗口先加载页面，标题栏/最小尺寸等可选设置失败独立容错并记录启动日志。
普通退出保留连接意图，显式断开才暂停自动恢复；模拟器产品仅运行真实应用内隧道。


## Go 业务包设计

`fncpn` Go 程序承载除原生 UI 以外的全部核心业务逻辑，不只是供 UI 调用的工具库。各平台 UI 仅负责展示、收集登录输入和用户操作，并调用本地 IPC；fnOS 管理页通过网关 HTTP API 操作服务端。

同一代码库按目标平台构建一个 `fncpn` 可执行文件，不同职责通过子命令启动。内部 package 按业务领域和平台边界命名，不设置 `core`、`common` 或 `utils` 等宽泛的中间层。

| Package | 职责 | 输入 / 输出 |
|---|---|---|
| `internal/model` | 客户端状态、服务端配置、设备、网络计划和稳定错误码 | 跨业务边界共享的数据契约 |
| `internal/peer` | 已认证的本地进程身份及其 context 传递 | IPC 识别的本地调用方身份 → 权限边界 |
| `internal/client` | 授权、配置同步、自动选路、连接状态机和 WSS bridge | 用户意图 + 网络事件 → 客户端状态 |
| `internal/server` | 服务端设置、设备、地址池、HTTP API、中继和配置版本 | 管理请求 → 服务端期望状态 |
| `internal/ipc` | framed JSON、请求响应与平台 IPC（Unix Socket / Named Pipe） | 本地进程请求 ↔ 结构化响应 |
| `internal/dns` | DNS 消息编解码、域名校验、客户端转发与服务端解析链 | DNS 查询 ↔ 结构化解析结果 |
| `internal/privileged` | client/server 特权操作白名单、客户端凭据文件和计划调度 | 已校验请求 → 网络适配或受限凭据存储 |
| `internal/platform/darwin` | utun、路由和系统事件适配 | 网络计划 ↔ macOS |
| `internal/platform/windows` | Wintun、IP Helper、路由和网络事件适配 | 网络计划 ↔ Windows |
| `internal/platform/linux` | netlink、内核 WireGuard；服务端另负责转发和 nftables | 客户端/服务端期望状态 ↔ Linux |
| `internal/command` | 子命令解析、进程组装和生命周期入口 | 命令行 → 对应进程职责 |

### Go 实现约束

- package 名称必须表达具体领域或平台职责，禁止创建承载无关逻辑的 `core`、`common`、`shared` 或 `utils` package。
- `internal/client` 和 `internal/server` 可以依赖 `internal/model` 和 `internal/dns`，但不能相互依赖。
- `internal/model` 只保存跨边界稳定契约，不放文件、网络、日志或进程操作。
- `internal/platform` 只能实现上层定义的窄接口，不能持有产品状态机。
- 不执行 shell，不拼接系统命令。
- 不依赖全局变量表达当前连接，状态由单一 session 实例拥有。
- 发现、HTTP、时间和平台网络操作通过窄接口注入，便于确定性测试。
- 错误必须归一化为稳定错误码，同时保留可记录的底层 cause。
- 关键状态变更输出明确 error 日志，但日志字段必须脱敏。
- 所有 apply 操作要么完整成功，要么按逆序回滚到旧状态。
- 源码声明的 Go 基线为 `1.27`；发布构建应固定工具链 patch 并记录实际版本。
- HTTP、JSON、Cookie、加密、进程管理、日志和 IPC 优先使用 Go 标准库。
- 标准库不提供或自行实现风险明显更高的能力才引入外部依赖。

## 进程与二进制依赖

### 单一二进制与子命令

macOS、Windows、Linux 客户端和 fnOS 分别构建适配自身平台的 Go 核心可执行文件（Windows 为 `fncpn.exe`）。同一平台的不同职责复用该文件，由 LaunchAgent、LaunchDaemon、Windows 服务、systemd 或 fnOS 生命周期脚本以不同子命令启动；原生 UI 和平台驱动依赖单独打包。

| 子命令 | 平台 | 运行身份 | 职责 |
|---|---|---|---|
| `fncpn client daemon` | macOS / Windows / Linux | 当前用户 | 客户端核心业务和用户 IPC |
| `fncpn client privileged-daemon` | macOS / Windows / Linux | root / LocalSystem | WireGuard（Linux 为内核实现）、平台虚拟接口、路由和 DNS 配置 |
| `fncpn server daemon` | fnOS | 应用专用普通用户 | HTTP/WSS、设备和配置管理 |
| `fncpn server privileged-daemon` | fnOS | root | 内核 WireGuard、转发和防火墙 |
| `fncpn status/connect/...` | macOS / Windows / Linux | 当前用户 | 用户 CLI，通过 IPC 控制 client daemon |

各平台原生 UI 独立于 Go 核心二进制：macOS 使用 AppKit、ServiceManagement、UserNotifications 和 WebKit；Windows 使用原生 Windows UI 与 WebView2；Linux 使用 Wails v2 与 GTK3/WebKitGTK。

### WireGuard 集成

- `wireguard-go` 可以通过 `golang.zx2c4.com/wireguard/device`、`tun` 和 `conn` 包直接链接到 `fncpn`。
- 客户端 privileged-daemon 在进程内创建 TUN 和 WireGuard device，不再启动 `wireguard-go` 子进程。
- `wg` 是 wireguard-tools 提供的 C 命令，不能作为 Go 包直接链接；正式实现不携带、不释放也不执行 `wg`。
- privileged-daemon 直接调用 WireGuard device API/UAPI 写入私钥、peer、endpoint、AllowedIPs 和 keepalive。
- WireGuard 源码依赖固定到明确版本，并在内部适配层隔离其 API 变化。

### 平台网络实现

- macOS 客户端在进程内运行 WireGuard，并由 privileged-daemon 使用固定绝对路径调用系统自带的 `/sbin/ifconfig` 和 `/sbin/route`。
- Windows 客户端通过 Wintun 创建虚拟接口，使用 Windows 网络 API 配置地址、路由和网络事件，由 LocalSystem 服务执行特权操作。
- 系统命令必须通过 `exec.CommandContext` 直接调用，不经过 shell；命令路径、操作集合和参数结构固定，所有动态值来自已复验的结构化计划。
- 1.0.x 不修改系统 DNS；访问远端资源使用 IP。1.1.0 起局域网域名解析采用 split DNS，
  设计见 [局域网域名解析（Split DNS）](#局域网域名解析split-dns)。
- fnOS 使用 Linux 内核 WireGuard、Go netlink、wgctrl 和 Go nftables 库。
- fnOS 不依赖系统中的 `ip`、`wg`、`nft` 或其他第三方 CLI。
- 若内核 WireGuard 不可用，P0 明确报错，不静默下载或执行未知二进制。

### 外部依赖原则

允许的外部依赖必须满足“标准库没有等价能力”或“自行实现安全风险明显更高”：

| 能力 | 依赖原则 |
|---|---|
| WireGuard 协议与 macOS TUN | 使用官方 `wireguard-go` Go 包 |
| Linux WireGuard 配置 | 使用 `wgctrl` |
| Linux netlink / nftables | 使用成熟 Go 库，不调用外部命令 |
| WebSocket | 使用维护活跃的小型 Go 库，避免自行实现协议 |
| 系统调用 | 优先标准库，其次 `golang.org/x/sys` |

每个新增依赖必须说明必要性、维护状态、许可证和替代方案。禁止仅为少量辅助函数引入大型框架。

### 版本规则

产品版本、本地 IPC 与持久化 schema 分别管理；升级、备份与回滚以 [版本与兼容性](versioning-and-compatibility.md) 为准。

## 数据存储设计

### macOS 当前用户

| 数据 | 位置 | 权限与规则 |
|---|---|---|
| WireGuard 私钥 | `/var/db/fncpn/credentials/<uid>/<hash>.secret` | 普通 daemon 通过特权 IPC 存取；root 属主，文件 `0600` |
| FN Connect 凭证集 | 同上，使用独立的凭据类型 | 按 UID、FN ID 隔离；更新时原子替换，不写入日志或普通配置 |
| 服务端与设备配置 | `~/Library/Application Support/FnCPN/` | 目录 `0700`，文件 `0600`，采用临时文件加原子重命名 |
| 运行时 socket | 用户私有运行目录 | 目录 `0700`，socket `0600`，退出后删除 |
| 日志 | `~/Library/Logs/FnCPN/` | 轮转、限制大小、默认脱敏 |

### macOS root 状态

- `/var/db/fncpn/state.json` 只保存活动接口、精确地址/路由、owner token、进程实例和
  当前控制台用户等回滚信息。
- 目录归属 `root:wheel` 且权限 `0700`。
- `credentials/` 及每个 UID 子目录均为 root 所有、`0700`；文件为 `0600`，
  临时文件同样受保护，写入后 fsync、原子重命名并同步目录。
- 存取拒绝符号链接、错误属主、宽权限和超限文件。凭据最多 64 KiB。
- 凭据文件不提供额外磁盘加密或应用签名隔离；IPC 按当前控制台 UID 授权，
  不能防御同一用户下通过 IPC 读取凭据的进程或已取得 root 权限的进程。
- 默认卸载保留所有用户凭据；`--purge-user-data UID` 仅删除指定用户的凭据、
  配置和日志。不承诺迁移开发期凭据格式；保留受支持格式身份时重新授权复用原密钥。
- 重启或异常退出后，client privileged-daemon 根据状态执行幂等清理。

### fnOS 服务端

| 文件 | 内容 | 规则 |
|---|---|---|
| `server.key` | 服务端私钥 | 仅 server privileged-daemon 可读；`0600`，原子创建，升级保留 |
| 普通 daemon 的 `state.json` | overlay、端口、LAN 策略及完整设备列表 | 严格 schema；单文件原子替换；由业务 Service 串行提交并发布完整快照 |
| 服务日志 | 生命周期、配置、连接和错误 | 不记录私钥、Cookie 和完整请求 Header |

不迁移开发期 settings/devices/transaction 拆分文件；缺少 `state.json` 而存在旧业务文件时明确拒绝初始化，不静默丢弃身份。所有业务写入只更新 `state.json`。原子重命名为提交点；提交前失败回滚网络，
重命名后目录同步失败仍以新状态为准并报告错误。

Linux root 只持久化 `server.key` 和最小 cleanup journal。journal 记录带随机 owner token
的 WireGuard 接口 alias 与专属 nftables table，不保存完整 `ServerPlan`。
当宿主机存在 iptables-nft 的 `ip filter / DOCKER-USER` 链时，FnCPN 仅在该扩展链中
维护带 `<firewallTable>:forward` 注释的规则，限定隧道接口、overlay 源/目标和已启用
的 LAN 源/目标。规则放在现有管理员规则之后、无条件 `RETURN` 之前，不修改
Docker 的全局策略或其他链。更新和清理与专属表在同一 nftables 事务提交；
即使专属表已被删除，仍根据 journal 中的表名清理自己的扩展规则。
现有每分钟网络 reconcile 会重新同步这些规则，不新增独立后台循环。
FnCPN 会按需
启用系统 IPv4 forwarding，但停止或卸载时不自动关闭这一全局能力，以免破坏运行期间
开始依赖它的其他服务。

运行日志采用结构化 JSON，单文件上限 10 MiB、保留 5 份。macOS 用户 daemon 写入
`~/Library/Logs/FnCPN/client.log`，macOS root daemon 写入
`/var/log/fncpn/client-privileged.log`，fnOS daemon 写入
`${TRIM_PKGVAR}/logs/` 下相互隔离的目录。

## 网络检测与恢复

### 统一调度，保留用户意图

自动恢复、手动检测、配置更新、特权组件生命周期和鉴权恢复由同一协调器调度，网络变更与身份操作串行执行。这样可以避免多个触发源同时修改网络或争用凭据。重复请求合并，真实换网使旧任务失效并取消；只有当前网络代次的结果可以提交，防止迟到结果覆盖新连接。

断开、退出登录或忘记身份先取消恢复任务，再清理网络。后台检测、权限恢复和迟到回调都不能重新开启用户已关闭的自动连接。

### 根据物理网络重新评估

系统通知作为重新观察的信号，合并重复事件，并用快照轮询补偿遗漏。网络身份依据物理接口、地址前缀和平台网络标识，排除自有虚拟接口及 IPv6 临时地址轮换，避免自身网络配置触发重连循环；快照读取失败不能当成网络已消失。

多网卡环境按端点与物理网络的前缀关系选择探测接口，但网段匹配只决定走哪张网卡。进入 LOCAL 仍须通过 NAS 的 HMAC 身份证明及版本准入，不能凭地址可达就信任目标。

### 先评估路径，再变更连接

路径优先级为 LOCAL → IPv6 DIRECT → FN Connect RELAY。优先验证缓存的已认证端点，必要时并行刷新端点、发现信息和配置，使公网暂时不可达时仍可恢复局域网访问。

网络身份、计划和健康状态未变时复用有效配置及现有连接。健康维护持续尝试更优路径，但升级探测失败不拆除健康的 DIRECT/RELAY；WSS 自身重连与完整隧道恢复分别处理，避免传输恢复期间反复重建网络。

### 有界恢复与取消

一次评估及其各阶段均有预算，DIRECT 为 RELAY 和清理预留时间，特权调用及握手继承剩余预算，避免单个阶段无限等待。具体超时值由源码与测试维护，不作为任意环境下的连接耗时承诺。

取消须传播到特权进程；正常清理继承调用方取消状态，失败回滚使用独立短预算，以收拢已经应用的部分状态。失败按指数退避并由定时器重试，不依赖下一次系统通知；真实换网和手动检测可绕过旧网络的退避，取消失效任务不增加故障退避。

### macOS 局域网权限

启用 CGO 的 macOS daemon 通过 Network.framework 观察实际 NAS 端点。只有系统明确报告局域网权限被拒绝才显示权限等待，普通连接失败和 Bonjour 自发现均不能代替权限证据；TCP 可达也不能代替身份与版本校验。

权限等待独立于普通 LOCAL 探测预算且有上限，用户可选择“继续远程连接”。该操作只跳过当前等待，不修改配置、自动连接意图或远程准入规则。允许权限后重新评估路径，已暂停的连接保持暂停；换网、端点变化、退出和取消均按生命周期释放或重建观察器。

恢复相关测试入口见 [开发文档](development.md#网络恢复检查)，实际结果与待验收范围见 [验证记录](validation.md#网络恢复验收边界)。

## fnOS 控制请求与管理后台

fnOS 接口、签名及原生网页行为见[协议参考](fnos-protocols.md)。以下记录本项目的选路、
会话生命周期与兼容决策，复用既有 fnOS 接口，不增加服务端要求。

### 控制请求选路

| 连接状态 | 优先路径 |
| --- | --- |
| LOCAL | 经身份验证的 NAS 局域网 IP，使用物理网络 |
| IPv6 DIRECT / RELAY | 经已建立的隧道访问 NAS 私网 IP；RELAY 数据仍经过中继 |
| 尚未连接、缺少端口或直达不可用 | FN Connect |

配置监听、版本检查、CLI/Web 登录与认证恢复、后台代理及活动 WebSocket 共用此选路。
核心链路为：**建立连接 → 验证 NAS 身份和路径 → 选择 Web 入口 → 携带对应会话凭据请求**。
路径切换不替代 token 鉴权、权限检查或版本准入。

- 端口来自发现响应的 `httpPort` / `httpsPort`，独立缓存并限频刷新，不写死默认端口。
  缓存不含凭据，缺失时沿用 FN Connect，忘记身份时删除。
- 通过 `/api/v1/probe`（404 时兼容 `/probe`）验证 HMAC 证明及服务端版本，每分钟复核。
  隧道入口必须位于实际 AllowedIPs 内；iOS 使用 Packet Tunnel 专用传输。
- 优先已发现的 HTTP 入口；HTTPS 保留证书及域名验证，只接受已发现的同一 NAS HTTPS
  入口，不携带凭据跟随任意重定向。
- 网络错误时读请求可回退；写请求只有明确在拨号阶段失败且正文可重建时才能回退，
  不重复执行结果不明的写请求。认证拒绝和普通业务错误不触发网络回退。
- 网络或 NAS 切换时丢弃旧入口，取消旧配置监听及 Web 活动连接。初始发现、未建联访问
  和 RELAY 数据通道仍可能使用 FN Connect。

### 配置长轮询

配置接口为 `GET /app/fncpn/api/v1/devices/<device-id>/config?watch=1&after=<cursor>`，
是 HTTP 长轮询。cursor 变化时立即返回配置；无变化最多等待 25 秒后返回
`{"changed":false,"cursor":"<cursor>"}`，客户端随后继续监听。每轮先检查 `/version`，
监听 HTTP 超时至少 30 秒。后台状态接口及完整字段见[接口契约](interfaces.md)。
这些等待时间属于 FnCPN 实现，与 fnOS token 有效期无关。

### 管理后台会话

Web 登录及 OTP 固定请求 `stay: 1`。FnCPN 保存独立 Browser 身份、短 token、longToken 和解密后的签名密钥，使用系统安全
凭据存储，不保存密码、不借用 CLI 凭据。核心恢复链路为：**探测或代理收到明确认证拒绝
→ 合并并发恢复 → 使用原 Browser 身份换取新 token → 比较并保存会话 → 重试请求一次**。
退出、忘记身份或新登录后，旧恢复结果不能覆盖当前凭据。

明确收到 fnOS `invalid token` 后直接使用 `user.tokenLogin`，普通 WebSocket 认证仍保留
`user.authToken`。网络、TLS、超时、5xx 和访问码挑战保留凭据；长期授权明确失效或旧会话缺少恢复凭据时
才要求重新登录。代理只对网关的精确拒绝重试，可重建正文最多缓冲 1 MiB；普通应用错误
和结果不明的写请求不重放。CLI 配置监听等待真实恢复结果，排队本身不作为连接错误。

后台使用期间维护 Browser WebSocket，15 秒 ping、每分钟 `user.active`；最后一次后台
请求后空闲两分钟、代理关闭、退出或 token 替换时停止。页面轮询计入活动。

Web 会话 schema 2 增加可选长期凭据，保留 schema 1 原样读取；旧会话到期后需重新登录
一次。配置、CLI 凭据及 IPC 不变；回滚应恢复配套凭据备份或重新登录。此修复不增加
服务端要求。恢复和选路的观测方法见[诊断](diagnostics.md)。

## 局域网域名解析（Split DNS）

1.1.0 DNS 为随连接自动生效的内建能力，无管理员启用步骤；完整契约见
[局域网域名解析](interfaces.md#局域网域名解析110)。

```text
系统解析器 → 本机 loopback DNS（client daemon，UDP/TCP）
                           │ LAN HTTP 54790
                           ▼
                     server daemon
               去除隔离后缀 → LAN DNS / mDNS
```

- `<NAS hostname>.<后缀>` 为内建 NAS 名称，直接解析到已启用 LAN 内的 NAS 本机地址；管理页显示真实默认后缀，
  并按实际管理协议与端口提供打开入口。server daemon 从本机 Unix Socket 和网关配置读取 FN Connect ID、协议及管理端口，
  经 bootstrap 下发；不依赖客户端传参、浏览器域名或 fnOS 桌面桥接。
- 后缀本身及 `<NAS hostname>.<后缀>` 均解析到 NAS。
- 默认后缀 `<FN Connect ID>.fncpn`，管理员可自定义；两端安装/升级后随连接自动应用。
  `abc.<后缀>` 优先 NAS LAN DNS，无记录或上游不可用时尝试 mDNS `abc.local`，不维护来源固定状态；
  `abc.local.<后缀>` 明确走 mDNS，`abc.home.lan.<后缀>` 明确走 LAN DNS。
  原始 `.local` 和家庭 DNS 后缀不被接管，避免与客户端当前网络同名设备冲突。
- 私网 DNS 默认信任内网环境，直接收发解析 JSON，不增加请求/响应 HMAC、时间窗口或防重放；
  原有 `/api/v1/probe` 与 `/probe` 的身份 HMAC 证明保持不变。DNS 不依赖探测密钥。
- NAS 自动检测有效 LAN DNS 和搜索域，在已启用 LAN 接口上代查 mDNS；用户无需逐设备建表。
  54790 为共享的 NAS 私网 API（身份探测及 DNS）；解析响应返回地址，客户端不转发或重播 mDNS 组播，不依赖系统对 `.local` 的接管。
- DNS 生命周期独立于 WireGuard，LOCAL/DIRECT/RELAY 均应用；LOCAL 不创建隧道，
  同一带隔离后缀的名称继续通过 NAS 查询并按当前真实路由验证。
- macOS 使用 loopback 高位端口和 resolver 的显式 port，普通 daemon 直接解析；特权组件
  仅维护 resolver 文件。Windows 自动探测可用 loopback IP 的 UDP/TCP 53 并写入 NRPT，优先复用上次地址，需实机验证。
  不增加 `watch-dns-query`/`respond-dns-query`，不依赖隧道地址，也不在 NAS 上新开 DNS 端口。
- 独立 DNS 计划与 journal 管理 owner/generation、规则和监听器生命周期；先启动监听器，
  后安装规则，DNS 子事务失败只降级名称功能。监督器恢复、owner 存活检查、退避重试与
  清理规则共同避免崩溃后的解析黑洞。
- server 校验名称及结果地址范围；client 再按实际路由过滤答案。NXDOMAIN、NODATA、
  SERVFAIL 分开处理，故障不进入否定缓存；设置变化和路径变化失效缓存。
- 新字段、新 IPC 和持久化设置需要按版本契约管理：远程功能通过 1.1 MINOR 引入；本地 IPC
  独立定版，严格解码的持久化设置升级 schema，事务备份后迁移，保留身份和连接意图。
  自动化协议测试不能替代历史 1.0 正式产物和平台系统解析验收。

## 安全与权限边界

- 每台设备独立密钥；客户端私钥不上传，服务端私钥不导出。
- 服务端允许已登录用户注册设备，仅管理员可清理离线记录；设备级授权和安全撤销进入 P1。
- 客户端原生会话、Cookie jar、私钥和配置分层存储；client privileged-daemon
  只保存不透明凭据，不解析会话或 Cookie。
- macOS 用户态 IPC 校验同 UID，root IPC 校验对端凭据与当前控制台用户，用户切换时清理旧网络；Windows 校验 SID、会话与活动本地控制台身份。Linux 特权实例按 UID 隔离，不套用控制台用户限制。具体端点及授权见 [本地 IPC](interfaces.md#本地进程通讯协议)。
- client 和 server privileged-daemon 只执行白名单操作，不接受调用者提供的命令、环境变量或文件路径。
- server daemon 以应用专用普通用户运行，HTTP/WSS 外部输入不能直接进入 root 进程。
- 安装包不携带 `wireguard-go`、`wg` 或其他网络工具；macOS 仅调用系统自带的固定 `/sbin/ifconfig` 和 `/sbin/route`，fnOS 不调用外部网络命令。
- 所有网络变更使用声明式计划并在失败时逆序回滚。
- WSS 目标固定为本机 WireGuard 端口，并限制帧大小、速率、连接数和队列。
- DNS 解析服务只应答已配置后缀内的名称，解析结果地址限定在已启用的 LAN/overlay CIDR，
  NAS HTTP 接口仅返回已启用网段内的 IPv4 地址，不增加设备鉴权；客户端监听器只绑定 loopback，
  丢弃非 loopback 来源；仍没有本机调用方认证，安全依赖后缀白名单、
  地址校验、超时和并发上限。
- 日志和诊断统一脱敏；严重错误必须记录组件、阶段和可操作原因。
- 服务停止、升级、卸载和系统关机均执行幂等清理。

在无 Developer ID 的本地安装模式下，不能把代码签名身份作为唯一安全边界。P0 安全边界依赖 Unix peer credential、目录权限、严格 schema、固定操作集合和参数复验。

DNS 名称解析仅支持 IPv4 A（家庭 DNS、mDNS 及 NAS hostname 入口）。客户端收到 AAAA 查询时本地返回 NOERROR 空答案，不向 NAS 发起请求；DNS API 拒绝非 A 查询。此限制不影响 DIRECT 的 IPv6 UDP 传输。
