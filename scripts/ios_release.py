"""Unsigned iOS release structure and provenance, shared by build and publication."""
import hashlib
import json
from pathlib import Path, PurePosixPath
import plistlib
import struct
import zipfile

CHECKS = {'device-arm64', 'app-and-packet-tunnel', 'unsigned', 'version', 'notices'}


def packages(version):
    return [f'fncpn-{version}-ios-arm64-unsigned.ipa'] if tuple(map(int, version.split('.'))) >= (1, 1, 5) else []


def verify_binary(data):
    if len(data) < 32:
        raise ValueError('Truncated Mach-O')
    magic, cpu, _, _, count, size, _, _ = struct.unpack_from('<8I', data)
    if magic != 0xfeedfacf or cpu != 0x100000c or 32 + size > len(data):
        raise ValueError('Expected ARM64 Mach-O')
    offset = 32
    device = False
    for _ in range(count):
        if offset + 8 > 32 + size:
            raise ValueError('Truncated Mach-O command')
        command, length = struct.unpack_from('<2I', data, offset)
        if length < 8 or offset + length > 32 + size:
            raise ValueError('Invalid Mach-O command')
        if command == 0x1d:
            raise ValueError('IPA must not contain a code signature')
        if command == 0x32 and length >= 24:
            device = struct.unpack_from('<I', data, offset + 8)[0] == 2
        if command == 0x25:  # LC_VERSION_MIN_IPHONEOS (older linker)
            device = True
        offset += length
    if not device:
        raise ValueError('Expected iPhoneOS device binary, not a simulator')


def verify_ipa(path, version, build_number):
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        if len(names) != len(set(names)):
            raise ValueError('Duplicate IPA members')
        for name in names:
            relative = PurePosixPath(name)
            if relative.is_absolute() or '..' in relative.parts or '\\' in name:
                raise ValueError('Unsafe IPA member')
            if '_CodeSignature' in relative.parts or relative.name == 'embedded.mobileprovision':
                raise ValueError('Unsigned IPA contains signing material')
        roots = {PurePosixPath(n).parts[1] for n in names if n.startswith('Payload/') and len(PurePosixPath(n).parts) > 2}
        if roots != {'FnCPN.app'}:
            raise ValueError('Expected exactly Payload/FnCPN.app')
        for root, bundle in [('Payload/FnCPN.app/', 'cn.rectcircle.fncpn'),
                             ('Payload/FnCPN.app/PlugIns/PacketTunnel.appex/', 'cn.rectcircle.fncpn.PacketTunnel')]:
            info = plistlib.loads(archive.read(root + 'Info.plist'))
            if (info.get('CFBundleIdentifier') != bundle or info.get('CFBundleShortVersionString') != version or
                    info.get('CFBundleVersion') != str(build_number) or info.get('MinimumOSVersion') != '15.0'):
                raise ValueError('IPA bundle version, identifier or minimum OS mismatch')
            verify_binary(archive.read(root + info['CFBundleExecutable']))
        extension = plistlib.loads(archive.read('Payload/FnCPN.app/PlugIns/PacketTunnel.appex/Info.plist'))
        if extension.get('NSExtension', {}).get('NSExtensionPointIdentifier') != 'com.apple.networkextension.packet-tunnel':
            raise ValueError('Missing Packet Tunnel extension declaration')
        if 'Payload/FnCPN.app/THIRD_PARTY_NOTICES.md' not in names:
            raise ValueError('Missing third-party notices')


def candidates(root, version, commit):
    artifacts = []
    build = {}
    for name in packages(version):
        path = Path(root) / 'ios' / name
        build = json.loads(path.with_name('build-manifest.json').read_text())
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        if (build.get('productVersion') != version or build.get('commit') != commit or
                build.get('workingTreeDirty') is not False or build.get('configuration') != 'Release' or
                build.get('signed') is not False or build.get('architecture') != 'arm64' or
                not isinstance(build.get('buildNumber'), int) or build['buildNumber'] <= 0 or
                set(build.get('checks', [])) != CHECKS or
                build.get('artifacts') != [{'file': name, 'sha256': digest}]):
            raise ValueError('iOS candidate must match clean Release source, unsigned device build and hash')
        verify_ipa(path, version, build['buildNumber'])
        artifacts.append({'file': name, 'path': 'ios/' + name, 'sha256': digest})
    return artifacts, build


def validate_inventory(manifest, version):
    expected = set(packages(version))
    if not expected:
        return
    actual = {a['file'] for a in manifest['artifacts'] if a['file'].endswith('.ipa')}
    build = manifest.get('build', {}).get('ios', {})
    if (actual != expected or build.get('signed') is not False or build.get('configuration') != 'Release' or
            build.get('architecture') != 'arm64' or build.get('productVersion') != version or
            build.get('commit') != manifest.get('commit') or build.get('workingTreeDirty') is not False or
            not isinstance(build.get('buildNumber'), int) or build['buildNumber'] <= 0 or
            set(build.get('checks', [])) != CHECKS):
        raise ValueError('Release inventory is missing verified unsigned iOS candidate')
