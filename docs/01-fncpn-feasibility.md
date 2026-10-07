# 基于 FN Connect 的异地私有网络技术调研与可行性分析

调研日期：2026-09-27

## 结论摘要

整体方案**技术上可行，但当前不能直接进入完整产品开发**。

可行部分：

- WireGuard 可作为内层私有网络协议；公网 IPv6 可用时直接使用原生 UDP。
- fnOS 第三方应用的统一网关明确支持 WebSocket，可把 `/app/{appname}/ws` 转发到应用 Unix Socket，并在转发前校验 fnOS 登录态。[cite:3]
- WireGuard UDP 报文可以封装进 WebSocket；成熟项目 `wstunnel` 已验证这种模式。[cite:9]
- fnOS 支持 Native 应用、常驻服务、生命周期脚本、x86/ARM 包和 Root 运行模式，具备承载服务端的基础条件。[cite:4][cite:5][cite:6]
- macOS 正式产品可以使用 Network Extension 的 Packet Tunnel Provider，并复用 WireGuardKit。[cite:11][cite:12]

当前主要阻塞：

1. **FN Connect 中继下，CLI 如何获得并持续刷新 fnOS 网关登录凭证尚无公开稳定方案。**
2. **FN Connect 是否对第三方统一网关路径完整透传 WebSocket Upgrade，需安装最小回声应用实测。**
3. **fnOS 应用能否稳定创建 TUN/WireGuard 接口、配置转发和 nftables/iptables，需真机权限 PoC。**
4. **FN Connect 是有带宽限制的中继服务，不适合作为高吞吐主链路。**[cite:1]

因此建议先做两个窄 PoC：

- PoC A：fnOS 统一网关 WebSocket 经 FN Connect 中继双向传输二进制帧。
- PoC B：fnOS 应用以最小必要特权创建 WireGuard 接口，并访问 NAS 所在局域网。

只有两个 PoC 都通过，才开始完整 core 和 macOS 产品层。

## 已验证的 FN Connect 机制

### 地址发现

访问 `https://<fn-id>.fnos.net/` 时，服务会先跳转到：

```text
https://fnos.net/<fn-id>/
```

前端随后调用：

```http
POST https://fnos.net/api/v1/fn/con
Content-Type: application/json
fn-sign: <sha256>
authx: nonce=<nonce>&timestamp=<timestamp>&sign=<md5>

{"fnId":"<fn-id>"}
```

本次实测返回了局域网 IPv4、公网 IPv4、公网 IPv6、FN 中继域名、fnOS HTTP/HTTPS 端口、版本和探测校验值。该接口不依赖用户 Cookie，但包含两层由公开前端常量计算的签名：

```text
fn-sign = SHA256("trim_connect`" + fnId + "`" + timestamp + "`anna")

