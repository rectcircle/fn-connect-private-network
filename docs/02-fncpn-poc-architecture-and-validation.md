# FnCPN PoC 架构与验证结论

> 本文记录 PoC 当时的参数和验证结果。正式 P0 实现已经改用
> `10.253.203.0/24`、`54789/UDP`、随机客户端 relay loopback 端口及 MTU 1280；
> 当前产品契约以 `03-fncpn-product-requirements.md` 为准。

> 文档状态：PoC 总结  
> 日期：2026-09-28  
> fnOS 当前包版本：`0.4.1`  
> macOS 实测版本：`0.1.0`

## 1. 文档目的

本文记录 FnCPN PoC 的整体架构、核心验证链路、实测证据和最终结论。

PoC 只回答以下技术问题：

1. fnOS 第三方应用能否运行 WireGuard 服务端并管理多个设备。
2. FN Connect 是否能转发第三方应用的二进制 WebSocket 长连接。
3. WireGuard datagram 能否通过 WSS 中继到达 fnOS 服务端。
4. macOS 在不使用 Network Extension、无需付费开发者账号的条件下，能否创建和管理系统隧道。
5. 同一套 WireGuard 配置能否分别通过 IPv6 UDP 直连和 FN Connect WSS 中继工作。
6. 最小 root Helper、普通用户 CLI 和普通用户 bridge 的权限拆分是否成立。

PoC 不负责完整的自动选路、无感授权、局域网冲突处理和正式产品界面。这些属于可行性确认后的产品实现。

## 2. 总体架构

### 2.1 组件关系

```text
macOS
┌──────────────────────────────────────────────────────────────┐
│                                                              │
│  FnCPN.app / fncpnctl                                        │
│          │ Unix Socket                                       │
│          ▼                                                   │
│  fncpn-helper (root LaunchDaemon)                             │
│          │                                                   │
│          ├── wireguard-go ── utun4                            │
│          ├── wg setconf                                      │
│          ├── ifconfig                                        │
│          ├── route                                           │
│          └── networksetup                                     │
│                                                              │
│  fncpn-client bridge (普通用户)                               │
│          │                                                   │
│          ├── UDP 127.0.0.1:51821                             │
│          └── WSS                                             │
│                                                              │
└───────────────┬───────────────────────────────┬──────────────┘
                │ IPv6 UDP                     │ HTTPS/WSS
                │                              │
                ▼                              ▼
fnOS NAS
┌──────────────────────────────────────────────────────────────┐
│                                                              │
│  UDP :51820 ────────────────────────────────┐                 │
│                                             ▼                 │
│                                      fncpn0                   │
│                                  10.203.0.1/24                │
│                                             ▲                 │
│                                             │ UDP localhost   │
│  FN Connect                                 │                 │
│       │                                      │                 │
│       ▼                                      │                 │
│  fnOS 统一网关 ── Unix Socket ── fncpn-poc /wg               │
│                                  WebSocket ↔ UDP bridge       │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

### 2.2 控制面

控制面负责服务状态、设备管理、密钥和配置，不承载用户流量。

```text
浏览器
  │
  │ FN Connect / fnOS 统一网关
  ▼
fncpn-poc HTTP API
  │
  ├── 查询 WireGuard 状态
  ├── 创建设备
  ├── 修改设备名称和启用状态
  └── 删除设备
       │
       ▼
WireGuard Manager
  │
  ├── server.key
  ├── devices.json
  ├── 地址池分配
  └── wgctrl ReplacePeers
```

关键实现：

- 服务入口：[cmd/fncpn-poc/main.go](../cmd/fncpn-poc/main.go)
- HTTP 与管理接口：[internal/server/server.go](../internal/server/server.go)
- 设备接口与 WSS bridge：[internal/server/wireguard_handlers.go](../internal/server/wireguard_handlers.go)
- WireGuard Manager：[internal/wireguard/manager.go](../internal/wireguard/manager.go)
- 设备与地址池：[internal/wireguard/device.go](../internal/wireguard/device.go)

### 2.3 数据面

数据面始终使用 WireGuard 保护实际流量。IPv6 直连和 WSS 中继只是 WireGuard 外层报文的两种运输路径。

#### IPv6 UDP 直连

```text
应用流量
  ↓
macOS utun
  ↓
wireguard-go
  ↓ IPv6 UDP :51820
fnOS fncpn0
  ↓
