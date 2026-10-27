# 平台支持与技术决策

本文汇总各客户端平台的支持范围、技术选择及其依据。范围依据仓库当前实现和既有验证记录整理；构建目标不等于实机验收通过。操作命令见 [开发、构建与发布](development.md)，实际验收结果见 [验证记录](validation.md)。

## 支持范围

| 平台 | 当前范围 | 交付方式与验证边界 |
| --- | --- | --- |
| macOS | macOS 13 起，Apple Silicon / Intel | 分架构 PKG；默认原生 UI，可选 Wails；Intel 原生验收不能由交叉构建替代 |
| Windows | Windows 10 22H2（build 19045）起的 x64 / ARM64 客户端系统 | 分架构 MSI；不支持 Windows Server、32 位；Windows 10 与原生 x64 已有用户验收 |
| Linux | 下表列明的 systemd 常规发行版 | Core/UI 双包；已有容器矩阵及 Debian 13 arm64 验证，其他发行版实机集成仍待验收 |
| Nix / NixOS | x86_64-linux、aarch64-linux | Flake 源码构建、NixOS 模块；双架构构建和前端启动已验证，NixOS 实机集成待验收 |
| iOS / iPadOS | iOS 15.0 起的构建目标；ARM64 设备与本机架构模拟器 | 1.1.5 新增未签名 IPA；设备证书重签自测通过，最终草稿验收见验证记录 |
| HarmonyOS | API20+，ARM64；手机/折叠屏/平板/PC/2in1 | 未签名 HAP，需自行签名；云真机功能测试通过，智慧屏不支持；发布门槛见验证记录 |
| Android | 当前配置 Android 8.0 / API 26 起，arm64-v8a、x86_64 | 提供正式签名 APK；移动端开发自测完成，最终草稿仍需同 commit 核验 |

Linux 目标以 [targets.json](../packaging/linux/targets.json) 为唯一机器可读清单，以下保留 2026-10-21 核对结果，不自动承诺未来版本：

| 系列 | 列入目标的版本 | 架构 | 格式 |
| --- | --- | --- | --- |
| Debian | 12、13 | amd64、arm64 | deb |
| Ubuntu | 22.04、24.04、26.04 LTS；26.10 | amd64、arm64 | deb |
| Fedora | 43、44 | amd64、arm64 | RPM |
| Arch Linux | 完整更新的 rolling 系统 | amd64 | pkg.tar.zst |
| openSUSE Leap | 16.0 | amd64、arm64 | RPM |
| openSUSE Tumbleweed | 完整更新的 rolling 系统 | amd64、arm64 | RPM |

这些目标共 21 个发行版/架构组合，共用 10 个 Core/UI 安装包，不为每个发行版另编译一份。Alpine/apk、Arch Linux ARM 以及 Fedora Atomic、openSUSE Aeon/Kalpa/MicroOS 等不可变系统不在普通包支持范围内。发行版发布、EOL 或滚动 ABI 变化时须重新核对清单、依赖和 CI。

## 共用原则

各端复用 Go 连接核心，统一认证、版本检查、配置同步、LOCAL → IPv6 UDP → WSS 选路和恢复语义；UI 与系统网络适配按平台实现。这样可复用协议和安全逻辑，同时遵守各系统权限与生命周期约束。

只接管 NAS overlay 和用户启用的 LAN 路由，不默认接管互联网全流量；重叠 LAN 不安装冲突路由。域名能力按平台接入系统解析器，失败时明确降级，避免影响公共 DNS。绕过系统解析器的应用不保证私有后缀解析。