authx.sign = MD5(
  "NDzZTVxnRKP8Z0jXg1VAMonaG8akvh" + "_" +
  requestPath + "_" +
  nonce + "_" +
  timestamp + "_" +
  MD5(jsonBody) + "_" +
  "zIGtkc3dqZnJpd29qZXJqa2w7c"
)
```

`timestamp` 为 Unix 毫秒时间戳；`nonce` 必须使用网页端的六位十进制格式，范围为 `100000` 至 `999999`。公开脚本使用 `(Math.floor(Math.random()*9e5)+1e5).toString().padStart(6,"0")`。[cite:2]

2026-10-01 使用随机不存在的 FN ID 对公共接口进行对照：32 位十六进制 nonce 返回 `5000: invalid sign`，六位十进制 nonce 返回 `3000037: Not Found Error`。后者说明请求通过了签名校验，但不代表真实 NAS 连接已通过。

这属于客户端校验或反滥用机制，**不是用户身份认证**。算法与常量都在公开前端资源中，不能作为本项目的安全边界。[cite:2]

该接口是未公开的内部接口。实现时应隔离在 `fnconnect/discovery` 适配器内，并允许协议变化后快速替换。

### 访问路径选择

当前网页端逻辑大致如下：[cite:2]

1. 查询 FN ID 对应的地址集合。
2. 优先探测 `ipv4`、`ipv6` 中的局域网地址。
3. 新版本使用 WebRTC STUN 探测公网地址和校验端口；旧版本使用隐藏 iframe 加 `/static/bridge.html` 探测。
4. 局域网地址可达时直接跳转至本地 HTTP 地址。
5. 否则并行检查公网地址、DDNS 和 FN 中继。
6. 公网地址不可达时自动进入中继。

官方说明也确认 FN Connect 会根据当前网络选择公网直连或中继；移动 App 在 IPv6 可达时使用 P2P，失败后回退中继。[cite:1]

项目不应照搬网页的 STUN 校验作为 WireGuard 可达性判断。网页探测的是 fnOS Web 服务端口，不能证明自定义 WireGuard UDP 端口可达。应由服务端实现独立的 UDP challenge。

### 中继模式

本次实测确认：

- 从 `fnos.net/<fn-id>/` 回到 `<fn-id>.fnos.net` 时，响应设置 `mode=relay; HttpOnly` Cookie。
- 带 `mode=relay` 请求同一域名时，返回 NAS 上的 fnOS 页面，而不是再次跳转。
- 未携带有效 fnOS 登录凭证访问 `/app/...` 时，NAS 返回 `invalid token`。
- `<fn-id>.fnos.net` 使用该 FN ID 独立域名证书，并包含通配 SAN。

这说明 FN Connect 中继是面向 fnOS HTTP 入口的反向代理路径，不是一个可直接连接任意 NAS TCP/UDP 端口的通用隧道。

官方隐私政策说明中继流量会经飞牛服务器转发，并称链路加密且不存储、解析中继内容。[cite:10] 官方帮助同时明确中继会限速。[cite:1]

### WebSocket 可行性

fnOS 统一网关公开文档明确支持 WebSocket：

```text
wss://<fnos-host>/app/<appname>/ws
        │
        └── /var/apps/<appname>/target/app.sock
```

网关在 Upgrade 前校验 fnOS 登录态，并向应用转发：

```text
X-Trim-Userid
X-Trim-Isadmin
X-Trim-Username
```

[cite:3]

因此第三方应用侧具备 WebSocket 接入点。FN Connect 已能转发同域下的普通统一网关请求，但本次未安装测试应用，无法最终证明中继代理保留所有 WebSocket Upgrade、长连接、帧大小和空闲超时行为。

结论标记：

- 统一网关支持 WebSocket：**已确认**。
- FN Connect 中继可到达统一网关路径：**已确认**。
- FN Connect 中继可稳定承载长时间二进制 WebSocket：**待 PoC**。

## 推荐架构

### 组件划分

```text
cmd/fncpn/          单一 Go 命令入口

internal/
  model/            跨边界数据契约
  client/           发现、授权、选路、路由计划、WSS 和连接状态机
  server/           设置、设备、地址池、HTTP API 和中继
  ipc/              本地进程通讯协议
  privileged/       特权操作白名单和计划调度
  platform/
    darwin/         utun、路由、Keychain 和系统事件
    linux/          netlink、内核 WireGuard、转发和 nftables
  command/          子命令解析与进程组装

ui/
  macos/            AppKit 薄 UI
  fnos/             fnOS 薄 UI
