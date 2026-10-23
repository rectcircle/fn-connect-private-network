import pathlib
import tempfile
import unittest
import prepare

class PrepareTests(unittest.TestCase):
    def test_read_only_module_sources_can_be_copied_repeatedly(self):
        with tempfile.TemporaryDirectory() as directory:
            source = pathlib.Path(directory) / 'source.java'
            output = pathlib.Path(directory) / 'generated.java'
            source.write_text('upstream source')
            source.chmod(0o444)
            prepare.copy_generated(source, output)
            output.chmod(0o444)  # output left by the previous copy2 implementation
            prepare.copy_generated(source, output)
            self.assertEqual(output.read_text(), 'upstream source')
            self.assertTrue(output.stat().st_mode & 0o200)
