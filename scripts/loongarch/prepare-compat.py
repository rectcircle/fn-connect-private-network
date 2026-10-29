#!/usr/bin/env python3
"""Create derivative test userspaces exclusively from upstream release sources."""
import os
from pathlib import Path
import subprocess
import tempfile
import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from linux_support import package_profile, runtime_profile
from validate import download


def run(*args):
    subprocess.run(args, check=True, timeout=1800)


def main():
    profile = runtime_profile(os.environ['TARGET'], 'loong64')
    with tempfile.TemporaryDirectory(prefix='fncpn-compat-rootfs-') as temporary:
        archive = Path(temporary) / 'rootfs.tar.gz'
        if profile.get('rootfsURL'):
            download(profile['rootfsURL'], archive, profile['rootfsSHA256'])
            run('docker', 'import', '--platform=linux/loong64', str(archive), profile['image'])
        elif profile['osID'] != 'anolis':
            if profile.get('imageArchive'):
                download(profile['imageArchive'], archive, profile['imageArchiveSHA256'])
                run('docker', 'load', '-i', str(archive))
            else:
                run('docker', 'pull', '--platform=linux/loong64', profile['image'])
        else:
            baseline = package_profile('rpm-loong64')
            download(baseline['imageArchive'], archive, baseline['imageArchiveSHA256'])
            run('docker', 'load', '-i', str(archive))
            container = 'fncpn-anolis-seed'
            root = Path.cwd() / 'dist/anolis-root'
            root.mkdir(parents=True, exist_ok=True)
            run('docker', 'run', '-d', '--platform=linux/loong64', '--name', container,
                '-v', str(root) + ':/candidate', baseline['image'], 'sleep', 'infinity')
            try:
                run('docker', 'exec', container, 'dnf', '--installroot=/candidate', '--releasever=23.5',
                    '--disablerepo=*', '--repofrompath=anolis,' + profile['repositoryURL'],
                    '--setopt=anolis.gpgcheck=1', '--setopt=anolis.gpgkey=' + profile['repositoryKey'],
                    '--setopt=install_weak_deps=False', '--setopt=timeout=30', '--setopt=retries=2',
                    'install', '-y', 'filesystem', 'anolis-release', 'bash', 'dnf', 'rpm', 'python3',
                    'tar', 'xz', 'coreutils', 'ca-certificates')
                exported = Path(temporary) / 'anolis-root.tar'
                with exported.open('wb') as stream:
                    run_result = subprocess.run(['sudo', 'tar', '--numeric-owner', '-C', str(root), '-cpf', '-', '.'], stdout=stream)
                if run_result.returncode:
                    raise RuntimeError('Could not export Anolis userspace')
                run('docker', 'import', '--platform=linux/loong64', str(exported), profile['image'])
            finally:
                subprocess.run(['docker', 'rm', '-f', container], check=False)
        run('docker', 'run', '-d', '--shm-size=1g', '--platform=linux/loong64',
            '--name', 'fncpn-format', '-e', 'TARGET', '-e', 'RELEASE', '-e', 'DIST_DIR',
            '-e', 'FULL_ACCEPTANCE', '-e', 'FNCPN_VALIDATION_COMMIT', '-v', str(Path.cwd()) + ':/workspace', profile['image'], 'sleep', 'infinity')
        if profile['osID'] == 'openEuler':
            cache = Path.home()/'.cache/fncpn-loongarch-runtime'/os.environ['TARGET']/'dnf'
            if cache.is_dir() and any(cache.iterdir()):
                # Copy cache into a fresh userspace; never restore an installed
                # rootfs. Docker export then also includes metadata for offline VM checks.
                run('docker', 'exec', 'fncpn-format', 'mkdir', '-p', '/var/cache/dnf')
                run('docker', 'cp', str(cache) + '/.', 'fncpn-format:/var/cache/dnf')



if __name__ == '__main__':
    main()
