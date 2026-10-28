"""Bound guest commands and retain diagnostics without leaving child processes."""
import os
from pathlib import Path
import signal
import subprocess
import tempfile


def run_command(args, *, timeout=180, log=None):
    print('package check:', *args, flush=True)
    with tempfile.TemporaryFile(mode='w+') as capture:
        process = subprocess.Popen(args, stdout=capture, stderr=subprocess.STDOUT,
                                   text=True, start_new_session=True)
        expired = False
        try:
            code = process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            expired = True
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            code = process.wait()
        capture.seek(0)
        output = capture.read()
    if log is not None:
        with Path(log).open('a') as stream:
            stream.write(f'Command: {args!r}\n{output}\nExit: {code}; timeout: {expired}\n')
    if expired or code:
        print(output, flush=True)
        if expired:
            raise subprocess.TimeoutExpired(args, timeout, output=output)
        raise subprocess.CalledProcessError(code, args, output=output)
    return output.strip()
