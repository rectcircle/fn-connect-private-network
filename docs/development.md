# 开发、构建与发布

开发需要 Go 1.27；macOS 构建需要 Swift/AppKit 工具链，Python 3 用于资源同步。
包职责见 [架构](architecture.md)，正式发布须同时遵守 [版本契约](versioning-and-compatibility.md) 和 [验收](release-acceptance.md)。

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

- `FnCPN-<version>-arm64-unsigned.pkg`：Apple Silicon macOS 客户端。
- `fncpn-<version>-x86.fpk`：x86_64 fnOS 服务端。
- `fncpn-<version>-arm.fpk`：ARM64 fnOS 服务端。

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
正式发布前仍须完成安装升级、真实网络与 fnOS 的实机验收。

## GitHub 发布与 Homebrew

流水线固定 Go 1.27.1、Xcode 16.4 / 对应 SDK、Node 22.14.0 和 fnpack 1.2.3（下载验证 SHA256），使用 macOS 15 Apple Silicon runner。临时目录按构建随机隔离。
本地首发环境可能不同，发布清单记录实际工具版本；两端同一 commit 不等于构建字节可复现。

1. 更新 `internal/version/VERSION`（例如 `1.0.0-rc.1`）、同步模板、更新 `docs/release-notes.md` 和 CHANGELOG，提交代码。
2. 创建并推送对应 tag。tag 流水线运行检查、构建并上传草稿，不自动公开。
3. 草稿出错可修复、重建及替换；公开 RC 后修复应发 `rc.2`，公开正式版后兼容修复应升 PATCH。
4. 候选验收后，在 workflow_dispatch 指定 tag 并勾选 publish。流水线下载原草稿产物复核并公开，不重新构建；也可使用本地已验收的原始产物运行 `python3 scripts/publish-release.py dist --publish`，避免将另一批未经确认的构建视为已验收。
5. 发布脚本下载远端所有资产逐字节复核后公开；已公开版本只允许校验一致的幂等重试，不覆盖。
6. `python3 scripts/update-cask.py <version>` 从公开发布清单生成 `Casks/fncpn-rc.rb` 或 `Casks/fncpn.rb`；稳定版 Cask 不跟随 RC 更新。提交并推送生成文件。

PKG Cask 安装使用系统 installer，卸载先运行项目脚本做网络清理，再清理 package receipt；默认保留用户配置与凭据。
不配置自动 zap/purge，防止误删其他用户数据。RC 与正式 Cask 互斥。
首次正式 `1.0.0` 再归档协议及数据基线；RC 归档不代表正式兼容验收完成。
