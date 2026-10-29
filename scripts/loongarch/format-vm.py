#!/usr/bin/env python3
"""Run format acceptance with a real kernel; reuse the clean Debian kernel cache.

The product userspace remains openEuler or Arch. Debian supplies only the boot
kernel/initramfs/modules, not product runtime libraries or package dependencies.
"""
import json
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

from validate import ROOT, OUT, ENVIRONMENT, Report, download, read_guest_results, start_container


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def main():
    target = os.environ['TARGET']
    output = ROOT / 'dist/linux' / target / 'loong64'
    OUT.mkdir(parents=True, exist_ok=True)
    report = Report(OUT / 'kernel-cache')
    kernel_container = 'fncpn-format-kernel'
    container = 'fncpn-format'
    with tempfile.TemporaryDirectory(prefix='fncpn-format-vm-') as directory:
        temporary = Path(directory)
        mounted = False
        mount = temporary / 'mount'
        mount.mkdir()
        try:
            if not start_container(report, kernel_container, runtime=True):
                raise RuntimeError('Could not prepare shared kernel environment')
            run('docker', 'cp', f'{kernel_container}:/boot', str(temporary / 'boot'))
            run('docker', 'cp', f'{kernel_container}:/usr/lib/modules', str(temporary / 'modules'))
            run('docker', 'rm', '-f', kernel_container)
            kernel = sorted((temporary / 'boot').glob('vmlinuz-*'))[-1]
            initrd = kernel.with_name(kernel.name.replace('vmlinuz-', 'initrd.img-', 1))
            firmware = temporary / 'firmware.fd'
            download(ENVIRONMENT['firmwareURL'], firmware, ENVIRONMENT['firmwareSHA256'])
            disk = temporary / 'rootfs.raw'
            with disk.open('wb') as stream:
                stream.truncate(10 * 1024**3)
            run('mkfs.ext4', '-F', str(disk))
            run('sudo', 'mount', '-o', 'loop', str(disk), str(mount))
            mounted = True
            export = subprocess.Popen(['docker', 'export', container], stdout=subprocess.PIPE)
            try:
                unpack = subprocess.run(['sudo', 'tar', '-xpf', '-', '-C', str(mount)], stdin=export.stdout)
            finally:
                export.stdout.close()
            if export.wait() or unpack.returncode:
                raise RuntimeError('Could not export clean candidate userspace')
            # Docker export excludes the checkout bind mount. Include tracked
            # sources explicitly, plus only the candidate target's packages.
            run('sudo', 'mkdir', '-p', str(mount / 'workspace'))
            archive = subprocess.Popen(['git', 'archive', 'HEAD'], cwd=ROOT, stdout=subprocess.PIPE)
            try:
                run('sudo', 'tar', '-xf', '-', '-C', str(mount / 'workspace'), stdin=archive.stdout)
            finally:
                archive.stdout.close()
            if archive.wait():
                raise RuntimeError('Could not archive candidate scripts')
            guest_output = mount / 'workspace/dist/linux' / target / 'loong64'
            run('sudo', 'mkdir', '-p', str(guest_output))
            run('sudo', 'cp', '-a', str(output) + '/.', str(guest_output))
            run('sudo', 'cp', '-a', str(temporary / 'modules'), str(mount / 'usr/lib/'))
            if os.environ.get('FULL_ACCEPTANCE') == '1':
                run('sudo', 'mkdir', '-p', str(mount / 'validation'))
                run('sudo', 'cp', str(OUT / 'probe'), str(mount / 'validation/probe'))
                tests = json.loads((OUT / 'tests/manifest.json').read_text())
                for name in ('client', 'privileged', 'linux'):
                    item = next(item for item in tests if item['package'].endswith('/' + name))
                    run('sudo', 'cp', str(OUT / 'tests' / item['binary']), str(mount / f'validation/{name}.test'))
            guest = temporary / 'format-guest.sh'
            guest.write_text('''#!/bin/bash
set -euo pipefail
out=/workspace/dist/linux/$TARGET/loong64
exec > >(tee "$out/guest.log") 2>&1
finish() {
 code=$?
 trap - EXIT
 journalctl -b --no-pager > "$out/journal.log" || true
 printf '%s\\n' "$code" > "$out/exit-code"
 mkdir -p /validation/results
 cp "$out/"*.json "$out/"*.log "$out/"*.txt "$out/exit-code" /validation/results/ 2>/dev/null || true
 if test -c /dev/virtio-ports/fncpn.results; then
  tar -C /validation/results -czf /dev/virtio-ports/fncpn.results . || true
 fi
 sync
 systemctl poweroff --no-block
}
trap finish EXIT
modprobe virtio_console
udevadm settle --timeout=30
test -c /dev/virtio-ports/fncpn.results
python3 /workspace/scripts/loongarch/runtime-format.py finish
''')
            unit = temporary / 'fncpn-format-validation.service'
            unit.write_text('[Unit]\nAfter=local-fs.target dbus.service\nWants=dbus.service\n'
                            '[Service]\nType=oneshot\nStandardOutput=journal+console\n'
                            f'Environment=TARGET={target} DIST_DIR=/workspace/dist/linux FULL_ACCEPTANCE={os.environ.get("FULL_ACCEPTANCE", "0")} RELEASE={os.environ.get("RELEASE", "0")} FNCPN_VALIDATION_COMMIT={os.environ.get("FNCPN_VALIDATION_COMMIT", "")}\n'
                            'ExecStart=/bin/bash /validation/format-guest.sh\nTimeoutStartSec=1800\n'
                            '[Install]\nWantedBy=multi-user.target\n')
            run('sudo', 'mkdir', '-p', str(mount / 'validation'), str(mount / 'etc/systemd/system/multi-user.target.wants'))
            run('sudo', 'cp', str(guest), str(mount / 'validation/format-guest.sh'))
            run('sudo', 'cp', str(unit), str(mount / 'etc/systemd/system/'))
            run('sudo', 'ln', '-sf', '../fncpn-format-validation.service', str(mount / 'etc/systemd/system/multi-user.target.wants/fncpn-format-validation.service'))
            run('sudo', 'umount', str(mount))
            mounted = False
            results = temporary / 'guest-results.tar.gz'
            command = ['qemu-system-loongarch64', '-machine', 'virt', '-accel', 'tcg', '-m', '4096', '-smp', '2',
                       '-bios', str(firmware), '-kernel', str(kernel), '-initrd', str(initrd),
                       '-append', 'root=/dev/vda rw rootwait console=ttyS0 init=/usr/lib/systemd/systemd systemd.unit=multi-user.target',
                       '-drive', f'file={disk},format=raw,if=none,id=root', '-device', 'virtio-blk-pci,drive=root',
                       '-device', 'virtio-serial-pci', '-chardev', f'file,id=results,path={results}',
                       '-device', 'virtserialport,chardev=results,name=fncpn.results', '-nic', 'none',
                       '-display', 'none', '-serial', 'stdio', '-monitor', 'none', '-no-reboot']
            try:
                with (output / 'serial.log').open('w') as log:
                    result = subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, timeout=1900)
                read_guest_results(results, output, result.returncode)
                evidence = json.loads((output / 'runtime-validation.json').read_text())
                if not evidence.get('passed'):
                    raise RuntimeError('Guest acceptance did not pass')
                if os.environ.get('FULL_ACCEPTANCE') == '1' and target != 'anolis23-loong64':
                    from linux_acceptance import validate_evidence
                    build = json.loads((output / 'build-manifest.json').read_text())
                    hashes = {a['path']: hashlib.sha256((output / a['path']).read_bytes()).hexdigest() for a in build['artifacts']}
                    validate_evidence(evidence, target, 'loong64', build['productVersion'], build['commit'], hashes)
                    receipt = ROOT / 'dist/linux-compat' / f'{target}-loong64.json'
                    receipt.parent.mkdir(parents=True, exist_ok=True)
                    receipt.write_text(json.dumps(evidence, indent=2) + '\n')
            finally:
                if results.exists():
                    # Preserve a failed guest's diagnostic archive too.
                    shutil.copy2(results, output / 'guest-results.tar.gz')
        finally:
            if mounted:
                subprocess.run(['sudo', 'umount', str(mount)], check=False)
            subprocess.run(['docker', 'rm', '-f', kernel_container], check=False)


if __name__ == '__main__':
    main()
