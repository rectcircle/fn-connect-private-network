# FnCPN P0 发布验收

## 实机验收记录

截至 2026-10-02，已完成一轮 RELAY 基础访问、LAN 转发、日常恢复和设备清理验证，
但尚未完成全部 P0 发布验收。下表仅记录用户已回传结果的项目；后文的验收清单
仍是完整测试要求，不能视为全部执行完毕。PoC 结论也不替代本轮正式安装包验收。

### 环境与证据

| 项目 | 本轮条件 |
| --- | --- |
| 执行位置 | 用户操作目标 Mac 和 fnOS，回传终端输出或界面结果；不使用开发机安装状态代替实机证据 |
| fnOS | x86_64，fnOS 1.2.x；最新验证按用户更新流程记录为服务端 `0.1.16`，未另附服务端版本命令输出 |
| macOS | Apple Silicon，macOS 27.0；从客户端 `0.1.13`、`0.1.14` 验证到 `0.1.17`；后者安装后由用户确认版本与连接正常 |
| NAS 网络 | `192.168.71.2`，LAN `192.168.71.0/24`，网关 `192.168.71.1`，物理接口 `wlo1` |
| Overlay | `10.253.203.0/24`，服务端 `10.253.203.1`，当前客户端地址 `10.253.203.3/32` |
| 客户端物理网络 | `en0`，`10.211.55.0/24`，默认网关 `10.211.55.1`；本轮无可用公网 IPv6，`lanOverlap=false` |
| 隧道 | `RELAY / fn-connect`，MTU `1280`；回传输出中接口为 `utun4`，后续验收不依赖固定接口编号 |

“终端输出”表示有诊断、日志、路由或探测结果；“用户确认”表示用户按给定步骤操作后
反馈成功，但未附该次完整输出。两类结果均保留，后者不推导未提供的耗时、吞吐或计数。
早期安装记录保留各自版本，不将它们当成 `0.1.16 + 0.1.17` 组合的全新安装结果。

### 已通过项目

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

### 已关闭的现场问题

| 问题 | 失败证据与回归结果 |
| --- | --- |
| 网关会话错误被误报为坏 JSON | `0.1.12` 收到 HTTP 200 后报 `PROTOCOL_ERROR`；`0.1.13` 将已识别的 `invalid token` 报为 `AUTH_REQUIRED`，重新授权后真实连接通过（V-04）。 |
| NAS 可访问但 LAN 网关不通 | 初始网关 ping 0/4；NAS 自身 ping 网关 4/4，转发已开启，Docker FORWARD 的 DROP 计数为 4 包/336 字节。隔离测试复现并验证修复，正式服务下网关访问也已通过（V-08）。 |
| 删除按钮无响应 | 改用页面内确认框后，用户确认离线删除成功（V-12）。原生 confirm 被 iframe 限制的原因已在受控浏览器复现，但现场没有单独的浏览器控制台证据。 |
| 停服期间的 404 终止自动恢复 | `0.1.14` 的 90 秒测试仅 35/90 回复；21:42:28 的 WSS 404 被标为 `NOT_FOUND / retryable=false`，随后桥接停止、网络被清理，末尾 ping 0/4。`0.1.17` 修复分类后按 V-14 复测，用户确认通过。 |

历史 WSS 403 的具体拒绝源没有取得完整现场证据，不能因后续连接成功就倒推为
Origin 校验导致。旧同名设备对应的密钥变化历史也未逐条核实；本轮验证的是
保留身份的重连和按设备 ID 删除离线记录，不是名称去重。

### 自动化与实机的边界

- fnOS/Linux 隔离网络测试由用户执行 v2 程序，全部 PASS，覆盖转发/NAT、Docker DROP、
  规则限制、幂等、事务失败、更新与清理。它不改宿主机规则；正式转发结果另记为 V-08。
- 构建 `0.1.17` 前，`go test ./...`、`go test -race ./...`、`go vet ./...` 通过；
  新增 HTTP/WSS 网关恢复用例连续三轮 race 测试通过，特权网络使用测试替身。
- `0.1.17` Mac 包已展开核对版本/build、ad-hoc 签名、BOM/CPIO UID/GID `0/0`、
  文件内容、执行权限和符号链接；不代表 Developer ID 签名、公证或公开分发验收通过。
- 后台权限拒绝、并发删除、失败回滚和受限 iframe 等受控测试的覆盖，不能替代
  对应的实机安全及故障注入验收。

### 剩余验收

