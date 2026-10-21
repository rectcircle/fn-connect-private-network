#!/usr/bin/env python3
"""ABI parsing and failure-path regression tests; no distro installation required."""
import importlib.util
import json
import pathlib
import tempfile
import unittest
from unittest.mock import patch
from linux_abi import version_needs, version_definitions, version_key, inspect

spec = importlib.util.spec_from_file_location('compatibility', pathlib.Path(__file__).with_name('linux-compatibility.py'))
compatibility = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compatibility)

spec_policy = importlib.util.spec_from_file_location('sandbox_policy', pathlib.Path(__file__).with_name('prepare-linux-sandbox.py'))
sandbox_policy = importlib.util.module_from_spec(spec_policy)
spec_policy.loader.exec_module(sandbox_policy)

VERSIONS = '''Version definition section '.gnu.version_d' contains 1 entry:
  0x0010: Rev: 1 Flags: BASE Index: 1 Cnt: 1 Name: executable
Version needs section '.gnu.version_r' contains 3 entries:
  0x0010: Version: 1 File: libc.so.6 Cnt: 2
  0x0020: Name: GLIBC_2.9 Flags: none Version: 2
  0x0030: Name: GLIBC_2.35 Flags: none Version: 3
  0x0040: Version: 1 File: libstdc++.so.6 Cnt: 1
  0x0050: Name: GLIBCXX_3.4.29 Flags: none Version: 4
'''

class CompatibilityTest(unittest.TestCase):
    def test_elf_versions_distinguish_definitions_and_requirements(self):
        self.assertEqual(version_needs(VERSIONS), {'libc.so.6': {'GLIBC_2.9', 'GLIBC_2.35'}, 'libstdc++.so.6': {'GLIBCXX_3.4.29'}})
        self.assertEqual(version_definitions(VERSIONS), {'executable'})
        self.assertGreater(version_key('GLIBC_2.35'), version_key('GLIBC_2.9'))

    def test_core_dynamic_dependency_is_rejected(self):
        with patch('linux_abi.output', side_effect=['INTERP', '(NEEDED) libc.so.6']):
            with self.assertRaisesRegex(ValueError, 'Core unexpectedly'):
                inspect('core', 'ui')

    def test_ui_missing_version_is_rejected_against_actual_provider(self):
        def output(*args):
            if args[:3] == ('readelf', '-W', '-l'):
                return '' if args[-1] == 'core' else '[Requesting program interpreter: /lib/ld.so]'
            if args[:3] == ('readelf', '-W', '-d'):
                return ''
            if args[0] == 'ldd':
                return 'libc.so.6 => /lib/libc.so.6 (0x123)\nlibstdc++.so.6 => /lib/libstdc++.so.6 (0x456)'
            if args[-1] == 'ui':
                return VERSIONS
            return "Version definition section '.gnu.version_d':\n Name: GLIBC_2.9\n"
        with patch('linux_abi.output', side_effect=output), patch('pathlib.Path.is_file', return_value=True):
            with self.assertRaisesRegex(ValueError, 'missing required versions'):
                inspect('core', 'ui')

    def test_ready_file_must_match_the_release_commit(self):
        class Process:
            def __init__(self, command, **kwargs):
                pathlib.Path(command[-1]).write_text(json.dumps({'version':'1.1.2','commit':'old','platform':'linux','frontendReady':True}))
            def wait(self, **kwargs):
                return 0
        with tempfile.TemporaryDirectory() as directory, patch.object(compatibility.subprocess, 'Popen', Process), patch.object(compatibility, 'sandbox_probe'), patch.object(compatibility, 'require_smoke_tools'):
            with self.assertRaisesRegex(ValueError, 'source/version mismatch'):
                compatibility.smoke(pathlib.Path(directory), '1.1.2', 'new', as_user=False)

    def test_process_exit_without_frontend_handshake_is_failure(self):
        class Process:
            def __init__(self, *args, **kwargs): pass
            def wait(self, **kwargs): return 0
        with tempfile.TemporaryDirectory() as directory, patch.object(compatibility.subprocess, 'Popen', Process), patch.object(compatibility, 'sandbox_probe'), patch.object(compatibility, 'require_smoke_tools'):
            with self.assertRaisesRegex(RuntimeError, 'before frontend handshake'):
                compatibility.smoke(pathlib.Path(directory), '1.1.2', 'new', as_user=False)

    def test_suse_test_tools_use_native_x_server_and_wrapper_packages(self):
        with patch.object(compatibility, 'run') as run:
            compatibility.install_test_tools({'format':'rpm', 'osID':'opensuse-leap'})
            command=run.call_args.args
            self.assertIn('xorg-x11-server',command)
            self.assertIn('xvfb-run',command)
            self.assertIn('dbus-1-daemon',command)
            self.assertNotIn('xvfb',command)

    def test_missing_dbus_wrapper_is_reported_as_environment_failure(self):
        with patch.object(compatibility.shutil,'which',side_effect=lambda name: None if name=='dbus-run-session' else '/usr/bin/'+name):
            with self.assertRaisesRegex(RuntimeError,'environment is missing commands: dbus-run-session'):
                compatibility.require_smoke_tools(as_user=True)

    def test_unavailable_nested_sandbox_fails_before_ui_launch(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(compatibility.shutil, 'which', return_value='/usr/bin/bwrap'), patch.object(compatibility.subprocess, 'run') as run:
            run.return_value.returncode=1
            with self.assertRaisesRegex(RuntimeError,'sandbox unavailable'):
                compatibility.sandbox_probe(pathlib.Path(directory), as_user=True)
            command=run.call_args.args[0]
            self.assertEqual(command[:4],['runuser','-u','fncpn-smoke','--'])
            self.assertIn('--dev',command)

    def test_runner_policy_preparation_is_guarded_and_verified(self):
        with tempfile.TemporaryDirectory() as directory:
            policy=pathlib.Path(directory)/'userns-policy'
            policy.write_text('1\n')
            with self.assertRaisesRegex(RuntimeError,'restricted to disposable'):
                sandbox_policy.prepare(policy,github_actions='false')
            self.assertEqual(policy.read_text(),'1\n')
            report=sandbox_policy.prepare(policy,github_actions='true')
            self.assertEqual(report['before'],'1')
            self.assertEqual(report['after'],'0')
            self.assertEqual(policy.read_text(),'0\n')
            report=sandbox_policy.prepare(pathlib.Path(directory)/'absent',github_actions='true')
            self.assertFalse(report['present'])

    def test_reinstall_uses_native_maintenance_transaction(self):
        with patch.object(compatibility, 'run') as run:
            compatibility.install({'format':'rpm','osID':'fedora'}, ['core.rpm','ui.rpm'], reinstall=True)
            self.assertEqual(run.call_args.args[:2], ('dnf','reinstall'))
            compatibility.install({'format':'deb'}, ['core.deb','ui.deb'], reinstall=True)
            self.assertIn('--reinstall',run.call_args.args)

if __name__ == '__main__':
    unittest.main()
