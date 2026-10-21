#!/usr/bin/env bash
# Cross-compile on the development host; run only test binaries on the VM.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST="${1:-parallels-debian13}"
MACHINE="$(ssh -o BatchMode=yes "$HOST" 'uname -m')"
case "$MACHINE" in aarch64) ARCH=arm64 ;; x86_64) ARCH=amd64 ;; *) echo "Unsupported VM architecture: $MACHINE" >&2; exit 1 ;; esac
ssh -o BatchMode=yes "$HOST" '. /etc/os-release; test "$ID" = debian; test "$VERSION_ID" = 13; sudo -n true'
REMOTE="$(ssh -o BatchMode=yes "$HOST" 'mktemp -d /tmp/fncpn-unit.XXXXXX')"
[[ "$REMOTE" =~ ^/tmp/fncpn-unit\.[a-zA-Z0-9]+$ ]] || exit 1
OUT="$ROOT_DIR/.dev/debian13-tests/$ARCH"
mkdir -p "$OUT"
trap 'ssh -o BatchMode=yes "$HOST" "sudo -n rm -rf -- $REMOTE" >/dev/null 2>&1 || true' EXIT
for PACKAGE in internal/platform/linux internal/privileged internal/command internal/client internal/dns internal/wireguard internal/version internal/model; do
 NAME="${PACKAGE//\//-}"
 (cd "$ROOT_DIR" && GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go test -c -o "$OUT/$NAME.test" "./$PACKAGE")
 scp -q "$OUT/$NAME.test" "$HOST:$REMOTE/"
 ssh -o BatchMode=yes "$HOST" "$REMOTE/$NAME.test -test.timeout=3m"
done
# An isolated network namespace cannot alter the VM's SSH, routes or resolver.
ssh -o BatchMode=yes "$HOST" "sudo -n unshare --net env FNCPN_KERNEL_TESTS=1 $REMOTE/internal-platform-linux.test -test.v -test.run TestKernelClientCapability"
ssh -o BatchMode=yes "$HOST" "sudo -n unshare --net env FNCPN_KERNEL_TESTS=1 $REMOTE/internal-privileged.test -test.v -test.run TestKernelDNSBridgeCapability"
