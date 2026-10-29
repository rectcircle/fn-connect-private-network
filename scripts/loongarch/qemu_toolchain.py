"""Pinned system emulator for LoongArch kernel acceptance (QEMU issue 4209)."""
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
PIN = json.loads((Path(__file__).with_name('environment.json')).read_text())['qemu']


def prefix():
    return Path.home() / '.cache/fncpn-qemu' / PIN['version']


def require_emulator(executable='qemu-system-loongarch64'):
    version = subprocess.check_output([str(executable), '--version'], text=True).splitlines()[0]
    if not re.search(r'\bversion ' + re.escape(PIN['version']) + r'(?:\s|$)', version):
        raise RuntimeError('LoongArch acceptance requires pinned QEMU ' + PIN['version'] +
                           ' with the upstream FCC/FPE fix (issue 4209); run qemu_toolchain.py install')
    return str(executable)


def verify_source(directory):
    code = (directory / 'target/loongarch/tcg/insn_trans/trans_branch.c.inc').read_text()
    body = re.search(r'static bool gen_cz_bc\([^}]+\}', code)
    if not body or 'CHECK_FPE;' not in body[0]:
        raise RuntimeError('QEMU source lacks the issue 4209 floating-point context fix')


def extract_source(archive, destination):
    def source_filter(member, path):
        # The official tarball includes EDK2's absolute X11 include symlink.
        # We use separately pinned firmware, so this host-specific link is unused.
        if member.issym() and Path(member.linkname).is_absolute():
            return None
        return tarfile.data_filter(member, path)
    with tarfile.open(archive) as source:
        source.extractall(destination, filter=source_filter)


def install():
    if sys.platform != 'linux':
        raise RuntimeError('Build this acceptance emulator on a Linux host')
    target = prefix()
    binary = target / 'bin/qemu-system-loongarch64'
    receipt = target / 'build.json'
    if (binary.is_file() and receipt.is_file() and json.loads(receipt.read_text()).get('source') == PIN
            and json.loads(receipt.read_text()).get('binarySHA256') == hashlib.sha256(binary.read_bytes()).hexdigest()):
        require_emulator(binary)
        return
    with tempfile.TemporaryDirectory(prefix='fncpn-qemu-') as temporary:
        work = Path(temporary)
        archive = work / 'source.tar.xz'
        with urllib.request.urlopen(PIN['url'], timeout=120) as source, archive.open('wb') as output:
            shutil.copyfileobj(source, output)
        if hashlib.sha256(archive.read_bytes()).hexdigest() != PIN['sha256']:
            raise ValueError('QEMU source SHA256 mismatch')
        extract_source(archive, work / 'source')
        source = work / 'source' / ('qemu-' + PIN['version'])
        verify_source(source)
        build = work / 'build'
        build.mkdir()
        subprocess.run([str(source / 'configure'), '--prefix=' + str(target),
                        '--target-list=loongarch64-softmmu', '--without-default-features', '--disable-download',
                        '--disable-docs', '--disable-guest-agent', '--disable-tools',
                        '--disable-werror', '--disable-rust', '--disable-gtk', '--disable-sdl', '--disable-vnc',
                        '--enable-tcg', '--enable-pixman', '--enable-fdt=system'], cwd=build, check=True)
        subprocess.run(['ninja', '-j2', 'install'], cwd=build, check=True)
        require_emulator(binary)
        receipt.write_text(json.dumps({'source': PIN, 'binarySHA256':
                          hashlib.sha256(binary.read_bytes()).hexdigest()}, indent=2) + '\n')


if __name__ == '__main__':
    if sys.argv[1:] == ['install']:
        install()
    elif sys.argv[1:] == ['bin']:
        print(prefix() / 'bin')
    else:
        raise SystemExit('Usage: qemu_toolchain.py install|bin')
