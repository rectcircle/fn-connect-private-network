# Nix Flake 与 NixOS

Linux 目标为 `x86_64-linux`、`aarch64-linux`。Flake 提供 `core`、`ui`、`frontend` 包、
`core`/`ui` app、overlay、开发 shell 和 `nixosModules.default`。
默认包为无头 Core；UI 使用 Wails v2、原生 HTML/CSS/JavaScript。
依赖由 `flake.lock` 锁定；Go vendor hash 和 npm lockfile 固定依赖内容。
不额外发布 Nix Release 二进制包。首次构建通常在本地编译项目；有受信缓存时可复用缓存，
目前不配置项目自己的二进制缓存。

```sh
# 仓库内（新文件须纳入 Git，Git Flake 不包含未跟踪文件）
nix build .#core
nix build .#ui
nix develop
nix flake check
```

单独 `nix run .#ui` 或安装包不会向宿主注册 root 服务、用户服务或 polkit 策略。
NixOS 使用下方模块；其他 Linux 使用其原生安装包进行宿主集成。
NixOS 模块不使用 deb/RPM 的命令式安装维护脚本，也不写 `/usr/bin`。

## NixOS 配置

把此仓库作为已提交、可访问的 Flake input（选择明确 tag 或 commit），然后：

```nix
{
  imports = [ inputs.fncpn.nixosModules.default ];
  users.users.alice = { isNormalUser = true; uid = 1000; };
  services.fncpn = {
    enable = true;
    users = [ "alice" ];
    ui.enable = true; # 无头机器设为 false
    dns.enable = true;
  };
}
```

`users` 必须明确列出已有普通用户及稳定 UID（至少 1000）；模块只配置这些 UID 的特权实例。
模块安装匹配版本的 Core/UI、polkit 策略和 systemd 用户服务，系统启动时启用各用户独立的
root 实例，登录时启动用户 daemon。UI 自启仍由用户设置；注销后保留用户服务可自行配置 linger。
UI 授权入口只启动已声明实例，不再执行 `systemctl enable`。

默认启用 systemd-resolved。应用模块前应检查宿主 DNS 配置；分域 DNS 仍要求
`/etc/resolv.conf` 使用 `127.0.0.53` stub。`dns.enable = false` 不更改宿主 resolver，
未满足 stub 条件时明确显示 DNS 降级。模块不修改物理网络、转发或防火墙。

数据路径与普通 Linux 包相同，详见 [Linux 支持](linux-support.md)。删除模块配置不会删除
身份、配置和连接意图。NixOS generation 回滚程序并不回滚持久化数据，跨 schema 降级仍须按
[版本契约](versioning-and-compatibility.md) 恢复配套备份，不能假定旧程序读取新数据。
自定义 `package`/`ui.package` 时需使用同版本、同发布 commit 的成套组件。

## 维护和验证

`check-nix.yml` 在普通 push/PR 中只执行 Flake 与模块求值；草稿构建在两个原生 Linux
架构执行 Core/UI 构建、模块检查和虚拟显示中的真实前端启动。
这些检查不代替 NixOS 实机的 polkit、systemd、resolved、桌面和网络验收。
Nix 开发依赖只进入 CI，不在 Debian 验证虚拟机安装 Nix。

更新 Go 依赖或影响 UI vendoring 的根模块源码后，使用 `scripts/update-nix-go-hashes.sh` 重新生成
`nix/go-hashes.json`。该脚本在临时副本执行 `go mod vendor` 并用 Nix 计算 NAR hash；
不修改工作区依赖文件。更新 `flake.lock`、Go、Node 或图形依赖后须重新运行两个架构的检查。

本地验证记录（2026-10-21）：使用下载到项目 `.dev/` 的官方 Nix 2.31.2，
核验锁定 nixpkgs 源码及两个 vendor 目录的 NAR hash，并对两个 Linux 架构的
Core/UI derivation 和 NixOS 模块检查完成求值。未安装系统级 Nix，未在 macOS 构建 Linux 包。
后续草稿 CI 已通过 Linux 双架构原生构建及前端启动；NixOS 实机集成仍待验收。
