#!/usr/bin/env python3
"""Failure-path checks for the experiment's evidence and exit-code handling."""
import hashlib
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('validation', Path(__file__).with_name('validate.py'))
validation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validation)


class EvidenceTests(unittest.TestCase):
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