```

macOS 和 fnOS 每个平台只发布一个 `fncpn` Go 二进制，并通过不同子命令启动普通 daemon 与 root privileged-daemon。单一二进制不等于单一进程，HTTP/WSS、Cookie 和状态机不得直接运行在 root 进程。macOS 数据面将 `wireguard-go` 作为 Go 包链接进 privileged-daemon，不依赖外部 `wg` 命令。

### 数据路径

#### 同一局域网

- 使用 FN 地址发现结果中的局域网地址做应用级探测。
- 若目标服务可直达，则不创建私有网络接口、不安装路由。
- 不以 SSID 相同作为判断条件；访客网络、AP 隔离和多 VLAN 会产生误判。

#### 公网 IPv6

- 从发现接口取得 NAS 公网 IPv6。
- 对自定义 WireGuard UDP 端口执行 challenge，而不是只做 ICMP 或 TCP 探测。
- 可达时将 WireGuard peer endpoint 设置为 `[ipv6]:port`。
- 这是性能最优的主路径。

#### FN Connect 中继

- WireGuard endpoint 指向客户端本地 UDP socket。
- 客户端桥接器把每个 UDP datagram 封装成一个二进制 WebSocket message。
- 服务端每个 WebSocket 会话创建一个独立的 connected UDP socket，目标为 `127.0.0.1:<wireguard-port>`。
- WireGuard 服务端将不同 UDP 源端口视为不同 peer endpoint；重连后由 WireGuard 的 authenticated roaming 更新 endpoint。

每个 WebSocket 会话使用独立 UDP socket，可避免解析 WireGuard receiver index 来分流回包。

WebSocket 外层是 TCP。丢包时会出现队头阻塞，因此它只能是兼容性兜底，不能和原生 UDP 宣称同等性能。`wstunnel` 官方也把 UDP/WireGuard over WebSocket 作为支持场景，并建议降低 MTU、避免隧道路由回环。[cite:9]

### WireGuard 服务端

优先顺序：

1. fnOS 内核 WireGuard。
2. `wireguard-go` + TUN。
3. 不建议首版实现纯用户态 L3/L4 netstack。

Linux 上 WireGuard 官方建议优先使用内核实现；`wireguard-go` 可作为缺少内核支持时的替代。[cite:8]

服务端需要：

- 创建 WireGuard 接口和独立 overlay 地址。
- 开启 IPv4/IPv6 forwarding。
- 对进入家庭 LAN 的流量做 SNAT/MASQUERADE，避免要求家庭其他设备添加回程路由。
- 限制 peer 的 AllowedIPs，避免客户端伪造其他私有网络地址。
- 仅把 WS 桥接目标固定为本机 WireGuard UDP 端口，禁止成为通用代理。

这些操作通常需要 Root 或 `CAP_NET_ADMIN`。fnOS 文档允许 Root 模式，但明确建议长期服务降权运行。[cite:5] 推荐由生命周期脚本完成接口和防火墙配置，再以包用户运行 WebSocket 服务；如果平台无法细粒度授予能力，再评估最小 Root 常驻进程。

## 路由与网段冲突

规则应收敛为：

| 条件 | 安装到私有网络的路由 |
|---|---|
| 本地可直接访问 NAS | 不启用私有网络 |
| 本地网段与 NAS 网段不重叠 | NAS 整个 LAN CIDR |
| 网段重叠，但目标 IP 在本地未被占用 | 仅目标 IP `/32` 或 `/128` |
| 目标 IP 本身冲突 | 不能透明区分，必须做地址映射或仅提供服务代理 |

示例：

```text
客户端 LAN: 192.168.1.0/24
NAS LAN:    192.168.1.0/24
目标 NAS:   192.168.1.10
```

如果客户端本地没有 `192.168.1.10`，可安装 `192.168.1.10/32` 指向私有网络。若本地已经存在同 IP 设备，则目标地址本身没有足够信息区分两端，不能靠路由优先级同时访问二者。

需要访问冲突网段内多台设备时，应提供可选的地址映射，例如：

```text
100.96.1.0/24 -> 远端 192.168.1.0/24
```

这比为整个冲突网段强行安装路由更可控。

## 认证设计

### 不应采用的方案

- 不把 `/api/v1/fn/con` 的公开签名当成用户认证。
- 不读取用户日常 Chrome/Safari 的 Cookie 数据库。
- 不要求用户复制 Cookie。
- 不导出或长期保存浏览器 Cookie 快照；仅在 root 私有凭据区保存用途隔离的
  CLI 原生会话与管理页 Web token。
- 不只依赖 WireGuard 公钥而完全放开 WS 网关，避免中继资源被滥用。

### 推荐的配对模型

理想流程：

1. 客户端生成 WireGuard key pair 和一次性 `state`。
2. 客户端打开浏览器到 `/app/fncpn/pair?state=...`。
3. fnOS 网关完成 NAS 登录校验，并向应用提供用户 Header。
4. 管理员确认设备、公钥和允许访问的网段。
5. 服务端签发一次性 authorization code。
6. 浏览器回调客户端 loopback URL。
7. 客户端交换得到可撤销、可轮换的应用 token。

问题在于：后续 CLI 连接 `/app/fncpn/ws` 时，fnOS 统一网关仍会先要求系统登录态。公开文档没有说明如何对指定 WebSocket 路径关闭系统认证，也没有公开的第三方登录 OAuth。[cite:3][cite:7]

后续真机验证确认 FN Connect 可以转发 fnOS 原生认证 WebSocket。正式客户端因此使用
`user.login` 获取 `token + longToken + secret`，短 token 失效后通过
`user.tokenLogin` 恢复；影视 OAuth、WebView Cookie 导出和浏览器 CDP 均不作为主路径。
该协议属于 fnOS 原生但未公开承诺的接口，版本兼容性仍需回归测试。

管理页不复用上述 CLI token。客户端在同一次密码输入期间额外执行一次加密 Web 登录，
使用独立 DID 获取短 Web token；本地 loopback AdminProxy 仅将该 token 作为
`fnos-token` 注入 `/app/fncpn` 请求。实机已确认该路径可直接读取管理页及 admin
snapshot，不需要 ticket、ost、entry-token 或向 WKWebView 暴露 HMAC secret。

## fnOS 应用实现约束

fnOS 应用包使用 `.fpk`，可由 `fnpack` 创建和构建；Native 服务通过 `cmd/main` 管理启动、停止和状态。[cite:4][cite:6]

建议包结构：

```text
fncpn/
├── app/
│   ├── server/
│   │   └── fncpn-server
│   └── ui/
│       ├── config
│       └── assets/
├── cmd/
│   ├── install_init
│   ├── install_callback
│   ├── main
│   ├── uninstall_init
│   └── uninstall_callback
├── config/
│   ├── privilege
│   └── resource
├── manifest
├── ICON.PNG
└── ICON_256.PNG
```

关键配置：

- `manifest.platform`：Go 二进制需要分别构建 x86 和 ARM 包，不能声明 `all`。[cite:6]
- `ctl_stop=true`：后台服务需要启停控制。
- 统一网关入口：`gatewayPrefix=/app/fncpn`、`gatewaySocket=app.sock`。[cite:3]
- `cmd/main`：管理进程、日志、WireGuard 接口和清理。
- `config/privilege`：PoC 阶段验证 Root；产品阶段拆分特权初始化与非特权服务。[cite:5]
- 日志写入 `TRIM_PKGVAR`，关键路径必须输出明确错误。

公开文档没有承诺：

- 内核包含 WireGuard 模块。
- 应用可访问 `/dev/net/tun`。
- 应用审核允许常驻 Root 私有网络服务。
- 应用可修改系统转发和防火墙规则。

这些都必须以真机测试和官方确认作为准入条件。

## macOS 客户端

### CLI

`wireguard-go` 可在 macOS 使用 `utun`，适合早期验证。[cite:8]

CLI 仍需要管理员权限处理：

- utun/TUN 生命周期。
- 路由表更新。
- 冲突路由。
- 网络切换后的恢复。

CLI 适合作为协议和选路 PoC，不应直接成为最终 GUI 的底层进程模型。

### 正式客户端

当前 P0 实现采用 AppKit 菜单栏应用、当前用户 client daemon 和 root
privileged-daemon。WireGuard Go 包运行在特权进程内，凭据保存于按 UID 隔离的 root
私有文件，GUI 只通过窄 IPC 交互。该方案已经在无 Developer ID、无 Network
Extension 的本地安装模式下通过实机验证。Packet Tunnel Provider / WireGuardKit
仍是未来正式签名分发时可评估的替代部署方式，不是当前实现依赖。

## 可靠性与安全要求

### 传输

- 一个 UDP datagram 对应一个二进制 WS message。
- 设置明确的最大报文长度，拒绝超限帧。
- WS 写队列满时丢弃旧数据报或低优先级数据，不允许无限阻塞。
- 实现 ping/pong、指数退避和带抖动重连。
- 切换到直连后保留短暂回退窗口，避免网络抖动频繁切换。
- 初始 MTU 建议从 1280 或 1300 开始，以实测结果调整。[cite:9]

### 安全

- WireGuard 密钥与 FN 登录凭证分离。
- 每台客户端独立 peer 和撤销状态。
- WS 入口增加速率限制、连接数限制和最大帧限制。
- 服务端桥接目标固定为 localhost WireGuard 端口。
- 配对仅管理员可操作，并绑定 WireGuard 公钥。
- 不记录私钥、完整 token 或用户流量。
- FN Connect 内部发现签名只用于兼容，不用于授权。

### 选路状态机

```text
DISABLED_LAN
    ↑
