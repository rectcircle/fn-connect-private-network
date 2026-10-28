#!/usr/bin/env python3
"""Experimental new-world validation; never publishes or changes release targets."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / 'dist/loongarch-validation'
ENVIRONMENT = json.loads(Path(__file__).with_name('environment.json').read_text())


class Report:
    def __init__(self, directory, **metadata):
        self.directory = Path(directory)
        self.directory.mkdir(parents=True, exist_ok=True)
        self.data = dict(metadata, checks={}, passed=False)

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


def compile_tests():
    environment = dict(os.environ, GOOS='linux', GOARCH='loong64', CGO_ENABLED='0')
    packages = subprocess.check_output(['go', 'list', './...'], cwd=ROOT, env=environment, text=True).splitlines()
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
    report = Report(OUT / 'unit-tests', emulation='qemu-user; host kernel, not LoongArch kernel')
    manifest = json.loads((OUT / 'tests/manifest.json').read_text())
    if not manifest:
        raise RuntimeError('No compiled tests')
    for index, item in enumerate(manifest):
        report.check(str(index), [str(OUT / 'tests' / item['binary']), '-test.v', '-test.timeout=180s'],
                     cwd=ROOT / item['directory'], timeout=240)
    if not report.data['passed']:
        raise SystemExit(1)


def system_vm(container, temporary):
    """Boot the provisioned userland under its own kernel, without SSH or NAT."""
    firmware = temporary / 'firmware.fd'
    download(ENVIRONMENT['firmwareURL'], firmware, ENVIRONMENT['firmwareSHA256'])
    subprocess.run(['docker', 'exec', container, 'mkdir', '-p', '/validation/out', '/validation/scripts', '/validation/packaging/linux'], check=True)
    files = {'fncpn': OUT / 'fncpn', 'probe': OUT / 'probe', 'guest.sh': ROOT / 'scripts/loongarch/guest.sh',
             'version': ROOT / 'internal/version/VERSION', 'commit': OUT / 'commit'}
    if (OUT / 'fncpn-ui').exists():
        files['fncpn-ui'] = OUT / 'fncpn-ui'
    manifest = json.loads((OUT / 'tests/manifest.json').read_text())
    for package in ('linux', 'privileged'):
        expected = '/internal/platform/linux' if package == 'linux' else '/internal/privileged'
        item = next(item for item in manifest if item['package'].endswith(expected))
        files[package + '.test'] = OUT / 'tests' / item['binary']
    for target, source in files.items():
        subprocess.run(['docker', 'cp', str(source), f'{container}:/validation/{target}'], check=True)
    for name in ('linux-ui-smoke.py', 'linux-compatibility.py', 'linux_support.py', 'linux_abi.py', 'rpm_signing.py'):
        subprocess.run(['docker', 'cp', str(ROOT / 'scripts' / name), f'{container}:/validation/scripts/{name}'], check=True)
    subprocess.run(['docker', 'cp', str(ROOT / 'packaging/linux/targets.json'), f'{container}:/validation/packaging/linux/targets.json'], check=True)
    unit = temporary / 'fncpn-validation.service'
    unit.write_text('[Unit]\nAfter=local-fs.target dbus.service\nWants=dbus.service\n'
                    '[Service]\nType=oneshot\nEnvironment=FNCPN_LOONGARCH_VM=1\n'
                    'ExecStart=/bin/bash /validation/guest.sh\nTimeoutStartSec=1200\n'
                    '[Install]\nWantedBy=multi-user.target\n')
    subprocess.run(['docker', 'cp', str(unit), f'{container}:/etc/systemd/system/fncpn-validation.service'], check=True)
    subprocess.run(['docker', 'exec', container, 'bash', '-ec',
                    'systemctl enable fncpn-validation.service; '
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
        command = ['qemu-system-loongarch64', '-machine', 'virt', '-accel', 'tcg',
                   '-m', '4096', '-smp', '2', '-bios', str(firmware),
                   '-kernel', str(kernel), '-initrd', str(initrd),
                   '-append', 'root=/dev/vda rw rootwait console=ttyS0 systemd.unit=multi-user.target',
                   '-drive', f'file={disk},format=raw,if=none,id=root', '-device', 'virtio-blk-pci,drive=root',
                   '-nic', 'none', '-display', 'none', '-serial', 'stdio', '-monitor', 'none', '-no-reboot']
        with (OUT / 'serial.log').open('w') as log:
            process = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            try:
                code = process.wait(timeout=1500)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
                raise RuntimeError('VM timed out; see serial.log')
        subprocess.run(['sudo', 'mount', '-o', 'loop,ro', str(disk), str(mount)], check=True)
        mounted = True
        subprocess.run(['sudo', 'cp', '-a', str(mount / 'validation/out'), str(OUT / 'system')], check=True)
        subprocess.run(['sudo', 'chown', '-R', f'{os.getuid()}:{os.getgid()}', str(OUT / 'system')], check=True)
        result = OUT / 'system/exit-code'
        if code or not result.exists() or result.read_text().strip() != '0':
            raise RuntimeError('Guest did not complete all checks; see serial.log and system/guest.log')
    finally:
        if mounted:
            subprocess.run(['sudo', 'umount', str(mount)], check=True)


def main():
    if sys.argv[1:] == ['compile-tests']:
        compile_tests()
        return
    if sys.argv[1:] == ['userspace-tests']:
        userspace_tests()
        return
    if sys.argv[1:]:
        raise SystemExit('No arguments, compile-tests or userspace-tests')
    if sys.platform != 'linux' or os.environ.get('GITHUB_ACTIONS') != 'true':
        raise SystemExit('Run on a disposable GitHub Actions Linux runner')
    commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    report = Report(OUT, commit=commit, environment=ENVIRONMENT, experimental=True,
                    notValidated=['physical hardware and GPU', 'real fnOS login/OTP and end-to-end session recovery',
                                  'historical release compatibility', 'package installation/upgrade/uninstall',
                                  'Wayland, interactive tray and polkit flows', 'old-world ABI'])
    (OUT / 'commit').write_text(commit + '\n')
    (OUT / 'environment.json').write_text(json.dumps(ENVIRONMENT, indent=2) + '\n')
    container = 'fncpn-loongarch-validation'
    environment = dict(os.environ, GOOS='linux', GOARCH='loong64', CGO_ENABLED='0')
    try:
        report.check('host-tools', ['bash', '-c', 'go version; node --version; docker version; qemu-system-loongarch64 --version'])
        core = report.check('core-build', ['go', 'build', '-trimpath', '-ldflags',
                            f'-X github.com/rectcircle/fn-connect-private-network/internal/version.Revision={commit}',
                            '-o', str(OUT / 'fncpn'), './cmd/fncpn'], env=environment)
        tests = report.check('test-build', [sys.executable, __file__, 'compile-tests'], timeout=1200)
        frontend = report.check('frontend', ['bash', '-ec', 'npm --prefix ui/frontend ci && npm --prefix ui/frontend run build'], timeout=600)
        started = report.check('container', ['docker', 'run', '-d', '--platform', 'linux/loong64', '--name', container,
                               '-v', f'{ROOT}:/workspace', ENVIRONMENT['image'], 'sleep', 'infinity'])
        if not started:
            return
        ready = report.check('provision', ['docker', 'exec', container, 'bash', '/workspace/scripts/loongarch/provision.sh'], timeout=2400)
        if not ready:
            return
        if core:
            report.check('core-runtime', ['docker', 'exec', container, '/workspace/dist/loongarch-validation/fncpn', 'version'])
        if tests:
            report.check('unit-tests', ['docker', 'exec', container, 'python3', '/workspace/scripts/loongarch/validate.py', 'userspace-tests'], timeout=2400)
        with tempfile.TemporaryDirectory(prefix='fncpn-loongarch-') as temporary:
            temporary = Path(temporary)
            if frontend:
                archive = temporary / 'go.tar.gz'
                download(ENVIRONMENT['goURL'], archive, ENVIRONMENT['goSHA256'])
                subprocess.run(['docker', 'cp', str(archive), f'{container}:/tmp/go.tar.gz'], check=True)
                subprocess.run(['docker', 'exec', container, 'bash', '-ec', 'mkdir -p /opt; tar -xzf /tmp/go.tar.gz -C /opt; rm /tmp/go.tar.gz'], check=True)
                report.check('ui-build', ['docker', 'exec', '-e', f'VALIDATION_COMMIT={commit}', container, 'bash', '/workspace/scripts/loongarch/build-ui.sh'], timeout=3600)
            if core and tests:
                # Keep system validation independent of unit/UI failure. A missing UI
                # is an explicit guest failure after the kernel checks have run.
                try:
                    system_vm(container, temporary)
                    report.data['checks']['full-system'] = {'status': 'passed'}
                except Exception as error:
                    report.data['checks']['full-system'] = {'status': 'failed', 'error': str(error)}
                    print(f'[full-system] FAILED: {error}', flush=True)
    except Exception as error:
        report.data['checks']['infrastructure'] = {'status': 'failed', 'error': str(error)}
        raise
    finally:
        subprocess.run(['docker', 'rm', '-f', container], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        report.save()
        lines = ['# LoongArch 新世界实验验证', '', f'Commit: `{commit}`', '',
                 '| 检查 | 结果 |', '| --- | --- |']
        lines += [f'| {name} | {item["status"]} |' for name, item in report.data['checks'].items()]
        lines += ['', '**这不是正式平台支持或发布验收结论。**', '', '未验证：']
        lines += ['- ' + item for item in report.data['notValidated']]
        (OUT / 'summary.md').write_text('\n'.join(lines) + '\n')
        if not report.data['passed']:
            raise SystemExit(1)


if __name__ == '__main__':
    main()
