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

macOS 状态栏使用22 × 18 pt 模板图标，自动适配深浅色及菜单选中颜色。应用与状态栏图标都由 `scripts/generate-icons.swift` 中同一个 `connectionMark()` 绘制，使用一致的“内网边界、飞牛标志与接入路径”图形。应用图标采用 fnOS 默认图标风格的浅蓝底与亮蓝前景。所有状态保留内网边界和飞牛标志，以连接线最左侧的端点替换图形表达状态：圆点表示已连接，省略号表示连接中，叉号表示断开，感叹号表示异常或需要登录。局域网、IPv6 直连、中继共用已连接图标，具体连接方式由悬停提示和界面详情说明。生成脚本和生成的资源均保留在仓库中。

正式图标采用圆角矩形表示内网边界，内部复用 `packaging/assets/fnos-original.png` 中的飞牛原始标志，外部连接采用圆角水平 / 垂直折线，并通过边框预留的入口进入内网。原始标志素材与绘图脚本一同保留，重新生成时无需联网。

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
要求干净 checkout、当前 commit 对应 `v<version>` tag，并拒绝覆盖同名产物。
构建生成仅包含当前发布产物的 `release-<version>.json` 和 `SHA256SUMS-<version>`。
发布时应归档安装包、元数据、协议与数据样本，后续兼容验收使用这些历史基线。
正式发布前仍须完成安装升级、真实网络与 fnOS 的实机验收。
