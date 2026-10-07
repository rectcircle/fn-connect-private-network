# FN Connect Private Network 产品需求与技术架构

> 文档状态：需求讨论稿  
> 版本：0.3  
> 日期：2026-09-29  
> 首发客户端：macOS 13+  
> 服务端：fnOS x86_64 / ARM64

面向 fnOS NAS 用户的异地私有网络产品需求与技术架构。目标是在不要求用户理解密钥、路由、端口或中继协议的前提下，自动选择局域网、IPv6 直连或 FN Connect 中继路径。

## 1. 产品结论

用户在 fnOS 安装服务端、在 Mac 安装客户端后，只需在客户端完成一次 FN Connect 授权，即可自动注册设备、生成配置并开始使用。正常情况下不再要求用户复制 Cookie、管理密钥、编辑配置文件、启动 bridge 或执行终端命令。

PoC 已证明内核 WireGuard、macOS 用户态隧道、IPv6 UDP 直连、FN Connect WSS 中继、设备地址分配和最小特权进程均可行。正式实现的重点从“证明链路成立”转为“把手工步骤收敛为稳定的产品流程”。

核心产品决策：

- macOS 和 fnOS 每个平台只发布一个 `fncpn` Go 二进制，通过子命令启动不同职责的进程。
- 系统采用“普通权限 daemon + 最小 root privileged-daemon”的双进程结构。
- GUI 和用户 CLI 只与普通权限 daemon 通讯。
- fnOS 会话协议、Cookie 解析、自动选路和 WSS 中继永远不进入 root 进程。
- fnOS UI 和 macOS UI 仅负责展示和用户交互；登录、会话恢复和网络逻辑统一由 Go 程序实现。

## 2. 产品契约

### 2.1 核心定义

| 项目 | 定义 |
|---|---|
| 主要用户 | 拥有 fnOS 管理权限、希望从外部网络访问 NAS 及家庭局域网的个人用户 |
| 触发条件 | 用户已启用 FN Connect，并分别完成 fnOS 服务端和 macOS 客户端安装 |
| 核心任务 | 无论当前网络是否具有 IPv6，用户都能以同一操作方式访问远端 NAS |
| 完成标志 | 客户端显示“已连接”或“局域网直达”，并能访问 NAS 的私有网络地址 |

### 2.2 角色

| 角色 | 职责与权限 | 限制 |
|---|---|---|
| fnOS 管理员 | 安装服务端、授权客户端注册和管理可访问网段 | P0 仅管理员可注册新设备，不提供设备级授权管理 |
| 已注册设备用户 | 连接、断开、查看状态和重新授权当前设备 | 不能修改服务端网络策略；P0 不提供设备 owner 和委托授权 |
| 客户端 privileged-daemon | 以 root 身份执行网络变更，并按系统 peer UID 保存客户端凭据 | 凭据仅作为不透明数据保存；不能执行调用者指定的命令、读取任意路径或发起 HTTP 请求 |
| 服务端 privileged-daemon | 以 root 身份管理 WireGuard、转发和防火墙状态 | 不接收 HTTP/WSS，不解析浏览器输入 |

### 2.3 核心对象

| 对象 | 定义 | 所有者 |
|---|---|---|
| 服务端配置 | FN ID、服务端公钥、overlay 网段、监听端口、可访问 LAN 网段和配置版本 | fnOS 服务端 |
| 设备记录 | 设备 ID、展示名称、公钥、分配地址和最近握手信息；P0 仅服务自动注册和 WireGuard peer | fnOS 服务端 |
| 本地配置 | 服务端标识、设备 ID、连接偏好和最后一次有效配置 | macOS 当前用户 |
| fnOS 原生会话 | 用户名、稳定设备 ID、短 token、长 token、HMAC secret 和 backId；不含密码 | macOS root 私有文件，按用户 UID 和 FN ID 隔离 |
| 管理页 Web 会话 | 独立 DID、短 Web token；不含密码，不用于隧道 | macOS root 私有文件，按用户 UID 和 FN ID 隔离 |
| 网关 Cookie jar | 由原生短 token 生成并由 HTTP/WSS `Set-Cookie` 更新的兼容凭证集 | macOS root 私有文件，按用户 UID 和 FN ID 隔离 |
| 连接会话 | 当前路径、接口、路由、握手状态、重连状态和错误原因 | macOS 用户态守护进程 |

## 3. 范围与优先级

### 3.1 P0：最小完整产品

- fnOS 服务端安装后自动启动并生成长期服务端密钥。
- macOS 客户端首次启动提供 FN Connect ID、fnOS 用户名和密码的原生登录页。
- 授权完成后自动生成客户端密钥、注册设备、下发配置并连接。
- 自动按“局域网直达 → IPv6 UDP 直连 → FN Connect WSS 中继”选择路径；仅当 NAS 返回可用 IPv6 地址且本机支持 IPv6 网络时才尝试 IPv6 直连，否则直接走中继。
- 支持访问 NAS overlay 地址和自动识别的主要 LAN 网段。
- 支持多个自动注册设备，每台设备具有独立密钥和地址。
- 管理员可查看设备活性，并在确认后按设备 ID 删除离线记录。
- 支持自动连接、断线重连、网络切换和睡眠唤醒恢复。
- 提供 macOS 状态界面、菜单栏入口和最小 CLI。
- 每个平台只安装一个 `fncpn` Go 二进制，不依赖 Homebrew、用户手工安装或额外第三方运行时命令。

### 3.2 P1：后续能力

- 管理多个 fnOS 服务端并在客户端切换。
- 完整设备身份和授权模型：
  - 为设备绑定 owner、注册用户或独立设备级凭证。
  - 配置读取使用设备级 token、公钥挑战等能力证明，不能长期依赖不可猜测的 Device ID。
  - 支持设备邀请、非管理员自助注册和应用级短期授权。
  - 支持设备重命名、启停、安全撤销、删除、审计和重新授权。
  - 安全撤销必须阻止原设备继续同步配置和建立数据面，不能只删除当前 WireGuard peer。
