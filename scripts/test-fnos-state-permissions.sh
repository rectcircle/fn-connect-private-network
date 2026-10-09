#!/usr/bin/env bash
# Run as root on an isolated CI runner; never target a live NAS installation.
set -euo pipefail
cd "$(dirname "$0")/.."
[[  "$(id -u)" = 0 ]] || { echo 'This fixture requires root' >&2; exit 1; }
fixture="$(mktemp -d /tmp/fncpn-state-permissions.XXXXXX)"
trap 'rm -rf "$fixture"' EXIT
chmod 0755 "$fixture"
export TRIM_PKGETC="$fixture/etc" TRIM_PKGVAR="$fixture/var" TRIM_APPDEST="$fixture/app"
export TEST_GROUP="$(id -gn nobody)" TEST_ROOT_GROUP="$(id -gn root)"
mkdir -p "$TRIM_PKGETC/control" "$TRIM_PKGETC/privileged"
printf 'retained state\n' > "$TRIM_PKGETC/control/state.json"
printf 'retained probe key\n' > "$TRIM_PKGETC/control/probe.key"
printf 'retained server key\n' > "$TRIM_PKGETC/privileged/server.key"
printf 'retained recovery journal\n' > "$TRIM_PKGETC/privileged/network-state.json"
chmod 0700 "$TRIM_PKGETC/control/"* "$TRIM_PKGETC/privileged/"*
chown "nobody:$TEST_GROUP" "$TRIM_PKGETC/control"
chmod 0755 "$TRIM_PKGETC"
chmod 0700 "$TRIM_PKGETC/control"
if sudo -u nobody test -r "$TRIM_PKGETC/control/state.json"; then
    echo 'Root-owned 0700 fixture unexpectedly readable' >&2
    exit 1
fi
python3 - "$fixture/functions.sh" <<'PY'
import pathlib, sys
source = pathlib.Path('packaging/fnos/cmd/main').read_text()
definitions, separator, _ = source.partition('\ncase "${1:-}" in\n')
assert separator
pathlib.Path(sys.argv[1]).write_text(definitions)
PY
bash -c '
    source "$1/functions.sh"
    APP_USER=nobody
    # nobody has a different group name on Linux; preserve real chown behavior.
    chown() {
        local owner="$1"
        shift
        case "$owner" in
            nobody:nobody) owner="nobody:$TEST_GROUP" ;;
            root:nobody) owner="root:$TEST_GROUP" ;;
            root:root) owner="root:$TEST_ROOT_GROUP" ;;
        esac
        command chown "$owner" "$@"
    }
    prepare_state
' fixture "$fixture"
sudo -u nobody env TRIM_PKGETC="$TRIM_PKGETC" sh -ec '
    test -r "$TRIM_PKGETC/control/state.json"
    test -r "$TRIM_PKGETC/control/probe.key"
    test -w "$TRIM_PKGETC/control"
    test "$(cat "$TRIM_PKGETC/control/state.json")" = "retained state"
    test "$(cat "$TRIM_PKGETC/control/probe.key")" = "retained probe key"
    test ! -r "$TRIM_PKGETC/privileged/server.key"
    test ! -x "$TRIM_PKGETC/privileged"
'
test "$(cat "$TRIM_PKGETC/privileged/server.key")" = 'retained server key'
test "$(cat "$TRIM_PKGETC/privileged/network-state.json")" = 'retained recovery journal'
echo 'Real root-owned state permission recovery passed'
