#!/usr/bin/env python3
"""Select exact candidate packages; never rebuild the product during acceptance."""
import json
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from linux_support import ROOT, COMPATIBILITY_TARGETS, package_profile
from validate import OUT, compile_tests


def prepare_tests():
    OUT.mkdir(parents=True, exist_ok=True)
    os.environ['FNCPN_ROOT_REGRESSION'] = '0'
    compile_tests()
    paths = [OUT/'probe', OUT/'tests/manifest.json', *sorted((OUT/'tests').glob('*.test'))]
    record = {'commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip(),
              'files': {str(p.relative_to(OUT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}}
    (OUT/'acceptance-tests.json').write_text(json.dumps(record, indent=2)+'\n')


def verify_tests(commit):
    record = json.loads((OUT/'acceptance-tests.json').read_text())
    if record['commit'] != commit:
        raise ValueError('Shared acceptance tests differ from the candidate commit')
    for name, digest in record['files'].items():
        path = Path(name)
        if path.is_absolute() or '..' in path.parts or hashlib.sha256((OUT/path).read_bytes()).hexdigest() != digest:
            raise ValueError('Shared acceptance test checksum/path differs')
    manifest = json.loads((OUT/'tests/manifest.json').read_text())
    required = {'/internal/client', '/internal/privileged', '/internal/platform/linux'}
    if not all(any(item['package'].endswith(name) and 'tests/'+item['binary'] in record['files'] for item in manifest) for name in required) or 'probe' not in record['files']:
        raise ValueError('Missing shared VM acceptance tests')
    for name in record['files']:
        if name.endswith('.test') or name == 'probe':
            (OUT/name).chmod(0o755)  # upload-artifact does not preserve executable mode.


def main():
    target = os.environ['TARGET']
    version = os.environ.get('VERSION') or (ROOT/'internal/version/VERSION').read_text().strip()
    full = os.environ.get('FULL_ACCEPTANCE') == '1'
    root = Path('dist/linux')
    destination = root / target / 'loong64'
    destination.mkdir(parents=True, exist_ok=True)
    baseline = COMPATIBILITY_TARGETS.get(target, {}).get('base', target)
    profile = package_profile(baseline, 'loong64')
    source = root / profile['bundle'] / 'loong64'
    record = json.loads((source / 'build-manifest.json').read_text())
    if version and record['productVersion'] != version:
        raise ValueError('Artifact version differs from requested version')
    expected = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
    if record.get('workingTreeDirty') is not False or record.get('architecture') != 'loong64' or record.get('distribution') != profile['bundle']:
        raise ValueError('Artifacts require a clean matching-architecture build from the selected baseline')
    if record['commit'] != expected:
        raise ValueError('Artifacts differ from the checked-out candidate commit')
    if source != destination:
        shutil.copytree(source, destination, dirs_exist_ok=True)
    if full and not (destination / 'upgrade-fixture/fixture.json').is_file():
        raise ValueError('Full acceptance requires artifacts built with full_validation=true')
    if full:
        verify_tests(expected)


if __name__ == '__main__':
    prepare_tests() if sys.argv[1:] == ['tests'] else main()
