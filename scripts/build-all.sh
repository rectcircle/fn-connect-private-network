#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"${ROOT_DIR}/scripts/build-fpk.sh"
"${ROOT_DIR}/scripts/build-macos-pkg.sh"

(
  cd "${ROOT_DIR}/dist"
  shasum -a 256 \
    FnCPN-*-unsigned.pkg \
    fncpn-*-arm.fpk \
    fncpn-*-x86.fpk \
    > SHA256SUMS
)

cat "${ROOT_DIR}/dist/SHA256SUMS"