- 精确 IP 冲突时提供远端地址映射。
- 按设备配置不同的 LAN 网段访问权限。
- Intel Mac、Windows 和 Linux 图形客户端。

### 3.3 明确不进入 P0

- 设备 owner、设备级认证与授权、邀请、启停、安全撤销、审计和重新授权；仅离线记录清理进入 P0。
- 要求用户复制浏览器 Cookie、编辑 JSON 或手工维护 WireGuard 配置。
- 把 PoC 的 `fncpn-client bridge` 作为用户长期运行的公开命令。
- 依赖 fnOS 或 macOS 预装第三方网络工具。
- 在目标 IP 精确冲突时静默覆盖本地路由。
- 承诺 FN Connect 中继与 IPv6 直连具有相同吞吐和延迟。

## 4. 开箱即用流程

### 4.1 首次使用主流程

```text
安装 fnOS 服务端
    ↓
自动生成服务端密钥、overlay 接口、地址池和网络策略
    ↓
安装 macOS 客户端
    ↓
输入 FN ID，在客户端内置窗口完成 fnOS 登录
    ↓
自动生成客户端密钥、注册设备并同步配置
    ↓
自动选择局域网、IPv6 直连或 FN Connect 中继
    ↓
客户端显示“已连接”或“局域网直达”
```

### 4.2 首次授权详细流程

1. 客户端要求用户输入 FN Connect ID、fnOS 用户名和密码，并规范化服务端标识。
2. App 通过当前用户私有 IPC 把本次登录输入提交给 client daemon；密码不写入日志、
   配置、Cookie jar 或 root 凭据文件。
3. client daemon 经 FN Connect 认证 WebSocket 调用 `user.login`，取得
   `token + longToken + secret + backId + did`。
4. daemon 以独立 DID 执行加密 Web 登录，取得仅供管理页使用的短 Web token；
   Web 登录失败不改变 CLI 会话和建链结果。
5. daemon 通过特权 IPC 分开保存两类会话，并以 CLI 短 token 生成统一网关所需的
   `fnos-token` Cookie；root 进程只保存不透明字节，不参与协议。
6. daemon 生成本机 WireGuard 密钥，并调用服务端 bootstrap 校验会话；bootstrap 同时返回
   `administrator` 标记，用于区分非管理员建链与仅限管理员的 Web 管理后台访问。
7. 客户端提交设备名称和公钥；服务端分配最低可用 overlay 地址并启用设备。
8. 服务端返回连接配置；客户端持久化配置并立即开始自动选路。
9. 界面进入连接详情页并显示当前路径，首次流程结束。

“一次授权”指正常使用周期内无需反复登录。短 token 失效时客户端先自动使用长 token
恢复；长 token 也失效、用户主动退出、修改密码或服务端撤销会话时，详情页显示
“重新登录”。点击后返回原生登录页，预填 FN Connect ID 和用户名，不预填密码。

## 5. 服务端功能设计

### SRV-01 自动初始化

服务端安装完成后自动启动。首次启动生成服务端私钥、创建 overlay 地址池、识别主要 LAN 网段并准备 UDP 直连与 WSS 中继入口。

- 服务端私钥一经生成持续复用，升级和重启不得自动更换。
- 默认 overlay 网段为 `10.253.203.0/24`，服务端占用 `10.253.203.1`。
- 管理员可在服务端设置中修改 overlay IPv4 网段。
- 默认监听端口为 `54789/UDP`。
- 默认网段与本机 LAN 重叠或默认端口被占用时不自动随机切换；服务端以不可用状态保留
  管理页面，由管理员修改后重试。
- 若直接路径初始化失败但中继仍可工作，服务状态为“受限可用”，并显示失败原因。
- 若核心接口无法创建，服务状态为“不可用”，不得只显示进程正在运行。

### SRV-02 服务状态

应用首页提供可扫描的运行状态，不展示面向开发者的协议测试控件。

| 区域 | 显示内容 | 用户动作 |
|---|---|---|
| 服务 | 正常、受限可用、不可用；接口地址和启动时间 | 启动、停止、重试 |
| 连接能力 | IPv6 UDP 可用性、FN Connect 中继入口、最近错误 | 刷新检测 |
| 网络范围 | overlay 网段、主要 LAN 网段和是否已启用转发 | 进入网络设置 |
| 设备摘要 | 自动注册设备总数、活性状态和最近握手时间 | 查看、删除离线设备 |

### SRV-03 设备自动注册

- P0 设备记录只服务自动注册、地址分配和 WireGuard peer 配置，不作为完整设备管理系统。
- 新设备默认取得地址池中的最低可用地址。
- 经过 fnOS 统一网关认证的会话均可注册设备并建立连接；仅管理员会话可访问 Web 管理后台。
- 每台设备生成独立 WireGuard 密钥；客户端私钥不离开本机，服务端只保存公钥。
- 同一公钥是注册幂等键；重复注册返回现有设备和最新配置，不能重复占用地址。
- 设备名称只用于展示，不参与身份判断或授权。
- P0 不提供设备重命名、启停、安全撤销和 owner 绑定；这些能力进入 P1 统一设计。
- 管理页独立展示启用状态和连接状态；连接状态是 `connected`、`disconnected` 或
  `unknown`，仅存在于管理响应，不写入设备记录或客户端配置。
- 客户端现有 keepalive 为 25 秒。服务端每 5 秒采样各公钥的 WireGuard RX 计数，
  增量表示近期接收活动；超过 90 秒没有增量视为离线，不以累计流量、TX 或握手时间替代。
- 服务端监测启动、计数重置、peer/接口变化或监测中断后先标记未知；收到增量后在线，
  否则需要重新完成 90 秒连续观测。状态过期、不可用或 degraded 时禁止删除。
