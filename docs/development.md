# 开发、构建与发布

开发需要 Go 1.27；macOS 构建需要 Swift/AppKit 工具链，Python 3 用于资源同步。
包职责见 [架构](architecture.md)，正式发布须同时遵守 [版本契约](versioning-and-compatibility.md) 和 [验收](validation.md)。

## 本地运行

```bash
go run ./cmd/fncpn version
```

需要在本机长期使用 CLI 时安装到 `$GOBIN`：

```bash
go install ./cmd/fncpn
```

## 测试

```bash
go test -race ./...
go vet ./...
node --test internal/server/web/index.test.cjs
```

## 构建安装包

```bash
./scripts/build-all.sh
./scripts/build-macos-pkg.sh
./scripts/build-fpk.sh
```

产物统一写入 `dist/`，打包中间文件使用系统临时目录。

FPK 构建要求通过 `FNPACK` 或 `PATH` 显式提供 `fnpack`，不依赖本地 PoC 文件。
首次初始化默认使用 `10.253.203.0/24` 和 `54789/UDP`；冲突时
服务端保持管理页面可用，由管理员修改后重试。

- `fncpn-<version>-macos-arm64-unsigned.pkg`：Apple Silicon macOS 客户端。
- `fncpn-<version>-macos-x64-unsigned.pkg`：Intel macOS 客户端。
- `fncpn-<version>-fnos-x64.fpk`：x86_64 fnOS 服务端。
- `fncpn-<version>-fnos-arm64.fpk`：ARM64 fnOS 服务端。

### 图标生成

应用图标由 `scripts/generate-icons.swift` 使用 AppKit 程序绘制，macOS 构建时自动生成完整 Retina iconset 和 `AppIcon.icns`，fnOS 包根目录的 `ICON.PNG` / `ICON_256.PNG` 使用同源的 512 像素 PNG，已在 `0.1.52` 实机确认解决应用中心图标模糊问题（官方文档规定分别为 64 / 256，本项目采用实测有效的高清资源）；桌面入口单独保留 64 / 256 像素资源，固定引用 `images/icon_256.png`。macOS 上可运行 `swift scripts/generate-icons.swift packaging/assets` 重新生成；其他平台打包使用已提交的 PNG。

macOS 状态栏使用22 × 18 pt 模板图标，自动适配深浅色及菜单选中颜色。应用与状态栏图标都由 `scripts/generate-icons.swift` 中同一个 `connectionMark()` 绘制，使用一致的“内网边界、存储圆柱与接入路径”图形。应用图标采用 fnOS 默认图标风格的浅蓝底与亮蓝前景。所有状态保留内网边界和存储圆柱，以连接线最左侧的端点替换图形表达状态：圆点表示已连接，省略号表示连接中，叉号表示断开，感叹号表示异常或需要登录。局域网、IPv6 直连、中继共用已连接图标，具体连接方式由悬停提示和界面详情说明。生成脚本和生成的资源均保留在仓库中。

正式图标采用圆角矩形表示内网边界，内部采用项目自行绘制的存储圆柱，外部连接采用圆角水平 / 垂直折线，并通过边框预留的入口进入内网。绘图脚本与生成资源一同保留，重新生成时无需联网。

## 中英文资源

开发与测试需要 Python 3（仅使用标准库，无新增运行时依赖）。
统一文案源为 `localization/messages.json`：键使用中文源文案，英文值为对应翻译；
动态参数使用 `{0}`、`{1}`，不要拼接需要翻译的句子。更新后运行：

```bash
python3 scripts/sync-localizations.py
python3 scripts/sync-localizations.py --check
node --test internal/server/web/index.test.cjs
```

脚本生成 macOS `.lproj/Localizable.strings` 和管理页内嵌词典，并检查缺失翻译和参数一致性。
生成文件需一起提交；macOS 打包会复制两种语言资源。`go test ./...` 包含资源同步检查及
macOS 中英文/回退运行测试；`FNCPN_LAYOUT_CHECK=1 go test ./packaging -count=1` 检查两种语言的窗口布局。

## 产品版本与正式发布准备

