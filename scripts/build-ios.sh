#!/bin/bash
# Development builds only. Device signing is configured separately in Xcode.
set -Eeuo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SDK=iphonesimulator
INSTALL=''
while [[ $# -gt 0 ]]; do
    case "$1" in
        --device) SDK=iphoneos; shift ;;
        --install) [[ $# -ge 2 ]] || exit 2; INSTALL=$2; shift 2 ;;
        -h|--help) echo 'Usage: bash scripts/build-ios.sh [--device | --install BOOTED_SIMULATOR_UDID]'; exit 0 ;;
        *) echo "Unknown argument: $1" >&2; exit 2 ;;
    esac
done
[[ -z $INSTALL || $SDK == iphonesimulator ]] || { echo '--install only supports simulators' >&2; exit 2; }
cd "$ROOT"
mkdir -p .dev/logs
LOG="$ROOT/.dev/logs/build-ios-$(date +%Y%m%d-%H%M%S)-$$.log"
exec > >(tee -a "$LOG") 2>&1
python3 scripts/ios/project.py
xcodebuild -project apps/ios/FnCPN.xcodeproj -scheme FnCPN -configuration Debug \
    -sdk "$SDK" -derivedDataPath "$ROOT/.dev/ios/DerivedData" CODE_SIGNING_ALLOWED=NO build
APP="$ROOT/.dev/ios/DerivedData/Build/Products/Debug-$SDK/FnCPN.app"
if [[ $SDK == iphonesimulator ]]; then
    codesign --force --sign - "$APP/PlugIns/PacketTunnel.appex"
    codesign --force --sign - "$APP"
fi
if [[ -n $INSTALL ]]; then
    xcrun simctl install "$INSTALL" "$APP"
    xcrun simctl launch "$INSTALL" cn.rectcircle.fncpn
fi
printf 'Development app: %s\nLog: %s\n' "$APP" "$LOG"
