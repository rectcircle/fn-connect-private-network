#!/usr/bin/env python3
"""Exercise mirror recovery, immutable published bytes and credential transport without network."""
import copy
import hashlib
import importlib.util
import io
import json
import subprocess
import shutil
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import zipfile

spec = importlib.util.spec_from_file_location('quark_release', Path(__file__).with_name('quark-release.py'))
mirror = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mirror)


def auth():
    return {'deviceId': 'device', 'currentUserId': 'user',
            'agent_auth': {'codex': {'userId': 'user', 'login_time': 1}},
            'user': {'accessToken': 'access-secret', 'refreshToken': 'refresh-secret',
                     'clientToken': 'client-secret', 'userId': 'user', 'update_time': 1},
            'other-user': {'accessToken': 'unrelated-secret'}}


class FakeQuark:
    def __init__(self):
        self.dirs = {'public': [], 'private': []}
        self.data = {}
        self.calls = []
        self.corrupt_upload = False
        self.fail_upload_after = None

    def add_folder(self, parent, name):
        fid = str(len(self.dirs))
        self.dirs[fid] = []
        self.dirs[parent].append({'fid': fid, 'filename': name, 'file_type': '0'})
        return fid

    def add_file(self, parent, name, content):
        fid = 'file-' + str(len(self.data))
        self.data[fid] = content
        self.dirs[parent].append({'fid': fid, 'filename': name, 'file_type': '1', 'size': len(content)})

    def folder(self, parent, name, create=False):
        matches = [r for r in self.dirs[parent] if r['filename'] == name]
        return matches[0]['fid'] if matches else self.add_folder(parent, name) if create else None

    def browse(self, fid):
        return copy.deepcopy(self.dirs[fid])

    def call(self, *args):
        self.calls.append(args)
        if args[0] == 'upload':
            count = 0
            for path in args[1:-2]:
                if count == self.fail_upload_after:
                    raise RuntimeError('Network interrupted')
                path = Path(path)
                content = path.read_bytes()
                self.add_file(args[-1], path.name, b'BAD!' if self.corrupt_upload else content)
                count += 1
        elif args[0] == 'download':
            row = next(r for rows in self.dirs.values() for r in rows if r['fid'] == args[2])
            (Path(args[4]) / row['filename']).write_bytes(self.data[args[2]])
        elif args[0] == 'move':
            row = next(r for rows in self.dirs.values() for r in rows if r['fid'] == args[1])
            for rows in self.dirs.values():
                if row in rows:
                    rows.remove(row)
                    break
            self.dirs[args[-1]].append(row)
        else:
            raise AssertionError(args)
        return {}


