# 技术架构与核心决策

以源码为依据，产品行为见 [产品需求](product.md)，远程与本地契约见 [接口](interfaces.md)。
客户端共享 Go 连接核心，以平台适配层接入原生界面、IPC 和网络能力。macOS 与 Windows 在特权组件内嵌 wireguard-go，fnOS 使用内核 WireGuard；不随包启动外部 wg/bridge 工具。

## 总体架构

```text
客户端（当前：macOS、Windows；后续平台复用业务核心）
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

客户端数据面由系统特权组件持有 WireGuard（macOS root / Windows LocalSystem）；RELAY 的 WSS 连接由普通 client daemon 持有，通过 loopback UDP 转发密文；LOCAL 不创建隧道。图中的进程关系不表示 root 进程发起 WSS。

### 客户端平台适配与扩展

总体图描述客户端的逻辑职责，具体启动方式、IPC 和系统权限由平台实现。当前已支持 macOS 与 Windows；
后续客户端复用 `internal/client` 的连接状态机、配置同步、选路及 DNS 逻辑，按平台接入 UI、
进程生命周期、凭据存储、IPC、WireGuard、路由、DNS 和网络事件。

| 能力 | macOS | Windows |
|---|---|---|
| 原生界面 | `FnCPN.app`，AppKit / WebKit | 原生 Windows UI，WebView2 |
| 后台生命周期 | LaunchAgent / LaunchDaemon | 用户 daemon / Windows 系统服务 |
| 特权身份 | root | LocalSystem |
| 本地 IPC | Unix Socket 与 peer credential | Named Pipe 与 Windows 身份校验 |
| WireGuard / 虚拟接口 | wireguard-go / utun | wireguard-go / Wintun |
| 系统 DNS | loopback DNS + resolver 文件 | loopback DNS + NRPT |

新增平台应实现业务核心定义的窄接口，保持 UI、连接状态机和系统网络权限的职责边界。
若目标系统要求不同的进程或系统扩展模型，由平台适配承接；支持范围以实际实现和验收为准。

### 职责边界

| 组件 | 负责 | 明确不负责 |
|---|---|---|
| 平台原生 UI（macOS / Windows） | 授权窗口、首次引导、状态和设置 | 不直接配置网络，不长期承载连接 |
| `fncpn` 用户 CLI | 脚本化查询和控制客户端 daemon | 不读取 profile 执行连接，不直接调用系统特权能力 |
| `fncpn client daemon` | 唯一客户端状态机、配置同步、授权凭证、选路和 WSS bridge | 以普通用户运行，不直接修改系统网络 |
| `fncpn client privileged-daemon` | 进程内运行 WireGuard，并按结构化计划配置接口和路由 | 不联网、不解析凭据、不接受命令名和文件路径 |
| `fncpn server daemon` | 设备、地址池、配置、HTTP API 和中继桥 | 不以 root 运行，不直接操作接口、防火墙和转发 |
| `fncpn server privileged-daemon` | 管理服务端私钥、内核 WireGuard、转发和防火墙 | 不监听 HTTP/WSS，不处理浏览器或客户端原始输入 |

单一二进制不代表单一进程。当前桌面客户端的普通权限 daemon 与系统特权 privileged-daemon 作为独立进程运行，通过窄 IPC 保持权限隔离。

## Go 业务包设计

`fncpn` Go 程序承载除原生 UI 以外的全部核心业务逻辑，不只是供 UI 调用的工具库。macOS、Windows 原生 UI 仅负责展示、收集登录输入和用户操作，并调用本地 IPC；fnOS 管理页通过网关 HTTP API 操作服务端。

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
| `internal/platform/linux` | netlink、内核 WireGuard、转发和 nftables 适配 | 服务端期望状态 ↔ Linux |
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

macOS、Windows 和 fnOS 分别构建适配自身平台的 Go 核心可执行文件（Windows 为 `fncpn.exe`）。同一平台的不同职责复用该文件，由 LaunchAgent、LaunchDaemon、Windows 服务或 fnOS 生命周期脚本以不同子命令启动；原生 UI 和平台驱动依赖单独打包。

| 子命令 | 平台 | 运行身份 | 职责 |
|---|---|---|---|
| `fncpn client daemon` | macOS / Windows | 当前用户 | 客户端核心业务和用户 IPC |
| `fncpn client privileged-daemon` | macOS / Windows | root / LocalSystem | 进程内 WireGuard、平台虚拟接口、路由和 DNS 配置 |
| `fncpn server daemon` | fnOS | 应用专用普通用户 | HTTP/WSS、设备和配置管理 |
| `fncpn server privileged-daemon` | fnOS | root | 内核 WireGuard、转发和防火墙 |
| `fncpn status/connect/...` | macOS / Windows | 当前用户 | 用户 CLI，通过 IPC 控制 client daemon |

各平台原生 UI 独立于 Go 核心二进制：macOS 使用 AppKit、ServiceManagement、UserNotifications 和 WebKit；Windows 使用原生 Windows UI 与 WebView2。

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
- 用户态 IPC 校验同 UID；root IPC 校验 peer credential 和当前控制台用户。P0 仅允许
  当前控制台用户持有系统 VPN，快速用户切换时 root 清理旧用户网络状态。
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
