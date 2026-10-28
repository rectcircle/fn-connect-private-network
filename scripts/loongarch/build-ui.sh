#!/bin/bash
set -euo pipefail
export PATH=/opt/go/bin:$PATH
export GOTOOLCHAIN=local
test "$(go env GOARCH)" = loong64
cd /workspace
sh scripts/test-linux-activation.sh
cd ui
go test -tags webkit2_41 -timeout 10m ./...
