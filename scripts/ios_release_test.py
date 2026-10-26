"""Reject signed/simulator/tampered device candidates before upload or publication."""
import copy
import hashlib
import json
from pathlib import Path
import plistlib
import struct
import tempfile
import unittest
import zipfile
from ios_release import CHECKS, candidates, packages, validate_inventory, verify_ipa


def binary(platform=2, signature=False):
    commands = struct.pack('<6I', 0x32, 24, platform, 0xf0000, 0x1b0100, 0)
    if signature:
        commands += struct.pack('<4I', 0x1d, 16, 0, 0)
    return struct.pack('<8I', 0xfeedfacf, 0x100000c, 0, 2, 2 if signature else 1, len(commands), 0, 0) + commands


def fixture(path, platform=2, signature=False, profile=False):
    with zipfile.ZipFile(path, 'w') as archive:
        for suffix, executable in [('', 'FnCPN'), ('/PlugIns/PacketTunnel.appex', 'PacketTunnel')]:
            root = 'Payload/FnCPN.app' + suffix
            info = {'CFBundleIdentifier': 'cn.rectcircle.fncpn' + ('.PacketTunnel' if suffix else ''),
                    'CFBundleShortVersionString': '1.1.5', 'CFBundleVersion': '2',
                    'CFBundleExecutable': executable, 'MinimumOSVersion': '15.0'}
            if suffix:
                info['NSExtension'] = {'NSExtensionPointIdentifier': 'com.apple.networkextension.packet-tunnel'}
            archive.writestr(root + '/Info.plist', plistlib.dumps(info))
            archive.writestr(root + '/' + executable, binary(platform, signature))
        archive.writestr('Payload/FnCPN.app/THIRD_PARTY_NOTICES.md', 'licenses')
        if profile:
            archive.writestr('Payload/FnCPN.app/embedded.mobileprovision', 'secret')


class ReleaseTests(unittest.TestCase):
    def test_structure_rejects_signed_simulator_and_profiles(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'candidate.ipa'
            fixture(path); verify_ipa(path, '1.1.5', 2)
            for options in [{'platform': 7}, {'signature': True}, {'profile': True}]:
                fixture(path, **options)
                with self.assertRaises(ValueError): verify_ipa(path, '1.1.5', 2)
            fixture(path)
            with self.assertRaises(ValueError): verify_ipa(path, '1.1.6', 2)

    def test_provenance_and_inventory(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); (root / 'ios').mkdir()
            name = packages('1.1.5')[0]; path = root / 'ios' / name; fixture(path)
            build = {'productVersion': '1.1.5', 'commit': 'a'*40, 'workingTreeDirty': False,
                     'configuration': 'Release', 'signed': False, 'architecture': 'arm64',
                     'buildNumber': 2, 'checks': sorted(CHECKS),
                     'artifacts': [{'file': name, 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}]}
            manifest_path = root / 'ios/build-manifest.json'
            manifest_path.write_text(json.dumps(build))
            artifacts, evidence = candidates(root, '1.1.5', 'a'*40)
            manifest = {'commit': 'a'*40, 'artifacts': artifacts, 'build': {'ios': evidence}}
            validate_inventory(manifest, '1.1.5')
            for key, value in [('commit', 'b'*40), ('workingTreeDirty', True), ('signed', True), ('configuration', 'Debug')]:
                bad = copy.deepcopy(build); bad[key] = value; manifest_path.write_text(json.dumps(bad))
                with self.assertRaises(ValueError): candidates(root, '1.1.5', 'a'*40)
                bad_inventory = copy.deepcopy(manifest); bad_inventory['build']['ios'][key] = value
                with self.assertRaises(ValueError): validate_inventory(bad_inventory, '1.1.5')
            manifest_path.write_text(json.dumps(build)); path.write_bytes(b'tampered')
            with self.assertRaises(ValueError): candidates(root, '1.1.5', 'a'*40)
            manifest['artifacts'] = []
            with self.assertRaises(ValueError): validate_inventory(manifest, '1.1.5')

    def test_historical_releases_do_not_require_ipa(self):
        self.assertEqual(packages('1.1.4'), [])
        validate_inventory({'artifacts': []}, '1.1.4')


if __name__ == '__main__': unittest.main()
