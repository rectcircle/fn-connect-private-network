#!/usr/bin/env python3
"""Reject stale, incomplete or foreign same-package release acceptance."""
import copy
import unittest
from linux_acceptance import BASE_CHECKS, SYSTEM_CHECKS, validate_evidence
from linux_support import TARGETS, package_name
from rpm_signing import signing_identity


class AcceptanceTest(unittest.TestCase):
    def test_each_supported_target_requires_full_same_commit_same_package_evidence(self):
        for target, profile in TARGETS.items():
            if 'loong64' not in profile['architectures']: continue
            hashes = {package_name(c, '1.1.8', target, 'loong64'): c * 16 for c in ('core', 'ui')}
            evidence = {'target': target, 'architecture': 'loong64', 'bundle': profile['format'],
                'productVersion': '1.1.8', 'commit': 'candidate', 'validationCommit': 'candidate',
                'passed': True, 'artifacts': hashes, 'checks': sorted(BASE_CHECKS | SYSTEM_CHECKS)}
            if profile['format'] == 'rpm':
                evidence.update(rpmSigning=signing_identity())
                evidence['checks'].append('rpm-signature')
            validate_evidence(evidence, target, 'loong64', '1.1.8', 'candidate', hashes)
            mutations = [('commit', 'old'), ('validationCommit', 'foreign'), ('validationCommit', None),
                         ('passed', False), ('productVersion', '1.1.7'), ('artifacts', {}),
                         ('rpmSigning', {})] if profile['format'] == 'rpm' else [
                         ('commit', 'old'), ('validationCommit', 'foreign'), ('validationCommit', None),
                         ('passed', False), ('productVersion', '1.1.7'), ('artifacts', {})]
            for key, value in mutations:
                bad = copy.deepcopy(evidence); bad[key] = value
                with self.subTest(target=target, key=key), self.assertRaises(ValueError):
                    validate_evidence(bad, target, 'loong64', '1.1.8', 'candidate', hashes)
            for check in evidence['checks']:
                bad = copy.deepcopy(evidence); bad['checks'].remove(check)
                with self.subTest(target=target, missing=check), self.assertRaises(ValueError):
                    validate_evidence(bad, target, 'loong64', '1.1.8', 'candidate', hashes)


if __name__ == '__main__':
    unittest.main()
