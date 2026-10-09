#!/usr/bin/env python3
"""Stable-only channel enforcement without contacting GitHub or touching real casks."""
import contextlib
import io
import json
import os
import pathlib
import runpy
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = pathlib.Path(__file__).with_name('update-cask.py').resolve()


class CaskChannelTest(unittest.TestCase):
    def test_rc_rejected_before_network_or_file_changes(self):
        with patch('sys.argv', [str(SCRIPT), '1.1.0-rc.1']), patch('subprocess.check_output') as network:
            with self.assertRaisesRegex(SystemExit, 'stable X.Y.Z'):
                runpy.run_path(str(SCRIPT), run_name='__main__')
            network.assert_not_called()

    def test_draft_or_prerelease_cannot_update_stable_cask(self):
        for draft, prerelease in [(True, False), (False, True)]:
            with self.subTest(draft=draft, prerelease=prerelease):
                with patch('sys.argv', [str(SCRIPT), '1.0.0']), patch('subprocess.check_output', return_value=json.dumps({'isDraft': draft, 'isPrerelease': prerelease})) as network:
                    with self.assertRaisesRegex(SystemExit, 'public stable release'):
                        runpy.run_path(str(SCRIPT), run_name='__main__')
                    self.assertEqual(network.call_count, 1)

    def test_stable_release_generates_only_unified_identifier(self):
        previous = pathlib.Path.cwd()
        with tempfile.TemporaryDirectory() as temporary:
            try:
                os.chdir(temporary)
                replies = [json.dumps({'isDraft': False, 'isPrerelease': False}), json.dumps({'version': '1.0.0', 'artifacts': [{'file': 'FnCPN-1.0.0-arm64-unsigned.pkg', 'sha256': 'a' * 64}]})]
                with patch('sys.argv', [str(SCRIPT), '1.0.0']), patch('subprocess.check_output', side_effect=replies), contextlib.redirect_stdout(io.StringIO()):
                    runpy.run_path(str(SCRIPT), run_name='__main__')
                self.assertEqual([p.name for p in pathlib.Path('Casks').iterdir()], ['fncpn.rb'])
                text = pathlib.Path('Casks/fncpn.rb').read_text()
                self.assertIn('cask "fncpn"', text)
                self.assertIn('version "1.0.0"', text)
                self.assertIn('sha256 "' + 'a' * 64 + '"', text)
                self.assertNotIn('fncpn-rc', text)
            finally:
                os.chdir(previous)


if __name__ == '__main__':
    unittest.main()
