#!/usr/bin/env python3
"""Release-only compatibility gate: install the exact packages in a clean distro."""
import hashlib
import json
import os
import pathlib
import shlex
import signal
import shutil
import subprocess
import sys
import tempfile
from linux_support import ROOT, package_name, target_profile
from linux_abi import inspect
from rpm_signing import KEY, verify


def run(*args, **kwargs):
    return subprocess.run(args, check=True, timeout=900, **kwargs)


def install(profile, packages, reinstall=False):
    names = list(map(str, packages))
    if profile['format'] == 'deb':
        run('apt-get', 'install', '-y', '--no-install-recommends', *(['--reinstall'] if reinstall else []), *names)
    elif profile['format'] == 'rpm':
        if profile['osID'].startswith('opensuse'):
            run('zypper', '--non-interactive', 'install', '--no-recommends', *(['--force'] if reinstall else []), *names)
        else:
            run('dnf', 'reinstall' if reinstall else 'install', '-y', '--setopt=localpkg_gpgcheck=True', '--setopt=install_weak_deps=False', *names)
    else:
        run('pacman', '-U', '--noconfirm', *names)


def installed_packages(profile):
    if profile['format'] == 'deb':
        command = ['dpkg-query', '-W', '-f=${Package}\t${Version}\n']
    elif profile['format'] == 'rpm':
        command = ['rpm', '-qa', '--qf', '%{NAME}\t%{VERSION}-%{RELEASE}\n']
    else:
        command = ['pacman', '-Q']
    return subprocess.check_output(command, text=True).splitlines()


def install_test_tools(profile):
    # Install AFTER the real packages so test tools cannot hide missing runtime dependencies.
    if profile['format'] == 'deb':
        run('apt-get', 'install', '-y', '--no-install-recommends', 'binutils', 'xvfb', 'xauth', 'dbus', 'fonts-dejavu-core', 'passwd', 'libgl1-mesa-dri')
    elif profile['format'] == 'rpm':
        if profile['osID'].startswith('opensuse'):
            run('zypper', '--non-interactive', 'install', '--no-recommends', 'binutils', 'xorg-x11-server', 'xvfb-run', 'xauth', 'dbus-1', 'dbus-1-daemon', 'dejavu-fonts', 'shadow', 'Mesa-dri')
        else:
            run('dnf', 'install', '-y', 'binutils', 'xorg-x11-server-Xvfb', 'xorg-x11-xauth', 'dbus-daemon', 'dejavu-sans-fonts', 'shadow-utils', 'mesa-dri-drivers')
    else:
        run('pacman', '-S', '--needed', '--noconfirm', 'binutils', 'xorg-server-xvfb', 'xorg-xauth', 'dbus', 'ttf-dejavu', 'shadow', 'mesa')


def require_smoke_tools(as_user):
    tools = ['dbus-run-session', 'dbus-daemon', 'xvfb-run', 'Xvfb', 'xauth']
    if as_user:
        tools.extend(['runuser', 'useradd'])
    missing = [tool for tool in tools if not shutil.which(tool)]
    if missing:
        raise RuntimeError('UI test environment is missing commands: ' + ', '.join(missing))


def sandbox_probe(out, as_user):
    # Recent GTK uses Glycin/bubblewrap even for SVG theme icons. Test the
    # nested user/mount/device namespaces first, with the same unprivileged user.
    # Older distro stacks may not use an external bwrap executable.
    if not shutil.which('bwrap'):
        return
    command = (['runuser', '-u', 'fncpn-smoke', '--'] if as_user else []) + [
        'bwrap', '--unshare-all', '--die-with-parent', '--chdir', '/',
        '--ro-bind', '/', '/', '--dev', '/dev', '--proc', '/proc', '/usr/bin/true']
    with (out / 'sandbox.log').open('w') as log:
        result = subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, timeout=30)
    if result.returncode:
        raise RuntimeError('Nested image/WebKit sandbox unavailable; see sandbox.log')


def smoke(out, version, commit, binary='/usr/bin/fncpn-ui', as_user=True):
    require_smoke_tools(as_user)
    if as_user:
        run('useradd', '--create-home', '--uid', '12345', 'fncpn-smoke')
    sandbox_probe(out, as_user)
    with tempfile.TemporaryDirectory(prefix='fncpn-ui-smoke-') as directory:
        if as_user:
            os.chown(directory, 12345, 12345)
        result = pathlib.Path(directory) / 'ready.json'
        command = (['runuser', '-u', 'fncpn-smoke', '--'] if as_user else []) + ['env',
                   'XDG_RUNTIME_DIR=' + directory, 'GDK_BACKEND=x11',
                   'LIBGL_ALWAYS_SOFTWARE=1', 'LD_BIND_NOW=1', 'G_MESSAGES_DEBUG=all',
                   'dbus-run-session', '--', 'xvfb-run', '-a',
                   '--server-args=-screen 0 1024x768x24 -nolisten tcp',
                   str(binary), '--smoke-test', str(result)]
        # Kill the full process group on timeout, including WebKit/Xvfb children.
        with (out / 'ui.log').open('w') as log:
            process = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            try:
                code = process.wait(timeout=90)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
                raise RuntimeError('UI smoke timed out; see ui.log')
        if code != 0 or not result.is_file():
            raise RuntimeError('UI failed before frontend handshake; see ui.log')
        ready = json.loads(result.read_text())
        if ready != {'version': version, 'commit': commit, 'platform': 'linux', 'frontendReady': True}:
            raise ValueError('UI handshake source/version mismatch: ' + repr(ready))
        (out / "ready.json").write_text(json.dumps(ready, indent=2) + "\n")
        return ready


