# 底层可行性验证证据

以下为 2026-09-28 PoC 历史实测（fnOS PoC 包 0.4.1、macOS 0.1.0），仅证明底层方案成立。
其中 fncpn-poc、fncpnctl、外部 wg、51820 端口与 10.203.0.0/24 均为当时测试布局，不能用于当前安装操作。
当前实现使用进程内 WireGuard、54789/UDP 与 10.253.203.0/24；正式验收见 [发布验收](release-acceptance.md)。
Cookie 与原生会话证据见 [fnOS 协议研究](fnos-protocols.md)。

### 3.1 链路 A：FN Connect 转发第三方二进制 WebSocket

验证目标：

- fnOS 统一网关是否允许第三方应用注册 WebSocket 路径。
- FN Connect 中继是否保留 WebSocket Upgrade 和二进制帧。
- 网关改写 Host 后，同源检查能否安全兼容。

验证路径：

```text
外部浏览器
  ↓ wss://<fn-id>.fnos.net/app/fncpn-poc/ws
FN Connect
  ↓
fnOS 统一网关
  ↓ Unix Socket
fncpn-poc 二进制回声服务
```

实测结果：

- FN Connect relay 模式下 WebSocket Upgrade 成功。
- 二进制消息可双向传输并正确回显。
- 完成约 31.6 MiB 压力传输。
- 未发现丢包、内容校验失败或协议错误。
- 对带 `X-Trim-Userid` 的网关认证请求兼容原始 Origin 后，同源检查工作正常。

结论：

> FN Connect 能够承载第三方 fnOS 应用的二进制 WebSocket 长连接，具备作为 WireGuard datagram 兜底运输层的能力。

### 3.2 链路 B：fnOS 应用网络权限

验证目标：

- fnOS 应用进程身份和内核能力是否足以运行服务端。

真机结果：

| 检查项 | 结果 |
|---|---|
| 进程身份 | UID 0 |
| 创建内核 WireGuard 接口 | PASS |
| 创建 TUN 接口 | PASS |
| 创建与删除 nftables 表 | PASS |
| 读取 IPv4 forwarding | PASS |
| 读取 IPv6 forwarding | PASS |
| 清理测试资源 | PASS |

结论：

> fnOS Native 应用具备创建 WireGuard/TUN、配置转发和管理防火墙资源所需的基础权限，服务端不需要容器或额外宿主机安装步骤。

### 3.3 链路 C：WireGuard 服务端与设备模型

验证目标：

- 服务端能否创建稳定接口、持久化密钥并管理多个设备。
- 地址池能否避免重复并复用已释放地址。

验证结果：

- 真机成功创建 `fncpn0`。
- 服务端地址为 `10.203.0.1/24`。
- UDP 监听端口为 `51820`。
- 服务端私钥重启后保持不变。
- 客户端设备成功分配 `10.203.0.2/32`。
- 设备状态能读取最近握手、RX 和 TX。
- 自动化测试覆盖：
  - 私钥持久化。
  - 最低可用地址分配。
  - 删除后的地址复用。
  - 设备表持久化。
  - 旧 `peer.json` 向 `devices.json` 迁移。

结论：

> 服务端 WireGuard 接口、稳定身份、多设备数据模型和 overlay 地址池方案成立。

多台真实客户端同时在线尚未实测，但该项不再依赖新的底层技术能力。

### 3.4 链路 D：macOS IPv6 UDP 直连

验证目标：

- 不使用 Network Extension 时，macOS 是否能通过随包 `wireguard-go` 创建系统隧道。
- IPv6 UDP 是否能直接到达 NAS WireGuard 端口。

验证路径：

```text
macOS utun4
  ↓ wireguard-go
[NAS 公网 IPv6]:51820
  ↓
fnOS fncpn0
  ↓
10.203.0.1
```

实测证据：

