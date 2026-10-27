#!/bin/bash
set -Eeuo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
python3 -m unittest discover -s scripts/harmonyos -p '*_test.py'
go test ./apps/harmonyos/go ./apps/harmonyos/diagnostics/go ./internal/platform/harmonyos ./internal/support ./internal/clienthost ./internal/client
HARMONYOS_NODE=$(python3 -c 'import json,pathlib; p=pathlib.Path("apps/harmonyos/toolchains.json"); print(pathlib.Path.home()/".local/opt/harmonyos/command-line-tools"/json.loads(p.read_text())["cli"]["version"]/"tool/node/bin/node")')
"$HARMONYOS_NODE" apps/harmonyos/tests/multidevice.cjs
python3 scripts/sync-version.py --check
python3 scripts/sync-localizations.py --check
python3 scripts/harmonyos/setup_macos.py --check
