# 故障排查与使用限制

本文面向安装、登录和连接失败时的自查。错误码、日志位置和诊断字段见 [诊断指南](diagnostics.md)，各平台已完成的实机测试见 [验证记录](validation.md)。

## 先检查这些条件

- NAS 已启用 FN Connect，FnCPN 应用已安装并在应用中心运行；先确认通过 FN Connect 能正常访问 fnOS。
- fnOS 安装包要求系统版本不低于 `1.1.3100`；选择与 NAS 架构一致的 x86_64 或 ARM64 FPK。
- Mac 需要 macOS 13 或更新版本，安装包区分 Apple Silicon 和 Intel。
- Windows 需要 Windows 10 22H2（build 19045）或更新的 ARM64/x64 系统，不支持 32 位或 Windows Server。缺少 WebView2 Runtime 时首次安装需要联网。Windows 10 和原生 x64 已由用户完成验收。
- 安装客户端、启动特权服务和修改系统网络需要管理员授权；安装 NAS 应用和修改服务端网络设置需要 fnOS 管理员。普通 fnOS 用户可以登录和连接，但不能管理服务端。
- NAS 需要内核 WireGuard、netlink、nftables 及相应特权服务正常工作。FnCPN 不要求预装 `wg`、`ip` 或 `nft` 命令；缺少命令本身不代表缺少运行能力。

## 登录失败：两步验证、访问码与会话

从 1.1.1 起，macOS、Windows 客户端和 Go CLI 的原生登录支持 **FN Connect 访问码**与 **fnOS 两步验证（OTP）**。输入 FN Connect ID 后会探测访问码要求；图形客户端在有需要时显示访问码字段。提交账号密码后，若需要两步验证，进入独立 OTP 步骤，只显示 OTP 和“信任此设备”，可用“上一步”返回修改账号密码。管理后台使用独立 Web 登录会话，并复用访问码网关会话。

- 提示 `ACCESS_CODE_REQUIRED` / "该 NAS 开启了访问码"：FN Connect 网关拦截了未验证的请求，输入访问码后重试。忘记访问码时，管理员可在 NAS SSH 中执行 `/usr/trim/bin/security_service --access-code` 查看。
- 提示 `ACCESS_CODE_INVALID`：重新填写访问码后重试，已填写的用户名和密码会保留。
- 提示 `ACCESS_CODE_RATE_LIMITED`（HTTP 429）：网关已临时封禁尝试，等待后重试，不要连续提交。
- 提示 `TWO_FACTOR_REQUIRED` / `TWO_FACTOR_CODE_INVALID`：输入当前有效的六位 TOTP 码；勾选“信任此设备”后，fnOS 可能在后续登录中跳过该设备的 OTP，具体由 NAS 的信任策略决定。
- 提示 `TWO_FACTOR_SETUP_REQUIRED`：该账号强制两步验证但尚未绑定密钥，需先在 fnOS 网页端完成绑定，客户端不会代为绑定。
- 提示密码错误：核对 FN ID、用户名和密码，确认使用的是目标 NAS 的 fnOS 账号。
- 提示登录过期、`AUTH_REQUIRED`、401 或 `invalid token`：在客户端重新登录。密码不会持久化保存；长期会话失效后需要再次输入。访问码验证结果以 `os-access-code` 会话 cookie 保存，重连时自动重放，无需重复输入。
- 提示 403 / `PERMISSION_DENIED`：检查当前操作是否需要管理员，以及网关或访问策略是否拒绝请求；403 不一定表示密码错误。
- 提示发现失败、429 或超时：检查 FN Connect、DNS 和网络出口，避免连续点击重试触发上游限流。

如果在 fnOS 关闭 OTP 后仍停留在原 OTP 页面，点击“上一步”重新提交账号密码，让客户端获取新的认证结果。关闭或取消勾选“信任此设备”不会撤销 fnOS 已保存的设备信任。

需要重新触发 OTP 验证时，可执行 `fncpn logout` 后重新登录：这会清除原生登录、访问码及管理后台会话，保留 NAS 配置与隧道设备身份，并在下次登录生成新的 fnOS 登录设备标识。

支持范围及后续能力见 [产品需求](product.md)。

## 服务端端口与防火墙

端口必须按实际使用路径放行，包括 NAS 防火墙、路由器 IPv6 防火墙、VLAN/访客网络隔离，以及客户端所在网络的出站策略。下表为默认值；修改过端口时以 fnOS 设置、FnCPN 管理页及发现结果为准。

