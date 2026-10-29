#!/usr/bin/env python3
"""Record the exact target's matching Core/UI packages and source evidence."""
import hashlib
import json
import os
import pathlib
import subprocess
import sys

from linux_support import ROOT, package_name, package_profile
from linux_abi import inspect
from rpm_signing import verify


def main():
    out=pathlib.Path(sys.argv[1]);target=sys.argv[2];arch=sys.argv[3]
    profile=package_profile(target,arch)
    version=(ROOT/'internal/version/VERSION').read_text().strip()
    def git(*args): return subprocess.check_output(['git','-C',str(ROOT),*args],text=True).strip()
    artifacts=[]
    for component in ('core','ui'):
        name=package_name(component,version,target,arch)
        path=out/name
        if not path.is_file():raise SystemExit('Missing package: '+str(path))
        artifacts.append({'path':name,'sha256':hashlib.sha256(path.read_bytes()).hexdigest()})
    # Package content/metadata validation is mandatory before writing evidence.
    subprocess.run([sys.executable,str(ROOT/'scripts/check-linux-packages.py'),str(out),target,arch],check=True)
    os_release=pathlib.Path('/etc/os-release').read_text()
    record={'productVersion':version,'commit':git('rev-parse','HEAD'),'architecture':arch,
            'distribution':target,'packageFormat':profile['format'],'buildOSRelease':os_release,
            'workingTreeDirty':bool(git('status','--porcelain')),'artifacts':artifacts}
    fixture = out / 'upgrade-fixture/fixture.json'
    if fixture.exists():
        metadata = json.loads(fixture.read_text())
        if metadata.get('productVersion') != version or metadata.get('commit') != record['commit']:
            raise ValueError('Upgrade fixture differs from candidate')
        metadata['artifacts'] = [{'path': path.name, 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}
                                 for path in sorted(fixture.parent.iterdir()) if path.name.endswith(('.deb', '.rpm', '.pkg.tar.zst'))]
        if len(metadata['artifacts']) != 2:
            raise ValueError('Expected exactly two upgrade fixture packages')
        fixture.write_text(json.dumps(metadata, indent=2)+'\n')
        record['upgradeFixture'] = metadata
    if profile['format'] == 'rpm' and os.environ.get('RELEASE') == '1':
        record['rpmSigning'] = verify([out/a['path'] for a in artifacts])
    record['abi'] = inspect(out/'fncpn', out/'fncpn-ui')
    (out/'build-manifest.json').write_text(json.dumps(record,indent=2)+'\n')
    (out/'SHA256SUMS').write_text(''.join(f"{a['sha256']}  {a['path']}\n" for a in artifacts))


if __name__=='__main__':main()
