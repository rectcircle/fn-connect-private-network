#!/bin/bash
set -euo pipefail
export PATH=/opt/go/bin:/opt/tools:$PATH
export GOTOOLCHAIN=local GOBIN=/opt/tools
cd /workspace
git config --global --add safe.directory /workspace
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.40.0
export NFPM=/opt/tools/nfpm TARGET=deb-loong64 ARCH=loong64 FRONTEND_PREBUILT=1
bash scripts/build-linux-core.sh
bash scripts/build-linux-ui.sh
python3 scripts/check-linux-packages.py dist/linux/deb-loong64/loong64 deb-loong64 loong64
python3 scripts/loongarch/upgrade-fixture.py
