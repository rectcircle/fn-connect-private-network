#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PACKAGE_SOURCE="${ROOT_DIR}/packaging/fnos"
BUILD_DIR="${TMPDIR:-/tmp}/fncpn-fpk-${UID}"
DIST_DIR="${ROOT_DIR}/dist"
VERSION="$(sed -n 's/^version[[:space:]]*=[[:space:]]*//p' "${PACKAGE_SOURCE}/manifest" | head -n 1)"

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
    cp "${ROOT_DIR}/packaging/assets/ICON.PNG" "$stage/ICON.PNG"
    cp "${ROOT_DIR}/packaging/assets/ICON_256.PNG" "$stage/ICON_256.PNG"
    cp "${ROOT_DIR}/packaging/assets/ICON.PNG" "$stage/app/ui/images/icon_64.png"
    cp "${ROOT_DIR}/packaging/assets/ICON_256.PNG" "$stage/app/ui/images/icon_256.png"
    sed "s/^platform=.*/platform=${platform}/" "$stage/manifest" > "$stage/manifest.tmp"
    mv "$stage/manifest.tmp" "$stage/manifest"

    (
        cd "$ROOT_DIR"
        CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build \
            -trimpath \
            -ldflags "-s -w -X main.version=${VERSION}" \
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
if [ "$(uname -s)" = "Darwin" ]; then
    swift "${ROOT_DIR}/scripts/generate-icons.swift" "${ROOT_DIR}/packaging/assets"
fi
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR" "$DIST_DIR"

(cd "$ROOT_DIR" && go test ./...)
build_target x86 amd64
build_target arm arm64

ls -lh "${DIST_DIR}/fncpn-${VERSION}-"*.fpk
