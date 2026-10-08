#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/release-version.sh"
export DIST_DIR="${DIST_DIR:-${ROOT_DIR}/dist}"
"${ROOT_DIR}/scripts/build-fpk.sh"
"${ROOT_DIR}/scripts/build-macos-pkg.sh"
ARCH="${ARCH:-arm64}"
python3 "${ROOT_DIR}/scripts/release-manifest.py" "$DIST_DIR" "$VERSION" "$REVISION" "$ARCH"
