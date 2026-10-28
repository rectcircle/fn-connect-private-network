#!/usr/bin/env python3
"""Lower DEB version using candidate code: installer fixture, never a historical release."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
spec = importlib.util.spec_from_file_location('package', ROOT / 'scripts/linux-package.py')
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)
directory = ROOT / 'dist/linux/deb-loong64/loong64'
out = ROOT / 'dist/loongarch-validation/upgrade-fixture'
out.mkdir(parents=True, exist_ok=True)
version = (ROOT / 'internal/version/VERSION').read_text().strip() + '~loongarch-upgrade-fixture'
for component, binary in (('core', 'fncpn'), ('ui', 'fncpn-ui')):
    with tempfile.TemporaryDirectory() as temporary:
        temporary = Path(temporary)
        runtime = builder.elf_dependencies(str(directory / binary), builder.package_profile('deb-loong64', 'loong64')) if component == 'ui' else []
        config = builder.configuration(component, 'deb-loong64', 'loong64', version, str(directory / binary), temporary, runtime)
        source = temporary / 'nfpm.json'
        source.write_text(json.dumps(config))
        subprocess.run([os.environ['NFPM'], 'package', '-f', str(source), '-p', 'deb',
                        '-t', str(out / f'fncpn-{component}.deb')], cwd=ROOT, check=True)
