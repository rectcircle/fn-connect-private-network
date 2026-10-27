import {detectDownloadPlatform} from './download-platform.mjs';

const translations = {
  '● 连接示意':'● Connection illustration','macOS / Windows / Linux':'macOS / Windows / Linux','fnOS x86_64 & ARM64':'fnOS x86_64 & ARM64','MIT 开源':'MIT open source',' · fnOS x86_64 & ARM64 · MIT 开源':' · fnOS x86_64 & ARM64 · MIT open source','fnOS 服务端':'fnOS server','x86_64 / ARM64 · 需要启用 FN Connect':'x86_64 / ARM64 · FN Connect required','。':'.','在外，也在网内':'Your network, anywhere','共享 NAS':'Shared NAS','私有网络 · 已连接':'Private network · Connected','本地局域网':'Local network','个人 · 家人 · 团队':'You · Family · Team','内网':'LAN','加密隧道':'Encrypted tunnel','一条连接，回到熟悉的网络。':'One connection brings your familiar network close.','距离变了，访问习惯不必变。':'Your location changes. Your access stays familiar.','NAS 文件':'NAS files','本地局域网':'Local network','原生客户端':'Native apps','自动选路 ↗':'Automatic routing ↗',
  '首页':'Home','产品介绍':'Overview','连接方式':'How it works','开始使用':'Get started','常见问题':'FAQ','下载客户端':'Download','连接 fnOS 所在的私有网络':'Connect to the private network your fnOS NAS is on','无论在哪。':'Wherever you are.','你的网络，':'Your network,','就在身旁。':'is close by.','无论是自己的文件、家人的 NAS，还是团队的共享资源，都能从 Mac、Windows 或 Linux 连接 fnOS NAS 和局域网。无需自建中转服务器，无需为每个服务逐一配置端口暴露，也无需手动配置密钥与路由。让熟悉的访问方式，不受地点限制。':'Access your files, family NAS, and team resources from Mac, Windows, or Linux. Connect to fnOS and your LAN without building a relay, exposing services one by one, or configuring keys and routes by hand.','开始连接':'Connect now','在 GitHub 查看源码 ↗':'View source on GitHub ↗','在外，也在网内':'Your network, anywhere','本地局域网':'Local network','个人 · 家人 · 团队':'You · Family · Team','一条连接，回到熟悉的网络。':'One connection brings your familiar network close.',
  '01 / 为日常而连接':'01 / Made for everyday use','把连接的复杂，':'Connection complexity','留在界面背后。':'stays behind the scenes.','输入 FN Connect ID，用 fnOS 账号授权。':'Enter your FN Connect ID and authorize with your fnOS account.','从设备注册到建立连接，FnCPN 帮你完成。':'FnCPN handles device registration and connection setup.','像在本地一样访问':'Access your network like you are there','用连接详情中的 NAS 地址访问设备，也可访问服务端启用的 LAN 网段。':'Use the NAS address in connection details, or access LAN subnets enabled by the server.','网络变化，自动恢复':'Reconnects as your network changes','网络变化后自动尝试恢复连接。主动断开会暂停自动连接，直到你再次连接。':'FnCPN tries to reconnect when your network changes. Disconnecting pauses automatic reconnection until you connect again.','顺手的原生体验':'A native app experience','Mac 与 Windows 原生客户端，Linux 提供桌面 UI 和无头 Core。桌面端支持简体中文与英文，命令行也能查看状态与控制连接。':'Native Mac and Windows apps, plus a Linux desktop UI and headless Core, in Simplified Chinese and English. Check status and control connections from the command line too.',
  '02 / 连接你的网络':'02 / Connect your network','三种路径。':'Three paths.','同一个目的地。':'One destination.','自动选择局域网直达、IPv6 直连或 FN Connect 中继。':'FnCPN chooses LAN, direct IPv6, or FN Connect relay automatically.','你关注访问，FnCPN 负责连接。':'You get access. FnCPN handles the connection.','局域网直达':'Local network','在同一网络，就走近路。':'On the same network, take the direct route.','IPv6 直连':'Direct IPv6','在外，尝试直接相连。':'Try a direct connection from afar.','FN Connect 中继':'FN Connect relay','无法直连时，多一条路。':'A fallback when direct paths are unavailable.','路径示意':'Connection path','你的电脑':'Your computer','fnOS 官方':'Official fnOS','中转服务器':'relay server','fnOS NAS':'fnOS NAS','就在同一个网络里。':'On the same local network.','电脑与 NAS 在同一局域网时，自动选择局域网直达，使用 NAS 局域网地址访问。':'When your computer and NAS share a LAN, FnCPN connects directly. Access the NAS using its local address.','使用 fnOS 官方的 FN Connect 中继服务，无需手动搭建服务器。可享受 FN Connect 基础版的免费中继权益；带宽权益与付费方案详见':'Use the official FN Connect relay without setting up a server. The FN Connect Basic plan includes free relay access. See','FN Connect 官方页面 ↗':'the official FN Connect page ↗','自动选路':'Automatic routing','无需手动切换网络配置':'No need to switch network settings manually',
  '访问方式 / 做个比较':'ACCESS / A COMPARISON','连接一个网络，':'Connect to your network','不用逐个开放服务。':'without exposing each service.','端口暴露适合向外提供指定服务；FnCPN 适合让自己、家人或团队成员通过授权的电脑访问 NAS 和局域网资源。选择适合访问对象的方式。':'Port forwarding can expose specific services. FnCPN lets you and authorized family or team members access a NAS and LAN from their computers. Choose the approach that fits who needs access.','FnCPN 与直接公网端口暴露的比较':'FnCPN compared with exposing public ports','比较项':'Criteria','FnCPN 私有网络':'FnCPN private network','直接公网端口暴露':'Direct public port exposure','安装与准备':'Setup','NAS 安装服务端，电脑安装客户端；启用 FN Connect 后授权连接。':'Install the server on your NAS and the client on your computer. Enable FN Connect and authorize the connection.','配置路由器或网关；直连通常需要可达的公网地址。经隧道转发则依赖额外服务。':'Configure a router or gateway. Direct access usually needs a reachable public address; tunnel forwarding needs an additional service.','配置与维护':'Configuration and maintenance','无需自建中转、逐个映射端口或手动配置密钥与路由；LAN 访问范围由服务端设置。':'No self-hosted relay, per-service port mapping, or manual key and route setup. The server controls LAN access.','通常需要为各服务配置映射、域名与访问规则，并维护认证、TLS 和防火墙。':'Usually requires service mappings, domains, access rules, and ongoing management of authentication, TLS, and firewall settings.','安全边界':'Security','远程通过加密隧道访问私有地址，减少逐项公开服务入口；仍需保护账号、授权设备及内网服务。':'Remote access uses an encrypted tunnel to private addresses, reducing public service endpoints. Protect accounts, authorized devices, and LAN services.','公开的服务入口可能被扫描和尝试访问，需要持续管理服务更新、认证与访问限制。':'Public endpoints may be scanned and probed. Keep services updated and manage authentication and access controls.','访问对象':'Who can access','适合自己、家人或团队成员，用各自的 fnOS 账号授权 Mac / Windows / Linux 访问 NAS 和已启用的 LAN 网段；每台电脑需要安装客户端。':'For you, family, or teammates. Each person authorizes a Mac, Windows, or Linux computer with their fnOS account to access the NAS and enabled LAN subnets. Each computer needs the client.','适合对外提供指定服务；是否需要客户端取决于服务，例如网页通常可直接用浏览器访问。':'For providing selected services publicly. A client may not be needed; websites, for example, can usually be opened in a browser.','速度与成本':'Speed and cost','自动选路；可享受 FN Connect 基础版的免费中继权益。中继速度受服务权益及网络条件影响，更高带宽等付费权益':'Automatic routing. FN Connect Basic includes free relay access. Relay speeds depend on service entitlements and network conditions. See','以官方服务说明为准 ↗':'the official service details ↗','直连取决于 NAS 所在网络的上行带宽与网络条件；使用第三方隧道或自建中转时，可能有额外费用。':'Direct speeds depend on the NAS network upload bandwidth and conditions. Third-party tunnels or self-hosted relays may cost extra.','私有网络不替代服务自身的安全措施。请按需启用 LAN 网段，并保持 fnOS 与应用更新。':'A private network does not replace service security. Enable only the LAN subnets you need, and keep fnOS and apps updated.',
  '03 / 从这里开始':'03 / Get started','三步，接入你的私有网络。':'Join your private network in three steps.','先安装 NAS 服务端':'Install the NAS server','在 fnOS 应用中心手动安装对应架构的 FPK，打开 FnCPN，确认服务正常运行。':'Install the matching FPK from the fnOS App Center, open FnCPN, and confirm the service is running.','安装电脑客户端':'Install the desktop client','Mac 与 Windows 按架构选择安装包；Linux 使用自动安装脚本，按桌面环境安装 UI 或无头 Core。':'Choose the installer for your Mac or Windows architecture. On Linux, the install script selects the desktop UI or headless Core.','授权，然后连接':'Authorize and connect','输入 FN Connect ID、fnOS 用户名和密码，按提示填写访问码或 OTP 并连接。Linux 客户端从 1.1.2 起提供。密码不会持久化保存。':'Enter your FN Connect ID, fnOS username, and password. Supply an access code or OTP when prompted, then connect. The Linux client is available from 1.1.2. Your password is not stored.',
  '下一站，':'Your next stop,','保持连接。':'always connected.','先安装服务端，再安装客户端。':'Install the server first, then the client.','所有安装包均从 GitHub Releases 获取。':'Download all installers from GitHub Releases.','查看所有发布版本 ↗':'View all releases ↗','自愿赞助，用于开发、测试和基础设施。软件继续免费开源。':'Voluntary support funds development, testing, and infrastructure. FnCPN remains free and open source.','13 或更新版本':'macOS 13 or later','支持 Apple Silicon 与 Intel Mac':'Apple Silicon and Intel Macs','Homebrew 安装':'Install with Homebrew','推荐':'Recommended','在终端执行以下命令，由 Homebrew 下载并安装。':'Run these commands in Terminal to download and install with Homebrew.','复制':'Copy','不使用 Homebrew？也可':'Not using Homebrew?','下载安装包 ↗':'download an installer ↗','，按芯片架构选择 PKG。两种方式任选其一，无需重复安装。':'. Choose the PKG for your processor. Use either method, not both.','详细步骤见':'For detailed steps, see','安装文档':'the installation guide','。PKG 未签名、未公证，App 使用 ad-hoc 签名。':'. The PKG is unsigned and not notarized; the app uses ad-hoc signing.','Windows 10 22H2 或更新版本，不支持 Windows Server。':'Windows 10 22H2 or later. Windows Server is not supported.','选择 Windows 安装包 ↗':'Choose a Windows installer ↗','MSI / EXE 未签名。Windows 10 和原生 x64 已完成验收。':'MSI and EXE installers are unsigned. Windows 10 and native x64 have passed acceptance testing.','需要启用 FN Connect':'FN Connect required','下载 FPK ↗':'Download FPK ↗',
  '04 / 你可能想知道':'04 / FAQ','连接之前，':'Before you connect,','了解多一点。':'learn a little more.','阅读完整使用文档 ↗':'Read the full user guide ↗','故障排查与使用限制 ↗':'Troubleshooting and limitations ↗','需要公网 IPv4、自建中转或端口映射吗？':'Do I need public IPv4, a self-hosted relay, or port forwarding?','无需公网 IPv4、无需自建中转服务器，也无需为每个服务逐一配置端口映射。FnCPN 会选择局域网直达、IPv6 直连或 FN Connect 中继；实际可用路径取决于 NAS、FN Connect 和网络环境，中继速度取决于上游服务和网络条件。':'No public IPv4, self-hosted relay, or per-service port forwarding is required. FnCPN chooses LAN, direct IPv6, or FN Connect relay. Available paths depend on your NAS, FN Connect, and network; relay speed depends on the service and network conditions.','普通 fnOS 用户可以使用吗？':'Can a standard fnOS user use FnCPN?','普通用户可以连接。安装软件和修改服务端网络设置需要管理员权限。':'Standard users can connect. Installing software and changing server network settings requires an administrator.','家人和团队成员怎么使用？':'How can family and teammates connect?','管理员先在 NAS 上安装并配置 FnCPN。每位成员在自己的电脑安装客户端，用各自的 fnOS 账号授权连接，无需共享账号密码。请按实际需要设置 LAN 访问范围；私有网络连接不能替代 NAS 与内网应用自身的账号权限。':'An administrator installs and configures FnCPN on the NAS. Each member installs the client on their computer and authorizes with their own fnOS account; no shared passwords are needed. Set LAN access as needed. Network access does not replace permissions on the NAS or LAN apps.','远程能访问哪些设备？':'Which devices can I access remotely?','可以通过连接详情中的私有地址访问 NAS，也可以访问服务端启用的 LAN 网段。本地与远端网段重叠时会提示冲突，并保留 NAS 私有地址访问。':'Access the NAS using its private address in connection details, and reach LAN subnets enabled by the server. If local and remote subnets overlap, FnCPN warns you and keeps the NAS private address available.','客户端和服务端必须同版本吗？':'Do the client and server need the same version?','从 1.0.0 正式版起，MAJOR 必须相同，client MINOR 不得高于 server MINOR；PATCH 不参与准入判断。不兼容时按 App 提示升级对应端。':'Since 1.0.0, the MAJOR versions must match and the client MINOR must not exceed the server MINOR. PATCH versions do not affect compatibility. Update the indicated side if versions are incompatible.','这是 fnOS 官方产品吗？':'Is FnCPN an official fnOS product?','FnCPN 是采用 MIT 许可证的独立开源项目，与 fnOS、FN Connect 无隶属或背书关系。商标归各自权利人所有。':'FnCPN is an independent open-source project under the MIT License. It is not affiliated with or endorsed by fnOS or FN Connect. Trademarks belong to their respective owners.','联系作者 ↗':'Contact ↗','独立开源项目 · 与 fnOS / FN Connect 无隶属或背书关系。':'Independent open-source project · Not affiliated with or endorsed by fnOS / FN Connect.','主导航':'Main navigation','打开菜单':'Open menu','关闭菜单':'Close menu',
  "支持访问码和两步验证（OTP）吗？":"Are access codes and two-factor authentication (OTP) supported?",
  "从 1.1.1 起，macOS、Windows 客户端和 Go CLI 均支持 fnOS 访问码和两步验证（OTP）。图形客户端在需要时显示访问码；提交账号密码后，若需 OTP，则进入独立步骤，可选择信任设备，也可返回上一步修改账号密码。":"Starting with version 1.1.1, the macOS and Windows clients and Go CLI support fnOS access codes and two-factor authentication (OTP). The desktop clients show an access code field when needed. After you submit your username and password, OTP appears in a separate step, with options to trust the device or go back to edit your credentials."
};
Object.assign(translations, {
  "amd64 / arm64 · systemd": "amd64 / arm64 · systemd",
  "Debian 12+、Ubuntu 22.04+，以及支持清单中的 Fedora、Arch 和 openSUSE。": "Debian 12+, Ubuntu 22.04+, and Fedora, Arch, and openSUSE versions in the support matrix.",
  "自动安装": "Automatic installation",
  "自动识别发行版和桌面环境；有桌面安装 Core+UI，无头环境仅安装 Core。": "Detects your distribution and desktop. Installs Core+UI on desktops or Core only on headless systems.",
  "复制 Linux 安装命令": "Copy Linux install commands",
  "默认获取 GitHub latest 公开版；Linux 包从 1.1.2 起提供，未公开前 latest 可能无包。可追加 --type core 或 --version 1.1.2；草稿需要 GitHub token。": "Uses the latest public GitHub release. Linux packages start at 1.1.2; latest may have no Linux assets until publication. Add --type core or --version 1.1.2 as needed. Drafts require a GitHub token.",
  "Arch 仅 amd64；不支持 Alpine。GNOME 顶栏图标需要 AppIndicator 扩展；分域 DNS 需要 systemd-resolved stub。": "Arch supports amd64 only; Alpine is unsupported. GNOME tray icons need the AppIndicator extension. Split DNS requires the systemd-resolved stub.",
  "完整支持矩阵与手动安装 ↗": "Support matrix and manual installation ↗",
  "Linux 故障排查 ↗": "Linux troubleshooting ↗",
  "Nix / NixOS 通过 Flake 源码构建，不提供独立二进制包。": "Nix / NixOS builds from Flake sources; no separate binary package is published.",
  "Nix Flake 与 NixOS 模块 ↗": "Nix Flake and NixOS module ↗"
});
Object.assign(translations, {
  '下一站，保持连接。': 'Your next stop, always connected.',
  '选择你的客户端': 'Choose your client',
  '客户端系统': 'Client operating system',
  '版本选择与系统要求': 'Versions and system requirements',
  '提供无头 Core、桌面 UI 和 NixOS 服务模块，按需声明安装。': 'Includes a headless Core, desktop UI, and NixOS service module for declarative installation.'
});
Object.assign(translations, {
  "从 1.1.4 起提供。大多数手机选择 ARM64 APK；x86_64 用于对应设备及模拟器。": "Available from 1.1.4. Choose ARM64 for most phones, or x86_64 for compatible devices and emulators.",
  "选择 Android 安装包 ↗": "Choose Android APK ↗",
  "下载 APK，允许浏览器或文件管理器安装应用；登录后按系统提示授权 VPN。使用固定 FnCPN 正式签名，后续同包名覆盖升级保留数据。": "Download the APK and allow installation from your browser or file manager. Sign in and approve the VPN request. All builds use the fixed FnCPN signing certificate; later updates with the same package preserve data.",
  "以最新公开版为准，草稿需要仓库访问权限。与其他 VPN 不能同时使用；暂不支持始终开启与锁定模式。": "Use the latest public release; drafts require repository access. Other VPNs cannot run at the same time. Always-on and lockdown VPN are not supported.",
  "Android 安装与升级说明 ↗": "Android installation and upgrade guide ↗",
  "安装客户端": "Install the client",
  "Mac、Windows、Android 按架构选择安装包；Linux 使用自动安装脚本，按桌面环境安装 UI 或无头 Core。": "Choose a package for your Mac, Windows PC or Android device. On Linux, the installer selects the desktop UI or headless Core.",
  "Mac 与 Windows 原生客户端，Android 提供 Fyne 移动界面，Linux 提供桌面 UI 和无头 Core。桌面端支持简体中文与英文，命令行也能查看状态与控制连接。": "Native apps for Mac and Windows, a Fyne mobile UI for Android, and a desktop UI or headless Core for Linux. The command line also controls connections and shows status.",
  "管理员先在 NAS 上安装并配置 FnCPN。每位成员在自己的电脑或 Android 手机安装客户端，用各自的 fnOS 账号授权连接，无需共享账号密码。请按实际需要设置 LAN 访问范围；私有网络连接不能替代 NAS 与内网应用自身的账号权限。": "An administrator installs FnCPN on the NAS. Each member installs the client on their computer or Android phone and signs in with their own fnOS account. Configure LAN access as needed; the network connection does not replace application permissions.",
  "适合自己、家人或团队成员，用各自的 fnOS 账号授权 Mac / Windows / Linux / Android / HarmonyOS 访问 NAS 和已启用的 LAN 网段；每台设备需要安装客户端。": "For you, family or team members to authorize Mac, Windows, Linux, Android, iPhone, iPad or HarmonyOS devices with their own fnOS accounts. Each device needs the client to access the NAS and enabled LAN subnets.",
  "无论是自己的文件、家人的 NAS，还是团队的共享资源，都能从 Mac、Windows、Linux、Android、iPhone、iPad 或鸿蒙设备 连接 fnOS NAS 和局域网。无需自建中转服务器，无需为每个服务逐一配置端口暴露，也无需手动配置密钥与路由。让熟悉的访问方式，不受地点限制。": "Access your files, family NAS and team resources from Mac, Windows, Linux, Android, iPhone, iPad or HarmonyOS devices. Connect to fnOS and your LAN without building a relay, exposing services one by one, or configuring keys and routes by hand."
});
Object.assign(translations, {
 'iOS 15.0+ 构建目标 · ARM64':'iOS 15.0+ build target · ARM64',
 '从 1.1.5 起提供未签名 IPA，不能直接安装。':'Unsigned IPAs are available starting with 1.1.5. They cannot be installed directly.',
 '下载未签名 IPA ↗':'Download unsigned IPA ↗',
 '维护者没有 Apple 开发者付费订阅，当前无 App Store / TestFlight 分发。需要支持 Packet Tunnel 网络扩展的设备签名材料。':'The maintainer has no paid Apple Developer subscription. No App Store or TestFlight distribution is available. Device signing materials must authorize the Packet Tunnel network extension.',
 '维护者已验证 DxSign、KravaSign；其他证书服务未验证。下载前请先阅读签名与安装教程。':'The maintainer has verified DxSign and KravaSign; other certificate services are untested. Read the signing and installation guide before downloading.',
 '仅对应版本公开后可下载；草稿不对公众开放。':'Downloads are available once the corresponding version is public; drafts are not public.',
 '签名与安装教程 ↗':'Signing and installation guide ↗'
});
Object.assign(translations, {
 '手机（含折叠屏）、平板、PC / 二合一；智慧屏暂不支持。': 'Phones (including foldables), tablets and PCs / 2-in-1s. TVs are not supported.',
 "下载未签名 HAP ↗": "Download unsigned HAP \u2197",
 '1.1.6 起纳入发布清单；仅对应版本公开后可下载，草稿不对公众开放。': 'Included in release inventories from 1.1.6. Downloads become available after publication; drafts are not public.',
 "暂未上架华为应用市场；登录和 VPN 连接等功能已通过华为云真机调试验证。": "Not yet listed on Huawei AppGallery. Features including sign-in and VPN connections have been tested on Huawei cloud devices.",
 "未签名 HAP 不能直接安装。可尝试使用自己的实名认证华为开发者账号，通过 HapSign 自行签名安装，通常需要电脑和设备调试授权。": "Unsigned HAPs cannot be installed directly. Try signing with HapSign using your own identity-verified Huawei developer account; a computer and device debugging authorization are usually required.",
 "维护者暂无华为实体设备，跨账号同包名的自行签名安装流程尚未验证，用户可自行尝试并反馈。": "The maintainer has no physical Huawei device. Cross-account signing of the same package name remains unverified; users may try it and report results.",
 '鸿蒙安装与升级说明 ↗': 'HarmonyOS installation and upgrade guide ↗'
});
const downloadTabs = [...document.querySelectorAll('[data-download-platform]')];
const downloadTabBar = document.querySelector('.download-tabs');
const downloadTabFrame = document.querySelector('.download-tabs-shell');
const downloadTabPrevious = downloadTabFrame.querySelector('.previous');
const downloadTabNext = downloadTabFrame.querySelector('.next');
Object.assign(translations, {'向左滚动标签':'Scroll tabs left','向右滚动标签':'Scroll tabs right'});
for (const [button, direction] of [[downloadTabPrevious, -1], [downloadTabNext, 1]]) {
  button.addEventListener('click', () => downloadTabBar.scrollBy({
    left:direction * Math.max(120, downloadTabBar.clientWidth * 0.7),
    behavior:matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth'
  }));
}

