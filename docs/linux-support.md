# Linux 客户端支持

目标范围为 Debian 12 起、Ubuntu 22.04 起，以及仍受上游支持的 Fedora、Arch Linux、
openSUSE 常规系统。统一产品版本为 1.1.2，遵守 [版本与兼容性契约](versioning-and-compatibility.md)。
以下是**构建与验收目标**，不是已经通过完整实机验收的声明。

## 支持矩阵与生命周期

清单核对日期：2026-10-21。唯一机器可读清单是 `packaging/linux/targets.json`，
CI、依赖、产物名称和发布完整性校验共同读取它。

| 系列 | 当前列入目标的版本 | 架构 | 安装包 |
| --- | --- | --- | --- |
| Debian | 12、13 | amd64、arm64 | deb |
| Ubuntu | 22.04、24.04、26.04 LTS；26.10 | amd64、arm64 | deb |
| Fedora | 43、44 | amd64、arm64 | RPM |
| Arch Linux | 当前完整更新的 rolling 系统 | amd64 | pkg.tar.zst |
| openSUSE Leap | 16.0 | amd64、arm64 | RPM |
| openSUSE Tumbleweed | 当前完整更新的 rolling 系统 | amd64、arm64 | RPM |

共 21 个发行版/架构验证组合，共享 **10 个 Release 安装包**：deb 4 个、RPM 4 个、Arch 2 个。
每种格式/架构提供 Core/UI 双包，不按发行版分别发布。Nix/NixOS 通过 Flake 源码构建，
支持 x86_64-linux、aarch64-linux，不增加 Release 二进制资产；详见 [Nix](nix.md)。
Alpine/apk 不支持。Arch Linux ARM 是独立项目，
不在本清单中。Fedora Atomic、openSUSE Aeon/Kalpa/MicroOS 等不可变系统需要另行设计
系统组件安装与更新，不直接沿用普通 RPM 安装支持声明。

Debian/Ubuntu 的“起”表示技术基线；已经结束上游支持的中间版本不作为持续验收目标。
新正式版本发布、旧版本 EOL 或滚动发行版 ABI 变化时，维护者应重新核对上游生命周期并
更新清单、依赖和 CI，再宣布支持；不使用通配版本自动承诺未来系统。
Fedora 45、Leap 16.1 在本次核对时尚未正式发布，未混入稳定矩阵。

