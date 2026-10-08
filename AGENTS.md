# 项目开发约束

## 版本与兼容性

开发、评审、测试和发布涉及版本、client/server 通信、本地 IPC、持久化数据或打包时，
必须阅读并遵守 [版本管理与兼容性](docs/07-versioning-and-compatibility.md)。
该契约从 **1.0.0** 正式版开始生效，不追溯保证 `0.x` 测试版本兼容；
面向 `1.0.0` 的实现必须落实契约，不能仅提升版本号。

- `1.0.0` 建立正式数据基线，不承诺迁移 `0.x` 开发期历史数据。准备阶段删除仅用于
  `0.x` 的数据迁移、旧路径/服务标识清理及历史兼容分支和测试。不得将此理解为
  自动清空用户数据；保留正常断开、卸载、故障恢复与当前运行残留清理。

- macOS client、fnOS server 和 Go CLI 统一产品版本，同一发布来自同一 commit；
  根目录 Go module 跟随源码 tag，不另设独立产品版本。
- 远程版本准入只采用：`client.MAJOR == server.MAJOR && client.MINOR <= server.MINOR`。
  PATCH 不参与判定。不要引入逐功能协商或最低 PATCH 等额外准入条件。
- 同一 MAJOR 的 server 必须兼容所有较低或相同 MINOR 的正式 client，保留其已有
  功能与协议语义。PATCH 修复不得要求另一端更新；client 新增 server 要求必须升 MINOR。
- 同一 MINOR 允许 client PATCH 领先 server；client MINOR 更大时阻止连接并提示升级
  server。MAJOR 不同时阻止连接并提示升级 MAJOR 较小的一端。
- 破坏既有契约或清理历史兼容支持必须升 MAJOR；纯内部兼容重构无需升 MAJOR。
- 最小 bootstrap 版本识别契约跨 MAJOR 保持可识别。注册设备或配置网络前检查版本，
  重连时重新检查。未知响应字段不得使旧 client 失败，必需字段与安全语义仍须校验。
- 本地 IPC 和数据 schema 独立版本化。本地组件成套升级；同 MAJOR 的数据升级保留
  身份、配置和连接意图。降级按配套备份恢复，不假定旧程序能读取新 schema。
- 协议及升级行为变更必须补充有意义的历史版本兼容或迁移验证；发布验收按文档执行。
  已发布版本与产物不可覆盖。
