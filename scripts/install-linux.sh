#!/usr/bin/env bash
# Standalone installer; no checkout, compiler or frontend toolchain required.
set -euo pipefail

REPOSITORY=rectcircle/fn-connect-private-network
API=https://api.github.com
INSTALL_TYPE=auto
REQUESTED_VERSION=latest
WORK_DIR=
RPM_KEY_SHA256=cada20dd8f3458db0840ce52fccdff21ac3ce13aa018058cc3df2963e5dbaa24

fail() { printf '错误：%s\n' "$*" >&2; exit 1; }
info() { printf '%s\n' "$*" >&2; }
usage() {
    cat <<'HELP'
用法：bash install-linux.sh [--type all|core] [--version latest|VERSION]
  --type all      安装同版本的 Core 和桌面 UI
  --type core     仅安装 Core
  默认           检测到桌面或已有 UI 时安装全部，否则仅安装 Core
  --version      默认为 GitHub 最新公开正式版，也接受 1.1.2 或 v1.1.2
  --help         显示帮助
私有仓库或指定草稿可通过 GH_TOKEN / GITHUB_TOKEN 提供有读取权限的 token。
HELP
}
parse_args() {
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --type)
                [ "$#" -ge 2 ] || fail '--type 缺少参数'
                case "$2" in all|core) INSTALL_TYPE=$2 ;; *) fail '--type 只能为 all 或 core' ;; esac
                shift 2 ;;
            --version)
                [ "$#" -ge 2 ] || fail '--version 缺少参数'
                REQUESTED_VERSION=${2#v}
                [[ "$REQUESTED_VERSION" == latest || "$REQUESTED_VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || fail '版本应为 latest 或 MAJOR.MINOR.PATCH'
                shift 2 ;;
            --help|-h) usage; exit 0 ;;
            *) fail "未知参数：$1" ;;
        esac
    done
}

