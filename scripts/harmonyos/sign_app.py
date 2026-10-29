"""Sign an existing unsigned release HAP and package a signed AppGallery APP."""
import argparse
import getpass
import hashlib
import json
import os
from pathlib import Path
import pty
import select
import subprocess
import sys
import tempfile
import time
import zipfile
from setup_macos import ROOT, cli_root

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from harmonyos_release import validate_inventory, verify_hap


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def inspect_source(source, manifest=None):
    with zipfile.ZipFile(source) as archive:
        app = json.loads(archive.read('module.json'))['app']
        pack = json.loads(archive.read('pack.info'))
    version, number = app['versionName'], app['versionCode']
    verify_hap(source, version, number)
    if pack['summary']['app']['version'] != {'name': version, 'code': number}:
        raise ValueError('pack.info version differs from module.json')
    modules = pack['packages']
    if len(modules) != 1 or modules[0]['name'] != 'entry-default':
        raise ValueError('Expected a single entry-default module')
    commit = None
    if manifest:
        release = json.loads(manifest.read_text())
        validate_inventory(release, version)
        artifact = next(a for a in release['artifacts'] if a['file'] == source.name)
        if artifact['sha256'] != digest(source) or release['build']['harmonyos']['buildNumber'] != number:
            raise ValueError('Release manifest does not match the input HAP')
        commit = release['commit']
    return version, number, commit


def sign(command, password):
    # Keep passwords out of argv, environment, files and console output.
    master, slave = pty.openpty()
    process = subprocess.Popen(command, stdin=slave, stdout=slave, stderr=slave)
    os.close(slave)
    buffer = b''
    try:
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            if select.select([master], [], [], 1)[0]:
                try:
                    chunk = os.read(master, 65536)
                except OSError:
                    break
                if not chunk:
                    break
                buffer = (buffer + chunk)[-65536:]
                if b'please input' in buffer.lower() and b':' in buffer.lower().split(b'please input')[-1]:
                    os.write(master, password + b'\n')
                    buffer = b''
            if process.poll() is not None:
                break
        try:
            result = process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
            raise RuntimeError('Official signer timed out') from None
        if result:
            raise RuntimeError('Official signing failed; check certificate, profile, alias and password (credential output suppressed)')
    finally:
        os.close(master)
        if process.poll() is None:
            process.kill()
            process.wait()


def verify(jar, package, temporary, native=False):
    result = subprocess.run(['java', '-jar', str(jar), 'verify-app', '-inFile', str(package),
                             '-outCertChain', str(temporary/'verify.cer'),
                             '-outProfile', str(temporary/'verify.p7b')], capture_output=True)
    if result.returncode or (native and b'trying verify codesign block' not in result.stdout + result.stderr):
        raise RuntimeError('Official signature verification failed: ' + package.name)


