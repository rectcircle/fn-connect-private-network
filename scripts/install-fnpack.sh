#!/usr/bin/env bash
set -euo pipefail
# Official fnpack 1.2.3, verified against the existing validated local tool.
output="${1:?Usage: install-fnpack.sh OUTPUT}"
curl --fail --location --retry 3 https://static2.fnnas.com/fnpack/fnpack-1.2.3-darwin-arm64 -o "$output"
printf 'd40cb00896cb2a5d211357d255750ed0cbe7f2d141df671c2b717afb4e74bf77  %s\n' "$output" | shasum -a 256 -c -
chmod 0755 "$output"
