# 文档索引

文档按主题维护，以源码及明确的产品契约为依据。修改行为时更新对应主题，不再追加一份与现有设计并行的阶段方案。

| 文档 | 内容 |
| --- | --- |
| [产品需求](product.md) | 用户流程、P0 范围、角色、状态、验收条件和后续能力 |
| [技术架构](architecture.md) | 进程边界、WireGuard、存储、事务与安全决策 |
| [接口契约](interfaces.md) | FnCPN HTTP/WSS、本地 IPC 与局域网探测 |
| [网络恢复](network-recovery.md) | 调度、物理网络身份、选路、超时与取消 |
| [fnOS 协议研究](fnos-protocols.md) | 地址发现、原生登录、Cookie、会话恢复、独立 Web 会话及验证 |
| [诊断](diagnostics.md) | 错误契约、日志、排障与安全边界 |
| [开发与构建](development.md) | 环境、测试、打包、图标、本地化和正式构建 |
| [版本与兼容性](versioning-and-compatibility.md) | 从 1.0.0 生效的版本、协议、数据升级和发布约束 |
| [验证记录](validation.md) | 按版本记录的验证范围、未验证项及有价值的历史实测 |

用户安装和使用从 [项目 README](../README.md) 开始；变更记录见 [CHANGELOG](../CHANGELOG.md)。

需求与实现不能替代验证结果。历史 PoC 参数和旧版实测只在证据文档中有效，不作为安装说明；未完成场景及明确暂缓的范围如实记录。上游未公开协议按研究时间记录，不宣称稳定支持。