def verify_payload(source, hap, app):
    with zipfile.ZipFile(source) as original, zipfile.ZipFile(hap) as signed:
        if not (set(signed.namelist()) - set(original.namelist())) <= {'.pages.info'}:
            raise ValueError('Signer added unexpected payload members')
        if any(original.read(name) != signed.read(name) for name in original.namelist()):
            raise ValueError('Signing changed the compiled HAP payload')
    with zipfile.ZipFile(app) as archive:
        if archive.read('entry-default.hap') != hap.read_bytes():
            raise ValueError('APP packing changed the signed inner HAP')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('hap', type=Path, help='Existing unsigned system-VPN release HAP; never rebuilt')
    parser.add_argument('--output-dir', type=Path, help='New output directory; refuses to overwrite')
    parser.add_argument('--manifest', type=Path, help='release-<version>.json; autodetected alongside HAP')
    parser.add_argument('--password-source', choices=['keychain', 'prompt'], default='keychain')
    args = parser.parse_args()
    source = args.hap.expanduser().resolve()
    with zipfile.ZipFile(source) as archive:
        version = json.loads(archive.read('module.json'))['app']['versionName']
    manifest = args.manifest
    adjacent = source.with_name(f'release-{version}.json')
    if manifest is None and adjacent.is_file():
        manifest = adjacent
    version, number, commit = inspect_source(source, manifest)
    output = (args.output_dir or ROOT/f'dist/harmonyos/appgallery-{version}-{digest(source)[:8]}').expanduser().resolve()
    if output.exists():
        raise ValueError('Output directory already exists; choose a new --output-dir: ' + str(output))
    base = Path.home()/'.harmonyos/signing'
    credentials = {name: Path(os.environ.get(env, str(default))).expanduser().resolve() for name, env, default in [
        ('keystore', 'HARMONYOS_KEYSTORE', base/'release.p12'),
        ('certificate', 'HARMONYOS_APP_CERT', base/'release.cer'),
        ('profile', 'HARMONYOS_PROFILE', base/'profiles/fncpn-release.p7b')]}
    for name, path in credentials.items():
        if not path.is_file():
            raise ValueError('Missing signing ' + name + ': ' + str(path))
    alias = os.environ.get('HARMONYOS_KEY_ALIAS', 'release')
    tools = cli_root()/'sdk/default/openharmony/toolchains/lib'
    jar = tools/'hap-sign-tool.jar'
    if args.password_source == 'keychain':
        password = subprocess.check_output(['security', 'find-generic-password', '-s',
                    os.environ.get('HARMONYOS_SIGNING_KEYCHAIN_SERVICE', 'harmonyos-signing-release'),
                    '-a', alias, '-w'], stderr=subprocess.DEVNULL).rstrip(b'\r\n')
    else:
        password = getpass.getpass('Signing keystore/key password: ').encode()
    if not password:
        raise ValueError('Empty signing password')
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.appgallery-', dir=output.parent) as directory:
        work = Path(directory)
        def sign_package(input_file, output_file):
            sign(['java', '-jar', str(jar), 'sign-app', '-mode', 'localSign', '-keyAlias', alias,
                  '-keystoreFile', str(credentials['keystore']), '-appCertFile', str(credentials['certificate']),
                  '-profileFile', str(credentials['profile']), '-inFile', str(input_file), '-outFile', str(output_file),
                  '-signAlg', 'SHA256withECDSA', '-compatibleVersion', '20', '-signCode', '1', '-pwdInputMode', '1'], password)
        hap = work/'entry-default.hap'
        sign_package(source, hap)
        verify(jar, hap, work, native=True)
        print('HAP and native code signatures verified')
        with zipfile.ZipFile(source) as archive:
            (work/'pack.info').write_bytes(archive.read('pack.info'))
        unsigned = work/'unsigned.app'
        result = subprocess.run(['java', '-jar', str(tools/'app_packing_tool.jar'), '--mode', 'app',
            '--hap-path', str(hap), '--pack-info-path', str(work/'pack.info'), '--replace-pack-info', 'false',
            '--out-path', str(unsigned), '--force', 'true'], capture_output=True)
        if result.returncode:
            raise RuntimeError('Official APP packing failed')
        app = work/f'fncpn-{version}-harmonyos-arm64-appgallery.app'
        sign_package(unsigned, app)
        verify(jar, app, work)
        verify_payload(source, hap, app)
        proof = {'version': version, 'versionCode': number, 'sourceCommit': commit,
                 'sourceSha256': digest(source), 'sourceManifestSha256': digest(manifest) if manifest else None,
                 'hapSignatureVerified': True, 'nativeCodeSignatureVerified': True, 'appSignatureVerified': True,
                 'compiledPayloadUnchanged': True, 'app': app.name, 'appSha256': digest(app)}
        (work/'submission-manifest.json').write_text(json.dumps(proof, indent=2)+'\n')
        (work/'SHA256SUMS').write_text(''.join(digest(p)+'  '+p.name+'\n' for p in [app, hap]))
        for path in [unsigned, work/'verify.cer', work/'verify.p7b', work/'pack.info']:
            path.unlink(missing_ok=True)
        # Publish only fully verified output; incomplete signing stays temporary.
        work.rename(output)
    print(output/app.name)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, RuntimeError, OSError, KeyError, zipfile.BadZipFile, subprocess.CalledProcessError) as error:
        raise SystemExit(str(error)) from None
