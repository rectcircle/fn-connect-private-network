#!/usr/bin/env python3
"""Run the shared UI handshake test for a Nix package as the current user."""
import importlib.util
import pathlib
import sys

spec = importlib.util.spec_from_file_location('compatibility', pathlib.Path(__file__).with_name('linux-compatibility.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
if __name__ == '__main__':
    binary, directory, version, commit = sys.argv[1:]
    out = pathlib.Path(directory).resolve()
    out.mkdir(parents=True, exist_ok=True)
    module.smoke(out, version, commit, pathlib.Path(binary).resolve(), as_user=False)
