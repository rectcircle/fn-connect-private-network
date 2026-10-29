#!/bin/bash
# Shared loong64 build entry; target profiles supply distribution/package details.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"
export PATH=/opt/go/bin:/opt/tools:$PATH GOTOOLCHAIN=local
export TARGET="${TARGET:?Set the Linux target profile}" ARCH=loong64
export NFPM="${NFPM:-/opt/tools/nfpm}" FRONTEND_PREBUILT=1 DEFER_LINUX_MANIFEST=1
out="${DIST_DIR:-$ROOT_DIR/dist/linux}/$TARGET/loong64"
git config --global --add safe.directory "$ROOT_DIR"
test -x "$NFPM"
bash scripts/build-linux-core.sh
bash scripts/build-linux-ui.sh
readelf -l "$out/fncpn-ui"
ldd -r "$out/fncpn-ui" > "$out/ldd.txt" 2>&1
cat "$out/ldd.txt"
if grep -E 'not found|undefined symbol' "$out/ldd.txt"; then exit 1; fi
"$out/fncpn" version
if [ "${FULL_ACCEPTANCE:-0}" = 1 ]; then
 python3 scripts/loongarch/upgrade-fixture.py "$TARGET" "$out"
fi
if [ "${RELEASE:-0}" = 1 ] && [ "$(python3 -c 'import sys; sys.path.insert(0,"scripts"); from linux_support import package_profile; print(package_profile(sys.argv[1])["format"])' "$TARGET")" = rpm ]; then
 packages=("$out/"*.rpm)
 if [ "${FULL_ACCEPTANCE:-0}" = 1 ]; then packages+=("$out/upgrade-fixture/"*.rpm); fi
 python3 scripts/rpm_signing.py sign "${packages[@]}"
fi
# Includes package structure/architecture verification and final SHA256/ABI evidence.
python3 scripts/linux-build-manifest.py "$out" "$TARGET" loong64
