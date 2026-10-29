#!/usr/bin/env python3
"""Build and gate new-world LoongArch packages; never publishes a release."""
import hashlib
import datetime
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / 'dist/loongarch-validation'
ENVIRONMENT = json.loads(Path(__file__).with_name('environment.json').read_text())
PACKAGE_OUT = ROOT / 'dist/linux/deb-loong64/loong64'
# qemu-user successfully sets SO_BINDTODEVICE but its getsockopt translation
# returns EINVAL. Execute this exact test against the guest kernel instead.
DEFERRED_TEST = 'TestLinuxLocalProbeBindsPhysicalInterface'


class Report:
    def __init__(self, directory, required=(), **metadata):
        self.directory = Path(directory)
        self.directory.mkdir(parents=True, exist_ok=True)
        self.data = dict(metadata, checks={name: {'status': 'pending'} for name in required}, passed=False)
        self.save()

    def check(self, name, command, *, cwd=ROOT, env=None, timeout=900):
        print(f'[{name}] {command[0]}', flush=True)
        start = time.monotonic()
        entry = self.data['checks'][name] = {'status': 'failed', 'command': list(map(str, command))}
        try:
            with (self.directory / f'{name}.log').open('w') as log:
                process = subprocess.Popen(command, cwd=cwd, env=env, stdout=log,
                                           stderr=subprocess.STDOUT, start_new_session=True)
                try:
                    code = process.wait(timeout=timeout)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
                    raise RuntimeError(f'timeout after {timeout}s')
            entry['exitCode'] = code
            if code:
                raise RuntimeError(f'exit {code}; see {name}.log')
            entry['status'] = 'passed'
            return True
        except Exception as error:
            entry['error'] = str(error)
            print(f'[{name}] FAILED: {error}', flush=True)
            log = self.directory / f'{name}.log'
            if log.exists():
                print(log.read_text(errors='replace')[-6000:], flush=True)
            return False
        finally:
            entry['seconds'] = round(time.monotonic() - start, 2)
            self.save()

    def save(self):
        self.data['passed'] = bool(self.data['checks']) and all(
            item['status'] == 'passed' for item in self.data['checks'].values())
        (self.directory / 'report.json').write_text(json.dumps(self.data, indent=2) + '\n')


def download(url, destination, sha256):
    digest = hashlib.sha256()
    temporary = destination.with_suffix('.partial')
    try:
        with urllib.request.urlopen(url, timeout=120) as source, temporary.open('wb') as output:
            while block := source.read(1024 * 1024):
                output.write(block)
                digest.update(block)
        if digest.hexdigest() != sha256:
            raise ValueError(f'SHA256 mismatch for {url}')
        temporary.replace(destination)
    finally:
        temporary.unlink(missing_ok=True)


def read_guest_results(archive, destination, code):
    # Read the independent virtio channel, never mount a filesystem last written
    # by a newer guest kernel. Missing/incomplete archives and exit codes fail.
    with tarfile.open(archive, 'r:gz') as result:
        result.extractall(destination, filter='data')
    status = destination / 'exit-code'
    if code or not status.is_file() or status.read_text().strip() != '0':
        raise RuntimeError('Guest did not complete all checks; see serial.log and system/guest.log')


def replace_binary(source, destination):
    # Docker's root owns the previous bind-mounted UI output. The runner owns
    # its parent directory, so replace the inode instead of truncating that file.
    descriptor, name = tempfile.mkstemp(prefix=destination.name + '.', dir=destination.parent)
    os.close(descriptor)
    temporary = Path(name)
    try:
        shutil.copy2(source, temporary)
        temporary.replace(destination)
    finally:
        temporary.unlink(missing_ok=True)


