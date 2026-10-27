"""Run the system VPN prototype on an explicitly selected, unlocked test device."""
import argparse
import json
from pathlib import Path
import subprocess
import time
from setup_macos import ROOT


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--device', required=True)
    parser.add_argument('--cycles', type=int, default=2)
    args = parser.parse_args()
    if not 1 <= args.cycles <= 10:
        parser.error('--cycles must be between 1 and 10')
    pin = json.loads((ROOT / 'apps/harmonyos/toolchains.json').read_text())
    hdc = Path.home() / '.local/opt/harmonyos/command-line-tools' / pin['cli']['version'] / 'sdk/default/openharmony/toolchains/hdc'
    output = ROOT / '.dev/harmonyos/vpn-probe'
    output.mkdir(parents=True, exist_ok=True)
    def call(*command):
        return subprocess.check_output([str(hdc), '-t', args.device, *command], text=True, timeout=30)
    bundle = 'cn.rectcircle.fncpn.harmonysmoke'
    report = {'device': args.device, 'cycles': [], 'status': 'failed'}
    try:
        for cycle in range(args.cycles):
            call('shell', 'aa', 'force-stop', bundle)
            response = call('shell', 'aa', 'start', '-a', 'EntryAbility', '-b', bundle, '--ps', 'vpnAction', 'start')
            if 'start ability successfully' not in response:
                report['reason'] = response
                break
            deadline = time.monotonic() + 30
            passed = False
            while time.monotonic() < deadline:
                log = call('shell', 'hilog', '-x')
                pid = call('shell', 'pidof', bundle).strip().split()
                current = '\n'.join(line for line in log.splitlines() if pid and pid[0] in line.split() and 'HarmonyVPN:' in line)
                (output / f'cycle-{cycle+1}.log').write_text(current + '\n')
                if 'request VPN extension' in current and 'bundle not exist -n com.huawei.hmos.vpndialog' in log:
                    report['status'] = 'blocked'
                    report['reason'] = 'System VPN authorization component com.huawei.hmos.vpndialog is missing'
                    evidence = '\n'.join(line for line in log.splitlines() if 'com.huawei.hmos.vpndialog' in line)
                    (output / 'authorization.log').write_text(evidence + '\n')
                    break
                if 'cleanup complete' in current:
                    passed = 'PASS TUN echo and protected socket bypass' in current and 'FAIL ' not in current
                    break
                time.sleep(0.5)
            report['cycles'].append({'cycle': cycle+1, 'passed': passed})
            if not passed:
                break
        if len(report['cycles']) == args.cycles and all(c['passed'] for c in report['cycles']):
            report['status'] = 'passed'
    finally:
        call('shell', 'aa', 'force-stop', bundle)
        (output / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
        print(json.dumps(report, indent=2))
    return 0 if report['status'] == 'passed' else 2 if report['status'] == 'blocked' else 1


if __name__ == '__main__':
    raise SystemExit(main())
