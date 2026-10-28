#!/usr/bin/env python3
"""Failure-path checks for the experiment's evidence and exit-code handling."""
import hashlib
import importlib.util
import json
import io
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('validation', Path(__file__).with_name('validate.py'))
validation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validation)


class EvidenceTests(unittest.TestCase):
    def test_cached_runtime_is_saved_before_candidate_state_and_reused(self):
        with tempfile.TemporaryDirectory() as temporary:
            calls = []
            class Report:
                def check(self, name, command, **kwargs):
                    calls.append((name, command))
                    return True
            def docker(command, **kwargs):
                calls.append(('docker', command))
                if command[:2] == ['docker', 'save']:
                    Path(command[3]).write_bytes(b'provisioned environment')
            with patch.object(validation.Path, 'home', return_value=Path(temporary)), patch.object(validation.subprocess, 'run', side_effect=docker):
                self.assertTrue(validation.start_container(Report(), 'runtime', runtime=True))
                archives = list(Path(temporary).rglob('runtime-*.tar'))
                self.assertEqual(len(archives), 1)
                self.assertTrue(any('RUNTIME_ONLY=1' in cmd for _, cmd in calls))
                self.assertFalse(any('docker' in cmd and 'cp' in cmd for _, cmd in calls))
                calls.clear()
                self.assertTrue(validation.start_container(Report(), 'runtime', runtime=True))
                self.assertTrue(any(cmd[:2] == ['docker', 'load'] for _, cmd in calls))
                self.assertFalse(any('RUNTIME_ONLY=1' in cmd for _, cmd in calls))
                self.assertTrue(any('runtime-provision' == name for name, _ in calls))

    def test_container_output_can_be_replaced_without_writing_old_inode(self):
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / 'candidate'
            source.write_bytes(b'new executable')
            source.chmod(0o755)
            destination = Path(temporary) / 'fncpn-ui'
            destination.write_bytes(b'old container output')
            destination.chmod(0o444)
            validation.replace_binary(source, destination)
            self.assertEqual(destination.read_bytes(), b'new executable')
            self.assertEqual(destination.stat().st_mode & 0o777, 0o755)
            self.assertEqual(source.read_bytes(), b'new executable')
            self.assertEqual(sorted(p.name for p in Path(temporary).iterdir()), ['candidate', 'fncpn-ui'])

    def test_guest_evidence_requires_a_success_marker(self):
        for marker in (None, b'1\n', b'0\n'):
            with self.subTest(marker=marker), tempfile.TemporaryDirectory() as temporary:
                archive = Path(temporary) / 'results.tar.gz'
                with tarfile.open(archive, 'w:gz') as stream:
                    if marker is not None:
                        member = tarfile.TarInfo('exit-code')
                        member.size = len(marker)
                        stream.addfile(member, io.BytesIO(marker))
                destination = Path(temporary) / 'system'
                if marker == b'0\n':
                    validation.read_guest_results(archive, destination, 0)
                    with self.assertRaises(RuntimeError):
                        validation.read_guest_results(archive, destination, 1)
                else:
                    with self.assertRaises(RuntimeError):
                        validation.read_guest_results(archive, destination, 0)

    def test_truncated_guest_evidence_fails(self):
        with tempfile.TemporaryDirectory() as temporary:
            archive = Path(temporary) / 'results.tar.gz'
            archive.write_bytes(b'partial results')
            with self.assertRaises(tarfile.ReadError):
                validation.read_guest_results(archive, Path(temporary) / 'system', 0)

    def test_incomplete_run_cannot_report_success(self):
        with tempfile.TemporaryDirectory() as temporary:
            report = validation.Report(temporary, required=['build', 'runtime'])
            self.assertTrue(report.check('build', [sys.executable, '-c', 'pass']))
            self.assertFalse(report.data['passed'])
            self.assertEqual(report.data['checks']['runtime']['status'], 'pending')

    def test_failure_is_preserved_after_independent_success(self):
        with tempfile.TemporaryDirectory() as temporary:
            report = validation.Report(temporary)
            self.assertFalse(report.check('bad', [sys.executable, '-c', 'raise SystemExit(7)']))
            self.assertTrue(report.check('good', [sys.executable, '-c', 'print("ok")']))
            data = json.loads((Path(temporary) / 'report.json').read_text())
            self.assertFalse(data['passed'])
            self.assertEqual(data['checks']['bad']['exitCode'], 7)

    def test_timeout_does_not_pass(self):
        with tempfile.TemporaryDirectory() as temporary:
            report = validation.Report(temporary)
            self.assertFalse(report.check('timeout', [sys.executable, '-c', 'import time; time.sleep(10)'], timeout=0.05))
            self.assertIn('timeout', report.data['checks']['timeout']['error'])
            self.assertFalse(report.data['passed'])

    def test_download_rejects_wrong_digest_without_replacing_file(self):
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / 'source'
            source.write_bytes(b'firmware fixture')
            destination = Path(temporary) / 'firmware'
            destination.write_bytes(b'previous verified copy')
            with self.assertRaises(ValueError):
                validation.download(source.as_uri(), destination, '0' * 64)
            self.assertEqual(destination.read_bytes(), b'previous verified copy')
            self.assertFalse(destination.with_suffix('.partial').exists())
            validation.download(source.as_uri(), destination, hashlib.sha256(source.read_bytes()).hexdigest())
            self.assertEqual(destination.read_bytes(), source.read_bytes())


if __name__ == '__main__':
    unittest.main()
