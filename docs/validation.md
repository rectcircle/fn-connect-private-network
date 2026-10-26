# 发布验收

本文件只保留版本级验收结论与支持边界，不记录日常部署或逐次排障。
操作命令见[开发文档](development.md)，判定依据见[版本契约](versioning-and-compatibility.md)。

## 1.1.5 发布准备（2026-10-26，未提交、未发布）

### 开发验证与最终候选

| 项目 | 当前结论 | 发布前要求 |
|---|---|---|
| Android 移动端 | 维护者确认本轮开发自测完成；PJZ110 已验证主要登录、网络及交互 | 最终正式签名 APK 覆盖安装、确认连接与数据保留 |
| iPhone | 已使用 DxSign、KravaSign 材料重签，安装及连接验证通过；本轮开发自测完成 | 对最终 unsigned IPA 重签安装，确认 App/扩展均来自同一候选 |
| iPhone Duo | 模拟器折叠/展开、左右侧栏、旋转、菜单与登录返回经维护者确认 | 草稿构建须保留同一实现；不扩大为真实 Duo 硬件验收 |
| iPad | 通用 target 与共享布局可构建 | 实际 iPad/多窗口未覆盖；不承诺多窗口 |
| 自动化 | `check-release.sh` 全套通过，含根模块及移动模块普通/竞态测试、vet、打包、网站与交叉编译；iOS 驱动和资产检查通过 | 提交后的全部必需 jobs 成功，失败不可跳过 |
| 版本与数据 | 统一 1.1.5；无新增 server 要求，远程准入和持久化 schema 不变 | 当前 client 对历史正式 server 的兼容与覆盖升级检查 |
| Release 资产 | 新增未签名 ARM64 IPA、结构校验和构建 manifest | 干净 commit、完整 SHA、同版本/commit 的全部平台资产及 SHA256 |
| 发布操作 | 尚未 commit、push 或 dispatch，等待维护者 review | Review 后才能提交及触发草稿；公开为独立人工步骤 |

本地 Release IPA 已构建并通过结构核验；Android 双架构 Release 编译完成，打包按既有规则
拒绝 dirty 工作区，未生成可发布候选。Nix UI vendor 哈希已随共享源码更新；本机没有 Nix，
完整 Nix derivation 验证仍由提交后的必需 CI 执行。

本地 `--allow-dirty` IPA 只用于 review，其 manifest 不接受为发布候选。开发自测结论
不能代替最终 Release 安装包与完整源码 commit 的对应证明。

### 草稿验收门槛

1. 确认 `internal/version/VERSION`、平台模板及 Changelog 均为 1.1.5；iOS build number
   来自 `ios/BUILD_NUMBER`，App 与扩展相同；记录干净候选的完整 commit。
2. Windows、Linux、Nix、Android、iOS、fnOS 及统一发布 job 全部成功。
   iOS 使用 Xcode 27.1；不将桌面 Xcode 16.4 构建当作移动端证据。
3. 核对 `release-1.1.5.json`、SHA256SUMS 与下载的每份资产，包含两架构 APK 和
   `fncpn-1.1.5-ios-arm64-unsigned.ipa`。IPA 无签名、描述文件或私钥；两 target 的版本、
   ARM64/device Mach-O、Packet Tunnel 声明与许可证通过结构检查。
4. 对最终 Android APK、重签后的 iOS IPA 覆盖安装，确认身份与配置保留、登录与附加验证、
   LOCAL / DIRECT / RELAY、私网 DNS、Safari/浏览器访问 NAS、断开与恢复、系统 VPN 关闭。
   同签名身份升级与更换团队/标识的行为分开记录。
5. 移动端重点回归：首次菜单、中英文、键盘 Next/Go 与遮挡、横屏表单独立滚动、
   冷启动及退出重开、后台 loading/会话/外部链接、日志文件界面与诊断 ZIP 脱敏。
   Duo 覆盖相机在左/右/下的姿态、折叠/展开、图标直达与展开菜单两种登录返回。
6. 根核心变更覆盖桌面 daemon 正常关闭与连接意图保留；按版本契约执行历史正式版本的
   连接与升级组合。至少明确记录同 MINOR PATCH 领先及拒绝更高 MINOR/不同 MAJOR 的提示。
