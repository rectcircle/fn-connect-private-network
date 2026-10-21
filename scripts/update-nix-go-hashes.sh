#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TEMP_DIR"' EXIT
mkdir -p "$TEMP_DIR/source/ui"
cp "$ROOT_DIR/go.mod" "$ROOT_DIR/go.sum" "$TEMP_DIR/source/"
cp -R "$ROOT_DIR/cmd" "$ROOT_DIR/internal" "$TEMP_DIR/source/"
# Copy only source; caches and generated binaries cannot affect vendoring.
tar -C "$ROOT_DIR/ui" --exclude=node_modules --exclude=vendor --exclude=build --exclude=dist -cf - . | tar -C "$TEMP_DIR/source/ui" -xf -
(cd "$TEMP_DIR/source" && GOOS=linux GOARCH=amd64 go mod vendor)
(cd "$TEMP_DIR/source/ui" && GOOS=linux GOARCH=amd64 go mod vendor)
CORE_HASH="$(nix --extra-experimental-features nix-command hash path "$TEMP_DIR/source/vendor")"
UI_HASH="$(nix --extra-experimental-features nix-command hash path "$TEMP_DIR/source/ui/vendor")"
python3 - "$ROOT_DIR/nix/go-hashes.json" "$CORE_HASH" "$UI_HASH" <<'PY'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps(dict(core=sys.argv[2],ui=sys.argv[3]),indent=2)+'\n')
PY
