#!/usr/bin/env bash
# Install a pinned official Node binary in a disposable CI workspace.
set -euo pipefail
NODE_VERSION=22.14.0
case "$(uname -m)" in x86_64) NODE_ARCH=x64 ;; aarch64) NODE_ARCH=arm64 ;; *) exit 1 ;; esac
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
ARCHIVE="node-v$NODE_VERSION-linux-$NODE_ARCH.tar.gz"
BASE="https://nodejs.org/dist/v$NODE_VERSION"
curl --fail --location --retry 3 "$BASE/$ARCHIVE" -o "$STAGE/$ARCHIVE"
curl --fail --location --retry 3 "$BASE/SHASUMS256.txt" -o "$STAGE/SHASUMS256.txt"
(cd "$STAGE" && awk -v name="$ARCHIVE" '$2 == name {print; found=1} END {if (!found) exit 1}' SHASUMS256.txt | sha256sum --check --strict)
NODE_DIRECTORY="$RUNNER_TEMP/fncpn-node-$NODE_VERSION-$NODE_ARCH"
mkdir -p "$NODE_DIRECTORY"
tar -xzf "$STAGE/$ARCHIVE" --strip-components=1 -C "$NODE_DIRECTORY"
echo "$NODE_DIRECTORY/bin" >> "$GITHUB_PATH"