对照 [P0 验收条件](03-fncpn-product-requirements.md#17-p0-验收条件)，AC-05 的
无可用 IPv6（NAS 未返回 IPv6 候选或本机不支持 IPv6 网络）时直接中继场景已通过；
其他组合条件不能仅凭某个子项通过就整体勾选。

| 范围 | 当前缺口 |
| --- | --- |
| AC-01、AC-02 安装与依赖 | 历史全新安装及后续升级已覆盖部分路径；最新两端组合的完整首次安装、无控制台用户安装、安装目录及依赖核验仍需补充。 |
| AC-03、AC-04、AC-07、AC-08 | LOCAL 同局域网、公网 IPv6 DIRECT、多路径切换、LAN 网段重叠、多客户端并发尚未实机验证。 |
| AC-06 凭据 | 失效识别和原凭据重用已验证；Cookie 正常自动续期、不同 fnOS 版本下的续期行为及长时间到期场景尚未完整验证。 |
| AC-09 恢复 | 暂停意图、服务端应用停启、Mac 重启已通过；物理网络切换、短时断网、睡眠唤醒和快速用户切换仍待验证，服务端应用重启不能替代这些场景。 |
| AC-10 清理 | 主动断开的接口/路由清理通过；最新版卸载/purge、跨 UID 保留、SIGKILL/journal 恢复及宿主机残留资源逐项检查仍待验证。 |
| AC-11 配置同步 | 在线修改 overlay/LAN、地址重分配、修改后的下发及失败回滚尚未实机验证。 |
| AC-12 安全 | 管理员正常授权与访问可用；普通用户、伪造网关 Header、外部端口扫描、无有效私钥访问及跨用户特权请求等负向场景尚未完整验证。 |
| AC-13 设备删除 | 离线删除和在线页面保护通过；绕过页面的在线/未知/非管理员请求、并发状态变化、删除失败回滚、持久化及重新刷新/重启后的专项核验仍待实机补充。 |
| 其他稳定性 | 精确吞吐、长时间传输/运行、文件哈希、fnOS 整机重启、ARM fnOS 和其他 macOS 版本未由本轮覆盖。 |

下一步停在 LOCAL 测试前：已询问目标 Mac 能否接入 NAS 的 `192.168.71.0/24`，
尚未收到网络条件确认；不要将本轮经隧道访问 NAS LAN 地址记为 LOCAL 通过。

## 自动化基线

```bash
go test ./...
go test -race ./...
go vet ./...
node --test internal/server/web/index.test.cjs
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...
swiftc -typecheck -parse-as-library -framework AppKit \
  platform/macos/FnCPNApp.swift
# macOS + WindowServer: offscreen AppKit fixture, no real NAS or installed daemon.
FNCPN_LAYOUT_CHECK=1 go test ./packaging -count=1
./scripts/build-macos-pkg.sh
FNPACK=/absolute/path/to/fnpack ./scripts/build-fpk.sh
```

管理页浏览器回归需要本机 Chrome 和单独安装的 Playwright，不访问真实 NAS：

```bash
PLAYWRIGHT_MODULE=/absolute/path/to/node_modules/playwright \
  node --test internal/server/web/iframe.test.cjs
```

PKG 必须满足：

```bash
pkgutil --payload-files dist/FnCPN-*-unsigned.pkg | grep -E '(^|/)\._'
# 预期无输出，退出码为 1。
```

展开 `Payload` 后，系统级文件的归档 owner 必须为 `root:wheel`，helper、卸载脚本和
可执行文件不得被普通用户写入。
`pkgbuild` 收集文件时使用 `--ownership preserve`，规避推荐属主处理中的临时 BOM 写入异常；
随后显式将 BOM、CPIO Payload 的 UID/GID 统一为 `0/0` 并分别校验，不修改构建源文件属主。
`TestMacOSPackageArchiveOwnership` 使用真实打包工具验证归档，并检查错误属主会阻止发布。

## macOS 实机

1. 分别验证有控制台用户和无控制台用户安装；已启动的 daemon 必须通过 socket
   healthcheck，启动失败时安装返回非零。自动连接的凭据/网络请求阻塞时，
   客户端 healthcheck 仍应及时响应，不因连接失败判定安装失败。
2. 授权应直接进入 HTTPS 中继登录页，不再显示访问方式选择；仅预置 `mode=relay`
   时不得提交授权。验证登录后自动进入 FnCPN 并注册、取消、超时、错误账号；
   失败不得覆盖最后有效 Cookie，关闭窗口后不得继续自动注册。
3. 在 Wi-Fi 切换、睡眠唤醒和快速用户切换后检查状态、utun 与路由；非控制台用户
   不得 Apply。
4. 分别验证 LOCAL、多个 IPv6 候选的 DIRECT、WSS RELAY 及三者之间的回滚切换。
5. SIGKILL privileged-daemon 后重启，确认 journal 只清理其中记录的接口和路由。
6. 验证 `/var/db/fncpn/credentials/<uid>/` 为 root 所有、目录 `0700`、文件 `0600`；
   凭据和日志不得相互混入。伪造请求 UID、跨用户读取、宽权限文件和符号链接均不能泄露凭据。
7. 卸载默认保留配置、日志和 root 凭据文件；显式 purge 仅清理指定 UID，
   不删除其他用户凭据。安装、启动、授权及卸载均不调用 Keychain。

## fnOS 实机

1. 验证全新安装、升级和卸载；失败时检查 `${TRIM_PKGVAR}/logs` 与 cleanup journal。
2. 预占 `54789/UDP` 或制造 `10.253.203.0/24` LAN 冲突，确认服务以 degraded 状态
   提供管理页，修改网络后可恢复。
3. 对 fresh apply/update 注入 WireGuard、nftables 和持久化失败，确认回滚错误可见且
   journal 可重试。
4. 停止/卸载后确认 FnCPN 接口、专属 nftables table 和带自身所有权标记的
   `DOCKER-USER` 规则消失；其他规则不变，`ip_forward` 保持 enabled。
5. 从 NAS IPv4/IPv6 扫描确认仅私网地址上的 LOCAL probe TCP listener 对外可达，
   管理 API 和 WSS 只通过统一网关/Unix Socket 提供。
6. 以实际 `fncpn` 用户执行 privileged IPC 健康检查，确认 socket 及父目录权限正确。
7. Docker 使用 iptables-nft 且 `FORWARD` 默认 `DROP` 时，验证客户端可访问已启用的
   LAN 设备，不只验证 NAS 自身地址；不得通过关闭 Docker 或改变全局策略使测试通过。

### 清理安全约束

- 清理操作只在目标 fnOS 执行。先确认 SSH 或管理入口不依赖 FnCPN 隧道，
  避免停服后失去控制。继续验收或保留数据升级不要求清空重装。
- 先通过当前安装版本的生命周期脚本停止服务并清理网络，再核验资源、通过应用中心
  卸载，最后才处理明确要求删除的残留数据；不能先删除二进制或 cleanup journal。
- 停止或清理失败时立即中止，保留二进制、journal、日志和完整错误，不跳过失败
  继续卸载或强删目录。安装失败也不得用临时 chmod/chown 掩盖源码问题。
- 停止后核验 FnCPN 进程、接口、路由、socket、专属 nftables 表及自身所有权标记的
  `DOCKER-USER` 规则均已清理。只处理 FnCPN 所有的资源，不动其他 WireGuard、
  Docker 或应用资源；禁止 `nft flush ruleset`、清空 Docker 链或改变全局转发策略。
  `ip_forward` 是共享设置，不在卸载时强行关闭。
- 数据路径按实际安装环境及 `/var/apps/fncpn/` 下的 `target`、`etc`、`var`、
  `tmp`、`home` 链接核实，不假定卷名或照搬历史绝对路径。
- 卸载必须通过 fnOS 应用中心，确认应用条目移除后才清除残留；
  不以直接删除 `/var/apps/fncpn` 代替卸载，不手工编辑应用注册或系统用户数据库。
- 全量清除需事先明确确认范围：服务端身份密钥、设备记录、配置、日志、运行状态和
  应用私有备份会不可逆删除，重装后旧客户端需要按新服务端身份重新配置。
  只删除已核实的 FnCPN 私有路径，不涉及开发机源码或其他应用数据。
- 系统快照、外置备份和用户另存的备份需单独确认；不得全盘模糊匹配后批量删除。
  全新安装验收不恢复这些旧测试数据，也不降级安装历史验证包。

### Linux 隔离转发测试

macOS 只交叉编译，不在开发机启动虚拟机；由用户在 fnOS/Linux 上执行：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c \
  -o dist/fncpn-netfilter-test-linux-amd64 ./internal/platform/linux
# 将测试程序传到 Linux 后执行，需要 root、iptables-nft、nsenter 和 ping。
sudo env FNCPN_NETFILTER_TEST=1 ./fncpn-netfilter-test-linux-amd64 \
  -test.run='^Test(UnconditionalDockerReturn|DockerForwardingIntegration)$' \
  -test.v -test.timeout=120s
```

测试子进程通过 `CLONE_NEWNET` 在 Go runtime 启动前建立隔离，不在宿主机命名空间
操作规则；隔离失败直接退出。匿名命名空间随进程和文件描述符关闭释放，不创建
宿主机命名空间挂载或持久化网络配置。测试接口仅使用 `NETLINK_ROUTE`，不要求
NAS 内核支持无关的 XFRM/IPsec。

覆盖无 Docker 时的转发/NAT、早期 base-chain ACCEPT 被 Docker 默认 DROP 覆盖、
修复后的网段限制与 iptables-nft 兼容性、幂等更新、失败事务不部分提交、网段变更、
管理员 DROP 优先级、停用 LAN，以及专属表丢失后的清理。2026-10-02 用户在 fnOS
执行 v2 测试程序，全部 PASS；这不替代正式安装后的真实客户端到 LAN 设备验收。

## 网关鉴权

通过真实 fnOS 统一网关验证：

- 未登录访问 API/WSS 被拒绝；网关返回 401 或 HTTP 200 纯文本 `invalid token`
  时，客户端均进入 `AUTH_REQUIRED`，保留真实 HTTP 状态与原因。
- 普通用户可读取自身配置和连接 relay，但注册和网络更新返回 403。
- 管理员可注册设备和更新网络。
- 外部伪造 `X-Trim-Userid`、`X-Trim-Isadmin` 会被网关剥离并替换，不能提权。
- 非 Unix Socket 启动 server daemon 会失败。
- 已登录的 WSS 请求不依赖 Origin/Host 匹配；网关改写 Host、缺失 Origin 均不误拒绝。

## 错误诊断

- 按 [全链路诊断](05-error-diagnostics.md) 验证 HTTP/IPC、授权、发现、选路、
  中继重连、特权状态和生命周期错误；主操作与回滚原因必须同时可见。
- 401 要求登录；403 展示权限拒绝与实际返回原因，不再误导用户反复登录。
- 失败日志及诊断保留阶段、HTTP 状态、外部业务码、请求 ID 和安全详情。
- 成功授权到建联的关键节点有 INFO，能区分注册新设备/复用设备、WSS 建立和
  WireGuard 握手；自动重连有触发原因，进程有启动/就绪/退出记录。
- 正常状态轮询、未变化的后台同步和 Cookie 刷新不刷 INFO；新增日志不包含报文、
  Cookie 或密钥值。
- `Cookie`、私钥、URL 凭据/查询、HTML 和未知 JSON 错误正文不得泄漏。
- 安装失败输出最后一次健康检查原因及日志路径，不能只输出 healthcheck failed。

## 离线设备清理

- 设备管理：连接状态按 25 秒 keepalive 对应的 RX 增量采样，超过 90 秒无接收活动
  才视为离线。启动观察期、计数重置、监测中断或状态读取失败时不可删除。
- 删除需确认名称和地址，后台按设备 ID 再次检查活性和管理员权限；即使页面曾显示
  离线，只要删除前发生新收包也必须拒绝。其他同名设备及地址保持不变。
- 删除成功后持久化记录与 WireGuard peer 均移除，监听得到更新；网络下发或文件
  重命名前失败须保留旧记录并回滚，重命名后的 sync 失败遵循既有提交点语义。
- `internal/server/devices_test.go` 覆盖活性超时/恢复、TX 和握手不能替代 RX、未知保护、
  实时检查、权限、同名设备、并发删除、回滚、持久化和状态日志。
- 管理页测试覆盖取消确认、禁止重复提交、错误透传、旧响应不能恢复已删行、
  未提交的网络表单不被覆盖，以及桌面/窄屏布局。
- 必须在跨源 iframe 且不允许 `allow-modals` 的环境验证页面内确认框；删除确认
  不依赖 `allow-forms`。不能只验证顶层页面或用测试桩代替浏览器原生 `confirm()`。
- 取消、Esc 关闭不得发送删除请求；确认期间设备上线、消失或状态不可用时，
  页面应取消删除并明确提示，后台的实时检查仍保留。Overlay 修改复用页面内确认。

## 数据升级

- 从当前旧版 `settings.json` v2、`devices.json` v1、transaction v2 迁移到单个业务
  `state.json`；存在 transaction 时优先恢复它。已有 `state.json` 后不再重放旧文件。
- 服务端私钥、设备 ID、客户端 root 凭据文件和用户连接意图保持不变。
- 保留密钥重新授权或重启进程，设备 ID 与地址不变；同名不同公钥保持独立身份。
- 从 Keychain 测试版本切换时不迁移旧凭据；安装后应进入 `AUTH_REQUIRED`，
  重新授权生成文件凭据并注册新设备，旧 Keychain 条目和服务端旧设备不自动删除。
- 未知更高 schema version 明确拒绝，不覆盖原文件。
- 业务状态原子重命名前失败不提交，网络回滚到旧期望状态；重命名后目录 sync 失败，
  进程内状态与正式文件均保持新提交状态。root cleanup journal 独立保留。
- macOS 路由或地址已提前消失时，重复清理仍成功；无法读取系统状态或真实权限错误
  不能被当成资源不存在。
- Logout/Forget/重新授权后，旧 watch 响应不能恢复凭证或覆盖新配置；
  控制台用户切回后只恢复原本开启 AutoConnect 的连接。
