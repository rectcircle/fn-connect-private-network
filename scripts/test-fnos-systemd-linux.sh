#!/usr/bin/env bash
# Real systemd + kernel WireGuard lifecycle test, only on a disposable CI VM.
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "${GITHUB_ACTIONS:-}" = true && "$(id -u)" = 0 && "$(uname -s)" = Linux ]] || {
    echo 'Run only as root on a disposable GitHub Actions Linux VM' >&2; exit 1;
}
for unit in fncpn-server.service fncpn-server-privileged.service; do
    [[ "$(systemctl show "$unit" -p LoadState --value)" = not-found ]] || {
        echo "Refusing to replace existing $unit" >&2; exit 1;
    }
done
fixture="$(mktemp -d /opt/fncpn-systemd.XXXXXX)"
namespace="fncpn-ci-$$"
app_user="fncpn-ci-$$"
cleanup() {
    systemctl stop fncpn-server.service fncpn-server-privileged.service || true
    ip netns pids "$namespace" 2>/dev/null | xargs -r kill -TERM || true
    rm -f /run/systemd/system/fncpn-server{,-privileged}.service
    rm -rf /run/systemd/system/fncpn-server{,-privileged}.service.d
    systemctl daemon-reload
    ip netns del "$namespace" || true
    userdel "$app_user" || true
    rm -rf "$fixture"
}
trap cleanup EXIT
chmod 0755 "$fixture"
useradd --system --no-create-home --user-group "$app_user"
ip netns add "$namespace"
ip -n "$namespace" link set lo up
export TRIM_APPDEST="$fixture/app" TRIM_PKGETC="$fixture/etc" TRIM_PKGVAR="$fixture/var"
mkdir -p "$TRIM_APPDEST/server" "$fixture/old"
go build -o "$fixture/new-fncpn" ./cmd/fncpn
# Execute the genuine old launcher AND old binary, not a synthetic old daemon.
git archive v1.1.3 | tar -x -C "$fixture/old"
(cd "$fixture/old" && go build -o "$TRIM_APPDEST/server/fncpn" ./cmd/fncpn)
sed "s/APP_USER=\"fncpn\"/APP_USER=\"$app_user\"/" \
    "$fixture/old/packaging/fnos/cmd/main" > "$fixture/legacy-main"
chmod +x "$fixture/legacy-main"
ip netns exec "$namespace" "$fixture/legacy-main" initialize
ip netns exec "$namespace" "$fixture/legacy-main" start
# Seed a real registered peer through the historical HTTP implementation.
curl --fail --silent --show-error --unix-socket "$TRIM_APPDEST/run/app.sock" \
    -H 'X-Trim-Userid: ci-user' -H 'Content-Type: application/json' \
    -d '{"name":"retained-ci-device","publicKey":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="}' \
    http://localhost/api/v1/devices > "$fixture/device.json"
# Record actual old installation identity/configuration bytes before replacing it.
(cd "$TRIM_PKGETC" && sha256sum control/state.json control/probe.key privileged/server.key) > "$fixture/before.sha256"
# Atomic replacement also tests detection of the old executable's deleted inode.
cp "$fixture/new-fncpn" "$TRIM_APPDEST/server/fncpn.new"
mv "$TRIM_APPDEST/server/fncpn.new" "$TRIM_APPDEST/server/fncpn"
for unit in fncpn-server.service fncpn-server-privileged.service; do
    mkdir -p "/run/systemd/system/$unit.d"
    printf '[Service]\nNetworkNamespacePath=/run/netns/%s\n' "$namespace" \
        > "/run/systemd/system/$unit.d/namespace.conf"
done
python3 - "$fixture/functions.sh" "$app_user" <<'PY'
from pathlib import Path
import sys
source = Path('packaging/fnos/cmd/main').read_text().split('\ncase "${1:-}" in\n')[0]
source = source.replace('APP_USER="fncpn"', 'APP_USER="' + sys.argv[2] + '"')
source = source.replace('UNIT_DIR="/etc/systemd/system"', 'UNIT_DIR="/run/systemd/system"')
Path(sys.argv[1]).write_text(source)
PY
export fixture
run_fixture() { ip netns exec "$namespace" bash -ec 'source "$fixture/functions.sh"; '"$1"; }
run_fixture start_app
(cd "$TRIM_PKGETC" && sha256sum -c "$fixture/before.sha256")
privileged_pid="$(systemctl show fncpn-server-privileged.service -p MainPID --value)"
server_pid="$(systemctl show fncpn-server.service -p MainPID --value)"
[[ "$(awk '/^Uid:/{print $3}' "/proc/$privileged_pid/status")" = 0 ]]
[[ "$(awk '/^CapEff:/{print $2}' "/proc/$privileged_pid/status")" = 0000000000001000 ]]
[[ "$(awk '/^Uid:/{print $3}' "/proc/$server_pid/status")" = "$(id -u "$app_user")" ]]
[[ "$(awk '/^CapEff:/{print $2}' "/proc/$server_pid/status")" = 0000000000000000 ]]
[[ "$(stat -c %a "$TRIM_PKGVAR/run/server-privileged.sock")" = 660 ]]
[[ "$(stat -c %G "$TRIM_PKGVAR/run/server-privileged.sock")" = "$app_user" ]]
runuser -u "$app_user" -- test ! -r "$TRIM_PKGETC/privileged/server.key"
runuser -u "$app_user" -- test ! -w "$TRIM_APPDEST/server/fncpn"
run_fixture start_app
[[ "$(systemctl show fncpn-server.service -p MainPID --value)" = "$server_pid" ]]
kill -KILL "$privileged_pid"
# Allow systemd recovery and the business daemon's 5-second network refresh.
for attempt in $(seq 1 40); do
    new_pid="$(systemctl show fncpn-server-privileged.service -p MainPID --value)"
    if [[ "$new_pid" != 0 && "$new_pid" != "$privileged_pid" ]] &&
        run_fixture units_healthy && ip -n "$namespace" link show fncpn0 >/dev/null 2>&1; then
        break
    fi
    sleep 1
done
[[ "$new_pid" != 0 && "$new_pid" != "$privileged_pid" ]]
run_fixture units_healthy
ip -n "$namespace" link show fncpn0
(cd "$TRIM_PKGETC" && sha256sum -c "$fixture/before.sha256")
run_fixture stop_app
! systemctl is-active --quiet fncpn-server.service
! systemctl is-active --quiet fncpn-server-privileged.service
! ip -n "$namespace" link show fncpn0 >/dev/null 2>&1
run_fixture stop_app
# fnOS remains the sole owner of startup intent.
[[ ! -e /etc/systemd/system/multi-user.target.wants/fncpn-server.service ]]
echo 'Historical upgrade, real capabilities, crash recovery and stop passed'
