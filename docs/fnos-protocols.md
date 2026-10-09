# fnOS / FN Connect 协议与验证

本文记录 2026-09-27 至 2026-10-04 的协议研究与实测，不是 fnOS 官方稳定 API 承诺。
上游变动需重新验证；适配实现见 `internal/client/discovery.go`、`native_session.go`、`cookies.go`、`adminproxy.go` 为准。
FnCPN 自有接口见 [接口契约](interfaces.md)。

## 地址发现接口

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

2026-09-27 实测返回了局域网 IPv4、公网 IPv4、公网 IPv6、FN 中继域名、fnOS HTTP/HTTPS 端口、版本和探测校验值。该接口不依赖用户 Cookie，但包含两层由公开前端常量计算的签名：

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

该接口是未公开的内部接口。适配逻辑隔离在 `internal/client/discovery.go` 中，并允许协议变化后快速替换。


## 登录、Cookie 与会话恢复

### 链路 I：FN Connect 凭证与 fnOS 原生会话

验证目标：

- 完整 fnOS Cookie 快照能否通过 FN Connect 统一网关认证。
- FN Connect 是否转发 fnOS 自带的认证 WebSocket。
- 客户端能否从 fnOS 原生会话恢复统一网关接受的短 token。
- 长 token 能否在短 token 失效后恢复会话。

#### Cookie 快照路径

原 PoC 先验证了浏览器 Cookie 路径：

- `mode=relay` 是进入 FN Connect 中继模式的路由 Cookie，不是用户身份凭证。
- 早期 fnOS/FN Connect 实测的认证 Cookie 包含 `entry-token`、`osrt` 和 `ost`；
  不同 fnOS 版本返回的具体 Cookie 集合可能变化，客户端不能硬编码名称。
- Cookie 可以从 Safari Network 面板中的已认证请求获取；HttpOnly Cookie
  不能通过 `document.cookie` 读取。
- 将完整 Cookie 集合保存到权限 `0600` 的文件后，普通 CLI WSS bridge
  能通过 FN Connect 统一网关认证并连接 FnCPN。
- PoC 假设的单一 `fnos-token` 当时没有被 FN Connect 代理直接透传给应用，
  因此不能依赖服务端导出一个固定 token 字符串。
- 正式客户端保留通用 Cookie jar 作为兼容层，并处理 HTTP/WSS 响应中的
  `Set-Cookie`，但主登录路径不再导出浏览器 Cookie。

这条路径证明了“完整浏览器会话快照可以连接”，但快照自身不提供稳定的长期恢复语义；
Cookie 真正失效时仍需要重新登录。

#### fnOS 原生 token 路径

这不是 OAuth2，而是 fnOS 自带的 WebSocket RPC 会话协议。经 FN Connect
建立认证通道时，客户端先发送：

```http
GET /websocket?type=main HTTP/1.1
Host: <fn-id>.fnos.net
Cookie: mode=relay
Connection: Upgrade
Upgrade: websocket
```

FN Connect 返回 `101 Switching Protocols` 后，客户端在 WebSocket 文本帧中
调用以下 RPC：

1. 首次原生登录使用未签名的 `user.login`，包含 `user`、`password`、
   `stay=1`、`deviceName`、`deviceType`、稳定 `did` 和 `reqid`。
2. 登录成功返回短期 `token`、长期 `longToken`、HMAC 密钥 `secret`、
   请求上下文 `backId`、`uid` 和管理员标记。密码只用于本次 WSS 登录，
   不持久化。
3. 已有会话先调用未签名的 `util.getSI`，再使用 `secret` 对
   `user.authToken` 请求做 HMAC-SHA256 签名，确认或刷新短 token。
4. `user.authToken` 失败时，在新连接上重新获取 `si`，再签名调用
   `user.tokenLogin`，其中 `token` 字段承载 `longToken`。
5. 签名线格式为 `<base64-hmac-signature><json-body>`，没有分隔符；
   HMAC 密钥是 `secret` 的 Base64 解码值。
6. 恢复响应只覆盖实际返回的非空字段。实测 `user.tokenLogin` 返回了新短 token
   和 `backId`，但没有返回新的 `longToken` 或 `secret`；客户端必须保留旧值，
   不能把缺失字段清空。
7. 有效短 token 作为 `fnos-token` Cookie 访问 `/app/fncpn` 的 HTTP 和 WSS
   入口；`mode=relay` 仍只负责选择 FN Connect 中继路径。

