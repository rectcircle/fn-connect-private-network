#!/usr/bin/env python3
"""Check the shared packages on a disposable target container, without starting services."""
import hashlib
import json
import pathlib
import shlex
import subprocess
import sys
import tempfile
from linux_support import ROOT, package_name, target_profile


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def main():
    target, arch, directory = sys.argv[1:]
    profile = target_profile(target, arch)
    release = dict((line.split('=',1)[0], shlex.split(line.split('=',1)[1])[0]) for line in pathlib.Path('/etc/os-release').read_text().splitlines() if '=' in line and shlex.split(line.split('=',1)[1]))
    if release.get('ID') != profile['osID'] or (profile['osVersion'] and release.get('VERSION_ID') != profile['osVersion']):
        raise SystemExit('Compatibility container does not match requested target')
    directory = pathlib.Path(directory).resolve() / profile['format'] / arch
    version = (ROOT / 'internal/version/VERSION').read_text().strip()
    packages = [directory / package_name(component, version, target, arch) for component in ('core', 'ui')]
    run(sys.executable, str(ROOT / 'scripts/check-linux-packages.py'), str(directory), target, arch)
    # Only graphics runtime libraries are installed; system services remain untouched.
    dependencies = [p for p in profile['uiDependencies'] if p not in ('policykit-1', 'polkit', 'polkitd', 'pkexec')]
    if profile['format'] == 'deb':
        run('apt-get', 'install', '-y', '--no-install-recommends', 'binutils', 'libarchive-tools', *dependencies)
        run('apt-get', '--simulate', 'install', *map(str, packages))
    elif profile['format'] == 'rpm':
        if profile['osID'].startswith('opensuse'):
            run('zypper', '--non-interactive', 'install', '--no-recommends', 'binutils', 'bsdtar', *dependencies)
            run('zypper', '--non-interactive', 'install', '--dry-run', '--allow-unsigned-rpm', *map(str, packages))
        else:
            run('dnf', 'install', '-y', 'binutils', 'bsdtar', *dependencies)
            run('dnf', 'install', '-y', '--downloadonly', '--setopt=localpkg_gpgcheck=False', *map(str, packages))
    else:
        run('pacman', '-S', '--needed', '--noconfirm', 'binutils', 'libarchive', *dependencies)
        # --print resolves the complete paired transaction without installing it.
        run('pacman', '-U', '--noconfirm', '--print', *map(str, packages))
    with tempfile.TemporaryDirectory(prefix='fncpn-compat-') as temporary:
        root = pathlib.Path(temporary)
        for package in packages:
            if profile['format'] == 'deb':
                run('dpkg-deb', '-x', str(package), temporary)
            else:
                run('bsdtar', '-xf', str(package), '-C', temporary)
        run(str(root / 'usr/bin/fncpn'), 'version')
        output = subprocess.check_output(['ldd', str(root / 'usr/bin/fncpn-ui')], text=True, stderr=subprocess.STDOUT)
        if 'not found' in output:
            raise SystemExit(output)
    evidence = {
        'target': target, 'architecture': arch, 'bundle': profile['format'], 'productVersion': version,
        'commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip(),
        'checks': ['package-metadata', 'dependency-resolution', 'core-version', 'ui-dynamic-linking'],
        'artifacts': {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in packages},
    }
    out = ROOT / 'dist/linux-compat' / f'{target}-{arch}.json'
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(evidence, indent=2) + '\n')


if __name__ == '__main__': main()
