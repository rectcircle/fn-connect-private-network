#!/usr/bin/env python3
import hashlib
import importlib.util
import json
import os
import pathlib
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('publisher', pathlib.Path(__file__).with_name('publish-release.py'))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)


class PublicationTest(unittest.TestCase):
    def exercise(self, draft, corrupt=False, publish=True, version="1.0.0-rc.1"):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            previous = pathlib.Path.cwd()
            os.chdir(root)
            try:
                pathlib.Path('CHANGELOG.md').write_text('## [Unreleased]\n\nCurrent release changes.\n\n## [0.9.0] - 2026-01-01\n\nOlder changes.\n')
                pathlib.Path('internal/version').mkdir(parents=True)
                pathlib.Path('internal/version/VERSION').write_text(version + '\n')
                out = root / 'dist'
                out.mkdir()
                (out / 'candidate.pkg').write_bytes(b'candidate')
                sha = hashlib.sha256(b'candidate').hexdigest()
                (out / f'release-{version}.json').write_text(json.dumps({'version': version, 'commit': 'commit', 'artifacts': [{'file': 'candidate.pkg', 'sha256': sha}]}))
                (out / f'SHA256SUMS-{version}').write_text(f'{sha}  candidate.pkg\n')
                calls = []

                def run(*args):
                    calls.append(args)
                    if args[:3] == ('gh', 'release', 'create'):
                        notes_path = pathlib.Path(args[args.index('--notes-file') + 1])
                        self.assertEqual(notes_path.read_text(), 'Current release changes.\n')
                        self.assertIn('--draft', args)
                        self.assertEqual('--prerelease' in args, '-rc.' in version)
                    if args[:3] == ('gh', 'release', 'edit'):
                        self.assertIn('--draft=false', args)
                        self.assertIn('--latest=false' if '-rc.' in version else '--latest', args)
                        self.assertNotIn('--latest' if '-rc.' in version else '--latest=false', args)
                    if args[:2] == ('git', 'status'):
                        return ''
                    if args[0] == 'git':
                        return 'commit'
                    if args[:2] == ('gh', 'api') and '/git/ref/' in args[-1]:
                        return json.dumps({'object': {'type': 'commit', 'sha': 'commit'}})
                    if args[:2] == ('gh', 'api'):
                        return json.dumps([[] if draft is None else [{'tag_name': 'v' + version, 'draft': draft}]])
                    if args[:3] == ('gh', 'release', 'view') and 'isDraft,assets' in args:
                        return json.dumps({'isDraft': True, 'assets': []})
                    if args[:3] == ('gh', 'release', 'download'):
                        target = pathlib.Path(args[args.index('--dir') + 1])
                        for p in out.iterdir():
                            (target / p.name).write_bytes(p.read_bytes())
                        if corrupt:
                            (target / 'candidate.pkg').write_bytes(b'corrupt')
                    return 'https://example.invalid/release'

                with patch.object(publisher, 'run', run):
                    if corrupt:
                        with self.assertRaises(SystemExit):
                            publisher.publish(out, 'owner/repo', publish)
                    else:
                        publisher.publish(out, 'owner/repo', publish)
                mutations = [c[2] for c in calls if c[:2] == ('gh', 'release') and c[2] in ('upload', 'edit', 'create')]
                return mutations
            finally:
                os.chdir(previous)

    def test_notes_select_requested_version_without_history(self):
        with patch.object(publisher.pathlib.Path, 'read_text', return_value='## [Unreleased]\nFuture.\n## [1.0.0] - 2026-10-09\nRelease changes.\n## [0.9.0]\nHistory.\n'):
            self.assertEqual(publisher.release_notes('1.0.0'), 'Release changes.\n')

    def test_empty_notes_are_rejected(self):
        with patch.object(publisher.pathlib.Path, 'read_text', return_value='## [Unreleased]\n\n'):
            with self.assertRaisesRegex(SystemExit, 'Missing release notes'):
                publisher.release_notes('1.0.0')

    def test_first_release_is_created_as_prerelease_draft(self):
        self.assertEqual(self.exercise(None), ['create', 'upload', 'edit'])

    def test_first_stable_release_is_a_draft_then_latest(self):
        self.assertEqual(self.exercise(None, version='1.0.0'), ['create', 'upload', 'edit'])

    def test_stable_remote_corruption_prevents_publication(self):
        self.assertEqual(self.exercise(True, corrupt=True, version='1.0.0'), ['upload'])

    def test_draft_can_be_replaced_and_published(self):
        self.assertEqual(self.exercise(True), ['upload', 'edit'])

    def test_corrupt_remote_stays_unpublished(self):
        self.assertEqual(self.exercise(True, corrupt=True), ['upload'])

    def test_public_release_is_verified_without_mutation(self):
        self.assertEqual(self.exercise(False), [])
        self.assertEqual(self.exercise(False, corrupt=True), [])

    def test_default_keeps_draft(self):
        self.assertEqual(self.exercise(True, publish=False), ['upload'])


if __name__ == '__main__':
    unittest.main()
