from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import dnf_downloads
from guest_commands import run_command


class CommandTests(unittest.TestCase):
    def test_success_and_failure_retain_output(self):
        with tempfile.TemporaryDirectory() as temporary:
            log = Path(temporary) / 'commands.log'
            self.assertEqual(run_command([sys.executable, '-c', 'print("ok")'], log=log), 'ok')
            with self.assertRaises(subprocess.CalledProcessError) as failure:
                run_command([sys.executable, '-c', 'print("diagnostic"); raise SystemExit(7)'], log=log)
            self.assertEqual(failure.exception.returncode, 7)
            self.assertIn('diagnostic', failure.exception.output)
            self.assertIn('diagnostic', log.read_text())

    def test_timeout_stops_children_and_preserves_diagnostics(self):
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary) / 'child-finished'
            child = 'import time; from pathlib import Path; time.sleep(1); Path(' + repr(str(marker)) + ').touch()'
            parent = 'import subprocess,sys,time; print("install progress",flush=True); subprocess.Popen([sys.executable,"-c",' + repr(child) + ']); time.sleep(10)'
            log = Path(temporary) / 'commands.log'
            with self.assertRaises(subprocess.TimeoutExpired) as failure:
                run_command([sys.executable, '-c', parent], timeout=0.3, log=log)
            self.assertIn('install progress', failure.exception.output)
            self.assertIn('timeout: True', log.read_text())
            # Wait beyond the child's planned write to prove descendants were stopped.
            import time
            time.sleep(1.1)
            self.assertFalse(marker.exists())


class DNFDownloadsTest(unittest.TestCase):
    def test_transient_transport_retries_are_bounded_and_keep_signature_options(self):
        failure = subprocess.CalledProcessError(1, ['dnf'], output='Curl error (28): Timeout was reached')
        with patch.object(dnf_downloads, 'run_command', side_effect=[failure, 'ok']) as command, patch.object(dnf_downloads.time, 'sleep'):
            self.assertEqual(dnf_downloads.run_dnf(['install', '-y', '--setopt=localpkg_gpgcheck=True', 'candidate.rpm']), 'ok')
            self.assertEqual(command.call_count, 2)
            args = command.call_args.args[0]
            self.assertIn('--setopt=keepcache=True', args)
            self.assertIn('--setopt=localpkg_gpgcheck=True', args)
            self.assertNotIn('--nogpgcheck', args)
        with patch.object(dnf_downloads, 'run_command', side_effect=failure) as command, patch.object(dnf_downloads.time, 'sleep'):
            with self.assertRaises(subprocess.CalledProcessError): dnf_downloads.run_dnf(['install', 'candidate.rpm'])
            self.assertEqual(command.call_count, 3)

    def test_offline_and_terminal_errors_do_not_retry(self):
        for args, output in [(['install', 'candidate.rpm'], 'GPG check FAILED'),
                             (['install', 'candidate.rpm'], 'conflicting requests'),
                             (['install', 'candidate.rpm'], 'Curl error (28)\nError: Transaction failed'),
                             (['--cacheonly', 'install', 'candidate.rpm'], 'Curl error (28)')]:
            failure = subprocess.CalledProcessError(1, ['dnf'], output=output)
            with self.subTest(args=args, output=output), patch.object(dnf_downloads, 'run_command', side_effect=failure) as command, patch.object(dnf_downloads.time, 'sleep') as sleep:
                with self.assertRaises(subprocess.CalledProcessError): dnf_downloads.run_dnf(args)
                self.assertEqual(command.call_count, 1)
                sleep.assert_not_called()
                if '--cacheonly' in args:
                    self.assertEqual(command.call_args.args[0], ('dnf', *args))


if __name__ == '__main__':
    unittest.main()
