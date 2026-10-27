"""Exercise orchestration with a fake Nix; never install or alter host tools."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
import nix_go_hashes as hashes


class HashTests(unittest.TestCase):
    def test_check_uses_both_actual_derivations_and_rebuilds_cached_outputs(self):
        calls = []
        def run(command, **kwargs):
            calls.append(command)
            return subprocess.CompletedProcess(command, 0)
        hashes.check(Path('/source'), 'aarch64-linux', run)
        self.assertEqual(len(calls), 3)
        for part in ('core', 'ui'):
            self.assertIn(f'.#packages.aarch64-linux.{part}.goModules', calls[0])
        self.assertIn('--no-update-lock-file', calls[0])
        self.assertIn('--rebuild', calls[1])
        self.assertIn('--rebuild', calls[2])
        self.assertIn('internal/client/manager.go', calls[2][-1])
        self.assertIn('internal/version/VERSION', calls[2][-1])

    def test_check_failure_is_actionable(self):
        with self.assertRaisesRegex(RuntimeError, 'update-nix-go-hashes.sh'):
            hashes.check(Path('/source'), 'x86_64-linux', lambda *a, **k: subprocess.CompletedProcess(a, 1))

    def fixture(self, root):
        (root/'nix').mkdir()
        target = root/'nix/go-hashes.json'
        target.write_text('{"core":"old-core","ui":"old-ui"}\n')
        return target

    def runner(self, fail_part=None, fail_check=False):
        def run(command, **kwargs):
            if '--expr' not in command or '--rebuild' in command:
                return subprocess.CompletedProcess(command, int(fail_check))
            expr = command[-1]
            part = 'core' if '.core.goModules' in expr else 'ui'
            if part == fail_part:
                return subprocess.CompletedProcess(command, 1, '', 'network failure')
            actual = 'sha256-' + ('B' if part == 'core' else 'C')*43 + '='
            output = (f"error: hash mismatch in fixed-output derivation '/nix/store/hash-fncpn-{part}-1.1.4-go-modules.drv':\n"
                      f' specified: {hashes.FAKE_HASH}\n got: {actual}\n')
            self.assertIn('overrideAttrs', expr)
            self.assertIn('FNCPN_NIX_SOURCE', expr)
            self.assertEqual(kwargs['env']['FNCPN_NIX_SOURCE'], str(kwargs['cwd']))
            return subprocess.CompletedProcess(command, 1, '', output)
        return run

    def test_update_commits_both_hashes_only_after_success(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);target=self.fixture(root)
            hashes.update(root,'x86_64-linux',self.runner())
            self.assertEqual(json.loads(target.read_text()),{'core':'sha256-'+'B'*43+'=','ui':'sha256-'+'C'*43+'='})

    def test_failure_does_not_leave_partial_or_unverified_hashes(self):
        for run in (self.runner(fail_part='ui'), self.runner(fail_check=True)):
            with tempfile.TemporaryDirectory() as temp:
                root=Path(temp);target=self.fixture(root);before=target.read_bytes()
                with self.assertRaises(RuntimeError):hashes.update(root,'x86_64-linux',run)
                self.assertEqual(target.read_bytes(),before)
                self.assertEqual(list((root/'nix').iterdir()),[target])

    def test_dependency_mismatch_is_not_mistaken_for_application_hash(self):
        run = self.runner()
        def foreign(command, **kwargs):
            result = run(command, **kwargs)
            result.stderr = result.stderr.replace('fncpn-core-', 'unrelated-dependency-')
            return result
        with self.assertRaisesRegex(RuntimeError,'Cannot determine'):
            hashes.discover(Path('/source'),'x86_64-linux','core',foreign)


if __name__=='__main__':unittest.main()