detect_distribution() {
    local file=${1:-/etc/os-release} ID= VERSION_ID= VERSION_CODENAME= VERSION=
    LOONGARCH_DISTRO=0
    [ -r "$file" ] || fail '无法读取 /etc/os-release'
    # os-release is root-managed and uses shell assignment syntax.
    . "$file"
    case "$ID:$VERSION_ID" in
        debian:12|debian:13|ubuntu:22.04|ubuntu:24.04|ubuntu:26.04|ubuntu:26.10)
            FORMAT=deb; MANAGER=apt-get; EXTENSION=deb ;;
        debian:*)
            [ "$VERSION_CODENAME" = forky ] || fail '此 Debian 版本未列入支持范围'
            FORMAT=deb; MANAGER=apt-get; EXTENSION=deb; LOONGARCH_DISTRO=1 ;;
        deepin:25) FORMAT=deb; MANAGER=apt-get; EXTENSION=deb; LOONGARCH_DISTRO=2 ;;
        fedora:43|fedora:44) FORMAT=rpm; MANAGER=dnf; EXTENSION=rpm ;;
        opensuse-leap:16.0|opensuse-tumbleweed:*) FORMAT=rpm; MANAGER=zypper; EXTENSION=rpm ;;
        openEuler:24.03)
            [[ "$VERSION" == *LTS-SP4* ]] || fail 'openEuler LoongArch 目前仅验证 24.03 LTS SP4'
            FORMAT=rpm; MANAGER=dnf; EXTENSION=rpm; LOONGARCH_DISTRO=2 ;;
        arch:*) FORMAT=archlinux; MANAGER=pacman; EXTENSION=pkg.tar.zst ;;
        nixos:*) fail 'NixOS 请使用本项目的 Nix Flake / NixOS 模块，不能使用普通 Linux 安装包' ;;
        alpine:*) fail '不支持 Alpine Linux / apk' ;;
        *) fail "不支持此发行版或版本：$ID ${VERSION_ID:-rolling}；请参阅 README 的支持矩阵" ;;
    esac
    DISTRIBUTION="$ID ${VERSION_ID:-rolling}"
}
detect_architecture() {
    case "$(uname -m)" in
        x86_64) ARCH=amd64 ;;
        aarch64|arm64) ARCH=arm64 ;;
        loongarch64) ARCH=loong64 ;;
        *) fail "不支持此架构：$(uname -m)" ;;
    esac
    [ "$FORMAT:$ARCH" != archlinux:arm64 ] || fail 'Arch 包支持 x86_64 和新世界 LoongArch64，不支持 Arch Linux ARM'
    if [ "$ARCH" = loong64 ]; then
        [ "${LOONGARCH_DISTRO:-0}" != 0 ] || [ "$FORMAT" = archlinux ] || fail 'LoongArch 支持 Debian forky、deepin 25、openEuler 24.03 SP4 和 Loong Arch Linux 新世界系统'
        { [ "$FORMAT" != deb ] || [ "$(dpkg --print-architecture)" = loong64 ]; } && loongarch_loader_available || fail '不支持 LoongArch 旧世界 ABI'
        local kernel major minor
        kernel=$(uname -r); major=${kernel%%.*}; minor=${kernel#*.}; minor=${minor%%.*}
        [[ "$major" =~ ^[0-9]+$ && "$minor" =~ ^[0-9]+$ ]] || fail '无法识别 LoongArch 内核版本'
        (( major > 5 || (major == 5 && minor >= 19) )) || fail 'LoongArch 需要 Linux 5.19 或更高版本'
    else
        [ "${LOONGARCH_DISTRO:-0}" = 0 ] || fail '该发行版配置目前仅验证 loong64 架构'
    fi
}
loongarch_loader_available() { [ -r /lib64/ld-linux-loongarch-lp64d.so.1 ]; }
has_desktop() {
    [ -n "${DISPLAY:-}" ] || [ -n "${WAYLAND_DISPLAY:-}" ] ||
        compgen -G '/usr/share/xsessions/*.desktop' >/dev/null ||
        compgen -G '/usr/share/wayland-sessions/*.desktop' >/dev/null ||
        [ -L /etc/systemd/system/display-manager.service ]
}
installed_version() {
    local component=$1 record
    case "$FORMAT" in
        deb)
            record=$(dpkg-query -W -f='${db:Status-Status} ${Version}' "fncpn-$component" 2>/dev/null) || return 0
            [[ "$record" == 'installed '* ]] && printf '%s\n' "${record#installed }" || true ;;
        rpm)
            record=$(rpm -q --qf '%{VERSION}\n' "fncpn-$component" 2>/dev/null) || return 0
            printf '%s\n' "$record" ;;
        archlinux)
            record=$(pacman -Q "fncpn-$component" 2>/dev/null) || return 0
            record=${record#* }; printf '%s\n' "${record%-*}" ;;
    esac
}
as_root() {
    if [ "$(id -u)" = 0 ]; then "$@"; else sudo -- "$@"; fi
}
refresh_apt() {
    if [ "${APT_REFRESHED:-0}" != 1 ]; then
        as_root apt-get update || return
        APT_REFRESHED=1
    fi
}
ensure_tools() {
    local -a packages=()
    command -v curl >/dev/null || packages+=(curl)
    command -v jq >/dev/null || packages+=(jq)
    command -v sha256sum >/dev/null || packages+=(coreutils)
    if [ ! -s /etc/ssl/certs/ca-certificates.crt ] && [ ! -s /etc/pki/tls/certs/ca-bundle.crt ] && [ ! -s /etc/ssl/ca-bundle.pem ]; then
        packages+=(ca-certificates)
    fi
    if [ "${#packages[@]}" -gt 0 ]; then
        info '安装缺少的下载/校验工具和 CA 证书，不安装开发环境。'
        case "$MANAGER" in
            apt-get) refresh_apt; as_root apt-get install -y "${packages[@]}" ;;
            dnf) as_root dnf install -y "${packages[@]}" ;;
            zypper) as_root zypper --non-interactive install "${packages[@]}" ;;
            pacman)
                info 'Arch 将先完整更新系统，避免部分升级。'
                as_root pacman -Syu --needed --noconfirm "${packages[@]}" ;;
        esac
    fi
}
select_type() {
    if [ "$INSTALL_TYPE" = auto ]; then
        if has_desktop || [ -n "$(installed_version ui)" ]; then INSTALL_TYPE=all; else INSTALL_TYPE=core; fi
    elif [ "$INSTALL_TYPE" = all ] && ! has_desktop; then
        info '未检测到桌面环境；按 --type all 安装 UI，运行 UI 仍需桌面会话。'
    fi
}

