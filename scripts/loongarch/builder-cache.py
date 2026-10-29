#!/usr/bin/env python3
"""Cache only provisioned tools, before product builds or signing secrets exist."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from linux_support import ROOT, package_profile


def main():
    target = os.environ['TARGET']
    digest = hashlib.sha256(json.dumps(package_profile(target), sort_keys=True).encode())
    digest.update(datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m').encode())
    for name in ('scripts/loongarch/probe-format.sh', 'scripts/loongarch/environment.json', 'scripts/loongarch/configure-openeuler-repos.sh', 'scripts/linux-ci-dependencies.py'):
        digest.update((ROOT / name).read_bytes())
    key = digest.hexdigest()[:24]
    image = 'fncpn-builder-' + target + ':' + key
    archive = Path.home() / '.cache/fncpn-loongarch-formats' / target / ('builder-' + key + '.tar')
    hit = archive.is_file()
    if sys.argv[1] == 'restore':
        if hit:
            subprocess.run(['docker', 'load', '-i', str(archive)], check=True)
        with Path(os.environ['GITHUB_ENV']).open('a') as env:
            env.write('BUILDER_CACHE_HIT=' + ('1' if hit else '0') + '\n')
            env.write('BUILDER_IMAGE=' + image + '\n')
    elif sys.argv[1] == 'save':
        if hit:
            return
        # Called after verified SDK extraction, before compilation/signing.
        # Checkout, module/build caches and packaging tool are bind mounts and
        # excluded from docker commit. No runtime users or product state exist.
        subprocess.run(['docker', 'exec', 'fncpn-format', 'rm', '-f', '/opt/go.tar.gz'], check=True)
        subprocess.run(['docker', 'commit', 'fncpn-format', image], check=True)
        archive.parent.mkdir(parents=True, exist_ok=True)
        partial = archive.with_suffix('.partial')
        subprocess.run(['docker', 'save', '-o', str(partial), image], check=True)
        partial.replace(archive)
    else:
        raise ValueError('Expected restore or save')


if __name__ == '__main__':
    main()
