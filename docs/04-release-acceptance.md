# FnCPN P0 发布验收

## 自动化基线

```bash
go test ./...
go test -race ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...
swiftc -typecheck -parse-as-library -framework AppKit -framework WebKit \
  platform/macos/FnCPNApp.swift
./scripts/build-macos-pkg.sh
FNPACK=/absolute/path/to/fnpack ./scripts/build-fpk.sh
```

PKG 必须满足：

```bash
pkgutil --payload-files dist/FnCPN-*-unsigned.pkg | grep -E '(^|/)\._'
# 预期无输出，退出码为 1。
```

展开 `Payload` 后，系统级文件的归档 owner 必须为 `root:wheel`，helper、卸载脚本和
可执行文件不得被普通用户写入。

## macOS 实机

1. 分别验证有控制台用户和无控制台用户安装；已启动的 daemon 必须通过 socket
   healthcheck，启动失败时安装返回非零。
2. 验证授权成功、取消、超时、错误账号；失败不得覆盖最后有效 Cookie。
3. 在 Wi-Fi 切换、睡眠唤醒和快速用户切换后检查状态、utun 与路由；非控制台用户
   不得 Apply。
4. 分别验证 LOCAL、多个 IPv6 候选的 DIRECT、WSS RELAY 及三者之间的回滚切换。
5. SIGKILL privileged-daemon 后重启，确认 journal 只清理其中记录的接口和路由。
6. 卸载默认保留用户数据；显式 purge 成功和 Keychain 锁定失败都必须有明确结果。

## fnOS 实机

1. 验证全新安装、升级和卸载；失败时检查 `${TRIM_PKGVAR}/logs` 与 cleanup journal。
2. 预占 `54789/UDP` 或制造 `10.253.203.0/24` LAN 冲突，确认服务以 degraded 状态
   提供管理页，修改网络后可恢复。
3. 对 fresh apply/update 注入 WireGuard、nftables 和持久化失败，确认回滚错误可见且
   journal 可重试。
4. 停止/卸载后确认 FnCPN 接口和 nftables table 消失，`ip_forward` 保持 enabled。
5. 从 NAS IPv4/IPv6 扫描确认仅私网地址上的 LOCAL probe TCP listener 对外可达，
   管理 API 和 WSS 只通过统一网关/Unix Socket 提供。
6. 以实际 `fncpn` 用户执行 privileged IPC 健康检查，确认 socket 及父目录权限正确。

## 网关鉴权

通过真实 fnOS 统一网关验证：

- 未登录访问 API/WSS 返回 401。
- 普通用户可读取自身配置和连接 relay，但注册和网络更新返回 403。
- 管理员可注册设备和更新网络。
- 外部伪造 `X-Trim-Userid`、`X-Trim-Isadmin` 会被网关剥离并替换，不能提权。
- 非 Unix Socket 启动 server daemon 会失败。

## 数据升级

- 从当前旧版 `settings.json` v2、`devices.json` v1、transaction v2 迁移到单个业务
  `state.json`；存在 transaction 时优先恢复它。已有 `state.json` 后不再重放旧文件。
- 服务端私钥、设备 ID、客户端 Keychain 和用户连接意图保持不变。
- 未知更高 schema version 明确拒绝，不覆盖原文件。
- 业务状态原子重命名前失败不提交，网络回滚到旧期望状态；重命名后目录 sync 失败，
  进程内状态与正式文件均保持新提交状态。root cleanup journal 独立保留。
- macOS 路由或地址已提前消失时，重复清理仍成功；无法读取系统状态或真实权限错误
  不能被当成资源不存在。
- Logout/Forget/重新授权后，旧 watch 响应不能恢复凭证或覆盖新配置；
  控制台用户切回后只恢复原本开启 AutoConnect 的连接。
