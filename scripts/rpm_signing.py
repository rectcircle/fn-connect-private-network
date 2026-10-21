#!/usr/bin/env python3
"""Sign RPMs using ephemeral CI keys; verify against only FnCPN's public key."""
import argparse
import hashlib
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
KEY = ROOT / 'packaging/linux/keys/RPM-GPG-KEY-fncpn'
FINGERPRINT = ROOT / 'packaging/linux/keys/fingerprint'


def signing_identity():
    fingerprint = FINGERPRINT.read_text().strip()
    if not re.fullmatch(r'[A-F0-9]{40}', fingerprint):
        raise ValueError('Expected an OpenPGP v4 signing fingerprint')
    return {'fingerprint': fingerprint, 'publicKeySha256': hashlib.sha256(KEY.read_bytes()).hexdigest()}


def require_valid_signature(result):
    # rpmkeys can return success for digest-only unsigned packages. Require an
    # actual successful signature, not only exit status or a payload checksum.
    if result.returncode or not re.search(r'signature[^\n]*\bOK\b', result.stdout, re.I) or re.search(r'NOT OK|NOKEY|BAD|UNSIGNED', result.stdout, re.I):
        raise ValueError('RPM signature verification failed: ' + result.stdout)


def verify(packages):
    identity = signing_identity()
    with tempfile.TemporaryDirectory(prefix='fncpn-rpm-keyring-') as temporary:
        base = ['rpmkeys', '--dbpath', temporary]
        subprocess.run(base + ['--import', str(KEY)], check=True, capture_output=True)
        for package in packages:
            result = subprocess.run(base + ['--checksig', '--verbose', str(package)], text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env={**os.environ, 'LC_ALL': 'C'})
            require_valid_signature(result)
    return identity


def sign(packages):
    private = os.environ.pop('RPM_SIGNING_PRIVATE_KEY', '')
    passphrase = os.environ.pop('RPM_SIGNING_PASSPHRASE', '')
    if not private or not passphrase:
        raise ValueError('RPM signing secrets are required for a release')
    fingerprint = signing_identity()['fingerprint']
    with tempfile.TemporaryDirectory(prefix='fncpn-rpm-sign-') as temporary:
        home = Path(temporary)
        home.chmod(0o700)
        secret = home / 'passphrase'
        secret.write_text(passphrase + '\n'); secret.chmod(0o600)
        env = {**os.environ, 'GNUPGHOME': temporary, 'LC_ALL': 'C'}
        try:
            # Secret material travels through stdin, never command-line arguments.
            subprocess.run(['gpg', '--batch', '--import'], input=private, text=True, env=env, check=True, capture_output=True)
            listing = subprocess.check_output(['gpg', '--batch', '--with-colons', '--list-secret-keys'], env=env, text=True)
            fingerprints = [line.split(':')[9] for line in listing.splitlines() if line.startswith('fpr:')]
            if fingerprints != [fingerprint]:
                raise ValueError('Signing secret does not match the committed public key')
            gpg = shutil.which('gpg')
            wrapper = home / 'gpg-sign'
            import shlex
            wrapper.write_text('#!/bin/sh\nexec ' + shlex.quote(gpg) + ' --batch --pinentry-mode loopback --passphrase-file ' + shlex.quote(str(secret)) + ' "$@"\n')
            wrapper.chmod(0o700)
            command = ['rpmsign', '--define', '_gpg_name ' + fingerprint,
                       '--define', '_openpgp_sign_id ' + fingerprint,
                       '--define', '_openpgp_sign gpg', '--define', '__gpg ' + str(wrapper),
                       '--define', '_gpg_digest_algo sha256']
            version = subprocess.check_output(['rpm', '--version'], text=True)
            if int(re.search(r'\d+', version).group()) >= 6:
                command.append('--rpmv4')
            subprocess.run(command + ['--addsign'] + list(map(str, packages)), env=env, check=True)
        finally:
            subprocess.run(['gpgconf', '--homedir', temporary, '--kill', 'all'], env=env, check=False, capture_output=True)
    return verify(packages)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=['sign', 'verify'])
    parser.add_argument('packages', nargs='+', type=Path)
    args = parser.parse_args()
    import json
    print(json.dumps((sign if args.operation == 'sign' else verify)(args.packages)))
