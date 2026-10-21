#!/usr/bin/env python3
"""Fail-closed signing regression tests; optional real signed RPM fixture."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import rpm_signing


class SigningTest(unittest.TestCase):
    def result(self, text, code=0):
        return subprocess.CompletedProcess([], code, stdout=text)

    def test_digest_only_is_not_a_signature(self):
        for text in ['digests OK', 'Header SHA256 digest: OK\nPayload SHA256 digest: OK', 'Header RSA/SHA256 Signature, key ID 1234: NOKEY', 'RSA Signature: NOT OK']:
            with self.subTest(text=text), self.assertRaises(ValueError):
                rpm_signing.require_valid_signature(self.result(text))

    def test_valid_signature_still_rejects_corrupt_payload(self):
        good='Header V4 RSA/SHA256 Signature, key ID 1234: OK\nPayload SHA256 digest: OK'
        rpm_signing.require_valid_signature(self.result(good))
        with self.assertRaises(ValueError):
            rpm_signing.require_valid_signature(self.result(good+'\nPayload digest: BAD', 1))

    def test_missing_secrets_never_signs(self):
        with patch.dict(os.environ, {}, clear=True), patch('rpm_signing.subprocess.run') as run:
            with self.assertRaisesRegex(ValueError, 'secrets are required'):
                rpm_signing.sign(['package.rpm'])
            run.assert_not_called()

    @unittest.skipUnless(os.environ.get('FNCPN_TEST_SIGNED_RPM'), 'real RPM fixture is supplied by signing CI')
    def test_real_signed_unsigned_corrupt_and_untrusted_packages(self):
        package=Path(os.environ['FNCPN_TEST_SIGNED_RPM'])
        self.assertEqual(rpm_signing.verify([package]), rpm_signing.signing_identity())
        with tempfile.TemporaryDirectory(prefix='fncpn-sign-test-') as directory:
            root=Path(directory)
            stripped=root/'unsigned.rpm';shutil.copyfile(package,stripped)
            subprocess.run(['rpmsign','--delsign',str(stripped)],check=True,capture_output=True)
            with self.assertRaises(ValueError):rpm_signing.verify([stripped])
            corrupt=root/'corrupt.rpm';data=bytearray(package.read_bytes());data[-1]^=1;corrupt.write_bytes(data)
            with self.assertRaises(ValueError):rpm_signing.verify([corrupt])
            home=root/'gpg';home.mkdir(mode=0o700)
            env={**os.environ,'GNUPGHOME':str(home)}
            try:
                subprocess.run(['gpg','--batch','--passphrase','','--quick-generate-key','Test-only untrusted RPM key','rsa2048','sign','1d'],env=env,check=True,capture_output=True)
                public=root/'other.asc'
                public.write_bytes(subprocess.check_output(['gpg','--armor','--export'],env=env))
                with patch.object(rpm_signing,'KEY',public), self.assertRaises(ValueError):
                    rpm_signing.verify([package])
            finally:
                subprocess.run(['gpgconf','--homedir',str(home),'--kill','all'],env=env,check=False,capture_output=True)


if __name__=='__main__':unittest.main()