def compile_tests():
    environment = dict(os.environ, GOOS='linux', GOARCH='loong64', CGO_ENABLED='0')
    packages = subprocess.check_output(['go', 'list', './...'], cwd=ROOT, env=environment, text=True).splitlines()
    if os.environ.get('FNCPN_ROOT_REGRESSION') == '0':
        required = ('/internal/client', '/internal/privileged', '/internal/platform/linux')
        packages = [package for package in packages if package.endswith(required)]
    manifest = []
    directory = OUT / 'tests'
    directory.mkdir(exist_ok=True)
    for index, package in enumerate(packages):
        binary = directory / f'{index}.test'
        subprocess.run(['go', 'test', '-c', '-o', str(binary), package], cwd=ROOT, env=environment, check=True)
        if binary.exists():
            source = subprocess.check_output(['go', 'list', '-f', '{{.Dir}}', package], cwd=ROOT, env=environment, text=True).strip()
            manifest.append({'package': package, 'binary': binary.name, 'directory': str(Path(source).relative_to(ROOT))})
    (directory / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    subprocess.run(['go', 'test', '-c', '-tags', 'loongarch_validation', '-o', str(OUT / 'probe'),
                    './scripts/loongarch/probe'], cwd=ROOT, env=environment, check=True)


def userspace_tests():
    manifest = json.loads((OUT / 'tests/manifest.json').read_text())
    if not manifest:
        raise RuntimeError('No compiled tests')
    report = Report(OUT / 'unit-tests', required=[str(i) for i in range(len(manifest))],
                    emulation='qemu-user; host kernel, not LoongArch kernel',
                    deferredToFullSystem=[DEFERRED_TEST])
    for index, item in enumerate(manifest):
        command = [str(OUT / 'tests' / item['binary']), '-test.v', '-test.timeout=180s']
        if item['package'].endswith('/internal/client'):
            command += ['-test.skip', '^' + DEFERRED_TEST + '$']
        report.check(str(index), command,
                     cwd=ROOT / item['directory'], timeout=240)
    if not report.data['passed']:
        raise SystemExit(1)


def system_vm(container, temporary):
    """Boot the provisioned userland under its own kernel, without SSH or NAT."""
    firmware = temporary / 'firmware.fd'
    download(ENVIRONMENT['firmwareURL'], firmware, ENVIRONMENT['firmwareSHA256'])
    subprocess.run(['docker', 'exec', container, 'mkdir', '-p', '/validation/out', '/validation/scripts', '/validation/packaging/linux'], check=True)
    files = {'fncpn': OUT / 'fncpn', 'probe': OUT / 'probe', 'guest.sh': ROOT / 'scripts/loongarch/guest.sh',
             'version': ROOT / 'internal/version/VERSION', 'commit': OUT / 'commit',
             'guest_commands.py': ROOT / 'scripts/loongarch/guest_commands.py'}
    if (OUT / 'fncpn-ui').exists():
        files['fncpn-ui'] = OUT / 'fncpn-ui'
    manifest = json.loads((OUT / 'tests/manifest.json').read_text())
    for package in ('linux', 'privileged', 'client'):
        expected = '/internal/platform/linux' if package == 'linux' else '/internal/' + package
        item = next(item for item in manifest if item['package'].endswith(expected))
        files[package + '.test'] = OUT / 'tests' / item['binary']
    for target, source in files.items():
        subprocess.run(['docker', 'cp', str(source), f'{container}:/validation/{target}'], check=True)
    for name in ('linux-ui-smoke.py', 'linux-compatibility.py', 'linux_support.py', 'linux_abi.py', 'rpm_signing.py', 'check-linux-packages.py'):
        subprocess.run(['docker', 'cp', str(ROOT / 'scripts' / name), f'{container}:/validation/scripts/{name}'], check=True)
    subprocess.run(['docker', 'cp', str(ROOT / 'packaging/linux/targets.json'), f'{container}:/validation/packaging/linux/targets.json'], check=True)
    subprocess.run(['docker', 'cp', str(ROOT / 'scripts/loongarch/package-lifecycle.py'), f'{container}:/validation/package-lifecycle.py'], check=True)
    unit = temporary / 'fncpn-validation.service'
    unit.write_text('[Unit]\nAfter=local-fs.target dbus.service\nWants=dbus.service\n'
                    '[Service]\nType=oneshot\nEnvironment=FNCPN_LOONGARCH_VM=1\n'
                    'StandardOutput=journal+console\nStandardError=inherit\n'
                    'ExecStart=/bin/bash /validation/guest.sh\nTimeoutStartSec=1200\n'
                    '[Install]\nWantedBy=multi-user.target\n')
    subprocess.run(['docker', 'cp', str(unit), f'{container}:/etc/systemd/system/fncpn-validation.service'], check=True)
    subprocess.run(['docker', 'exec', container, 'bash', '-ec',
                    'systemctl enable fncpn-validation.service; '
                    'systemctl mask console-getty.service serial-getty@ttyS0.service getty@tty1.service; '
                    'rm -rf /opt/go /root/.cache /root/go; '
                    'rm -f /etc/machine-id; touch /etc/machine-id; '
                    'ls /boot/vmlinuz-* /boot/initrd.img-*'], check=True)
    boot = temporary / 'boot'
    subprocess.run(['docker', 'cp', f'{container}:/boot', str(boot)], check=True)
    kernel = sorted(boot.glob('vmlinuz-*'))[-1]
    initrd = boot / kernel.name.replace('vmlinuz-', 'initrd.img-', 1)
    disk = temporary / 'rootfs.raw'
    with disk.open('wb') as stream:
        stream.truncate(10 * 1024**3)
    subprocess.run(['mkfs.ext4', '-F', str(disk)], check=True)
    mount = temporary / 'mount'
    mount.mkdir()
    mounted = False
    try:
        subprocess.run(['sudo', 'mount', '-o', 'loop', str(disk), str(mount)], check=True)
        mounted = True
        export = subprocess.Popen(['docker', 'export', container], stdout=subprocess.PIPE)
        try:
            unpack = subprocess.run(['sudo', 'tar', '-xpf', '-', '-C', str(mount)], stdin=export.stdout)
        finally:
            export.stdout.close()
        if export.wait() or unpack.returncode:
            raise RuntimeError('Failed to export disposable rootfs')
        subprocess.run(['sudo', 'umount', str(mount)], check=True)
        mounted = False
        subprocess.run(['docker', 'rm', '-f', container], check=True)
        # TCG only: no nested virtualization, firmware variables, or host net changes.
        results = OUT / 'guest-results.tar.gz'
        command = ['qemu-system-loongarch64', '-machine', 'virt', '-accel', 'tcg',
                   '-m', '4096', '-smp', '2', '-bios', str(firmware),
                   '-kernel', str(kernel), '-initrd', str(initrd),
                   '-append', 'root=/dev/vda rw rootwait console=ttyS0 systemd.unit=multi-user.target',
                   '-drive', f'file={disk},format=raw,if=none,id=root', '-device', 'virtio-blk-pci,drive=root',
                   '-device', 'virtio-serial-pci', '-chardev', f'file,id=results,path={results}',
                   '-device', 'virtserialport,chardev=results,name=fncpn.results',
                   '-nic', 'none', '-display', 'none', '-serial', 'stdio', '-monitor', 'none', '-no-reboot']
        with (OUT / 'serial.log').open('w') as log:
            process = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            try:
                code = process.wait(timeout=1500)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
                raise RuntimeError('VM timed out; see serial.log')
        read_guest_results(results, OUT / 'system', code)
    finally:
        if mounted:
            subprocess.run(['sudo', 'umount', str(mount)], check=True)


def start_container(report, name, runtime=False):
    """Cache only provisioned system dependencies, before any candidate is copied."""
    cache = Path.home() / '.cache/fncpn-loongarch'
    epoch = datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m')
    digest = hashlib.sha256((ROOT / 'scripts/loongarch/provision.sh').read_bytes()
                            + json.dumps(ENVIRONMENT, sort_keys=True).encode()
                            + epoch.encode()).hexdigest()[:20]
    role = 'runtime' if runtime else 'builder'
    image = f'fncpn-loongarch-{role}:{digest}'
    archive = cache / 'environments' / f'{role}-{digest}.tar'
    provision = 'runtime-provision' if runtime else 'provision'
    report.data.setdefault('environmentCache', {})[role] = {'hit': archive.exists(), 'image': image}
    if archive.exists():
        subprocess.run(['docker', 'load', '--input', str(archive)], check=True)
    command = ['docker', 'run', '-d', '--platform', 'linux/loong64', '--name', name,
               '-v', f'{ROOT}:/workspace']
    if not runtime:
        command += ['-v', f'{ROOT}:{ROOT}:ro',
                    '-v', f'{cache / "go-build"}:/root/.cache/go-build',
                    '-v', f'{cache / "go-mod"}:/root/go/pkg/mod',
                    '-v', f'{cache / "toolchain/bin/linux_loong64"}:/opt/tools:ro']
    command += [image if archive.exists() else ENVIRONMENT['image'], 'sleep', 'infinity']
    if not report.check('runtime-container' if runtime else 'container', command):
        return False
    if archive.exists():
        # Re-record the exact cached package versions in this run's evidence.
        record = 'runtime-base-packages.tsv' if runtime else 'packages.tsv'
        return report.check(provision, ['docker', 'exec', name, 'bash', '-ec',
            'test "$(dpkg --print-architecture)" = loong64; '
            "dpkg-query -W -f='${Package}\\t${Version}\\t${Architecture}\\n' > /workspace/dist/loongarch-validation/" + record])
    arguments = ['docker', 'exec']
    if runtime:
        arguments += ['-e', 'RUNTIME_ONLY=1']
    if not report.check(provision, arguments + [name, 'timeout', '--kill-after=10s', '2300s',
                         'bash', '/workspace/scripts/loongarch/provision.sh'], timeout=2400):
        return False
    # Bind mounts (checkout, Go caches and tools) are excluded by docker commit.
    # Runtime images are saved before packages, users or test state are added.
    archive.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(['docker', 'commit', name, image], check=True)
    partial = archive.with_suffix('.partial')
    subprocess.run(['docker', 'save', '--output', str(partial), image], check=True)
    partial.replace(archive)
    return True


def root_regression_paths(paths):
    # Separate Go modules cannot affect root-package tests. Keep root Go files
    # even inside a normally documentation/platform-only directory.
    excluded = ('ios/', 'docs/', 'website/', 'platform/macos/')
    return sorted(path for path in paths if not path.startswith(('mobile/', 'ui/'))
                  and (path.endswith('.go') or (not path.startswith(excluded)
                  and path not in ('README.md', 'CHANGELOG.md', 'TODO.txt'))))


def root_regression_fingerprint():
    paths = subprocess.check_output(['git', 'ls-files', '-z'], cwd=ROOT).decode().strip('\0').split('\0')
    digest = hashlib.sha256(b'loongarch-root-regression-v1')
    digest.update(json.dumps(ENVIRONMENT, sort_keys=True).encode())
    digest.update(datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m').encode())
    digest.update(subprocess.check_output(['go', 'env', 'GOVERSION'], cwd=ROOT))
    for path in root_regression_paths(paths):
        digest.update(path.encode() + b'\0' + (ROOT / path).read_bytes())
    return digest.hexdigest()


def root_regression_plan(cache, fingerprint, force=False):
    receipt = cache / 'root-tests' / f'{fingerprint}.json'
    try:
        proof = json.loads(receipt.read_text())
    except (OSError, ValueError):
        proof = {}
    if not isinstance(proof, dict):
        proof = {}
    commit = proof.get('commit', '')
    valid_commit = isinstance(commit, str) and len(commit) == 40 and all(c in '0123456789abcdef' for c in commit)
    reuse = (not force and proof.get('passed') is True and proof.get('fingerprint') == fingerprint and valid_commit)
    return {'fingerprint': fingerprint, 'reused': reuse, 'previousCommit': commit if reuse else None}


def load_report():
    report = Report.__new__(Report)
    report.directory = OUT
    report.data = json.loads((OUT / 'report.json').read_text())
    commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    if report.data['commit'] != commit:
        raise RuntimeError('Stage source commit differs from the prepared candidate')
    return report


def validation_plan(full, defer_system=False):
    required = ['host-tools', 'frontend', 'container', 'provision', 'packaging-tool', 'package-build']
    if full:
        required += ['core-build', 'test-build', 'core-runtime', 'unit-tests', 'ui-tests']
        if not defer_system:
            required += ['runtime-provision', 'offline-dependencies', 'full-system']
    return ('source-and-build' if full and defer_system else 'full' if full else 'build'), required


def phase(stage):
    container, runtime = 'fncpn-loongarch-validation', 'fncpn-loongarch-runtime'
    work = ROOT / 'dist/loongarch-work'
    cache = Path.home() / '.cache/fncpn-loongarch'
    environment = dict(os.environ, GOOS='linux', GOARCH='loong64', CGO_ENABLED='0')
    if stage == 'prepare':
        if OUT.exists():
            shutil.rmtree(OUT)
        if work.exists():
            shutil.rmtree(work)
        work.mkdir(parents=True)
        commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
        full = os.environ.get('FULL_VALIDATION') == '1'
        level, required = validation_plan(full, os.environ.get('DEFER_SYSTEM_VALIDATION') == '1')
        report = Report(OUT, required=required, validationLevel=level,
            commit=commit, environment=ENVIRONMENT, deferredToFullSystem=[DEFERRED_TEST],
            notValidated=['physical hardware and GPU', 'real fnOS login/OTP and end-to-end session recovery',
                          'historical release compatibility (upgrade fixture uses candidate code)',
                          'Wayland, interactive tray and polkit flows', 'old-world ABI'])
        if level == 'source-and-build':
            report.data['notValidated'].append('package/system acceptance is deferred to separate same-package jobs')
        if not full:
            report.data['notValidated'].append('build-only: package lifecycle, UI startup and kernel/DNS acceptance were not run')
        (OUT / 'commit').write_text(commit + '\n')
        (OUT / 'environment.json').write_text(json.dumps(ENVIRONMENT, indent=2) + '\n')
    else:
        report = load_report()
        commit = report.data['commit']
    full = report.data['validationLevel'] in ('full', 'source-and-build')
    system_validation = report.data['validationLevel'] == 'full'
    started = time.monotonic()
    try:
        if stage == 'prepare':
            report.check('host-tools', ['bash', '-ec', 'go version; node --version; docker version' + ('; qemu-system-loongarch64 --version' if system_validation else '')])
            if full:
                fingerprint = root_regression_fingerprint()
                scope = root_regression_plan(cache, fingerprint, os.environ.get('FORCE_ROOT_REGRESSION') == '1')
                reuse = scope['reused']
                report.data['rootRegression'] = scope
                report.check('core-build', ['go', 'build', '-trimpath', '-ldflags',
                    f'-X github.com/rectcircle/fn-connect-private-network/internal/version.Revision={commit}',
                    '-o', str(OUT / 'fncpn'), './cmd/fncpn'], env=environment)
                report.check('test-build', [sys.executable, __file__, 'compile-tests'], timeout=1200,
                             env=dict(os.environ, FNCPN_ROOT_REGRESSION='0' if reuse else '1'))
            report.check('frontend', ['bash', '-ec', 'npm --prefix ui/frontend ci && npm --prefix ui/frontend run build'], timeout=600)
            for directory in ('go-build', 'go-mod', 'toolchain/bin/linux_loong64'):
                (cache / directory).mkdir(parents=True, exist_ok=True)
            report.check('packaging-tool', ['go', 'install', 'github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.40.0'],
                         env=dict(environment, GOBIN='', GOPATH=str(cache / 'toolchain')))
        elif stage == 'builder':
            start_container(report, container)
        elif stage == 'tests':
            if full:
                report.check('core-runtime', ['docker', 'exec', container, '/workspace/dist/loongarch-validation/fncpn', 'version'])
                scope = report.data['rootRegression']
                if scope['reused']:
                    report.data['checks']['unit-tests'] = {'status': 'passed', 'reused': True,
                        'fingerprint': scope['fingerprint'], 'previousCommit': scope['previousCommit'], 'seconds': 0}
                    print('Reusing unchanged root regression:', scope, flush=True)
                elif report.check('unit-tests', ['docker', 'exec', container, 'timeout', '--kill-after=10s', '2300s',
                    'python3', '/workspace/scripts/loongarch/validate.py', 'userspace-tests'], timeout=2400):
                    receipt = cache / 'root-tests' / (scope['fingerprint'] + '.json')
                    receipt.parent.mkdir(parents=True, exist_ok=True)
                    receipt.write_text(json.dumps({'passed': True, 'fingerprint': scope['fingerprint'], 'commit': commit}) + '\n')
            archive = work / 'go.tar.gz'
            download(ENVIRONMENT['goURL'], archive, ENVIRONMENT['goSHA256'])
            subprocess.run(['docker', 'cp', str(archive), f'{container}:/tmp/go.tar.gz'], check=True)
            subprocess.run(['docker', 'exec', container, 'bash', '-ec', 'mkdir -p /opt; tar -xzf /tmp/go.tar.gz -C /opt; rm /tmp/go.tar.gz'], check=True)
            if full:
                report.check('ui-tests', ['docker', 'exec', container, 'timeout', '--kill-after=10s', '3500s',
                             'bash', '/workspace/scripts/loongarch/build-ui.sh'], timeout=3600)
        elif stage == 'packages':
            if report.check('package-build', ['docker', 'exec',
                '-e', 'FULL_VALIDATION=' + ('1' if full else '0'),
                '-e', 'RELEASE=' + os.environ.get('RELEASE', '0'),
                '-e', 'RELEASE_COMMIT=' + os.environ.get('RELEASE_COMMIT', ''),
                '-e', 'RELEASE_VERSION=' + os.environ.get('RELEASE_VERSION', ''), container,
                'timeout', '--kill-after=10s', '1800s', 'bash', '/workspace/scripts/loongarch/build-packages.sh'], timeout=1900):
                for binary in ('fncpn', 'fncpn-ui'):
                    replace_binary(PACKAGE_OUT / binary, OUT / binary)
        elif stage == 'runtime':
            if start_container(report, runtime, runtime=True):
                subprocess.run(['docker', 'exec', runtime, 'mkdir', '-p', '/validation'], check=True)
                subprocess.run(['docker', 'cp', str(PACKAGE_OUT), f'{runtime}:/validation/packages'], check=True)
                report.check('offline-dependencies', ['docker', 'exec', runtime, 'bash', '-ec',
                    'apt-get install -y --no-install-recommends --download-only /validation/packages/*.deb '
                    'xvfb xauth fonts-dejavu-core libgl1-mesa-dri'], timeout=900)
            subprocess.run(['docker', 'rm', '-f', container], check=True)
        elif stage == 'system':
            system_vm(runtime, work)
            source = OUT / 'system/package-evidence.json'
            if json.loads(source.read_text()).get('passed') is not True:
                raise RuntimeError('Installed-package lifecycle did not pass')
            compatibility = ROOT / 'dist/linux-compat'
            compatibility.mkdir(exist_ok=True)
            shutil.copy2(source, compatibility / 'debian-forky-loong64.json')
            report.data['checks']['full-system'] = {'status': 'passed', 'seconds': round(time.monotonic() - started, 2)}
        elif stage == 'finish':
            for name in (container, runtime):
                subprocess.run(['docker', 'rm', '-f', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if cache.exists():
                subprocess.run(['sudo', 'chown', '-R', f'{os.getuid()}:{os.getgid()}', str(cache)], check=True)
            report.save()
            lines = ['# LoongArch 新世界 DEB 构建与验收', '', f'Commit: `{commit}`', '',
                     '验证范围：' + report.data['validationLevel'], '', '| 检查 | 结果 | 秒数 |', '| --- | --- | --- |']
            lines += [f'| {name} | {item["status"]} | {item.get("seconds", "")} |' for name, item in report.data['checks'].items()]
            lines += ['', '环境缓存：`' + json.dumps(report.data.get('environmentCache', {})) + '`',
                      '', '根模块回归：`' + json.dumps(report.data.get('rootRegression', {})) + '`', '', '未验证：']
            lines += ['- ' + item for item in report.data['notValidated']]
            (OUT / 'summary.md').write_text('\n'.join(lines) + '\n')
            if not report.data['passed']:
                raise RuntimeError('Incomplete or failed LoongArch checks')
        else:
            raise ValueError('Unknown stage: ' + stage)
    except Exception as error:
        report.data['checks']['stage-' + stage] = {'status': 'failed', 'error': str(error)}
        if stage == 'system':
            for log in (OUT / 'serial.log', OUT / 'system/guest.log'):
                if log.exists():
                    print(log.read_text(errors='replace')[-6000:], flush=True)
        raise
    finally:
        report.data.setdefault('stages', {})[stage] = {'seconds': round(time.monotonic() - started, 2)}
        report.save()
    if any(item['status'] == 'failed' for item in report.data['checks'].values()):
        raise SystemExit(1)


def main():
    if sys.argv[1:] == ['compile-tests']:
        compile_tests()
        return
    if sys.argv[1:] == ['userspace-tests']:
        userspace_tests()
        return
    if sys.platform != 'linux' or os.environ.get('GITHUB_ACTIONS') != 'true':
        raise SystemExit('Run on a disposable GitHub Actions Linux runner')
    if len(sys.argv) == 3 and sys.argv[1] == '--stage':
        phase(sys.argv[2])
        return
    if sys.argv[1:]:
        raise SystemExit('Use --stage STAGE, compile-tests or userspace-tests')
    try:
        stages = ['prepare', 'builder', 'tests', 'packages']
        if os.environ.get('FULL_VALIDATION') == '1':
            stages += ['runtime', 'system']
        for stage in stages:
            phase(stage)
    finally:
        phase('finish')


if __name__ == '__main__':
    main()