- 删除按钮只对离线设备可用，确认中展示设备名和地址；后端仍需管理员权限，并在
  串行写操作内重新读取实时 RX 状态。新收包时拒绝删除，未知状态时也拒绝。
- 成功删除会移除对应 WireGuard peer 和持久化记录，并通知配置与管理页监听；
  其他设备的 ID、密钥和地址不变。同名记录不自动合并或删除。
- 管理员拥有注册新设备的权限，因此仅删除一个 peer 不能构成安全撤销，不得在 P0
  对外宣称为设备撤销能力。
- 离线是超时策略，不是 WireGuard 的确定断开信号；删除检查后再次建联的时序不由
  此策略锁定。LOCAL 不使用 WireGuard 隧道，也不作为本活性判据的在线证据。

### SRV-04 网络范围

- overlay 网段必须使用 RFC1918 或 `100.64.0.0/10` 地址，前缀长度限制为 `/16` 至 `/29`。
- 服务端使用 overlay 网段的第一个可用地址；设备从第二个可用地址开始按最低可用地址分配。
- overlay 网段不得与服务端已启用的 LAN 网段重叠。
- 修改 overlay 网段属于影响全部设备的操作，必须二次确认。
- 修改时服务端为所有设备重新分配地址、原子更新 WireGuard 配置并提升配置版本；在线设备会断开并在同步新配置后重连。
- overlay 变更任一步失败时保持旧网段、旧设备地址和旧配置版本，不允许部分生效。
- 服务端自动识别默认路由所在接口的私有 LAN CIDR，并作为主要 LAN 网段。
- 忽略 loopback、overlay、容器和明显的虚拟接口网段。
- 管理员可启用或停用 LAN 网段；overlay 服务端地址始终保留。
- 服务端负责转发和必要的源地址转换，不要求家庭其他设备添加回程路由。
- 网络配置变化产生新的配置版本，客户端下次同步后重新计算路由。

### SRV-05 中继入口

- 每个 WebSocket binary message 只承载一个 WireGuard datagram。
- 每个 WSS 会话拥有独立的本地 UDP socket，目标固定为服务端 WireGuard 端口。
- 禁止客户端指定 UDP 目标，避免中继入口成为通用代理。
- 限制单帧大小、并发连接数和待发送队列；过载时丢弃 datagram，不无限占用内存。
- 只接受经过 fnOS 统一网关认证的会话。

## 6. 客户端功能设计

### CLI-UX-01 首次启动

- 未配置状态只显示 FN ID 输入和“授权并连接”主操作。
- 支持粘贴 FN ID、`https://<fn-id>.fnos.net` 或 FN Connect 分享地址。
- 用户取消授权时保留未配置状态，不创建半完成设备。
- 授权成功后自动连接，不再要求用户选择传输模式。

### CLI-UX-02 日常状态

菜单栏显示当前连接状态；主窗口提供详细状态和少量必要操作。

| 状态 | 用户文案 | 允许操作 |
|---|---|---|
| `UNCONFIGURED` | 尚未设置 | 授权并连接 |
| `AUTHORIZING` | 正在授权 | 取消 |
| `PROBING` | 正在检测可用路径 | 断开 |
| `LOCAL` | 局域网直达 | 暂停自动连接、查看详情 |
| `DIRECT` | IPv6 直连 | 断开、查看详情 |
| `RELAY` | FN Connect 中继 | 断开、查看详情 |
| `RECONNECTING` | 正在恢复连接 | 断开、立即重试 |
| `AUTH_REQUIRED` | 需要重新授权 | 重新授权 |
| `PAUSED` | 已暂停 | 连接 |
| `ERROR` | 连接失败，并显示可执行的原因 | 重试、诊断、重新授权 |

### CLI-UX-03 自动连接

- 首次授权成功后默认启用自动连接。
- 用户登录 macOS 后，用户态守护进程自动启动；GUI 无需常驻前台。
- 网络变化、睡眠唤醒或临时断网后自动重新探测路径。
- 用户主动断开后进入 `PAUSED`，网络变化不得擅自重连，直至用户再次连接。
- 守护进程重启时恢复用户最后一次“自动连接”或“暂停”意图。

### CLI-UX-04 诊断与恢复

- 状态详情显示服务端、当前路径、接口、最后握手、配置版本和最近错误。
- “复制诊断信息”必须脱敏，不包含 Cookie、私钥、完整 token 或用户流量。
- 认证失败只引导重新授权；路由冲突明确指出仅保持 NAS overlay 访问。
- 卸载时清理接口、路由、进程和 root 状态；是否删除用户配置由用户确认。

## 7. 共享规则与异常

### 7.1 路径选择规则

| 优先级 | 条件 | 行为 | 失败处理 |
|---|---|---|---|
| 1 | NAS 局域网地址可直接完成应用级探测 | 不创建隧道，不安装路由，状态为 `LOCAL` | 继续检测 IPv6 |
| 2 | NAS 返回至少一个可用公网 IPv6 地址，且本机支持 IPv6 网络 | 使用原生 UDP 尝试握手，状态为 `DIRECT` | 握手失败或无 IPv6 上行则直接中继 |
| 3 | 具有有效 FN Connect 凭证 | 连接 WSS bridge，状态为 `RELAY` | 重连；认证失败进入 `AUTH_REQUIRED` |

- 路径切换采用连续成功或失败阈值，避免网络抖动时频繁切换。
- 中继恢复后应复用现有 WireGuard 会话，非必要不重建设备配置。
- 切换路径时先保证新路径可用，再清理旧路径；失败则回滚到最后有效状态。

### 7.2 路由冲突规则

| 本地与远端关系 | P0 行为 |
|---|---|
| 网段不重叠 | 安装远端 LAN CIDR 路由 |
| 网段重叠 | 不猜测目标地址是否空闲，不安装 LAN `/32`；仅保留当前 overlay 服务端地址访问并明确提示 |

### 7.3 设备与配置规则

