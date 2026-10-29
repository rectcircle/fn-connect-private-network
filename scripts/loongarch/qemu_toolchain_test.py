import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import qemu_toolchain as qemu


class ToolchainTests(unittest.TestCase):
    def test_known_affected_and_similar_versions_fail_closed(self):
        for version in ('10.2.1', qemu.PIN['version'] + '0'):
            with patch.object(qemu.subprocess, 'check_output', return_value='QEMU emulator version ' + version):
                with self.assertRaisesRegex(RuntimeError, 'issue 4209'):
                    qemu.require_emulator()
        with patch.object(qemu.subprocess, 'check_output', return_value='QEMU emulator version ' + qemu.PIN['version']):
            self.assertEqual(qemu.require_emulator('/fixed/qemu'), '/fixed/qemu')

    def test_source_must_guard_fcc_branch_access(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / 'target/loongarch/tcg/insn_trans/trans_branch.c.inc'
            source.parent.mkdir(parents=True)
            source.write_text('static bool gen_cz_bc(void) { return true; }')
            with self.assertRaisesRegex(RuntimeError, 'issue 4209'):
                qemu.verify_source(root)
            source.write_text('static bool gen_cz_bc(void) { CHECK_FPE; return true; }')
            qemu.verify_source(root)

    def test_untrusted_archive_never_reaches_configure(self):
        with tempfile.TemporaryDirectory() as directory, \
             patch.object(qemu.sys, 'platform', 'linux'), \
             patch.object(qemu, 'prefix', return_value=Path(directory)), \
             patch.object(qemu.urllib.request, 'urlopen', return_value=io.BytesIO(b'tampered')), \
             patch.object(qemu.subprocess, 'run') as run:
            with self.assertRaisesRegex(ValueError, 'SHA256'):
                qemu.install()
            run.assert_not_called()

    def test_cached_build_requires_matching_source_receipt(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / 'bin/qemu-system-loongarch64'
            binary.parent.mkdir()
            binary.write_bytes(b'cached emulator')
            (root / 'build.json').write_text(json.dumps({'source': qemu.PIN,
                'binarySHA256': qemu.hashlib.sha256(binary.read_bytes()).hexdigest()}))
            with patch.object(qemu.sys, 'platform', 'linux'), patch.object(qemu, 'prefix', return_value=root), \
                 patch.object(qemu, 'require_emulator') as verify, patch.object(qemu.urllib.request, 'urlopen') as download:
                qemu.install()
                verify.assert_called_once_with(binary)
                download.assert_not_called()


if __name__ == '__main__':
    unittest.main()