7. README、官网及中英文安装教程链接可用；旧 latest 不含 IPA 时不得误导为可直接安装。
   公开前记录验收人、设备/系统、候选 commit、产物哈希和最终结论。

草稿可在同版本下替换未公开产物；公开之后不得覆盖。检查完成不会自动创建 tag 或公开。

### 明确的支持边界

- iOS 15.0 是构建目标，不代表全部 iOS 版本及设备都经真机验收。
- 模拟器应用内 HTTP/DNS 调试不代表系统 VPN、Safari 或其他 App 已经过隧道。
- 无 App Store/TestFlight 分发；未签名 IPA 需要支持 Packet Tunnel 的配套签名材料。
- 证书服务的可用性、设备绑定及撤销由平台与 Apple 决定，项目不保证证书寿命。
- Android 不承诺 Always-on/lockdown、强制停止后的自动恢复或所有厂商省电条件下永久在线。
- 锁屏、蜂窝/Wi-Fi 切换、弱网、多 VPN 和长时间运行按实际设备记录，不外推为所有环境保证。

## 历史发布结论

历史记录作为兼容与技术基线，不证明 1.1.5 的全部场景通过。原始安装包、协议样本及
发布 manifest 按版本留存，历史失败不作为当前版本单独 Changelog 条目。

| 版本 | 已有结论 |
|---|---|
| 1.1.4 | Android ARM64/x86_64 构建、正式证书与资产核验已接入；PJZ110 主要功能、IPv6 直连及中继经过实测。候选与公开状态以对应 GitHub Release 为准。 |
| 1.1.2 | Linux Core/UI 安装与依赖矩阵、正式资产及镜像核验建立基线；NixOS 保持源码/模块安装。 |
| 1.1.1 | fnOS 授权恢复、访问码及 OTP 兼容修复；macOS 安装后启动入口形成正式行为。 |
| 1.1.0 | DNS、客户端恢复与 Windows 网络配置完成相应候选验证；跨网络长期行为仍受实际系统与环境影响。 |
| 1.0.x | 正式协议与数据基线、macOS/fnOS 安装、Homebrew 升级、Windows 安装维护协议及产物哈希核验建立历史依据。 |

### 历史正式产物依据

以下仅保留正式产物核验与边界，详细操作过程不作为当前发布门槛：

| 版本 | 源码 commit | 构建或发布依据 |
|---|---|---|
| 1.0.0 | `478c4e6cb1076071e903c11823a59fa004f364de` | 流水线 37926567426；macOS/fnOS 版本、架构、许可证与 SHA256 通过；未重新安装正式包。 |
| 1.0.1 | `15bed1aaa5ba6532e89d51f86cd2e29148130b6c` | 流水线 37930012922；产物核验、公开及 Homebrew 更新完成；fnOS 重装未覆盖。 |
| 1.0.2 | `0fcb0a89f3a822e55ee17a8bd2d82386faee7b12` | 流水线 37933160822；原始资产复核后公开，不据此声称 NAS 启用已验证。 |
| 1.0.3 | `0b74a7208c8d3436c101af2ef659839e6481c49a` | 流水线 37935906843；runner 真实权限回归与包核验通过，不等同目标 NAS ACL 实测。 |
| 1.0.10 | `ed73f7f83e531c2aa37ec6f963f8e68e4c79602c` | 草稿 38602567019、公开 38605607575；8 个资产与镜像逐项复核。主分支 Checks 38602543466 未记为通过。 |
| 1.1.2 | `c3fd6b2e67243ff1cafde2d2ec3ab5e4c11d210d` | 草稿 39634507085；21 组 Linux 与双架构 Nix 检查，18 个资产核验；Debian 13 ARM64 最终包覆盖安装及兼容验收由用户确认。 |

### 网络恢复验收边界

系统验证覆盖 Wi-Fi/Ethernet 或蜂窝切换、睡眠/锁屏唤醒、NAS 重启、弱网、多网卡与其他 VPN，
观察健康连接保留、恢复及手动暂停意图。macOS 还需检查首次权限弹窗、拒绝/延迟允许、
设置变更及 unsigned/ad-hoc 覆盖安装后的系统身份变化。自动化预算不代表所有环境的实际耗时。
