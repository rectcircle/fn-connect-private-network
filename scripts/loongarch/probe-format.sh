#!/bin/bash
# Runs only in a disposable candidate image; never publishes or installs FnCPN.
set -euo pipefail
cd /workspace
export PATH=/opt/go/bin:/opt/tools:$PATH GOTOOLCHAIN=local
export ARCH=loong64 FRONTEND_PREBUILT=1 RELEASE=0 DEFER_LINUX_MANIFEST=1
export NFPM=/opt/tools/nfpm DIST_DIR=/workspace/dist/loongarch-formats
out="$DIST_DIR/$TARGET/loong64"
mkdir -p "$out"
case "$1" in
 bootstrap)
  if command -v dnf >/dev/null; then
   dnf install -y --setopt=install_weak_deps=False git ca-certificates curl python3 tar xz
  elif command -v microdnf >/dev/null; then
   microdnf install -y git ca-certificates curl python3 tar xz dnf
  elif command -v pacman >/dev/null; then
   pacman -Syu --noconfirm --needed git ca-certificates curl python tar xz
  else
   echo 'Candidate image has no supported package manager' >&2; exit 1
  fi
  git config --global --add safe.directory /workspace
  python3 scripts/linux-ci-dependencies.py "$TARGET"
  cat /etc/os-release
  if command -v rpm >/dev/null; then rpm -qa | sort > "$out/installed-packages.txt"; else pacman -Q > "$out/installed-packages.txt"; fi
  ;;
 toolchain)
  test "$(go env GOARCH)" = loong64
  test -r /lib64/ld-linux-loongarch-lp64d.so.1
  go version | tee "$out/go-version.txt"
  gcc --version > "$out/gcc-version.txt"
  gcc -dumpmachine | tee "$out/compiler-target.txt"
  grep -q '^loongarch64' "$out/compiler-target.txt"
  pkg-config --modversion gtk+-3.0 webkit2gtk-4.1 ayatana-appindicator3-0.1 | tee "$out/ui-dependencies.txt"
  printf '#include <stdio.h>\nint main(void){puts("C lp64d runtime works");return 0;}\n' > "$out/probe.c"
  gcc -mabi=lp64d "$out/probe.c" -o "$out/c-probe"
  "$out/c-probe"
  printf 'package main\nimport "fmt"\nfunc main(){fmt.Println("Go loong64 runtime works")}\n' > "$out/probe.go"
  go build -o "$out/go-probe" "$out/probe.go"
  "$out/go-probe"
  ;;
 build)
  bash scripts/build-linux-core.sh
  bash scripts/build-linux-ui.sh
  ldd -r "$out/fncpn-ui" > "$out/ldd.txt" 2>&1
  cat "$out/ldd.txt"
  if grep -E 'not found|undefined symbol' "$out/ldd.txt"; then exit 1; fi
  "$out/fncpn" version
  python3 scripts/linux-build-manifest.py "$out" "$TARGET" loong64
  python3 scripts/check-linux-packages.py "$out" "$TARGET" loong64
  # Query the native package manager as well as our shared verifier.
  if command -v rpm >/dev/null; then
   for package in "$out/"*.rpm; do
    test "$(rpm -qp --qf '%{ARCH}' "$package")" = loongarch64
   done
  else
   for package in "$out/"*.pkg.tar.zst; do
    bsdtar -xOf "$package" .PKGINFO | grep -qx 'arch = loong64'
   done
  fi
  python3 - "$out" <<'PY'
import json, pathlib, sys
out = pathlib.Path(sys.argv[1])
build = json.loads((out / 'build-manifest.json').read_text())
(out / 'validation.json').write_text(json.dumps({'passed': True, 'scope': 'toolchain, dependencies, compilation and package structure only',
    'experimental': True, 'commit': build['commit'], 'productVersion': build['productVersion'], 'artifacts': build['artifacts'],
    'notValidated': ['installation and service lifecycle', 'UI startup', 'kernel/network', 'RPM signature']}, indent=2)+'\n')
PY
  ;;
 *) exit 2 ;;
esac