10.203.0.1
```

#### FN Connect WSS 中继

```text
应用流量
  ↓
macOS utun
  ↓
wireguard-go
  ↓ UDP 127.0.0.1:51821
fncpn-client bridge
  ↓ WSS binary message
FN Connect
  ↓
fnOS 统一网关
  ↓ Unix Socket
fncpn-poc /wg
  ↓ UDP 127.0.0.1:51820
fncpn0
  ↓
10.203.0.1
```

中继规则：

- 一个 UDP datagram 对应一个 WebSocket binary message。
- 客户端 bridge 只监听 `127.0.0.1:51821/UDP`。
- 服务端每个 WebSocket 会话创建一个独立 UDP socket。
- 服务端 UDP 目标固定为 `127.0.0.1:51820`，不能由客户端修改。
- 最大 datagram 为 65535 字节。
- 客户端发送队列固定为 256，队列满时丢弃，不无限阻塞。
- 等待超过 5 秒的旧 datagram 不再发送。
- WSS 断开后使用指数退避自动重连。

### 2.4 fnOS 服务端

fnOS 服务端以 Native FPK 形式安装，通过统一网关 Unix Socket 接收 HTTP 和 WebSocket 请求。

运行时：

- 应用进程以 root 启动。
- 使用 Go netlink 创建 Linux 内核 WireGuard 接口。
- 使用 `wgctrl` 配置服务端私钥、监听端口和 peers。
- 不依赖系统中的 `ip`、`wg`、`nft` 或 `unshare` 二进制。
- 服务停止时删除 `fncpn0`。

默认配置：

| 项目 | 值 |
|---|---|
| 接口 | `fncpn0` |
| 服务端地址 | `10.203.0.1/24` |
| UDP 端口 | `51820` |
| 接口 MTU | `1420` |
| 服务端私钥 | `server.key`，权限 `0600` |
| 设备表 | `devices.json` |

设备管理规则：

- 每台设备使用独立 WireGuard 公钥。
- 服务端从 `10.203.0.2` 开始分配最低可用 `/32`。
- NAS 占用 `10.203.0.1`。
- 公钥、设备 ID 和 overlay 地址均禁止重复。
- 删除设备后地址可以复用。
- 只把 `enabled=true` 的设备写入内核配置。
- 每次设备变化通过 `ReplacePeers` 统一重建 peers。

### 2.5 macOS 客户端

macOS PoC 使用 unsigned PKG 安装以下组件：

| 组件 | 身份 | 作用 |
|---|---|---|
| `FnCPN.app` | 普通用户 | 最小状态界面和 profile 启停 |
| `fncpnctl` | 普通用户 | 向 Helper 发送 `status/start/stop/resume` |
| `fncpn-client` | 普通用户 | 密钥生成和 UDP over WSS bridge |
| `fncpn-helper` | root | 创建 utun、配置 WireGuard、路由和 DNS |
| `wireguard-go` | root 子进程 | 创建 macOS 用户态 WireGuard 接口 |
| `wg` | root 短进程 | 向接口写入 WireGuard 配置 |

第三方二进制 `wireguard-go` 和 `wg` 随 PKG 安装，不依赖目标 Mac 安装 Homebrew。

Helper 通过 `/var/run/fncpn-helper.sock` 提供固定 IPC：

```text
status
start
stop
resume
```

Helper 安全约束：

- 必须以 root 运行。
- 通过 `LOCAL_PEERCRED` 和 `LOCAL_PEERPID` 获取调用者 UID/PID。
- 只允许 root 或当前控制台用户。
- 请求 JSON 最大 64 KiB。
- 拒绝未知字段和非白名单操作。
- 不接受任意命令、环境变量或 Helper 侧文件路径。
- 只调用固定绝对路径的二进制。
- 启动任一步失败时逆序删除路由、恢复 DNS 并停止进程。

关键实现：

- IPC 协议：[internal/helperproto/protocol.go](../internal/helperproto/protocol.go)
- Helper 服务：[internal/macoshelper/server.go](../internal/macoshelper/server.go)
- 隧道生命周期：[internal/macoshelper/engine.go](../internal/macoshelper/engine.go)
- 固定命令封装：[internal/macoshelper/system.go](../internal/macoshelper/system.go)
- macOS peer credential：[internal/macoshelper/peercred_darwin.go](../internal/macoshelper/peercred_darwin.go)
- 客户端 bridge：[cmd/fncpn-client/main.go](../cmd/fncpn-client/main.go)

## 3. 核心验证链路

### 3.1 链路 A：FN Connect 转发第三方二进制 WebSocket

验证目标：

- fnOS 统一网关是否允许第三方应用注册 WebSocket 路径。
- FN Connect 中继是否保留 WebSocket Upgrade 和二进制帧。
- 网关改写 Host 后，同源检查能否安全兼容。

验证路径：

```text
外部浏览器
  ↓ wss://<fn-id>.fnos.net/app/fncpn-poc/ws
