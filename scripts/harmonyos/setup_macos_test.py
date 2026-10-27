import json
from pathlib import Path
import tempfile
import unittest
import zipfile
from unittest.mock import patch

import setup_macos as setup


class SetupTests(unittest.TestCase):
    def archive(self, root):
        archive = root / 'tools.zip'
        with zipfile.ZipFile(archive, 'w') as bundle:
            for name in ('bin/ohpm', 'bin/hvigorw', 'sdk/default/openharmony/toolchains/hdc',
                         'sdk/default/openharmony/native/llvm/bin/clang', 'tool/node/bin/node'):
                info = zipfile.ZipInfo('command-line-tools/' + name)
                info.external_attr = 0o100755 << 16
                bundle.writestr(info, '#!/bin/sh\nexit 0\n')
            bundle.writestr('command-line-tools/sdk/native/build/ohos.toolchain.cmake', '')
        return archive

    def test_verified_install_reuse_and_refuse_overwrite(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = self.archive(root)
            checksum = setup.digest(archive)
            args = (archive, checksum, '6.0.1.251', root / 'install', root / 'cache')
            target = setup.install(*args)
            self.assertEqual(setup.install(*args), target)
            self.assertEqual(len(setup.locate(target)), 5)
            marker = target / '.harmonyos-install.json'
            marker.write_text('{}')
            with self.assertRaisesRegex(ValueError, 'refusing to overwrite'):
                setup.install(*args)

    def test_checksum_failure_does_not_install(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with self.assertRaisesRegex(ValueError, 'SHA256 mismatch'):
                setup.install(self.archive(root), '0' * 64, '6.0.1', root / 'install', root / 'cache')
            self.assertFalse((root / 'install').exists())

    def test_unsafe_archives(self):
        for name, mode in (('../escape', 0o100644), ('/escape', 0o100644),
                           ('tools/link', 0o120777)):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                archive = root / 'bad.zip'
                with zipfile.ZipFile(archive, 'w') as bundle:
                    info = zipfile.ZipInfo(name)
                    info.external_attr = mode << 16
                    bundle.writestr(info, 'target')
                with self.assertRaisesRegex(ValueError, 'Unsafe archive'):
                    setup.extract(archive, root / 'out')

    def test_internal_link_chain_is_preserved(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / 'links.zip'
            with zipfile.ZipFile(archive, 'w') as bundle:
                bundle.writestr('tools/clang-15', 'compiler')
                for name, target in (('clang', 'clang-15'), ('clang++', 'clang')):
                    info = zipfile.ZipInfo('tools/' + name)
                    info.external_attr = 0o120777 << 16
                    bundle.writestr(info, target)
            setup.extract(archive, root / 'out')
            self.assertTrue((root / 'out/tools/clang').is_symlink())
            self.assertEqual((root / 'out/tools/clang++').read_text(), 'compiler')

    def test_link_parent_is_rejected_before_writes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / 'links.zip'
            with zipfile.ZipFile(archive, 'w') as bundle:
                info = zipfile.ZipInfo('alias')
                info.external_attr = 0o120777 << 16
                bundle.writestr(info, 'real')
                bundle.writestr('real/', '')
                bundle.writestr('alias/file', 'bad')
            with self.assertRaisesRegex(ValueError, 'ancestor'):
                setup.extract(archive, root / 'out')
            self.assertFalse((root / 'out').exists())

    def test_link_cycle_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / 'links.zip'
            with zipfile.ZipFile(archive, 'w') as bundle:
                for name, target in (('a', 'b'), ('b', 'a')):
                    info = zipfile.ZipInfo(name)
                    info.external_attr = 0o120777 << 16
                    bundle.writestr(info, target)
            with self.assertRaisesRegex(ValueError, 'cycle'):
                setup.extract(archive, root / 'out')

    def test_incomplete_install_is_not_committed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / 'empty.zip'
            with zipfile.ZipFile(archive, 'w') as bundle:
                bundle.writestr('readme', 'incomplete')
            with self.assertRaises(ValueError):
                setup.install(archive, setup.digest(archive), '6.0.1', root / 'install', root / 'cache')
            self.assertFalse((root / 'install/6.0.1').exists())

    def test_cross_environment_does_not_leak(self):
        with patch.dict('os.environ', {'GOOS': 'android', 'GOROOT': '/wrong', 'CC': '/wrong'}):
            env = setup.clean_env()
        self.assertNotIn('GOOS', env)
        self.assertNotIn('GOROOT', env)
        self.assertNotIn('CC', env)
        self.assertEqual(env['GOTOOLCHAIN'], 'local')


if __name__ == '__main__':
    unittest.main()