- 客户端私钥永不离开本机；服务端只接收公钥。
- 服务端配置带单调递增版本，客户端只接受当前服务端的更新版本。
- 同一 FN ID 和同一 WireGuard 公钥表示同一设备身份；Device ID 是服务端记录主键。
- P0 不提供设备级撤销；fnOS 管理员会话仍可注册新密钥。设备撤销和重新授权策略进入 P1。
- 服务端不可达时，客户端保留最后有效配置，但不得绕过凭证校验新建中继会话。

## 8. 总体架构

```text
macOS
┌──────────────────────────────┐
│ FnCPN.app / fncpn CLI        │
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
│ root；内嵌 WireGuard、网络配置 │
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

### 8.1 职责边界

| 组件 | 负责 | 明确不负责 |
|---|---|---|
| FnCPN.app | 授权窗口、首次引导、状态和设置 | 不直接配置网络，不长期承载连接 |
| `fncpn` 用户 CLI | 脚本化查询和控制客户端 daemon | 不读取 profile 执行连接，不直接调用 root 能力 |
| `fncpn client daemon` | 唯一客户端状态机、配置同步、授权凭证、选路和 WSS bridge | 不以 root 运行，不直接修改系统网络 |
| `fncpn client privileged-daemon` | 进程内运行 WireGuard，并按结构化计划配置接口和路由 | 不联网、不解析凭据、不接受命令名和文件路径 |
| `fncpn server daemon` | 设备、地址池、配置、HTTP API 和中继桥 | 不以 root 运行，不直接操作接口、防火墙和转发 |
| `fncpn server privileged-daemon` | 管理服务端私钥、内核 WireGuard、转发和防火墙 | 不监听 HTTP/WSS，不处理浏览器或客户端原始输入 |

单一二进制不代表单一进程。普通权限 daemon 与 root privileged-daemon 必须作为独立进程运行，通过窄 IPC 保持权限隔离。

## 9. Go 业务包设计

`fncpn` Go 程序承载除原生 UI 以外的全部核心业务逻辑，不只是供 UI 调用的工具库。macOS 和 fnOS UI 仅负责展示、收集登录输入和用户操作，并调用本地 IPC。

同一代码库按目标平台构建一个 `fncpn` 可执行文件，不同职责通过子命令启动。内部 package 按业务领域和平台边界命名，不设置 `core`、`common` 或 `utils` 等宽泛的中间层。

| Package | 职责 | 输入 / 输出 |
|---|---|---|
| `internal/model` | 客户端状态、服务端配置、设备、网络计划和稳定错误码 | 跨业务边界共享的数据契约 |
| `internal/client` | 授权、配置同步、自动选路、连接状态机和 WSS bridge | 用户意图 + 网络事件 → 客户端状态 |
| `internal/server` | 服务端设置、设备、地址池、HTTP API、中继和配置版本 | 管理请求 → 服务端期望状态 |
| `internal/ipc` | framed JSON、请求响应和本地 Unix Socket 通讯 | 本地进程请求 ↔ 结构化响应 |
| `internal/privileged` | client/server 特权操作白名单、客户端凭据文件和计划调度 | 已校验请求 → 网络适配或受限凭据存储 |
| `internal/platform/darwin` | utun、路由和系统事件适配 | 网络计划 ↔ macOS |
| `internal/platform/linux` | netlink、内核 WireGuard、转发和 nftables 适配 | 服务端期望状态 ↔ Linux |
| `internal/command` | 子命令解析、进程组装和生命周期入口 | 命令行 → 对应进程职责 |

### 9.1 Go 实现约束

- package 名称必须表达具体领域或平台职责，禁止创建承载无关逻辑的 `core`、`common`、`shared` 或 `utils` package。
- `internal/client` 和 `internal/server` 可以依赖 `internal/model`，但不能相互依赖。
- `internal/model` 只保存跨边界稳定契约，不放文件、网络、日志或进程操作。
- `internal/platform` 只能实现上层定义的窄接口，不能持有产品状态机。
- 不执行 shell，不拼接系统命令。
- 不依赖全局变量表达当前连接，状态由单一 session 实例拥有。
- 发现、HTTP、时间和平台网络操作通过窄接口注入，便于确定性测试。
- 错误必须归一化为稳定错误码，同时保留可记录的底层 cause。
- 关键状态变更输出明确 error 日志，但日志字段必须脱敏。
- 所有 apply 操作要么完整成功，要么按逆序回滚到旧状态。
- 使用当前稳定 Go 工具链；实现基线为 Go `1.27`，CI 和发布构建固定到明确的 patch 版本。
- HTTP、JSON、Cookie、加密、进程管理、日志和 IPC 优先使用 Go 标准库。
- 标准库不提供或自行实现风险明显更高的能力才引入外部依赖。

## 10. 进程与二进制依赖

### 10.1 单一二进制与子命令

macOS 和 fnOS 分别构建适配自身平台的 `fncpn`，但每个平台的安装包中只包含一个 Go 可执行文件。同一文件可以被不同的 LaunchAgent、LaunchDaemon 或 fnOS 生命周期脚本以不同子命令启动。

| 子命令 | 平台 | 运行身份 | 职责 |
|---|---|---|---|
| `fncpn client daemon` | macOS | 当前用户 | 客户端核心业务和用户 IPC |
| `fncpn client privileged-daemon` | macOS | root | 进程内 WireGuard、utun 和路由 |
| `fncpn server daemon` | fnOS | 应用专用普通用户 | HTTP/WSS、设备和配置管理 |
| `fncpn server privileged-daemon` | fnOS | root | 内核 WireGuard、转发和防火墙 |
| `fncpn status/connect/...` | macOS | 当前用户 | 用户 CLI，通过 IPC 控制 client daemon |

`FnCPN.app` 仍是一个薄原生 UI 壳，不属于 Go 核心二进制；它只链接 AppKit。

### 10.2 WireGuard 集成

- `wireguard-go` 可以通过 `golang.zx2c4.com/wireguard/device`、`tun` 和 `conn` 包直接链接到 `fncpn`。
- 客户端 privileged-daemon 在进程内创建 TUN 和 WireGuard device，不再启动 `wireguard-go` 子进程。
- `wg` 是 wireguard-tools 提供的 C 命令，不能作为 Go 包直接链接；正式实现不携带、不释放也不执行 `wg`。
- privileged-daemon 直接调用 WireGuard device API/UAPI 写入私钥、peer、endpoint、AllowedIPs 和 keepalive。
- WireGuard 源码依赖固定到明确版本，并在内部适配层隔离其 API 变化。

### 10.3 平台网络实现

- macOS 客户端在进程内运行 WireGuard，并由 privileged-daemon 使用固定绝对路径调用系统自带的 `/sbin/ifconfig` 和 `/sbin/route`。
- 系统命令必须通过 `exec.CommandContext` 直接调用，不经过 shell；命令路径、操作集合和参数结构固定，所有动态值来自已复验的结构化计划。
- P0 不修改系统 DNS；访问远端资源使用 IP。内部域名与 split DNS 在出现明确需求后单独设计。
- fnOS 使用 Linux 内核 WireGuard、Go netlink、wgctrl 和 Go nftables 库。
- fnOS 不依赖系统中的 `ip`、`wg`、`nft` 或其他第三方 CLI。
- 若内核 WireGuard 不可用，P0 明确报错，不静默下载或执行未知二进制。

### 10.4 外部依赖原则

允许的外部依赖必须满足“标准库没有等价能力”或“自行实现安全风险明显更高”：

| 能力 | 依赖原则 |
|---|---|
| WireGuard 协议与 macOS TUN | 使用官方 `wireguard-go` Go 包 |
| Linux WireGuard 配置 | 使用 `wgctrl` |
| Linux netlink / nftables | 使用成熟 Go 库，不调用外部命令 |
| WebSocket | 使用维护活跃的小型 Go 库，避免自行实现协议 |
| 系统调用 | 优先标准库，其次 `golang.org/x/sys` |

每个新增依赖必须说明必要性、维护状态、许可证和替代方案。禁止仅为少量辅助函数引入大型框架。

### 10.5 版本规则

- 发布产物记录 Go 工具链版本、依赖锁定版本和源码校验值。
- 控制协议使用独立版本号，不与 App 版本隐式绑定。
- 升级必须保持本地配置、root 凭据文件、服务端私钥和设备表。切换存储的测试版本不自动访问或删除旧 Keychain 条目，需重新授权生成文件凭据。

## 11. Client CLI 接口

用户 CLI 与 daemon 使用同一个 `fncpn` 二进制。普通用户命令只连接 `fncpn client daemon`，不再接受 WireGuard profile 文件，也不自行建立 WSS 连接。

| 命令 | 行为 | 重要规则 |
|---|---|---|
| `fncpn status [--json]` | 输出配置、连接状态、当前路径、握手和最近错误 | 默认人类可读；脚本使用稳定 JSON schema |
| `fncpn authorize <fn-id>` | 唤起 FnCPN.app 的授权窗口并等待结果 | CLI 不接收 Cookie 参数 |
| `fncpn connect` | 清除暂停状态并立即开始自动选路 | 未授权时返回 `AUTH_REQUIRED` |
| `fncpn disconnect` | 停止当前连接并进入暂停状态 | 重复执行成功且不产生副作用 |
| `fncpn retry` | 清除可重试错误并重新探测 | 不绕过 FN Connect 认证失败 |
| `fncpn logout` | 断开并删除本地 FN Connect 凭证 | 保留设备密钥和注册信息，重新授权后可继续使用 |
| `fncpn forget` | 删除本地服务端配置、密钥和凭证 | 需要交互确认；P0 不请求服务端撤销设备 |
| `fncpn diagnose [--json]` | 输出脱敏诊断结果 | 不得输出任何密钥或 Cookie |
| `fncpn version` | 输出二进制、运行中 daemon 和协议版本 | 组件不可达时仍输出已知版本 |

命令退出码：

| 退出码 | 含义 |
|---|---|
| `0` | 成功 |
| `1` | 其他失败 |
| `2` | 参数错误 |
| `3` | 未授权 |
| `4` | 服务不可用 |
| `5` | 操作冲突 |
| `6` | 权限不足 |
| `7` | 资源不存在或设备失效 |
| `8` | 前置条件不满足 |
| `124` | 操作超时 |
| `130` | 操作取消 |

## 12. 本地进程通讯协议

### 12.1 用户 CLI/UI 与 client daemon

- 使用当前用户私有目录中的 Unix domain socket，父目录权限 `0700`，socket 权限 `0600`。
- `fncpn client daemon` 从内核读取 peer UID，必须与 daemon UID 一致。
- 协议使用 4-byte big-endian 长度前缀加 UTF-8 JSON，单帧最大 256 KiB。
- 每个请求包含协议版本和唯一 ID；响应必须回显 ID。
- 拒绝未知字段、未知方法和超限消息。

### 12.2 请求与响应

请求：

```json
{
  "version": 4,
  "id": "2e52c41c-8124-4eb7-8fd7-2ff452e41e66",
  "method": "status",
  "params": {}
}
```

成功响应：

```json
{
  "version": 4,
  "id": "2e52c41c-8124-4eb7-8fd7-2ff452e41e66",
  "ok": true,
  "result": {
    "state": "DIRECT",
    "path": "ipv6",
    "interface": "utun4"
  }
}
```

错误响应：

```json
{
  "version": 4,
  "id": "2e52c41c-8124-4eb7-8fd7-2ff452e41e66",
  "ok": false,
  "error": {
    "code": "AUTH_REQUIRED",
    "message": "FN Connect authorization is required",
    "retryable": false
  }
}
```

稳定错误码至少包含：

- `INVALID_ARGUMENT`
- `AUTH_REQUIRED`
- `PERMISSION_DENIED`
- `NOT_FOUND`
- `ALREADY_EXISTS`
- `FAILED_PRECONDITION`
- `DEVICE_REVOKED`（预留给 P1 设备撤销）
- `UNAVAILABLE`
- `CONFLICT`
- `TIMEOUT`
- `CANCELED`
- `INTERNAL`
- `PROTOCOL_ERROR`
- `RESOURCE_EXHAUSTED`
- `DISCOVERY_FAILED`

错误还携带 `operation`、脱敏 `detail`、`httpStatus`、`remoteCode` 和 `requestId`。
本地保留 cause 链，跨 HTTP/IPC 传递安全详情；401 与 403 不混为同一类认证错误。
完整分类、记录职责和日志路径见 [错误码与全链路诊断](05-error-diagnostics.md)。

### 12.3 client daemon 与 client privileged-daemon

- 使用独立的 `/var/run/fncpn-client-privileged.sock`，与用户 IPC 分离。
- 方法只允许 `status`、`watch-lifecycle`、`apply`、`remove` 以及
  `get-client-secret`、`put-client-secret`、`delete-client-secret`。
- `apply` 接收完整结构化网络计划，privileged-daemon 独立复验并收敛地址、接口、
  路由和 endpoint。
- privileged-daemon 只接受 root 或当前控制台用户进程。
- 任何错误必须保留请求 ID；鉴权发生在解码前时允许空 ID 错误响应。
- 凭据请求仅允许 WireGuard 私钥、fnOS 原生会话、管理页 Web 会话与 Cookie jar
  四种命名空间；用户 UID 取自操作系统
  Unix Socket peer identity，请求不能指定 UID 或路径。FN ID 和凭据类型哈希后生成文件名。
- root 保存不透明凭据字节，不参与 FN Connect 会话、Cookie 解析或 HTTP/WSS 请求。
- 安装健康检查只依赖 IPC 就绪；自动连接在后台执行，不等待网络或用户授权后才监听 IPC。

### 12.4 server daemon 与 server privileged-daemon

- 使用 fnOS 应用私有目录中的 Unix domain socket，归属 `root:<app-group>`，权限 `0660`。
- 普通 server daemon 是唯一允许连接该 socket 的非 root 身份。
- 方法只允许 `status`、`apply` 和 `remove`。
- `apply` 携带完整期望状态，不发送增量系统命令或版本号。
- privileged-daemon 独立校验 overlay、peer 地址、监听端口、转发网段和防火墙规则。
- 服务端私钥只由 privileged-daemon 生成和读取；普通 server daemon 只获得服务端公钥。
- HTTP/WSS 请求及其 Header、Cookie 和原始 body 不得透传到 privileged-daemon。

## 13. 数据存储设计

### 13.1 macOS 当前用户

| 数据 | 位置 | 权限与规则 |
|---|---|---|
| WireGuard 私钥 | `/var/db/fncpn/credentials/<uid>/<hash>.secret` | 普通 daemon 通过特权 IPC 存取；root 属主，文件 `0600` |
| FN Connect 凭证集 | 同上，使用独立的凭据类型 | 按 UID、FN ID 隔离；更新时原子替换，不写入日志或普通配置 |
| 服务端与设备配置 | `~/Library/Application Support/FnCPN/` | 目录 `0700`，文件 `0600`，采用临时文件加原子重命名 |
| 运行时 socket | 用户私有运行目录 | 目录 `0700`，socket `0600`，退出后删除 |
| 日志 | `~/Library/Logs/FnCPN/` | 轮转、限制大小、默认脱敏 |

### 13.2 macOS root 状态

- `/var/db/fncpn/state.json` 只保存活动接口、精确地址/路由、owner token、进程实例和
  当前控制台用户等回滚信息。
- 目录归属 `root:wheel` 且权限 `0700`。
- `credentials/` 及每个 UID 子目录均为 root 所有、`0700`；文件为 `0600`，
  临时文件同样受保护，写入后 fsync、原子重命名并同步目录。
- 存取拒绝符号链接、错误属主、宽权限和超限文件。凭据最多 64 KiB。
- 凭据文件不提供额外磁盘加密或应用签名隔离；IPC 按当前控制台 UID 授权，
  不能防御同一用户下通过 IPC 读取凭据的进程或已取得 root 权限的进程。
- 默认卸载保留所有用户凭据；`--purge-user-data UID` 仅删除指定用户的凭据、
  配置和日志。旧 Keychain 条目不自动迁移或删除；首次重新授权会注册新设备。
- 重启或异常退出后，client privileged-daemon 根据状态执行幂等清理。

### 13.3 fnOS 服务端

| 文件 | 内容 | 规则 |
|---|---|---|
| `server.key` | 服务端私钥 | 仅 server privileged-daemon 可读；`0600`，原子创建，升级保留 |
| 普通 daemon 的 `state.json` | overlay、端口、LAN 策略及完整设备列表 | 严格 schema；单文件原子替换；由业务 Service 串行提交并发布完整快照 |
| 服务日志 | 生命周期、配置、连接和错误 | 不记录私钥、Cookie 和完整请求 Header |

旧版 settings/devices/transaction 仅在业务 `state.json` 不存在时读取并迁移，
此后所有业务写入只更新 `state.json`。原子重命名为提交点；提交前失败回滚网络，
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

## 14. Client 获取与维护 fnOS 会话

正式方案：FnCPN.app 提供原生登录表单，client daemon 经 FN Connect 调用 fnOS
原生 WebSocket 会话协议。客户端不读取浏览器数据，不要求复制 Cookie，且不持久化密码。

### 14.1 原生登录页规则

- 首次启动直接显示 FN Connect ID、用户名和密码三个字段。
- 密码使用安全输入框，仅随单次 `authorize-native` IPC 请求进入 client daemon。
- `user.login` 必须通过 `wss://<fn-id>.fnos.net/websocket?type=main`，并显式携带
  `Cookie: mode=relay`；不得回退到明文 WS。
