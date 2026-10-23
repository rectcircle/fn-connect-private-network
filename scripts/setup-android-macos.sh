#!/bin/bash
# macOS command-line Android setup. No Android Studio or emulator.
set -Eeuo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SKIP_SMOKE=0
ACCEPT_LICENSES=0
usage() {
    cat <<'EOF'
Usage: bash scripts/setup-android-macos.sh [--skip-smoke] [--accept-licenses]
  --skip-smoke       Skip assembling the real dual-ABI Debug APK.
  --accept-licenses  Accept Android SDK licenses non-interactively.
Existing matching tools are reused. Missing JDK/Python use an existing Homebrew.
Go/Gradle install under ~/.local/opt; SDK defaults to ~/Library/Android/sdk.
Environment exports are written directly to zsh/bash startup files (with backups).
EOF
}
for arg in "$@"; do
    case "$arg" in
        --skip-smoke) SKIP_SMOKE=1 ;;
        --accept-licenses) ACCEPT_LICENSES=1 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown option: $arg" >&2; usage >&2; exit 2 ;;
    esac
done
[[ $(uname -s) == Darwin ]] || { echo '仅支持 macOS。' >&2; exit 1; }
case $(uname -m) in arm64) GO_ARCH=arm64 ;; x86_64) GO_ARCH=amd64 ;; *) exit 1 ;; esac

TOOLS="$HOME/.local/opt"
DOWNLOADS="$ROOT/.dev/downloads"
mkdir -p "$TOOLS" "$DOWNLOADS" "$ROOT/.dev/logs"
LOG="$ROOT/.dev/logs/setup-android-macos-$(date +%Y%m%d-%H%M%S)-$$.log"
exec > >(tee -a "$LOG") 2>&1
SECONDS=0
log() { printf '[setup-android-macos] [累计耗时=%s秒] %s\n' "$SECONDS" "$*"; }
trap 'log "错误：第 $LINENO 行执行失败；修复后可重跑。日志：$LOG"' ERR
stage() {
    local name=$1 start=$SECONDS
    shift
    log "检查：$name"
    "$@"
    log "阶段耗时：${name}，$((SECONDS - start))秒"
}
die() { log "错误：$*"; exit 1; }
brew_install() {
    command -v brew >/dev/null || die '缺少 Homebrew；请先安装 Homebrew，或自行安装缺失的 JDK 17 / Python 3 后重跑。'
    brew install "$@"
}
# Check cached archives too; partial downloads never become valid cache entries.
download() {
    local url=$1 file=$2 hash=$3 algorithm=${4:-256}
    if [[ -f "$file" ]] && [[ $(shasum -a "$algorithm" "$file" | awk '{print $1}') == "$hash" ]]; then
        log "跳过：下载缓存已校验 $(basename "$file")"
        return
    fi
    log "下载：$url"
    curl --fail --location --retry 3 "$url" -o "$file.partial"
    [[ $(shasum -a "$algorithm" "$file.partial" | awk '{print $1}') == "$hash" ]] || die "下载校验失败：$file.partial"
    mv "$file.partial" "$file"
}
# Extract on the destination filesystem, then rename as the installation commit.
install_archive() {
    local file=$1 destination=$2 inner=$3 kind=$4 staging
    [[ ! -e "$destination" ]] || die "安装目录已存在但验证未通过，拒绝覆盖：$destination"
    mkdir -p "$(dirname "$destination")"
    staging=$(mktemp -d "$(dirname "$destination")/.setup-XXXXXX")
    if [[ $kind == zip ]]; then unzip -q "$file" -d "$staging"
    else tar -xzf "$file" -C "$staging"; fi
    mv "$staging/$inner" "$destination"
    rmdir "$staging"
}

