#!/usr/bin/env bash

set -euo pipefail
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-13.0}"
export COPYFILE_DISABLE=1
export COPY_EXTENDED_ATTRIBUTES_DISABLE=1

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${DIST_DIR:-${ROOT_DIR}/dist}"
source "${ROOT_DIR}/scripts/release-version.sh"
ARCH="${ARCH:-arm64}"
if [ "$ARCH" != arm64 ] || [ "$(uname -m)" != arm64 ]; then
  echo "The macOS release requires an Apple Silicon build host and arm64 target" >&2; exit 1
fi
if [ "${RELEASE:-0}" = 1 ]; then
 for artifact in "${DIST_DIR}/FnCPN-${VERSION}-${ARCH}-unsigned.pkg"; do
  if [ -e "$artifact" ]; then echo "Refusing to replace release artifact: $artifact" >&2; exit 1; fi
 done
fi
BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/fncpn-macos-pkg.XXXXXX")"
trap 'rm -rf "$BUILD_DIR"' EXIT
PAYLOAD="${BUILD_DIR}/root"
SCRIPTS="${BUILD_DIR}/scripts"
TOOL_DIR="${PAYLOAD}/Library/PrivilegedHelperTools/cn.rectcircle.fncpn"
APP_DIR="${PAYLOAD}/Applications/FnCPN.app"

if [ "$(uname -s)" != "Darwin" ]; then
  echo "macOS package must be built on macOS" >&2
  exit 1
fi
for tool in go python3 swiftc pkgbuild pkgutil plutil codesign bsdtar lsbom mkbom gzip iconutil otool; do
  command -v "$tool" >/dev/null || {
    echo "missing build dependency: $tool" >&2
    exit 1
  }
done

mkdir -p \
  "$TOOL_DIR" \
  "${PAYLOAD}/Library/LaunchDaemons" \
  "${PAYLOAD}/Library/LaunchAgents" \
  "${PAYLOAD}/usr/local/bin" \
  "${APP_DIR}/Contents/MacOS" \
  "${APP_DIR}/Contents/Resources" \
  "$SCRIPTS" \
  "$DIST_DIR"
ASSET_DIR="${BUILD_DIR}/assets"
cp -R "${ROOT_DIR}/packaging/assets" "$ASSET_DIR"
swift "${ROOT_DIR}/scripts/generate-icons.swift" "$ASSET_DIR"
iconutil -c icns "${ASSET_DIR}/AppIcon.iconset"

(
  cd "$ROOT_DIR"
  go test ./...
  CGO_ENABLED=1 GOOS=darwin GOARCH="$ARCH" go build \
    -trimpath \
    -ldflags "-s -w -X github.com/rectcircle/fn-connect-private-network/internal/version.Revision=${REVISION}" \
    -o "${TOOL_DIR}/fncpn" \
    ./cmd/fncpn
)

swiftc \
  -target "${ARCH}-apple-macos${MACOSX_DEPLOYMENT_TARGET}" \
  -O \
  -parse-as-library \
  -framework AppKit \
  -framework ServiceManagement \
  -framework UserNotifications \
  -framework WebKit \
  "${ROOT_DIR}/platform/macos/FnCPNApp.swift" \
  -o "${APP_DIR}/Contents/MacOS/FnCPN"

minos="$(otool -l "${APP_DIR}/Contents/MacOS/FnCPN" | awk '/minos/{print $2; exit}')"
if [ "$minos" != "$MACOSX_DEPLOYMENT_TARGET" ]; then
  echo "Swift binary minimum macOS $minos differs from deployment target $MACOSX_DEPLOYMENT_TARGET" >&2
  exit 1
fi

cp -R "${ROOT_DIR}/packaging/macos/en.lproj" "${ROOT_DIR}/packaging/macos/zh-Hans.lproj" "${APP_DIR}/Contents/Resources/"
cp "${ROOT_DIR}/LICENSE" "${ROOT_DIR}/THIRD_PARTY_NOTICES.md" "${APP_DIR}/Contents/Resources/"
cp -X "${ROOT_DIR}/packaging/macos/Info.plist" "${APP_DIR}/Contents/"
plutil -replace FnCPNProductVersion -string "$VERSION" \
  "${APP_DIR}/Contents/Info.plist"
plutil -replace CFBundleVersion -string "$BUILD_NUMBER" \
  "${APP_DIR}/Contents/Info.plist"
cp -X "${ASSET_DIR}"/Status-*.png "${APP_DIR}/Contents/Resources/"
cp -X "${ASSET_DIR}/AppIcon.icns" \
  "${APP_DIR}/Contents/Resources/AppIcon.icns"
cp -X "${ROOT_DIR}/packaging/macos/cn.rectcircle.fncpn.privileged.plist" \
  "${PAYLOAD}/Library/LaunchDaemons/"
# The client LaunchAgent is user-managed: ship its template inside the app bundle
# and let the app install it into ~/Library/LaunchAgents on first launch.
mkdir -p "${APP_DIR}/Contents/Resources"
cp -X "${ROOT_DIR}/packaging/macos/cn.rectcircle.fncpn.client.plist" \
  "${APP_DIR}/Contents/Resources/client-agent.plist"
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
  "${APP_DIR}/Contents/Resources/client-agent.plist" \
  "${PAYLOAD}/Library/LaunchDaemons/cn.rectcircle.fncpn.privileged.plist"

codesign --force --sign - "${TOOL_DIR}/fncpn"
codesign --force --sign - "${APP_DIR}"
codesign --verify --strict "${TOOL_DIR}/fncpn"
codesign --verify --deep --strict "${APP_DIR}"
find "$PAYLOAD" -name '._*' -delete
if find "$PAYLOAD" -name '._*' -print -quit | grep -q .; then
  echo "AppleDouble file remains in package payload" >&2
  exit 1
fi
ln -s "/Library/PrivilegedHelperTools/cn.rectcircle.fncpn/fncpn" \
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
  --identifier cn.rectcircle.fncpn \
  --version "$PACKAGE_VERSION" \
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
