"""Bound network retries while retaining DNF signature and transaction checks."""
import re
import subprocess
import time
from guest_commands import run_command

# Low-speed repository links should not abort after the original 30s/1000 B/s.
# Both the command timeout and retry count remain bounded.
OPTIONS = ('--setopt=timeout=60', '--setopt=minrate=100', '--setopt=retries=2',
           '--setopt=max_parallel_downloads=3', '--setopt=keepcache=True', '--setopt=ip_resolve=4')


def run_dnf(args, *, log=None):
    offline = '--cacheonly' in args
    command = ('dnf', *(() if offline else OPTIONS), *args)
    for attempt in range(1 if offline else 3):
        try:
            return run_command(command, timeout=600, log=log)
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
            output = error.output or ''
            if isinstance(output, bytes):
                output = output.decode(errors='replace')
            network_failure = re.search(r'Curl error \((?:6|7|18|28|35|52|56)\)|Status code: (?:429|5\d\d)', output)
            # Never retry signature, dependency, transaction or scriptlet errors.
            if offline or attempt == 2 or not network_failure or re.search(
                    r'GPG check FAILED|Transaction test error|Error: Transaction failed|conflicting requests|scriptlet failed', output):
                raise
            print(f'Retrying DNF transport failure ({attempt + 2}/3); keeping downloaded packages', flush=True)
            time.sleep(5 * (attempt + 1))
