# 发布验收

本文件只保留版本级验收结论与支持边界，不记录日常部署或逐次排障。
操作命令见[开发文档](development.md)，判定依据见[版本契约](versioning-and-compatibility.md)。

## 1.1.8 LoongArch 新世界候选（2026-10-29，未发布）

- [Arch 新世界格式验证](https://github.com/rectcircle/fn-connect-private-network/actions/runs/40731600172/job/122969194281)已通过：
  commit `e4c924f`，固定镜像中 C lp64d、官方 Go、GTK/WebKit/AppIndicator、Core/UI 编译、包头与动态链接检查均成功。
  仅为实验候选，尚未验证安装、UI 启动或服务生命周期，也未加入正式发布清单。
  [ALT Sisyphus RPM 构建验证](https://github.com/rectcircle/fn-connect-private-network/actions/runs/40745432371)
  已通过，commit `f381cd5`；C/Go/UI 构建、包头和动态链接检查成功，尚不代表安装和启动通过。
  RPM、Arch 新增干净容器安装及 UI 握手验证，结果待运行；真实 systemd 生命周期尚未覆盖。
- openSUSE LoongArch 兼容性尚未验证：2026-10-29 查询
  [官方 OBS 项目元数据](https://api.opensuse.org/public/source/devel:LoongArch:Factory/_meta)，
  `standard` 仓库列有 `loongarch64`，但项目设置 `<publish><disable/></publish>`；
  `https://download.opensuse.org/repositories/devel:/LoongArch:/Factory/` 和
  `https://download.opensuse.org/ports/loongarch/` 返回 HTTP 404。
  这表明该开发项目目前没有公开发布的安装仓库，不能作为可复现的运行验收基线；
  不据此断言 ALT 二进制不能运行，也不宣称不存在其他社区镜像。
  要验证 openSUSE，仍需可核实的 LoongArch 新世界 rootfs/镜像及配套完整仓库，
  然后对同一 ALT 产物进行依赖解析、ABI 和启动检查。


- [第一步 GitHub 验证](https://github.com/rectcircle/fn-connect-private-network/actions/runs/40668515602)
  成功，源码 commit `88b0a0be9429d300fede97121a141f4b127ec989`；JSON 报告 `passed=true`。
  19 个根 module 测试包交叉编译，用户态回归通过；socket 查询移交完整 VM 并通过。
- `local-socket`、`kernel-client`、`dns-bridge`、`netfilter`、`network-and-resolved`、`ui`
  六项 VM 检查均通过。真实内核 WireGuard 握手/HTTP、IPv6 底层恢复、断开重连、resolved D-Bus/stub 查询与清理已验证。
  普通用户 WebKit 返回 `frontendReady=true`、平台 linux、产品版本 1.1.8 和上述精确 commit。
- 环境为固定 digest 的社区 Debian forky 新世界用户空间、官方 Go 1.27.1、配套 Debian 内核及 QEMU TCG。
  完整依赖与内核版本保留在该运行的日志和 artifact 中；没有龙芯物理设备。
- 第二步新增独立 DEB 基线及 Core/UI 包、安装识别、发布清单门槛和干净 VM 安装/升级/重装/卸载测试。
  [第二步包验收](https://github.com/rectcircle/fn-connect-private-network/actions/runs/40694605149)成功，
  源码 commit `5403993b9932593d0e9b24322252168e978018a3`，产品版本 1.1.8，包证据 `passed=true`。
  14 项检查覆盖 Core 独立安装且无 GUI 库、Core/UI 依赖及 ABI、systemd 服务、升级保留配置和身份、
  同版本重装、普通用户 WebKit 握手、真实内核网络及 resolved、卸载保留用户数据。
  发布工作流复用该验收任务，发布清单要求同 commit、同版本、同包哈希的通过证据。
  升级夹具使用当前代码配较低包版本，只证明包维护行为，不证明历史程序或 schema 迁移。
- 真实 fnOS 登录/OTP、历史正式版本协议、真机 GPU、Wayland/托盘和 polkit 弹窗仍未验收；不支持旧世界。

## 1.1.8 HarmonyOS 审核修复候选（2026-10-28，未发布）

- 用户提供的 1.1.7 真机 JsCrash 定位为窗口销毁后调用 `off(windowSizeChange)` 抛出 1300002。
  清理逻辑已改为可重复执行并隔离原生窗口异常；回归覆盖窗口先销毁、重复生命周期回调及 Go 停止失败后仍清理适配器。
- 应用／Ability 图标改为前景和满幅背景分层 PNG，均 1024×1024；像素检查确认背景无透明圆角或透明边距。
  应用内品牌图形保留现有布局；资源来自共享矢量，不引入新的品牌设计。
- 完整 `scripts/check-release.sh`、25 项鸿蒙 Python 测试和 ArkTS 策略／生命周期回归均通过。
- 随后已安装 1.1.8（构建号 3）到平板模拟器，用户操作两次退出后未发现新的 JsCrash；
  故障目录最新记录仍属于更新前的 1.1.7。原审核机型仍需使用新包复测。
- 普通系统 VPN 与截图产品均已完成本地 release 构建。普通候选已打包签名 APP，内部 HAP 原生代码签名及
  APP 外层签名均通过官方工具校验；这是本地 dirty 候选，未上传、未公开，不代替原真机系统的审核复测。

## 1.1.7 合入检查与 HarmonyOS 候选（2026-10-27，未发布）

新增 HarmonyOS 平台沿用既有认证、版本准入和网络协议；PATCH 不要求服务端更新。
本轮为工作树合入前检查，不是正式发布或完整历史版本验收。

- 固定 CLI/社区 Go 已完成 ARM64 共享库、ArkTS、N-API 与普通/模拟器 HAP 构建；本地 dirty 候选的外部签名及官方验签已通过，尚无干净 commit 的正式发布产物。
- 主机自动回归覆盖 IPC 帧、共享客户端、支持报告脱敏、DNS、TUN替换/回滚与真实 WireGuard 加密 UDP 往返；
  主机 TUN/protect 回调不能替代设备系统权限验证。
- HarmonyOS7 ARM64 模拟器已验证外部线程/并发、定时器/GC、HTTPS200、WSS握手与应用启动。
  此镜像缺少 `com.huawei.hmos.vpndialog`，系统 VPN 只能在具备授权组件的真机/云设备上验收。
- 用户经云设备测试已反馈系统 VPN 连接、NAS访问、私有DNS、登录/菜单/日志与多设备UI当前可用；
  PC首次状态刷新、管理后台独立窗口修正后也反馈测试通过。该反馈未逐项证明所有协议/网络组合。
- 范围为手机/折叠屏/平板/PC/2in1 API20+ ARM64；智慧屏暂不支持。
- 公开分发未签名 HAP；功能已有云真机反馈，跨账号自行调试签名安装尚未验证。

### 构建环境遗留门槛

Go 依赖变更后的 Core vendor 哈希已由首轮 Linux x64 与 ARM64 CI 实际计算并交叉核对一致，
已更新 `nix/go-hashes.json`；完整 Nix 依赖重建、源码隔离检查及两种架构的实际包构建均已在后续 CI 通过。
当前 macOS 环境无 Linux Nix builder，不将主机脚本测试视为 Nix 实际构建通过。
HarmonyOS 已接入发布资产汇总、公开与夸克镜像校验；构建工作流已改用 GitHub 托管 Ubuntu 24.04 x64、26.0.0.821 Linux CLI 社区镜像（固定 SHA256）及原固定 commit 社区 Go；只提供未签名 HAP，不需要外部签名材料。本地 macOS 保留 26.0.0.851。Linux 安装逻辑已通过离线测试；Linux 完整 HAP 构建、未签名结构核验及 CI artifact 上传已通过。线上草稿汇总仍待全平台检查通过，不等于已公开发布。

### 仍需发布验收的场景

| 场景 | 发布门槛 |
| --- | --- |
| 安装与双语 | 最终同commit签名HAP安装；中文/英文、深浅色、字号放大、输入法与复制反馈 |
| 授权 | 密码、访问码、OTP、信任设备、取消、重新登录、更换NAS与权限不足 |
| 网络与DNS | LOCAL/DIRECT/RELAY各路径、TCP/UDP、IP/私有域、DNS隔离、系统加密DNS策略、IPv6与切网 |
| 生命周期 | 锁屏/后台、扩展销毁、进程重启、网络/服务端恢复、授权撤销、VPN冲突和失败清理 |
| 多设备 | 折展/半折叠/分屏不丢连接；PC缩放、键鼠、独立后台复用/关闭与身份失效 |
| 兼容与升级 | 真实历史server准入与拒绝方向、重连重检；覆盖安装保留身份/配置/连接意图、AssetStore持久化 |
| 候选证据 | 干净commit、摘要/签名/工具链与安装日志；社区Go长期压力及系统策略测试 |

未执行的场景不得标记通过；云平台若限制VPN/UDP或后台，应换适合的测试环境。
严格加密DNS政策尚需设备验证，不把使用物理DNS等同于已经满足该政策。

### 草稿验收门槛

1. 确认 `internal/version/VERSION`、平台模板及 Changelog 均为 1.1.7；iOS build number
   来自 `ios/BUILD_NUMBER`，App 与扩展相同；记录干净候选的完整 commit。
2. Windows、Linux、Nix、Android、iOS、fnOS 及统一发布 job 全部成功。
   iOS 使用 Xcode 27.1；不将桌面 Xcode 16.4 构建当作移动端证据。
3. 核对 `release-1.1.7.json`、SHA256SUMS 与下载的每份资产，包含两架构 APK 和
   `fncpn-1.1.7-ios-arm64-unsigned.ipa`。IPA 无签名、描述文件或私钥；两 target 的版本、
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

## 1.1.5 开发阶段基线（2026-10-26）

以下保留 iOS 阶段的验证依据，当前发布编号及产物核验按 1.1.7 执行。

### 开发验证与最终候选

| 项目 | 当时结论 | 后续要求 |
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
