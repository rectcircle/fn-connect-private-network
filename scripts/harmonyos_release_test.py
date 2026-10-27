"""Offline tests for HAP publication/mirroring, without uploading or signing."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import shutil
import zipfile
from harmonyos_release import CHECKS, candidates, packages, validate_inventory, verify_hap


def fixture(root, version='1.1.7', commit='a'*40):
    folder = Path(root)/'harmonyos'
    folder.mkdir()
    name = packages(version)[0]
    path = folder/name
    app = {'bundleName': 'cn.rectcircle.fncpn', 'versionName': version, 'versionCode': 2,
           'debug': False, 'buildMode': 'release', 'minAPIVersion': 60000020}
    module = {'deviceTypes': ['phone', 'tablet', '2in1'],
              'extensionAbilities': [{'name': 'VpnAbility', 'type': 'vpn', 'exported': False}]}
    elf = bytearray(64)
    elf[:6] = b'\x7fELF\x02\x01'
    elf[18:20] = (183).to_bytes(2, 'little')
    with zipfile.ZipFile(path, 'w') as archive:
        archive.writestr('module.json', json.dumps({'app': app, 'module': module}))
        archive.writestr('ets/modules.abc', b'bytecode')
        for name in ['libfncpn.so', 'libfncpnbridge.so']:
            archive.writestr('libs/arm64-v8a/'+name, elf)
        for name in ['THIRD_PARTY_NOTICES.md', 'COMMUNITY_GO_LICENSE']:
            archive.writestr('resources/rawfile/'+name, 'license')
    build = {'productVersion': version, 'commit': commit, 'dirty': False, 'signed': False,
             'buildMode': 'release', 'product': 'default', 'applicationTunnel': False,
             'abi': 'arm64-v8a', 'buildNumber': 2, 'checks': sorted(CHECKS),
             'filename': path.name, 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}
    (folder/'build-manifest-release.json').write_text(json.dumps(build))
    return path, build


class HarmonyReleaseTest(unittest.TestCase):
    def test_version_boundary_and_clean_candidate(self):
        self.assertEqual(packages('1.1.6'), [])
        with tempfile.TemporaryDirectory() as root:
            path, build = fixture(root)
            rows, actual = candidates(root, '1.1.7', 'a'*40)
            self.assertEqual(actual, build)
            self.assertEqual(rows[0]['file'], path.name)
            manifest = {'commit': 'a'*40, 'artifacts': rows, 'build': {'harmonyos': build}}
            validate_inventory(manifest, '1.1.7')
            for key, bad in [('dirty', True), ('signed', True), ('applicationTunnel', True),
                             ('product', 'simulator'), ('commit', 'b'*40), ('buildMode', 'debug')]:
                broken = copy.deepcopy(manifest)
                broken['build']['harmonyos'][key] = bad
                with self.subTest(key=key), self.assertRaises(ValueError):
                    validate_inventory(broken, '1.1.7')
            broken = copy.deepcopy(manifest)
            broken['artifacts'] = []
            with self.assertRaises(ValueError): validate_inventory(broken, '1.1.7')
            path.write_bytes(path.read_bytes() + b'changed')
            with self.assertRaises(ValueError): candidates(root, '1.1.7', 'a'*40)

    def test_signed_hap_rejected_even_with_unsigned_manifest(self):
        import struct
        with tempfile.TemporaryDirectory() as root:
            path, _ = fixture(root)
            data = path.read_bytes()
            with zipfile.ZipFile(path) as archive:
                offset = archive.start_dir
            block = b'<hap sign block>' + b'\x00' * 4
            edited = bytearray(data[:offset] + block + data[offset:])
            eocd = edited.rfind(b'PK\x05\x06')
            struct.pack_into('<I', edited, eocd + 16, offset + len(block))
            path.write_bytes(edited)
            with self.assertRaisesRegex(ValueError, 'must be unsigned'):
                verify_hap(path, '1.1.7', 2)

    def test_debug_tv_wrong_version_license_and_abi_rejected(self):
        with tempfile.TemporaryDirectory() as root:
            path, _ = fixture(root)
            with self.assertRaises(ValueError): verify_hap(path, '1.1.8', 2)
            with zipfile.ZipFile(path) as archive:
                members = {n: archive.read(n) for n in archive.namelist()}
            for change in ['debug', 'tv', 'vpn', 'license', 'abi']:
                edited = dict(members)
                module = json.loads(edited['module.json'])
                if change == 'debug': module['app']['debug'] = True
                if change == 'tv': module['module']['deviceTypes'].append('tv')
                if change == 'vpn': module['module']['extensionAbilities'] = []
                if change == 'license': del edited['resources/rawfile/COMMUNITY_GO_LICENSE']
                if change == 'abi': edited['libs/arm64-v8a/libfncpn.so'] = b'not an ELF'
                edited['module.json'] = json.dumps(module).encode()
                with zipfile.ZipFile(path, 'w') as archive:
                    for name, data in edited.items(): archive.writestr(name, data)
                with self.subTest(change=change), self.assertRaises((ValueError, KeyError)):
                    verify_hap(path, '1.1.7', 2)

    def test_quark_accepts_flattened_original_hap_and_digest(self):
        spec = importlib.util.spec_from_file_location('quark', Path(__file__).with_name('quark-release.py'))
        quark = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(quark)
        with tempfile.TemporaryDirectory() as root:
            path, build = fixture(root)
            downloaded = Path(root)/'downloaded'
            downloaded.mkdir()
            shutil.copyfile(path, downloaded/path.name)
            rows = [{'file': path.name, 'sha256': build['sha256']}]
            for system, suffix in [('macos', '-unsigned.pkg'), ('windows', '-unsigned.msi'), ('fnos', '.fpk')]:
                for arch in ['arm64', 'x64']:
                    file = downloaded/f'fncpn-1.1.7-{system}-{arch}{suffix}'
                    file.write_bytes(b'existing desktop fixture')
                    rows.append({'file': file.name, 'sha256': hashlib.sha256(file.read_bytes()).hexdigest()})
            manifest = {'version': '1.1.7', 'commit': 'a'*40, 'channel': 'stable',
                        'artifacts': rows, 'build': {'harmonyos': build}}
            (downloaded/'release-1.1.7.json').write_text(json.dumps(manifest))
            (downloaded/'SHA256SUMS-1.1.7').write_text(''.join(f"{a['sha256']}  {a['file']}\n" for a in rows))
            release = {'assets': [{'name': f.name, 'size': f.stat().st_size,
                                   'digest': 'sha256:'+hashlib.sha256(f.read_bytes()).hexdigest()} for f in downloaded.iterdir()]}
            # Other platform parsers have their own real-byte tests. Keep the
            # mirror's inventory, HAP, size, hash and flattened layout checks real.
            with patch.object(quark, 'release_packages', return_value=[]), patch.object(quark, 'android_packages', return_value=[]), patch.object(quark, 'ios_packages', return_value=[]), patch.object(quark, 'validate_android_inventory'), patch.object(quark, 'validate_ios_inventory'):
                sources = quark.inventory(downloaded, 'v1.1.7', release, 'a'*40)
                self.assertEqual(sources[path.name].read_bytes(), path.read_bytes())

    def test_quark_checks_hap_structure_after_inventory(self):
        spec = importlib.util.spec_from_file_location('quark', Path(__file__).with_name('quark-release.py'))
        quark = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(quark)
        with tempfile.TemporaryDirectory() as root:
            path, build = fixture(root)
            row = {'file': path.name, 'sha256': build['sha256']}
            # Exercise the production mirror's new early validation. A fake byte
            # archive cannot reach upload even with matching manifest hashes.
            bad = path.parent.parent/path.name
            bad.write_bytes(b'bad HAP')
            build['sha256'] = row['sha256'] = hashlib.sha256(bad.read_bytes()).hexdigest()
            manifest = {'version': '1.1.7', 'commit': 'a'*40, 'channel': 'stable',
                        'artifacts': [row], 'build': {'harmonyos': build}}
            (Path(root)/'release-1.1.7.json').write_text(json.dumps(manifest))
            with self.assertRaises(zipfile.BadZipFile):
                quark.inventory(Path(root), 'v1.1.7', {}, 'a'*40)


if __name__ == '__main__': unittest.main()