FN Connect
  ↓
fnOS 统一网关
  ↓ Unix Socket
fncpn-poc 二进制回声服务
```

实测结果：

- FN Connect relay 模式下 WebSocket Upgrade 成功。
- 二进制消息可双向传输并正确回显。
- 完成约 31.6 MiB 压力传输。
- 未发现丢包、内容校验失败或协议错误。
- 对带 `X-Trim-Userid` 的网关认证请求兼容原始 Origin 后，同源检查工作正常。

结论：

> FN Connect 能够承载第三方 fnOS 应用的二进制 WebSocket 长连接，具备作为 WireGuard datagram 兜底运输层的能力。

### 3.2 链路 B：fnOS 应用网络权限

验证目标：

- fnOS 应用进程身份和内核能力是否足以运行服务端。

真机结果：

| 检查项 | 结果 |
|---|---|
| 进程身份 | UID 0 |
| 创建内核 WireGuard 接口 | PASS |
| 创建 TUN 接口 | PASS |
| 创建与删除 nftables 表 | PASS |
| 读取 IPv4 forwarding | PASS |
| 读取 IPv6 forwarding | PASS |
| 清理测试资源 | PASS |

结论：

> fnOS Native 应用具备创建 WireGuard/TUN、配置转发和管理防火墙资源所需的基础权限，服务端不需要容器或额外宿主机安装步骤。

### 3.3 链路 C：WireGuard 服务端与设备模型

验证目标：

- 服务端能否创建稳定接口、持久化密钥并管理多个设备。
- 地址池能否避免重复并复用已释放地址。

验证结果：

- 真机成功创建 `fncpn0`。
- 服务端地址为 `10.203.0.1/24`。
- UDP 监听端口为 `51820`。
- 服务端私钥重启后保持不变。
- 客户端设备成功分配 `10.203.0.2/32`。
- 设备状态能读取最近握手、RX 和 TX。
- 自动化测试覆盖：
  - 私钥持久化。
  - 最低可用地址分配。
  - 删除后的地址复用。
  - 设备表持久化。
  - 旧 `peer.json` 向 `devices.json` 迁移。

结论：

> 服务端 WireGuard 接口、稳定身份、多设备数据模型和 overlay 地址池方案成立。

多台真实客户端同时在线尚未实测，但该项不再依赖新的底层技术能力。

### 3.4 链路 D：macOS IPv6 UDP 直连

验证目标：

- 不使用 Network Extension 时，macOS 是否能通过随包 `wireguard-go` 创建系统隧道。
- IPv6 UDP 是否能直接到达 NAS WireGuard 端口。

验证路径：

```text
macOS utun4
  ↓ wireguard-go
[NAS 公网 IPv6]:51820
  ↓
fnOS fncpn0
  ↓
10.203.0.1
```

实测证据：

- NAS 公网 IPv6 可由客户端直接 `ping6` 到达。
- `fncpnctl start` 成功创建 `utun4`。
- `route -n get 10.203.0.1` 指向 `utun4`。
- 客户端与服务端完成 WireGuard 握手。
- 5 次基础 ping：5/5 成功，0% 丢包。
- 100 次 1200-byte payload：100/100 成功，0% 丢包。
- 该组大包测试平均 RTT 为 64.183 ms。
- 服务端设备统计中的 RX/TX 持续增长。

结论：

> macOS 无需 Network Extension，也能通过 root Helper、`wireguard-go` 和 `wg` 建立可用的系统级 WireGuard 隧道；公网 IPv6 UDP 直连路径成立。

### 3.5 链路 E：WireGuard over FN Connect WSS

验证目标：

- WireGuard endpoint 改为本地 UDP bridge 后，能否经 FN Connect 到达同一服务端。
- WSS 中继是否会破坏 WireGuard 握手和数据报。

验证路径：

```text
macOS WireGuard
  ↓ UDP 127.0.0.1:51821
