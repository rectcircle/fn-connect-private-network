# v1.0.0 版本兼容性准备审查

本清单依据 [版本管理与兼容性](07-versioning-and-compatibility.md)。下文编号条目保留
改造前的审查记录；实际完成情况以本节为准，不表示正式发布或实机验收已完成。

## 实现进度

- 已建立 `internal/version/VERSION` 唯一来源、模板同步检查与 MAJOR/MINOR 判定器；
  当前验证版本为 `0.1.52`，没有提前发布或标记 `1.0.0`。
- 已增加独立于业务配置的 `/version`，移除 HTTP 中的整数协议版本。HTTP/relay
  响应保留产品版本，客户端与服务端执行同一准入规则。
- 已接入授权、连接、配置长轮询恢复与 relay 自动重连。版本失败停止网络并保留身份，
  缓存配置不能绕过；LOCAL 通过 nonce 身份证明绑定当前 serverVersion，无须公共网关。
- 已放宽远程 JSON 响应的未知字段限制，保留单 JSON、大小、类型和现有业务校验；
  本地 IPC、请求及数据文件继续严格校验。
- 已提供非自动重试的版本错误、两端版本和升级方向，贯通 CLI 与中英文 macOS UI。
- 本地 IPC 版本已移至 IPC 包并由测试校验 Swift 常量；响应附带产品版本，Go/Swift
  检查本地组件一致性，macOS 安装前停止旧 App。
- 已移除服务端旧文件迁移、Darwin 旧网络状态转换、Linux legacy owner 删除特例和
  旧系统级 LaunchAgent 专用清理。旧业务文件存在时明确拒绝初始化，不删除用户数据。
- 已添加正式构建的干净 checkout/tag 检查、产物覆盖保护、当前版本精确校验和及
  commit 清单；图标在临时目录生成，可用 DIST_DIR 隔离验证产物。
- 已补版本矩阵、未知字段、注册阻断、缓存阻断、本地版本防篡改、组件一致性和
  发布清单测试。共享模型目前通过明确序列化行为及测试维护，未机械复制所有 DTO。

仍须在正式发布时完成：归档不可变的 `1.0.0` 安装包及协议/数据基线、安装/升级/回滚
实机验证和真实 LOCAL/DIRECT/RELAY 网络验收。当前没有 `1.x` 历史发布，不能声称已验证
真实历史正式产物互通；后续 schema 变化时必须补迁移前备份和历史数据迁移验证。


## 必须完成：远程版本准入

1. **缺少产品版本的统一运行时来源和判定器。**
   `cmd/fncpn/main.go` 的 `main.version` 默认是 `dev`，通过命令环境用于版本输出和日志。
   `internal/client/remote.go` 的 Bootstrap 没有 server 产品版本，
   `internal/server/http.go` 的 bootstrap 仅返回整数 `protocolVersion`。
   应统一注入产品版本，解析 MAJOR/MINOR/PATCH，仅按 MAJOR 相等且 client MINOR 不大于
   server MINOR 放行。未知、缺失、无效版本不能默认兼容；开发及预发布版本行为需显式定义。

2. **整数协议版本混用远程协议与本地 IPC。**
   `internal/model/model.go` 定义 `ProtocolVersion = 4` 及相等校验；
   HTTP bootstrap、admin snapshot、本地 IPC 都引用它，Swift 还另写一份 `4`。
   删除远程响应对整数协议版本的依赖，使用产品版本准入。本地 IPC 可保留独立版本，
   将常量及校验移到 IPC 职责内，Swift 使用同源生成或构建校验，避免手工漂移。
   本地 IPC 版本不得决定远程是否兼容。

3. **检查覆盖不足：bootstrap 只在授权主流程调用。**
   `internal/client/manager.go` 的 `authorize` 调用 Bootstrap 后没有版本检查；
   `connectWithOptionalConfiguration`、`watchConfiguration` 等普通连接及恢复流程直接
   获取配置或使用已有配置。应把准入纳入统一连接生命周期，在注册、网络配置及恢复
   前执行，并确保配置长轮询、relay 恢复等入口不会绕过。不应仅在 authorize 增加判断。

