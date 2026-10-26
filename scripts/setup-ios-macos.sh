#!/bin/bash
# Explicit iOS development bootstrap; does not build a product or configure signing.
set -Eeuo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
if [[ ${1:-} == --help || ${1:-} == -h ]]; then
    python3 "$ROOT/scripts/ios/setup_macos.py" --help
    exit 0
fi
mkdir -p "$ROOT/.dev/logs"
LOG="$ROOT/.dev/logs/setup-ios-macos-$(date +%Y%m%d-%H%M%S)-$$.log"
exec > >(tee -a "$LOG") 2>&1
trap 'echo "iOS 环境准备失败；修复后可重跑。日志：$LOG" >&2' ERR
python3 "$ROOT/scripts/ios/setup_macos.py" "$@"
echo "日志：$LOG"
