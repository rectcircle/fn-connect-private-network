#!/usr/bin/env python3
"""Check/update hashes with the flake's exact locked goModules derivations."""
import argparse
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SYSTEMS = ('x86_64-linux', 'aarch64-linux')
FAKE_HASH = 'sha256-' + 'A' * 43 + '='


def check(root, system, run=subprocess.run):
    command = ['nix', '--extra-experimental-features', 'nix-command flakes', 'build', '--no-link', '--no-update-lock-file']
    command += [f'.#packages.{system}.{part}.goModules' for part in ('core', 'ui')]
    result = run(command, cwd=root)
    if result.returncode == 0:
        # A fixed-output cache hit alone cannot prove the current source still
        # generates the pinned bytes. Rebuild it to detect stale local-module code.
        result = run(command + ['--rebuild'], cwd=root)
    if result.returncode:
        raise RuntimeError('Nix Go dependencies failed verification. For a hash mismatch, run '
                           f'scripts/update-nix-go-hashes.sh --system {system}, review nix/go-hashes.json, then rerun --check.')


def discover(root, system, part, run=subprocess.run):
    # Override only the expected hash of the actual fixed-output derivation.
    # The locked Go compiler, source filter, hooks and vendoring remain identical.
    expression = ('let f = builtins.getFlake (builtins.getEnv "FNCPN_NIX_SOURCE"); '
                  f'in f.packages.{system}.{part}.goModules.overrideAttrs '
                  f'(_: {{ outputHash = "{FAKE_HASH}"; }})')
    environment = dict(os.environ, FNCPN_NIX_SOURCE=str(root), NO_COLOR='1')
    result = run(['nix', '--extra-experimental-features', 'nix-command flakes', 'build', '--no-link', '--no-update-lock-file', '--impure', '--expr', expression],
                 cwd=root, env=environment, text=True, capture_output=True)
    output = re.sub(r'\x1b\[[0-9;]*m', '', result.stderr)
    matches = re.findall(r"hash mismatch in fixed-output derivation '([^']+)':\s*"
                         r'specified:\s*(sha256-[A-Za-z0-9+/]+=)\s*'
                         r'got:\s*(sha256-[A-Za-z0-9+/]+=)', output)
    expected = [(path, got) for path, specified, got in matches
                if f'fncpn-{part}-' in Path(path).name and path.endswith('-go-modules.drv') and specified == FAKE_HASH]
    if result.returncode == 0 or len(matches) != 1 or len(expected) != 1:
        raise RuntimeError(f'Cannot determine {part} vendor hash; no files changed.\n{output}')
    return expected[0][1]


def update(root, system, run=subprocess.run):
    hashes = {part: discover(root, system, part, run) for part in ('core', 'ui')}
    target = root / 'nix/go-hashes.json'
    before = target.read_bytes()
    if json.loads(before) == hashes:
        check(root, system, run)
        return
    # Publish both hashes together. If the real pinned build fails, restore bytes.
    fd, temporary = tempfile.mkstemp(dir=target.parent, prefix='.go-hashes-')
    try:
        os.fchmod(fd, target.stat().st_mode & 0o777)
        with os.fdopen(fd, 'w') as stream:
            stream.write(json.dumps(hashes, indent=2) + '\n')
        os.replace(temporary, target)
        try:
            check(root, system, run)
        except BaseException:
            target.write_bytes(before)
            raise
    finally:
        Path(temporary).unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='Verify both pinned dependency outputs without modifying files')
    parser.add_argument('--system', choices=SYSTEMS, default=('aarch64-linux' if platform.machine() in ('aarch64', 'arm64') else 'x86_64-linux'))
    args = parser.parse_args()
    if shutil.which('nix') is None:
        parser.error('Nix is required; run on a Linux Nix builder or CI. No tools are installed automatically.')
    try:
        (check if args.check else update)(ROOT, args.system)
    except (RuntimeError, OSError) as error:
        print(error, file=sys.stderr)
        return 1
    print(f'Nix Core/UI dependency hashes verified for {args.system}')
    return 0


if __name__ == '__main__':
    sys.exit(main())