唯一产品版本源为 `internal/version/VERSION`；修改后运行
`python3 scripts/sync-version.py` 同步 fnOS manifest 与 macOS plist 模板。
Go CLI 默认构建也会嵌入这个版本，不再报告 `dev`。
远程兼容性只检查 MAJOR 相同且 client MINOR 不大于 server MINOR，PATCH 不参与。
稳定的 `/version` 入口、HTTP/relay 响应版本和带身份校验的局域网探测支持升级后重新检查。
开发期已使用该判定器进行验证，正式兼容性承诺从 `1.0.0` 生效。

普通开发构建允许重建本地产物。正式构建使用 `RELEASE=1 ./scripts/build-all.sh`，
要求干净 checkout、当前 commit 对应 `v<version>` tag，并拒绝覆盖本地同名产物；重建草稿应使用新的 `DIST_DIR`。
构建生成仅包含当前发布产物的 `release-<version>.json` 和 `SHA256SUMS-<version>`。
发布时应归档安装包、元数据、协议与数据样本，后续兼容验收使用这些历史基线。
正式产物需完成安装与基础连接确认；已有验证及暂缓范围见 [验证记录](validation.md)。

## GitHub 发布与 Homebrew

流水线固定 Go 1.27.1、Xcode 16.4 / 对应 SDK、Node 22.14.0 和 fnpack 1.2.3（下载验证 SHA256），使用 macOS 15 Apple Silicon runner。临时目录按构建随机隔离。
本地首发环境可能不同，发布清单记录实际工具版本；两端同一 commit 不等于构建字节可复现。

1. 更新 `internal/version/VERSION`（纯数字 `MAJOR.MINOR.PATCH`）、同步模板、更新 CHANGELOG，提交代码。
2. 创建并推送对应 tag。tag 流水线运行检查、构建并上传草稿，不自动公开。
3. 草稿出错可修复、重建及替换；公开后兼容修复应升 PATCH。
4. 候选验收后，推荐运行 `python3 scripts/quark-release.py dispatch v1.0.10`（替换为待发布版本），先检查本地夸克授权并通过 `gh secret set` 同步 Secret，再触发公开发布。仅需 GitHub 发布时仍可运行 `gh workflow run release.yml --ref main -f tag=v1.0.10 -f publish=true`，但必须预先准备夸克 Secret，否则后续镜像 job 会失败。独立的 publish job 下载原草稿产物复核并公开，不依赖构建 job，不安装编译工具、不重建或重新上传资产；也可使用本地已验收的原始产物运行 `python3 scripts/publish-release.py dist --publish`，避免将另一批未经确认的构建视为已验收。
5. Release 说明从 CHANGELOG 对应版本条目提取；尚未登记版本时使用 Unreleased，公开前应完成版本条目。发布脚本下载远端所有资产逐字节复核后公开；已公开版本只允许校验一致的幂等重试，不覆盖。
6. `python3 scripts/update-cask.py <version>` 仅从公开稳定版发布清单生成 `Casks/fncpn.rb`；脚本拒绝非数字版本、草稿和 GitHub prerelease；公开任务自动提交并推送 Cask 更新。提交并推送生成文件。

PKG Cask 安装使用系统 installer，卸载先运行项目脚本做网络清理，再清理 package receipt；默认保留用户配置与凭据。
不配置自动 zap/purge，防止误删其他用户数据。Homebrew 统一标识符为 `fncpn`，只跟随稳定版。
首次正式 `1.0.0` 归档协议及数据基线。

首次正式发布：review 后提交并推送 `v1.0.0`，从该提交重建三种安装包；核对草稿的
版本/build number、固定 App 路径、签名、许可证与 SHA256，完成安装及基础连接确认后公开。
归档正式产物、commit、工具版本和脱敏的协议/数据样本，不归档真实私钥、凭据或 Cookie。
发布前将 CHANGELOG 的准备条目整理为对应版本说明，公开后补记实际发布日期并更新 README 的公开状态；后续修复递增 PATCH。

安装包命名使用小写 `fncpn-<version>-<os>-<arch>[-unsigned].<extension>`，系统为 `macos`、`windows`、`fnos`，架构为 `arm64`、`x64`。fnOS manifest 内的 `x86`/`arm` 平台标识保持不变，仅对外文件名统一。`release-<version>.json` 与 `SHA256SUMS-<version>` 为发布清单和校验和，沿用现有名称。已公开安装包不改名，Homebrew 生成器支持历史文件名。