class MirrorTest(unittest.TestCase):
    def test_failure_reports_operation_and_safe_codes_without_vendor_credentials(self):
        result = subprocess.CompletedProcess(['node'], 1,
            '{"type":"result","code":401,"message":"access-secret https://private/?token=secret"}\n',
            'ECONNRESET refresh-secret')
        with patch.object(mirror.subprocess, 'run', return_value=result):
            with self.assertRaises(RuntimeError) as raised:
                mirror.checked(['node', 'cli', 'upload'], operation='Quark upload')
        message = str(raised.exception)
        self.assertIn('Quark upload', message)
        self.assertIn('code=401', message)
        self.assertIn('ECONNRESET', message)
        for secret in ('access-secret', 'refresh-secret', 'https://', 'token=secret'):
            self.assertNotIn(secret, message)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.output = self.root / 'out'; self.output.mkdir()
        self.sources = {}
        for name in ('installer.pkg', 'release.json', 'SHA256SUMS'):
            path = self.root / name; path.write_bytes(name.encode())
            self.sources[name] = path
        self.q = FakeQuark()

    def sync(self):
        mirror.sync(self.q, 'v1.0.10', self.sources, self.output, 'public', 'private')

    def test_publish_only_after_all_readbacks_and_retry_is_read_only(self):
        self.sync()
        move = next(i for i,c in enumerate(self.q.calls) if c[0] == 'move')
        self.assertEqual(sum(c[0] == 'download' for c in self.q.calls[:move]), 3)
        self.assertIsNotNone(self.q.folder('public', 'v1.0.10'))
        self.q.calls.clear()
        self.sync()
        self.assertTrue(all(c[0] == 'download' for c in self.q.calls))

    def test_interrupted_upload_resumes_without_duplicate_or_overwrite(self):
        self.q.fail_upload_after = 1
        with self.assertRaises(RuntimeError): self.sync()
        self.assertIsNone(self.q.folder('public', 'v1.0.10'))
        self.q.fail_upload_after = None
        self.q.calls.clear()
        self.sync()
        upload = next(c for c in self.q.calls if c[0] == 'upload')
        self.assertEqual(len(upload[1:-2]), 2)

    def test_corrupt_upload_never_reaches_public_folder(self):
        self.q.corrupt_upload = True
        with self.assertRaises(ValueError): self.sync()
        self.assertIsNone(self.q.folder('public', 'v1.0.10'))
        self.assertFalse(any(c[0] == 'move' for c in self.q.calls))

    def test_existing_conflict_stops_before_upload(self):
        parent = self.q.folder('private', 'fncpn-staging', create=True)
        candidate = self.q.folder(parent, 'v1.0.10', create=True)
        self.q.add_file(candidate, 'installer.pkg', b'wrong')
        with self.assertRaises(ValueError): self.sync()
        self.assertFalse(any(c[0] in ('upload', 'move') for c in self.q.calls))

    def test_incomplete_public_version_is_never_repaired_in_place(self):
        self.q.add_folder('public', 'v1.0.10')
        with self.assertRaises(ValueError): self.sync()
        self.assertEqual(self.q.calls, [])

    def test_existing_corrupt_public_version_is_never_overwritten(self):
        final = self.q.add_folder('public', 'v1.0.10')
        for name, path in self.sources.items():
            self.q.add_file(final, name, b'X' * path.stat().st_size)
        with self.assertRaises(ValueError): self.sync()
        self.assertFalse(any(c[0] in ('upload', 'move') for c in self.q.calls))

    def test_duplicate_inventory_rejected(self):
        with self.assertRaises(ValueError):
            mirror.unique_rows([{'filename':'x'}, {'filename':'x'}])