2026-09-27 实测的 FN Connect 浏览器会话使用 `ost` 作为短 token，并在页面存储中保存
`fnos-Secret`。浏览器中继会话没有暴露 `fnos-long-token`，因此仅导出浏览器
Cookie 不能形成完整的长期恢复会话。正式客户端现已保存
`token + longToken + secret + backId + did + username`，同时保留完整 Cookie jar
作为统一网关的兼容层；密码只用于单次登录，不持久化。

#### 管理后台独立 Web token

管理后台与隧道会话严格隔离。用户提交密码时，client daemon 在 CLI
`user.login` 之外，以独立 DID 执行一次加密 Web 登录：

1. `util.crypto.getRSAPub` 返回 RSA 公钥和 `si`。
2. 客户端生成 32 字节 base62 AES key 和 16 字节 IV，以 AES-256-CBC
   加密完整 `user.login`，再以 RSA PKCS#1 v1.5 加密 AES key。
3. 不携带 `ver` 的 Web 登录返回管理页专用短 token；它不覆盖
   `NativeSession`，也不进入隧道 Cookie jar。
4. loopback AdminProxy 只允许 `/app/fncpn` 子树，清除浏览器传入 Cookie，
   再注入 `mode=relay + Web fnos-token`。

实机最终链路中，`GET /app/fncpn` 与
`GET /app/fncpn/api/v1/admin/snapshot` 均返回 200，且 Web 登录后 CLI token
仍可访问 bootstrap。此前侦察过的 ticket/ost/entry-token 属于 fnOS 首页浏览器会话，
不是 FnCPN 管理页必需条件，正式实现不再使用。

#### 真机验证

在测试 NAS 上取得以下证据：

- 原 PoC 将浏览器已认证 Cookie 保存为 `0600` 文件后，CLI WSS bridge
  已通过 FN Connect 统一网关建立连接。
- Cookie 认证后的 WireGuard over FN Connect WSS 完成握手和数据传输，
  基础 ping 及 100 次 1200-byte payload 均为 0% 丢包。
- 未认证访问 FnCPN HTTP/WSS 均返回 `invalid token`，负向基线成立。
- 携带 `mode=relay` 访问
  `wss://<fn-id>.fnos.net/websocket?type=main`，FN Connect 返回 `101`。
- 在同一条 FN Connect 认证 WebSocket 上，`util.getSI` 和签名后的
  `user.authToken` 均成功。
- 恢复出的短 token 经 FN Connect 请求 FnCPN bootstrap 返回结构化 HTTP `200`。
- 同一短 token 经 FN Connect 建立 FnCPN relay WSS，Upgrade 返回 `101`。
- 使用公网 IPv6 直连 NAS 的 `5667/WSS` 执行 `user.login`，成功取得
  `token`、`longToken`、`secret` 和 `backId`。
- 使用该长期会话执行 `user.tokenLogin` 成功；恢复出的短 token 再次经
  FN Connect 通过 FnCPN HTTP `200` 和 WSS `101`。
- 使用 Node.js WebSocket 客户端直接连接 FN Connect 域名，并在两条独立连接上
  显式携带 `Cookie: mode=relay`：第一条执行 `user.login`，成功返回
  `token`、`longToken`、`secret` 和 `backId`；第二条执行
  `util.getSI + user.tokenLogin`，成功恢复短 token 和 `backId`。
- 上述 Node.js 验证全程未使用公网 IPv6、`trim-cli` 或本地代理；恢复出的短 token
  经同一 FN Connect 域名访问 FnCPN bootstrap 返回 HTTP `200`，建立 relay WSS
  返回 `101`。
- `trim-cli` 直接连接 `<fn-id>.fnos.net` 会收到跳转到
  `fnos.net/<fn-id>/websocket` 的 `302`，原因是它不能注入
  `mode=relay` Cookie，不代表 FN Connect 不支持该认证 WebSocket。

验证边界：

- Cookie 快照连接已经验证，但不同 fnOS 版本的 Cookie 名称、过期时间和续期行为
  不能从单次快照推导。
- `GET /websocket?type=main`、`user.login`、`user.authToken` 和
  `user.tokenLogin` 均已通过 FN Connect 实机执行。
- 上述实测未覆盖 2FA、信任设备、密码修改或服务端撤销设备后的恢复分支。
- 影视 OAuth 只签发 Media token，不需要用于 FnCPN 系统网关认证。

结论：

