#!/usr/bin/env python3
"""Install exact archives in a clean userspace, then accept them in a real VM."""
import hashlib
import importlib.util
import json
import os
import shlex
import shutil
from pathlib import Path
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from linux_support import EXTENSIONS, runtime_profile
from guest_commands import run_command

COMMAND_LOG = None


def run(*args):
    return run_command(args, timeout=600, log=COMMAND_LOG)


def state_hashes():
    files = [Path('/home/fncpn-validation/.config/fncpn/config.json')]
    files += sorted(Path('/var/lib/fncpn/client/1000/credentials').rglob('*'))
    return {str(path): hashlib.sha256(path.read_bytes()).hexdigest() for path in files if path.is_file()}


def run_test(binary, name, *prefix):
    if name not in run(binary, '-test.list', '^' + name + '$').splitlines():
        raise ValueError('Missing acceptance test: ' + name)
    run(*prefix, binary, '-test.v', '-test.timeout=180s', '-test.run', '^' + name + '$')


def configure_netfilter_test(directory=Path('/validation/netfilter-bin')):
    """Select nft only for the disposable test process, never system alternatives."""
    if 'nf_tables' in run('iptables', '--version'):
        return
    nft = shutil.which('iptables-nft')
    if nft is None:
        raise ValueError('Network acceptance requires the iptables-nft test tool')
    directory.mkdir(parents=True, exist_ok=True)
    (directory/'iptables').symlink_to(nft)
    os.environ['PATH'] = str(directory) + os.pathsep + os.environ['PATH']
    if 'nf_tables' not in run('iptables', '--version'):
        raise ValueError('Network acceptance did not select the nft backend')


def fixture_packages(out, build):
    metadata = json.loads((out / 'upgrade-fixture/fixture.json').read_text())
    if (metadata != build.get('upgradeFixture') or metadata['commit'] != build['commit']
            or metadata['productVersion'] != build['productVersion']
            or metadata['kind'] != 'candidate-code-lower-package-version'):
        raise ValueError('Upgrade fixture does not match the candidate build')
    if tuple(map(int, metadata['metadataVersion'].split('.'))) >= tuple(map(int, build['productVersion'].split('.'))):
        raise ValueError('Upgrade fixture must have a lower package version')
    if any(Path(entry['path']).name != entry['path'] for entry in metadata['artifacts']):
        raise ValueError('Invalid upgrade fixture path')
    paths = [out / 'upgrade-fixture' / entry['path'] for entry in metadata['artifacts']]
    if len(paths) != 2 or len(set(paths)) != 2 or any(hashlib.sha256(path.read_bytes()).hexdigest() != entry['sha256']
                              for path, entry in zip(paths, metadata['artifacts'])):
        raise ValueError('Upgrade fixture checksum mismatch')
    return paths, metadata


