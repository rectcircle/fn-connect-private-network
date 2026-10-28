#!/usr/bin/env python3
"""Skip platform work only when every changed file is documentation."""
import os
import subprocess


def requires_checks(paths):
    paths = list(paths)
    # Unknown/empty comparisons fail open to full checking.
    return not paths or any(not (
        path.startswith('docs/') or path in ('TODO.txt', 'README.md', 'CHANGELOG.md')
    ) for path in paths)


def main():
    base = os.environ.get('CI_BASE', '')
    run = True
    if base and set(base) != {'0'}:
        comparison = subprocess.run(['git', 'diff', '--name-only', '-z', base, 'HEAD'], capture_output=True)
        if comparison.returncode == 0:
            run = requires_checks(comparison.stdout.decode().strip('\0').split('\0'))
    with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
        output.write(f'run={str(run).lower()}\n')


if __name__ == '__main__':
    main()
