#!/usr/bin/env bash
# Shared contract for complete macOS App bundles. Source from a UI builder.
macos_app_init() {
  APP_OUTPUT=""
  DAEMON_BINARY=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --output|--daemon)
        [ "$#" -ge 2 ] && [ -n "$2" ] || { echo "Missing value for $1" >&2; return 2; }
        case "$1" in --output) APP_OUTPUT="$2" ;; --daemon) DAEMON_BINARY="$2" ;; esac
        shift 2 ;;
      --help|-h) echo "Usage: $0 [--output /path/FnCPN.app] [--daemon /path/fncpn]"; exit 0 ;;
      *) echo "Unknown option: $1" >&2; return 2 ;;
    esac
  done
  [ "$(uname -s)" = Darwin ] || { echo 'Build macOS Apps on macOS' >&2; return 1; }
  export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-13.0}"
  export CGO_CFLAGS="${CGO_CFLAGS:-} -mmacosx-version-min=$MACOSX_DEPLOYMENT_TARGET"
  export CGO_LDFLAGS="${CGO_LDFLAGS:-} -mmacosx-version-min=$MACOSX_DEPLOYMENT_TARGET"
  export COPYFILE_DISABLE=1
  export COPY_EXTENDED_ATTRIBUTES_DISABLE=1
  ARCH="${ARCH:-arm64}"
  case "$ARCH" in
    arm64) GO_ARCH=arm64; MACH_ARCH=arm64 ;;
    amd64|x64) ARCH=x64; GO_ARCH=amd64; MACH_ARCH=x86_64 ;;
    *) echo "Unsupported macOS architecture: $ARCH" >&2; return 2 ;;
  esac
  source "$ROOT_DIR/scripts/release-version.sh"
  APP_OUTPUT="${APP_OUTPUT:-$ROOT_DIR/dist/macos-ui/$UI/$ARCH/FnCPN.app}"
  APP_OUTPUT="$(python3 -c 'import os,sys; print(os.path.abspath(sys.argv[1]))' "$APP_OUTPUT")"
  # An explicit output must be an absent .app path: never delete an installed or
  # user-supplied bundle. Default development outputs can be replaced safely.
  case "$APP_OUTPUT" in *.app) ;; *) echo '--output must name an App bundle' >&2; return 2 ;; esac
  if [ -e "$APP_OUTPUT" ]; then
    if [ "$APP_OUTPUT" = "$ROOT_DIR/dist/macos-ui/$UI/$ARCH/FnCPN.app" ]; then
      rm -rf "$APP_OUTPUT"
    else
      echo "App output already exists: $APP_OUTPUT" >&2; return 1
    fi
  fi
  mkdir -p "$(dirname "$APP_OUTPUT")"
  APP_WORK="$(mktemp -d "${TMPDIR:-/tmp}/fncpn-macos-app.XXXXXX")"
  trap 'rm -rf "$APP_WORK"' EXIT
  APP="$APP_WORK/FnCPN.app"
  mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
}

macos_app_finish() {
  local resources="$APP/Contents/Resources"
  if [ -n "$DAEMON_BINARY" ]; then
    cp -X "$DAEMON_BINARY" "$resources/fncpn"
  else
    (cd "$ROOT_DIR" && CGO_ENABLED=1 GOOS=darwin GOARCH="$GO_ARCH" CC="clang -arch $MACH_ARCH" go build -trimpath \
      -ldflags "-s -w -X github.com/rectcircle/fn-connect-private-network/internal/version.Revision=$REVISION" \
      -o "$resources/fncpn" ./cmd/fncpn)
  fi
  cp -R "$ROOT_DIR/assets/branding" "$APP_WORK/assets"
  swift "$ROOT_DIR/scripts/generate-icons.swift" "$APP_WORK/assets"
  iconutil -c icns "$APP_WORK/assets/AppIcon.iconset"
  cp -X "$APP_WORK/assets/"Status-*.png "$resources/"
  cp -X "$APP_WORK/assets/AppIcon.icns" "$resources/"
  cp -R "$ROOT_DIR/apps/macos/packaging/en.lproj" "$ROOT_DIR/apps/macos/packaging/zh-Hans.lproj" "$resources/"
  cp -X "$ROOT_DIR/LICENSE" "$ROOT_DIR/THIRD_PARTY_NOTICES.md" "$resources/"
  cp -X "$ROOT_DIR/apps/macos/packaging/install-prompt.applescript" "$ROOT_DIR/apps/macos/packaging/install-prompt.plist" "$resources/"
  python3 - "$ROOT_DIR" "$APP" "$APP_OUTPUT" "$VERSION" "$BUILD_NUMBER" "$MACOSX_DEPLOYMENT_TARGET" <<'PY'
import pathlib, plistlib, sys
root, app, final_app = map(pathlib.Path, sys.argv[1:4])
path = app/'Contents/Info.plist'
info = plistlib.loads(path.read_bytes(), fmt=plistlib.FMT_XML) if path.exists() else {}
# Preserve runtime-specific bundle keys, but share product identity and metadata.
info.update(plistlib.loads((root/'apps/macos/packaging/Info.plist').read_bytes()))
info['FnCPNProductVersion'] = info['CFBundleShortVersionString'] = sys.argv[4]
info['CFBundleVersion'] = sys.argv[5]
info['LSMinimumSystemVersion'] = sys.argv[6]
path.write_bytes(plistlib.dumps(info))
agent = plistlib.loads((root/'apps/macos/packaging/cn.rectcircle.fncpn.client.plist').read_bytes())
agent['ProgramArguments'][0] = str(final_app/'Contents/Resources/fncpn')
(app/'Contents/Resources/client-agent.plist').write_bytes(plistlib.dumps(agent))
PY
  for binary in "$APP/Contents/MacOS/FnCPN" "$resources/fncpn"; do
    [ "$(lipo -archs "$binary")" = "$MACH_ARCH" ] || { echo "Unexpected architecture: $binary" >&2; return 1; }
  done
  local minos
  minos="$(otool -l "$APP/Contents/MacOS/FnCPN" | awk '/minos/{print $2; exit}')"
  [ "$minos" = "$MACOSX_DEPLOYMENT_TARGET" ] || { echo "UI minimum macOS $minos differs from $MACOSX_DEPLOYMENT_TARGET" >&2; return 1; }
  codesign --force --sign - "$resources/fncpn"
  codesign --force --sign - "$APP"
  codesign --verify --deep --strict "$APP"
  mv "$APP" "$APP_OUTPUT"
  printf '%s\n' "$APP_OUTPUT"
}
