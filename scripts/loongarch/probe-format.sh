#!/bin/bash
# Runs only in a disposable candidate image; never publishes FnCPN. Runtime checks use a separate clean container.
set -euo pipefail
cd /workspace
export PATH=/opt/go/bin:/opt/tools:$PATH GOTOOLCHAIN=local
export ARCH=loong64 FRONTEND_PREBUILT=1 DEFER_LINUX_MANIFEST=1
export RELEASE="${RELEASE:-0}"
export NFPM=/opt/tools/nfpm DIST_DIR="${DIST_DIR:-/workspace/dist/loongarch-formats}"
out="$DIST_DIR/$TARGET/loong64"
mkdir -p "$out"
case "$1" in
 bootstrap|runtime-bootstrap)
  if [ "$TARGET" = openeuler-loong64 ] || [ "$TARGET" = rpm-loong64 ]; then
   # Use the pinned release's published base URLs and retain package signatures.
   sed -i -e '/^metalink=/d' -e 's|gpgkey=http:|gpgkey=https:|' /etc/yum.repos.d/openEuler.repo
   dnf --disablerepo='*source*' --disablerepo=debuginfo --setopt=timeout=30 --setopt=retries=2 install -y git ca-certificates curl python3 tar xz
  elif command -v dnf >/dev/null; then
   dnf --setopt=timeout=20 --setopt=retries=2 --setopt=ip_resolve=4 install -y --setopt=install_weak_deps=False git ca-certificates curl python3 tar xz
  elif command -v microdnf >/dev/null; then
   microdnf install -y git ca-certificates curl python3 tar xz dnf
  elif command -v pacman >/dev/null; then
   pacman -Syu --noconfirm --needed git ca-certificates curl python tar xz
  elif command -v apt-get >/dev/null && command -v rpm >/dev/null; then
   # Activate the mirror already supplied by this pinned ALT image, preserving
   # its [sisyphus-loongarch64] signature/key identity and repository branch.
   for file in /etc/apt/sources.list /etc/apt/sources.list.d/*.list; do
    [ ! -f "$file" ] || sed -i 's/^rpm /#rpm /' "$file"
   done
   grep -E '^#rpm \[sisyphus-loongarch64\] http://mirror.yandex.ru/altlinux ports/loongarch64/Sisyphus/(loongarch64|noarch) classic$' /etc/apt/sources.list.d/yandex.list | sed 's/^#//' > /etc/apt/sources.list.d/fncpn-yandex.list
   test "$(grep -c '^rpm ' /etc/apt/sources.list.d/fncpn-yandex.list)" = 2
   apt-get -o Acquire::Retries=2 -o Acquire::http::Timeout=20 update
   apt-get -o Acquire::Retries=2 -o Acquire::http::Timeout=20 install -y git ca-certificates curl python3-base tar xz
  elif command -v apt-get >/dev/null; then
   apt-get update
   apt-get install -y --no-install-recommends git ca-certificates curl python3 tar xz-utils
  else
   echo 'Candidate image has no supported package manager' >&2; exit 1
  fi
  if [ "$TARGET" = openeuler-loong64 ] || [ "$TARGET" = rpm-loong64 ]; then
   python3 - <<'PYREPO'
import configparser
from pathlib import Path
path = Path('/etc/yum.repos.d/openEuler.repo')
config = configparser.ConfigParser(interpolation=None)
config.read(path)
for section in config.sections():
    if 'source' in section or section == 'debuginfo':
        config[section]['enabled'] = '0'
with path.open('w') as output:
    config.write(output)
PYREPO
  fi
  git config --global --add safe.directory /workspace
  if [ "$1" = bootstrap ]; then python3 scripts/linux-ci-dependencies.py "$TARGET"; fi
  cat /etc/os-release
  if command -v rpm >/dev/null; then rpm -qa | sort > "$out/installed-packages.txt"; elif command -v dpkg-query >/dev/null; then dpkg-query -W > "$out/installed-packages.txt"; else pacman -Q > "$out/installed-packages.txt"; fi
  ;;
 toolchain)
  test "$(go env GOARCH)" = loong64
  test -r /lib64/ld-linux-loongarch-lp64d.so.1
  go version | tee "$out/go-version.txt"
  gcc --version > "$out/gcc-version.txt"
  gcc -dumpmachine | tee "$out/compiler-target.txt"
  grep -q '^loongarch64' "$out/compiler-target.txt"
  indicator=$(python3 -c 'import sys; sys.path.insert(0,"scripts"); from linux_support import package_profile; print(package_profile(sys.argv[1]).get("indicatorPkgConfig","ayatana-appindicator3-0.1"))' "$TARGET")
  pkg-config --modversion gtk+-3.0 webkit2gtk-4.1 "$indicator" | tee "$out/ui-dependencies.txt"
  printf '#include <stdio.h>\nint main(void){puts("C lp64d runtime works");return 0;}\n' > "$out/probe.c"
  gcc -mabi=lp64d "$out/probe.c" -o "$out/c-probe"
  "$out/c-probe"
  printf 'package main\nimport "fmt"\nfunc main(){fmt.Println("Go loong64 runtime works")}\n' > "$out/probe.go"
  go build -o "$out/go-probe" "$out/probe.go"
  "$out/go-probe"
  ;;
 build)
  bash scripts/loongarch/build-target.sh
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