function updateDownloadTabEdges() {
  downloadTabFrame.classList.toggle('is-overflowing', downloadTabBar.scrollWidth > downloadTabFrame.clientWidth + 1);
  const remaining = downloadTabBar.scrollWidth - downloadTabBar.clientWidth - downloadTabBar.scrollLeft;
  downloadTabPrevious.disabled = downloadTabBar.scrollLeft <= 1;
  downloadTabNext.disabled = remaining <= 1;
  downloadTabBar.style.setProperty('--fade-left', downloadTabBar.scrollLeft > 1 ? '18px' : '0px');
  downloadTabBar.style.setProperty('--fade-right', remaining > 1 ? '18px' : '0px');
}
function revealDownloadTab(tab) {
  const bar = downloadTabBar.getBoundingClientRect();
  const item = tab.getBoundingClientRect();
  if (item.left < bar.left + 18) downloadTabBar.scrollBy({left:item.left - bar.left - 18});
  else if (item.right > bar.right - 18) downloadTabBar.scrollBy({left:item.right - bar.right + 18});
  updateDownloadTabEdges();
}
downloadTabBar.addEventListener('scroll', updateDownloadTabEdges, {passive:true});
const downloadTabResize = new ResizeObserver(() => {
  const selected = downloadTabs.find(tab => tab.getAttribute('aria-selected') === 'true');
  if (selected) revealDownloadTab(selected);
});
downloadTabResize.observe(downloadTabBar);
for (const tab of downloadTabs) downloadTabResize.observe(tab);