> 完整 Cookie 快照和 fnOS 原生 token 是两条均已取得真机证据的认证路径。
> Cookie 路径已证明能够直接建立 FnCPN 中继；原生会话进一步提供短 token
> 确认和长 token 恢复能力。正式客户端应优先管理原生会话，同时保留通用
> Cookie jar 作为兼容路径。`user.login`、`user.tokenLogin` 及恢复后的
> FnCPN HTTP/WSS 鉴权已全部通过 FN Connect 实机验证，无需依赖客户端具备 IPv6。


## 客户端会话规则

## Client 获取与维护 fnOS 会话

正式方案：FnCPN.app 提供原生登录表单，client daemon 经 FN Connect 调用 fnOS
原生 WebSocket 会话协议。客户端不读取浏览器数据，不要求复制 Cookie，且不持久化密码。

### 原生登录页规则

- 首次启动直接显示 FN Connect ID、用户名和密码三个字段。
- 密码使用安全输入框，仅随单次 `authorize-native` IPC 请求进入 client daemon。
- `user.login` 必须通过 `wss://<fn-id>.fnos.net/websocket?type=main`，并显式携带
  `Cookie: mode=relay`；不得回退到明文 WS。
- 登录成功后只持久化 `token + longToken + secret + backId + did + username`。
- daemon 必须再通过 FnCPN bootstrap 校验会话并取得 `administrator` 标记；
  非管理员同样可以注册设备与建链，但 Web 管理后台仅对管理员开放。
  不能以 `user.login` 成功代替应用授权。
- 鉴权失败时详情页提供“重新登录”；登录页预填 FN Connect ID 和用户名，密码始终为空。
- 原生登录不支持 2FA challenge；检测到 2FA challenge 时明确报错，不保存不完整会话。

### 管理后台会话

- 管理页 Web 登录使用独立 DID，不覆盖 CLI token、longToken 或 secret。
- 本地 AdminProxy 只监听 loopback，只代理 `/app/fncpn` 子树，并以一次性高熵
  bootstrap nonce 建立本地 HttpOnly 会话。
- 代理始终删除浏览器传入 Cookie，只注入独立 Web token 生成的
  `mode=relay; fnos-token=...`。
- 每次打开管理页前先以 Web token 探测 admin snapshot。明确的 `invalid token/401`
  只清除 Web session 并提示重新登录；网络失败只提示管理后台暂不可用。
- 管理代理探测不调用 `m.fail`，不修改客户端连接状态，不调用 CLI session recovery，
  不清理或回退使用 CLI token。
- Logout、Forget 和 daemon 退出必须关闭管理代理并清除 Web 会话。

### 凭证使用与刷新

- 除刚完成 `user.login` 的首次连接外，连接前使用
  `util.getSI + user.authToken` 确认短 token。
- 短 token 被拒绝时，在新 WebSocket 连接上执行
  `util.getSI + user.tokenLogin(longToken)`；只覆盖响应实际返回的非空字段。
- 恢复短 token 后必须重新加载 Cookie 并重建 RemoteClient；不得使用仍持有旧
  `fnos-token` 的内存客户端重试。
- 配置长轮询或 relay 重连遇到明确鉴权失败时，先执行一次原生会话恢复，再决定是否进入
  `AUTH_REQUIRED`。
- `fncpn client daemon` 使用标准 Cookie jar 为控制请求和 WSS 握手选择匹配 Cookie。
- 响应中的 `Set-Cookie` 必须更新 Cookie jar 和 root 凭据文件，不把 Cookie 当作固定字符串。
- 发现接口无需用户凭证时，不附带 Cookie。
- 长 token 也被拒绝时状态切换为 `AUTH_REQUIRED`，保留设备密钥、配置、FN Connect ID
  和用户名，以便用户重新登录。

### 信任边界

上述实测中的 FN Connect 统一网关要求有效 fnOS 登录态，因此客户端保存的是具有 fnOS 会话能力的敏感凭证。WireGuard 密钥不能替代该网关凭证。

正式实现必须：

- 把凭据保存为 root 私有文件，特权 IPC 按系统确认的调用用户 UID 隔离。
- 明确提供“退出登录”和“忘记此服务端”操作。
- 不在日志、诊断数据和普通配置文件中写入 token、secret、Cookie 或密码。


## 调研来源（保留原调研引用编号）


[cite:1] 飞牛，《如何远程访问到飞牛 NAS？》  
https://help.fnnas.com/articles/v1/access/how-access

[cite:2] 2026-09-27 的 FN Connect 网页前端资源与实测
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
