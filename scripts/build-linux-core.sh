#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT_DIR/scripts/release-version.sh"
export FNCPN_ARCH="${ARCH:-arm64}"
case "$FNCPN_ARCH" in arm64|amd64|loong64) ;; *) echo 'ARCH must be arm64, amd64 or loong64' >&2; exit 1 ;; esac
TARGET="${TARGET:-deb}"
python3 "$ROOT_DIR/scripts/linux_support.py" "$TARGET" "$FNCPN_ARCH" >/dev/null
OUT="${DIST_DIR:-$ROOT_DIR/dist/linux}/$TARGET/$FNCPN_ARCH"
mkdir -p "$OUT"
export FNCPN_VERSION="$VERSION"
export FNCPN_CORE_BINARY="$OUT/fncpn"
(cd "$ROOT_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH="$FNCPN_ARCH" go build -trimpath -ldflags "-s -w -X github.com/rectcircle/fn-connect-private-network/internal/version.Revision=$REVISION" -o "$FNCPN_CORE_BINARY" ./cmd/fncpn)
if [ "${BINARY_ONLY:-0}" != 1 ]; then
 export NFPM="${NFPM:-$(go env GOPATH)/bin/nfpm}"
 python3 "$ROOT_DIR/scripts/linux-package.py" core "$TARGET" "$FNCPN_ARCH" "$FNCPN_CORE_BINARY" "$OUT"
fi
printf '%s\n' "$FNCPN_CORE_BINARY"
