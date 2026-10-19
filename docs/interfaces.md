# FnCPN 接口契约

## 远程 HTTP / WebSocket

外部统一前缀为 `/app/fncpn`，应用内部路由如下。控制接口只监听应用私有 Unix Socket；私网探测与 DNS 服务另复用 LAN TCP 54790。
网关验证登录并剥离外部伪造的 `X-Trim-*`，注入可信用户身份。已登录用户可以注册设备、读取配置和建立 relay；管理接口仅限管理员。

| 方法 | 路径 | 权限 / 行为 |
| --- | --- | --- |
| GET | `/version` | 已登录；最小 `{ "serverVersion": "…" }`，不依赖网络配置成功 |
| GET | `/api/v1/bootstrap` | 已登录；服务端版本、网络与管理员标记 |
| POST | `/api/v1/devices` | 已登录；按公钥幂等注册并返回配置 |
| GET | `/api/v1/devices/{id}/config` | 已登录；读取设备配置，支持配置长轮询 |
| GET | `/api/v1/devices/{id}/local-probe` | 已登录；取得局域网身份证明所需配置 |
| POST | `/api/v1/admin/dns-test` | 管理员；测试 NAS 侧 IPv4 A 名称解析 |
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
bootstrap 增加可选 `hostname`、`nasInfo` 响应字段。`nasInfo` 包含 `fnId`、`defaultDNSSuffix`、
`fnConnectState`（`ready/not-configured/unavailable`）、`managementScheme`（`http/https`）、
`managementPort`、`managementState`（`ready/unavailable`）。普通 server daemon 通过本机
`/run/trim-connect.sock` 的 `GET /trimfn` 读取 FN ID，通过
`/usr/trim/etc/network_gateway_setting.conf` 读取管理端口及 `force_https`，不读取或传输登录凭据。
读取上限为 FN ID 响应 1024 字节、网关配置 4096 字节，HTTP 最长 1 秒，不跟随重定向；
未知配置字段可忽略，必需端口和协议字段仍需校验。两项读取独立降级，失败不阻断 bootstrap，
不清空持久化自定义后缀；页面不从 URL 参数或页面域名推断这些系统信息。

远程响应容忍未知字段，仍校验必需字段、类型和安全语义；请求、本地 IPC、持久化文件保持严格解码。

## 数据面与局域网探测

LOCAL 通过 nonce / HMAC proof 绑定服务端身份与当前版本，不能仅凭 IP 可达就放行。
私网 HTTP 服务默认监听 NAS LAN IP 的 TCP 54790（配置的自定义探测端口同样适用）。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| POST | `/api/v1/probe` | nonce / HMAC 身份与版本证明，不接受登录 Cookie |
| POST | `/probe` | v1.x.x 客户端兼容入口，语义不变，v2.x.x 将废弃 |
| POST | `/api/v1/dns-query` | 无需额外鉴权的私网 LAN DNS / mDNS 代查 |

