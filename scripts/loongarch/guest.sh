#!/bin/bash
# PID 1's one-shot child in an isolated full-system VM. No host networking.
set -euo pipefail
out=/validation/out
mkdir -p "$out"
exec > >(tee "$out/guest.log" /dev/ttyS0) 2>&1
finish() {
  code=$?
  trap - EXIT
  journalctl -b --no-pager > "$out/journal.log" || true
  ip -details address > "$out/addresses.log" || true
  ip route show table all > "$out/routes.log" || true
  resolvectl status > "$out/resolved.log" || true
  nft list ruleset > "$out/nftables.log" || true
  printf '%s\n' "$code" > "$out/exit-code"
  sync
  systemctl poweroff --no-block
}
trap finish EXIT
test "$(uname -m)" = loongarch64
test "$(cat /proc/1/comm)" = systemd
uname -a
modprobe wireguard
modprobe nf_tables
systemctl start systemd-resolved
ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf
/validation/fncpn version
# These tests execute the real project engines and crash-recovery logic.
FNCPN_KERNEL_TESTS=1 /validation/linux.test -test.v -test.timeout=180s -test.run '^TestKernelClientCapability$'
FNCPN_KERNEL_TESTS=1 /validation/privileged.test -test.v -test.timeout=180s -test.run '^TestKernelDNSBridgeCapability$'
FNCPN_NETFILTER_TEST=1 /validation/linux.test -test.v -test.timeout=180s -test.run '^TestDockerForwardingIntegration$'
# Separate test exercises the real resolved service (the existing bridge test
# deliberately mocks its D-Bus calls), plus WireGuard traffic and reconnect.
/validation/probe -test.v -test.timeout=180s
if [ -x /validation/fncpn-ui ]; then
  python3 /validation/scripts/linux-ui-smoke.py /validation/fncpn-ui "$out" "$(cat /validation/version)" "$(cat /validation/commit)"
else
  echo 'UI binary missing: the experiment cannot pass.'
  exit 1
fi