PROBING -> DIRECT_IPV6 -> RELAY_WS
              ↓             ↓
           DEGRADED <-------
```

优先级固定为：

```text
LAN disabled > direct IPv6 UDP > FN Relay WSS
```

只有连续探测失败达到阈值后才降级；升级回直连也需要连续成功，避免路径抖动。

## PoC 计划与验收标准

### PoC A：FN Connect WebSocket

实现一个最小 fnOS 应用：

- 监听 Unix Socket。
- 提供 `/app/fncpn/ws`。
- 收发随机二进制帧并计算 hash。
- 不包含 WireGuard。

验收：

- 局域网访问成功。
- 强制 `mode=relay` 后 Upgrade 返回 `101`。
- 连续运行 2 小时不被中继主动断开。
- 验证单帧限制、吞吐、RTT、空闲超时、断网重连。
- 验证 CLI 如何获得网关认可的登录凭证。

若无法以受支持方式完成最后一项，应暂停基于 FN Connect 的 CLI 产品化。

### PoC B：fnOS WireGuard 权限

验收：

- x86 和 ARM 目标至少各一台设备验证。
- 能创建接口、配置 peer、启用 forwarding。
- 能从私有网络客户端访问 NAS。
- 能通过 SNAT 访问 NAS LAN 中另一台设备。
- 应用停止、卸载和异常退出后无残留路由、防火墙规则或进程。
- fnOS 重启后可幂等恢复。

### PoC C：路径切换

验收：

- 同 LAN 时不创建私有网络。
- 外网 IPv6 可达时走原生 UDP。
- 屏蔽 IPv6 或 UDP 后 10 秒内切换到 WSS。
- 恢复 IPv6 后无路由泄漏、无死循环。
- 网段重叠时只安装目标 `/32`。
- 目标 IP 冲突时明确报错或启用地址映射，不静默覆盖本地设备。

## 最终判断

| 方向 | 判断 |
|---|---|
| WireGuard + IPv6 直连 | 高可行 |
| WireGuard UDP over WSS | 可行，适合作为兜底 |
| fnOS Native 应用承载服务 | 基础能力可行，特权能力待验证 |
| FN Connect 转发第三方 WS | 高概率可行，必须实测 |
| 纯 CLI 浏览器授权 | 当前缺少公开稳定方案 |
| macOS 正式客户端 | 可行，应使用 Network Extension |
| 冲突网段自动处理 | `/32` 可行；精确 IP 冲突需映射 |

建议立项状态：**有条件可行，先 PoC，不进入完整产品开发。**

## Sources

[cite:1] 飞牛，《如何远程访问到飞牛 NAS？》  
https://help.fnnas.com/articles/v1/access/how-access

[cite:2] FN Connect 当前网页前端资源与 2026-09-27 实测  
https://static2.fnnas.com/connect/assets/1MPg8Gvv7C7Lrf46.js  
https://fnos.net/api/v1/fn/con

[cite:3] 飞牛应用开放平台，《统一网关》  
https://developer.fnnas.com/docs/core-concepts/gateway-registration/

[cite:4] 飞牛应用开放平台，《应用框架》  
https://developer.fnnas.com/docs/core-concepts/framework/

[cite:5] 飞牛应用开放平台，《应用权限》  
https://developer.fnnas.com/docs/core-concepts/privilege/

[cite:6] 飞牛应用开放平台，《Manifest》与《fnpack》  
https://developer.fnnas.com/docs/core-concepts/manifest/  
https://developer.fnnas.com/docs/cli/fnpack/

[cite:7] 飞牛应用开放平台，《开放 API 调用方式》  
https://developer.fnnas.com/api/calling/

[cite:8] WireGuard，Protocol 与 wireguard-go  
https://www.wireguard.com/protocol/  
https://github.com/WireGuard/wireguard-go

[cite:9] erebe/wstunnel，WireGuard and wstunnel  
https://github.com/erebe/wstunnel

[cite:10] 飞牛 App 隐私政策  
https://www.fnnas.com/privacy

[cite:11] Apple, Configuring network extensions  
https://developer.apple.com/documentation/xcode/configuring-network-extensions

[cite:12] WireGuard, wireguard-apple / WireGuardKit  
https://github.com/WireGuard/wireguard-apple

[cite:13] Apple, TN3134: Network Extension provider deployment  
https://developer.apple.com/documentation/technotes/tn3134-network-extension-provider-deployment
