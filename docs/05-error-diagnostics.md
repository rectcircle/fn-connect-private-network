# 错误码与全链路诊断

当前为未发布调试版本，客户端、GUI、服务端与特权进程直接使用同一份错误契约，
不提供旧版兼容或错误字段迁移。协议号仍为 `4`。

本文描述诊断规则与受控回归；各版本的现场失败、修复后用户确认及尚未覆盖的范围，
统一记录在 [发布验收的实机记录](04-release-acceptance.md#实机验收记录)。

## 错误契约

`internal/model.Error` 在 HTTP、IPC、授权结果、状态快照和诊断输出中使用相同字段：

| 字段 | 含义 |
| --- | --- |
| `code` | 稳定分类，决定客户端是否要求登录、暂停或重试 |
| `message` | 简短原因，不以 `internal error` 替代已知业务错误 |
| `retryable` | 是否可以自动重试；不能只根据 HTTP 状态猜测 |
| `operation` | 最具体的失败阶段，例如 `relay.handshake ...`、`ipc.apply`、`configuration.load` |
| `detail` | 脱敏后的底层原因，包括主操作及回滚失败；不是原始响应转储 |
| `httpStatus` | 实际 HTTP/WSS Upgrade 状态；没有 HTTP 交互时省略 |
| `remoteCode` | 外部服务业务码，例如 FN Connect `5000`、`3000037` |
| `requestId` | 错误源的请求 ID，用于跨日志定位 |

Go 的 `Cause` 保留本地 `errors.Is/As` 错误链，不序列化。跨进程由 `detail`
传递可安全显示的原因，GUI、管理页和 CLI 不再只显示 `message`。
`diagnose.errors` 列出本次诊断中同时出现的特权、物理网络和配置读取问题，
不因为已有 `status.lastError` 而丢弃其他诊断失败。

示例：

```json
{
  "code": "PERMISSION_DENIED",
  "message": "remote request returned HTTP 403 Forbidden",
  "retryable": false,
  "operation": "relay.handshake wss://example.fnos.net/app/fncpn/relay/v1/wireguard",
  "detail": "forbidden origin; failed to WebSocket dial: expected handshake response status code 101 but got 403",
  "httpStatus": 403,
  "requestId": "example-request"
}
```

## 分类

| 错误码 | 典型场景 | 常见 HTTP 映射 | CLI 退出码 |
| --- | --- | --- | --- |
| `INVALID_ARGUMENT` | 参数、网络计划或请求体非法 | 400 | 2 |
| `AUTH_REQUIRED` | 缺少或失效的网关会话 | 401 | 3 |
| `PERMISSION_DENIED` | 管理员/控制台 UID 权限不足、网关 403、系统权限失败 | 403 | 6 |
| `NOT_FOUND` | 资源不存在 | 404 | 7 |
| `ALREADY_EXISTS` | 资源冲突 | 409 | 5 |
| `FAILED_PRECONDITION` | 本地配置损坏、状态或前置条件不满足 | 412 | 8 |
| `DEVICE_REVOKED` | 设备删除或禁用 | 410 | 7 |
| `UNAVAILABLE` | DNS/TCP/TLS 连接失败、服务未就绪、502/503、中继断线 | 503 | 4 |
| `CONFLICT` | 并发状态冲突 | 409 | 5 |
| `TIMEOUT` | context/网络超时、握手超时、授权过期 | 504 | 124 |
| `CANCELED` | 用户取消或父 context 终止 | 499 | 130 |
| `INTERNAL` | 无法归入已知类别的内部故障 | 500 | 1 |
| `PROTOCOL_ERROR` | 非法 JSON、IPC 协议/响应 ID 错误、错误的 WSS Upgrade、探测证明不匹配 | 400 | 8 |
| `RESOURCE_EXHAUSTED` | 中继连接容量、HTTP 429、磁盘/文件描述符耗尽 | 429 | 4 |
| `DISCOVERY_FAILED` | FN Connect 返回非零业务码 | 外部响应可能仍为 200 | 4 |

外部响应中的 HTTP 状态保留原值。已知结构化错误保持它自己的分类；
无结构化错误时，401 映射登录失效，403 映射权限拒绝，不能把二者混为一谈。
fnOS 网关也会以 HTTP 200、纯文本 `invalid token` 拒绝缺失或无效的登录会话。
API 与 WSS 统一将这一完整响应识别为 `AUTH_REQUIRED`，保留真实 HTTP 200 和
网关原因，提示重新授权；不将它误报为 JSON 解码失败。只匹配这条已确认的响应，
其他坏 JSON、包含相似文本的页面、读取失败或超限响应仍按协议错误处理。
一般 5xx 可重试；容量 429 可重试；本机磁盘/文件描述符耗尽不可盲目自动重试。
FN Connect 非零业务码不伪装为内部程序错误，也不在未知语义下自动重试。

### 应用停用与自动恢复

fnOS 停用应用时，网关可能撤下 `/app/fncpn/` 路由，返回非 FnCPN 错误结构的
HTTP 404，而不只是 502/503。将它归为不可重试的 `NOT_FOUND` 会使 WSS 重拨退出、
客户端清理隧道并停留在 ERROR；之后启用应用也不会自动恢复。

因此，仅对请求路径位于 `/app/fncpn/` 下、且响应未携带可识别 FnCPN 错误结构的
404，统一按 `UNAVAILABLE`、`retryable=true` 处理。HTTP 配置/长轮询与 WSS
共用此规则；真实 HTTP 404、请求阶段、请求 ID 和安全原因仍保留，使用既有退避重试。
空正文、HTML 和未知 JSON 也适用，但正文继续遵守脱敏与大小限制。

其他路径的 404 保持 `NOT_FOUND`；FnCPN 明确返回的结构化错误优先，
不把设备不存在、设备撤销或权限拒绝改成临时停服。401、403、410 和
HTTP 200 `invalid token` 的分类也不变。此规则表示客户端可重试，
不保证服务端会恢复：应用一直停用时仍无法通信，用户可主动断开。

## INFO 里程碑

成功路径也写入原有进程日志。daemon 日志携带 `role` 和 `version`；
HTTP/IPC 请求上下文分别携带 `http_request_id`、`ipc_request_id`，客户端授权调用
继续传递 `authorization_id`。按需记录 `fn_id`、`device_id`、接口、路径和
`elapsed_ms`，不输出完整配置、Cookie、密钥或数据报文。

| 阶段 | 关键 INFO |
| --- | --- |
| 启动/退出 | daemon starting/stopped、`IPC listener ready/stopped`、`server HTTP listener ready`、特权恢复完成 |
| 授权 | `authorization requested`、`authorization credentials received`、`gateway session validated`、`authorization saved/completed`；主动取消单独记录 |
| 注册 | 客户端 `existing device reused` 或 `device registration completed`；服务端同名事件用 `created` 明确区分创建和复用 |
| 选路 | `device configuration ready`、LOCAL 探测开始/结束、FN Connect 发现开始/结束、路由选择、DIRECT 尝试或 RELAY 选择 |
| 建联 | `relay WebSocket connecting/established`、`relay bridge ready`、`client network applied`、`WireGuard handshake waiting/confirmed`、`client connected` |
| 恢复 | `client reconnect requested` 带触发原因；`relay WebSocket reconnecting/reconnected`；维护错误恢复单独记录 |
| 服务端 | bootstrap 权限检查、设备注册/删除、设备连接状态变化、网络设置变化、网络状态变化、中继会话建立/结束 |
| 身份与退出 | WireGuard 凭据存储/删除、客户端断开、退出登录保留身份、忘记配置删除身份 |

WSS 接通不等于 WireGuard 握手成功；两者必须是独立节点。服务器注册完成也不等于
客户端配置已保存或隧道已建立，不能把中间成功当作最终成功。

正常状态/诊断轮询、未变化的配置监听/网络 reconcile、凭据读取、Cookie 自动刷新和
每个数据包不打印 INFO。异常仍按 ERROR/WARN 记录，并保留既有错误原因。

## 同名设备与身份

设备名称只用于展示，注册身份是 WireGuard 公钥。同一公钥重复注册返回同一设备；
保留密钥的重新授权和进程重启不会新增设备。只清理非敏感配置但保留密钥时，
服务端仍按原公钥复用设备。

彻底清空凭据、忘记身份，或从钥匙串切换到不迁移旧密钥的文件存储，会产生新密钥。
此后注册是一个新设备，旧记录不会自动删除。不同设备也可能使用同一个主机名，
因此不能按名称合并；流量和握手显示来自 WireGuard 运行态，也不能单凭 `0 B` 或
`-` 判定历史设备可删除。具体条目应按设备 ID、公钥及创建时间核实。

root 日志中的 `client WireGuard identity stored` 用 `replaced=false` 表示创建凭据文件，
`replaced=true` 表示覆盖已有文件，不记录文件内容。客户端如发现保存的设备公钥与
当前凭据不匹配，会记录 `saved device identity changed` WARN 和原设备 ID；
服务端注册日志再用 `created` 说明实际是否创建记录。

## 离线设备删除

管理页的在线/离线/未知状态来自服务端内存观测，不是设备名称或累计流量判断。
客户端每 25 秒 keepalive，服务端每 5 秒采样各 peer 的 RX 增量；超过 90 秒未见
新增接收才视为离线，握手时间和 TX 增长不作为替代。首次观测、计数归零、
peer/接口变化和监测中断后先标记未知，避免把历史计数当成最近活动。

`DELETE /api/v1/admin/devices/{id}` 仅管理员可调用。接口在写操作锁内重新查询
实时状态：仍在线返回 `FAILED_PRECONDITION`，未知返回 `UNAVAILABLE`，
不存在返回 `NOT_FOUND`；状态读取、网络下发或存储失败保留原始原因，且不绕过检查。
成功时 INFO `device deleted` 携带请求 ID、设备 ID、名称和地址。
`device connection state changed` 只在状态切换时记录前后状态及 `idle_timeout=1m30s`，
不会为每次正常采样刷日志。

管理页以 iframe 嵌入 fnOS 桌面。浏览器原生 `confirm()` 在未授予 `allow-modals`
时会被忽略，旧实现会直接走取消分支，不发送 DELETE，因此服务端没有该次请求日志。
此路径已在受限 iframe 中复现，但不据此推定每个现场无响应都由同一限制导致。
删除和 Overlay 修改改用页面内 `<dialog>`；弹窗打开失败会显示错误，
取消/Esc 不发请求，确认后再次检查当前页面的设备状态，再交由后端实时校验。

删除只清理当前注册记录和 peer，不封禁密钥，管理员仍可以重新注册。
LOCAL 没有 WireGuard 隧道，不属于此活性指标。该规则是超时策略，不能保证检查后
对端绝不会重新发起握手；需要严格会话生命周期时应另行定义协议，而非伪装成确定离线。

## 各段记录职责

| 链路 | 原因来源与记录位置 |
| --- | --- |
| 安装及启动 | 安装脚本保留最后一次健康检查错误并给出日志路径；进程初始化和退出失败进入进程日志及 stderr |
| GUI 登录 | 原生表单校验错误留在页面；daemon 的 `user.login`、会话保存或管理员校验失败通过结构化 IPC 错误返回，不记录密码 |
| 授权等待 | 授权过期有 `TIMEOUT`、阶段和请求 ID 日志；普通主动取消不是 ERROR |
| 设备注册及配置 HTTP | HTTP 状态、已知 JSON 错误或短文本原因保留；JSON 解码、超限、无效游标单独分类 |
| FN Connect 发现 | 记录发现阶段、HTTP 状态或外部业务码/消息，保留网络超时分类 |
| 凭据与配置 | 读取、保存、清除、忘记均附阶段；文件系统错误与特权拒绝进入 IPC/客户端错误链 |
| LOCAL 探测 | 请求拒绝、无效证明、响应读取错误不再静默丢失；未命中 LOCAL 可继续其他选路 |
| IPv6 DIRECT | 握手失败及回退原因记录 WARN；应用/清理失败保留主因和回滚原因 |
| WSS 握手 | 失败正文、HTTP 状态及请求 ID 进入错误；不会将所有 403 当成需要重新登录 |
| WSS 会话及重拨 | 异常读写、重拨失败记录日志并更新客户端诊断；永久拒绝停止重拨，恢复清除对应中继错误 |
| HTTP 服务端 | 鉴权、参数、容量、网络、存储及拒绝响应统一记录；WebSocket 库自己写出的错误也记录状态 |
| IPC | handler 返回失败在写响应前记录；peer 校验、解帧和写回失败分别可见，不输出 params |
| 特权网络 | Apply/Remove 原因经 IPC 返回；WireGuard 状态读取失败同时标记 degraded 和 `lastError` |
| 后台维护 | 配置监听、特权生命周期监听、状态刷新、回滚及清理错误记录具体阶段；已有连接不因临时配置获取失败被无条件拆掉 |

错误在拥有上下文的边界记录，不给每个 `return err` 重复打印日志。
HTTP 生成 `X-Request-ID`；服务端日志的 `http_request_id` 标识当前 HTTP 请求，
`request_id` 保留上游错误来源。IPC 日志的 `ipc_request_id` 标识当前 IPC 调用，
`request_id` 可以继续指向原始服务端或特权请求。使用这些字段关联各段。

## 日志位置

| 进程 | 路径 |
| --- | --- |
| macOS 用户 daemon | `~/Library/Logs/FnCPN/client.log` |
| macOS root daemon | `/var/log/fncpn/client-privileged.log` |
| fnOS server daemon | `/var/apps/fncpn/var/logs/server/server.log` |
| fnOS root daemon | `/var/apps/fncpn/var/logs/privileged/privileged.log` |

fnOS 生命周期失败也写 stderr 和应用中心的临时错误文件，不跨属主写业务日志。
日志轮转上限 10 MiB，保留 5 份。macOS 安装阶段脚本输出由系统安装器收集。

## 脱敏与正常退出

- 不记录请求/响应完整 Header、Cookie、凭据文件、私钥、WireGuard UAPI 或报文内容。
- URL 去除 userinfo、query 和 fragment；凭据赋值、密钥和 JWT 样式内容脱敏。
- `authorization is required`、`read privateKey failed` 是描述，不因出现敏感词而整句丢失。
- HTTP 失败最多读取 8 KiB；WSS 库最多提供失败正文前 1 KiB。HTML、未知 JSON
  只保留类型说明，不导出网页正文。诊断文本最多约 2 KiB，截断时明确标记。
- 不保证任意外部文本都适合日志：新增字段必须按内容审查，不能把脱敏函数当作记录
  任意请求载荷的许可。
- 正常 WebSocket 关闭、主动取消、已关闭 socket 和应用退出造成的状态监听
  `broken pipe` 不作为业务 ERROR；异常断线和其他 IPC 写入失败仍有记录。
- 队列满、过期数据报、非预期本地 UDP 来源属于传输层有意丢弃，不逐包打印，
  避免日志洪泛和泄露 WireGuard 报文。

## Origin 决策

仅 `/relay/v1/wireguard` 不校验 Origin，包含 WebSocket 库的默认 Origin 限制。
原生客户端可以自行填写 Origin，且 FN Connect 可能改写 Host，二者相等不能用作
原生客户端认证。这个接口只搬运 WireGuard 密文：网关认证会话，WireGuard 认证
设备并保护明文。仍保留登录校验、二进制报文、报文大小、连接数和队列限制。

剩余风险是恶意网页可能借已有浏览器登录态占用中继资源；它没有设备私钥时不能
获得隧道权限。这个决定不取消管理 HTTP API 的管理员检查，也不开放 root IPC。

## LAN 转发与 Docker

能访问 NAS 的 overlay 和 LAN 地址，只证明到本机的 INPUT 路径可用，不能证明
FORWARD 路径可用。若 NAS 自身能访问 LAN 设备、`ip_forward=1`、客户端路由正确，
但客户端无法访问该设备，应同时检查 FnCPN 和宿主机其他 base chain。

nftables 中一个 base chain 的 ACCEPT 不是最终放行，后续同 hook 的链仍可 DROP。
本次实机中，FnCPN 的优先级 `-10` 链先放行，随后 Docker 的 `ip filter / FORWARD`
默认 DROP 丢弃了四个 ICMP 请求；数据包未到 POSTROUTING，已有 masquerade 不能补救。

修复仅针对已有的 iptables-nft `DOCKER-USER` 扩展链增加带所有权注释的窄范围规则。
不修改 FORWARD 策略、不清空 Docker 链、不关闭 Docker，也不绕过位于这些规则前的
管理员 DROP。相关读取、同步和清理失败继续经特权 IPC 及服务端日志报告；停止和
卸载时只清理 journal 对应的规则。其他防火墙布局不自动改写。

原理和扩展点参考：
- [nftables base-chain priority 与 verdict 语义](https://wiki.nftables.org/wiki-nftables/index.php/Configuring_chains#Base_chain_priority)
- [Docker 宿主机接口转发规则](https://docs.docker.com/engine/network/firewall-iptables/#allow-forwarding-between-host-interfaces)

## 回归依据

- `internal/client/error_chain_test.go`：真实 Unix IPC → 服务端 HTTP → 客户端 →
  第二次 IPC，验证原因、回滚失败和请求 ID 不丢失。
- `internal/client/errors_test.go`：HTTP/WSS 200 `invalid token`、401、403、404、410、
  429、5xx、HTML、敏感正文、坏 JSON、读取失败、发现业务码和最新中继事件。
- `internal/client/gateway_recovery_test.go`：应用入口 404 的分类边界、结构化永久错误
  优先；真实 HTTP/WebSocket 模拟断线、404/502 和恢复，验证配置监听自动恢复、
  管理器不清理现有网络、不更换设备身份、中继恢复后数据报可往返。特权网络为
  测试替身，不据此声称已验证实机 WireGuard 握手或 NAS 转发。
- `internal/client/manager_logging_test.go`：网关 200 鉴权失败进入 `AUTH_REQUIRED`，
  客户端状态和日志均保留真实 HTTP 状态与原因。
- `internal/client/observability_test.go`、`internal/server/observability_test.go`：
  授权和连接 INFO、请求上下文、跨重新授权/重启的设备幂等、同名不同密钥、
  正常轮询和维护不刷日志，以及凭据不泄漏。
- `internal/privileged/observability_test.go`、`internal/logging/context_test.go`：
  身份文件创建/覆盖/删除事件与请求日志作用域；凭据读取和 Cookie 刷新不刷日志。
- `internal/server/devices_test.go`：RX 超时、计数重置和未知状态保护、实时删除校验、
  权限、失败回滚、并发删除、状态日志及同名记录隔离。
- `internal/server/web/iframe.test.cjs`：跨源受限 iframe 中的真实 Chrome 回归，
  验证确认/取消/Esc、确认期间重新上线、后台拒绝、删除失败保留行和窄屏布局。
- `internal/client/relay_test.go`、`internal/server/relay_test.go`：中继建立、正常结束、
  断线重拨失败与恢复的日志，不包含 URL 查询、Cookie 或报文内容。
- `internal/server/relay_test.go`：网关改写 Host、Origin 缺失/不同均可升级，
  无网关身份仍拒绝，容量限制和二进制转发保留。
- `internal/model/error_details_test.go`、`internal/logging/rotate_test.go`：
  错误链、联合错误、脱敏、字段保留和系统错误分类。
- 授权 Service、原生 AppKit 登录页与管理页测试验证错误展示、失败授权记录，
  以及后台错误不会覆盖尚未提交的表单错误。
- `internal/platform/linux/firewall_docker_linux_test.go`：在独立 Linux 网络命名空间中
  验证真实转发、NAT、Docker 默认 DROP、iptables-nft 兼容性、规则更新和清理，
  不修改宿主机现有网络规则。