- 登录成功后只持久化 `token + longToken + secret + backId + did + username`。
- daemon 必须再通过 FnCPN bootstrap 校验会话并取得 `administrator` 标记；
  非管理员同样可以注册设备与建链，但 Web 管理后台仅对管理员开放。
  不能以 `user.login` 成功代替应用授权。
- 鉴权失败时详情页提供“重新登录”；登录页预填 FN Connect ID 和用户名，密码始终为空。
- 当前版本不支持 2FA 登录；检测到 2FA challenge 时明确报错，不保存不完整会话。

### 14.2 管理后台会话

- 管理页 Web 登录使用独立 DID，不覆盖 CLI token、longToken 或 secret。
- 本地 AdminProxy 只监听 loopback，只代理 `/app/fncpn` 子树，并以一次性高熵
  bootstrap nonce 建立本地 HttpOnly 会话。
- 代理始终删除浏览器传入 Cookie，只注入独立 Web token 生成的
  `mode=relay; fnos-token=...`。
- 每次打开管理页前先以 Web token 探测 admin snapshot。明确的 `invalid token/401`
  只清除 Web session 并提示重新登录；网络失败只提示管理后台暂不可用。
- 管理代理探测不调用 `m.fail`，不修改客户端连接状态，不调用 CLI session recovery，
  不清理或回退使用 CLI token。
