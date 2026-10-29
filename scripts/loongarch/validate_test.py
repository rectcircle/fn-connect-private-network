#!/usr/bin/env python3
"""Failure-path checks for the experiment's evidence and exit-code handling."""
import hashlib
import importlib.util
import json
import io
import os
import subprocess
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
    def test_dnf_cache_is_copied_into_a_fresh_runtime_not_an_installed_rootfs(self):
        spec = importlib.util.spec_from_file_location('prepare', Path(__file__).with_name('prepare-compat.py'))
        prepare = importlib.util.module_from_spec(spec); spec.loader.exec_module(prepare)
        with tempfile.TemporaryDirectory() as temporary:
            cache = Path(temporary)/'.cache/fncpn-loongarch-runtime/openeuler-loong64/dnf'
            cache.mkdir(parents=True); (cache/'dependency.rpm').write_bytes(b'cached')
            with patch.object(prepare.Path, 'home', return_value=Path(temporary)), patch.dict(os.environ, {'TARGET': 'openeuler-loong64'}), patch.object(prepare, 'download'), patch.object(prepare, 'run') as command:
                prepare.main()
            calls = [call.args for call in command.call_args_list]
            create = next(i for i, args in enumerate(calls) if args[:3] == ('docker', 'run', '-d'))
            copy = next(i for i, args in enumerate(calls) if args[:2] == ('docker', 'cp'))
            self.assertLess(create, copy)
            self.assertEqual(calls[copy], ('docker', 'cp', str(cache)+'/.', 'fncpn-format:/var/cache/dnf'))

    def test_vm_entrypoint_imports_release_validator_in_isolated_python(self):
        directory = Path(__file__).resolve().parent
        code = 'import runpy,sys;sys.path.insert(0,sys.argv[1]);runpy.run_path(sys.argv[2],run_name="import_check")'
        subprocess.run([sys.executable, '-I', '-c', code, str(directory), str(directory/'format-vm.py')], check=True)

    def test_netfilter_backend_selection_is_scoped_to_test_path(self):
        spec = importlib.util.spec_from_file_location('runtime', Path(__file__).with_name('runtime-format.py'))
        runtime = importlib.util.module_from_spec(spec); spec.loader.exec_module(runtime)
        with tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, {'PATH': '/usr/bin'}):
            directory = Path(temporary)/'bin'
            with patch.object(runtime, 'run', side_effect=['iptables (legacy)', 'iptables (nf_tables)']), patch.object(runtime.shutil, 'which', return_value='/usr/sbin/iptables-nft'):
                runtime.configure_netfilter_test(directory)
            self.assertEqual((directory/'iptables').readlink(), Path('/usr/sbin/iptables-nft'))
            self.assertEqual(os.environ['PATH'], str(directory)+':/usr/bin')
        with patch.object(runtime, 'run', return_value='iptables (legacy)'), patch.object(runtime.shutil, 'which', return_value=None):
            with self.assertRaisesRegex(ValueError, 'iptables-nft'): runtime.configure_netfilter_test()
        with patch.object(runtime, 'run', return_value='iptables (nf_tables)'), patch.object(runtime.shutil, 'which') as lookup:
            runtime.configure_netfilter_test()
            lookup.assert_not_called()

    def test_upgrade_fixture_is_lower_and_rejects_tamper_and_path_traversal(self):
        def load(name):
            spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + '.py'))
            module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
            return module
        fixture = load('upgrade-fixture')
        runtime = load('runtime-format')
        for current, lower in [('1.1.8', '1.1.7'), ('1.2.0', '1.1.0'), ('2.0.0', '1.0.0')]:
            self.assertEqual(fixture.fixture_version(current), lower)
        with self.assertRaises(ValueError): fixture.fixture_version('0.0.0')
        with tempfile.TemporaryDirectory() as temporary:
            out = Path(temporary); (out/'upgrade-fixture').mkdir()
            metadata = {'kind': 'candidate-code-lower-package-version', 'productVersion': '1.1.8',
                        'metadataVersion': '1.1.7', 'commit': 'candidate', 'artifacts': []}
            for name in ('fncpn-core.rpm', 'fncpn-ui.rpm'):
                (out/'upgrade-fixture'/name).write_bytes(name.encode())
                metadata['artifacts'].append({'path': name, 'sha256': hashlib.sha256(name.encode()).hexdigest()})
            build = {'commit': 'candidate', 'productVersion': '1.1.8', 'upgradeFixture': metadata}
            receipt = out/'upgrade-fixture/fixture.json'
            receipt.write_text(json.dumps(metadata))
            self.assertEqual(len(runtime.fixture_packages(out, build)[0]), 2)
            (out/'upgrade-fixture/fncpn-core.rpm').write_bytes(b'tampered')
            with self.assertRaisesRegex(ValueError, 'checksum'): runtime.fixture_packages(out, build)
            metadata['artifacts'][0]['path'] = '../outside.rpm'
            receipt.write_text(json.dumps(metadata))
            with self.assertRaisesRegex(ValueError, 'path'): runtime.fixture_packages(out, build)

    def test_shared_acceptance_tests_require_candidate_hashes_and_restore_execute_mode(self):
        spec = importlib.util.spec_from_file_location('acceptance', Path(__file__).with_name('prepare-acceptance.py'))
        acceptance = importlib.util.module_from_spec(spec); spec.loader.exec_module(acceptance)
        with tempfile.TemporaryDirectory() as temporary:
            out = Path(temporary); (out/'tests').mkdir()
            manifest = []
            for index, package in enumerate(('internal/client', 'internal/privileged', 'internal/platform/linux')):
                name = str(index)+'.test'; (out/'tests'/name).write_bytes(package.encode())
                manifest.append({'package': 'project/'+package, 'binary': name})
            (out/'tests/manifest.json').write_text(json.dumps(manifest))
            (out/'probe').write_bytes(b'probe')
            files = [out/'probe', *sorted((out/'tests').iterdir())]
            record = {'commit': 'candidate', 'files': {str(p.relative_to(out)): hashlib.sha256(p.read_bytes()).hexdigest() for p in files}}
            (out/'acceptance-tests.json').write_text(json.dumps(record))
            with patch.object(acceptance, 'OUT', out):
                acceptance.verify_tests('candidate')
                self.assertEqual((out/'probe').stat().st_mode & 0o777, 0o755)
                with self.assertRaisesRegex(ValueError, 'commit'): acceptance.verify_tests('foreign')
                (out/'probe').write_bytes(b'tampered')
                with self.assertRaisesRegex(ValueError, 'checksum'): acceptance.verify_tests('candidate')

    def test_root_regression_receipt_requires_matching_success_and_commit(self):
        with tempfile.TemporaryDirectory() as temporary:
            cache = Path(temporary)
            receipt = cache / 'root-tests/fingerprint.json'
            receipt.parent.mkdir()
            self.assertFalse(validation.root_regression_plan(cache, 'fingerprint')['reused'])
            for contents in ('broken JSON', '[]', json.dumps({'passed': False}),
                             json.dumps({'passed': True, 'fingerprint': 'foreign', 'commit': 'a' * 40}),
                             json.dumps({'passed': True, 'fingerprint': 'fingerprint', 'commit': 'invalid'})):
                receipt.write_text(contents)
                self.assertFalse(validation.root_regression_plan(cache, 'fingerprint')['reused'])
            receipt.write_text(json.dumps({'passed': True, 'fingerprint': 'fingerprint', 'commit': 'a' * 40}))
            self.assertTrue(validation.root_regression_plan(cache, 'fingerprint')['reused'])
            self.assertFalse(validation.root_regression_plan(cache, 'fingerprint', force=True)['reused'])

    def test_root_scope_keeps_embedded_and_unknown_inputs(self):
        paths = ['mobile/view.go', 'ui/main.go', 'docs/development.md', 'README.md',
                 'internal/server/web/index.html', 'internal/client/client.go', 'go.sum',
                 'scripts/loongarch/validate.py', 'new-build-input', 'docs/fixture.go']
        self.assertEqual(validation.root_regression_paths(paths), sorted(paths[4:]))

    def test_stage_resume_rejects_another_candidate(self):
        with tempfile.TemporaryDirectory() as temporary:
            out = Path(temporary)
            (out / 'report.json').write_text(json.dumps({'commit': 'old'}))
            with patch.object(validation, 'OUT', out), patch.object(validation.subprocess, 'check_output', return_value='new'):
                with self.assertRaisesRegex(RuntimeError, 'commit differs'):
                    validation.load_report()

    def test_cached_runtime_is_saved_before_candidate_state_and_reused(self):
        with tempfile.TemporaryDirectory() as temporary:
            calls = []
            class Report:
                data = {}
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
