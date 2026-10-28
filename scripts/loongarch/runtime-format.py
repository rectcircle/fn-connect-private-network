#!/usr/bin/env python3
"""Unsigned candidate install/startup checks in a clean disposable container.

This does not certify a booted systemd guest, kernel networking or RPM signatures.
"""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys

sys.path.insert(0, '/workspace/scripts')
from linux_support import package_profile


def run(*args):
    subprocess.run(args, check=True, timeout=600)


def main():
    target = os.environ['TARGET']
    profile = package_profile(target, 'loong64')
    out = Path(os.environ.get('DIST_DIR', '/workspace/dist/loongarch-formats')) / target / 'loong64'
    build = json.loads((out / 'build-manifest.json').read_text())
    packages = sorted(out.glob('*.rpm' if profile['format'] == 'rpm' else '*.pkg.tar.zst'))
    assert len(packages) == 2, 'Expected exact Core/UI candidate pair'
    report = {'passed': False, 'target': target, 'commit': build['commit'],
              'scope': 'clean-container installation and UI startup',
              'artifacts': {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in packages},
              'checks': [], 'notValidated': ['booted systemd services', 'kernel/network', 'RPM signature']}
    assert report['artifacts'] == {a['path']: a['sha256'] for a in build['artifacts']}, 'Candidate hashes differ from build manifest'
    def install(items, reinstall=False):
        if profile.get('packageManager') == 'apt-rpm':
            run('apt-get', '-o', 'Acquire::Retries=2', '-o', 'Acquire::http::Timeout=20', 'install', '-y', *(['--reinstall'] if reinstall else []), *map(str, items))
        elif profile['format'] == 'rpm':
            run('dnf', 'reinstall' if reinstall else 'install', '-y', *(['--setopt=localpkg_gpgcheck=True'] if os.environ.get('RELEASE') == '1' else []), *map(str, items))
        else:
            run('pacman', '-U', '--noconfirm', *map(str, items))
    try:
        if profile['format'] == 'rpm' and os.environ.get('RELEASE') == '1':
            from rpm_signing import KEY, verify
            report['rpmSigning'] = verify(packages)
            run('rpmkeys', '--import', str(KEY))
            report['checks'].append('rpm-signature')
        core = next(p for p in packages if p.name.startswith('fncpn-core'))
        install([core])
        # No build dependencies or UI package have been installed in this image.
        query = ['rpm', '-qa'] if profile['format'] == 'rpm' else ['pacman', '-Qq']
        installed = subprocess.check_output(query, text=True)
        (out / 'runtime-core-packages.txt').write_text(installed)
        if any(line.startswith(('libgtk+3', 'libwebkit', 'gtk3', 'webkit2gtk')) for line in installed.splitlines()):
            raise RuntimeError('Core-only environment unexpectedly contains GUI libraries')
        run('/usr/bin/fncpn', 'version')
        report['checks'] += ['core-only-install', 'core-version']
        install(packages)
        report['checks'].append('dependency-resolution')
        linked = subprocess.run(['ldd', '-r', '/usr/bin/fncpn-ui'], text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        (out / 'runtime-ldd.txt').write_text(linked.stdout)
        if linked.returncode or 'not found' in linked.stdout or 'undefined symbol:' in linked.stdout:
            raise RuntimeError('Installed UI cannot resolve its ABI; see runtime-ldd.txt')
        report['checks'].append('ui-dynamic-linking')
        for unit in ('/usr/lib/systemd/user/fncpn-client.service', '/usr/lib/systemd/system/fncpn-client-privileged@.service'):
            assert Path(unit).is_file(), 'Missing service unit: ' + unit
        report['checks'].append('service-unit-files-installed')
        # Tools are added only after product dependency and linking checks.
        if profile.get('packageManager') == 'apt-rpm':
            run('apt-get', 'install', '-y', 'xorg-xvfb', 'xauth', 'dbus', 'fonts-ttf-dejavu', 'shadow-utils', 'util-linux')
        elif profile['format'] == 'rpm':
            run('dnf', 'install', '-y', 'xorg-x11-server-Xvfb', 'xorg-x11-xauth', 'dbus-daemon', 'dejavu-fonts', 'shadow', 'mesa-dri-drivers')
        else:
            run('pacman', '-S', '--noconfirm', '--needed', 'xorg-server-xvfb', 'xorg-xauth', 'dbus', 'ttf-dejavu', 'shadow', 'mesa')
        spec = importlib.util.spec_from_file_location('compatibility', '/workspace/scripts/linux-compatibility.py')
        compatibility = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(compatibility)
        os.environ['FNCPN_UI_SMOKE_TIMEOUT'] = '300'
        report['ui'] = compatibility.smoke(out, build['productVersion'], build['commit'])
        report['checks'].append('ui-frontend-handshake')
        install(packages, reinstall=True)
        report['checks'].append('package-reinstall')
        if profile.get('packageManager') == 'apt-rpm':
            run('apt-get', 'remove', '-y', 'fncpn-ui', 'fncpn-core')
        elif profile['format'] == 'rpm':
            run('dnf', 'remove', '-y', 'fncpn-ui', 'fncpn-core')
        else:
            run('pacman', '-R', '--noconfirm', 'fncpn-ui', 'fncpn-core')
        assert not Path('/usr/bin/fncpn').exists() and not Path('/usr/bin/fncpn-ui').exists()
        report['checks'].append('package-remove')
        report['passed'] = True
    except Exception as error:
        report['error'] = str(error)
        raise
    finally:
        (out / 'runtime-validation.json').write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    main()