class InventoryTest(unittest.TestCase):
    def exercise(self, tamper=None, version='1.0.10'):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            artifacts = []
            for system, suffix in [('macos', '-unsigned.pkg'), ('windows', '-unsigned.msi'), ('fnos', '.fpk')]:
                for arch in ('arm64', 'x64'):
                    name = f'fncpn-{version}-{system}-{arch}{suffix}'
                    (root/name).write_bytes(name.encode())
                    artifacts.append({'file':name, 'sha256':mirror.digest(root/name)})
            if tuple(map(int, version.split('.'))) >= (1, 1, 2):
                for _, _, _, name in mirror.release_packages(version):
                    (root/name).write_bytes(name.encode())
                    artifacts.append({'file':name,'sha256':mirror.digest(root/name)})
            build = {}
            from android_release import ABIS, certificate_fingerprint
            from ios_release import CHECKS as IOS_CHECKS
            from ios_release_test import fixture as ipa_fixture
            from harmonyos_release_test import fixture as hap_fixture
            for name in mirror.android_packages(version):
                (root/name).write_bytes(name.encode())
                artifacts.append({'file': name, 'sha256': mirror.digest(root/name)})
                build['android'] = {'certificateSha256': certificate_fingerprint(), 'abis': list(ABIS), 'versionCode': 2}
            for name in mirror.ios_packages(version):
                ipa_fixture(root/name, version=version)
                artifacts.append({'file': name, 'sha256': mirror.digest(root/name)})
                build['ios'] = {'productVersion': version, 'commit': 'abc', 'workingTreeDirty': False,
                    'configuration': 'Release', 'signed': False, 'architecture': 'arm64', 'buildNumber': 2,
                    'checks': sorted(IOS_CHECKS)}
            if mirror.harmonyos_packages(version):
                path, build['harmonyos'] = hap_fixture(root, version, 'abc')
                shutil.move(path, root/path.name)
                shutil.rmtree(path.parent)
                artifacts.append({'file': path.name, 'sha256': mirror.digest(root/path.name)})
            (root/f'release-{version}.json').write_text(json.dumps({'version':version, 'commit':'abc', 'channel':'stable','artifacts':artifacts, 'build': build}))
            (root/f'SHA256SUMS-{version}').write_text(''.join(f"{a['sha256']}  {a['file']}\n" for a in artifacts))
            release={'assets':[{'name':p.name,'size':p.stat().st_size,'digest':'sha256:'+mirror.digest(p)} for p in root.iterdir()]}
            if tamper: tamper(root, release)
            return mirror.inventory(root, 'v'+version, release, 'abc')

    def test_valid_inventory(self): self.assertEqual(len(self.exercise()), 8)
    def test_linux_release_has_all_18_assets(self):
        self.assertEqual(len(self.exercise(version='1.1.2')),18)

    def test_linux_inventory_missing_component_duplicate_or_unknown_asset_rejected(self):
        for mode in ('missing','duplicate','extra'):
            def tamper(root, release):
                path=root/'release-1.1.2.json';manifest=json.loads(path.read_text())
                if mode=='missing':manifest['artifacts'].pop()
                elif mode=='duplicate':manifest['artifacts'][-1]=manifest['artifacts'][0]
                else:manifest['artifacts'].append({'file':'unknown.rpm','sha256':'0'*64})
                path.write_text(json.dumps(manifest))
            with self.subTest(mode=mode), self.assertRaisesRegex(ValueError,'platform inventory'):
                self.exercise(tamper,version='1.1.2')

    def test_current_inventory_contains_28_assets_and_all_six_loongarch_packages(self):
        inventory = self.exercise(version='1.1.8')
        self.assertEqual(len(inventory), 28)
        names = [name for name in inventory if '-linux-loong64.' in name]
        self.assertEqual(len(names), 6)
        self.assertEqual({name.rsplit('.', 1)[1] for name in names}, {'deb', 'rpm', 'zst'})
        self.assertEqual(len(self.exercise(version='1.1.7')), 22)

    def test_each_loongarch_package_is_required_unique_and_immutable(self):
        names = [name for _, arch, _, name in mirror.release_packages('1.1.8') if arch == 'loong64']
        for name in names:
            for mode in ('missing', 'duplicate', 'tamper'):
                def tamper(root, release):
                    path = root/'release-1.1.8.json'
                    record = json.loads(path.read_text())
                    if mode == 'missing':
                        record['artifacts'] = [a for a in record['artifacts'] if a['file'] != name]
                    elif mode == 'duplicate':
                        record['artifacts'].append(next(a for a in record['artifacts'] if a['file'] == name))
                    else:
                        (root/name).write_bytes(b'tampered')
                    path.write_text(json.dumps(record))
                with self.subTest(name=name, mode=mode), self.assertRaises(ValueError):
                    self.exercise(tamper, version='1.1.8')

    def test_modified_bytes_rejected(self):
        with self.assertRaises(ValueError):
            self.exercise(lambda root, release:(root/'fncpn-1.0.10-macos-arm64-unsigned.pkg').write_bytes(b'wrong'))
    def test_modified_metadata_digest_rejected(self):
        def tamper(root, release):
            (root/'release-1.0.10.json').write_text((root/'release-1.0.10.json').read_text()+' ')
            for a in release['assets']:
                a['size']=(root/a['name']).stat().st_size
        with self.assertRaises(ValueError): self.exercise(tamper)
    def test_extra_asset_rejected(self):
        with self.assertRaises(ValueError):
            self.exercise(lambda root, release:(root/'private-key').write_text('secret'))
    def test_unknown_tag_input_rejected(self):
        for tag in ('../v1.0.10','v1.0.10-rc.1','v01.0.10','--help'):
            with self.assertRaises(ValueError): mirror.version_from_tag(tag)