- NAS 公网 IPv6 可由客户端直接 `ping6` 到达。
- `fncpnctl start` 成功创建 `utun4`。
- `route -n get 10.203.0.1` 指向 `utun4`。
- 客户端与服务端完成 WireGuard 握手。
- 5 次基础 ping：5/5 成功，0% 丢包。
- 100 次 1200-byte payload：100/100 成功，0% 丢包。
- 该组大包测试平均 RTT 为 64.183 ms。
- 服务端设备统计中的 RX/TX 持续增长。

结论：

> macOS 无需 Network Extension，也能通过 root Helper、`wireguard-go` 和 `wg` 建立可用的系统级 WireGuard 隧道；公网 IPv6 UDP 直连路径成立。

### 3.5 链路 E：WireGuard over FN Connect WSS

验证目标：

- WireGuard endpoint 改为本地 UDP bridge 后，能否经 FN Connect 到达同一服务端。
- WSS 中继是否会破坏 WireGuard 握手和数据报。

验证路径：

```text
macOS WireGuard
  ↓ UDP 127.0.0.1:51821
fncpn-client bridge
  ↓ WSS /app/fncpn-poc/wg
FN Connect
  ↓
fncpn-poc bridge
  ↓ UDP 127.0.0.1:51820
fncpn0
```

实测证据：

- `fncpn-client bridge` 成功连接：

  ```text
  connected to wss://<fn-id>.fnos.net/app/fncpn-poc/wg
  ```

- 中继 profile 使用：

  ```json
  {
    "mode": "relay",
    "endpoint": "127.0.0.1:51821"
  }
  ```

- `fncpnctl start` 保持同样的 `utun4` 管理方式。
- 5 次基础 ping：5/5 成功，0% 丢包。
- 100 次 1200-byte payload：100/100 成功，0% 丢包。
- 该组测试平均 RTT 为 93.127 ms，最大 RTT 为 312.303 ms。
- 服务端设备握手时间和 RX/TX 正常更新。

结论：

> WireGuard datagram 可以完整地通过 FN Connect WSS 中继传输。该路径延迟和抖动高于 IPv6 UDP，但作为兼容性兜底链路可用。

### 3.6 链路 F：中继中断与恢复

验证目标：

- WSS bridge 与 WireGuard 隧道是否可以独立恢复。
- 中继断开是否要求重建 utun。

实测过程：

1. 中继工作时停止 `fncpn-client bridge`。
2. `fncpnctl status` 仍显示接口 active。
3. 对 `10.203.0.1` 的 3 次 ping 全部超时。
4. 重新启动 bridge。
5. bridge 自动重新连接 WSS。
6. 不重建 `utun4`，再次执行 5 次 ping 全部成功。

结论：

> bridge 是可独立重连的运输层。WSS 短暂中断不需要重新创建设备或 WireGuard 接口，正式实现可以把 bridge 重连收敛到用户态连接状态机。

### 3.7 链路 G：停止与路由清理

验证目标：

- 停止连接后是否清理系统路由。
- 重复停止是否安全。

实测结果：

- `fncpnctl stop` 后状态为 `active: false`。
- `10.203.0.1` 路由从 `utun4` 恢复到默认网关 `en0`。
- 再次执行 `stop` 仍成功，未产生错误或残留。

结论：

> Helper 的基本停止流程和路由清理具备幂等性。

### 3.8 链路 H：Helper 权限边界

验证目标：

- Helper 是否只允许固定操作。
- 非当前控制台用户是否会被拒绝。

实测结果：

- 发送不存在的 `exec` 操作时返回 `unsupported operation`。
- 使用 `nobody` 调用时，Helper 返回 `unauthorized caller`。
- macOS `LOCAL_PEERCRED` 与 `LOCAL_PEERPID` 测试确认得到的 UID/PID 与实际连接进程一致。
- `0.1.0` CLI 会把无 request ID 的鉴权错误显示成 `helper response ID mismatch`，但 Helper 已经完成拒绝，不构成越权。

结论：

> 普通用户 CLI 与 root Helper 之间的固定操作白名单和 peer credential 校验有效。`0.1.0` 的问题仅是错误展示，不影响访问控制结论。
