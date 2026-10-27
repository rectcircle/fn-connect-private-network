"""Sign a release candidate with the configured macOS runner's login Keychain."""
import os
from pathlib import Path
import pty
import select
import subprocess
import time
from setup_macos import ROOT


def main():
    base = Path.home()/'.harmonyos/signing'
    env = os.environ.copy()
    for name, value in {'HARMONYOS_KEYSTORE': str(base/'release.p12'), 'HARMONYOS_KEY_ALIAS': 'release',
                        'HARMONYOS_APP_CERT': str(base/'release.cer'),
                        'HARMONYOS_PROFILE': str(base/'profiles/fncpn-release.p7b')}.items():
        env.setdefault(name, value)
    password = subprocess.check_output(['security', 'find-generic-password', '-s',
        env.get('HARMONYOS_SIGNING_KEYCHAIN_SERVICE', 'harmonyos-signing-release'),
        '-a', env['HARMONYOS_KEY_ALIAS'], '-w']).rstrip(b'\r\n')
    if not password:
        raise SystemExit('Signing password not found in the runner Keychain')
    master, slave = pty.openpty()
    process = subprocess.Popen(['bash', 'scripts/package-harmonyos.sh', '--release', '--sign'],
        cwd=ROOT, env=env, stdin=slave, stdout=slave, stderr=slave)
    os.close(slave)
    buffer = b''
    deadline = time.monotonic() + 120
    try:
        while time.monotonic() < deadline:
            if select.select([master], [], [], 1)[0]:
                try:
                    chunk = os.read(master, 65536)
                except OSError:
                    break
                if not chunk:
                    break
                buffer += chunk
                if b'please input' in buffer.lower() and b':' in buffer.split(b'please input')[-1]:
                    os.write(master, password + b'\n')
                    buffer = b''
            if process.poll() is not None:
                break
    finally:
        os.close(master)
        try:
            result = process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            result = process.wait()
    # Never echo the PTY: terminal password echo or certificate details can be present.
    if result:
        raise SystemExit('HAP signing/verification failed; use the interactive signing command to diagnose')
    print('Signed release HAP and official signature verification completed')


if __name__ == '__main__':
    main()
