"""Shared acceptance contract for inventory creation and publication."""
from linux_support import TARGETS, package_name
from rpm_signing import signing_identity

BASE_CHECKS = {'package-metadata', 'dependency-resolution', 'core-version',
               'ui-dynamic-linking', 'core-only-install', 'elf-abi',
               'ui-frontend-handshake', 'package-reinstall', 'package-remove'}
SYSTEM_CHECKS = {'systemd-services', 'package-upgrade', 'upgrade-state-preserved',
                 'kernel-network', 'real-resolved'}


def validate_evidence(evidence, target, architecture, version, commit, artifacts):
    profile = TARGETS[target]
    expected = {package_name(c, version, target, architecture):
                artifacts[package_name(c, version, target, architecture)] for c in ('core', 'ui')}
    checks = BASE_CHECKS | (SYSTEM_CHECKS if architecture == 'loong64' else set())
    if profile['format'] == 'rpm':
        checks = checks | {'rpm-signature'}
        if evidence.get('rpmSigning') != signing_identity():
            raise ValueError('RPM compatibility signing evidence does not match the project key')
    if (evidence.get('target') != target or evidence.get('architecture') != architecture
            or evidence.get('bundle') != profile['format'] or evidence.get('productVersion') != version
            or evidence.get('commit') != commit or evidence.get('artifacts') != expected
            or evidence.get('passed') is not True
            or not checks.issubset(evidence.get('checks', []))):
        raise ValueError('Linux compatibility evidence does not match the release packages and commit')
    if architecture == 'loong64' and evidence.get('validationCommit') != commit:
        raise ValueError('Full release acceptance must use the candidate validation source')
