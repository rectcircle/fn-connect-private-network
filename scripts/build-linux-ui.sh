#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT_DIR/scripts/release-version.sh"
[ "$(uname -s)" = Linux ] || { echo 'Build UI in the matching Linux target container.' >&2; exit 1; }
TARGET="${TARGET:-deb}"
python3 "$ROOT_DIR/scripts/linux-build-environment.py" "$TARGET"
export FNCPN_ARCH="${ARCH:-$(go env GOARCH)}"
[ "$FNCPN_ARCH" = "$(go env GOARCH)" ] || { echo 'Use a native runner for each UI architecture.' >&2; exit 1; }
case "$FNCPN_ARCH" in arm64|amd64) ;; *) exit 1 ;; esac
python3 "$ROOT_DIR/scripts/linux_support.py" "$TARGET" "$FNCPN_ARCH" >/dev/null
OUT="${DIST_DIR:-$ROOT_DIR/dist/linux}/$TARGET/$FNCPN_ARCH"
mkdir -p "$OUT"
export FNCPN_VERSION="$VERSION"
WAILS="${WAILS:-$(go env GOPATH)/bin/wails}"
(cd "$ROOT_DIR/ui" && "$WAILS" build -clean -nopackage -platform "linux/$FNCPN_ARCH" -tags webkit2_41 -o fncpn-ui -ldflags "-s -w -X github.com/rectcircle/fn-connect-private-network/internal/version.Revision=$REVISION")
export FNCPN_UI_BINARY="$OUT/fncpn-ui"
cp "$ROOT_DIR/ui/build/bin/fncpn-ui" "$FNCPN_UI_BINARY"
export NFPM="${NFPM:-$(go env GOPATH)/bin/nfpm}"
python3 "$ROOT_DIR/scripts/linux-package.py" ui "$TARGET" "$FNCPN_ARCH" "$FNCPN_UI_BINARY" "$OUT"
if [ "${DEFER_LINUX_MANIFEST:-0}" != 1 ]; then
 python3 "$ROOT_DIR/scripts/linux-build-manifest.py" "$OUT" "$TARGET" "$FNCPN_ARCH"
fi
