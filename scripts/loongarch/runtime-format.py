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
    mode = sys.argv[1] if len(sys.argv) > 1 else 'all'
    target = os.environ['TARGET']
    profile = package_profile(target, 'loong64')
    out = Path(os.environ.get('DIST_DIR', '/workspace/dist/loongarch-formats')) / target / 'loong64'
    if os.environ.get('FNCPN_RELEASE_INVENTORY'):
        inventory = json.loads(Path(os.environ['FNCPN_RELEASE_INVENTORY']).read_text())
        build = {'commit': inventory['commit'], 'productVersion': inventory['version'],
                 'artifacts': [{'path': a['file'], 'sha256': a['sha256']} for a in inventory['artifacts']
                               if a['file'].endswith('-linux-loong64.' + ('deb' if profile['format'] == 'deb' else 'rpm'))]}
    else:
        build = json.loads((out / 'build-manifest.json').read_text())
    packages = sorted(out.glob({'rpm': '*.rpm', 'deb': '*.deb', 'archlinux': '*.pkg.tar.zst'}[profile['format']]))
    assert len(packages) == 2, 'Expected exact Core/UI candidate pair'
    report = {'passed': False, 'target': target, 'commit': build['commit'],
              'validationCommit': os.environ.get('FNCPN_VALIDATION_COMMIT', build['commit']),
              'scope': 'clean-container installation and UI startup',
              'artifacts': {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in packages},
              'checks': [], 'notValidated': ['booted systemd services', 'kernel/network'] + (['RPM signature'] if profile['format'] == 'rpm' and os.environ.get('RELEASE') != '1' else [])}
    assert report['artifacts'] == {a['path']: a['sha256'] for a in build['artifacts']}, 'Candidate hashes differ from build manifest'
    if mode == 'finish':
        report = json.loads((out / 'runtime-validation.json').read_text())
    def install(items, reinstall=False):
        if profile['format'] == 'deb':
            run('apt-get', 'install', '-y', '--no-install-recommends', *(['--reinstall'] if reinstall else []), *map(str, items))
        elif profile.get('packageManager') == 'apt-rpm':
            run('apt-get', '-o', 'Acquire::Retries=2', '-o', 'Acquire::http::Timeout=20', 'install', '-y', *(['--reinstall'] if reinstall else []), *map(str, items))
        elif profile['format'] == 'rpm':
            run('dnf', *(['--cacheonly'] if mode == 'finish' else []), 'reinstall' if reinstall else 'install', '-y', *(['--setopt=localpkg_gpgcheck=True'] if os.environ.get('RELEASE') == '1' else []), *map(str, items))
        else:
            run('pacman', '-U', '--noconfirm', *map(str, items))
    try:
        if mode != 'finish':
            run(sys.executable, '/workspace/scripts/linux-build-environment.py', target, 'loong64')
            if profile['format'] == 'rpm' and os.environ.get('RELEASE') == '1':
                from rpm_signing import KEY, verify
                report['rpmSigning'] = verify(packages)
                run('rpmkeys', '--import', str(KEY))
                report['checks'].append('rpm-signature')
            core = next(p for p in packages if p.name.startswith('fncpn-core'))
            install([core])
            # No build dependencies or UI package have been installed in this image.
            query = {'rpm': ['rpm', '-qa'], 'deb': ['dpkg-query', '-W', '-f=${Package}\n'], 'archlinux': ['pacman', '-Qq']}[profile['format']]
            installed = subprocess.check_output(query, text=True)
            (out / 'runtime-core-packages.txt').write_text(installed)
            if any(line.startswith(('libgtk', 'libwebkit', 'gtk3', 'webkit2gtk')) for line in installed.splitlines()):
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
            if profile['format'] == 'deb':
                run('apt-get', 'install', '-y', '--no-install-recommends', 'xvfb', 'xauth', 'dbus', 'fonts-dejavu-core', 'passwd', 'libgl1-mesa-dri', 'libpam-systemd', 'udev', 'kmod', 'binutils')
            elif profile.get('packageManager') == 'apt-rpm':
                run('apt-get', 'install', '-y', 'xorg-xvfb', 'xauth', 'dbus', 'fonts-ttf-dejavu', 'shadow-utils', 'util-linux')
            elif profile['format'] == 'rpm':
                run('dnf', 'install', '-y', 'xorg-x11-server-Xvfb', 'xorg-x11-xauth', 'dbus-daemon', profile.get('testFontPackage', 'dejavu-fonts'), 'shadow', 'mesa-dri-drivers', 'systemd-pam', 'systemd-udev', 'kmod')
            else:
                run('pacman', '-S', '--noconfirm', '--needed', 'xorg-server-xvfb', 'xorg-xauth', 'dbus', 'ttf-dejavu', 'shadow', 'mesa', 'kmod')
        if mode == 'prepare':
            if profile['osID'] == 'anolis':
                # Anaconda normally enables the selected D-Bus provider. Our
                # signed-RPM installroot starts with all services preset off.
                # Establish that OS boot baseline without replacing any unit.
                run('systemctl', 'enable', 'dbus-broker.service')
                if Path('/usr/lib/systemd/user/dbus-broker.service').exists():
                    run('systemctl', '--global', 'enable', 'dbus-broker.service')
            return
        spec = importlib.util.spec_from_file_location('compatibility', '/workspace/scripts/linux-compatibility.py')
        compatibility = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(compatibility)
        os.environ['FNCPN_UI_SMOKE_TIMEOUT'] = '300'
        report['ui'] = compatibility.smoke(out, build['productVersion'], build['commit'])
        report['checks'].append('ui-frontend-handshake')
        if mode == 'finish':
            assert Path('/proc/1/comm').read_text().strip() == 'systemd'
            run('loginctl', 'enable-linger', 'fncpn-smoke')
            run('systemctl', 'start', 'user@12345.service')
            run('/usr/libexec/fncpn/enable-client', '12345')
            user = ['runuser', '-u', 'fncpn-smoke', '--', 'env', 'XDG_RUNTIME_DIR=/run/user/12345', 'DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/12345/bus']
            run(*user, 'systemctl', '--user', 'enable', '--now', 'fncpn-client.service')
            run('systemctl', 'is-active', '--quiet', 'fncpn-client-privileged@12345.service')
            run(*user, 'systemctl', '--user', 'is-active', '--quiet', 'fncpn-client.service')
            run('/usr/bin/fncpn', 'internal-healthcheck', '--role', 'client-privileged', '--socket', '/run/fncpn/12345/privileged.sock')
            run(*user, '/usr/bin/fncpn', 'internal-healthcheck', '--role', 'client', '--socket', '/run/user/12345/fncpn/client.sock')
            report['checks'].append('systemd-services')
            report['scope'] = 'clean installation, full-system UI and service startup, reinstall and removal'
            report['notValidated'] = ['kernel/network regression', 'historical package upgrade']
            if profile['format'] == 'rpm' and os.environ.get('RELEASE') != '1':
                report['notValidated'].append('RPM signature')
        install(packages, reinstall=True)
        report['checks'].append('package-reinstall')
        if profile['format'] == 'deb':
            run('apt-get', 'remove', '-y', 'fncpn-ui', 'fncpn-core')
        elif profile.get('packageManager') == 'apt-rpm':
            run('apt-get', 'remove', '-y', 'fncpn-ui', 'fncpn-core')
        elif profile['format'] == 'rpm':
            run('dnf', *(['--cacheonly'] if mode == 'finish' else []), 'remove', '-y', 'fncpn-ui', 'fncpn-core')
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