def main():
    target, arch, directory = sys.argv[1:]
    profile = target_profile(target, arch)
    release = dict((line.split('=', 1)[0], shlex.split(line.split('=', 1)[1])[0])
                   for line in pathlib.Path('/etc/os-release').read_text().splitlines()
                   if '=' in line and shlex.split(line.split('=', 1)[1]))
    if release.get('ID') != profile['osID'] or (profile['osVersion'] and release.get('VERSION_ID') != profile['osVersion']):
        raise SystemExit('Compatibility container does not match requested target')
    directory = pathlib.Path(directory).resolve() / profile['format'] / arch
    version = (ROOT / 'internal/version/VERSION').read_text().strip()
    commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
    packages = [directory / package_name(c, version, target, arch) for c in ('core', 'ui')]
    out = ROOT / 'dist/linux-compat' / f'{target}-{arch}'
    out.mkdir(parents=True, exist_ok=True)
    evidence = {'target': target, 'architecture': arch, 'bundle': profile['format'],
                'productVersion': version, 'commit': commit, 'osRelease': release,
                'containerImage': os.environ.get('FNCPN_COMPAT_IMAGE', profile['image']),
                'checks': [], 'passed': False,
                'artifacts': {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in packages}}
    try:
        run(sys.executable, str(ROOT / 'scripts/check-linux-packages.py'), str(directory), target, arch)
        evidence['checks'].append('package-metadata')
        if profile['format'] == 'rpm':
            evidence['rpmSigning'] = verify(packages)
            run('rpmkeys', '--import', str(KEY))
            evidence['checks'].append('rpm-signature')
        before = installed_packages(profile)
        install(profile, packages[:1])
        core_packages = installed_packages(profile)
        added = set(core_packages) - set(before)
        if any(line.split()[0].startswith(('libgtk', 'gtk3', 'libwebkit', 'webkit2gtk')) for line in added):
            raise ValueError('Core-only installation pulled in GUI libraries')
        evidence['coreInstalledPackages'] = core_packages
        run('/usr/bin/fncpn', 'version')
        evidence['checks'].extend(['core-only-install', 'core-version'])
        install(profile, packages)
        evidence['runtimePackages'] = installed_packages(profile)
        evidence['checks'].append('dependency-resolution')
        # Check before Xvfb/font dependencies can mask missing ELF libraries.
        link_check = subprocess.run(['ldd', '-r', '/usr/bin/fncpn-ui'], text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        linked = link_check.stdout
        (out / 'ldd.log').write_text(linked)
        if link_check.returncode or 'not found' in linked or 'undefined symbol:' in linked:
            raise ValueError(linked)
        install_test_tools(profile)
        evidence['abi'] = inspect('/usr/bin/fncpn', '/usr/bin/fncpn-ui')
        baseline = json.loads((directory / 'build-manifest.json').read_text())
        if baseline['commit'] != commit or baseline['architecture'] != arch:
            raise ValueError('ABI baseline belongs to a different build')
        if baseline['abi']['requiredVersions'] != evidence['abi']['requiredVersions']:
            raise ValueError('Installed UI ABI differs from baseline artifact')
        evidence['abiBaseline'] = baseline['abi']
        evidence['checks'].extend(['ui-dynamic-linking', 'elf-abi'])
        evidence['ui'] = smoke(out, version, commit)
        evidence['checks'].append('ui-frontend-handshake')
        install(profile, packages, reinstall=True)  # Same-version maintenance scripts must also work.
        evidence['checks'].append('package-reinstall')
        if profile['format'] == 'deb':
            run('apt-get', 'remove', '-y', 'fncpn-ui', 'fncpn-core')
        elif profile['format'] == 'rpm':
            run(*(['zypper', '--non-interactive', 'remove'] if profile['osID'].startswith('opensuse') else ['dnf', 'remove', '-y']), 'fncpn-ui', 'fncpn-core')
        else:
            run('pacman', '-R', '--noconfirm', 'fncpn-ui', 'fncpn-core')
        if pathlib.Path('/usr/bin/fncpn-ui').exists() or pathlib.Path('/usr/bin/fncpn').exists():
            raise ValueError('Package removal left installed executables')
        evidence['checks'].append('package-remove')
        evidence['passed'] = True
    except Exception as error:
        evidence['error'] = str(error)
        raise
    finally:
        (out.parent / f'{target}-{arch}.json').write_text(json.dumps(evidence, indent=2) + '\n')


if __name__ == '__main__':
    main()
