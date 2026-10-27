"""Android release inventory shared by draft creation, publication and mirrors."""
import hashlib
import json
from pathlib import Path

ABIS = ('arm64-v8a', 'x86_64')
CHECKS = {'pinned-signing-certificate', 'package-version', 'expected-abis', 'jni-exports', '16kb-alignment'}


def certificate_fingerprint():
    return (Path(__file__).resolve().parents[1] / 'mobile/android/signing-certificate.sha256').read_text().strip()


def packages(version):
    if tuple(map(int, version.split('.'))) < (1, 1, 4):
        return []
    return [f'fncpn-{version}-android-{abi}.apk' for abi in ABIS]


def candidates(root, version, commit):
    artifacts = []
    codes = set()
    for abi, name in zip(ABIS, packages(version)):
        path = root / 'android' / name
        evidence = json.loads(path.with_name(name + '.verification.json').read_text())
        build = evidence.get('build', {})
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        if (evidence.get('localValidation', False) is not False or evidence.get('variant') != 'release' or evidence.get('apk') != name or evidence.get('sha256') != digest or
                evidence.get('signingCertificateSha256') != certificate_fingerprint() or
                not CHECKS.issubset(evidence.get('checks', [])) or
                build.get('version') != version or build.get('commit') != commit or build.get('dirty') is not False or
                not isinstance(build.get('versionCode'), int) or build['versionCode'] <= 0 or
                set(evidence.get('nativeSha256', {})) != {abi}):
            raise ValueError('Android candidate must match clean release source, signing identity, ABI and verified hash')
        codes.add(build['versionCode'])
        artifacts.append({'file': name, 'path': 'android/' + name, 'sha256': digest})
    if len(codes) != 1:
        raise ValueError('Android release candidates must have the same versionCode')
    return artifacts, {'certificateSha256': certificate_fingerprint(), 'versionCode': codes.pop(), 'abis': list(ABIS)}


def validate_inventory(manifest, version):
    expected = set(packages(version))
    if not expected:
        return
    actual = {a['file'] for a in manifest['artifacts'] if a['file'].endswith('.apk')}
    signing = manifest.get('build', {}).get('android', {})
    if (actual != expected or signing.get('certificateSha256') != certificate_fingerprint() or
            signing.get('abis') != list(ABIS) or not isinstance(signing.get('versionCode'), int) or signing['versionCode'] <= 0):
        raise ValueError('Release inventory is missing matching Android packages or signing evidence')
