import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import setup_linux
from setup_macos import cli_pin


class LinuxSetupTests(unittest.TestCase):
    def test_host_pin_and_architecture(self):
        with patch('platform.system', return_value='Linux'), patch('platform.machine', return_value='x86_64'):
            self.assertEqual(cli_pin()['version'], '26.0.0.821')
        with patch('platform.system', return_value='Darwin'):
            self.assertEqual(cli_pin()['version'], '26.0.0.851')
        with patch('platform.system', return_value='Linux'), patch('platform.machine', return_value='aarch64'):
            with self.assertRaises(ValueError): cli_pin()

    def test_corruption_does_not_install(self):
        with tempfile.TemporaryDirectory() as folder:
            target = Path(folder)/'installed'
            pin = {'version':'26.0.0.821', 'sha256':'0'*64, 'releaseBase':'https://example.invalid',
                   'archive':'cli.tar.gz', 'parts':['aa','ab']}
            def fetch(url, path): path.write_bytes(b'bad')
            with self.assertRaisesRegex(ValueError, 'SHA256 mismatch'):
                setup_linux.install(pin, target, fetch)
            self.assertFalse(target.exists())

    def test_split_archive_and_cached_identity(self):
        with tempfile.TemporaryDirectory() as folder:
            folder = Path(folder)
            archive = folder/'cli.tar.gz'
            with tarfile.open(archive, 'w:gz') as bundle:
                for name in ['bin/ohpm', 'sdk/sdk-pkg.json']:
                    item = tarfile.TarInfo('command-line-tools/'+name)
                    item.size = 2
                    bundle.addfile(item, io.BytesIO(b'{}'))
            data = archive.read_bytes()
            pin = {'version':'26.0.0.821', 'sha256':hashlib.sha256(data).hexdigest(),
                   'releaseBase':'https://example.invalid', 'archive':'cli.tar.gz', 'parts':['aa','ab']}
            def fetch(url, path): path.write_bytes(data[:len(data)//2] if url.endswith('.aa') else data[len(data)//2:])
            target = folder/'installed'
            with patch.object(setup_linux, 'locate'):
                setup_linux.install(pin, target, fetch)
                setup_linux.install(pin, target, lambda *_: self.fail('cache downloaded again'))
                marker = target/'.harmonyos-install.json'
                value = json.loads(marker.read_text());value['sha256'] = 'wrong';marker.write_text(json.dumps(value))
                with self.assertRaises(ValueError): setup_linux.install(pin, target, fetch)

    def test_archive_cannot_escape(self):
        with tempfile.TemporaryDirectory() as folder:
            folder = Path(folder)
            for name, link in [('../escaped', None), ('link', '../../escaped')]:
                archive = folder/'bad.tar.gz'
                with tarfile.open(archive, 'w:gz') as bundle:
                    item = tarfile.TarInfo(name)
                    if link:
                        item.type = tarfile.SYMTYPE; item.linkname = link
                    bundle.addfile(item)
                with self.assertRaises(tarfile.FilterError): setup_linux.extract(archive, folder/'out')


if __name__ == '__main__': unittest.main()
