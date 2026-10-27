# fnOS / FN Connect 协议参考

本文记录 fnOS 与 FN Connect 的地址发现、本地 Web / 转发入口、应用 API、登录认证、
WebSocket 会话、保持登录、访问码及两步验证协议。
内部接口和不同 fnOS 版本的响应可能变化，本文不代表官方稳定 API 承诺。
示例中的 ID、token、密钥和设备信息均为占位值。

## NAS 本机信息接口

### FN Connect ID

| 项目 | 值 |
| --- | --- |
| 传输 | HTTP over Unix Socket |
| Socket | `/run/trim-connect.sock`（`/var/run/trim-connect.sock` 为等价路径） |
| 方法与路径 | `GET /trimfn` |
| Host | `localhost` |
| 成功状态 | HTTP 200 |
| 响应类型 | `text/plain; charset=utf-8` |
| 响应正文 | 当前 FN Connect ID |

该接口不依赖 fnOS 用户登录 token。

### 管理端口配置

`/usr/trim/etc/network_gateway_setting.conf` 为 JSON 文件，相关字段如下：

| 字段 | 含义 |
| --- | --- |
| `schema.http.port` | HTTP 管理端口 |
| `schema.https.port` | HTTPS 管理端口 |
| `force_https` | 是否强制 HTTPS |
| `redirect` | 重定向设置 |

这些信息属于 NAS 本机接口，不是 FN Connect 公网 API。

## FN Connect 地址发现与路由

### 地址发现

FN Connect 入口包括 `https://<fn-id>.fnos.net/` 和 `https://fnos.net/<fn-id>/`。
地址发现请求如下：

```http
POST https://fnos.net/api/v1/fn/con
Content-Type: application/json
fn-sign: <sha256>
authx: nonce=<nonce>&timestamp=<timestamp>&sign=<md5>

{"fnId":"<fn-id>"}
```

响应信息包括局域网 IPv4、公网 IPv4、公网 IPv6、FN 中继域名、fnOS HTTP/HTTPS 端口、
版本和探测校验值。`port.httpPort` / `port.httpsPort` 对应 fnOS Web 入口的端口提示；
默认 5666/5667 不是固定协议端口，应以发现响应或 NAS 实际配置为准。
该请求不依赖 NAS 用户 Cookie，签名计算如下：

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

- `requestPath` 为 `/api/v1/fn/con`。
- `timestamp` 为 Unix 毫秒时间戳。
- `nonce` 为六位十进制字符串，范围 `100000` 至 `999999`。
- `jsonBody` 为实际发送的 JSON 正文，参与摘要计算的字节与请求正文一致。

相关错误码：

| 错误码 | 含义 |
| --- | --- |
| `5000` | `invalid sign`，签名校验失败 |
| `3000037` | `Not Found Error`，目标 FN ID 未找到 |

签名算法及常量来自公开前端资源，用于请求校验，不代表 NAS 用户身份认证。

### 中继路由

```http
Cookie: mode=relay
```

`mode=relay` 选择 FN Connect 中继路由，不是身份凭据。不带该 Cookie 的请求可能进入
FN Connect 公共入口页或发生跳转，而不是直接到达 NAS。

NAS 应用的网关路径形式为 `/app/<app-name>/...`，支持 HTTP 请求与 WebSocket Upgrade。
应用入口的身份认证由 fnOS 登录会话机制提供，不能把认证能力理解为 FN Connect
转发层独有；访问码开启时，远程入口还存在独立的访问码校验层。

## fnOS 本地 Web 入口与应用路由

fnOS 本地 Web 入口与 FN Connect 转发入口均提供 `/app/<app-name>/...` 路由，
支持 HTTP 和 WebSocket Upgrade。应用的业务接口由对应应用定义，不属于 fnOS 通用 API。

```text
https://<fn-id>.fnos.net/app/<app-name>/<path>
http://<nas-ip>:<http-port>/app/<app-name>/<path>
https://<nas-ip>:<https-port>/app/<app-name>/<path>
```

两种入口均由 fnOS 校验 `fnos-token` 后转给应用；本地可达不代表免登录。
`mode=relay` 与远程访问码属于 FN Connect 入口，本地 Web 入口不依赖这些 Cookie。
浏览器不会自动向 NAS IP 发送 FN Connect 域名下的 host-only Cookie，Secure Cookie
也不会自动用于 HTTP；两种地址的浏览器 Cookie 作用域不同。