setup_prerequisites() {
    local candidate
    JDK=''
    for candidate in "${JAVA_HOME:-}" "$(/usr/libexec/java_home -v 17 2>/dev/null || true)"; do
        if [[ -x "$candidate/bin/java" ]] && "$candidate/bin/java" -version 2>&1 | /usr/bin/grep -q 'version "17\.'; then
            JDK=$candidate; break
        fi
    done
    if [[ -z $JDK ]]; then
        brew_install --cask temurin@17
        JDK=$(/usr/libexec/java_home -v 17)
    else log "跳过：已有 JDK 17：$JDK"; fi
    export JAVA_HOME="$JDK"
    export PATH="$JAVA_HOME/bin:$PATH"
    java -version
    if ! command -v python3 >/dev/null || ! python3 --version; then brew_install python; fi
    python3 -c 'import json, pathlib, urllib.request, xml.etree.ElementTree'
    git --version
}
setup_go() {
    local candidate archive hash
    GO=''
    for candidate in "$(command -v go || true)" "$TOOLS/go-1.27.1/bin/go"; do
        if [[ -x $candidate ]] && [[ $(GOTOOLCHAIN=local "$candidate" env GOVERSION) == go1.27.1 ]]; then
            GO=$candidate; break
        fi
    done
    if [[ -z $GO ]]; then
        archive="go1.27.1.darwin-$GO_ARCH.tar.gz"
        curl -fsSL --retry 3 'https://go.dev/dl/?mode=json&include=all' -o "$DOWNLOADS/go-releases.json"
        hash=$(python3 - "$DOWNLOADS/go-releases.json" "$archive" <<'PY'
import json, sys
files = [f for r in json.load(open(sys.argv[1])) for f in r['files'] if f['filename'] == sys.argv[2]]
if len(files) != 1:
    raise SystemExit('Official Go release metadata did not contain the pinned archive')
print(files[0]['sha256'])
PY
)
        download "https://go.dev/dl/$archive" "$DOWNLOADS/$archive" "$hash"
        install_archive "$DOWNLOADS/$archive" "$TOOLS/go-1.27.1" go tar
        GO="$TOOLS/go-1.27.1/bin/go"
    else log "跳过：已有 Go 1.27.1：$GO"; fi
    [[ $(GOTOOLCHAIN=local "$GO" env GOVERSION) == go1.27.1 ]] || die 'Go 版本验证失败'
    export PATH="$(dirname "$GO"):$PATH"
    export GOTOOLCHAIN=local
    "$GO" version
}
setup_gradle() {
    local candidate
    GRADLE=''
    for candidate in "$(command -v gradle || true)" "$TOOLS/gradle-8.13/bin/gradle"; do
        if [[ -x $candidate ]] && "$candidate" --version | /usr/bin/grep -qx 'Gradle 8.13'; then
            GRADLE=$candidate; break
        fi
    done
    if [[ -z $GRADLE ]]; then
        download 'https://services.gradle.org/distributions/gradle-8.13-bin.zip' \
            "$DOWNLOADS/gradle-8.13-bin.zip" '20f1b1176237254a6fc204d8434196fa11a4cfb387567519c61556e8710aed78'
        install_archive "$DOWNLOADS/gradle-8.13-bin.zip" "$TOOLS/gradle-8.13" gradle-8.13 zip
        GRADLE="$TOOLS/gradle-8.13/bin/gradle"
    else log "跳过：已有 Gradle 8.13：$GRADLE"; fi
    "$GRADLE" --version | /usr/bin/grep -qx 'Gradle 8.13'
    export PATH="$(dirname "$GRADLE"):$PATH"
}
setup_sdk() {
    export ANDROID_HOME="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
    [[ $ANDROID_HOME == /* ]] || die 'ANDROID_HOME 必须为绝对路径'
    SDKMANAGER=''
    local candidate
    for candidate in "$ANDROID_HOME/cmdline-tools/12266719/bin/sdkmanager" \
        "$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager" "$(command -v sdkmanager || true)"; do
        if [[ -x $candidate ]] && "$candidate" --sdk_root="$ANDROID_HOME" --version; then
            SDKMANAGER=$candidate; break
        fi
    done
    if [[ -z $SDKMANAGER ]]; then
        # Official repository2-3.xml publishes this archive's SHA-1 checksum.
        download 'https://dl.google.com/android/repository/commandlinetools-mac-12266719_latest.zip' \
            "$DOWNLOADS/commandlinetools-mac-12266719_latest.zip" 'b2fb3e403104bf342be7abf0631c23e30559d222' 1
        install_archive "$DOWNLOADS/commandlinetools-mac-12266719_latest.zip" \
            "$ANDROID_HOME/cmdline-tools/12266719" cmdline-tools zip
        SDKMANAGER="$ANDROID_HOME/cmdline-tools/12266719/bin/sdkmanager"
    else log "跳过：已有可用 sdkmanager：$SDKMANAGER"; fi
    export PATH="$(dirname "$SDKMANAGER"):$PATH"
    if [[ $ACCEPT_LICENSES == 1 ]]; then
        log '许可：按 --accept-licenses 接受 Android SDK 许可证'
        # yes may exit with SIGPIPE after sdkmanager closes stdin; preserve the manager's exit status.
        local result
        if yes | "$SDKMANAGER" --sdk_root="$ANDROID_HOME" --licenses; then
            result=0
        else
            result=${PIPESTATUS[1]}
        fi
        [[ $result == 0 ]] || die 'SDK 许可证处理失败'
    else "$SDKMANAGER" --sdk_root="$ANDROID_HOME" --licenses; fi
    # sdkmanager verifies installed package IDs/revisions and skips complete matching packages.
    local ndk="${ANDROID_NDK_HOME:-$ANDROID_HOME/ndk/28.2.13676358}"
    if [[ -f "$ndk/source.properties" ]] && \
        /usr/bin/grep -Eq '^Pkg.Revision *= *28\.2\.13676358 *$' "$ndk/source.properties" && \
        [[ -x "$ndk/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android26-clang" ]]; then
        log "跳过：已有匹配 NDK：$ndk"
    else
        ndk="$ANDROID_HOME/ndk/28.2.13676358"
        "$SDKMANAGER" --sdk_root="$ANDROID_HOME" --install 'ndk;28.2.13676358'
    fi
    "$SDKMANAGER" --sdk_root="$ANDROID_HOME" --install \
        'platform-tools' 'platforms;android-36' 'build-tools;35.0.0'
    export ANDROID_NDK_HOME="$ndk"
    /usr/bin/grep -Eq '^Pkg.Revision *= *28\.2\.13676358 *$' "$ANDROID_NDK_HOME/source.properties"
    [[ -x "$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android26-clang" ]] || die 'NDK 安装不完整'
    [[ -f "$ANDROID_HOME/platforms/android-36/android.jar" ]] || die 'SDK platform 安装不完整'
    [[ -x "$ANDROID_HOME/build-tools/35.0.0/aapt2" ]] || die 'Build Tools 安装不完整'
    "$ANDROID_HOME/platform-tools/adb" version
}
write_environment() {
    # Write each dependency directly into both shells; no extra env file to source.
    python3 - "$JAVA_HOME" "$(dirname "$GO")" "$(dirname "$GRADLE")" \
        "$ANDROID_HOME" "$ANDROID_NDK_HOME" "$(dirname "$SDKMANAGER")" <<'PYPROFILES'
import datetime
import os
from pathlib import Path
import re
import shlex
import stat
import sys
import tempfile

java, go, gradle, sdk, ndk, manager = sys.argv[1:]
home = Path.home()
login = next((home / name for name in ('.bash_profile', '.bash_login', '.profile')
              if (home / name).exists()), home / '.bash_profile')
profiles = [Path(os.environ.get('ZDOTDIR') or home) / '.zshrc', home / '.bashrc', login]

def path_entry(directory):
    quoted = shlex.quote(directory)
    return ('case ":$PATH:" in\n'
            f'    *:{quoted}:*) ;;\n'
            f'    *) export PATH={quoted}:"$PATH" ;;\n'
            'esac\n')

blocks = {
    'Java': f'export JAVA_HOME={shlex.quote(java)}\n' + path_entry(java + '/bin'),
    'Go': path_entry(go) + 'export GOTOOLCHAIN=local\n',
    'Gradle': path_entry(gradle),
    'Android': (f'export ANDROID_HOME={shlex.quote(sdk)}\n'
                f'export ANDROID_NDK_HOME={shlex.quote(ndk)}\n'
                + path_entry(manager) + path_entry(sdk + '/platform-tools')),
}
# Resolve symlinks, and prepare every edit before writing any startup file.
changes = {}
for profile in profiles:
    target = profile.resolve()
    if target in changes:
        continue
    old = target.read_text() if target.exists() else ''
    updated = old
    for name, body in blocks.items():
        begin = f'# >>> FnCPN Android setup: {name} >>>'
        end = f'# <<< FnCPN Android setup: {name} <<<'
        if updated.count(begin) != updated.count(end) or updated.count(begin) > 1:
            raise SystemExit(f'Malformed setup markers; fix before rerunning: {profile}')
        block = begin + '\n' + body + end + '\n'
        if begin in updated:
            updated = re.sub(re.escape(begin) + r'.*?' + re.escape(end) + r'\n?',
                             lambda _: block, updated, flags=re.S)
        else:
            updated = updated.rstrip('\n') + '\n\n' + block
    changes[target] = (old, updated)

for target, (old, updated) in changes.items():
    if old == updated:
        print(f'跳过：shell 配置已匹配 {target}')
        continue
    target.parent.mkdir(parents=True, exist_ok=True)
    mode = stat.S_IMODE(target.stat().st_mode) if target.exists() else 0o600
    if target.exists():
        stamp = datetime.datetime.now().strftime('%Y%m%d-%H%M%S-%f')
        backup = target.with_name(target.name + f'.fncpn-backup-{stamp}')
        backup.write_text(old)
        backup.chmod(mode)
        print(f'备份：{backup}')
    fd, temporary = tempfile.mkstemp(prefix='.fncpn-shell-', dir=target.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            stream.write(updated)
        os.chmod(temporary, mode)
        os.replace(temporary, target)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    print(f'配置：{target}')
PYPROFILES
}

download_modules() { (cd "$ROOT/mobile" && "$GO" mod download); }
smoke() { (cd "$ROOT/mobile/android" && "$GRADLE" --no-daemon --console=plain :app:assembleDebug); }

log "开始：项目=${ROOT}；日志=$LOG"
# These cross-compilation variables must not leak into dependency/bootstrap commands.
unset GOOS GOARCH CGO_ENABLED
stage 'JDK 17、Python 3、Git' setup_prerequisites
stage 'Go 1.27.1' setup_go
stage 'Gradle 8.13' setup_gradle
stage 'Android SDK / Build Tools / NDK' setup_sdk
stage '直接配置 zsh / bash 用户启动文件' write_environment
stage 'Go 模块依赖下载（保留现有代理配置）' download_modules
if [[ $SKIP_SMOKE == 0 ]]; then stage '双架构 Debug APK 构建验证' smoke
else log '跳过：APK 构建验证（--skip-smoke）'; fi
log '成功：重新打开终端，或加载对应的 ~/.zshrc / ~/.bashrc 后即可构建；未安装 Android Studio/模拟器。'
