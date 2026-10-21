#!/usr/bin/env bash
# Build a complete Wails App, or delegate PKG creation to the common installer.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Dispatch before building: the package entry invokes this script in App mode.
if [ "${1:-}" = --pkg ]; then
  shift
  exec "$ROOT_DIR/scripts/build-macos-pkg.sh" --ui wails "$@"
fi
UI=wails
source "$ROOT_DIR/scripts/macos-app-common.sh"
macos_app_init "$@"
WAILS="${WAILS:-$(go env GOPATH)/bin/wails}"
[ -x "$WAILS" ] || { echo 'Install the pinned CLI: go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0' >&2; exit 1; }
(
  cd "$ROOT_DIR/ui"
  "$WAILS" build -clean -platform "darwin/$GO_ARCH" \
    -ldflags "-X github.com/rectcircle/fn-connect-private-network/internal/version.Revision=$REVISION"
)
# Copy the complete bundle, including any Wails runtime resources.
cp -R "$ROOT_DIR/ui/build/bin/FnCPN Wails.app/." "$APP/"
mv "$APP/Contents/MacOS/FnCPN Wails" "$APP/Contents/MacOS/FnCPN"
macos_app_finish
