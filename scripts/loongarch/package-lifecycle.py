#!/usr/bin/env python3
"""Install the exact LoongArch archives in the clean, offline systemd guest."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
from guest_commands import run_command

BASE = Path('/validation')
OUT = BASE / 'out'
sys.path.insert(0, str(BASE / 'scripts'))
from linux_abi import inspect as inspect_abi

spec = importlib.util.spec_from_file_location('packages', BASE / 'scripts/check-linux-packages.py')
packages = importlib.util.module_from_spec(spec)
spec.loader.exec_module(packages)


def run(*args, timeout=180):
    return run_command(args, timeout=timeout, log=OUT / 'package-commands.log')


def user(*args):
    return run('runuser', '-u', 'fncpn-validation', '--', 'env', 'XDG_RUNTIME_DIR=/run/user/1000',
               'DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus', *args)


def install(paths, reinstall=False):
    # APT's --no-download path rejects local DEBs before unpacking them.
    # The guest has no NIC: cached dependencies remain mandatory, while APT
    # must be allowed to acquire the explicitly supplied local archives.
    run('apt-get', 'install', '-y', '--no-install-recommends', '-o', 'Acquire::Retries=0',
        *(['--reinstall'] if reinstall else []), *map(str, paths), timeout=600)


def state_hashes():
    files = [Path('/home/fncpn-validation/.config/fncpn/config.json')]
    files += sorted(Path('/var/lib/fncpn/client/1000/credentials').rglob('*'))
    return {str(path): hashlib.sha256(path.read_bytes()).hexdigest() for path in files if path.is_file()}


def services():
    run('systemctl', 'is-active', '--quiet', 'fncpn-client-privileged@1000.service')
    user('systemctl', '--user', 'is-active', '--quiet', 'fncpn-client.service')
    run('/usr/bin/fncpn', 'internal-healthcheck', '--role', 'client-privileged', '--socket', '/run/fncpn/1000/privileged.sock')
    user('/usr/bin/fncpn', 'internal-healthcheck', '--role', 'client', '--socket', '/run/user/1000/fncpn/client.sock')


def main(mode):
    version = (BASE / 'version').read_text().strip()
    commit = (BASE / 'commit').read_text().strip()
    archives = sorted((BASE / 'packages').glob('*.deb'))
    evidence_file = OUT / 'package-evidence.json'
    evidence = json.loads(evidence_file.read_text()) if evidence_file.exists() else {
        'target': 'debian-forky', 'architecture': 'loong64', 'bundle': 'deb',
        'productVersion': version, 'commit': commit, 'passed': False, 'checks': [],
        'artifacts': {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in archives},
        'upgradeFixture': 'same candidate code at a lower DEB version; not historical release compatibility'}
    try:
        if mode == 'prepare':
            if len(archives) != 2:
                raise ValueError('Expected exactly matching Core/UI DEBs')
            for path in archives:
                fields = packages.inspect(path)
                packages.validate_target(fields, path, 'deb-loong64', 'loong64', version)
            evidence['checks'].append('package-metadata')
            core = next(p for p in archives if p.name.startswith('fncpn-core-'))
            install([core])
            installed = run('dpkg-query', '-W', '-f=${Package}\n').splitlines()
            if any(name.startswith(('libgtk', 'libwebkit', 'libjavascriptcoregtk')) for name in installed):
                raise ValueError('Core-only clean guest contains GUI libraries')
            evidence['checks'].append('core-only-install')
            if run('/usr/bin/fncpn', 'version') != version:
                raise ValueError('Installed Core version differs')
            evidence['checks'].append('core-version')
            install(archives)
            evidence['checks'].append('dependency-resolution')
            for binary in ('fncpn', 'fncpn-ui'):
                if Path('/usr/bin', binary).read_bytes() != (BASE / binary).read_bytes():
                    raise ValueError('Installed binary differs from build artifact')
            evidence['abi'] = inspect_abi('/usr/bin/fncpn', '/usr/bin/fncpn-ui')
            baseline = json.loads((BASE / 'packages/build-manifest.json').read_text())
            if baseline['commit'] != commit or baseline['abi']['requiredVersions'] != evidence['abi']['requiredVersions']:
                raise ValueError('Installed ABI does not match same-commit build manifest')
            evidence['checks'] += ['ui-dynamic-linking', 'elf-abi']
            # Tools cannot hide missing runtime dependencies in the checks above.
            install(['xvfb', 'xauth', 'fonts-dejavu-core', 'libgl1-mesa-dri'])
            run('apt-get', 'remove', '-y', 'fncpn-ui', 'fncpn-core')
            install(sorted((BASE / 'packages/upgrade-fixture').glob('*.deb')))
            run('useradd', '--create-home', '--uid', '1000', 'fncpn-validation')
            run('loginctl', 'enable-linger', 'fncpn-validation')
            run('systemctl', 'start', 'user@1000.service')
            run('/usr/libexec/fncpn/enable-client', '1000')
            user('env', 'FNCPN_PACKAGE_STATE=seed', '/validation/probe', '-test.run=^TestPackageState$', '-test.v')
            user('systemctl', '--user', 'enable', '--now', 'fncpn-client.service')
            services()
            before = state_hashes()
            if len(before) < 2:
                raise ValueError('Real config and root credential fixture was not created')
            install(archives)
            services()
            if run('dpkg-query', '-W', '-f=${Version}', 'fncpn-core') != version:
                raise ValueError('Package upgrade did not install candidate')
            evidence['checks'] += ['systemd-services', 'package-upgrade']
            if state_hashes() != before:
                raise ValueError('Package upgrade changed identity/configuration bytes')
            user('env', 'FNCPN_PACKAGE_STATE=check', '/validation/probe', '-test.run=^TestPackageState$', '-test.v')
            evidence['stateHashes'] = before
            evidence['checks'].append('upgrade-state-preserved')
            install(archives, reinstall=True)
            services()
            if state_hashes() != before:
                raise ValueError('Same-version reinstall changed saved state')
            evidence['checks'].append('package-reinstall')
        elif mode == 'finish':
            steps = dict(line.split('\t') for line in (OUT / 'steps.tsv').read_text().splitlines())
            if steps.get('ui') == 'passed':
                evidence['checks'].append('ui-frontend-handshake')
            if all(steps.get(name) == 'passed' for name in ('local-socket', 'kernel-client', 'netfilter', 'network-and-resolved')):
                evidence['checks'] += ['kernel-network', 'real-resolved']
            run('apt-get', 'remove', '-y', 'fncpn-ui', 'fncpn-core')
            if any(Path('/usr/bin', name).exists() for name in ('fncpn', 'fncpn-ui')):
                raise ValueError('Uninstall left installed executables')
            if subprocess.run(['systemctl', 'is-active', '--quiet', 'fncpn-client-privileged@1000.service']).returncode == 0:
                raise ValueError('Uninstall left privileged service running')
            if subprocess.run(['pgrep', '-x', 'fncpn']).returncode == 0:
                raise ValueError('Uninstall left client processes running')
            if state_hashes() != evidence.get('stateHashes'):
                raise ValueError('Uninstall changed retained user data')
            evidence['checks'].append('package-remove')
            required = {'package-metadata','dependency-resolution','core-version','ui-dynamic-linking','core-only-install',
                        'elf-abi','ui-frontend-handshake','package-reinstall','package-remove','systemd-services',
                        'package-upgrade','upgrade-state-preserved','kernel-network','real-resolved'}
            evidence['passed'] = set(evidence['checks']) == required and all(value == 'passed' for value in steps.values())
            if not evidence['passed']:
                raise ValueError('Incomplete package or full-system checks')
        else:
            raise ValueError('Expected prepare or finish')
    except Exception as error:
        evidence['passed'] = False
        evidence['error'] = str(error)
        raise
    finally:
        evidence_file.write_text(json.dumps(evidence, indent=2) + '\n')


if __name__ == '__main__':
    main(sys.argv[1])
