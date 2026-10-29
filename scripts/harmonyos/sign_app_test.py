"""Guard compiled payload integrity when wrapping a signed HAP in an APP."""
from pathlib import Path
import tempfile
import unittest
import zipfile
from sign_app import verify_payload


class PayloadTest(unittest.TestCase):
    def test_only_signer_metadata_may_be_added(self):
        with tempfile.TemporaryDirectory() as directory:
            source, hap, app = [Path(directory)/name for name in ['unsigned.hap', 'signed.hap', 'signed.app']]
            with zipfile.ZipFile(source, 'w') as z:
                z.writestr('ets/modules.abc', b'compiled')
            for extra, payload, valid in [('.pages.info', b'compiled', True),
                                          ('unexpected', b'compiled', False),
                                          ('.pages.info', b'changed', False)]:
                with self.subTest(extra=extra, payload=payload):
                    with zipfile.ZipFile(hap, 'w') as z:
                        z.writestr('ets/modules.abc', payload)
                        z.writestr(extra, b'metadata')
                    with zipfile.ZipFile(app, 'w') as z:
                        z.writestr('entry-default.hap', hap.read_bytes())
                    if valid:
                        verify_payload(source, hap, app)
                    else:
                        with self.assertRaises(ValueError): verify_payload(source, hap, app)
            with zipfile.ZipFile(hap, 'w') as z:
                z.writestr('ets/modules.abc', b'compiled')
            with zipfile.ZipFile(app, 'w') as z:
                z.writestr('entry-default.hap', b'rewritten by packer')
            with self.assertRaisesRegex(ValueError, 'inner HAP'):
                verify_payload(source, hap, app)


if __name__ == '__main__':
    unittest.main()