- Logout、Forget 和 daemon 退出必须关闭管理代理并清除 Web 会话。

### 14.3 凭证使用与刷新

- 除刚完成 `user.login` 的首次连接外，连接前使用
  `util.getSI + user.authToken` 确认短 token。
- 短 token 被拒绝时，在新 WebSocket 连接上执行
  `util.getSI + user.tokenLogin(longToken)`；只覆盖响应实际返回的非空字段。
- 恢复短 token 后必须重新加载 Cookie 并重建 RemoteClient；不得使用仍持有旧
  `fnos-token` 的内存客户端重试。
- 配置长轮询或 relay 重连遇到明确鉴权失败时，先执行一次原生会话恢复，再决定是否进入
  `AUTH_REQUIRED`。
- `fncpn client daemon` 使用标准 Cookie jar 为控制请求和 WSS 握手选择匹配 Cookie。
- 响应中的 `Set-Cookie` 必须更新 Cookie jar 和 root 凭据文件，不把 Cookie 当作固定字符串。
- 发现接口无需用户凭证时，不附带 Cookie。
- 长 token 也被拒绝时状态切换为 `AUTH_REQUIRED`，保留设备密钥、配置、FN Connect ID
  和用户名，以便用户重新登录。

### 14.4 信任边界

FN Connect 统一网关当前要求有效 fnOS 登录态，因此客户端保存的是具有 fnOS 会话能力的敏感凭证。WireGuard 密钥不能替代该网关凭证。

