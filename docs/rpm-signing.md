# RPM 签名与密钥维护

FnCPN 的 Core/UI RPM 使用项目专用 OpenPGP v4 RSA-4096 密钥和 SHA256 签名。
不需要购买证书或付费签名服务。Debian、Arch、macOS 和 Windows 包不使用此 RPM 签名。

## 公钥与首次安装

公钥：[RPM-GPG-KEY-fncpn](../packaging/linux/keys/RPM-GPG-KEY-fncpn)。完整指纹：

```text
B2AA E111 998B BE82 DECC 9F10 45BB E575 8B86 1A6A
```

自动安装脚本从所选版本 tag 读取公钥，先核对脚本固定的公钥 SHA256，再在只包含该
公钥的临时 RPM 数据库中验证所有待安装包。全部通过后才将公钥导入系统，保持原生
包管理器签名策略；Fedora 的本地包安装显式启用 `localpkg_gpgcheck=True`。
旧的未签名草稿 RPM 不满足此条件，应改用签名后的草稿资产。

手动安装时，从可信来源下载公钥，并核对完整指纹后执行：

```sh
# 有 GnuPG 的环境可先查看完整指纹，与上方记录比对
# 不要仅比较短 key ID
gpg --show-keys --with-fingerprint RPM-GPG-KEY-fncpn
sudo rpmkeys --import RPM-GPG-KEY-fncpn
rpmkeys --checksig --verbose ./fncpn-core-*.rpm ./fncpn-ui-*.rpm
# 然后使用 dnf 或 zypper 成套安装同版本 Core/UI
```

必须同时有有效签名和有效内容摘要；只有 `digest: OK` 不能证明包已签名。
公钥和 RPM 可公开分发，签名私钥与口令不进入仓库、日志或发布资产。

## 草稿构建

`release.yml` 将 `RPM_SIGNING_PRIVATE_KEY`、`RPM_SIGNING_PASSPHRASE` 两个 Actions
Secrets 传递给 Linux 构建流程。仅 RPM 发布构建的签名步骤接收它们；普通 PR、其他格式
和未开启发布模式的手动构建不会导入私钥。缺少 Secret、指纹不符或验签失败时直接失败。

`rpm_signing.py` 使用独立临时 GnuPG 目录，通过 stdin 导入私钥，签名后关闭临时 agent
并删除目录。口令只通过权限受限的临时文件传给 GnuPG，不放在命令行参数中。
上传目录是 `dist/linux/`，不包含签名临时目录。

流程顺序为：构建 Core/UI → 签名 RPM → 隔离公钥验签 → 生成构建清单和 SHA256 →
在 Fedora/openSUSE 安装同一份签名包 → 统一发布清单。
兼容性测试不再允许未签名本地 RPM，证据包含 `rpm-signature` 和公钥指纹；发布清单
拒绝缺少签名证据或使用其他公钥的候选。发布后不得重新签名覆盖旧包。

`rpm_signing_test.py` 覆盖摘要成功但未签名、签名/摘要失败、缺少 Secret，并在 RPM
签名 CI 中使用实际包验证正常签名、移除签名、篡改内容和错误公钥。

## 本地密钥与恢复

维护者的密钥保留在默认 `~/.gnupg/`，私钥位于 `private-keys-v1.d/`，吊销证书位于
`openpgp-revocs.d/`。本次自动配置的随机口令保存在 `~/.gnupg/fncpn-rpm-passphrase`，
文件权限为 `0600`；不要将该目录或口令文件加入项目、同步日志或公开分享。
用 `gpg --list-secret-keys --with-fingerprint` 查看密钥及到期时间。

GitHub Secrets 是 CI 使用副本，网页/API 不提供原值下载。必要时可另外编写仅手动触发、
经过审核的恢复 workflow，将 Secret 用维护者临时提供的公钥加密后输出密文；不能输出
明文私钥。当前没有创建恢复 workflow，也没有将私钥备份到仓库或 Release。

密钥续期或轮换前，更新公开公钥、指纹文件、安装脚本固定的公钥哈希和对应 Secrets，
同步说明旧包的信任方式并重新验证。不要删除仍用于验收历史包的公钥。
失去私钥和 Secret 副本后无法从公钥重建私钥；泄露时应使用吊销证书并更换密钥。

参考：[RPM 签名](https://rpm.org/docs/4.20.x/man/rpmsign.8)、
[RPM 公钥管理](https://rpm.org/docs/4.20.x/man/rpmkeys.8)、
[GnuPG 密钥管理](https://gnupg.org/documentation/manuals/gnupg/OpenPGP-Key-Management.html)。