function selectDownload(tab, focus = false) {
  for (const item of downloadTabs) {
    const selected = item === tab;
    item.setAttribute('aria-selected', String(selected));
    item.tabIndex = selected ? 0 : -1;
    document.getElementById(item.getAttribute('aria-controls')).hidden = !selected;
  }
  if (focus) tab.focus({preventScroll:true});
  revealDownloadTab(tab);
}
for (const [index, tab] of downloadTabs.entries()) {
  tab.addEventListener('click', () => selectDownload(tab));
  tab.addEventListener('keydown', event => {
    let next;
    if (event.key === 'ArrowRight') next = (index + 1) % downloadTabs.length;
    if (event.key === 'ArrowLeft') next = (index + downloadTabs.length - 1) % downloadTabs.length;
    if (event.key === 'Home') next = 0;
    if (event.key === 'End') next = downloadTabs.length - 1;
    if (next !== undefined) {event.preventDefault();selectDownload(downloadTabs[next], true);}
  });
}
// Unsupported systems can still choose an installer manually.
const detectedPlatform = detectDownloadPlatform(navigator);
selectDownload(downloadTabs.find(tab => tab.dataset.downloadPlatform === detectedPlatform) || downloadTabs[0]);
const languageToggle = document.querySelector('#language-toggle');
const menuToggle = document.querySelector('#menu-toggle');
const siteNav = document.querySelector('#site-nav');
const support = document.querySelector('#donate');
const browserLanguage = (navigator.languages?.[0] || navigator.language || 'zh').toLowerCase();
let savedLanguage = '';
try { savedLanguage = localStorage.getItem('fncpn-language') || ''; } catch {}
let currentLanguage = new URLSearchParams(location.search).get('lang') || savedLanguage || (browserLanguage.startsWith('en') ? 'en' : 'zh');
if (currentLanguage !== 'en') currentLanguage = 'zh';
function setLanguage(language) {
  currentLanguage = language;
  try { localStorage.setItem('fncpn-language', language); } catch {}
  const nextURL = new URL(location.href); nextURL.searchParams.set('lang', language); history.replaceState(null, '', nextURL);
  document.documentElement.lang = language === 'en' ? 'en' : 'zh-CN';
  document.querySelectorAll('.ios-guide').forEach(link => { link.href = 'https://github.com/rectcircle/fn-connect-private-network/blob/main/docs/ios-installation.' + (language === 'en' ? 'en' : 'zh-CN') + '.md'; });
  support.dataset.language = language;
  languageToggle.textContent = language === 'en' ? '中文' : 'EN';
  languageToggle.setAttribute('aria-label', language === 'en' ? '切换到简体中文' : 'Switch to English');
  document.querySelector('title').textContent = language === 'en' ? 'FnCPN · Your network, wherever you are' : 'FnCPN · 让你的网络，随你出发';
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  while (walker.nextNode()) {
    const node = walker.currentNode;
    if (node.__originalText === undefined) node.__originalText = node.nodeValue;
    const original = node.__originalText.trim();
    if (!original) continue;
    const translated = translations[original];
    if (language === 'en' && translated) node.nodeValue = node.__originalText.replace(original, translated);
    else node.nodeValue = node.__originalText;
  }
  document.querySelectorAll('[aria-label]').forEach(node => {
    if (!node.dataset.originalAriaLabel) node.dataset.originalAriaLabel = node.getAttribute('aria-label');
    const label = node.dataset.originalAriaLabel;
    if (language === 'en') node.setAttribute('aria-label', translations[label] || label);
    else node.setAttribute('aria-label', label);
  });
  document.querySelectorAll('.route-tabs button').forEach(tab => {
    const i = Number(tab.querySelector('span').textContent) - 1;
    const labels = language === 'en' ? [['Local network','On the same network, take the direct route.'],['Direct IPv6','Try a direct connection from afar.'],['FN Connect relay','A fallback when direct paths are unavailable.']] : [['局域网直达','在同一网络，就走近路。'],['IPv6 直连','在外，尝试直接相连。'],['FN Connect 中继','无法直连时，多一条路。']];
    tab.querySelector('strong').textContent = labels[i][0]; tab.querySelector('small').textContent = labels[i][1];
  });
  if (language === 'en') {
    document.querySelector('#route-label').textContent = 'LOCAL NETWORK'; document.querySelector('#route-title').textContent = 'On the same local network.';
    document.querySelector('#route-description').textContent = 'When your computer and NAS share a LAN, FnCPN connects directly. Access the NAS using its local address.';
    document.querySelector('.route-status > span:last-child').textContent = 'Connection path';
  } else {
    document.querySelector('#route-label').textContent = 'LOCAL NETWORK'; document.querySelector('#route-title').textContent = '就在同一个网络里。';
    document.querySelector('#route-description').textContent = '电脑与 NAS 在同一局域网时，自动选择局域网直达，使用 NAS 局域网地址访问。';
    document.querySelector('.route-status > span:last-child').textContent = '路径示意';
  }
  const svgLabels = document.querySelectorAll('.network-svg[aria-label]');
  svgLabels.forEach(node => node.setAttribute('aria-label', language === 'en' ? 'Laptop connected through an encrypted tunnel to a dual bay fnOS NAS on a local network' : '笔记本通过加密隧道进入本地局域网，连接内网中的双盘位 fnOS NAS，连接示意'));
  document.querySelectorAll('meta[name="description"],meta[property="og:description"]').forEach(meta => meta.content = language === 'en' ? 'Connect from Mac, Windows, Linux, Android, iPhone or iPad to your fnOS NAS and home network with automatic LAN, IPv6, and FN Connect relay paths.' : 'FnCPN 让个人、家人和团队从 Mac、Windows、Linux、Android、iPhone、iPad 或鸿蒙设备 自动连接 fnOS NAS 和局域网，支持局域网直达、IPv6 直连与 FN Connect 中继。');
  if (activeRoute) selectRoute(activeRoute);
}
document.querySelectorAll('a[href^="#"]').forEach(a => a.addEventListener('click', () => {siteNav.classList.remove('is-open'); menuToggle.setAttribute('aria-expanded','false');}));
languageToggle.addEventListener('click', () => setLanguage(currentLanguage === 'zh' ? 'en' : 'zh'));
menuToggle.addEventListener('click', () => { const open = menuToggle.getAttribute('aria-expanded') !== 'true'; menuToggle.setAttribute('aria-expanded', String(open)); menuToggle.setAttribute('aria-label', open ? (currentLanguage === 'en' ? 'Close menu' : '关闭菜单') : (currentLanguage === 'en' ? 'Open menu' : '打开菜单')); siteNav.classList.toggle('is-open', open); });
const routesEnglish = {
  lan: ['LOCAL NETWORK', 'Local network', 'On the same local network.', 'When your computer and NAS share a LAN, FnCPN connects directly. Access the NAS using its local address.'],
  ipv6: ['DIRECT CONNECTION', 'Encrypted tunnel', 'Connect directly over IPv6.', 'When away from home, FnCPN tries a direct IPv6 connection through an encrypted tunnel.'],
  relay: ['FN CONNECT RELAY', '', 'Another way to connect.', 'Connect through the official fnOS relay when direct paths are unavailable. Relay speeds depend on service entitlements and network conditions.']
};
let activeRoute = null;
const routes = {
  lan: ['LOCAL NETWORK', '本地局域网', '就在同一个网络里。', '电脑与 NAS 在同一局域网时，自动选择局域网直达，使用 NAS 局域网地址访问。'],
  ipv6: ['DIRECT CONNECTION', 'IPv6 直连', '跨越距离，直接相连。', '在远程网络中，网络条件允许时尝试 IPv6 直连，通过加密隧道连接NAS 所在的网络。'],
  relay: ['FN CONNECT RELAY', '', '直连之外，还有一条路。', '无法直连时，通过 fnOS 官方中转服务器连接 NAS，无需自建中转。中继速度取决于官方服务权益与网络条件。']
};
const tabs = [...document.querySelectorAll('[data-route]')];
function selectRoute(tab) {
  tabs.forEach(item => { item.setAttribute('aria-selected', String(item === tab)); item.tabIndex = item === tab ? 0 : -1; });
  const relay = tab.dataset.route === 'relay';
  ['relay-server', 'relay-wire', 'relay-info'].forEach(id => { document.getElementById(id).hidden = !relay; });
  document.querySelector('.route-diagram').classList.toggle('is-relay', relay);
  document.querySelector('#route-panel').setAttribute('aria-labelledby', tab.id);
  activeRoute = tab;
  const route = (currentLanguage === 'en' ? routesEnglish : routes)[tab.dataset.route];
  ['route-label', 'route-middle', 'route-title', 'route-description'].forEach((id, i) => { document.getElementById(id).textContent = route[i]; });
}
tabs.forEach((tab, index) => {
  tab.addEventListener('click', () => selectRoute(tab));
  tab.addEventListener('keydown', event => {
    let next;
    if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = (index + 1) % tabs.length;
    if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = (index + tabs.length - 1) % tabs.length;
    if (event.key === 'Home') next = 0;
    if (event.key === 'End') next = tabs.length - 1;
    if (next !== undefined) { event.preventDefault(); selectRoute(tabs[next]); tabs[next].focus(); }
  });
});
setLanguage(currentLanguage);
if (new URLSearchParams(location.search).get('donate') === '1') {
  const openDonationSection = async () => {
    if (document.fonts?.ready) await document.fonts.ready;
    const section = document.querySelector('#donate');
    if (!section) return;

    // Keep the query parameter as an entry point, then leave a clean anchor URL.
    const cleanURL = new URL(location.href);
    cleanURL.searchParams.delete('donate');
    cleanURL.hash = 'donate';
    history.replaceState(null, '', cleanURL);
    section.scrollIntoView({ block: 'start' });
  };

  if (document.readyState === 'complete') openDonationSection();
  else window.addEventListener('load', openDonationSection, { once: true });
}
let toastTimer;
for (const [button, command] of [
  ['copy-command', 'brew tap rectcircle/fn-connect-private-network https://github.com/rectcircle/fn-connect-private-network\nbrew install --cask rectcircle/fn-connect-private-network/fncpn'],
  ['copy-linux-command', document.querySelector('#linux-command').textContent]
]) {
  document.getElementById(button).addEventListener('click', async () => {
    const toast = document.querySelector('#toast');
    try {
      await navigator.clipboard.writeText(command);
      toast.textContent = currentLanguage === 'en' ? 'Copied the complete install commands.' : '已复制完整安装命令。';
    } catch {
      toast.textContent = currentLanguage === 'en' ? 'Could not copy automatically. Copy the full commands from the installation guide.' : '无法自动复制，请从安装文档复制完整命令。';
    }
    toast.classList.add('visible'); clearTimeout(toastTimer); toastTimer = setTimeout(() => toast.classList.remove('visible'), 4000);
  });
}