def main():
    global COMMAND_LOG
    mode = sys.argv[1]
    if mode not in ('prepare', 'finish'):
        raise ValueError('Expected prepare or finish')
    full = os.environ.get('FULL_ACCEPTANCE') == '1'
    target = os.environ['TARGET']
    profile = runtime_profile(target, 'loong64')
    out = Path(os.environ.get('DIST_DIR', '/workspace/dist/linux')) / target / 'loong64'
    COMMAND_LOG = out / 'package-commands.log'
    build = json.loads((out / 'build-manifest.json').read_text())
    packages = sorted(out.glob('*.' + EXTENSIONS[profile['format']]))
    if len(packages) != 2:
        raise ValueError('Expected exact Core/UI pair')
    hashes = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in packages}
    if hashes != {a['path']: a['sha256'] for a in build['artifacts']}:
        raise ValueError('Candidate hashes differ from build manifest')
    report = {'schemaVersion': 1, 'passed': False, 'target': target, 'architecture': 'loong64',
              'bundle': profile['format'], 'productVersion': build['productVersion'], 'commit': build['commit'],
              'validationCommit': os.environ.get('FNCPN_VALIDATION_COMMIT', build['commit']),
              'scope': 'full' if full else 'installation/startup', 'artifacts': hashes, 'checks': [],
              'notValidated': ['physical hardware, GPU, full desktop/Wayland/polkit interaction',
                               'real fnOS authentication and historical release compatibility']}
    if full and report['validationCommit'] != build['commit']:
        raise ValueError('Full acceptance source differs from product commit')
    if mode == 'finish':
        report = json.loads((out / 'runtime-validation.json').read_text())
        # A previous success can never survive a failed rerun.
        report['passed'] = False
    fixtures, fixture = fixture_packages(out, build) if full else ([], None)

    def install(items, reinstall=False):
        if profile['format'] == 'deb':
            run('apt-get', 'install', '-y', '--no-install-recommends', *(['--reinstall'] if reinstall else []), *map(str, items))
        elif profile['format'] == 'rpm':
            run('dnf', *(['--cacheonly'] if mode == 'finish' else []), 'reinstall' if reinstall else 'install', '-y',
                *(['--setopt=localpkg_gpgcheck=True'] if os.environ.get('RELEASE') == '1' else []), *map(str, items))
        else:
            run('pacman', '-U', '--noconfirm', *map(str, items))

    def remove():
        if profile['format'] == 'deb':
            run('apt-get', 'remove', '-y', 'fncpn-ui', 'fncpn-core')
        elif profile['format'] == 'rpm':
            run('dnf', *(['--cacheonly'] if mode == 'finish' else []), 'remove', '-y', 'fncpn-ui', 'fncpn-core')
        else:
            run('pacman', '-R', '--noconfirm', 'fncpn-ui', 'fncpn-core')

    def user(*args):
        return run('runuser', '-u', 'fncpn-validation', '--', 'env', 'XDG_RUNTIME_DIR=/run/user/1000',
                   'DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus', *args)

    def services():
        run('systemctl', 'is-active', '--quiet', 'fncpn-client-privileged@1000.service')
        user('systemctl', '--user', 'is-active', '--quiet', 'fncpn-client.service')
        run('/usr/bin/fncpn', 'internal-healthcheck', '--role', 'client-privileged', '--socket', '/run/fncpn/1000/privileged.sock')
        user('/usr/bin/fncpn', 'internal-healthcheck', '--role', 'client', '--socket', '/run/user/1000/fncpn/client.sock')

    try:
        if mode == 'prepare':
            fields = {line.split('=', 1)[0]: shlex.split(line.split('=', 1)[1])[0]
                      for line in Path('/etc/os-release').read_text().splitlines()
                      if '=' in line and shlex.split(line.split('=', 1)[1])}
            if (fields.get('ID') != profile['osID'] or (profile['osVersion'] and fields.get('VERSION_ID') != profile['osVersion'])
                    or (profile.get('osCodename') and fields.get('VERSION_CODENAME') != profile['osCodename'])):
                raise ValueError('Runtime distribution/version differs')
            report['osRelease'] = fields
            spec = importlib.util.spec_from_file_location('packages', '/workspace/scripts/check-linux-packages.py')
            inspector = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(inspector)
            for path in packages:
                inspector.validate_target(inspector.inspect(path), path, target, 'loong64', build['productVersion'])
            report['checks'].append('package-metadata')
            if profile['format'] == 'rpm' and os.environ.get('RELEASE') == '1':
                from rpm_signing import KEY, verify
                report['rpmSigning'] = verify(packages + fixtures)
                run('rpmkeys', '--import', str(KEY))
                report['checks'].append('rpm-signature')
            core = next(p for p in packages if p.name.startswith('fncpn-core'))
            install([core])
            query = {'rpm': ['rpm', '-qa'], 'deb': ['dpkg-query', '-W', '-f=${Package}\n'], 'archlinux': ['pacman', '-Qq']}[profile['format']]
            installed = run(*query)
            (out / 'runtime-core-packages.txt').write_text(installed)
            if any(line.startswith(('libgtk', 'libwebkit', 'libjavascriptcoregtk', 'gtk3', 'webkit2gtk')) for line in installed.splitlines()):
                raise RuntimeError('Core-only environment contains GUI libraries')
            if run('/usr/bin/fncpn', 'version') != build['productVersion']:
                raise ValueError('Installed Core version differs')
            report['checks'] += ['core-only-install', 'core-version']
            install(packages)
            report['checks'].append('dependency-resolution')
            linked = run('ldd', '-r', '/usr/bin/fncpn-ui')
            (out / 'runtime-ldd.txt').write_text(linked)
            if 'not found' in linked or 'undefined symbol:' in linked:
                raise RuntimeError('Installed UI cannot resolve its ABI')
            report['checks'].append('ui-dynamic-linking')
            # Add diagnostic tools only after product dependency resolution.
            if profile['format'] == 'deb':
                run('apt-get', 'install', '-y', '--no-install-recommends', 'xvfb', 'xauth', 'dbus', 'fonts-dejavu-core',
                    'passwd', 'libgl1-mesa-dri', 'libpam-systemd', 'udev', 'kmod', 'binutils', 'iproute2', 'iptables',
                    'nftables', 'wireguard-tools', 'iputils-ping')
            elif profile['format'] == 'rpm':
                run('dnf', 'install', '-y', 'xorg-x11-server-Xvfb', 'xorg-x11-xauth', 'dbus-daemon',
                    profile.get('testFontPackage', 'dejavu-fonts'), 'shadow', 'mesa-dri-drivers', 'systemd-pam',
                    'systemd-udev', 'kmod', 'binutils', 'iproute', 'iptables', 'nftables', 'wireguard-tools', 'iputils',
                    *(['iptables-nft'] if full else []))
            else:
                run('pacman', '-S', '--noconfirm', '--needed', 'xorg-server-xvfb', 'xorg-xauth', 'dbus', 'ttf-dejavu',
                    'shadow', 'mesa', 'kmod', 'binutils', 'iproute2', 'iptables', 'nftables', 'wireguard-tools', 'iputils')
            from linux_abi import inspect
            report['abi'] = inspect('/usr/bin/fncpn', '/usr/bin/fncpn-ui')
            if build.get('abi') and report['abi']['requiredVersions'] != build['abi']['requiredVersions']:
                raise ValueError('Installed ABI differs from candidate')
            report['checks'].append('elf-abi')
            if profile['osID'] == 'anolis':
                run('systemctl', 'enable', 'dbus-broker.service')
                if Path('/usr/lib/systemd/user/dbus-broker.service').exists():
                    run('systemctl', '--global', 'enable', 'dbus-broker.service')
            if full:
                remove()
                install(fixtures)
                report['upgradeFixture'] = fixture
            return

        if Path('/proc/1/comm').read_text().strip() != 'systemd':
            raise RuntimeError('Acceptance requires a booted systemd VM')
        run('useradd', '--create-home', '--uid', '1000', 'fncpn-validation')
        run('loginctl', 'enable-linger', 'fncpn-validation')
        run('systemctl', 'start', 'user@1000.service')
        run('/usr/libexec/fncpn/enable-client', '1000')
        user('systemctl', '--user', 'enable', '--now', 'fncpn-client.service')
        services()
        report['checks'].append('systemd-services')
        before = None
        if full:
            user('env', 'FNCPN_PACKAGE_STATE=seed', '/validation/probe', '-test.run=^TestPackageState$', '-test.v')
            before = state_hashes()
            if len(before) < 2:
                raise ValueError('Real identity/config fixture was not created')
            install(packages)
            services()
            version = (run('dpkg-query', '-W', '-f=${Version}', 'fncpn-core') if profile['format'] == 'deb' else
                       run('rpm', '-q', '--qf', '%{VERSION}', 'fncpn-core') if profile['format'] == 'rpm' else
                       run('pacman', '-Q', 'fncpn-core').split()[1].rsplit('-', 1)[0])
            if version != build['productVersion']:
                raise ValueError('Package upgrade did not install candidate version')
            if before != state_hashes():
                raise ValueError('Package upgrade changed identity/configuration')
            user('env', 'FNCPN_PACKAGE_STATE=check', '/validation/probe', '-test.run=^TestPackageState$', '-test.v')
            report['checks'] += ['package-upgrade', 'upgrade-state-preserved']
        spec = importlib.util.spec_from_file_location('compatibility', '/workspace/scripts/linux-compatibility.py')
        compatibility = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(compatibility)
        os.environ['FNCPN_UI_SMOKE_TIMEOUT'] = '300'
        report['ui'] = compatibility.smoke(out, build['productVersion'], build['commit'])
        report['checks'].append('ui-frontend-handshake')
        if full:
            os.environ.update(FNCPN_LOONGARCH_VM='1', FNCPN_KERNEL_TESTS='1', FNCPN_NETFILTER_TEST='1')
            for module in ('wireguard', 'nf_tables'):
                run('modprobe', module)
            run('systemctl', 'start', 'systemd-resolved')
            Path('/etc/resolv.conf').unlink(missing_ok=True)
            Path('/etc/resolv.conf').symlink_to('/run/systemd/resolve/stub-resolv.conf')
            for binary, name in [('client.test', 'TestLinuxLocalProbeBindsPhysicalInterface'),
                                 ('linux.test', 'TestKernelClientCapability'),
                                 ('privileged.test', 'TestKernelDNSBridgeCapability'),
                                 ('linux.test', 'TestDockerForwardingIntegration'),
                                 ('probe', 'TestWireGuardTrafficAndReconnect'), ('probe', 'TestRealResolvedDNS')]:
                if name == 'TestDockerForwardingIntegration':
                    configure_netfilter_test()
                run_test('/validation/' + binary, name)
            report['checks'] += ['kernel-network', 'real-resolved']
        install(packages, reinstall=True)
        services()
        if full and before != state_hashes():
            raise ValueError('Reinstall changed identity/configuration')
        report['checks'].append('package-reinstall')
        remove()
        if any(Path('/usr/bin', name).exists() for name in ('fncpn', 'fncpn-ui')):
            raise ValueError('Uninstall left executables')
        if subprocess.run(['pgrep', '-x', 'fncpn'], check=False).returncode == 0:
            raise ValueError('Uninstall left services running')
        if full and before != state_hashes():
            raise ValueError('Uninstall changed retained identity/configuration')
        report['checks'].append('package-remove')
        if full and target != 'anolis23-loong64':
            from linux_acceptance import validate_evidence
            report['passed'] = True
            validate_evidence(report, target, 'loong64', build['productVersion'], build['commit'], hashes)
        else:
            report['notValidated'] += ['kernel/network regression', 'package upgrade/state preservation']
        report['passed'] = True
        report['notValidated'].append('historical package upgrade (fixture uses candidate code)')
    except Exception as error:
        report['passed'] = False
        report['error'] = str(error)
        raise
    finally:
        (out / 'runtime-validation.json').write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    main()