| 端口 / 通信 | 用途 | 放行范围与要求 |
| --- | --- | --- |
| NAS `54789/UDP` | WireGuard 数据通道；公网 IPv6 DIRECT 直连 | 需要直连时，允许客户端到 NAS 该 UDP 端口及返回流量；尤其检查路由器和 NAS 的 IPv6 入站规则。管理页修改监听端口后，同步修改规则 |
| NAS `54790/TCP` | LOCAL 局域网身份探测及三种连接路径的 无需额外鉴权的 DNS 查询，仅监听 NAS 私网 IPv4 地址 | 允许物理 LAN 和 WireGuard 客户端访问该 NAS LAN IP；跨 VLAN 时检查 ACL。无需向公网开放或做公网端口映射 |
| NAS fnOS HTTPS 网关，默认 `5667/TCP` | 用户直接打开 NAS 的本地 HTTPS 管理入口 | 按 fnOS 实际配置放行；当前客户端登录和控制请求使用 FN Connect 网关，不依赖直连此端口 |
| FN Connect HTTPS/WSS 入口，通常 `443/TCP` | 发现、登录、控制接口和 RELAY 中继 | 客户端及 NAS 需能正常使用 FN Connect；客户端网络允许访问实际返回的入口，代理或网关需支持 WebSocket Upgrade 和长连接 |
| NAS 到 LAN 目标设备的实际服务端口 | 访问共享、网页等内网服务 | NAS 转发策略、目标设备防火墙及服务监听地址均需允许访问；FnCPN 连通不代表目标服务端口自动开放 |

FnCPN 的控制、管理与 WSS 接口只监听应用私有 Unix Socket，通过 fnOS 网关的 `/app/fncpn/` 路径提供服务，**没有需要额外映射到公网的独立 FnCPN HTTP TCP 端口**。fnOS 的 HTTP 管理端口默认是 `5666/TCP`，它不能替代客户端使用的 HTTPS 网关。

FN Connect 发现响应中的 `port.httpsPort` 会被解析，但当前连接逻辑没有使用该字段直连 NAS。原生登录使用 `wss://<FN ID>.fnos.net/websocket`，控制请求使用 FN Connect 域名，中继使用发现结果的 `fn` 入口。因此不能把“能读取自定义端口”理解为“客户端会连接该端口”；修改 NAS 本地 HTTPS 端口后，仍需保证 FN Connect 网关正常可用。

使用 FN Connect 中继通常无需在家用路由器上新增 IPv4 入站端口映射；公网 IPv6 直连则要求对应 UDP 入站可达。不要把上表理解为所有端口都要对公网开放，也不要通过关闭整个防火墙来排障。

检查顺序：

1. 在 fnOS 应用中心确认 FnCPN 已启动，管理页网络状态正常，没有端口占用或网段冲突。
2. 在 NAS、路由器及中间网络检查实际协议和端口：TCP 放行不能替代 UDP 放行，IPv4 规则也不能替代 IPv6 规则。
3. 同一局域网却未进入 LOCAL 时，检查 TCP 54790、FN Connect 网关、访客网络/AP 隔离及跨 VLAN 策略。
4. 网页能打开但 DIRECT 失败时，检查双方 IPv6 和 UDP，而非仅测试网页端口。TCP 端口测试或 ping 成功不能证明 WireGuard 握手成功。
5. RELAY 失败时，检查 HTTPS/WSS 出站和代理策略；WSS 建立后仍需完成 WireGuard 握手，才能判断隧道成功。

## 已连接但无法访问 NAS 或 LAN

