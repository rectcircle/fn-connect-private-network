#!/bin/bash
# Runs only in a disposable candidate image; never publishes FnCPN. Runtime checks use a separate clean container.
set -euo pipefail
cd /workspace
export PATH=/opt/go/bin:/opt/tools:$PATH GOTOOLCHAIN=local
export ARCH=loong64 FRONTEND_PREBUILT=1 DEFER_LINUX_MANIFEST=1
export RELEASE="${RELEASE:-0}"
export NFPM=/opt/tools/nfpm DIST_DIR="${DIST_DIR:-/workspace/dist/linux}"
out="$DIST_DIR/$TARGET/loong64"
mkdir -p "$out"
case "$1" in
 bootstrap|runtime-bootstrap)
  if [ "$TARGET" = openeuler-loong64 ] || [ "$TARGET" = rpm-loong64 ]; then
   bash scripts/loongarch/configure-openeuler-repos.sh
   dnf --disablerepo='*source*' --disablerepo=debuginfo --setopt=timeout=60 --setopt=minrate=100 --setopt=max_parallel_downloads=3 --setopt=keepcache=True --setopt=retries=2 install -y git ca-certificates curl python3 tar xz
  elif command -v dnf >/dev/null; then
   dnf --setopt=timeout=20 --setopt=retries=2 --setopt=ip_resolve=4 install -y --setopt=install_weak_deps=False git ca-certificates curl python3 tar xz
  elif command -v microdnf >/dev/null; then
   microdnf install -y git ca-certificates curl python3 tar xz dnf
  elif command -v pacman >/dev/null; then
   pacman -Syu --noconfirm --needed git ca-certificates curl python tar xz libarchive
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

  ;;
 *) exit 2 ;;
esac
