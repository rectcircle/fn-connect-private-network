# FnCPN P0 发布验收

## RC 安装问题（2026-10-09）

公开 `1.0.0-rc.1` 在目标 Mac 安装失败。`/var/log/install.log` 记录安装器将
`Applications/FnCPN.app` 重定位到工作区中的安装包展开副本，随后 postinstall 的
`chown /Applications/FnCPN.app` 因路径不存在失败。不是签名或网络错误。

`1.0.0-rc.2` 显式设置 `BundleIsRelocatable=false`，并校验生成包中不存在
relocate bundle、App 路径正确；真实 macOS 打包工具测试覆盖成功构建和拒绝重定位配置。
修复不清空身份或配置。rc.2 的实机安装仍待用户确认；历史 0.x 安装成功不代表 RC 已通过。

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
| V-18 | CLI/Web 会话隔离与管理后台 | `0.1.27` 实机 probe 证明 CLI 新短 token、`user.authToken`、`user.tokenLogin` 恢复及 Web 登录后 CLI token 继续有效；独立 Web token 可访问 FnCPN 网关。现场包中用户确认管理后台免登录进入 `/app/fncpn`，且上游仅携带 `mode + Web fnos-token` 时 `/app/fncpn/api/v1/admin/snapshot` 返回 200。提交前收敛后的安装包仍需一次 smoke test。 |
| V-19 | 恢复后 Cookie 切换 | 现场日志证明 `user.tokenLogin` 已成功但原 RemoteClient 继续使用旧内存 Cookie，配置重试仍返回 `invalid token`。修复后自动化测试要求第二个 RemoteClient 明确携带恢复后的短 token；实机重启回归仍待执行。 |

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