fncpn-client bridge
  ↓ WSS /app/fncpn-poc/wg
FN Connect
  ↓
fncpn-poc bridge
  ↓ UDP 127.0.0.1:51820
fncpn0
```

实测证据：

- `fncpn-client bridge` 成功连接：

  ```text
  connected to wss://<fn-id>.fnos.net/app/fncpn-poc/wg
  ```

- 中继 profile 使用：

  ```json
  {
    "mode": "relay",
    "endpoint": "127.0.0.1:51821"
  }
  ```

- `fncpnctl start` 保持同样的 `utun4` 管理方式。
- 5 次基础 ping：5/5 成功，0% 丢包。
- 100 次 1200-byte payload：100/100 成功，0% 丢包。
- 该组测试平均 RTT 为 93.127 ms，最大 RTT 为 312.303 ms。
- 服务端设备握手时间和 RX/TX 正常更新。

结论：

> WireGuard datagram 可以完整地通过 FN Connect WSS 中继传输。该路径延迟和抖动高于 IPv6 UDP，但作为兼容性兜底链路可用。

### 3.6 链路 F：中继中断与恢复

验证目标：

- WSS bridge 与 WireGuard 隧道是否可以独立恢复。
- 中继断开是否要求重建 utun。

实测过程：

1. 中继工作时停止 `fncpn-client bridge`。
2. `fncpnctl status` 仍显示接口 active。
3. 对 `10.203.0.1` 的 3 次 ping 全部超时。
4. 重新启动 bridge。
5. bridge 自动重新连接 WSS。
6. 不重建 `utun4`，再次执行 5 次 ping 全部成功。

结论：

> bridge 是可独立重连的运输层。WSS 短暂中断不需要重新创建设备或 WireGuard 接口，正式实现可以把 bridge 重连收敛到用户态连接状态机。

### 3.7 链路 G：停止与路由清理

验证目标：

- 停止连接后是否清理系统路由。
- 重复停止是否安全。

实测结果：

- `fncpnctl stop` 后状态为 `active: false`。
- `10.203.0.1` 路由从 `utun4` 恢复到默认网关 `en0`。
- 再次执行 `stop` 仍成功，未产生错误或残留。

结论：

> Helper 的基本停止流程和路由清理具备幂等性。

### 3.8 链路 H：Helper 权限边界

验证目标：

- Helper 是否只允许固定操作。
- 非当前控制台用户是否会被拒绝。

实测结果：

- 发送不存在的 `exec` 操作时返回 `unsupported operation`。
- 使用 `nobody` 调用时，Helper 返回 `unauthorized caller`。
- macOS `LOCAL_PEERCRED` 与 `LOCAL_PEERPID` 测试确认得到的 UID/PID 与实际连接进程一致。
- `0.1.0` CLI 会把无 request ID 的鉴权错误显示成 `helper response ID mismatch`，但 Helper 已经完成拒绝，不构成越权。

结论：

> 普通用户 CLI 与 root Helper 之间的固定操作白名单和 peer credential 校验有效。`0.1.0` 的问题仅是错误展示，不影响访问控制结论。

### 3.9 链路 I：FN Connect 凭证

验证目标：

- 普通 WebSocket 客户端是否能携带 fnOS 登录态通过统一网关认证。

实际发现：

- `mode=relay` 是进入 FN Connect 中继模式的 Cookie。
- 实际认证 Cookie 包含 `entry-token`、`osrt` 和 `ost`。
- Cookie 可以从 Safari Network 面板中的已认证请求获取。
- 将 Cookie 保存为权限 `0600` 的文件后，CLI WSS bridge 可以通过网关认证。
- `document.cookie` 无法读取 HttpOnly Cookie。
- PoC 假设的 `fnos-token` 没有被 FN Connect 代理透传给应用，因此服务端导出方案不可用。

结论：

> “非浏览器客户端携带 fnOS 会话建立 WSS”已经验证可行；“正式客户端如何自动、持续地获得和刷新凭证”没有在 PoC 中产品化，需要由客户端内置 WebKit 授权流程解决。

## 4. 自动化验证

当前自动化测试覆盖：

| 范围 | 主要内容 |
|---|---|
| `cmd/fncpn-client` | 本地 UDP 与 WebSocket 会话双向转发 |
| `internal/server` | 网关前缀、relay 识别、WebSocket 回声、Origin 校验 |
| `internal/server/wireguard_handlers` | 状态接口、peer 配置、WSS ↔ UDP 往返 |
| `internal/wireguard` | 私钥、设备地址、持久化和旧配置迁移 |
| `internal/macoshelper` | profile 校验、配置渲染、peer UID/PID |
| `cmd/fncpnctl` | Helper response ID 校验 |

已执行并通过：

```text
go test -race ./...
go vet ./...
```

构建产物：

```text
dist/fncpn-poc-0.4.1-x86.fpk
dist/fncpn-poc-0.4.1-arm.fpk
dist/FnCPN-0.1.0-unsigned.pkg
dist/FnCPN-0.1.1-unsigned.pkg
```

PoC 实际链路验证使用 macOS `0.1.0`。`0.1.1` 只修正 CLI 对 Helper 鉴权失败的错误展示。

## 5. 验证结论矩阵

| 命题 | 证据类型 | 结论 |
|---|---|---|
| fnOS 应用可创建内核 WireGuard 接口 | 真机 | 已证明 |
| fnOS 应用具有所需网络权限 | 真机 | 已证明 |
| fnOS 服务端可持久化密钥和设备 | 真机 + 自动化 | 已证明 |
| 多设备地址池和配置模型可行 | 自动化 | 已证明 |
| 多台真实客户端可同时连接 | 未实测 | 尚未证明 |
| FN Connect 可转发第三方二进制 WSS | 真机 | 已证明 |
| WireGuard over FN Connect WSS 可用 | 真机 | 已证明 |
| macOS 无 Network Extension 可创建系统隧道 | 真机 | 已证明 |
| IPv6 UDP 直连可用 | 真机 | 已证明 |
| WSS bridge 可独立重连并恢复流量 | 真机 | 已证明 |
| Helper 操作白名单和调用者校验有效 | 真机 + 自动化 | 已证明 |
| 停止后路由清理和重复停止 | 真机 | 已证明 |
| 自动获取和续期 FN Connect 凭证 | 未实现 | 尚未证明 |
| 自动选择局域网、IPv6 和中继 | 未实现 | 尚未证明 |
| 自动处理网段冲突 | 未实现 | 尚未证明 |
| 访问 NAS 所在完整 LAN | 未实测 | 尚未证明 |
| fnOS ARM64 真机运行 | 仅构建 | 尚未证明 |

## 6. PoC 未覆盖边界

以下项目没有完成，但不影响核心技术可行性结论：

- 自动判断当前客户端是否与 NAS 位于同一局域网。
- IPv6 失败后自动切换中继，以及 IPv6 恢复后自动切回。
- 网段重叠时自动选择整段路由或单主机路由。
- 客户端内置 WebKit 登录、Cookie 写入 Keychain 和自动续期。
- GUI 自动管理 WSS bridge。
- 两台以上真实客户端同时在线。
- 访问 NAS LAN 内其他设备及完整转发/NAT。
- fnOS ARM64 真机安装。
- 长时间中继 soak、极端丢包和性能上限。
- DNS 设置、睡眠唤醒、异常崩溃和升级卸载的完整产品级回归。

这些工作属于自动化、产品化和可靠性工程，不要求新的基础协议或系统能力。

## 7. 最终结论

PoC 已经证明以下核心架构闭环：

1. fnOS 第三方应用可以运行内核 WireGuard 服务端。
2. FN Connect 可以把第三方应用的二进制 WebSocket 从公网转发到 NAS。
3. WireGuard 加密 datagram 可以通过该 WebSocket 中继稳定往返。
4. macOS 无需 Network Extension 和付费开发者账号，也能通过随包工具创建系统隧道。
5. 同一个服务端和设备身份可以分别通过 IPv6 UDP 和 WSS 中继工作。
6. WSS 中断恢复不要求重建 WireGuard 接口。
7. root 网络操作可以被限制在小型 Helper 内，普通用户进程负责凭证和中继。
8. 多设备公钥、overlay 地址池和设备生命周期模型能够支撑正式实现。

因此可以得出明确结论：

> **FnCPN 的核心想法完全可实现。当前剩余工作是把已验证的能力收敛为自动授权、自动选路、完整 LAN 转发和稳定客户端体验，而不是继续证明底层链路是否成立。**