新客户端优先版本化探测路径，仅收到 404 时尝试旧路径，仍校验证明及版本准入。
登录、配置、管理和 relay 继续使用 `/app/fncpn/`，私网接口不信任 `X-Trim-*` 身份。
实现见 `internal/server/probe.go` 与 `internal/model/probe.go`。
DIRECT 使用 IPv6 UDP WireGuard 握手判断可用性，网页端口探测不能替代握手。
RELAY 每个 binary message 承载一个 WireGuard datagram；每条连接使用独立 UDP socket，
目标固定为服务端 WireGuard 端口。限制帧、队列和并发；无有效 WireGuard 私钥无法访问内网。
relay 不以 Origin/Host 匹配代替身份认证，剩余资源占用风险见 [错误诊断中的 Origin 决策](diagnostics.md#origin-决策)。

## 局域网域名解析（1.1.0）

### 自动生效与版本

DNS 是连接的内建能力，不设启用开关，不要求单独启动进程或手动填写后缀。
服务端安装/升级后自动准备隔离后缀、LAN DNS 检测和 mDNS 代查；客户端安装/升级后沿用已有授权和连接意图，
在版本准入通过、连接进入 LOCAL/DIRECT/RELAY 后自动应用 DNS。新安装仍需完成首次授权；
已暂停或主动断开的客户端不因升级自动连接。

唯一远程准入规则保持不变：MAJOR 相同且 client MINOR 不大于 server MINOR。
1.0 client 连接 1.1 server 时忽略新增响应字段；1.1 client 连接 1.0 server 时阻止连接并
提示升级 server，不能以关闭 DNS 绕过检查。DNS 运行故障才允许独立降级。

### 名称隔离、自动配置与解析规则

目标是复用 NAS 所在 LAN 已有的 mDNS 和单播 DNS 名称，不要求逐设备配置静态表。
FnCPN 只接管独立后缀，不接管客户端原有 `.local` 或家庭 DNS 后缀。

- `<NAS hostname>.<后缀>` 为内建 NAS 名称，返回已启用家庭网段内的 NAS 本机地址，不依赖家庭 DNS/mDNS。
  NAS 当前 hostname 的便捷入口保留给 NAS，TTL 为 0，仅返回 IPv4 地址。管理页按实际协议和管理端口生成打开链接。
- 默认访问后缀为 `<FN Connect ID>.fncpn`，例如 `myhome.fncpn`；管理员可修改 `dnsSuffix`。
  服务端下发自定义后缀；未设置时客户端依据已有 FN Connect ID 生成默认值。
  已有自定义值升级后保留，无启用开关。
  `.fncpn` 是产品约定的本地 split DNS 命名空间，不声称为 IANA 保留域，查询不发送公共 DNS。
- FN Connect ID 转小写后必须满足 DNS label 约束；不合法时使用 `id-` 加原 ID 的 SHA-256
  前 20 位十六进制作为 label，保证稳定、可复现，不直接拼接任意用户输入。
  用户界面始终展示实际后缀。FN Connect ID 变化时默认后缀随之更新，自定义后缀不自动改变。
- 后缀本身（如 `rectcircle-mi-nuc.fncpn`）直接解析到 NAS，与 `<NAS hostname>.<后缀>` 一样返回 NAS LAN IPv4 地址。
- `abc.<后缀>` 优先查询 NAS 家庭 DNS；短名称按系统 search domains 展开后再查询裸名称。
  无上游、无记录（NXDOMAIN/NODATA）或上游不可用时，尝试 mDNS `abc.local`。
  不固定来源；普通 DNS 缓存过期后重新按上述顺序查询。同名设备需明确使用 `.local` 入口时不走家庭 DNS。
- `abc.local.<后缀>` 明确查询 NAS LAN 的 mDNS `abc.local`，不向单播 DNS 转发。
  这是访问同名 mDNS 设备的确定入口，不接管本机 `abc.local`。
- `abc.home.lan.<后缀>` 明确查询 NAS LAN DNS 的 `abc.home.lan`，不添加搜索域，
  不回退 mDNS。多 label 输入保留原名，避免擅自修改用户指定名称。
- 例：后缀 `myhome.fncpn` 下，`abc.myhome.fncpn` 自动解析；需要指定来源时使用
  `abc.local.myhome.fncpn` 或 `abc.home.lan.myhome.fncpn`。两端安装/升级并恢复连接后
  自动生效；没有发布名称、没有 DNS 记录的设备不能凭空被发现。
- 单个活动 NAS 安装一套后缀规则。切换 NAS 替换规则并清空缓存。自定义后缀不得与现有
  resolver/NRPT 规则冲突；冲突报告受限，不覆盖其他软件。只对带 FnCPN 后缀的全名生效，
  不修改客户端搜索域，不承诺直接输入裸 `abc`。
- `ServerSettings` 仅增加可选 `dnsSuffix`；上游和搜索域自动读取 NAS 系统配置，
  不提供手动配置项。mDNS 自动在已启用 LAN CIDR 对应接口上运行，不经过 overlay 或 WAN。
- 自动上游优先取有效系统 resolver 的 LAN DNS，并读取 systemd 的真实上游配置；
  无真实 LAN 上游时允许系统明确配置的本机 loopback stub。NAS server 不监听 DNS 53，
  查询去掉 FnCPN 后缀且受总超时限制，不与本机 FnCPN 前端形成递归。真实上游限制在已启用 LAN 范围。
- 后缀及搜索域规范化小写，采用 ASCII LDH label，单 label ≤63、总长 ≤253 字节；
  后缀必须多 label，拒绝路径字符、空 label、IP 字面量、`local`/`localhost` 等特殊后缀。
  查询允许末尾根点；按 label 边界剥离后缀，检查包装后的完整域名长度。

`ClientConfiguration` additive 增加 `dnsSuffix`（空时客户端按 FN ID 生成）、`dnsRevision`；检测到上游、搜索域或
LAN 接口变化时推进配置游标和 revision，客户端失效缓存。旧 client 忽略新增字段。
管理页仅提供后缀设置、NAS 访问链接和名称解析测试；测试显示实际地址及来源。

### 解析请求与 DNS 语义

端口 `54790` 的职责为 NAS 私网 API（Private API），承载身份探测和 DNS 查询。
内部服务名为 `PrivateAPIService`，共享端点配置为 `PrivateAPIConfiguration`；其中
`Endpoints` 供两种能力使用，`Key` 仅供身份探测。DNS 客户端仅接收端点列表。
为兼容已发布 v1 客户端，配置获取 URL `/api/v1/devices/{id}/local-probe`、
JSON `endpoints`/`key` 和原有凭据存储标识不变；这些历史名称不再代表端口仅用于探测。

`POST http://<NAS LAN IP>:54790/api/v1/dns-query` 直接使用私网 HTTP，默认信任内网环境，
不增加请求/响应签名、时间戳、nonce、防重放或登录鉴权，也不依赖设备 ID、设备注册或启用状态。
客户端不携带登录 Cookie 或 Authorization，不读取设备探测密钥；既有 `/probe` 身份证明独立保留。
请求严格解码 `{ "name": "…", "type": "A" }`，最多 4096 字节；name 为去除隔离后缀后的 NAS 侧名称；空字符串表示直接访问后缀本身，返回 NAS 的 LAN IPv4 地址。
成功直接返回 `DNSAnswer`，失败使用普通 HTTP 状态码及统一错误对象。
沿用普通版本头 `X-FnCPN-Client-Version` / `X-FnCPN-Version`，不增加 DNS 专用版本协商。
答案包含 `name`、`type`、`result`、`addresses`、`ttl`、`source`、`revision` 和 `resolvedName`。
result 为 `answer`、`nodata`、`nxdomain`；source 为 `mdns`、`lan-dns` 或内建 NAS 名称的 `nas`。
addresses 最多 32 条；TTL 保留上游剩余值（允许 0，上限 3600），mDNS 当前采用每次实时查询，不保留地址缓存，返回 TTL 0，忽略 goodbye（TTL 0）地址记录；
前端向系统返回的 LAN DNS 正记录 TTL 最多 30 秒，限制系统旧答案的存活时间。
不识别的响应字段可忽略，必需字段和地址安全语义必须验证。

客户端先校验并剥离 FnCPN 后缀；服务端校验去包装名称，再按照上述规则选择解析来源；只接受 IPv4 A 查询，
不接受任意 DNS 报文或调用者指定的上游。单播 DNS 使用明确服务器，不递归调用自身。
CNAME 最多跟随 8 跳并检测环路，每一跳保留包装前的真实名称；最终只返回已启用 LAN/overlay
范围内可用的单播地址，拒绝 loopback、未指定、组播和链路本地地址。
服务端在选定 LAN 接口用临时 UDP 源端口发送 RFC 6762 legacy unicast-response 查询，
以 TTL 255 发往 224.0.0.251:5353；核对响应 ID、名称、发送端口和 LAN 地址，只接受 A 地址记录。
不占用系统已有的 5353 监听器，不在隧道传输或向客户端重播组播。实时查询不维护 mDNS
地址缓存，因此无需依赖长期缓存的 cache-flush；设备退出后后续查询重新判定。
mDNS 无应答仅说明本次未发现，不是权威 NXDOMAIN：便捷入口和显式 mDNS 查询无应答
返回 NODATA、TTL 0，不长时间否定缓存。mDNS 不支持 DNS-SD 服务列表浏览，本期支持名称地址解析。

- 名称不存在返回 NXDOMAIN；名称存在但没有所请求类型返回 NOERROR 空答案（NODATA）。
- 上游超时、无可用上游、无安全可达地址返回 UNAVAILABLE，客户端合成为 SERVFAIL；
  答案地址全部被已启用网段过滤时，附带 `remoteCode: DNS_NO_REACHABLE_ADDRESS`，供管理页显示
  “未找到当前连接可用的地址”，原始错误收进可展开的技术详情；不将其他上游故障伪装成无地址。
  并发资源耗尽返回 RESOURCE_EXHAUSTED，同样映射 SERVFAIL。故障不缓存为不存在。
- NXDOMAIN/NODATA 按上游 SOA 负 TTL 缓存，最多 30 秒；无可用负 TTL 不缓存。
  缓存隔离 NAS 身份、revision、规范化名称与类型；mDNS 未发现不进入否定缓存。
- 客户端对答案再做实际路由校验。DIRECT/RELAY 不返回被本地重叠网段排除的 LAN 地址；
  LOCAL 只返回当前 LAN 可达范围。过滤后没有地址时显示路由冲突或不可达原因，返回 SERVFAIL，
  防止访问到当前网络中的同地址设备。不因名称解析而绕过已有路由冲突策略。
- 本地 DNS 前端支持 UDP/TCP、EDNS 大小约束、TC 截断与 TCP 重试；只接受单 question、
  IN class 的普通查询，拒绝畸形报文及压缩指针循环。不声明 DNSSEC 已验证。
  白名单内未支持的记录类型（含 HTTPS/SVCB、TXT、SRV、PTR）返回 NOERROR 空答案，
  不谎报 NXDOMAIN；不匹配白名单的查询返回 REFUSED。
- HTTP 单次解析总预算 2 秒，上游和 CNAME 链共享预算；客户端合并重复在途查询。
  不设置固定 QPS 限制；保留全局在途 128、本地待处理 128 的并发上限，满时立即失败。
  有限重试不得形成应用查询乘以多层重试的放大。

### 本地应用与生命周期

DNS 计划独立于隧道网络计划，LOCAL 无须创建 utun、安装 LAN 路由或建立 WireGuard。
DIRECT/RELAY 切换不重建相同的 DNS 规则；切换 LOCAL 时按实际 LAN 路由校验答案并失效缓存。
DNS 统一使用上述私网 HTTP 入口，不增加端口，也不依赖 FN Connect 网关、证书或会话刷新。
LOCAL 经物理 LAN；DIRECT/RELAY 经已安装的 WireGuard LAN 路由到同一个 NAS LAN IP。
发送前和返回答案时均检查实际路由；网段冲突、地址不可达时 DNS 独立降级并自动重试，不回退其他入口。
HTTP 不加密，不增加应用层鉴权和防重放；仅复用已有配置中的 NAS LAN 端点。

- macOS：使用 `/etc/resolver/<domain>` 的 `nameserver 127.0.0.1` 和显式 `port`，由 client
  daemon 在 loopback 高位端口提供 UDP/TCP DNS。绑定两种传输成功后才向 privileged-daemon
  提交所选端口；不采用特权 DNS 转发 IPC，不依赖 overlay 地址。
- Windows：使用专用 loopback 地址的 UDP/TCP 53 监听器和 NRPT；自动探测可用 IPv4 loopback IP，端口固定为 53；
  Dnscache 行为仍须完成支持系统实机验收。不使用高位端口（NRPT 不提供此能力）。
  优先复用上次选择的地址，再探测 `127.0.0.153`–`127.0.0.216`；
  每个候选必须同时成功绑定 UDP/TCP 53，失败释放部分绑定，选定后才提交实际地址至 NRPT。
  全部候选冲突、命名空间规则冲突或实际生效策略未指向本产品监听器时报告降级并自动重试，不覆盖已有服务或组策略；策略路径存在时通过系统 `Get-DnsClientNrptPolicy -Effective` 检查实际生效结果。只绑定 loopback，不暴露 LAN/隧道入口。
- privileged-daemon 独立校验后缀、loopback 地址与端口范围，仅安装本产品 DNS 规则。
  同名外部规则冲突不覆盖；产品自有旧规则可按 journal 中的所有权和原内容校验后更新。
  NRPT 使用稳定产品标识，resolver 使用受控文件与所有权记录；不覆盖符号链接。
- 顺序为监听器就绪、提交清理 journal、原子安装规则、发布 DNS 就绪状态；失败回滚 DNS
  子事务，不回滚已成功的数据隧道。daemon 崩溃由组件监督器重启并先清理残留再重装；
  privileged-daemon 用 20 秒租约及每 5 秒清理周期检查 owner 心跳，失联移除规则，避免黑洞。
  客户端在状态变化时更新规则，并每 30 秒兜底检查；每 5 秒通过 `renew-dns` 轻量续约，续约不读取系统规则。
- 改记录、换后缀/上游、切网、切换 NAS、休眠恢复均重新验证并清理相关缓存；Windows 刷新系统 DNS 缓存，macOS 依靠 resolver 配置监视更新；
  系统正记录 TTL 最多 30 秒。断开、暂停、退出、卸载移除自有规则并停止监听；清理前检查内容未被
  外部修改，不删除其他软件资源。升级按现有连接意图自动恢复。
- 状态独立显示 `preparing/ready/degraded` 及结构化原因、当前后缀；管理页仅测试 IPv4 A，显示来源、实际名称和地址。
  失败自动退避重试，最长退避 30 秒，不要求点击启用；IP 访问保持工作。应用自行使用 DoH 或
  绕过系统解析器不在保证范围，HTTPS 证书和访问权限仍由目标服务负责。

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
  `get-client-secret`、`put-client-secret`、`delete-client-secret`。DNS 规则通过独立的 `apply-dns`、`renew-dns`、`remove-dns` 管理。
- `apply` 接收完整结构化网络计划，privileged-daemon 独立复验并收敛地址、接口、
  路由和 endpoint；独立 `apply-dns` 计划包含 generation、suffix、address、port，与隧道独立收敛。
  owner 绑定 IPC peer identity，不能由请求冒充其他用户；generation 防止旧进程清理新规则。
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

### 局域网权限等待

本地用户 IPC `continue-remote` 释放 daemon 当前的局域网权限等待，不启用 AutoConnect，
不注册设备，也不改变远程版本准入。状态响应可选字段 `localNetworkAccess` 表示
原生连接观察结果，`localNetworkWaiting` 表示是否正在等待用户处理权限。
`recheck-network` 只提交自动健康检测，遵守暂停与退出登录意图；本地组件成套升级。

### 1.1 DNS 数据升级

server state schema 1 → 2，client config schema 2 → 3；本地 IPC 版本为 5，两端原生界面同步更新。
读取旧正式 schema 并验证后，先保留 `state.schema-1.backup.json` / `config.json.schema-2.backup`，
再以原子写入提交新 schema。保留设备、网络、身份凭据和 autoConnect；DNS 设置缺省自动生效。
旧程序拒绝新 schema；回滚需恢复旧程序及对应备份，不自动清空或重新授权。
