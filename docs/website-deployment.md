# FnCPN 产品落地页

源码位于 `website/`，使用原生 HTML / CSS / JavaScript，无 npm 依赖或构建步骤。沿用 acu 的静态网站结构和 Netlify + Cloudflare 部署方案；网站部署与产品 tag、Release、Homebrew 更新独立，不改变产品版本或通信契约。

## 本地预览

```sh
python3 -m http.server 4174 --bind 127.0.0.1 --directory website
```

打开 <http://127.0.0.1:4174>，下载区可直接访问 <http://127.0.0.1:4174/#download>。
下载区采用左右分栏：左侧是全部版本、网盘与捐赠入口，右侧按服务端在上、客户端在下排列；客户端通过 macOS、Windows、Linux、Nix/NixOS Tab 切换。
浏览器识别到 Mac、Windows 或 Linux 时默认激活对应 Tab；无法识别或移动端默认 macOS，
用户仍可手动切换。浏览器不能可靠识别 Linux 发行版，Nix 需手动选择。
连接图和路径切换仅为示意，不建立实际网络连接。Homebrew 复制按钮复制包含 tap 的完整两行命令，Linux 按钮复制下载及执行脚本的完整两行命令，需要 localhost 或 HTTPS 安全上下文；失败时提示从安装文档复制。

平台识别的无依赖测试：`node --test website/download-platform.test.mjs`。

## Netlify

导入此仓库并选择生产分支，使用根目录 `netlify.toml`：

| 设置 | 值 |
| --- | --- |
| Base directory | 留空，仓库根目录 |
| Build command | 留空 |
| Publish directory | `website` |

配置包含 CSP、禁止嵌入、权限限制等响应头，资源每次再验证缓存。`website/404.html` 用于未知路径，不使用 SPA 通配回写。CSP 只允许本站资源，新增外部资源时需要同步检查配置。

## 自定义域名与 Cloudflare

生产域名为 `https://fncpn.rectcircle.cn`。首页 canonical、分享元数据、sitemap 和 robots.txt 已统一使用该地址。

1. 取得 Netlify 的实际生产地址，在 Domain management 中绑定 `fncpn.rectcircle.cn`并设为主域名，不迁移到 Netlify DNS。
2. 在现有 Cloudflare 区域添加 `fncpn` 子域名的 CNAME，目标使用实际 `<site>.netlify.app` 主机名，不包含协议或路径。检查同名记录用途，避免冲突。
3. 初始使用 DNS only（灰云），完成 Netlify 验证及自定义域名 HTTPS 证书签发。
4. 确认源站证书有效后开启 Proxied（橙云），SSL/TLS 使用 Full (strict)。如区域有其他站点，可针对该主机名配置规则。
5. 上线后检查 HTTPS、资源加载、404 状态、下载链接、路径切换和命令复制。若代理影响域名验证或证书续期，暂时切回灰云完成验证。

客户端官网菜单、服务端管理页、fnOS 应用描述、安装器元数据与 Homebrew 主页均使用此官网；发布版本以 GitHub 为发布源，中文下载区另提供夸克网盘永久分享；问题反馈使用 GitHub 对应入口。请在交付这些客户端/服务端更新前确认官网已可访问。

## 维护

- 下载区同时维护 Mac、Windows、Linux 和 Nix 入口；Linux 默认指向 latest 公开版，草稿需要 token，不能让网页自动选择草稿。GNOME 托盘、DNS 前提和包格式矩阵链接到对应文档。
- 内容以根目录 README 和版本兼容文档为依据。平台支持、签名和验证范围变化时同步更新页面。
- 下载链接进入 GitHub Releases；中文页面在 GitHub 或 Homebrew 访问较慢时另提供夸克网盘入口，用户按版本、平台及架构选包，避免网页硬编码产品版本导致过时。
- 后续通过 Netlify Git 部署发布网页修改，无需创建产品版本 tag。
- 本地预览通过不等于生产部署成功；推送后应确认 Netlify 部署与生产页面内容。本文不记录或推断当前 Cloudflare 配置。

参考：[Netlify 配置](https://docs.netlify.com/build/configure-builds/file-based-configuration/)、[外部 DNS](https://docs.netlify.com/manage/domains/configure-domains/configure-external-dns/)、[HTTPS](https://docs.netlify.com/manage/domains/secure-domains-with-https/https-ssl/)、[Cloudflare Full (strict)](https://developers.cloudflare.com/ssl/origin-configuration/ssl-modes/full-strict/)。
