# iPhone / iPad 安装与签名

[English](ios-installation.en.md)

FnCPN 从 1.1.5 起提供 iOS/iPadOS ARM64 未签名 IPA。项目构建目标为 iOS 15.0 起，
目前真机验证以维护者设备为限，具体范围见[验收记录](validation.md)。

## 为什么不能直接安装

Apple 对真机代码签名和系统能力授权有严格要求。维护者目前没有 Apple Developer Program
付费订阅，因此本项目没有 App Store 或 TestFlight 分发，只发布未签名 IPA，不能直接点击安装。

FnCPN 使用 Packet Tunnel，不是普通网页应用。签名材料必须包含
`com.apple.developer.networking.networkextension` 的 `packet-tunnel-provider` 权限；
App 与内嵌的 PacketTunnel 扩展都必须得到正确授权。普通免费账号签名流程不能作为本项目
的可用安装方案，仅能签名普通 IPA 的服务也未必能运行 FnCPN。
参见 [Apple Network Extension 权限](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.developer.networking.networkextension)
和[设备开发分发](https://help.apple.com/xcode/mac/current/en.lproj/dev31de635e5.html)。

## 已验证的证书服务

没有自己的付费开发者团队时，可使用按设备 UDID 注册的开发/测试签名证书服务，俗称
“超级签名”。购买的是设备注册与签名材料服务，不是你自己的 Apple 开发者账号订阅。

| 服务 | 维护者测试时价格 | 本项目验证 |
|---|---|---|
| [DxSign](https://dxsign.cc/) | US$2 | 证书重签、真机安装及连接已验证 |
| [KravaSign](https://www.kravasign.com/) | US$9.90 | 证书重签、真机安装及连接已验证 |

维护者与 DxSign、KravaSign 均无利益关系；列出这两家服务仅用于说明本项目的实测结果。

价格是维护者的测试记录，不是当前报价或永久承诺，购买时以平台套餐为准。KravaSign
公开说明包含 VPN 权限；仍需检查实际交付的描述文件是否含上述 entitlement。
其他平台是否可用尚未验证；淘宝可能有更便宜的商家，但维护者并未验证。

证书受 Apple 和平台的有效期、设备绑定与撤销规则约束。平台不是 Apple 官方服务，
项目不代售或保证证书寿命。购买前确认支持 Network Extension、你的设备 UDID、P12 私钥及
配套描述文件下载。不得把签名私钥或密码提交仓库、上传 Issue 或附进诊断 ZIP。

## 准备

需要 Mac，完整 Xcode（安装命令使用 `devicectl`）、Python 3、OpenSSL 和系统签名工具。
手机需开启开发者模式（开发签名需要）、解锁并信任 Mac。首次建议 USB 连接；
无线安装需要先完成设备配对，并保持同网可达。

1. 按平台步骤获取设备 UDID 并购买对应设备的证书。Device Hub 设备信息可核对 UDID。
2. 下载匹配的开发证书 `.p12`、`.mobileprovision` 和 P12 密码。KravaSign 同时提供多套材料时，
   使用配套的 Development 文件，不混搭开发/分发材料。
3. 从 [GitHub Releases](https://github.com/rectcircle/fn-connect-private-network/releases)
   下载 `fncpn-<version>-ios-arm64-unsigned.ipa`，按同版本 SHA256SUMS 核对文件。
4. 获取本仓库与 IPA 对应版本的源码，用其中的 `scripts/sign-ios.sh`。

签名材料保存在仓库外，例如：

```text
~/.ios/signing/kravasign/
  password
  Development.p12
  Development.mobileprovision
```

`password` 是只包含密码的一行文件。用本地编辑器创建，勿把密码写入命令历史。
目录权限设为 `700`，文件为 `600`。

## 签名

在仓库根目录执行，替换实际版本和文件路径：

```sh
bash scripts/sign-ios.sh \
  ~/.ios/signing/kravasign/password \
  ~/.ios/signing/kravasign/Development.p12 \
  ~/.ios/signing/kravasign/Development.mobileprovision \
  ~/Downloads/fncpn-1.1.5-ios-arm64-unsigned.ipa \
  ~/Downloads/fncpn-1.1.5-ios-arm64-signed.ipa
```

DxSign 使用相同命令，替换签名材料路径即可。脚本检查证书匹配、描述文件有效期与
Packet Tunnel 权限，使用临时钥匙串，完成后清理；输出文件已存在时拒绝覆盖。

脚本复现本项目已验证的单描述文件个人设备重签流程，自动设置 App 标识及其
`.PacketTunnel` 扩展标识；不是通用 App Store 导出工具，也不保证适用于任意平台材料。
签名校验成功不代表设备已经安装或 VPN 权限已经验证。脚本不自动安装。

## 安装与打开

```sh
xcrun devicectl list devices
```

从结果取设备 Identifier，替换下面的 `DEVICE_ID`。先把已签名 IPA 解压到新的临时目录：

```sh
install_dir=$(mktemp -d "${TMPDIR:-/tmp}/fncpn-install.XXXXXX")
ditto -x -k ~/Downloads/fncpn-1.1.5-ios-arm64-signed.ipa "$install_dir"
xcrun devicectl device install app --device DEVICE_ID "$install_dir/Payload/FnCPN.app"
/usr/libexec/PlistBuddy -c 'Print CFBundleIdentifier' "$install_dir/Payload/FnCPN.app/Info.plist"
```

将最后输出的实际 Bundle ID 替换到启动命令中（重签后不一定是原项目标识）：

```sh
xcrun devicectl device process launch --device DEVICE_ID YOUR_SIGNED_BUNDLE_ID
rm -rf "$install_dir"
```

也可在 Device Hub 中选择已配对设备，安装解压后的 App 并启动。按 iOS 提示信任开发者、
开启开发者模式或批准 VPN 配置；具体出现哪些提示取决于证书类型和系统版本。
登录后检查连接状态，再用 Safari 访问 NAS，确认不只是 App 能打开而是隧道实际可用。

## 升级与常见问题

- 覆盖升级沿用同一团队、App 标识及配套权限，通常保留沙盒数据。换团队/标识可能变成另一份 App，
  Keychain 不保证继承；保留旧 App，确认新安装成功后再处理旧版本。
- 提示缺少 `packet-tunnel-provider`：材料不满足本程序要求，联系服务方取得正确权限；不要删除扩展或剥离权限凑成安装包。
- 签名成功但安装失败：核对 UDID 是否登记、描述文件及证书是否有效、手机是否解锁并信任 Mac。
- App 可打开但 VPN 失败：检查扩展授权、系统 VPN 许可和诊断日志；普通 IPA 签名成功不能证明 Packet Tunnel 可运行。
- 证书过期或被撤销：按服务方流程更新材料并重新签名，不需要重新编译源码。

构建、模拟器和发布流程见[开发文档](development.md#ios-开发环境准备)。
