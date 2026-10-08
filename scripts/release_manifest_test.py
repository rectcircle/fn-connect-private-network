#!/usr/bin/env python3
"""Exercise release inventories without building or replacing real packages."""
import json, os, pathlib, subprocess, sys, tempfile, unittest
SCRIPT = pathlib.Path(__file__).with_name('release-manifest.py')
class ReleaseInventoryTest(unittest.TestCase):
    def test_inventory_is_exact_and_release_cannot_be_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            names = ['FnCPN-1.0.0-arm64-unsigned.pkg', 'fncpn-1.0.0-arm.fpk', 'fncpn-1.0.0-x86.fpk']
            for name in names:
                (root / name).write_bytes(b'package')
            (root / 'fncpn-0.1.0-arm.fpk').write_bytes(b'historical')
            command = [sys.executable, str(SCRIPT), str(root), '1.0.0', 'commit', 'arm64']
            environment = dict(os.environ, RELEASE='1')
            subprocess.run(command, env=environment, check=True, capture_output=True)
            before = (root / 'release-1.0.0.json').read_bytes()
            record = json.loads(before)
            self.assertEqual([a['file'] for a in record['artifacts']], names)
            self.assertEqual(record['commit'], 'commit')
            self.assertNotEqual(subprocess.run(command, env=environment, capture_output=True).returncode, 0)
            self.assertEqual((root / 'release-1.0.0.json').read_bytes(), before)
if __name__ == '__main__':
    unittest.main()