macOS 发布在 Apple Silicon runner 上构建 arm64 和 x64 两套 PKG，Swift 使用 `arm64`/`x86_64` target，Go 使用 `arm64`/`amd64` 并指定 cgo clang 架构。打包前校验 App 和 helper 的 Mach-O 架构。Intel 原生运行验证仍需 Intel Mac，不以交叉构建代替实机验收。

### 夸克网盘国内镜像

GitHub Release 是发布源。公开任务完成后，Homebrew 更新和夸克镜像作为两个独立 job 执行；
镜像失败不会撤回 GitHub Release 或阻断 Homebrew 更新，Actions 会明确报错。
`mirror-quark.yml` 也接收 GitHub 页面公开事件，并支持手动补传；通过 `GITHUB_TOKEN`
公开不会触发另一个工作流，因此 `release.yml` 显式调用可复用镜像工作流。

本地发布入口（Python 3.11+、Node 16+、已登录的 `gh`）：

```bash
# 草稿构建和验收完成后：检查本地授权、更新 Secret，然后公开并镜像。
python3 scripts/quark-release.py dispatch v1.0.10

# 已公开版本的镜像失败后：更新授权并只重试镜像，不重建或替换 GitHub 资产。
python3 scripts/quark-release.py dispatch v1.0.10 --mode mirror
```

CLI 默认位置是 `~/.local/share/quark-cli/1.0.24/quark-drive.cjs`，本地授权位于同一 CLI 根目录下的
`codex/config.json`（默认 `~/.local/share/quark-cli/codex/config.json`）。可通过 `--cli` 和
`--auth-config` 指定配套路径；首次登录或授权失效时，本地执行 `node <CLI路径> login` 完成授权，
然后重新执行发布入口。发布入口不会自动打开登录，也不会把授权码作为长期 token。

脚本仅导出当前 Codex 账号的 `accessToken`、`refreshToken`、`clientToken`、账号及设备标识，
通过标准输入传给仓库 `QUARK_AUTH` Secret，再触发 `main` 上的 workflow。
发布者需要更新 Actions Secret 及触发工作流的权限；不要将配置放入 workflow input、仓库、日志或 artifact。
同一时间只从一个本地会话发起发布或补传，避免后一次 Secret 更新抢在前一次 job 读取之前。

runner 固定 Node 22.14.0 / Python 3.12，验证夸克 CLI `1.0.24-29460f9` 压缩包 SHA256，
只提取 `quark-drive.cjs` 和 `hash-worker.cjs`；不安装 Skill，不执行官方安装或自动更新脚本。
授权通过 `CODEX_CI=1` 选择 Codex 通道，保存于临时运行目录的 `codex/config.json`（0600），
任务结束清理整个临时目录，不持久化 runner 刷新后的凭据。CLI 内部支持刷新令牌，
但如果服务端刷新使本地旧授权失效，下次发布须在本地重新授权；当前不承诺长期免维护。

镜像流程：

1. 拒绝草稿及 prerelease，从指定公开 tag 下载全部资产；校验清单版本、tag commit、六个平台安装包、
   `SHA256SUMS` 和 GitHub 提供的全部资产 SHA256，不构建或重新打包。
2. 上传到未分享的 `/releases/fncpn-staging/v<版本>/`。重试时回读已有文件，只补传缺失文件；
   同名不同内容、重复名称或额外文件立即失败，不覆盖或删除远端内容。
3. 全部文件从夸克回读验证 SHA256 后，移动版本目录到 `/releases/fncpn/v<版本>/`，已有永久分享自动包含新目录。
   已公开镜像只允许完整校验的幂等重试，不做原地补写或替换。
4. 所有版本共享同一个镜像并发组，避免同时操作同一账号。失败时保留未分享的暂存目录供重试；
   若发现内容冲突，先排查原因，不用自动覆盖绕过错误。

目标目录 FID 固定在 `scripts/quark-release.py`，对应专用发布账号的 `releases` 与 `fncpn`；
切换账号或重建目录时必须同时调整 FID，不能只替换授权。
官网中文下载入口使用永久分享，提取码为公开下载用途，不是登录凭据；英文页面仍使用 GitHub。

当前验证边界：本机已验证最小授权恢复及 1.0.9 全部 8 个文件上传/回读一致；
1.0.10 的 Ubuntu runner 跨机器授权、真实自动镜像和打包验收仍需发布时确认。
