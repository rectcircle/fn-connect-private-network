#!/bin/bash
# PID 1's one-shot child in an isolated full-system VM. No host networking.
set -euo pipefail
out=/validation/out
mkdir -p "$out"
exec > >(tee "$out/guest.log") 2>&1
finish() {
  code=$?
  trap - EXIT
  journalctl -b --no-pager > "$out/journal.log" || true
  ip -details address > "$out/addresses.log" || true
  ip route show table all > "$out/routes.log" || true
  resolvectl status > "$out/resolved.log" || true
  nft list ruleset > "$out/nftables.log" || true
  printf '%s\n' "$code" > "$out/exit-code"
  # Freeze the log files before archiving, so tar never reads its own output.
  # This channel is a character device connected to a host file, not a NIC.
  if [ -c /dev/virtio-ports/fncpn.results ]; then
    mkdir -p /validation/export
    cp -a "$out/." /validation/export/
    tar -C /validation/export -czf /dev/virtio-ports/fncpn.results . || true
  else
    echo 'Missing virtio results port; host must reject this run.'
  fi
  sync
  systemctl poweroff --no-block
}
trap finish EXIT
test "$(uname -m)" = loongarch64
test "$(cat /proc/1/comm)" = systemd
uname -a
modprobe virtio_console
udevadm settle --timeout=30
test -c /dev/virtio-ports/fncpn.results
modprobe wireguard
modprobe nf_tables
systemctl start systemd-resolved
ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf
/validation/fncpn version
run_test() {
  # Go's test runner otherwise returns success for a misspelled/missing test.
  "$1" -test.list "^$2$" | grep -Fx "$2" || return 1
  "$1" -test.v -test.timeout=180s -test.run "^$2$"
}
failed=0
check_step() {
  local name=$1
  shift
  if "$@"; then
    printf '%s\tpassed\n' "$name" >> "$out/steps.tsv"
  else
    failed=1
    printf '%s\tfailed\n' "$name" >> "$out/steps.tsv"
  fi
}
# qemu-user cannot translate getsockopt(SO_BINDTODEVICE); do not drop coverage.
check_step local-socket run_test /validation/client.test TestLinuxLocalProbeBindsPhysicalInterface
# These tests execute the real project engines and crash-recovery logic.
FNCPN_KERNEL_TESTS=1 check_step kernel-client run_test /validation/linux.test TestKernelClientCapability
FNCPN_KERNEL_TESTS=1 check_step dns-bridge run_test /validation/privileged.test TestKernelDNSBridgeCapability
FNCPN_NETFILTER_TEST=1 check_step netfilter run_test /validation/linux.test TestDockerForwardingIntegration
# Separate test exercises the real resolved service (the existing bridge test
# deliberately mocks its D-Bus calls), plus WireGuard traffic and reconnect.
check_step network-and-resolved /validation/probe -test.v -test.timeout=180s
if [ -x /validation/fncpn-ui ]; then
  check_step ui python3 /validation/scripts/linux-ui-smoke.py /validation/fncpn-ui "$out" "$(cat /validation/version)" "$(cat /validation/commit)"
else
  echo 'UI binary missing: the experiment cannot pass.'
  failed=1
  printf 'ui\tfailed\n' >> "$out/steps.tsv"
fi
exit "$failed"
