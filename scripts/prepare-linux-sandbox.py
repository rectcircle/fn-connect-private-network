#!/usr/bin/env python3
"""Permit nested desktop sandboxes on disposable GitHub-hosted Linux runners."""
import json
import os
import pathlib

POLICY = pathlib.Path('/proc/sys/kernel/apparmor_restrict_unprivileged_userns')


def prepare(policy=POLICY, github_actions=None):
    # Never change a developer/user machine merely because a smoke test runs.
    if github_actions != 'true':
        raise RuntimeError('Sandbox host preparation is restricted to disposable GitHub Actions runners')
    if not policy.exists():
        return {'policy': str(policy), 'present': False}
    before = policy.read_text().strip()
    policy.write_text('0\n')
    after = policy.read_text().strip()
    if after != '0':
        raise RuntimeError('AppArmor user namespace policy was not relaxed on the disposable runner')
    return {'policy': str(policy), 'present': True, 'before': before, 'after': after}


if __name__ == '__main__':
    if os.geteuid() != 0:
        raise SystemExit('Run CI sandbox preparation as root')
    print(json.dumps(prepare(github_actions=os.environ.get('GITHUB_ACTIONS')), indent=2))
