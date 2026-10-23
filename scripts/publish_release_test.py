#!/usr/bin/env python3
import hashlib
import importlib.util
import json
import os
import pathlib
import tempfile
import unittest
from linux_support import release_packages
from rpm_signing import signing_identity
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('publisher', pathlib.Path(__file__).with_name('publish-release.py'))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)


class PublicationTest(unittest.TestCase):
    def exercise(self, draft, corrupt=False, publish=True, version="1.0.0", flattened=False, missing_architecture=None, include_linux=False, candidate_target=None, tagged=None):
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
                sources = {}
                artifacts = []
                for architecture in ('arm64', 'x64'):
                    if architecture == missing_architecture:
                        continue
                    name = f'fncpn-{version}-windows-{architecture}-unsigned.msi'
                    source = out / name if flattened else out / 'windows' / architecture / name
                    source.parent.mkdir(parents=True, exist_ok=True)
                    source.write_bytes(architecture.encode())
                    sha = hashlib.sha256(architecture.encode()).hexdigest()
                    sources[name] = source
                    artifacts.append({'file': name, 'path': f'windows/{architecture}/{name}', 'sha256': sha})
                if include_linux:
                    for target,architecture,component,name in release_packages(version):
                        source=out/'linux'/target/architecture/name
                        source.parent.mkdir(parents=True,exist_ok=True)
                        content=name.encode();source.write_bytes(content)
                        sources[name]=source
                        artifacts.append({'file':name,'path':source.relative_to(out).as_posix(),'sha256':hashlib.sha256(content).hexdigest()})
                (out / f'release-{version}.json').write_text(json.dumps({'version': version, 'commit': 'a' * 40, 'artifacts': artifacts, 'build': {'rpmSigning': signing_identity()} if include_linux else {}}))
                (out / f'SHA256SUMS-{version}').write_text(''.join(f"{a['sha256']}  {a['file']}\n" for a in artifacts))
                calls = []
                tags = ([tagged] if tagged else (['a' * 40] if draft is False else []))

                def run(*args):
                    calls.append(args)
                    if args[:3] == ('gh', 'release', 'create'):
                        notes_path = pathlib.Path(args[args.index('--notes-file') + 1])
                        self.assertEqual(notes_path.read_text(), 'Current release changes.\n')
                        self.assertIn('--draft', args)
                        self.assertNotIn('--prerelease', args)
                    if args[:3] == ('gh', 'release', 'edit'):
                        if '--notes-file' in args:
                            notes_path = pathlib.Path(args[args.index('--notes-file') + 1])
                            self.assertEqual(notes_path.read_text(encoding='utf-8'), 'Current release changes.\n')
                            self.assertNotIn('--draft=false', args)
                        else:
                            self.assertIn('--draft=false', args)
                            self.assertIn('--latest', args)
                            self.assertNotIn('--latest=false', args)
                    if args[:2] == ('git', 'status'):
                        return ''
                    if args[0] == 'git':
                        return 'a' * 40
                    if args[:2] == ('gh', 'api'):
                        endpoint = args[2]
                        if 'matching-refs' in endpoint:
                            return json.dumps([{'ref': 'refs/tags/v' + version, 'object': {'type': 'commit', 'sha': sha}} for sha in tags])
                        if '--input' in args:
                            payload = json.loads(pathlib.Path(args[args.index('--input') + 1]).read_text())
                            if endpoint.endswith('/git/refs'):
                                self.assertTrue(any(c[:3] == ('gh', 'release', 'download') for c in calls))
                                self.assertFalse(corrupt)
                                tags.append(payload['sha'])
                            else:
                                self.assertEqual(payload.get('target_commitish'), 'a' * 40)
                            return json.dumps({'draft': True, 'assets': [], 'id': 1})
                        return json.dumps([[] if draft is None else [{'id': 1, 'target_commitish': candidate_target or 'a' * 40, 'tag_name': 'v' + version, 'draft': draft, 'assets': [{'name': f'FnCPN-{version}-arm64-unsigned.msi'}]}]])
                    if args[:3] == ('gh', 'release', 'view') and 'isDraft,assets' in args:
                        return json.dumps({'isDraft': True, 'assets': []})
                    if args[:3] == ('gh', 'release', 'download'):
                        target = pathlib.Path(args[args.index('--dir') + 1])
                        for name, source in sources.items():
                            (target / name).write_bytes(source.read_bytes())
                        for p in (out / f'release-{version}.json', out / f'SHA256SUMS-{version}'):
                            (target / p.name).write_bytes(p.read_bytes())
                        if corrupt:
                            (target / f'fncpn-{version}-windows-arm64-unsigned.msi').write_bytes(b'corrupt')
                    return 'https://example.invalid/release'

                with patch.object(publisher, 'run', run):
                    if corrupt:
                        with self.assertRaises(SystemExit):
                            publisher.publish(out, 'owner/repo', publish)
                    else:
                        publisher.publish(out, 'owner/repo', publish)
                deletions = [c for c in calls if c[:3] == ('gh', 'release', 'delete-asset')]
                self.assertEqual(len(deletions), 1 if draft is True and not publish else 0)
                uploads = [c for c in calls if c[:3] == ('gh', 'release', 'upload')]
                if uploads:
                    self.assertTrue(all(str(source.resolve()) in uploads[0] for source in sources.values()))
                mutations = [c[2] for c in calls if c[:2] == ('gh', 'release') and c[2] in ('upload', 'edit', 'create')]
                if not publish or corrupt:
                    self.assertEqual(tags, ['a' * 40] if draft is False else [])
                elif draft is True:
                    self.assertEqual(tags, ['a' * 40])
                return mutations
            finally:
                os.chdir(previous)

    def test_draft_rejects_existing_tag(self):
        with self.assertRaisesRegex(SystemExit, 'absent version tag'):
            self.exercise(True, publish=False, tagged='a' * 40)

    def test_publication_rejects_another_candidate(self):
        with self.assertRaisesRegex(SystemExit, 'candidate commit'):
            self.exercise(True, candidate_target='b' * 40)

    def test_publication_rejects_conflicting_tag(self):
        with self.assertRaisesRegex(SystemExit, 'tag does not match'):
            self.exercise(True, tagged='b' * 40)

    def test_notes_select_requested_version_without_history(self):
        with patch.object(publisher.pathlib.Path, 'read_text', return_value='## [Unreleased]\nFuture.\n## [1.0.0] - 2026-10-09\nRelease changes.\n## [0.9.0]\nHistory.\n'):
            self.assertEqual(publisher.release_notes('1.0.0'), 'Release changes.\n')

    def test_empty_notes_are_rejected(self):
        with patch.object(publisher.pathlib.Path, 'read_text', return_value='## [Unreleased]\n\n'):
            with self.assertRaisesRegex(SystemExit, 'Missing release notes'):
                publisher.release_notes('1.0.0')

    def test_first_release_is_created_as_draft(self):
        self.assertEqual(self.exercise(None, publish=False), ['upload'])

    def test_publication_requires_existing_draft(self):
        with self.assertRaisesRegex(SystemExit, 'existing release draft'):
            self.exercise(None)

    def test_publication_never_uploads_or_deletes_assets(self):
        self.assertEqual(self.exercise(True), ['edit'])

    def test_corrupt_remote_stays_unpublished_without_asset_mutation(self):
        self.assertEqual(self.exercise(True, corrupt=True), [])

    def test_public_release_is_verified_without_mutation(self):
        self.assertEqual(self.exercise(False), [])
        self.assertEqual(self.exercise(False, corrupt=True), [])

    def test_default_keeps_draft(self):
        self.assertEqual(self.exercise(True, publish=False), ['upload'])

    def test_downloaded_release_assets_can_be_verified_from_flat_layout(self):
        self.assertEqual(self.exercise(True, publish=False, flattened=True), ['upload'])

    def test_complete_linux_release_stays_a_draft(self):
        self.assertEqual(self.exercise(None,publish=False,version='1.1.2',include_linux=True),['upload'])

    def test_linux_release_requires_all_targets_and_components(self):
        with self.assertRaisesRegex(SystemExit, 'Linux'):
            self.exercise(None, publish=False, version='1.1.2')

    def test_inventory_missing_windows_architecture_is_rejected(self):
        with self.assertRaisesRegex(SystemExit, 'missing required Windows MSI assets'):
            self.exercise(None, missing_architecture='x64')

    def test_artifact_path_cannot_escape_inventory_directory(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            previous = pathlib.Path.cwd()
            os.chdir(root)
            try:
                pathlib.Path('internal/version').mkdir(parents=True)
                pathlib.Path('internal/version/VERSION').write_text('1.0.0\n')
                out = root / 'dist'
                out.mkdir()
                artifacts = []
                for architecture in ('arm64', 'x64'):
                    name = f'fncpn-1.0.0-windows-{architecture}-unsigned.msi'
                    payload = root / name
                    payload.write_bytes(architecture.encode())
                    sha = hashlib.sha256(architecture.encode()).hexdigest()
                    path = '../outside.msi' if architecture == 'arm64' else f'windows/x64/{name}'
                    artifacts.append({'file': name, 'path': path, 'sha256': sha})
                (out / 'release-1.0.0.json').write_text(json.dumps({'version': '1.0.0', 'commit': 'a' * 40, 'artifacts': artifacts}))
                def run(*args):
                    if args[:2] == ('git', 'status'):
                        return ''
                    return 'a' * 40
                with patch.object(publisher, 'run', run):
                    with self.assertRaisesRegex(SystemExit, 'Invalid release artifact path'):
                        publisher.publish(out, 'owner/repo')
            finally:
                os.chdir(previous)


if __name__ == '__main__':
    unittest.main()
