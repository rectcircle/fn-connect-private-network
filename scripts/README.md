# 脚本入口

详细参数、环境要求、命令与发布步骤统一见 [开发、构建与发布](../docs/development.md)。

| 用途 | 常用入口 | 说明 |
| --- | --- | --- |
| macOS / fnOS | `build-all.sh`、`build-macos-pkg.sh`、`build-fpk.sh` | [构建安装包](../docs/development.md#macos-与-fnos-构建安装包) |
| Windows | `setup-windows.ps1`、`build-windows.ps1`、`check-windows.ps1` | [开发与构建](../docs/development.md#windows-开发与构建)，内部辅助位于 `windows/` |
| Linux | `install-linux.sh`、`build-linux-core.sh`、`build-linux-ui.sh` | [构建与部署](../docs/development.md#linux-构建与部署) |
| Nix | `update-nix-go-hashes.sh` | [依赖维护](../docs/development.md#nix-flake-与-nixos) |
| Android | `setup-android-macos.sh`、`android/prepare.py`、`android/notices.py`、`android/verify_apk.py` | [环境初始化、构建与 CI](../docs/development.md#android-开发与构建)，打包入口是 `mobile/android/` |
| 检查 | `check-release.sh`、`test-fnos-state-permissions.sh`、`fnos_systemd_test.py`、`test-fnos-systemd-linux.sh`、`linux-compatibility.py` | [测试](../docs/development.md#测试)，平台检查还见对应章节 |
| 资源 | `sync-version.py`、`sync-localizations.py`、`sync-native-localizations.py`、`generate-icons.swift` | [版本](../docs/development.md#产品版本与正式发布准备)与[翻译](../docs/development.md#中英文资源) |
| RPM | `rpm_signing.py`、`rpm_signing_test.py` | [签名与密钥维护](../docs/development.md#rpm-签名与密钥维护) |
| 发布 | `release-version.sh`、`release-manifest.py`、`publish-release.py`、`quark-release.py`、`update-cask.py` | [发布与镜像](../docs/development.md#github-发布与-homebrew) |