4. **缓存回退可能吞掉终止性错误。**
   `connectWithOptionalConfiguration` 配置获取失败后，只要缓存配置有效便可能清除错误
   并继续连接；LOCAL 加有效缓存还可跳过配置请求。
   应区分暂时网络故障和明确不兼容、无效版本等终止性失败；后者不能缓存回退。
   版本不兼容应停止当前桥接、收敛网络状态，并保留身份、配置与用户连接意图。

5. **LOCAL 离线恢复与版本重新识别存在设计缺口。**
   当前本地探测返回身份 proof，不返回 server 产品版本；客户端可在公共网关不可用时
   使用缓存恢复 LOCAL。仅通过公共网关 bootstrap 检查会破坏这一能力；仅缓存旧版本
   则无法识别 NAS 已升级 MAJOR。
   实现前应明确可经局域网读取当前版本的路径，以及版本与已认证设备身份的绑定方式。
   本地版本路径必须使用同一个准入公式，不引入功能协商；不可将“本地可达”视为兼容证明。

6. **bootstrap 版本识别依赖业务配置成功。**
   `internal/server/http.go` 的 bootstrap 先计算 ServerAddress，失败时不返回版本。
   路径还位于 `/api/v1/bootstrap`，客户端直接解码完整 Bootstrap。
   应保留跨 MAJOR 可识别的最小入口，先识别产品版本再按兼容结果处理业务数据；
   配置异常不应阻止识别版本。不必删除 `/api/v1`，但未来不能只新增 `/api/v2`
   而移除旧客户端所需的版本识别入口。

## 必须完成：解码与错误表达

7. **远程响应拒绝未知字段。**
   `internal/client/errors.go:decodeResponse` 和
   `internal/client/manager.go:probeLocalEndpoint` 调用 `model.DecodeStrict`。
   新 server 增加响应字段可能导致旧 client 失败。
   为远程响应增加容忍未知字段的单 JSON 解码路径，保留大小限制、类型校验、必需字段
   与安全语义校验。不要全局放宽 DecodeStrict：服务端请求、本地 IPC 和数据文件
   的严格解码并不自动违反远程兼容性契约。

8. **缺少可呈现的版本不兼容错误。**
   `internal/model/model.go` 尚无携带两端版本和升级目标的专用表达；
   `platform/macos/FnCPNApp.swift` 将 PROTOCOL_ERROR 呈现为“响应格式无效，请重试”。
   应设计非自动重试的版本不兼容错误，贯通 Go、IPC、CLI、Swift 和中英文文案；
   显示两端版本及应升级哪一端。还需验证维护任务、网络事件和缓存回退不会让它无限重试。

## 必须完成：构建与发布

9. **版本来源分散，构建可分别覆盖。**
   fnOS 从 `packaging/fnos/manifest` 读取版本；macOS 构建脚本默认 `0.1.47`、build 47，
   允许环境变量覆盖；`packaging/macos/Info.plist` 也保存版本。
   建立单一产品版本源并生成或校验模板、打包元数据和二进制版本。正式构建验证
   tag、版本和 commit 一致；不能产出标称正式版但二进制报告 dev 的包。

10. **发布目录及产物缺少发布隔离和覆盖保护。**
    `scripts/build-all.sh` 对 dist 中所有历史通配产物生成校验和；单包脚本可替换同名包。
    应按当前发布收集确切产物，保存 commit、版本、架构与校验和，并在正式发布流程
    禁止覆盖已发布版本。普通本地开发重建不等同于覆盖正式发布，不必一概禁止。

11. **缺少跨版本兼容性发布门槛。**
    当前测试围绕同一源码实现，没有产品版本准入矩阵及历史正式产物兼容验证。
    v1.0.0 前建立判定、未知字段、注册前阻断、重连、缓存回退、LOCAL 路径和升级提示测试；
    首次发布保存 v1.0.0 产物及协议/数据样本，后续发布按主契约验证历史版本。
    v1.0.0 无须证明所有 0.x 版本兼容。

## 需要准备，但不是当前已证实的远程不兼容