对照 [P0 验收条件](product.md#p0-验收条件)，AC-05 的
无可用 IPv6（NAS 未返回 IPv6 候选或本机不支持 IPv6 网络）时直接中继场景已通过；
其他组合条件不能仅凭某个子项通过就整体勾选。

| 范围 | 当前缺口 |
| --- | --- |
| AC-01、AC-02 安装与依赖 | 常规安装与覆盖升级已验证：历史首次安装记录及用户多次小版本升级正常的确认；不重复要求常规升级矩阵。最终包全新安装、无控制台用户安装专项未执行。 |
| AC-03、AC-04、AC-07、AC-08 | 网络与配置已由用户确认实机验证完成（2026-10-09）；未提供逐项日志，不补写测量结果。 |
| AC-06 凭据 | CLI 短/长 token 恢复及 CLI/Web 隔离已验证；独立 Web token 长时间到期、密码修改及服务端撤销后的重新登录场景尚未完整验证。 |
| AC-09 恢复 | 暂停意图、服务端应用停启、Mac 重启已有记录；网络恢复按用户网络与配置验收确认完成，快速用户切换专项未执行。 |
| AC-10 清理 | 主动断开的接口/路由清理通过；最新版卸载/purge、跨 UID 保留、SIGKILL/journal 恢复及宿主机残留资源逐项检查仍待验证。 |
| AC-11 配置同步 | 网络与配置已由用户确认实机验证完成（2026-10-09）；无单独故障注入记录。 |
| AC-12 安全 | 管理员正常授权与访问可用；权限负向场景暂缓，不作为本次发布阻塞项，不标为通过。 |
| AC-13 设备删除 | 离线删除和在线页面保护通过；绕过页面的在线/未知/非管理员请求、并发状态变化、删除失败回滚、持久化及重新刷新/重启后的专项核验仍待实机补充。 |
| 其他稳定性 | 精确吞吐、长时间传输/运行、文件哈希、fnOS 整机重启、ARM fnOS 和其他 macOS 版本未由本轮覆盖。 |

历史记录中的经隧道访问 NAS LAN 地址不能记为 LOCAL 通过；最新版必须单独验收。

## 自动化基线

```bash
go test ./...
go test -race ./...
go vet ./...
node --test internal/server/web/index.test.cjs
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...
swiftc -typecheck -parse-as-library -framework AppKit -framework ServiceManagement \
  -framework UserNotifications -framework WebKit \
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
2. 授权使用原生 FN ID、用户名、密码表单，经 WSS 登录；验证自动注册与连接、取消、超时、错误账号和不支持的 2FA。失败不得覆盖有效会话，取消后不得继续自动注册。
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
- 普通用户可注册设备、读取配置和连接 relay；管理接口与网络更新返回 403。
- 管理员可注册设备和更新网络。
- 外部伪造 `X-Trim-Userid`、`X-Trim-Isadmin` 会被网关剥离并替换，不能提权。
- 非 Unix Socket 启动 server daemon 会失败。
- 已登录的 WSS 请求不依赖 Origin/Host 匹配；网关改写 Host、缺失 Origin 均不误拒绝。

## 错误诊断

- 按 [全链路诊断](diagnostics.md) 验证 HTTP/IPC、授权、发现、选路、
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

- 1.0.0 建立正式数据基线，不迁移 0.x 开发期拆分文件、旧凭据或网络状态格式。
- 当前格式的服务端密钥、设备身份、客户端凭据和连接意图不得因更新自动清空。
- 缺少正式 state.json 而存在旧业务文件时明确拒绝初始化；先停止服务、清理网络并备份，由用户主动处理旧数据。
- 从 1.0.0 起，同 MAJOR 正式版直接升级保留身份与连接意图；schema 迁移先备份，失败可恢复，降级恢复对应程序与备份。
- 未知更高 schema version 明确拒绝，不覆盖原文件。
- 业务状态原子重命名前失败不提交，网络回滚到旧期望状态；重命名后目录 sync 失败，
  进程内状态与正式文件均保持新提交状态。root cleanup journal 独立保留。
- macOS 路由或地址已提前消失时，重复清理仍成功；无法读取系统状态或真实权限错误
  不能被当成资源不存在。
- Logout/Forget/重新授权后，旧 watch 响应不能恢复凭证或覆盖新配置；
  控制台用户切回后只恢复原本开启 AutoConnect 的连接。

## 0.1.55 补充实机确认（2026-10-09）

- 用户安装 0.1.55 后确认局域网权限流程实机验证无问题。
- 用户按临时关闭 macOS Wi-Fi IPv6 的步骤完成中继回退测试，并确认可以提交。
- 以上为用户确认，未附完整日志、授权操作时间线或性能数据；不据此将其他网络切换、
  全新安装矩阵、多客户端、卸载及全部安全负向场景标为通过。

## 发布范围确认与代码核对（2026-10-09）

- 常规安装与覆盖升级：已验证。依据历史首次安装记录及用户确认已连续升级多个小版本、未发现问题；不推导未经逐项记录的专项场景通过。
- 网络与配置：用户确认已完成实机验证。此前表格中的相关待测状态由本次确认更新，不补写未提供的日志和性能数字。
- 安装、停服、卸载与 journal 恢复机制已完成代码核对；packaging、Darwin/Linux 网络引擎及 privileged 服务测试通过。macOS 卸载补充退出 App，并明确默认保留用户日志、删除特权日志。
- 卸载/purge、SIGKILL、无控制台用户安装及最终候选包全新安装专项未执行；代码核对及测试替身不记为这些专项实机通过。
- 权限负向场景暂缓；平台扩展验证暂缓，等待 issue 反馈。两者不作为本次发布阻塞项，也不标为已验证。
- macOS 分发采用 ad-hoc 签名，PKG 未做 Developer ID Installer 签名及公证；RC 打包已包含依赖许可证声明，并使用自行绘制的存储圆柱替换飞牛标志。

## 1.0.0-rc.1 构建与发布核验（2026-10-09）

- 发布源码 commit：`1d85e6795d2cc8fe188ae7d605bb33b8eda622c9`，tag：`v1.0.0-rc.1`。
- GitHub 候选流水线 `37816795546` 通过；Go test/race/vet、管理页测试、版本/本地化同步、发布清单及发布脚本测试、两种 Linux 架构编译与 Swift macOS 13 类型检查通过。
- 使用 Go 1.27.1、Xcode 16.4 / SDK 15.5、fnpack 1.2.3 构建；两端产物来自同一 commit，工具信息与 SHA256 随发布清单归档。
- 下载远端草稿展开核验：App/helper Mach-O 最低系统均为 13.0，完整产品版本为 `1.0.0-rc.1`，PKG 和 CFBundleShortVersionString 为 `1.0.0`；ad-hoc 签名、BOM root 属主、图标与许可证内容核验通过。
- fnOS 两包的版本、平台、CPU 架构与许可证文件核验通过。远端资产经逐字节复核后公开为 prerelease，未重新构建公开产物。
- 此项为构建与产物检查，没有在目标 Mac/fnOS 上安装本 RC，不替代前述未执行的实机专项。首次正式协议/数据基线仍待 `1.0.0` 正式版归档。
