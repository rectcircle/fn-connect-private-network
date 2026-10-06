#!/usr/bin/env bash

set -euo pipefail
export COPYFILE_DISABLE=1
export COPY_EXTENDED_ATTRIBUTES_DISABLE=1

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-0.1.25}"
BUILD_NUMBER="${BUILD_NUMBER:-25}"
ARCH="$(go env GOARCH)"
BUILD_DIR="${TMPDIR:-/tmp}/fncpn-macos-pkg-${UID}"
PAYLOAD="${BUILD_DIR}/root"
SCRIPTS="${BUILD_DIR}/scripts"
DIST_DIR="${ROOT_DIR}/dist"
TOOL_DIR="${PAYLOAD}/Library/PrivilegedHelperTools/com.rectcircle.fncpn"
APP_DIR="${PAYLOAD}/Applications/FnCPN.app"

if [ "$(uname -s)" != "Darwin" ]; then
  echo "macOS package must be built on macOS" >&2
  exit 1
fi
for tool in go swiftc pkgbuild pkgutil plutil codesign bsdtar lsbom mkbom gzip; do
  command -v "$tool" >/dev/null || {
    echo "missing build dependency: $tool" >&2
    exit 1
  }
done

rm -rf "$BUILD_DIR"
mkdir -p \
  "$TOOL_DIR" \
  "${PAYLOAD}/Library/LaunchDaemons" \
  "${PAYLOAD}/Library/LaunchAgents" \
  "${PAYLOAD}/usr/local/bin" \
  "${APP_DIR}/Contents/MacOS" \
  "${APP_DIR}/Contents/Resources" \
  "$SCRIPTS" \
  "$DIST_DIR"

(
  cd "$ROOT_DIR"
  go test ./...
  CGO_ENABLED=1 GOOS=darwin GOARCH="$ARCH" go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o "${TOOL_DIR}/fncpn" \
    ./cmd/fncpn
)

swiftc \
  -O \
  -parse-as-library \
  -framework AppKit \
  "${ROOT_DIR}/platform/macos/FnCPNApp.swift" \
  -o "${APP_DIR}/Contents/MacOS/FnCPN"

cp -X "${ROOT_DIR}/packaging/macos/Info.plist" "${APP_DIR}/Contents/"
plutil -replace CFBundleShortVersionString -string "$VERSION" \
  "${APP_DIR}/Contents/Info.plist"
plutil -replace CFBundleVersion -string "$BUILD_NUMBER" \
  "${APP_DIR}/Contents/Info.plist"
cp -X "${ROOT_DIR}/packaging/assets/ICON_256.PNG" \
  "${APP_DIR}/Contents/Resources/AppIcon.png"
cp -X "${ROOT_DIR}/packaging/macos/com.rectcircle.fncpn.privileged.plist" \
  "${PAYLOAD}/Library/LaunchDaemons/"
cp -X "${ROOT_DIR}/packaging/macos/com.rectcircle.fncpn.client.plist" \
  "${PAYLOAD}/Library/LaunchAgents/"
cp -X "${ROOT_DIR}/packaging/macos/scripts/preinstall" "$SCRIPTS/"
cp -X "${ROOT_DIR}/packaging/macos/scripts/postinstall" "$SCRIPTS/"
cp -X "${ROOT_DIR}/packaging/macos/uninstall.sh" "${TOOL_DIR}/"

chmod 0755 \
  "${TOOL_DIR}/fncpn" \
  "${TOOL_DIR}/uninstall.sh" \
  "${APP_DIR}/Contents/MacOS/FnCPN" \
  "${SCRIPTS}/preinstall" \
  "${SCRIPTS}/postinstall"
chmod 0644 \
  "${APP_DIR}/Contents/Info.plist" \
  "${PAYLOAD}/Library/LaunchDaemons/com.rectcircle.fncpn.privileged.plist" \
  "${PAYLOAD}/Library/LaunchAgents/com.rectcircle.fncpn.client.plist"

codesign --force --sign - "${TOOL_DIR}/fncpn"
codesign --force --sign - "${APP_DIR}"
find "$PAYLOAD" -name '._*' -delete
if find "$PAYLOAD" -name '._*' -print -quit | grep -q .; then
  echo "AppleDouble file remains in package payload" >&2
  exit 1
fi
ln -s "/Library/PrivilegedHelperTools/com.rectcircle.fncpn/fncpn" \
  "${PAYLOAD}/usr/local/bin/fncpn"

OUTPUT="${DIST_DIR}/FnCPN-${VERSION}-${ARCH}-unsigned.pkg"
RAW_OUTPUT="${BUILD_DIR}/raw.pkg"
EXPANDED_PACKAGE="${BUILD_DIR}/expanded"
BOM_LIST="${BUILD_DIR}/bom.list"
rm -f "$OUTPUT" "$RAW_OUTPUT"
# The recommended-ownership path can fail while flushing pkgbuild's temporary
# BOM. Preserve source ownership here, then normalize both archive formats below.
pkgbuild \
  --root "$PAYLOAD" \
  --scripts "$SCRIPTS" \
  --identifier com.rectcircle.fncpn \
  --version "$VERSION" \
  --install-location / \
  --ownership preserve \
  --filter '(^|/)\._[^/]*$' \
  --filter '(^|/)\.DS_Store$' \
  "$RAW_OUTPUT"

pkgutil --expand "$RAW_OUTPUT" "$EXPANDED_PACKAGE"
lsbom "$EXPANDED_PACKAGE/Bom" |
  awk -F '\t' -v OFS='\t' '
    $1 !~ /(^|\/)\._/ {
      if (NF < 3) exit 1
      $3 = "0/0"
      print
    }
  ' > "$BOM_LIST"
mkbom -i "$BOM_LIST" "$EXPANDED_PACKAGE/Bom.filtered"
mv "$EXPANDED_PACKAGE/Bom.filtered" "$EXPANDED_PACKAGE/Bom"
if lsbom "$EXPANDED_PACKAGE/Bom" | grep -Eq '(^|/)\._'; then
  echo "AppleDouble entry found in built package BOM" >&2
  exit 1
fi
if ! lsbom "$EXPANDED_PACKAGE/Bom" |
  awk -F '\t' 'NF < 3 || $3 != "0/0" {exit 1} END {if (NR == 0) exit 1}'; then
  echo "non-root owner found in built package BOM" >&2
  exit 1
fi
rm -f "$EXPANDED_PACKAGE/Payload"
(
  cd "$PAYLOAD"
  find . -print |
    bsdtar -cf - \
      --format cpio \
      --uid 0 \
      --gid 0 \
      --uname root \
      --gname wheel \
      --no-xattrs \
      --no-mac-metadata \
      --no-recursion \
      -T -
) | gzip -9 > "$EXPANDED_PACKAGE/Payload"
if ! bsdtar -tv --numeric-owner -f "$EXPANDED_PACKAGE/Payload" |
  awk 'NF < 4 || $3 != "0" || $4 != "0" {exit 1} END {if (NR == 0) exit 1}'; then
  echo "non-root owner found in built package payload" >&2
  exit 1
fi
pkgutil --flatten "$EXPANDED_PACKAGE" "$OUTPUT"

if pkgutil --payload-files "$OUTPUT" | grep -Eq '(^|/)\._'; then
  echo "AppleDouble file found in built package" >&2
  exit 1
fi

echo "$OUTPUT"
