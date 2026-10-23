#!/usr/bin/env python3
"""Validate the explicit version and immutable source of a release candidate."""
import os
import pathlib
import re
import subprocess


def check():
    commit = os.environ.get('RELEASE_COMMIT', '')
    version = os.environ.get('RELEASE_VERSION', '')
    if not re.fullmatch(r'[0-9a-f]{40}', commit):
        raise SystemExit('RELEASE_COMMIT must be a full lowercase commit SHA')
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', version):
        raise SystemExit('RELEASE_VERSION must be MAJOR.MINOR.PATCH')
    if pathlib.Path('internal/version/VERSION').read_text().strip() != version:
        raise SystemExit('Candidate version does not match checkout')
    if subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip() != commit:
        raise SystemExit('Candidate commit does not match checkout')
    if subprocess.check_output(['git', 'status', '--porcelain'], text=True).strip():
        raise SystemExit('Release requires a clean checkout')


if __name__ == '__main__':
    check()
