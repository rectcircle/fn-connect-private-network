import unittest
from ci_changes import requires_checks


class ChangesTest(unittest.TestCase):
    def test_docs_only_skip_platforms(self):
        self.assertFalse(requires_checks(['README.md', 'CHANGELOG.md', 'docs/development.md', 'TODO.txt']))

    def test_source_or_policy_change_keeps_checks(self):
        for path in ('internal/client/client.go', 'scripts/build-all.sh', '.github/workflows/release.yml',
                     'go.sum', 'AGENTS.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'website/download-platform.test.mjs', 'unknown-file'):
            with self.subTest(path=path):
                self.assertTrue(requires_checks(['README.md', path]))

    def test_empty_comparison_keeps_checks(self):
        self.assertTrue(requires_checks([]))
        self.assertTrue(requires_checks(['']))


if __name__ == '__main__':
    unittest.main()
