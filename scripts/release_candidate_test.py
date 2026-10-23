#!/usr/bin/env python3
import importlib.util
import os
import pathlib
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('candidate', pathlib.Path(__file__).with_name('check-release-candidate.py'))
candidate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(candidate)


class CandidateTest(unittest.TestCase):
    def exercise(self, commit='a' * 40, version='1.1.4', head='a' * 40, dirty=''):
        with patch.dict(os.environ, RELEASE_COMMIT=commit, RELEASE_VERSION=version), \
                patch.object(candidate.pathlib.Path, 'read_text', return_value='1.1.4\n'), \
                patch.object(candidate.subprocess, 'check_output', side_effect=[head, dirty]):
            candidate.check()

    def test_clean_candidate_needs_no_tag(self):
        self.exercise()

    def test_mutable_and_abbreviated_refs_are_rejected(self):
        for ref in ('main', 'v1.1.4', 'aaaaaaa', ''):
            with self.subTest(ref=ref), self.assertRaisesRegex(SystemExit, 'full lowercase'):
                self.exercise(commit=ref)

    def test_version_and_commit_are_bound_to_checkout(self):
        with self.assertRaisesRegex(SystemExit, 'version does not match'):
            self.exercise(version='1.1.5')
        with self.assertRaisesRegex(SystemExit, 'commit does not match'):
            self.exercise(head='b' * 40)

    def test_dirty_candidate_is_rejected(self):
        with self.assertRaisesRegex(SystemExit, 'clean checkout'):
            self.exercise(dirty=' M file')


if __name__ == '__main__':
    unittest.main()
