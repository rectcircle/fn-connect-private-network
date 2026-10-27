"""Unsigned HarmonyOS HAP structure/provenance shared by release and Quark mirror."""
import hashlib
import json
from pathlib import Path, PurePosixPath
import zipfile

CHECKS = {'release-arm64', 'system-vpn', 'version', 'notices', 'unsigned-package'}


def packages(version):
    return [f'fncpn-{version}-harmonyos-arm64-release-unsigned.hap'] if tuple(map(int, version.split('.'))) >= (1, 1, 7) else []


def verify_hap(path, version, build_number, *, unsigned=True):
    with zipfile.ZipFile(path) as archive:
        if unsigned:
            with open(path, "rb") as source:
                source.seek(max(0, archive.start_dir - 20))
                if source.read(16) in (b"<hap sign block>", b"HAP Sig Block 42"):
                    raise ValueError("Public HarmonyOS HAP must be unsigned")
        names = archive.namelist()
        if len(names) != len(set(names)):
            raise ValueError('Duplicate HAP members')
        for name in names:
            parts = PurePosixPath(name)
            if parts.is_absolute() or '..' in parts.parts or '\\' in name:
                raise ValueError('Unsafe HAP member')
        contents = json.loads(archive.read('module.json'))
        app, module = contents['app'], contents['module']
        if (app.get('bundleName') != 'cn.rectcircle.fncpn' or app.get('versionName') != version or
                app.get('versionCode') != build_number or app.get('debug') is not False or
                app.get('buildMode') != 'release' or app.get('minAPIVersion') != 60000020 or
                sorted(module.get('deviceTypes', [])) != ['2in1', 'phone', 'tablet']):
            raise ValueError('HAP must match the release version, build, minimum API and supported devices')
        if not any(e.get('name') == 'VpnAbility' and e.get('type') == 'vpn' and e.get('exported') is False
                   for e in module.get('extensionAbilities', [])):
            raise ValueError('Missing private system VPN extension')
        if 'ets/modules.abc' not in names:
            raise ValueError('Missing ArkTS bytecode')
        for library in ['libfncpn.so', 'libfncpnbridge.so']:
            data = archive.read('libs/arm64-v8a/' + library)
            if len(data) < 20 or data[:6] != b'\x7fELF\x02\x01' or int.from_bytes(data[18:20], 'little') != 183:
                raise ValueError('Expected ARM64 ELF native library')
        if any(n.startswith('libs/') and n.endswith('.so') and not n.startswith('libs/arm64-v8a/') for n in names):
            raise ValueError('Unexpected HAP native architecture')
        for notice in ['THIRD_PARTY_NOTICES.md', 'COMMUNITY_GO_LICENSE']:
            if not archive.read('resources/rawfile/' + notice).strip():
                raise ValueError('Empty HAP license notice')


def validate_build(build, version, commit):
    if (build.get('productVersion') != version or build.get('commit') != commit or build.get('dirty') is not False or
            build.get('signed') is not False or build.get('buildMode') != 'release' or build.get('product') != 'default' or
            build.get('applicationTunnel') is not False or build.get('abi') != 'arm64-v8a' or
            type(build.get('buildNumber')) is not int or build['buildNumber'] <= 0 or
            set(build.get('checks', [])) != CHECKS):
        raise ValueError('HarmonyOS candidate requires clean same-commit unsigned system-VPN release evidence')


def candidates(root, version, commit):
    artifacts, build = [], {}
    for name in packages(version):
        path = Path(root)/'harmonyos'/name
        build = json.loads((path.parent/'build-manifest-release.json').read_text())
        validate_build(build, version, commit)
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        if build.get('filename') != name or build.get('sha256') != digest:
            raise ValueError('HarmonyOS candidate filename/hash mismatch')
        verify_hap(path, version, build['buildNumber'])
        artifacts.append({'file': name, 'path': 'harmonyos/' + name, 'sha256': digest})
    return artifacts, build


def validate_inventory(manifest, version):
    expected = set(packages(version))
    if not expected:
        return
    actual = [a for a in manifest['artifacts'] if a['file'].endswith('.hap')]
    if len(actual) != len(expected) or {a['file'] for a in actual} != expected:
        raise ValueError('Release inventory is missing the unsigned HarmonyOS HAP')
    build = manifest.get('build', {}).get('harmonyos', {})
    validate_build(build, version, manifest.get('commit'))
    if build.get('filename') != actual[0]['file'] or build.get('sha256') != actual[0]['sha256']:
        raise ValueError('HarmonyOS inventory hash differs from build evidence')
