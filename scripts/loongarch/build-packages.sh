#!/bin/bash
set -euo pipefail
export PATH=/opt/go/bin:/opt/tools:$PATH
export GOTOOLCHAIN=local GOBIN=/opt/tools
cd /workspace
git config --global --add safe.directory /workspace
test -x /opt/tools/nfpm
export NFPM=/opt/tools/nfpm TARGET=deb-loong64 ARCH=loong64 FRONTEND_PREBUILT=1
bash scripts/build-linux-core.sh
bash scripts/build-linux-ui.sh
readelf -l dist/linux/deb-loong64/loong64/fncpn-ui
ldd -r dist/linux/deb-loong64/loong64/fncpn-ui
python3 scripts/check-linux-packages.py dist/linux/deb-loong64/loong64 deb-loong64 loong64
if [ "${FULL_VALIDATION:-0}" = 1 ]; then
 python3 scripts/loongarch/upgrade-fixture.py
fi