正式实现必须：

- 把凭据保存为 root 私有文件，特权 IPC 按系统确认的调用用户 UID 隔离。
- 明确提供“退出登录”和“忘记此服务端”操作。
- 不在日志、诊断数据和普通配置文件中写入 token、secret、Cookie 或密码。

## 15. 服务端控制接口

接口通过 fnOS 统一网关暴露，所有路径使用固定版本前缀。

### 15.1 P0 服务端鉴权

P0 使用两层认证，不自行接收 fnOS 用户名和密码：

1. 控制面和中继入口先由 fnOS 统一网关验证 FN Connect Cookie。
2. WireGuard 数据面再使用每台客户端独立的公私钥完成报文认证。

具体约束：

- server daemon 只监听 fnOS 应用私有 Unix Socket，不直接监听外部 TCP 管理端口。
- 所有 `/api/v1/*` 和 `/relay/v1/wireguard` 请求都必须先通过 fnOS 统一网关认证。
- 网关必须移除客户端自行提交的 `X-Trim-*` 身份 Header，再注入可信的
  `X-Trim-Userid` 和 `X-Trim-Isadmin`。
- server daemon 仅在请求来自私有 Unix Socket 时信任上述 Header。
- bootstrap、配置同步和 WSS relay 至少要求有效 `X-Trim-Userid`。
- 设备注册和网络设置要求 `X-Trim-Isadmin=true`；未登录返回 401，已登录但非管理员返回
  403。
- P0 配置同步使用“有效 fnOS 会话 + 128-bit 随机 Device ID”定位当前记录。配置响应只含
  服务端公钥、客户端地址、路由和版本，不含任何私钥或 Cookie；设备 owner 和更强的
  设备级配置授权进入 P1。
- WSS 建立成功只代表网关会话有效。中继中的每个 datagram 仍必须通过 WireGuard 公钥认证，
  未注册密钥或伪造报文由 WireGuard 丢弃，不能访问 overlay 或 LAN。
- 仅此密文中继接口不校验 Origin。原生程序可以自行设置 Origin，网关还可能改写 Host；
  不用两者匹配代替身份认证。保留网关登录及资源限制，接受浏览器会话被借用占用连接的剩余风险。
- server privileged-daemon 不接收 Cookie 和 HTTP Header，只接受专用普通用户通过
  `0660` Unix Socket 提交的结构化 `ServerPlan`，并独立复验所有网络参数。

因此，P0 的信任根是 fnOS 管理员会话和每台设备的 WireGuard 私钥。P0 不提供独立于
fnOS 管理员身份之外的设备 owner、委托授权或安全撤销。

### 15.2 接口

