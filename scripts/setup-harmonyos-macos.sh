#!/bin/bash
# Shared HarmonyOS tools; project-local logs only.
set -Eeuo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
if [[ ${1:-} == --help || ${1:-} == -h ]]; then
    exec python3 "$ROOT/scripts/harmonyos/setup_macos.py" --help
fi
mkdir -p "$ROOT/.dev/logs"
LOG="$ROOT/.dev/logs/setup-harmonyos-macos-$(date +%Y%m%d-%H%M%S)-$$.log"
exec > >(tee -a "$LOG") 2>&1
trap 'echo "环境准备未完成；修复后可重跑。日志：$LOG" >&2' ERR
python3 "$ROOT/scripts/harmonyos/setup_macos.py" "$@"
