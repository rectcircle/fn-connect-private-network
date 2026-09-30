# FN Connect Private Network

正式实现依据：

- [产品需求与技术架构](docs/03-fncpn-product-requirements.md)
- [PoC 架构与验证结论](docs/02-fncpn-poc-architecture-and-validation.md)

本仓库仅包含正式实现。PoC 源码在本地 `tmp/demo/` 留存，不纳入版本控制；
相关结论见上方 PoC 架构与验证文档。

## 当前状态

当前为 P0 初版实现，尚未完成端到端实机验证。已实现：

- Go `1.27` 模块，module 为 `github.com/rectcircle/fn-connect-private-network`。
- 单一 `fncpn` Go 二进制和多子命令进程模型。
- 版本化 framed JSON IPC。
- 客户端状态模型和 CLI 到 client daemon 的 Unix Socket 通讯。
- server daemon 的 bootstrap、只读设备状态和网络配置 API。
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
- macOS Keychain 私钥与 Cookie 存储，以及非敏感本地配置。
- FN Connect 地址发现、局域网优先、IPv6 直连、WSS fallback 和冲突路由。
- 网络变化事件、relay 自动重连和遵循用户连接意图的统一后台恢复。
- macOS App/PKG 与 fnOS 管理页面/FPK。

尚需在真实安装环境完成端到端验收，包括 fnOS 内核规则、WebKit 登录、
公网 IPv6、FN Connect 中继、升级和卸载清理。

macOS 接口和路由写入不依赖 CGO；正式 macOS 包为 Keychain、
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

macOS 卸载默认保留当前用户配置、日志和 Keychain。需要同时清除指定用户数据时：

```bash
sudo /Library/PrivilegedHelperTools/com.rectcircle.fncpn/uninstall.sh \
  --purge-user-data "$(id -u)"
```