桌面采用普通用户连接 daemon 与系统特权组件分离：前者管理身份和连接意图，后者配置网络。本地组件成套升级、校验调用方身份，只清理自身资源。详细安全与恢复机制见 [技术架构](architecture.md) 和 [网络恢复](architecture.md#网络检测与恢复)。

所有平台遵守 [版本与兼容性](versioning-and-compatibility.md)：同发布统一版本、同 commit；远程准入仅为 MAJOR 相同且 client.MINOR ≤ server.MINOR，PATCH 不参与。本地 IPC 和数据 schema 独立版本化；升级保留正式身份、配置和连接意图，降级恢复配套备份。新增平台复用已有协议且不增加 server 要求时允许 PATCH，编码完成不等于发布验收完成。

## macOS

**结论：默认使用 Swift/AppKit 原生界面，WebKit 承载管理后台；Go 用户 daemon 与 root daemon 分别由 LaunchAgent、LaunchDaemon 管理。** 保留 Wails 构建选项，默认发布使用原生 UI。

选择原生界面是为了接入 macOS 窗口、菜单栏、授权和通知体验；连接核心独立于窗口，使 UI 生命周期与网络职责分开。两个 UI 方案共用应用标识、安装路径和维护流程。

数据面采用进程内 wireguard-go 与系统 utun，无需安装第三方 WireGuard App；分域 DNS 使用 loopback DNS 与 resolver 文件，避免替换全局 DNS。特权操作集中在 root 组件，IPC 使用 Unix socket 及对端身份校验。

交付 Apple Silicon、Intel 两种 PKG，并提供 Homebrew Cask 安装入口。当前包未签名；构建、系统安全拦截处理和安装步骤见 [项目 README](../README.md) 与 [开发文档](development.md)。

## Windows

**结论：使用 C++ Win32 原生界面、WebView2 管理后台、Go 用户 daemon 和 LocalSystem 服务。** 原生 UI 接入 Windows 托盘和窗口，后台连接及网络逻辑继续复用 Go。

数据面采用 wireguard-go 与官方签名 Wintun，分域 DNS 使用 loopback DNS 与 NRPT；通过 Named Pipe 和 Windows 身份校验隔离普通用户与系统权限。凭据使用 DPAPI 保护。

采用每架构一个 MSI，成套安装和升级 UI、Go 核心及驱动依赖，减少组件错配。管理后台使用共享 Evergreen WebView2 Runtime；缺失时安装器下载微软签名引导器，因此首次安装可能需要联网。卸载保留共享 Runtime 和用户数据，清理当前用户数据必须显式选择。

当前项目 EXE/MSI 未签名，Wintun 与 WebView2 引导器保留官方签名。开发环境、构建检查、维护协议验证及安装日志见 [Windows 开发与发布](development.md#windows-开发与构建)。

## Linux

**结论：使用纯 Go Core 与可选 Wails UI，systemd 管理用户 daemon 和按 UID 隔离的 root 服务。** Core 可单独用于无头系统，UI 采用 GTK3、WebKitGTK 4.1 和 Ayatana AppIndicator，避免让无头安装引入图形依赖。

首次启用特权实例需要明确管理员授权；UI 使用受限 polkit 入口，CLI 使用安装配置入口，不授予任意网络命令免密权限。用户服务随登录启动；注销后持续运行由管理员选择 systemd linger。关闭主窗口隐藏至托盘，显式退出等待服务清理，断开则暂停自动连接。GNOME 等桌面的托盘支持取决于系统扩展，见 [故障排查](troubleshooting.md#linux系统托盘--顶栏没有-fncpn-图标)。

数据面直接使用内核 WireGuard、netlink 和 wgctrl，不依赖外部 wg/nft 命令，不开启转发或修改主机防火墙。DNS 选择 systemd-resolved 专属链路与受限转发，以支持按后缀解析而不改物理网卡 DNS；要求宿主使用 127.0.0.53 stub，不满足时明确降级。项目不改写 resolv.conf，但发行版安装 resolved 的维护脚本可能调整它，安装时仍须核对原网络集成。

按格式和架构共用安装包，是为了控制发布与验收成本；UI 必须在指定基线构建，并用各目标系统的真实依赖检查同一份包的 ABI 与启动行为。Core/UI 精确同版本，升级成套执行，卸载保留用户数据。

RPM 使用项目专用 OpenPGP 密钥签名，无需购买签名服务。安装时同时验证签名与内容摘要，不以 SHA256 或 digest 成功替代来源验证，也不关闭系统包签名策略。公钥、手动验签及密钥维护见 [开发文档](development.md#rpm-签名与密钥维护)。

草稿 CI 已覆盖 5 组基线构建和 21 组目标容器安装、ABI、真实 WebKit 前端启动、重装及卸载。发布证据须指向同 commit、同版本、同包哈希。容器检查不能证明真实 systemd、polkit 弹窗、Wayland/托盘、内核、DNS 或完整 fnOS 连接已验收；历史协议兼容仍须单独验证。

### Nix / NixOS

**结论：以 Flake 源码构建和声明式模块接入，不额外发布 Nix 二进制资产。** 依赖由 flake.lock 等锁定；默认包为无头 Core，UI 可选，目前没有项目自建二进制缓存。

NixOS 模块声明普通用户及稳定 UID，安装同版本组件、polkit 与 systemd 服务，避免套用 deb/RPM 的命令式安装脚本。单独运行 Flake UI 不会自动向宿主注册服务或权限；其他 Linux 的宿主集成使用原生安装包。

DNS 仍受 resolved stub 条件约束。删除模块不会删除身份和配置；generation 回滚只回滚程序，不回滚持久化数据，跨 schema 降级须恢复配套备份。配置示例和依赖更新见 [开发文档](development.md#nix-flake-与-nixos)。

## Android

**结论：采用 Fyne UI、薄 Java VpnService 系统入口与共享 Go 连接核心；单应用进程、单 Go runtime，无需 root。** Java 负责 VPN 授权、前台通知、TUN、网络回调和 Keystore；连接业务留在 Go，避免复制桌面逻辑或引入桌面双 daemon 与特权 IPC。

Service 持有唯一连接实例，UI 只提交命令和订阅状态。选择这一边界是为了让关闭 Activity 不结束连接，并允许系统直接重建 Service；后者已有本地构建及恢复逻辑测试，系统回收后的长期恢复仍需专项验收。不承诺强制停止后恢复、开机自动连接或厂商省电限制下永久在线；Always-on/lockdown 未验收前不声明支持。

数据面使用系统 VpnService TUN 与 wireguard-go，不依赖第三方 WireGuard App。控制连接、中继、直连与 DNS 上游须按用途保护并绑定底层网络，避免 VPN 回环；切网后重建旧网络上的连接。

Android 无桌面后缀 resolver 接口，因此系统名称解析采用 VPN 内 DNS 入口，同时处理 UDP/TCP；项目后缀经 NAS 查询与答案校验，公共域名经物理网络解析。**LOCAL 启用名称解析时仍保留 DNS 用 VPN/TUN，但不运行 WireGuard**；这是与桌面 LOCAL 的必要差异。Private DNS、DoH、普通互联网 IPv4/IPv6 及 LOCAL 后台资格仍须真机验证。

凭据由 Keystore 管理的密钥加密保存，设备身份及凭据排除自动备份，避免跨设备恢复共用身份。管理后台使用原生 WebView，保留原生会话与管理 Web 会话隔离。

发布前必须完成 Android 构建与 Service 冷启动、外部 App 的 NAS/LAN 访问、DIRECT/RELAY、切网与锁屏恢复、系统撤销、DNS、签名升级和资源清理验收。模拟器及 Go 测试不能代替蜂窝和不同厂商真机；当前不承诺完整桌面能力对齐或固定工期。构建入口与工具链见 [移动端开发](development.md#android-开发与构建)。

## iOS / iPadOS

采用通用 Fyne App、原生系统控制桥和 Swift Packet Tunnel 扩展；扩展通过 C archive
复用 Go clienthost、Service、Manager、RelayBridge 和 DNS 业务。App 只负责命令与展示，
不运行第二个活动连接 Manager；桌面双 daemon 与特权 IPC 不进入移动系统。
平台适配实现 packetFlow、网络计划、网络事件和 Keychain；扩展自身沙盒保存现有 schema 配置，
不引入服务端要求或新远程准入规则。

开发阶段已完成 iPhone 证书重签安装、连接与恢复测试，并经维护者确认移动端开发自测完成。
Duo 布局在模拟器验证；不据此承诺真实 Duo 或全部 iPad 多窗口行为。当前使用单 UIScene，
不支持多窗口。最低系统构建目标不等于所有 iOS 版本均经真机验证。

发布资产为未签名 IPA，不能直接安装；使用带 Packet Tunnel 权限的配套材料重签。
安装：[中文](ios-installation.zh-CN.md) / [English](ios-installation.en.md)。
最终版本证据见 [验收记录](validation.md)，构建见 [开发文档](development.md#iosipados-开发构建与模拟器)。

## HarmonyOS

手机（含不同展开比例的折叠屏）、平板、PC/2in1 使用同一 ARM64 HAP，最低 API20。
官方 CLI/SDK 固定26.0.0.851，采用社区 Go 的固定快照
`b637b8617624655906b737977f50de5280bf7f65`（go1.27.2-hmos-devel），不是官方 Go 平台支持。
使用 `GOOS=openharmony`、cgo、SDK Clang/sysroot 和外部链接；不能用普通 GOOS=linux 替代。
该快照采用 TLSDESC，禁用异步抢占/CPU profiling，不支持 race/MSan/ASan；发布前仍需压力和
生命周期验证。工具选择以 [固定源码说明](https://github.com/ZxillyFork/go-hmos/blob/b637b8617624655906b737977f50de5280bf7f65/doc/openharmony.md)
及 `apps/harmonyos/toolchains.json` 为准，不追随移动分支。

系统 VPN 使用 VpnExtensionAbility 的 fd 与 socket protect，不使用 Linux /dev/net/tun 或 netlink。
设备还需具备 NetManager.Vpn、AssetStore 等能力并完成授权；不支持时明确阻止连接，
不会降级成模拟器模式。智慧屏暂不纳入支持，不声明 tv 设备、不提供 TV 构建或候选包；
后续具备真实测试环境后再评估 API26 系统 VPN、遥控交互与待机恢复。

手机控制中心当前未找到适用于普通三方应用、等价 Android TileService 的公开 API；
PC quickBarManager 与鸿蒙智联设备入口不能替代手机连接开关，故本版不提供该入口。
设备 API 依据[VPN 增强管理](https://developer.huawei.com/consumer/cn/doc/doccenter-references/api/js-apis-net-vpnextension)，
窗口设计依据[PC 适配指南](https://developer.huawei.com/consumer/cn/multidevice/pc/adapt)与
[子窗口指南](https://developer.huawei.com/consumer/cn/doc/HarmonyOS-Guides/subwindow-guide)。

用户已反馈手机/折叠屏/平板/PC 当前测试可用；该反馈不代替发布产物核验、更多网络与长期生命周期验收。

公开分发提供 release 构建模式的未签名 HAP，暂未上架应用市场。用户可尝试使用自己的
实名认证开发者账号，通过 HapSign 或 HAP Resigner 获取设备绑定调试签名并安装，通常需要电脑。
功能已通过云真机调试验证；维护者暂无华为实体设备，跨账号同包名申请 Test Profile 及自行安装
的完整链路尚未验证，不承诺普通设备直接点击 HAP 即可安装。具体步骤见根 README。