| 能力 | 建议接口 | 权限 | 结果 |
|---|---|---|---|
| 引导信息 | `GET /api/v1/bootstrap` | 已登录用户 | 服务端身份、能力和是否可注册 |
| 注册设备 | `POST /api/v1/devices` | 已登录用户（无需管理员） | 设备 ID、分配地址和完整客户端配置 |
| 同步当前设备配置 | `GET /api/v1/devices/{id}/config` | 已登录用户；设备必须存在 | 服务端公钥、网络、端口和配置版本 |
| 网络设置 | `/api/v1/admin/networks` | 管理员 | 读取和更新 overlay 与允许的 LAN 网段 |
| 中继数据 | `GET /relay/v1/wireguard` | 已登录用户 | 升级为二进制 WebSocket |
| 设备列表 | `GET /api/v1/admin/devices` | 管理员 | 设备及连接状态、网络观测状态 |
| 清理离线设备 | `DELETE /api/v1/admin/devices/{id}` | 管理员；实时观测为离线 | 204；在线返回 412，未知返回 503，不存在返回 404 |
| 完整设备管理（P1） | `/api/v1/admin/devices` | 管理员和设备级授权策略 | 重命名、启停、安全撤销和重新授权 |

接口规则：

- 管理员权限以统一网关提供的可信 Header 为准，应用不接受客户端伪造的管理员字段。
- 设备注册必须校验 WireGuard 公钥格式、设备名称长度和幂等键。
- 配置响应不包含任何服务端私钥或其他设备信息。
- 变更接口使用结构化 JSON、拒绝未知字段并返回稳定错误码。
- 中继握手后仍由 WireGuard 报文认证保护数据面；无效报文只被丢弃。

## 16. 安全与权限边界

- 每台设备独立密钥；客户端私钥不上传，服务端私钥不导出。
- 服务端只允许管理员注册设备和清理离线记录；设备级授权和安全撤销进入 P1。
- 客户端原生会话、Cookie jar、私钥和配置分层存储；client privileged-daemon
  只保存不透明凭据，不解析会话或 Cookie。
- 用户态 IPC 校验同 UID；root IPC 校验 peer credential 和当前控制台用户。P0 仅允许
  当前控制台用户持有系统 VPN，快速用户切换时 root 清理旧用户网络状态。
- client 和 server privileged-daemon 只执行白名单操作，不接受调用者提供的命令、环境变量或文件路径。
- server daemon 以应用专用普通用户运行，HTTP/WSS 外部输入不能直接进入 root 进程。
- 安装包不携带 `wireguard-go`、`wg` 或其他网络工具；macOS 仅调用系统自带的固定 `/sbin/ifconfig` 和 `/sbin/route`，fnOS 不调用外部网络命令。
- 所有网络变更使用声明式计划并在失败时逆序回滚。
- WSS 目标固定为本机 WireGuard 端口，并限制帧大小、速率、连接数和队列。
- 日志和诊断统一脱敏；严重错误必须记录组件、阶段和可操作原因。
- 服务停止、升级、卸载和系统关机均执行幂等清理。

在无 Developer ID 的本地安装模式下，不能把代码签名身份作为唯一安全边界。P0 安全边界依赖 Unix peer credential、目录权限、严格 schema、固定操作集合和参数复验。

## 17. P0 验收条件

| ID | 场景 | 通过条件 |
|---|---|---|
| AC-01 | 全新安装 | 安装服务端和客户端后，仅输入 FN ID 并完成一次登录即可自动注册和连接 |
| AC-02 | 单一核心二进制 | 每个平台只安装一个 `fncpn` Go 二进制；用户无需 Homebrew、密钥复制、Cookie 复制、配置文件或终端 bridge |
| AC-03 | 局域网 | 客户端确认 NAS 可直达后不创建隧道和远端路由 |
| AC-04 | 公网 IPv6 | NAS 返回可用公网 IPv6 地址且本机支持 IPv6 网络时，客户端通过原生 UDP 完成握手并访问 NAS overlay 地址 |
| AC-05 | 无可用 IPv6 | NAS 未返回可用 IPv6 地址，或本机不支持 IPv6 网络时，客户端直接通过 FN Connect WSS 建立数据路径，无需用户切换模式 |
| AC-06 | 凭证续用 | 短 token 失效时自动使用长 token 恢复；长期会话失效时预填 ID/用户名并要求重新登录，不重新注册设备 |
| AC-07 | 路由冲突 | 网段重叠时不安装 LAN 路由，仅保留 overlay，绝不覆盖本地设备 |
| AC-08 | 多设备 | 至少两台客户端可同时连接，地址、密钥和流量相互隔离 |
| AC-09 | 恢复 | 网络切换、睡眠唤醒和临时断网后自动恢复；用户主动暂停时不自动恢复 |
| AC-10 | 清理 | 停止和卸载后不存在残留接口、路由、socket 或子进程 |
| AC-11 | overlay 配置 | 管理员可修改 overlay 网段；成功时全部设备获得新地址，失败时旧配置完整保留 |
| AC-12 | 服务端鉴权 | NAS 任意 IPv4/IPv6 地址均不暴露 FnCPN HTTP TCP listener；未登录请求不能访问 API/WSS；网关必须剥离外部伪造的 `X-Trim-*` Header；非管理员不能注册设备或修改网络；无有效 WireGuard 私钥不能访问 overlay/LAN |
| AC-13 | 离线设备清理 | 90 秒无 RX 增量才可删除；在线/未知/非管理员请求被拒绝；失败按持久化提交点回滚；同名其他设备不变；刷新不恢复已删除行 |

## 18. 待确认事项

### 18.1 多网卡默认范围 `[TBD]`

P0 暂定只自动启用默认路由所在物理接口的私有网段。存在多个物理 LAN 时，管理员从服务端界面手工追加。

### 18.2 fnOS 2FA 兼容性 `[TBD]`

当前原生登录已覆盖普通用户名和密码。2FA challenge、信任设备以及首次绑定 TOTP
仍需补充协议验证和原生 UI。

### 18.3 Mac 架构范围 `[TBD]`

P0 暂定 Apple Silicon、macOS 13+。Intel Mac 是否进入首发范围取决于正式打包验证，不影响 core 架构。

### 18.4 发布签名 `[TBD]`

当前以本地 unsigned PKG 为约束。Developer ID 签名、公证和公开分发作为发布阶段决策，不改变本需求中的进程与权限边界。
