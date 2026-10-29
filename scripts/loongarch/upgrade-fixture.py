#!/usr/bin/env python3
"""Lower package metadata with candidate binaries; never historical compatibility proof."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
from linux_support import EXTENSIONS, package_profile


def fixture_version(version):
    major, minor, patch = map(int, version.split('.'))
    # '~' ordering is not portable to pacman. A numeric predecessor works for
    # all three package managers; these archives are explicitly test fixtures.
    if patch:
        return f'{major}.{minor}.{patch-1}'
    if minor:
        return f'{major}.{minor-1}.0'
    if major:
        return f'{major-1}.0.0'
    raise ValueError('No lower fixture version')


def main(target, directory):
    spec = importlib.util.spec_from_file_location('package', ROOT / 'scripts/linux-package.py')
    builder = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(builder)
    directory = Path(directory)
    out = directory / 'upgrade-fixture'
    out.mkdir(parents=True, exist_ok=True)
    version = (ROOT / 'internal/version/VERSION').read_text().strip()
    lower = fixture_version(version)
    profile = package_profile(target, 'loong64')
    for component, binary in (('core', 'fncpn'), ('ui', 'fncpn-ui')):
        with tempfile.TemporaryDirectory() as temporary:
            temporary = Path(temporary)
            runtime = builder.elf_dependencies(str(directory / binary), profile) if component == 'ui' else []
            config = builder.configuration(component, target, 'loong64', lower, str(directory / binary), temporary, runtime)
            source = temporary / 'nfpm.json'
            source.write_text(json.dumps(config))
            subprocess.run([os.environ['NFPM'], 'package', '-f', str(source), '-p', profile['format'],
                            '-t', str(out / ('fncpn-' + component + '.' + EXTENSIONS[profile['format']]))], cwd=ROOT, check=True)
    (out / 'fixture.json').write_text(json.dumps({
        'kind': 'candidate-code-lower-package-version', 'productVersion': version, 'metadataVersion': lower,
        'commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
        'notValidated': ['historical release/schema migration']}, indent=2)+'\n')


if __name__ == '__main__':
    main(*sys.argv[1:])