核对依据：[Ubuntu 生命周期](https://ubuntu.com/about/release-cycle)、
[Ubuntu 26.10 发布说明](https://documentation.ubuntu.com/release-notes/26.10/)、
[Fedora 生命周期](https://docs.fedoraproject.org/en-US/releases/lifecycle/)、
[Fedora 45 发布决策日程](https://calendar.fedoraproject.org/list/Fedora%20release/)、
[Leap 15 EOL](https://news.opensuse.org/2026/04/01/leap-15-eol/)、
[Leap 16.1 RC 进展](https://news.opensuse.org/2026/09/24/leap-heads-into-release-candidate-phase/)。

## 运行方式

| 组件 | 身份与职责 |
| --- | --- |
| `fncpn-core` | 纯 Go CLI；用户 daemon 负责身份、连接意图、协议、诊断和现有 IPC |
| `fncpn-client.service` | systemd 用户服务，普通用户运行 |
| `fncpn-client-privileged@<uid>.service` | root 系统服务，每个用户独立 socket、状态目录及授权 |
| `fncpn-ui` | Wails v2.17.0、GTK3、WebKitGTK 4.1、Ayatana AppIndicator |

UI 沿用通过官方 Wails CLI vanilla 模板创建的 `ui/`，前端仅 HTML、CSS、JavaScript，
Vite 用于构建。通过已有 Unix socket IPC 调用 Core，不复制连接逻辑。
Linux 使用系统窗口装饰、SVG 功能图标和托盘；macOS 保留原生窗口与 SF Symbols。
管理后台在独立 GTK WebKit 窗口中打开，不注入 Wails Go 绑定。
退出 Linux UI 保留独立运行的 Core；关闭主窗口隐藏到托盘。
托盘是否显示依赖桌面环境的 AppIndicator 支持；GNOME 扩展的安装、启用和重新登录步骤见
[系统托盘故障排查](troubleshooting.md#linux系统托盘--顶栏没有-fncpn-图标)。

普通用户首次启动 UI 时，通过 pkexec 授权固定的 `enable-client <uid>` 安装入口，
启用对应 root 服务，再启动用户服务。CLI/无头用户可运行 `fncpn-linux-setup`，
通过 sudo 完成同样的显式授权；不会全局免密授权任意网络命令。

| 数据 | 默认路径 |
| --- | --- |
| 用户配置 | `${XDG_CONFIG_HOME:-~/.config}/fncpn/config.json` |
| 用户状态与日志 | `${XDG_STATE_HOME:-~/.local/state}/fncpn/` |
| 用户 IPC | `${XDG_RUNTIME_DIR:-/run/user/<uid>}/fncpn/client.sock` |
| 特权 IPC | `/run/fncpn/<uid>/privileged.sock` |
| 特权恢复日志 | `/var/lib/fncpn/client/<uid>/` |
| 特权运行日志 | `/var/log/fncpn/client-privileged-<uid>.log` |

只接受绝对 XDG 路径。用户服务由用户登录会话启动；需要注销后持续运行的无头部署，
由管理员自行决定是否配置 systemd linger。UI 登录自启使用用户 XDG autostart 文件。

## 网络和 DNS

Core 在 macOS 上使用 `CGO_ENABLED=0 GOOS=linux GOARCH=...` 交叉编译。
Linux Client Engine 直接使用内核 WireGuard、netlink 与 wgctrl，不依赖 wg/nft 命令。
仅添加本客户端需要的私有地址与路由，不开启转发、不修改主机防火墙。
每个用户接口名独立；接口别名包含随机所有权 token，恢复日志独立版本化。
拒绝覆盖已有异主接口、路由及未来 schema；恢复和断开只清理自身资源。
进程持有状态目录文件锁，防止第二个特权进程恢复仍在运行的实例。

分域 DNS 依赖 **systemd-resolved 及其 stub resolver**。现有用户 DNS 服务继续监听
非特权 loopback 端口；root 服务在专有 dummy 接口地址上提供 UDP/TCP 53 受限转发，
只接受自身/loopback 来源、只转发产品域名。通过 resolve1 D-Bus 配置该接口的 route-only
域名、DNS 和 `DefaultRoute=false`，租约存活期间重新确认设置。
删除自有接口同时移除对应 resolved per-link 配置。

项目代码不改写 `/etc/resolv.conf`、物理网卡 DNS 或 NetworkManager 设置。
若系统没有使用 `127.0.0.53` stub，DNS 会明确降级，网络服务仍可用。
安装 `systemd-resolved` 依赖时，Debian 自身维护脚本可能调整 resolver；管理员应核对
`/etc/resolv.conf` 与原网络管理器的集成，不能假定安装包自动兼容所有 DNS 布局。

## 自动安装

用户入口为 `scripts/install-linux.sh`，执行命令见 [README](../README.md#linux自动安装)。
默认自动选择桌面双包或无头 Core，支持 `--type all|core` 和 `--version latest|版本`。
它通过 GitHub Release API 选择精确资产并校验 SHA256，遵守系统包签名策略；不自动降级，
已有 UI 时要求组件成套更新。latest 只取公开正式版，指定草稿需要有读取权限的 token。
脚本安装软件包，不替用户开启登录自启、修改 DNS 布局或自动创建 NAS 连接。

## 构建与安装

```sh
# macOS 或 Linux：Core 可交叉构建，依赖 Go 与 nfpm v2.40.0
TARGET=deb ARCH=arm64 scripts/build-linux-core.sh
TARGET=rpm ARCH=amd64 scripts/build-linux-core.sh
TARGET=archlinux ARCH=amd64 scripts/build-linux-core.sh

# 基线容器、对应架构：deb 使用 Ubuntu 22.04；RPM 使用 openSUSE Leap 16.0；Arch 使用 rolling
TARGET=deb ARCH=amd64 scripts/build-linux-core.sh
TARGET=deb ARCH=amd64 scripts/build-linux-ui.sh
python3 scripts/check-linux-packages.py dist/linux/deb/amd64
```

`TARGET` 为 `deb`、`rpm` 或 `archlinux`，缺省为 `deb`。UI 构建检查 `/etc/os-release`，必须与清单中的构建基线一致，
避免把新系统链接的二进制错误标成旧系统产物。所有目标继续使用 GTK3、WebKitGTK 4.1、
Ayatana AppIndicator 和 `wails build -tags webkit2_41`；Ubuntu 22.04 的更新仓库已有
[WebKitGTK 4.1](https://packages.ubuntu.com/jammy-updates/libwebkit2gtk-4.1-dev)，
需要启用相应官方仓库并安装更新，不必另建 WebKitGTK 4.0 UI。

Debian 12/Ubuntu 22.04 使用 `libgtk-3-0`，较新 deb 目标使用 `libgtk-3-0t64`；
Ubuntu 22.04 的 resolved 随 systemd 提供，polkit 使用 `policykit-1`，其他目标依各自包名。
通用 deb 声明新旧包名的替代依赖；通用 RPM 使用文件能力与 ELF SONAME/符号版本，
避免绑定 Fedora/openSUSE 不同的图形库包名。Arch 要求完整系统升级，不支持部分升级。
UI 的实际 ELF 依赖由构建后的二进制提取：deb 使用 `dpkg-shlibdeps`，RPM 使用 SONAME 和
符号版本，Arch 记录实际 glibc 最低符号版本。不能只凭相同 GTK 包名推断 ABI 兼容。
开发依赖只安装到临时 CI 容器，不安装到验证虚拟机。

输出位于 `dist/linux/<format>/<arch>/`：
`fncpn-{core,ui}-<版本>-linux-<arch>.{deb,rpm,pkg.tar.zst}`、
`build-manifest.json`、`SHA256SUMS`。后缀取决于对应包管理器。
UI 精确依赖同产品版本 Core，本地组件成套更新。

以下安装入口仅供经批准后的验证和部署阶段使用；先从受信来源校验包与校验和。

```sh
# Debian / Ubuntu：同时安装同版本的两个包
sudo apt install ./fncpn-core-*.deb ./fncpn-ui-*.deb
# Fedora
sudo dnf install ./fncpn-core-*.rpm ./fncpn-ui-*.rpm
# openSUSE
sudo zypper install ./fncpn-core-*.rpm ./fncpn-ui-*.rpm
# Arch Linux（先完整更新系统）
sudo pacman -Syu
sudo pacman -U ./fncpn-core-*.pkg.tar.zst ./fncpn-ui-*.pkg.tar.zst
# CLI 用户首次启用服务；UI 用户也可使用图形管理员授权入口
fncpn-linux-setup
```

草稿 RPM 尚未签名，不将关闭包签名检查作为正式安装方案；签名、公钥分发与相关发行版的
安装验收仍须在公开发布前完成。SELinux 系统安装脚本恢复标准文件标签，不禁用强制访问控制；
SELinux/AppArmor 强制模式、WebKit 沙箱与各桌面会话须在真实目标系统验收。

包维护脚本按 dpkg、RPM、pacman 各自的参数和升级阶段生成，不能共用 dpkg 参数判断。
升级前停止 UI、用户 daemon 与特权服务，升级后恢复此前运行的用户及独立 root 服务；
RPM 在 post-transaction 阶段恢复，旧包升级移除阶段不停止新服务。
卸载保留身份、配置和连接意图；服务正常停止负责清理当前运行资源。
不包含开发期 0.x 迁移，也不会自动清空用户数据。

## CI 与发布边界

`.github/workflows/build-linux.yml` 将构建与兼容检查分开：5 组基线容器原生构建 Core/UI，
执行 Go/UI 单测、静态检查及包检查。21 组目标容器下载同一份对应格式/架构的包，
模拟依赖事务、解包执行 Core version，并检查 UI 动态库链接；不启动 systemd 或 GUI。
每组保存目标、commit、包哈希及检查项目的证据，不能用重新构建的包替代兼容检查。
并行上限为 4。开发工具只进入 CI 容器。

`check.yml` 复用该流程和 `check-nix.yml`；Nix 在两个 Linux 架构构建 Core/UI 并验证模块求值，
不上传额外 Release 包。手动运行构建 workflow 只上传 Actions artifacts。
统一 `release.yml` 等待 Linux 和 Nix 检查，与 macOS、Windows、fnOS 产物汇总。
从 1.1.2 起发布清单要求全部 10 个 Linux 包来自同一版本、同一干净 commit，
且 21 组兼容性证据必须指向这些相同包的哈希；缺少组件、目标证据或哈希不一致即阻止发布清单。
发布仍创建 GitHub Release 草稿，公开产物不可覆盖。
草稿用于安装验证，不自动公开；用户验收后再决定是否发布。

## 已验证及待验证

截至 2026-10-21，已完成：

- macOS 交叉编译 Core；Debian 13 arm64 执行预编译单测及隔离网络命名空间中的真实
  WireGuard、路由、恢复、所有权保护和受限 DNS bridge 能力测试。该 DNS 能力测试模拟 resolve1，
  本身不能证明宿主 resolver 集成。可重复运行 `scripts/test-debian13.sh`，不在虚拟机安装开发环境。
- GitHub CI 原生构建 5 组 Core/UI 双包，21 组目标容器检查同一份对应安装包的依赖解析、
  ELF、Core 版本及 UI 动态链接；Nix 在两个 Linux 架构构建 Core/UI 并检查模块。
- Debian 13 arm64 安装草稿 deb，检查桌面入口、polkit、服务及动态库。首次安装 resolved 后，
  通过重载 D-Bus、重启 resolved、刷新 NetworkManager DNS 集成恢复现有 DHCP DNS；
  此处是该虚拟机的安装验证记录，不代表所有 DNS 布局均能自动集成。
- 定位并修复自有 `fnc`/`fnd` 虚拟接口触发物理网络变化的反馈循环；临时修复版在虚拟机中
  验证 LOCAL 与 DNS ready 稳定、断开后 PAUSED 与 DNS inactive 保持、自有 DNS 接口清理。
  GitHub 草稿重建与覆盖安装须再次核对 commit 和校验和。
- Linux 包维护脚本、安装脚本、发布完整性与哈希证据测试，以及 Go/UI 单测、vet、前端测试/构建
  和 workflow 静态检查。打包单测中的 UI fixture 不视作实际 UI 构建证明。

仍须完成其他发行版的实机安装/升级/卸载、桌面托盘、协议唤起、DNS 与数据保留验收；
GNOME 扩展安装后需要重新登录，不能把“已安装扩展”当作“图标已在当前会话验收”。
完整 fnOS 连接端到端与正式历史版本兼容验收仍按项目发布契约另行完成。

### 草稿构建的发行版兼容性门禁

普通 push/PR 只运行 Linux 单测、脚本和前端检查；Nix 只进行求值。
完整 Linux 构建与发行版兼容性矩阵、Nix 原生构建及 UI 启动验证，由
Release 的草稿构建流程执行。不会增加定时任务；不会为每个发行版重新编译一份包。

每个包族在既定基线中构建一次，`linux-build-manifest.py` 记录 ELF ABI：
Core 必须没有动态加载器和动态库依赖；UI 不得包含 RPATH/RUNPATH 或依赖
`GLIBC_PRIVATE`。每个目标环境用自身的动态加载器和库校验同一包，逐项匹配所需
版本符号（包括 GLIBC、GLIBCXX、CXXABI），并执行 `ldd -r`。这比只比较 glibc
版本数字更全面，也会发现库缺失、SONAME 或其他动态符号不兼容。

`linux-compatibility.py` 在一次性目标容器中实际安装 Core，再成套安装 UI。
它不提前安装图形库，以验证原生包依赖；Core 单独安装不得引入 GTK/WebKit。
完成动态链接检查后才安装 Xvfb、字体等测试工具。随后以普通用户、独立 D-Bus
会话和虚拟 X11 显示启动包内的 `fncpn-ui --smoke-test /absolute/result.json`。
程序使用正式内嵌前端和真实 WebKit，前端渲染并调用现有 `Ready()` 桥接后写出
版本与 commit 证据并退出。该模式不启动守护进程或发起 polkit 授权，不需要 NAS。
最后执行同版本重装和卸载，验证维护脚本与可执行文件清理。

测试不关闭 WebKit 沙箱。兼容性容器允许 WebKit 创建沙箱命名空间；Nix 的临时
runner 允许其 Nix store 中的 bubblewrap 创建用户命名空间。Xvfb 使用软件渲染。
该测试不能替代 GNOME/KDE 托盘、Wayland、实际授权弹窗或不同发行版内核的验收。

成功与失败均上传 `linux-compat-*` 报告，包含镜像标签、OS 信息、已安装包版本、
ABI 基线及目标库路径、发布包 SHA256、安装日志和 UI 日志。镜像标签及软件仓库
会更新，报告记录的是当次环境，不宣称可凭标签重建完全相同的系统。
所有目标的检查、commit、版本与包校验和必须匹配且通过，才允许生成草稿发布资产。
Nix 两种架构另行验证各自构建的包与前端启动；不套用系统发行包的 RPATH 限制。
