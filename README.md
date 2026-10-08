# FN Connect Private Network

正式实现依据：

- [产品需求与技术架构](docs/03-fncpn-product-requirements.md)
- [PoC 架构与验证结论](docs/02-fncpn-poc-architecture-and-validation.md)
- [错误码与全链路诊断](docs/05-error-diagnostics.md)
- [发布验收与实机记录](docs/04-release-acceptance.md#实机验收记录)

fnOS 原生 token 获取、恢复协议及 FN Connect 验证边界已收敛到
[PoC 凭证章节](docs/02-fncpn-poc-architecture-and-validation.md#39-链路-ifn-connect-凭证与-fnos-原生会话)。

本仓库仅包含正式实现。PoC 源码在本地 `tmp/demo/` 留存，不纳入版本控制；
相关结论见上方 PoC 架构与验证文档。

## 当前状态

当前为 P0 初版实现。截至 2026-10-02，已完成一轮 RELAY 基础访问、LAN 转发、
断开清理、升级保留凭据/暂停意图、服务端停启恢复、Mac 重启自动连接，以及设备清理、
MTU 大包和文件下载的实机验证。完整 P0 发布验收尚未完成；版本、证据类型和剩余范围
见 [实机验收记录](docs/04-release-acceptance.md#实机验收记录)。已实现：

- Go `1.27` 模块，module 为 `github.com/rectcircle/fn-connect-private-network`。
- 单一 `fncpn` Go 二进制和多子命令进程模型。
- 版本化 framed JSON IPC。
- 客户端状态模型和 CLI 到 client daemon 的 Unix Socket 通讯。
- server daemon 的 bootstrap、设备状态、离线设备删除和网络配置 API。
- overlay 校验、最低可用地址分配、地址复用和全量重分配。
- 服务端业务状态使用单个原子写入的 `state.json`，兼容读取旧版拆分文件。
- Darwin/Linux Unix peer credential 识别和 privileged-daemon 调用者授权。
- privileged-daemon 的 `status/apply/remove/resume` 白名单协议。
- 客户端 WireGuard 计划校验和 UAPI 配置生成。
- macOS 进程内 `wireguard-go` runtime，不启动外部子进程。
- macOS 通过固定 `/sbin/ifconfig` 和 `/sbin/route` 配置接口与路由。
- 客户端网络 apply、remove 和 resume 的事务回滚。
- macOS root 网络状态原子持久化、启动恢复和退出清理。
- fnOS 内核 WireGuard、IPv4 forwarding、专属 nftables 规则和服务端密钥。
- server daemon 统一串行提交并发布配置/网络快照，root 按完整 Plan 声明式收敛。
- 幂等设备注册、配置长轮询同步和受限二进制 WebSocket relay。
- macOS root 私有文件保存私钥、fnOS 原生会话与 Cookie jar，按系统 peer UID 隔离；
  密码不持久化。
- FN Connect 地址发现、局域网优先、IPv6 直连、WSS fallback 和冲突路由。
- 网络变化事件、relay 自动重连和遵循用户连接意图的统一后台恢复。
- macOS App/PKG 与 fnOS 管理页面/FPK。

尚需验证 LOCAL、公网 IPv6 DIRECT、多路径/网络切换、睡眠唤醒、网段冲突、
多客户端并发、配置变更与故障回滚、fnOS 2FA、最新版卸载及安全负向场景。
单次下载正常不代表吞吐、文件哈希或长期稳定性验收已完成。

macOS 接口和路由写入不依赖 CGO；正式 macOS 包为
SCDynamicStore 网络与控制台用户监听启用 CGO。

## Package 结构

```text
cmd/fncpn/          单一命令入口
internal/model/     跨边界数据契约
internal/client/    客户端业务
internal/server/    服务端业务
internal/ipc/       本地通讯协议
internal/privileged/特权操作入口
internal/wireguard/ WireGuard 计划和配置
internal/platform/  系统能力适配
internal/command/   子命令解析与进程组装
```

package 按业务职责命名，不设置 `core`、`common` 或 `utils` 中间层。

## 本地运行

```bash
go run ./cmd/fncpn version
```

需要在本机长期使用 CLI 时安装到 `$GOBIN`：

```bash
go install ./cmd/fncpn
```

## 测试

```bash
go test -race ./...
go vet ./...
node --test internal/server/web/index.test.cjs
```

## 命令

```text
fncpn version
fncpn status [--json]
fncpn authorize <fn-id>
fncpn connect
fncpn disconnect
fncpn retry
fncpn logout
fncpn forget --yes
fncpn diagnose [--json]
fncpn client daemon
fncpn client privileged-daemon
fncpn server daemon --socket PATH --state-dir PATH [--log-file PATH]
fncpn server privileged-daemon
```

## 构建安装包

```bash
./scripts/build-all.sh
./scripts/build-macos-pkg.sh
./scripts/build-fpk.sh
```

产物统一写入 `dist/`，打包中间文件使用系统临时目录。

FPK 构建要求通过 `FNPACK` 或 `PATH` 显式提供 `fnpack`，不依赖本地 PoC 文件。
首次初始化默认使用 `10.253.203.0/24` 和 `54789/UDP`；冲突时
服务端保持管理页面可用，由管理员修改后重试。

- `FnCPN-<version>-arm64-unsigned.pkg`：Apple Silicon macOS 客户端。
- `fncpn-<version>-x86.fpk`：x86_64 fnOS 服务端。
- `fncpn-<version>-arm.fpk`：ARM64 fnOS 服务端。

macOS 凭据保存在 `/var/db/fncpn/credentials/<uid>/`：root 所有，目录 `0700`、
文件 `0600`，普通客户端通过受限特权 IPC 存取，不再调用 Keychain。
从仍使用 Keychain 的旧测试版切换时，不迁移旧凭据，需要重新授权；
旧 Keychain 条目不会自动读取或删除。已使用文件凭据的版本保留配置升级时不应主动
清空身份，`0.1.13` 升至 `0.1.14` 后复用原设备和凭据重连已通过实机验证。

macOS 卸载默认保留用户配置、日志和 root 凭据文件。需要同时清除指定用户数据时：

```bash
sudo /Library/PrivilegedHelperTools/cn.rectcircle.fncpn/uninstall.sh \
  --purge-user-data "$(id -u)"
```

### 图标生成

应用图标由 `scripts/generate-icons.swift` 使用 AppKit 程序绘制，macOS 构建时自动生成完整 Retina iconset 和 `AppIcon.icns`，fnOS 使用同源的 64 / 256 像素 PNG。macOS 上可运行 `swift scripts/generate-icons.swift packaging/assets` 重新生成；其他平台打包使用已提交的 PNG。

macOS 状态栏使用22 × 18 pt 模板图标，自动适配深浅色及菜单选中颜色。应用与状态栏图标都由 `scripts/generate-icons.swift` 中同一个 `connectionMark()` 绘制，使用一致的“内网边界、飞牛标志与接入路径”图形。应用图标采用 fnOS 默认图标风格的浅蓝底与亮蓝前景。所有状态保留内网边界和飞牛标志，以连接线最左侧的端点替换图形表达状态：圆点表示已连接，省略号表示连接中，叉号表示断开，感叹号表示异常或需要登录。局域网、IPv6 直连、中继共用已连接图标，具体连接方式由悬停提示和界面详情说明。生成脚本和生成的资源均保留在仓库中。

正式图标采用圆角矩形表示内网边界，内部复用 `packaging/assets/fnos-original.png` 中的飞牛原始标志，外部连接采用圆角水平 / 垂直折线，并通过边框预留的入口进入内网。原始标志素材与绘图脚本一同保留，重新生成时无需联网。
