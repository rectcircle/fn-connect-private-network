"""Build an unsigned, inspectable device Release IPA; never install or publish."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from ios_release import CHECKS, packages, verify_ipa

ROOT = Path(__file__).resolve().parents[2]


def run(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=Path, default=ROOT / 'dist/ios')
    parser.add_argument('--allow-dirty', action='store_true', help='Local review only; candidate inventory rejects this evidence')
    args = parser.parse_args()
    dirty = bool(run('git', 'status', '--porcelain'))
    if dirty and not args.allow_dirty:
        parser.error('Release IPA requires a clean checkout; use --allow-dirty only for local review')
    version = (ROOT / 'internal/version/VERSION').read_text().strip()
    number = int((ROOT / 'apps/ios/BUILD_NUMBER').read_text())
    commit = run('git', 'rev-parse', 'HEAD')
    out = args.out.resolve(); out.mkdir(parents=True, exist_ok=True)
    name = packages(version)[0]
    if (out / name).exists() or (out / 'build-manifest.json').exists():
        parser.error('Output already exists; choose a new directory')
    xcode = run('xcodebuild', '-version')
    if not xcode.startswith('Xcode 27.1\n'):
        parser.error('The iOS release toolchain requires Xcode 27.1')
    subprocess.run([sys.executable, 'scripts/ios/project.py'], cwd=ROOT, check=True)
    with tempfile.TemporaryDirectory(prefix='fncpn-ipa-') as temporary:
        work = Path(temporary)
        subprocess.run(['xcodebuild', '-project', 'apps/ios/FnCPN.xcodeproj', '-scheme', 'FnCPN',
                        '-configuration', 'Release', '-sdk', 'iphoneos', '-derivedDataPath', str(work / 'DerivedData'),
                        'CODE_SIGNING_ALLOWED=NO', 'build'], cwd=ROOT, check=True)
        source = work / 'DerivedData/Build/Products/Release-iphoneos/FnCPN.app'
        payload = work / 'Payload'; payload.mkdir()
        shutil.copytree(source, payload / 'FnCPN.app', symlinks=True)
        app = payload / 'FnCPN.app'
        # The Go ARM64 linker can emit an ad-hoc Mach-O signature even when
        # Xcode signing is disabled. Public IPA assets contain neither kind.
        for target in [app / 'PlugIns/PacketTunnel.appex', app]:
            signed = subprocess.run(['codesign', '-dv', str(target)], capture_output=True, text=True)
            if signed.returncode == 0:
                subprocess.run(['codesign', '--remove-signature', str(target)], check=True)
            elif 'not signed at all' not in signed.stderr:
                raise SystemExit('Cannot inspect code signature: ' + signed.stderr)

        subprocess.run(['ditto', '-c', '-k', '--norsrc', '--keepParent', str(payload), str(out / name)], check=True)
    verify_ipa(out / name, version, number)
    if not args.allow_dirty and run('git', 'status', '--porcelain'):
        raise SystemExit('iOS build changed tracked sources; candidate refused')
    build = {'productVersion': version, 'commit': commit, 'workingTreeDirty': dirty,
             'configuration': 'Release', 'signed': False, 'architecture': 'arm64', 'buildNumber': number,
             'xcode': xcode, 'sdk': run('xcrun', '--sdk', 'iphoneos', '--show-sdk-version'),
             'checks': sorted(CHECKS), 'artifacts': [{'file': name, 'sha256': hashlib.sha256((out / name).read_bytes()).hexdigest()}]}
    (out / 'build-manifest.json').write_text(json.dumps(build, indent=2) + '\n')
    print(out / name)


if __name__ == '__main__':
    main()
