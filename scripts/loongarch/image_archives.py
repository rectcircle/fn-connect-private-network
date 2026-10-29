"""Fetch identical official openEuler archives through mirrors and a content cache."""
import hashlib
import os
from pathlib import Path
import re
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from linux_support import runtime_profile

ROOTS = ('https://mirrors.huaweicloud.com/openeuler/',
         'https://mirrors.ustc.edu.cn/openeuler/', 'https://repo.openeuler.org/')


def image_archive(profile, cache=None):
    digest = profile['imageArchiveSHA256']
    origin = profile['imageArchive']
    if not re.fullmatch('[0-9a-f]{64}', digest) or not origin.startswith(ROOTS[-1]):
        raise ValueError('Expected a pinned official openEuler archive')
    cache = Path(cache) if cache is not None else Path.home()/'.cache/fncpn-loongarch-images'
    cache.mkdir(parents=True, exist_ok=True)
    destination = cache/(digest+'.tar.xz')
    if destination.is_file() and hashlib.sha256(destination.read_bytes()).hexdigest() == digest:
        print('Verified image archive cache hit:', digest, flush=True)
        return destination
    destination.unlink(missing_ok=True)
    partial = destination.with_suffix('.partial')
    errors = []
    for root in ROOTS:
        url = root + origin[len(ROOTS[-1]):]
        print('Downloading pinned image archive:', url, flush=True)
        try:
            subprocess.run(['curl', '-4', '--fail', '--silent', '--show-error', '--location',
                '--proto', '=https', '--proto-redir', '=https', '--connect-timeout', '6',
                '--max-time', '90', '--speed-limit', '51200', '--speed-time', '20',
                '--output', str(partial), url], check=True, timeout=100)
            if hashlib.sha256(partial.read_bytes()).hexdigest() != digest:
                raise ValueError('Image archive SHA256 differs from pinned official checksum')
            partial.replace(destination)
            return destination
        except (subprocess.SubprocessError, OSError, ValueError) as error:
            errors.append(f'{root}: {error}')
        finally:
            partial.unlink(missing_ok=True)
    raise RuntimeError('No verified image archive downloaded: ' + '; '.join(errors))


if __name__ == '__main__':
    path = image_archive(runtime_profile(sys.argv[1], 'loong64'))
    if sys.argv[2:] == ['--load']:
        subprocess.run(['docker', 'load', '-i', str(path)], check=True, timeout=300)