12. **数据 schema 有版本与旧格式读取，缺少通用迁移备份流程。**
    `internal/server/store.go` 有 state schema 和 legacy 读取；客户端配置及平台网络状态
    也有各自版本检查。严格 schema 校验应保留，未来添加字段需升级 schema 并迁移，
    不能直接修改结构体后期望旧数据自然兼容。为 1.0.0 数据保留样本，并准备迁移前备份、
    失败恢复和未来版本拒绝的验证。原子写入不等于可恢复的迁移备份。

13. **本地升级已有停启流程，但组件一致性检查不足。**
    macOS preinstall 停 client daemon/helper，postinstall 检查 helper；
    未见运行中 Swift App 的版本一致性握手，IPC 相同也不能证明 App 与 daemon 同版本。
    应处理旧 App 进程与新 daemon 混用，提供明确的完成更新/重启提示。
    fnOS upgrade 回调已有停启及健康检查，应验证残留进程和恢复失败行为，不能视为完全缺失。

14. **模型同时承担网络契约、内部状态和持久化职责，演进容易相互影响。**
    Bootstrap 引用 Settings/Network 模型，设备注册及配置响应直接使用 model 类型，
    Store 也持久化 model.ServerState。当前共享类型不直接违反契约，但内部重构可能
    意外改变 JSON。应为关键远程 DTO 和 schema 明确边界、保留契约样本，按需要分离；
    不要求仅为分层复制所有结构体。

## 推荐顺序与完成标准

### 0.x 历史逻辑清理（1.0.0 准备范围）

`1.0.0` 不承诺迁移开发期历史格式。下列历史分支应在准备阶段删除，并同步调整
对应测试及当前操作说明；历史验收记录可作为历史事实保留。

- `internal/server/store.go` 的 `readLegacyState`：读取 `state.transaction.json`、
  `settings.json`、`devices.json` 并转换为当前 state.json 的开发期迁移逻辑。
  删除后，缺少正式状态文件但存在不支持的旧文件时，不能静默初始化并忽略旧身份。
- `internal/platform/darwin/state.go` 的 `legacyNetworkState` 及 schema v1 转换：
  删除旧格式到当前 owner 状态的迁移分支，保留正式格式的版本校验、状态恢复和网络清理。
- `packaging/macos/scripts/postinstall` 中旧 `com.rectcircle.fncpn.client` 标识、
  旧系统级 LaunchAgent 的专用迁移清理。同步审查卸载脚本和测试中仅为历史安装布局
  服务的处理，保留当前服务正常停启及卸载。
- `internal/client/storage.go` 对 corrupt/legacy local-probe cache 的容错应拆分审视：
  删除仅针对开发期旧格式的特殊处理，但可重建缓存损坏后的重新获取仍是正常恢复机制。
- README 中旧 Keychain 切换、测试版凭据升级等当前操作说明，应调整为正式数据基线
  及不支持旧格式时的明确重置步骤，不继续承诺开发期迁移。

上述清理优先于未来正式版迁移框架的设计。第 12 项针对 `1.0.0` 起的正式数据，
不要求继续保留现有 `0.x` legacy 读取。仍被选作正式格式的当前 schema 可保留编号，
不需要只为发布 `1.0.0` 重置 schema 数字或删除有效数据。
不得执行无差别用户数据清空；正常断开、临时文件清理、故障恢复、用户主动清除和
卸载流程不属于应删除的历史兼容负担。

1. 统一版本源、运行时注入及纯准入判定器。
2. 固定最小版本识别契约，拆除远程整数协议版本，确定 LOCAL 版本识别路径。
3. 调整远程响应解码及业务校验，接入连接/恢复全生命周期和终止性错误处理。
4. 完成 UI/CLI 提示、构建一致性、本地更新一致性与数据迁移准备。
5. 建立兼容回归及发布门槛，保存 v1.0.0 基线产物和样本后发布。

验收核心：同 MAJOR 且 client MINOR 不大于 server MINOR 的组合允许使用，PATCH
无关；其他组合在网络配置前准确阻断，重连与缓存均不能绕过。远程新增字段不破坏旧
client，正式产物版本一致且可追溯。
