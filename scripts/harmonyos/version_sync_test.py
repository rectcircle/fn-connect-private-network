"""Check version changes cannot leave HarmonyOS labels/build numbers stale."""
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
import zipfile
import package_app

ROOT = Path(__file__).resolve().parents[2]

class HarmonyVersionSyncTest(unittest.TestCase):
    def test_product_and_independent_build_number_are_checked_together(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in ['scripts/sync-version.py', 'internal/version/VERSION',
                         'packaging/fnos/manifest', 'packaging/macos/Info.plist',
                         'apps/harmonyos/AppScope/app.json5', 'apps/harmonyos/BUILD_NUMBER',
                         'apps/harmonyos/entry/src/main/resources/base/element/string.json',
                         'apps/harmonyos/entry/src/main/resources/en_US/element/string.json']:
                target = root/name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT/name, target)
            (root/'internal/version/VERSION').write_text('1.1.7\n')
            (root/'apps/harmonyos/BUILD_NUMBER').write_text('17\n')
            def run(*flags):
                return subprocess.run([sys.executable, root/'scripts/sync-version.py', *flags], capture_output=True, text=True)
            self.assertNotEqual(run('--check').returncode, 0)
            self.assertEqual(run().returncode, 0)
            self.assertEqual(run('--check').returncode, 0)
            app = json.loads((root/'apps/harmonyos/AppScope/app.json5').read_text())['app']
            self.assertEqual((app['versionName'], app['versionCode']), ('1.1.7', 17))
            for language in ['base', 'en_US']:
                entries = json.loads((root/f'apps/harmonyos/entry/src/main/resources/{language}/element/string.json').read_text())['string']
                self.assertEqual(next(row['value'] for row in entries if row['name']=='product_version'), '1.1.7')
            (root/'apps/harmonyos/BUILD_NUMBER').write_text('0\n')
            self.assertNotEqual(run().returncode, 0)

class HarmonyPackageVersionTest(unittest.TestCase):
    def test_stale_product_or_build_number_cannot_be_relabelled(self):
        for version, number in [('1.1.5', 2), ('1.1.6', 1)]:
            with self.subTest(version=version, number=number), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                for relative, content in [('internal/version/VERSION', '1.1.6'), ('apps/harmonyos/BUILD_NUMBER', '2'),
                                          ('apps/harmonyos/toolchains.json', '{}')]:
                    target = root/relative
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_text(content)
                hap = root/'apps/harmonyos/entry/build/default/outputs/default/entry-default-unsigned.hap'
                hap.parent.mkdir(parents=True)
                with zipfile.ZipFile(hap, 'w') as archive:
                    archive.writestr('module.json', json.dumps({'app': {'versionName': version, 'versionCode': number}}))
                with mock.patch.object(package_app, 'ROOT', root), mock.patch.object(sys, 'argv', ['package_app.py', '--release']):
                    with self.assertRaisesRegex(SystemExit, 'Rebuild before packaging'):
                        package_app.main()
                self.assertFalse((root/'dist').exists(), 'stale HAP must not create a candidate')

if __name__ == '__main__':
    unittest.main()
