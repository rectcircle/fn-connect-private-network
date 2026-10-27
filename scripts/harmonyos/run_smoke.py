"""Install the smoke HAP on an explicitly selected device and verify runtime results."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import time
from setup_macos import ROOT

BUNDLE = 'cn.rectcircle.fncpn.harmonysmoke'


def parse_results(log, pid):
    results = []
    for line in log.splitlines():
        if pid not in line.split() or 'HarmonySmoke:' not in line:
            continue
        payload = line.split('HarmonySmoke:', 1)[1].strip()
        try:
            results.append(json.loads(payload))
        except json.JSONDecodeError:
            pass
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--device', required=True, help='Explicit hdc target; no automatic physical-device selection')
    parser.add_argument('--https', help='HTTPS test URL; omitted means no HTTP test')
    parser.add_argument('--wss', help='WSS test URL; omitted means no WebSocket test')
    args = parser.parse_args()
    if args.https and not args.https.startswith('https://'):
        parser.error('--https requires HTTPS')
    if args.wss and not args.wss.startswith('wss://'):
        parser.error('--wss requires WSS')
    pin = json.loads((ROOT / 'apps/harmonyos/toolchains.json').read_text())
    hdc = Path.home() / '.local/opt/harmonyos/command-line-tools' / pin['cli']['version'] / 'sdk/default/openharmony/toolchains/hdc'
    hap = ROOT / '.dev/harmonyos/hap-smoke/entry/build/default/outputs/default/entry-default-unsigned.hap'
    output = ROOT / '.dev/harmonyos/runtime'
    output.mkdir(parents=True, exist_ok=True)

    def call(*command):
        return subprocess.check_output([str(hdc), '-t', args.device, *command], text=True, timeout=30)

    install = call('install', '-r', str(hap))
    if 'install bundle successfully' not in install:
        raise RuntimeError('HAP installation failed: ' + install)
    report = {'hapSha256': hashlib.sha256(hap.read_bytes()).hexdigest(),
              'goCommit': pin['goPort']['installCommit'], 'device': args.device, 'results': {}}
    for name, url in [('runtime', ''), ('https', args.https), ('wss', args.wss)]:
        if url is None:
            continue
        call('shell', 'aa', 'force-stop', BUNDLE)
        command = ['shell', 'aa', 'start', '-a', 'EntryAbility', '-b', BUNDLE]
        if url:
            command.extend(['--ps', 'probeUrl', url])
        start = call(*command)
        if 'start ability successfully' not in start:
            raise RuntimeError('Ability start failed: ' + start)
        deadline = time.monotonic() + 25
        result = None
        while time.monotonic() < deadline:
            pid = call('shell', 'pidof', BUNDLE).strip().split()
            if pid:
                log = call('shell', 'hilog', '-x', '-T', 'HarmonySmoke')
                (output / (name + '.log')).write_text(log)
                results = parse_results(log, pid[0])
                if results:
                    result = results[-1]
                    break
            time.sleep(0.5)
        passed = bool(result and result.get('smoke') == 42 and result.get('workers') == 4 and result.get('version') == pin['goPort']['version'] and 'error' not in result)
        if name == 'https':
            passed = passed and result.get('httpStatus') == 200
        if name == 'wss':
            passed = passed and result.get('websocket') == 'connected'
        report['results'][name] = {'passed': passed, 'value': result}
        print(name + ': ' + ('passed' if passed else 'failed'), flush=True)
    report['passed'] = all(item['passed'] for item in report['results'].values())
    (output / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
