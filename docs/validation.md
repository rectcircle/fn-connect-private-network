# 验证记录

记录影响发布判断和技术选型的实测结论。日常测试与发布步骤见 [开发与构建](development.md)，
产品要求见 [产品需求](product.md)，升级保证见 [版本契约](versioning-and-compatibility.md)。
自动化通过、用户确认和带日志的实测分别记录；旧版证据不代表新版的所有专项通过。

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
