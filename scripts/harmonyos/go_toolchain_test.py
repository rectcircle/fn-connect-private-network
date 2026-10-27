import tempfile
from pathlib import Path
from unittest import TestCase, main
from unittest.mock import patch

import go_toolchain


class LockTests(TestCase):
    def test_lock_is_released_on_failure(self):
        with tempfile.TemporaryDirectory() as temporary:
            home = Path(temporary)
            with patch.object(Path, 'home', return_value=home), patch.object(
                    go_toolchain, '_prepare', side_effect=RuntimeError('build failed')):
                with self.assertRaisesRegex(RuntimeError, 'build failed'):
                    go_toolchain.prepare()
            self.assertFalse((home / '.local/opt/harmonyos/go/.install-lock').exists())

    def test_existing_lock_prevents_build(self):
        with tempfile.TemporaryDirectory() as temporary:
            home = Path(temporary)
            lock = home / '.local/opt/harmonyos/go/.install-lock'
            lock.mkdir(parents=True)
            with patch.object(Path, 'home', return_value=home), patch.object(
                    go_toolchain, '_prepare') as build:
                with self.assertRaisesRegex(ValueError, 'already running'):
                    go_toolchain.prepare()
                build.assert_not_called()
            self.assertTrue(lock.exists())


if __name__ == '__main__':
    main()