本地 HTTPS 的可用性、HTTP 到 HTTPS 的跳转由 NAS 配置决定；证书验证规则仍适用。
缺少或失效 token 的请求可能返回 HTTP 200、纯文本 `invalid token`，不能仅以状态码
判断认证成功。具体响应与访问码挑战见下文。

## WebSocket RPC 与登录会话

### 连接入口

经 FN Connect 建立连接：

```http
GET /websocket?type=main HTTP/1.1
Host: <fn-id>.fnos.net
Cookie: mode=relay
Connection: Upgrade
Upgrade: websocket
```

成功返回 `101 Switching Protocols`，后续使用 WebSocket 文本帧传输 JSON RPC。
访问码开启时，握手请求的 Cookie 还包含 `os-access-code`。

局域网或公网地址直连使用 NAS 的管理端口，路径同为 `/websocket?type=main`；
这类连接不经过 FN Connect 的中继路由。

### 请求与响应

普通请求以 `req` 指定方法，以 `reqid` 标识请求。响应通过 `reqid` 关联请求，
`result` 表示结果，失败响应可携带 `errno`、`errmsg` 等字段。

原生登录请求：

```json
{
  "req": "user.login",
  "reqid": "<request-id>",
  "user": "<username>",
  "password": "<password>",
  "stay": 1,
  "deviceName": "<device-name>",
  "deviceType": "CLI",
  "did": "<device-id>"
}
```

`user.login` 在尚未建立登录会话时使用未签名请求。完成登录后的主要响应字段：

| 字段 | 含义 |
| --- | --- |
| `token` | 短期登录 token |
| `longToken` | 长期会话恢复 token |
| `secret` | CLI 原生登录返回 Base64 编码的请求签名密钥；Web 加密登录的响应需先解密，见下文 |
| `backId` | 请求上下文标识 |
| `uid` | 用户 ID |
| 管理员标记 | 当前账号的管理员身份信息 |

账号需要两步验证时，`user.login` 可以先返回挑战信息，此时尚未签发完整登录会话。

### 请求签名与会话恢复

`util.getSI` 为未签名请求，返回当前连接的会话完整性值 `si`。
已有会话的认证请求使用 `secret` 签名，线格式为：

```text
Base64(HMAC-SHA256(Base64Decode(secret), jsonBody)) + jsonBody
```

签名与 JSON 正文直接拼接，中间没有分隔符。

短 token 认证请求包含 `req`、`reqid` 和以下字段：

```json
{
  "req": "user.authToken",
  "reqid": "<request-id>",
  "token": "<short-token>",
  "si": "<session-integrity>",
  "main": true,
  "active": true
}
```

长期会话恢复使用 `user.tokenLogin`，其中 `token` 承载 `longToken`：

```json
{
  "req": "user.tokenLogin",
  "reqid": "<request-id>",
  "token": "<long-token>",
  "si": "<session-integrity>",
  "deviceType": "CLI",
  "deviceName": "<device-name>",
  "did": "<device-id>"
}
```

恢复响应可能只返回新的短 token 和 `backId`。省略 `longToken` 或 `secret` 不表示
原有对应字段已失效。

`user.authToken` 认证现有短 token，`user.tokenLogin` 使用长期授权恢复会话。
前者不保证能够恢复已过期的短 token；网络超时或应用自身返回的 401 也不能证明
fnOS 登录 token 已失效。

### Web 加密登录

Web 登录使用 `util.crypto.getRSAPub` 获取 RSA 公钥及 `si`；也可由 `util.getSI` 获取
连接的 `si`。加密过程如下：

1. 生成 32 字节 base62 字符组成的 AES key 和 16 字节 IV。
2. 以 AES-256-CBC 和 PKCS#7 填充加密完整登录 JSON，正文包含 `req`、`reqid`、
   用户信息、设备信息及 `si`。
3. 以 RSA PKCS#1 v1.5 加密 AES key。
4. 发送加密封装帧：

```json
{
  "req": "encrypted",
  "iv": "<base64-iv>",
  "rsa": "<base64-encrypted-aes-key>",
  "aes": "<base64-encrypted-json>"
}
```

内层登录方法仍为 `user.login`，Web 会话使用 `deviceType="Browser"`。
加密握手响应的 `reqid` 可能不同于内层请求的 `reqid`。
Web 与 CLI 登录可分别签发会话；两者的 token 不应被视为同一个会话标识。

