#!/usr/bin/env bash
# Build a complete Swift/AppKit App using the shared macOS bundle contract.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UI=native
source "$ROOT_DIR/scripts/macos-app-common.sh"
macos_app_init "$@"
swiftc \
  -target "${MACH_ARCH}-apple-macos${MACOSX_DEPLOYMENT_TARGET}" \
  -O -parse-as-library \
  -framework AppKit -framework ServiceManagement -framework UserNotifications -framework WebKit \
  "$ROOT_DIR/platform/macos/FnCPNApp.swift" \
  -o "$APP/Contents/MacOS/FnCPN"
macos_app_finish