- **只有中继，没有直连**：DIRECT 要求 NAS 有可用公网 IPv6，且客户端网络支持 IPv6；不满足或握手失败时会回退中继。RELAY 是正常连接路径，速度受上游和网络条件影响。
- **访问地址不对**：LOCAL 使用 NAS 局域网地址；远程连接优先使用连接详情中的 NAS 私有地址。默认是 `10.253.203.1`，管理员改过网段后以详情为准。
- **网段或端口冲突**：默认 overlay 为 `10.253.203.0/24`，WireGuard 为 UDP 54789。服务端发现冲突不会自动随机切换；由管理员在管理页修改并同步防火墙规则。
- **本地与远端 LAN 网段重叠**：客户端会提示冲突并保留 NAS 私有地址访问。若需访问远端同网段设备，应调整网络规划；反复重连不会消除地址歧义。
- **NAS 可达，其他 LAN 设备不可达**：确认目标网段已在服务端启用，NAS 自身能访问目标设备，目标服务已监听正确地址。再检查 IPv4 转发、宿主机防火墙、Docker 的 FORWARD/DOCKER-USER 规则和目标设备防火墙。NAS 本机可达不等于转发路径已放行，详见 [LAN 转发与 Docker](diagnostics.md#lan-转发与-docker)。
- **其他 VPN 或安全软件冲突**：检查是否接管相同路由、DNS 后缀或拦截特权服务。可暂时断开其他 VPN 做对照，按冲突范围调整配置。
- **主动断开后不自动连接**：断开会保留暂停意图，重启或切换网络也不会取消暂停；需要再次点击连接。

## IP 可用，但域名不可用

支持局域网域名解析的版本通过 FnCPN 专用后缀访问远端设备，使用界面显示的完整名称，例如 `abc.myhome.fncpn`。裸 `abc` 或原来的 `abc.local` 仍由客户端本地网络解析；设备没有 LAN DNS 记录或发布 mDNS 名称时，FnCPN 也不能凭空发现它。

- 确认客户端能通过当前路径访问 NAS LAN IP 的 TCP 54790；DIRECT/RELAY 依赖已安装的 LAN 路由，网段冲突时 DNS 降级，不回退网关。
- 在服务端管理页测试 LAN DNS/mDNS；确认 NAS 能访问上游 DNS、目标设备位于已启用的 LAN 网段。
- 使用 LAN DNS 时允许 NAS 到实际上游的 UDP/TCP 53；使用 mDNS 时允许 NAS 与目标 LAN 的 UDP 5353 组播，网络隔离可能阻断它。无需把这些端口映射到公网，NAS 上 FnCPN 也不额外监听 DNS 53。
- macOS 依赖本机 loopback DNS 和系统 resolver；Windows 依赖 loopback UDP/TCP 53 和 NRPT；
  Linux 依赖 systemd-resolved stub 与自有 DNS 接口。端口或规则冲突可能只使 DNS 降级，IP 访问仍可用。
- 浏览器自带 DoH、其他 VPN 或自定义解析器可能绕过系统规则。检查对应应用的解析设置，并与系统解析结果对照。

解析语义和平台约束见 [接口契约](interfaces.md#局域网域名解析110)。

## Linux：系统 DNS 失效或所有域名都无法解析

若错误包含 `lookup ... on 127.0.0.53:53: server misbehaving`，先区分系统上游 DNS
故障与 FnCPN 专用后缀故障。网关/IP 可达而普通公网域名也无法解析，通常需要检查系统 resolver：

```sh
ip route
resolvectl status
resolvectl query github.com
# 使用 NetworkManager 的系统，检查物理网卡；替换 eth0 为实际接口
nmcli -f GENERAL.STATE,GENERAL.CONNECTION,IP4.DNS,IP6.DNS device show eth0
```

若 DHCP DNS 已出现在 NetworkManager 中，但 `resolvectl status` 的物理链路没有上游
DNS，可由管理员执行 `sudo nmcli general reload dns-full` 重新同步，再复查普通域名。
这仅适用于 NetworkManager 管理的系统；不要把固定公共 DNS、关闭 IPv6、覆盖
`/etc/resolv.conf` 或反复重连当作通用修复。其他网络管理器须按各自的 resolved 集成检查。

Linux 分域 DNS 要求 resolved stub；发行版安装 `systemd-resolved` 依赖时可能切换
`/etc/resolv.conf`，应同时确认原网络管理器继续向 resolved 提供 DHCP 上游。
若同一物理接口同时被 NetworkManager 和 ifupdown/dhcpcd 管理，或出现重复 DHCP 地址/默认路由，
先保存配置与日志，再在本地控制台统一管理方式；不要通过唯一的 SSH 会话直接停用网络服务。

## Linux：系统托盘 / 顶栏没有 FnCPN 图标

FnCPN 的 Linux 托盘使用 AppIndicator / StatusNotifierItem。GNOME 需要相应 Shell 扩展才能在
顶栏显示图标；仅安装应用的 Ayatana 运行库不会为 GNOME 添加托盘宿主。没有图标本身不表示
Core 连接失败。其他桌面请先确认面板已启用兼容的托盘组件，无需安装 GNOME 扩展。

### GNOME：安装托盘扩展

在对应发行版执行以下一组命令：

```bash
# Debian / Ubuntu
sudo apt update
sudo apt install gnome-shell-extension-appindicator

# Fedora
sudo dnf install gnome-shell-extension-appindicator

# Arch Linux：完整更新系统，避免部分升级
sudo pacman -Syu gnome-shell-extension-appindicator
```

包信息可核对 [Fedora 官方包页面](https://packages.fedoraproject.org/pkgs/gnome-shell-extension-appindicator/gnome-shell-extension-appindicator/)
和 [Arch 官方包页面](https://archlinux.org/packages/extra/any/gnome-shell-extension-appindicator/)。
openSUSE 等系统若官方仓库没有提供适配当前 GNOME 的包，可从
[GNOME Extensions 的 AppIndicator and KStatusNotifierItem Support 页面](https://extensions.gnome.org/extension/615/appindicator-support/)
按网站提示安装与当前 GNOME Shell 兼容的版本。

### 注销、重新登录，再以普通用户启用

安装后保存工作，**注销桌面并重新登录**，让 GNOME 加载新扩展；仅关闭 FnCPN 或重新打开终端
可能不够。在重新登录后的 GNOME 桌面终端执行，**不要使用 sudo**：

```bash
# 查找此系统实际安装的扩展 UUID
gnome-extensions list | grep -Ei 'appindicator|statusnotifier'
```

不同发行版使用的 UUID 可能不同，以前一条命令输出为准。常见启用命令如下，选择对应一条：

```bash
# Debian / Ubuntu
gnome-extensions enable ubuntu-appindicators@ubuntu.com

# Fedora / Arch
gnome-extensions enable appindicatorsupport@rgcjonas.gmail.com

# 确认已启用
gnome-extensions list --enabled
```

也可以在 GNOME 的“扩展”应用中打开 AppIndicator 扩展及扩展总开关。随后重新打开 FnCPN：

```bash
fncpn-ui
```

如果提示扩展不存在，先确认已经注销并重新登录，再核对 `gnome-extensions list` 中的 UUID，
不要反复用另一个发行版的 UUID 启用。如果扩展已列出但图标仍不出现，使用
`gnome-extensions info <实际UUID>` 检查状态及兼容性，并确认扩展总开关未关闭。

FnCPN 关闭主窗口后会隐藏到托盘；托盘尚未可用时，可再次执行 `fncpn-ui` 找回窗口，
不必重新安装 Core 或删除连接配置。“退出”会等待用户 Core 服务停止并清理连接，
保留配置和自动连接意图；停止失败时 UI 保留并在诊断中报错。
托盘“打开”会传递桌面激活令牌/菜单时间戳请求前台显示；Wayland 的最终焦点由桌面策略决定。

## 安装、证书与版本问题

- **macOS 拦截安装或启动**：当前包未做 Developer ID 签名和公证；按 [安装说明](../README.md#mac手动安装) 处理可信来源的安装包。移除下载隔离标记不会授予局域网权限；系统询问局域网访问时也需允许。
- **Windows 提示未知发布者**：当前 EXE/MSI 未签名。确认下载来源、架构及管理员授权；安装或界面失败时检查 WebView2 Runtime 是否可用。
- **本地 IPC / 特权服务不可用**：用完整安装包修复或覆盖安装，使 GUI、CLI 和后台服务成套更新；仅替换一个可执行文件可能造成不匹配。
- **TLS / 证书失败**：检查系统时间、域名、DNS、网络代理及证书有效期。FnCPN 使用系统信任和限定域名的内置官方根证书；不要以关闭证书校验作为解决办法。外部浏览器采用自己的信任设置。
- **版本不兼容**：正式版仅在 MAJOR 相同且 client MINOR 不大于 server MINOR 时允许连接，PATCH 差异不阻止连接。client MINOR 更高时升级 server；MAJOR 不同时升级 MAJOR 较小的一端。以界面诊断中的下载链接为准，详见 [版本与兼容性](versioning-and-compatibility.md)。
- **旧数据格式不受支持**：`1.0.0` 不承诺迁移 `0.x` 开发期数据。先备份并核对错误及发布说明，不要直接删除配置、身份或密钥；正式版降级按配套备份恢复。

## 仍无法解决时

保留发生时间、客户端和服务端版本、操作系统与架构、连接路径（LOCAL/DIRECT/RELAY）、完整错误详情，以及“NAS 能否访问、LAN IP 能否访问、是否只有域名失败”等现象。安装了 CLI 时可运行：

```bash
fncpn version
fncpn status
fncpn diagnose --json
```

结合 [诊断指南中的日志位置](diagnostics.md#日志位置) 检查对应时段。分享前检查并脱敏 FN ID、用户名、内外网地址等个人信息；不要提供密码、Cookie、token、私钥或完整凭据文件。runtime 崩溃日志可能包含未脱敏内容，应单独检查后再分享。