Web 加密登录响应中的 `secret` 是 AES 密文。使用本次登录的 AES key 与 IV 做 CBC
解密并去除 PKCS#7 填充，得到的是**原始二进制 HMAC 密钥**；然后将这些字节编码为
Base64 保存。网页对应逻辑为 `AES.decrypt(...).toString(Base64)`，不能把解密结果先当作
UTF-8 / Base64 文本再解码，否则会拒绝有效密钥或使用错误的签名 key。

### Web 保持登录与活动维护

网页的“保持登录”不仅影响登录请求的 `stay`，还包含本地保存和恢复逻辑：

| 项目 | 网页行为 |
| --- | --- |
| `stay` | 登录及 OTP 验证传递保持登录选项 `stay` |
| `fnos-token` | 短 token，网页使用未指定固定到期时间的会话 Cookie |
| `fnos-long-token` | 长期 token，网页 Cookie 保存期限为 30 天 |
| `localStorage["fnos-Secret"]` | 解密后的二进制签名密钥经 Base64 编码保存 |
| `localStorage["os-keep-login"]` | `"1"` 表示启用自动恢复 |
| 会话恢复 | 先 `user.authToken`；被拒绝且保持登录开启时，用长期 token 调用 `user.tokenLogin` |
| 设备身份 | Web 会话使用 `deviceType: "Browser"` 及对应设备身份 |
| 心跳 | 主 WebSocket 约每 15 秒发送 `{"req":"ping"}`，接收 `{"res":"pong"}` |
| 活动维护 | 网页在响应处理等链路触发 `user.active`，以 60 秒间隔节流；不是短 token 到期时间 |

网页另有 **1 小时**无鼠标/键盘活动后的前端检查：没有长期 token 且未开启保持登录时，
后续请求可触发退出。这不是服务端短 token 的 1 小时有效期。30 天也只是网页对长期
Cookie 的保存期限，不能保证服务端在这段时间内始终接受该 token。

### 网关 Cookie 与认证响应

```http
Cookie: mode=relay; fnos-token=<short-token>
```

`fnos-token` 承载 fnOS 短 token，供应用网关的 HTTP 与 WebSocket 请求认证。
浏览器会话也可能涉及 `entry-token`、`osrt`、`ost` 等 Cookie，具体集合随登录方式和
fnOS 版本变化。HttpOnly Cookie 不通过 `document.cookie` 暴露，Cookie 的更新和删除
由响应中的 `Set-Cookie` 表达。

网关拒绝无效登录会话时，可能返回正文 `invalid token`；该正文可能伴随 HTTP 200、
401 或 403。HTTP 200 本身不表示登录会话有效。该响应与下面的访问码挑战不同。

## 访问码（Access Code）

访问码是设备入口的前置校验，与账号密码和 OTP 相互独立，不属于 fnOS WebSocket RPC。
FN Connect 网关可在 HTTP 代理或 WebSocket Upgrade 之前执行该校验。

访问码覆盖 fnOS 网页、相关官方应用及 FN Connect 远程访问入口；局域网直连、
WebDAV、SMB、FTP 和独立分享链接不属于同一入口校验通道。

### 挑战响应

未通过访问码校验的请求可收到访问码 HTML 页面，响应带有：

```http
x-trim-safe-code-challenge: 1
```

该页面可能以 HTTP 200 返回，不是应用 API 的 JSON 响应。路由 Cookie `mode=relay`
与访问码会话互不替代：公共入口页不等同于访问码挑战页。

访问码是否开启可通过尚未携带访问码会话的网关请求及挑战标记识别；普通请求本身
不提交访问码，与验证接口的失败尝试计数不同。

### 验证请求

```http
GET /access_code_verify HTTP/1.1
Host: <fn-id>.fnos.net
x-access-code: <base64-encoded-utf8-access-code>
x-access-source: web
Cookie: mode=relay
```

`x-access-code` 是访问码 UTF-8 原始字节的标准 Base64 编码。

| 状态码 | 含义 |
| --- | --- |
| 204 | 验证成功，无响应正文，通过 `Set-Cookie` 返回访问码会话 |
| 401 / 403 | 访问码校验失败 |
| 429 | 尝试受到限制 |

### 访问码会话

