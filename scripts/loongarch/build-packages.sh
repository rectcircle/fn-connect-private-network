#!/bin/bash
set -euo pipefail
export PATH=/opt/go/bin:/opt/tools:$PATH
export GOTOOLCHAIN=local GOBIN=/opt/tools
cd /workspace
git config --global --add safe.directory /workspace
test -x /opt/tools/nfpm
export NFPM=/opt/tools/nfpm TARGET=deb-loong64 ARCH=loong64 FRONTEND_PREBUILT=1
export FULL_ACCEPTANCE="${FULL_VALIDATION:-0}"
bash scripts/loongarch/build-target.sh
