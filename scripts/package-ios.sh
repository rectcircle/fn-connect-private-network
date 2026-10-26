#!/bin/bash
set -Eeuo pipefail
cd "$(dirname "$0")/.."
python3 scripts/ios/package.py "$@"
