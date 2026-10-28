#!/usr/bin/env python3
"""Install target development dependencies only inside disposable Linux CI images."""
import subprocess
import sys
from linux_support import package_profile

profile=package_profile(sys.argv[1])
packages=profile['buildDependencies']
if profile.get('packageManager') == 'apt-rpm':
    command=['apt-get', '-o', 'Acquire::Retries=2', '-o', 'Acquire::http::Timeout=20', 'install', '-y']
elif profile['format']=='deb':
    command=['apt-get','install','-y','--no-install-recommends']
elif profile['format']=='archlinux':
    command=['pacman','-S','--needed','--noconfirm']
elif profile['osID'].startswith('opensuse'):
    command=['zypper','--non-interactive','install','--no-recommends']
else:
    command=['dnf','install','-y','--setopt=install_weak_deps=False']
    if profile.get('experimental'):
        command += ['--setopt=timeout=20', '--setopt=retries=2', '--setopt=ip_resolve=4']
subprocess.run(command+packages,check=True)
# Fail here if a target's development package naming no longer supplies the ABI.
subprocess.run(['pkg-config','--exists','gtk+-3.0','webkit2gtk-4.1','ayatana-appindicator3-0.1'],check=True)