api_get() {
    local endpoint=$1 destination=$2 accept=$3 token=${GH_TOKEN:-${GITHUB_TOKEN:-}}
    local -a args=(--fail --silent --show-error --location --retry 3 --connect-timeout 20 --max-time 600
        --proto '=https' --proto-redir '=https' -H "Accept: $accept" -H 'X-GitHub-Api-Version: 2022-11-28')
    # Keep credentials out of process arguments; curl strips auth on cross-host redirects.
    local headers
    headers=$(printf 'header = "User-Agent: fncpn-linux-installer"\n')
    if [ -n "$token" ]; then
        [[ "$token" != *$'\n'* && "$token" != *$'\r'* && "$token" != *'"'* && "$token" != *'\'* ]] || fail '无效的 GitHub token'
        headers="$headers"$'\n'"header = \"Authorization: Bearer $token\""
    fi
    printf '%s\n' "$headers" | curl --config - "${args[@]}" "$API/repos/$REPOSITORY/$endpoint" --output "$destination"
}
resolve_release() {
    local endpoint=releases/latest
    [ "$REQUESTED_VERSION" = latest ] || endpoint="releases/tags/v$REQUESTED_VERSION"
    if ! api_get "$endpoint" "$WORK_DIR/release.json" application/vnd.github+json; then
        # Tagless drafts are deliberately absent from /releases/tags/:tag.
        # Only an explicitly requested version plus credentials may use them;
        # latest must continue to mean the public stable release.
        [ "$REQUESTED_VERSION" != latest ] && [ -n "${GH_TOKEN:-${GITHUB_TOKEN:-}}" ] || fail '无法读取 GitHub Release；请检查网络、版本是否公开，或设置有读取权限的 GH_TOKEN'
        local page=1 found=0
        while [ "$page" -le 10 ]; do
            api_get "releases?per_page=100&page=$page" "$WORK_DIR/releases-page.json" application/vnd.github+json || fail '无法读取 GitHub Release 草稿列表'
            if jq -e --arg tag "v$REQUESTED_VERSION" '[.[] | select(.tag_name == $tag)] | select(length == 1) | .[0]' "$WORK_DIR/releases-page.json" > "$WORK_DIR/release.json"; then
                found=1; break
            fi
            [ "$(jq 'length' "$WORK_DIR/releases-page.json")" -eq 100 ] || break
            page=$((page + 1))
        done
        [ "$found" = 1 ] || fail '找不到指定版本的 GitHub Release 草稿'
    fi
    local tag
    tag=$(jq -er '.tag_name | select(type == "string")' "$WORK_DIR/release.json") || fail 'Release 缺少 tag_name'
    [[ "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || fail "不是正式产品版本：$tag"
    VERSION=${tag#v}
    if [ "$REQUESTED_VERSION" = latest ]; then
        jq -e '.draft == false and .prerelease == false' "$WORK_DIR/release.json" >/dev/null || fail 'latest 必须是公开正式版'
    else
        [ "$VERSION" = "$REQUESTED_VERSION" ] || fail 'GitHub 返回版本与指定版本不一致'
        if jq -e '.draft == true' "$WORK_DIR/release.json" >/dev/null; then info "正在安装显式指定的未公开草稿 ${tag}。"; fi
    fi
}
asset_id() {
    jq -er --arg name "$1" '[.assets[] | select(.name == $name)] | select(length == 1) | .[0].id | select(type == "number" and . > 0 and . == floor)' "$WORK_DIR/release.json"
}
download_asset() {
    local name=$1 asset
    asset=$(asset_id "$name") || fail "Release v$VERSION 缺少唯一资产 ${name}；该版本可能尚未提供 Linux 包"
    api_get "releases/assets/$asset" "$WORK_DIR/$name" application/octet-stream || fail "下载失败：$name"
}
version_is_older() {
    # Product versions use numeric MAJOR.MINOR.PATCH; no lexical comparison.
    local candidate=$1 installed=$2 i
    local -a a b
    [[ "$installed" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || fail "无法安全比较已安装版本 ${installed}，请手动处理升级/回滚"
    IFS=. read -r -a a <<< "$candidate"
    IFS=. read -r -a b <<< "$installed"
    for i in 0 1 2; do
        # Limit arithmetic to realistic package version fields, avoiding overflow.
        [ "${#a[$i]}" -le 9 ] && [ "${#b[$i]}" -le 9 ] || fail '版本字段超出支持范围'
        if (( 10#${a[$i]} < 10#${b[$i]} )); then return 0; fi
        if (( 10#${a[$i]} > 10#${b[$i]} )); then return 1; fi
    done
    return 1
}
check_upgrade() {
    local component current ui
    for component in core ui; do
        current=$(installed_version "$component")
        if [ -n "$current" ] && version_is_older "$VERSION" "$current"; then
            fail "拒绝从 $component $current 自动降级到 ${VERSION}；请按数据备份与回滚契约手动处理"
        fi
    done
    ui=$(installed_version ui)
    if [ "$INSTALL_TYPE" = core ] && [ -n "$ui" ] && [ "$ui" != "$VERSION" ]; then
        fail "已安装 UI ${ui}；请使用 --type all 将 Core/UI 成套更新到 $VERSION"
    fi
}
verify_packages() {
    local name checksum lines
    : > "$WORK_DIR/selected-SHA256SUMS"
    for name in "${PACKAGE_NAMES[@]}"; do
        lines=$(awk -v name="$name" '$2 == name {n++} END {print n+0}' "$WORK_DIR/SHA256SUMS-$VERSION")
        [ "$lines" = 1 ] || fail "校验清单缺少或重复列出 $name"
        checksum=$(awk -v name="$name" '$2 == name {print $1}' "$WORK_DIR/SHA256SUMS-$VERSION")
        [[ "$checksum" =~ ^[a-fA-F0-9]{64}$ ]] || fail "无效的 SHA256：$name"
        printf '%s  %s\n' "$checksum" "$name" >> "$WORK_DIR/selected-SHA256SUMS"
    done
    (cd "$WORK_DIR" && sha256sum --check --strict selected-SHA256SUMS) || fail '安装包 SHA256 不匹配，未安装'
}
verify_rpm_signatures() {
    [ "$FORMAT" = rpm ] || return 0
    local key="$WORK_DIR/RPM-GPG-KEY-fncpn" package output
    # Older immutable tags contain the same pinned key under the former source path.
    if ! api_get "contents/apps/linux/packaging/keys/RPM-GPG-KEY-fncpn?ref=v$VERSION" "$key" application/vnd.github.raw+json; then
        api_get "contents/packaging/linux/keys/RPM-GPG-KEY-fncpn?ref=v$VERSION" "$key" application/vnd.github.raw+json || fail '无法下载 RPM 公钥'
    fi
    printf '%s  %s\n' "$RPM_KEY_SHA256" "$key" | sha256sum --check --strict - || fail 'RPM 公钥与安装脚本固定的公钥不一致'
    mkdir -m 700 "$WORK_DIR/rpm-keyring"
    rpmkeys --dbpath "$WORK_DIR/rpm-keyring" --import "$key" || fail '无法读取 RPM 公钥'
    for package in "${PACKAGE_NAMES[@]}"; do
        output=$(LC_ALL=C rpmkeys --dbpath "$WORK_DIR/rpm-keyring" --checksig --verbose "$WORK_DIR/$package" 2>&1) || fail "RPM 签名校验失败：$package"
        [[ "$output" =~ [Ss]ignature.*OK ]] && [[ ! "$output" =~ NOKEY|NOT[[:space:]]OK|BAD|UNSIGNED ]] || fail "RPM 缺少有效的项目签名：$package"
    done
    # Import only after every selected package verifies in an isolated keyring.
    as_root rpmkeys --import "$key" || fail '无法向系统导入项目 RPM 公钥'
}
install_packages() {
    local name
    local -a files=()
    for name in "${PACKAGE_NAMES[@]}"; do files+=("$WORK_DIR/$name"); done
    case "$MANAGER" in
        apt-get) refresh_apt || return; as_root apt-get install -y "${files[@]}" ;;
        dnf) as_root dnf install -y --setopt=localpkg_gpgcheck=True "${files[@]}" ;;
        zypper) as_root zypper --non-interactive install "${files[@]}" ;;
        pacman)
            info 'Arch 将先完整更新系统，避免部分升级。'
            as_root pacman -Syu --noconfirm || return
            as_root pacman -U --noconfirm "${files[@]}" ;;
    esac
}
main() {
    [ "$(uname -s)" = Linux ] || fail '此脚本仅支持 Linux，不能在 macOS 或 Windows 上运行'
    parse_args "$@"
    detect_distribution
    detect_architecture
    [ ! -e /run/ostree-booted ] && [ ! -f /etc/transactional-update.conf ] || fail '不支持此不可变/事务式系统，请使用匹配系统的部署方式'
    command -v "$MANAGER" >/dev/null || fail "找不到发行版包管理器 $MANAGER"
    command -v systemctl >/dev/null && [ -d /run/systemd/system ] || fail '需要正在运行的 systemd 系统；不支持无 systemd 的容器或 WSL 会话'
    if [ "$(id -u)" != 0 ]; then command -v sudo >/dev/null || fail '需要 sudo，或由 root 运行脚本'; fi
    select_type
    info "系统：${DISTRIBUTION}；架构：${ARCH}；安装组件：$INSTALL_TYPE"
    ensure_tools
    umask 077
    WORK_DIR=$(mktemp -d /tmp/fncpn-install.XXXXXXXX)
    trap 'rm -rf -- "$WORK_DIR"' EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    resolve_release
    check_upgrade
    PACKAGE_NAMES=("fncpn-core-$VERSION-linux-$ARCH.$EXTENSION")
    [ "$INSTALL_TYPE" != all ] || PACKAGE_NAMES+=("fncpn-ui-$VERSION-linux-$ARCH.$EXTENSION")
    # Resolve every required asset before downloading or invoking the package manager.
    local name
    for name in "SHA256SUMS-$VERSION" "${PACKAGE_NAMES[@]}"; do asset_id "$name" >/dev/null || fail "v$VERSION 缺少资产：$name"; done
    info "下载并校验 v${VERSION}，来源 GitHub ${REPOSITORY}。"
    download_asset "SHA256SUMS-$VERSION"
    for name in "${PACKAGE_NAMES[@]}"; do download_asset "$name"; done
    verify_packages
    verify_rpm_signatures
    info '校验通过，使用系统包管理器安装并解析运行依赖（保留系统签名策略）。'
    install_packages || fail '包管理器安装失败；请查看包管理器错误；不要关闭签名校验'
    info "FnCPN $VERSION 安装完成。"
    if [ "$INSTALL_TYPE" = all ]; then
        info '在普通用户的桌面应用菜单打开 FnCPN，首次运行按提示授权特权服务。'
    else
        info '以需要连接 NAS 的普通用户运行 fncpn-linux-setup，启用该用户的服务。'
    fi
    info '分域 DNS 需要 systemd-resolved 的 127.0.0.53 stub；脚本不改写物理网络或 resolv.conf。'
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then main "$@"; fi
