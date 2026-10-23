#!/usr/bin/env python3
"""Portable lifecycle regression tests; no host services or NAS state are changed."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SOURCE = (ROOT / 'packaging/fnos/cmd/main').read_text()
DEFINITIONS = SOURCE.split('\ncase "${1:-}" in\n')[0]


class LifecycleTests(unittest.TestCase):
    def shell(self, body):
        with tempfile.TemporaryDirectory(prefix='fncpn-systemd-') as tmp:
            env = dict(os.environ, TRIM_APPDEST=tmp + '/app', TRIM_PKGVAR=tmp + '/var',
                       TRIM_PKGETC=tmp + '/etc', FIXTURE=tmp)
            result = subprocess.run(['bash', '-c', DEFINITIONS + '\n' + body], env=env,
                                    text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            return result.stdout

    def test_units_and_path_escaping(self):
        out = self.shell(r'''
UNIT_DIR="$FIXTURE/units"
# Do not require the fnOS user on developer machines.
id() { printf '974\n'; }
systemctl() { [ "$*" = daemon-reload ]; }
TRIM_APPDEST="$FIXTURE/percent% dollar\$ quote\" back\\slash"
BINARY="$TRIM_APPDEST/server/fncpn"
install_units
cat "$UNIT_DIR/$PRIVILEGED_UNIT" "$UNIT_DIR/$SERVER_UNIT"
''')
        self.assertIn('User=root\nGroup=fncpn', out)
        self.assertIn('CapabilityBoundingSet=CAP_NET_ADMIN\n', out)
        self.assertIn('User=fncpn\nGroup=fncpn', out)
        self.assertIn('CapabilityBoundingSet=\n', out)
        self.assertIn('percent%% dollar$$ quote\\" back\\\\slash', out)
        self.assertNotIn('[Install]', out)  # fnOS owns startup intent
        self.assertNotIn('PrivateNetwork=true', out)
        self.assertIn('Restart=on-failure', out)

    def test_legacy_upgrade_order_and_idempotent_start(self):
        self.shell(r'''
trace="$FIXTURE/trace"
units_healthy() { [ -f "$FIXTURE/healthy" ]; }
stop_app() { echo stop-old >> "$trace"; }
initialize_app() { echo prepare >> "$trace"; }
install_units() { echo install >> "$trace"; }
systemctl() { echo "$*" >> "$trace"; }
wait_for_health() { return 0; }
su() { return 0; }
# Becomes healthy after the business service starts.
systemctl() {
    echo "$*" >> "$trace"
    if [ "$*" = "start $SERVER_UNIT" ]; then touch "$FIXTURE/healthy"; fi
}
start_app
expected="$(printf 'stop-old\nprepare\ninstall\nstart %s\nstart %s' "$PRIVILEGED_UNIT" "$SERVER_UNIT")"
[ "$(cat "$trace")" = "$expected" ]
start_app
[ "$(cat "$trace")" = "$expected" ]
''')

    def test_stop_supervision_before_legacy_and_network_cleanup(self):
        self.shell(r'''
UNIT_DIR="$FIXTURE/units"
mkdir -p "$UNIT_DIR" "$RUN_DIR" "$APP_RUN_DIR" "$(dirname "$BINARY")"
touch "$UNIT_DIR/$SERVER_UNIT" "$UNIT_DIR/$PRIVILEGED_UNIT" "$APP_SOCKET" "$PRIVILEGED_SOCKET"
trace="$FIXTURE/trace"
export trace
cat > "$BINARY" <<'SH'
#!/bin/bash
printf 'cleanup\n' >> "$trace"
SH
chmod +x "$BINARY"
id() { echo 974; }
systemctl() { echo "$*" >> "$trace"; }
stop_process() { echo "legacy $3" >> "$trace"; }
stop_app
expected="$(printf 'stop %s\nstop %s\nlegacy daemon\nlegacy privileged-daemon\ncleanup' "$SERVER_UNIT" "$PRIVILEGED_UNIT")"
[ "$(cat "$trace")" = "$expected" ]
[ ! -e "$APP_SOCKET" ] && [ ! -e "$PRIVILEGED_SOCKET" ]
''')

    def test_stop_failure_prevents_second_engine(self):
        self.shell(r'''
units_healthy() { return 1; }
stop_app() { return 1; }
initialize_app() { touch "$FIXTURE/unexpected"; }
install_units() { touch "$FIXTURE/unexpected"; }
if start_app; then exit 1; fi
[ ! -e "$FIXTURE/unexpected" ]
''')

    def test_failed_start_stops_services(self):
        self.shell(r'''
units_healthy() { return 1; }
stop_app() { echo stop >> "$FIXTURE/stops"; }
initialize_app() { return 0; }
install_units() { return 0; }
systemctl() { return 1; }
if start_app; then exit 1; fi
[ "$(wc -l < "$FIXTURE/stops" | tr -d ' ')" = 2 ]
''')

    def test_formal_release_legacy_layout(self):
        # Inspect the actual historical implementations, rather than declaring
        # a made-up old layout compatible. Full history is required in CI.
        tags = subprocess.check_output(['git', 'tag', '--list'], cwd=ROOT, text=True).split()
        tags = [t for t in tags if t.startswith('v1.') and '-' not in t and t != 'v1.1.4']
        self.assertIn('v1.0.0', tags)
        self.assertIn('v1.1.3', tags)
        for tag in tags:
            with self.subTest(tag=tag):
                old = subprocess.check_output(['git', 'show', f'{tag}:packaging/fnos/cmd/main'],
                                              cwd=ROOT, text=True)
                for declaration in ['SERVER_PID_FILE="${RUN_DIR}/server.pid"',
                                    'PRIVILEGED_PID_FILE="${RUN_DIR}/privileged.pid"',
                                    'CONTROL_STATE="${TRIM_PKGETC}/control"',
                                    'PRIVILEGED_STATE="${TRIM_PKGETC}/privileged"']:
                    self.assertIn(declaration, old)
                    self.assertIn(declaration, SOURCE)


if __name__ == '__main__':
    unittest.main()
