#!/usr/bin/env python3
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import release_assets as api


class ReleaseAssetsTest(unittest.TestCase):
    def test_find_tagless_draft_through_paginated_list(self):
        calls = []
        def command(*args):
            calls.append(args)
            return json.dumps([[], [{'id': 42, 'tag_name': 'v1.1.4', 'draft': True}]])
        self.assertEqual(api.find_release('owner/repo', 'v1.1.4', command)['id'], 42)
        self.assertIn('--paginate', calls[0])
        self.assertNotIn('/tags/', ' '.join(calls[0]))

    def test_list_errors_do_not_become_not_found(self):
        with self.assertRaises(subprocess.CalledProcessError):
            api.find_release('owner/repo', 'v1.1.4', lambda *args: (_ for _ in ()).throw(subprocess.CalledProcessError(1, args)))

    def test_upload_uses_id_and_encoded_name_and_raw_file(self):
        calls = []
        api.upload_asset('owner/repo', 42, 'file name+1.apk', '/tmp/payload.apk', lambda *args: calls.append(args))
        self.assertEqual(calls[0][2], 'https://uploads.github.com/repos/owner/repo/releases/42/assets?name=file%20name%2B1.apk')
        self.assertEqual(calls[0][-2:], ('--input', '/tmp/payload.apk'))

    def test_draft_delete_rechecks_after_lost_response_and_bounds_retries(self):
        for disappeared, succeeds, expected in [(True, False, 1), (False, True, 2), (False, False, 3)]:
            deletes = []
            def command(*args):
                if 'DELETE' in args:
                    deletes.append(args)
                    if succeeds and len(deletes) == 2: return ''
                    raise subprocess.CalledProcessError(1, args)
                return json.dumps([[] if disappeared else [{'id': 7}]])
            with self.subTest(disappeared=disappeared, succeeds=succeeds), patch.object(api.time, 'sleep'):
                if not disappeared and not succeeds:
                    with self.assertRaises(subprocess.CalledProcessError):
                        api.delete_draft_asset('owner/repo', 42, 7, command)
                else:
                    api.delete_draft_asset('owner/repo', 42, 7, command)
                self.assertEqual(len(deletes), expected)

    def test_download_binary_uses_asset_id_and_preserves_bytes(self):
        def command(args, stdout, check):
            self.assertTrue(check)
            self.assertEqual(args[2], 'repos/owner/repo/releases/assets/123')
            self.assertIn('Accept: application/octet-stream', args)
            stdout.write(b'\x00\xff\r\n')
        with tempfile.TemporaryDirectory() as temporary, patch.object(api.subprocess, 'run', command):
            api.download_asset('owner/repo', {'id': 123, 'name': 'app.apk'}, temporary)
            self.assertEqual((Path(temporary) / 'app.apk').read_bytes(), b'\x00\xff\r\n')
            with self.assertRaises(FileExistsError):
                api.download_asset('owner/repo', {'id': 123, 'name': 'app.apk'}, temporary)

    def test_download_rejects_remote_path_traversal(self):
        for name in ('../bad', '/tmp/bad', 'a\\bad', '..', ''):
            with self.subTest(name=name), self.assertRaises(ValueError):
                api.download_asset('owner/repo', {'id': 1, 'name': name}, '/tmp')

    def test_named_manifest_download_does_not_require_tag_lookup(self):
        release = {'id': 42, 'tag_name': 'v1.1.4', 'draft': True}
        with tempfile.TemporaryDirectory() as temporary, \
                patch.object(api, 'find_release', return_value=release), \
                patch.object(api, 'assets', return_value=[{'id': 7, 'name': 'release.json'}, {'id': 8, 'name': 'app.apk'}]) as assets, \
                patch.object(api, 'download_asset') as download:
            api.download_release('owner/repo', 'v1.1.4', temporary, name='release.json')
            assets.assert_called_once_with('owner/repo', 42)
            download.assert_called_once_with('owner/repo', {'id': 7, 'name': 'release.json'}, temporary)


if __name__ == '__main__':
    unittest.main()