```http
Set-Cookie: os-access-code=<session-token>; Path=/; HttpOnly; SameSite=Lax
```

| 属性 | 含义 |
| --- | --- |
| `os-access-code` | 访问码校验得到的会话 token，与账号登录 token 独立 |
| Domain | 未声明时为 `<fn-id>.fnos.net` 的 host-only Cookie |
| Path | `/`，覆盖该主机的 HTTP 与 WebSocket 入口 |
| HttpOnly | 不向页面 JavaScript 暴露 |
| SameSite | 上例为 `Lax` |
| 有效期 | 未声明 `Expires` / `Max-Age` 时为会话 Cookie；不据此推导服务端的有效期 |

后续网关请求可以同时携带三类 Cookie：

```http
Cookie: mode=relay; os-access-code=<session-token>; fnos-token=<short-token>
```

三者分别表示中继路由、访问码会话和账号登录会话。

另有访问码路径入口：

```text
https://<fn-id>.fnos.net/c/{access-code}
```

验证成功后进入后续登录流程，失败可返回 HTTP 404。

## 两步验证（OTP）

fnOS 的两步验证使用 TOTP（RFC 6238），OTP 为六位数字，时间周期为 30 秒。
与访问码不同，两步验证是账号登录流程的一部分，由 `user.login` 的响应表达验证要求。

### 登录挑战

账号已绑定两步验证且设备尚未受信任时，登录响应可包含：

```json
{
  "result": "succ",
  "isBindTwofaSecret": true,
  "isTrustedDevice": false,
  "accessToken": "<challenge-token>",
  "secureEmail": "<masked-email>"
}
```

此处的 `result="succ"` 表示该阶段请求成功，不表示整个登录完成；响应尚未包含完整的
`token`、`longToken`、`secret`。`accessToken` 是本次两步验证挑战的凭据，与最终登录
`token` 及访问码 Cookie 均不同。

| 响应字段组合 | 含义 |
| --- | --- |
| `isTwofaEnforced=true` 且 `isBindTwofaSecret=false` | 账号被要求启用两步验证，但尚未绑定 |
| `isBindTwofaSecret=true` 且 `isTrustedDevice=false` | 账号已绑定两步验证，当前设备需要 OTP |

### 提交 OTP

```json
{
  "req": "user.2fa.loginVerify",
  "reqid": "<request-id>",
  "accessToken": "<challenge-token>",
  "code": "123456",
  "isTrustedDevice": true,
  "stay": 1,
  "deviceName": "<device-name>",
  "deviceType": "CLI",
  "did": "<device-id>",
  "si": "<session-integrity>"
}
```

| 字段 | 含义 |
| --- | --- |
| `accessToken` | 登录响应返回的挑战凭据 |
| `code` | 六位 OTP 字符串，保留开头的零 |
| `isTrustedDevice` | 是否请求信任当前设备 |
| `stay` | 保持登录选项 |
| `deviceName` / `deviceType` / `did` | 登录设备信息 |
| `si` | 当前 WebSocket 连接的会话完整性值 |

Web 加密登录同样使用该 RPC 方法，内层请求通过 `encrypted` 帧封装，设备类型为
`Browser`。`submitTwofaCode` 是部分客户端库的辅助函数名称，不是 fnOS RPC 方法名。

验证成功后返回完整登录会话。失败响应使用 `result="fail"` 和 `errno`、`errmsg` 等字段
表达原因；`135168` 为账号认证拒绝，不能将所有失败响应都解释为 OTP 错误。

## 参考资料

- [飞牛：远程访问](https://help.fnnas.com/articles/v1/access/how-access)
- [FN Connect 地址发现前端资源](https://static2.fnnas.com/connect/assets/1MPg8Gvv7C7Lrf46.js)
- [飞牛应用开放平台：统一网关](https://developer.fnnas.com/docs/core-concepts/gateway-registration/)
- [飞牛应用开放平台：开放 API 调用方式](https://developer.fnnas.com/api/calling/)
- [飞牛：访问码](https://help.fnnas.com/articles/v1/safety/access-code)
- [飞牛：双重验证](https://help.fnnas.com/articles/fnosV1/settings/setup-2fa.md)
- [fnos-ts-client](https://github.com/Timandes/fnos-ts-client)
- [fnOS Web API 文档（社区维护）](https://fnosp.github.io/fnnas-api/)
