# FnCPN 接口契约

## 远程 HTTP / WebSocket

外部统一前缀为 `/app/fncpn`，应用内部路由如下。server daemon 只监听应用私有 Unix Socket。
网关验证登录并剥离外部伪造的 `X-Trim-*`，注入可信用户身份。已登录用户可以注册设备、读取配置和建立 relay；管理接口仅限管理员。

| 方法 | 路径 | 权限 / 行为 |
| --- | --- | --- |
| GET | `/version` | 已登录；最小 `{ "serverVersion": "…" }`，不依赖网络配置成功 |
| GET | `/api/v1/bootstrap` | 已登录；服务端版本、网络与管理员标记 |
| POST | `/api/v1/devices` | 已登录；按公钥幂等注册并返回配置 |
| GET | `/api/v1/devices/{id}/config` | 已登录；读取设备配置，支持配置长轮询 |
| GET | `/api/v1/devices/{id}/local-probe` | 已登录；取得局域网身份证明所需配置 |
| GET | `/api/v1/admin/snapshot` | 管理员；管理页完整快照 |
| GET | `/api/v1/admin/devices` | 管理员；设备和实时活性 |
| DELETE | `/api/v1/admin/devices/{id}` | 管理员；仅允许删除实时观测为离线的设备 |
| PUT | `/api/v1/admin/networks` | 管理员；更新网络范围 |
| GET | `/relay/v1/wireguard` | 已登录；升级二进制 WebSocket |

路由与 DTO 的源码依据为 `internal/server/http.go`、`internal/model/model.go`；
配置监听使用 `watch=1` 与 `after=<cursor>`；游标相同时最多等待 25 秒，再返回当前快照。具体响应字段以 `deviceConfiguration` 与 `internal/client/remote.go` 为准。
响应携带 `X-FnCPN-Version` 和 `X-Request-ID`，客户端携带 `X-FnCPN-Client-Version`。
注册、网络配置与重连前检查版本，仅允许 MAJOR 相同且 client MINOR 不大于 server MINOR。
完整约束见 [版本与兼容性](versioning-and-compatibility.md)。
远程响应容忍未知字段，仍校验必需字段、类型和安全语义；请求、本地 IPC、持久化文件保持严格解码。

## 数据面与局域网探测

LOCAL 通过 nonce / HMAC proof 绑定服务端身份与当前版本，不能仅凭 IP 可达就放行。
探测服务仅监听私网地址；实现见 `internal/server/probe.go` 与 `internal/model/probe.go`。
DIRECT 使用 IPv6 UDP WireGuard 握手判断可用性，网页端口探测不能替代握手。
RELAY 每个 binary message 承载一个 WireGuard datagram；每条连接使用独立 UDP socket，
目标固定为服务端 WireGuard 端口。限制帧、队列和并发；无有效 WireGuard 私钥无法访问内网。
relay 不以 Origin/Host 匹配代替身份认证，剩余资源占用风险见 [错误诊断中的 Origin 决策](diagnostics.md#origin-决策)。

## 本地进程通讯协议

### 用户 CLI/UI 与 client daemon

- 使用当前用户私有目录中的 Unix domain socket，父目录权限 `0700`，socket 权限 `0600`。
- `fncpn client daemon` 从内核读取 peer UID，必须与 daemon UID 一致。
- 协议使用 4-byte big-endian 长度前缀加 UTF-8 JSON，单帧最大 256 KiB。
- 每个请求包含协议版本和唯一 ID；响应必须回显 ID。
- 拒绝未知字段、未知方法和超限消息。

### 请求与响应

请求包含 `version`、`id`、`method` 和可选 `params`；响应包含独立 IPC `version`、
`productVersion`、回显 `id`、`ok`，以及 `result` 或结构化 `error`。
常量与编码以 `internal/ipc/protocol.go`、`internal/ipc/codec.go` 为准；
本地组件成套升级，不能用 IPC 版本替代远程产品版本检查。
错误字段和分类见 [错误诊断](diagnostics.md)。

### client daemon 与 client privileged-daemon

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

### server daemon 与 server privileged-daemon

- 使用 fnOS 应用私有目录中的 Unix domain socket，归属 `root:<app-group>`，权限 `0660`。
- 普通 server daemon 是唯一允许连接该 socket 的非 root 身份。
- 方法只允许 `status`、`apply` 和 `remove`。
- `apply` 携带完整期望状态，不发送增量系统命令或版本号。
- privileged-daemon 独立校验 overlay、peer 地址、监听端口、转发网段和防火墙规则。
- 服务端私钥只由 privileged-daemon 生成和读取；普通 server daemon 只获得服务端公钥。
- HTTP/WSS 请求及其 Header、Cookie 和原始 body 不得透传到 privileged-daemon。
