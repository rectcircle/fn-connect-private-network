#!/usr/bin/env python3
"""Exercise release inventories without building or replacing real packages."""
import hashlib, json, os, pathlib, subprocess, sys, tempfile, unittest
SCRIPT = pathlib.Path(__file__).with_name('release-manifest.py')
class ReleaseInventoryTest(unittest.TestCase):
    def create_windows_candidates(self, root, version='1.0.0', commit='commit', fingerprint='a' * 64):
        for architecture in ('arm64', 'x64'):
            candidate = root / 'windows' / architecture
            candidate.mkdir(parents=True)
            package = f'FnCPN-{version}-{architecture}-unsigned.msi'
            contents = f'{architecture} package'.encode()
            (candidate / package).write_bytes(contents)
            (candidate / 'build-manifest.json').write_text(json.dumps({
                'productVersion': version,
                'commit': commit,
                'workingTreeDirty': False,
                'sourceFingerprint': fingerprint,
                'architecture': architecture,
                'signed': False,
                'artifacts': [{'path': package, 'sha256': hashlib.sha256(contents).hexdigest()}],
            }))

    def test_inventory_is_exact_and_release_cannot_be_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            names = ['FnCPN-1.0.0-arm64-unsigned.pkg', 'fncpn-1.0.0-arm.fpk', 'fncpn-1.0.0-x86.fpk']
            for name in names:
                (root / name).write_bytes(b'package')
            self.create_windows_candidates(root)
            (root / 'fncpn-0.1.0-arm.fpk').write_bytes(b'historical')
            command = [sys.executable, str(SCRIPT), str(root), '1.0.0', 'commit', 'arm64']
            environment = dict(os.environ, RELEASE='1')
            empty_path = root / 'empty-bin'
            empty_path.mkdir()
            environment['PATH'] = str(empty_path)
            subprocess.run(command, env=environment, check=True, capture_output=True)
            before = (root / 'release-1.0.0.json').read_bytes()
            record = json.loads(before)
            all_names = names + ['FnCPN-1.0.0-arm64-unsigned.msi', 'FnCPN-1.0.0-x64-unsigned.msi']
            self.assertEqual([a['file'] for a in record['artifacts']], all_names)
            self.assertEqual(record['artifacts'][3]['path'], 'windows/arm64/FnCPN-1.0.0-arm64-unsigned.msi')
            self.assertEqual(record['build']['windowsArchitectures'], ['arm64', 'x64'])
            self.assertEqual(record['commit'], 'commit')
            self.assertEqual(record['build']['go'], 'unavailable')
            self.assertEqual(record['build']['xcode'], 'unavailable')
            self.assertEqual(record['build']['sdk'], 'unavailable')
            self.assertNotEqual(subprocess.run(command, env=environment, capture_output=True).returncode, 0)
            self.assertEqual((root / 'release-1.0.0.json').read_bytes(), before)

    def test_windows_candidates_must_match_and_be_clean(self):
        for mutation, message in (
            (lambda manifest: manifest.update(commit='other'), 'version and commit'),
            (lambda manifest: manifest.update(workingTreeDirty=True), 'clean, unsigned'),
            (lambda manifest: manifest.update(architecture='x64'), 'matching-architecture'),
            (lambda manifest: manifest.update(sourceFingerprint='b' * 64), 'different source fingerprints'),
            (lambda manifest: manifest['artifacts'][0].update(sha256='0' * 64), 'checksum does not match'),
        ):
            with self.subTest(message=message), tempfile.TemporaryDirectory() as directory:
                root = pathlib.Path(directory)
                for name in ('FnCPN-1.0.0-arm64-unsigned.pkg', 'fncpn-1.0.0-arm.fpk', 'fncpn-1.0.0-x86.fpk'):
                    (root / name).write_bytes(b'package')
                self.create_windows_candidates(root)
                manifest_path = root / 'windows/arm64/build-manifest.json'
                manifest = json.loads(manifest_path.read_text())
                mutation(manifest)
                manifest_path.write_text(json.dumps(manifest))
                result = subprocess.run([sys.executable, str(SCRIPT), str(root), '1.0.0', 'commit', 'arm64'], capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertFalse((root / 'release-1.0.0.json').exists())
if __name__ == '__main__':
    unittest.main()
