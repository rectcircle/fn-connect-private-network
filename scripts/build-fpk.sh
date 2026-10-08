#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PACKAGE_SOURCE="${ROOT_DIR}/packaging/fnos"
BUILD_DIR="${TMPDIR:-/tmp}/fncpn-fpk-${UID}"
DIST_DIR="${DIST_DIR:-${ROOT_DIR}/dist}"
source "${ROOT_DIR}/scripts/release-version.sh"
if [ "${RELEASE:-0}" = 1 ]; then
 for artifact in "${DIST_DIR}/fncpn-${VERSION}-x86.fpk" "${DIST_DIR}/fncpn-${VERSION}-arm.fpk"; do
  if [ -e "$artifact" ]; then echo "Refusing to replace release artifact: $artifact" >&2; exit 1; fi
 done
fi

resolve_fnpack() {
    if [ -n "${FNPACK:-}" ]; then
        printf '%s\n' "$FNPACK"
        return
    fi
    if command -v fnpack >/dev/null 2>&1; then
        command -v fnpack
        return
    fi
    echo "fnpack is required; set FNPACK or install it in PATH" >&2
    return 1
}

build_target() {
    local platform="$1"
    local goarch="$2"
    local stage="${BUILD_DIR}/fnos-${platform}"
    rm -rf "$stage"
    mkdir -p "$stage/app/server" "$stage/app/ui/images" "$DIST_DIR"
    cp -R "${PACKAGE_SOURCE}/." "$stage/"
    cp "${ASSET_DIR}/ICON.PNG" "$stage/ICON.PNG"
    cp "${ASSET_DIR}/ICON_256.PNG" "$stage/ICON_256.PNG"
    cp "${ASSET_DIR}/DesktopIcon64.png" "$stage/app/ui/images/icon_64.png"
    cp "${ASSET_DIR}/DesktopIcon256.png" "$stage/app/ui/images/icon_256.png"
    sed -e "s/^platform=.*/platform=${platform}/" -e "s/^version=.*/version=${VERSION}/" "$stage/manifest" > "$stage/manifest.tmp"
    mv "$stage/manifest.tmp" "$stage/manifest"

    (
        cd "$ROOT_DIR"
        CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build \
            -trimpath \
            -ldflags "-s -w -X github.com/rectcircle/fn-connect-private-network/internal/version.Revision=${REVISION}" \
            -o "$stage/app/server/fncpn" \
            ./cmd/fncpn
    )
    chmod 0755 "$stage/app/server/fncpn" "$stage/cmd/"*
    (
        cd "$stage"
        "$FNPACK_BIN" build
    )
    local artifact
    artifact="$(find "$stage" -maxdepth 1 -type f -name '*.fpk' -print -quit)"
    if [ -z "$artifact" ]; then
        echo "fnpack produced no package for ${platform}" >&2
        return 1
    fi
    mv "$artifact" "${DIST_DIR}/fncpn-${VERSION}-${platform}.fpk"
}

FNPACK_BIN="$(resolve_fnpack)"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR" "$DIST_DIR"
ASSET_DIR="${BUILD_DIR}/assets"
cp -R "${ROOT_DIR}/packaging/assets" "$ASSET_DIR"
if [ "$(uname -s)" = "Darwin" ]; then
    swift "${ROOT_DIR}/scripts/generate-icons.swift" "$ASSET_DIR"
fi

(cd "$ROOT_DIR" && go test ./...)
build_target x86 amd64
build_target arm arm64

ls -lh "${DIST_DIR}/fncpn-${VERSION}-"*.fpk