class AuthorizationTest(unittest.TestCase):
    def test_export_only_selected_identity_and_private_file_mode(self):
        exported = mirror.minimal_auth(auth())
        self.assertNotIn('other-user', exported)
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp)/'codex/config.json'
            mirror.write_auth(path, exported)
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertEqual(path.parent.stat().st_mode & 0o777, 0o700)

    def test_dispatch_secret_uses_stdin_before_workflow_and_never_input(self):
        calls=[]
        def gh(*args, **kwargs):
            calls.append((args,kwargs))
            return ''
        def download(repo, tag, directory, name=None):
            (Path(directory) / name).write_text(json.dumps({'version': '1.0.10', 'commit': 'a' * 40}))
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);entry=root/'scripts/quark-drive.cjs';entry.parent.mkdir();entry.touch()
            config=root/'codex/config.json';mirror.write_auth(config,auth())
            with patch.object(mirror,'gh',gh),patch.object(mirror,'find_release',return_value={'draft':True,'prerelease':False}),patch.object(mirror,'download_release',download),patch.object(mirror,'checked',return_value=mirror.CLI_VERSION),patch.object(mirror.Quark,'call',return_value={}):
                mirror.dispatch('v1.0.10','owner/repo','publish',entry,config)
        secret = next(c for c in calls if c[0][:2] == ('secret', 'set'))
        self.assertEqual(secret[0][:3],('secret','set','QUARK_AUTH'))
        self.assertEqual(json.loads(secret[1]['input'])['user']['accessToken'],'access-secret')
        self.assertEqual(calls[-1][0][:3],('workflow','run','release.yml'))
        self.assertNotIn('access-secret',str(calls[-1]))

    def test_failed_local_authorization_never_exports_or_dispatches(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            cli = root / 'scripts/quark-drive.cjs'
            cli.parent.mkdir()
            cli.touch()
            config = root / 'codex/config.json'
            mirror.write_auth(config, auth())
            with patch.object(mirror, 'gh') as remote, patch.object(mirror, 'checked', return_value=mirror.CLI_VERSION), patch.object(mirror.Quark, 'call', side_effect=RuntimeError('Expired')):
                with self.assertRaises(RuntimeError):
                    mirror.dispatch('v1.0.10', 'owner/repo', 'publish', cli, config)
                remote.assert_not_called()

    def test_subprocess_errors_never_echo_credentials(self):
        import subprocess
        failure = subprocess.CompletedProcess(['node'], 1, 'access-secret', 'refresh-secret')
        with patch.object(mirror.subprocess, 'run', return_value=failure):
            with self.assertRaises(RuntimeError) as error:
                mirror.checked(['node', 'example'])
        self.assertNotIn('access-secret', str(error.exception))
        self.assertNotIn('refresh-secret', str(error.exception))

    def test_draft_never_mirrored_or_credentials_installed(self):
        with patch.object(mirror,'gh',return_value=json.dumps({'tagName':'v1.0.10','isDraft':True,'isPrerelease':False})),patch.object(mirror,'install_cli') as install:
            with self.assertRaises(ValueError):mirror.mirror('v1.0.10','owner/repo')
            install.assert_not_called()

    def test_cli_only_extracts_runtime_and_checks_checksum(self):
        buffer=io.BytesIO()
        with zipfile.ZipFile(buffer,'w') as z:
            for name in ('scripts/quark-drive.cjs','scripts/hash-worker.cjs','SKILL.md','references/auth.md','scripts/install.sh'):
                z.writestr(name,b'example')
        data=buffer.getvalue()
        with tempfile.TemporaryDirectory() as temp:
            with patch.object(mirror.urllib.request,'urlopen',return_value=io.BytesIO(data)),patch.object(mirror,'CLI_SHA256',hashlib.sha256(data).hexdigest()),patch.object(mirror,'checked',return_value=mirror.CLI_VERSION):
                mirror.install_cli(Path(temp))
            self.assertEqual({p.name for p in Path(temp).rglob('*') if p.is_file()},{'quark-drive.cjs','hash-worker.cjs'})
        with tempfile.TemporaryDirectory() as temp:
            with patch.object(mirror.urllib.request,'urlopen',return_value=io.BytesIO(data)):
                with self.assertRaises(ValueError):mirror.install_cli(Path(temp))


if __name__=='__main__':unittest.main()
