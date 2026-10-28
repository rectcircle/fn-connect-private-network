#!/bin/bash
set -euo pipefail
export PATH=/opt/go/bin:$PATH
export GOTOOLCHAIN=local
test "$(go env GOARCH)" = loong64
cd /workspace
sh scripts/test-linux-activation.sh
cd ui
# Equivalent production Go tags from Wails v2's builder. Frontend assets and
# checked-in bindings are prepared by the host; no Wails CLI fork is needed.
go test -tags webkit2_41 -timeout 10m ./...
CGO_ENABLED=1 go build -buildvcs=false -trimpath -tags desktop,production,webkit2_41 \
  -ldflags "-X github.com/rectcircle/fn-connect-private-network/internal/version.Revision=$VALIDATION_COMMIT" \
  -o /workspace/dist/loongarch-validation/fncpn-ui .
readelf -l /workspace/dist/loongarch-validation/fncpn-ui
ldd -r /workspace/dist/loongarch-validation/fncpn-ui
