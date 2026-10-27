"""Install the pinned Linux CLI mirror and prepare the community Go toolchain."""
import argparse
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.request

from setup_macos import cli_pin, cli_root, digest, locate


def extract(archive, destination):
    # Python's data filter rejects escaping paths/links and special device files.
    with tarfile.open(archive, 'r:gz') as bundle:
        bundle.extractall(destination, filter='data')


def download(url, target):
    request = urllib.request.Request(url, headers={'User-Agent': 'harmonyos-cli-setup'})
    with urllib.request.urlopen(request, timeout=120) as response, target.open('wb') as output:
        shutil.copyfileobj(response, output)


def install(pin, target, fetch=download):
    marker = {'version': pin['version'], 'sha256': pin['sha256'],
              'hostArchitecture': platform.machine(), 'source': pin['releaseBase']}
    if target.exists():
        if json.loads((target / '.harmonyos-install.json').read_text()) != marker:
            raise ValueError('Existing Linux CLI identity mismatch; refusing to overwrite')
        locate(target)
        return target
    target.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.linux-install-', dir=target.parent) as temporary:
        staging = Path(temporary)
        archive = staging / pin['archive']
        with archive.open('wb') as merged:
            for suffix in pin['parts']:
                part = staging / ('part.' + suffix)
                print('Downloading Linux CLI part ' + suffix, flush=True)
                fetch(pin['releaseBase'] + '/' + pin['archive'] + '.' + suffix, part)
                with part.open('rb') as stream:
                    shutil.copyfileobj(stream, merged)
                part.unlink()
        if digest(archive) != pin['sha256']:
            raise ValueError('Linux CLI archive SHA256 mismatch')
        unpacked = staging / 'unpacked'
        unpacked.mkdir()
        extract(archive, unpacked)
        archive.unlink()
        candidates = [unpacked] + list(unpacked.glob('*/'))
        cli = next((p for p in candidates if (p/'bin').is_dir() and (p/'sdk').is_dir()), None)
        if cli is None:
            raise ValueError('Linux CLI archive missing bin/ and sdk/')
        locate(cli)
        (cli / '.harmonyos-install.json').write_text(json.dumps(marker, indent=2) + '\n')
        cli.rename(target)
    return target


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='Inspect installed tools without downloading')
    args = parser.parse_args()
    if platform.system() != 'Linux' or platform.machine() not in ('x86_64', 'AMD64'):
        parser.error('Linux x86_64 required')
    if sys.version_info < (3, 12):
        parser.error('Python 3.12+ required for safe tar extraction')
    root = cli_root()
    if not args.check:
        install(cli_pin(), root)
    command = [sys.executable, str(Path(__file__).with_name('setup_macos.py')), '--cli-root', str(root)]
    if args.check:
        command.append('--check')
    return subprocess.run(command, env=os.environ.copy()).returncode


if __name__ == '__main__':
    raise SystemExit(main())
