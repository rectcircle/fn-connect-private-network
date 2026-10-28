#!/usr/bin/env python3
"""Reject builds linked against a different distribution's runtime."""
import pathlib
import shlex
import sys
from linux_support import package_profile

profile=package_profile(sys.argv[1], sys.argv[2] if len(sys.argv)>2 else None)
fields={}
for line in pathlib.Path('/etc/os-release').read_text().splitlines():
    if '=' in line:
        key,value=line.split('=',1)
        parsed=shlex.split(value)
        fields[key]=parsed[0] if parsed else ''
if fields.get('ID')!=profile['osID'] or (profile['osVersion'] and fields.get('VERSION_ID')!=profile['osVersion']):
    raise SystemExit('Build this target in its matching container: '+profile['image'])
if profile.get('osCodename') and fields.get('VERSION_CODENAME') != profile['osCodename']:
    raise SystemExit('Build this target in its matching new-world codename: '+profile['osCodename'])
