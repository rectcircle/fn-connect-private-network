"""Bootstrap integration tests with fake tools; no host installation or downloads."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parent


class SetupTests(unittest.TestCase):
    def test_download_then_reuse_runtime_device_and_tools(self):
        with tempfile.TemporaryDirectory(prefix="ios setup ") as tmp:
            root = Path(tmp)
            target = root / "scripts/ios"
            target.mkdir(parents=True)
            shutil.copy2(SOURCE / "setup_macos.py", target)
            (root / "mobile").mkdir()
            tools = root / "tools"
            tools.mkdir()
            home = root / "home"
            home.mkdir()
            calls = root / "calls"

            def tool(name, body):
                path = tools / name
                path.write_text('#!/usr/bin/env python3\n' + body)
                path.chmod(0o755)

            tool("go", '''import os, pathlib, sys
with open(os.environ['CALLS'], 'a') as f: f.write('go ' + ' '.join(sys.argv[1:]) + '\\n')
if sys.argv[1:3] == ['env', 'GOVERSION']: print('go1.27.1')
elif sys.argv[1:3] == ['version', '-m']: print('\\tmod\\tfyne.io/tools\\tv1.7.3\\thash')
elif sys.argv[1] == 'install':
 p = pathlib.Path(os.environ['GOBIN']) / 'fyne'
 p.write_text('#!/bin/sh\\necho v1.7.3\\n'); p.chmod(0o755)
''')
            tool("xcodebuild", '''import os, pathlib, sys
with open(os.environ['CALLS'], 'a') as f: f.write('xcodebuild ' + ' '.join(sys.argv[1:]) + '\\n')
if '-downloadPlatform' in sys.argv:
 if os.environ.get('FAIL_RUNTIME'): sys.exit(9)
 pathlib.Path(os.environ['HOME'], 'runtime').touch()
''')
            tool("xcrun", '''import json, os, pathlib, sys
home = pathlib.Path(os.environ['HOME'])
with open(os.environ['CALLS'], 'a') as f: f.write('xcrun ' + ' '.join(sys.argv[1:]) + '\\n')
if '--show-sdk-version' in sys.argv: print('27.1')
elif '--show-sdk-path' in sys.argv: print('/fake sdk')
elif sys.argv[1:4] == ['simctl', 'list', 'runtimes']:
 r = dict(isAvailable=True, version='27.1', identifier='com.apple.CoreSimulator.SimRuntime.iOS-27-1', supportedDeviceTypes=[dict(name='iPhone Test', identifier='phone')])
 if os.environ.get('OMIT_TYPES'): r.pop('supportedDeviceTypes')
 print(json.dumps(dict(runtimes=[r] if (home/'runtime').exists() else [])))
elif sys.argv[1:4] == ['simctl', 'list', 'devicetypes']:
 print(json.dumps(dict(devicetypes=[dict(productFamily='iPhone', identifier='phone', minRuntimeVersion=1769472, maxRuntimeVersion=4294967295)])))
elif sys.argv[1:4] == ['simctl', 'list', 'devices']:
 d = dict(name='FnCPN iOS Development', isAvailable=True, udid='test-udid')
 print(json.dumps(dict(devices={'com.apple.CoreSimulator.SimRuntime.iOS-27-1':[d] if (home/'device').exists() else []})))
elif sys.argv[1:3] == ['simctl', 'create']:
 (home/'device').touch(); print('test-udid')
''')
            env = dict(os.environ, HOME=str(home), CALLS=str(calls),
                       PATH=str(tools) + ':' + os.environ['PATH'], GOOS='android', GOARCH='arm64')
            for _ in range(2):
                result = subprocess.run(['python3', str(target/'setup_macos.py'), '--skip-smoke'],
                                        env=env, text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            output = calls.read_text()
            self.assertEqual(output.count('go install '), 1)
            self.assertEqual(output.count('xcodebuild -downloadPlatform '), 1)
            self.assertEqual(output.count('xcrun simctl create '), 1)
            self.assertEqual(output.count('go mod download'), 2)
            self.assertFalse((home/'.zshrc').exists())
            # Older simctl JSON omits supportedDeviceTypes: use version bounds.
            (home/'device').unlink()
            result = subprocess.run(['python3', str(target/'setup_macos.py'), '--skip-smoke'],
                                    env=dict(env, OMIT_TYPES='1'), text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn('xcrun simctl list devicetypes', calls.read_text())
            # Failed downloads must never create a device or report success.
            (home/'runtime').unlink()
            (home/'device').unlink()
            result = subprocess.run(['python3', str(target/'setup_macos.py'), '--skip-smoke'],
                                    env=dict(env, FAIL_RUNTIME='1'), text=True, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((home/'device').exists())
            self.assertNotIn('环境准备完成', result.stdout)

    def test_help(self):
        result = subprocess.run(['python3', str(SOURCE/'setup_macos.py'), '--help'],
                                text=True, capture_output=True)
        self.assertEqual(result.returncode, 0)
        self.assertIn('--skip-runtime', result.stdout)


if __name__ == '__main__':
    unittest.main()
