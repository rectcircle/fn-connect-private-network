from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
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


if __name__ == '__main__':
    unittest.main()
