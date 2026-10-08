#!/usr/bin/env python3
"""Inventory the exact product release; RELEASE=1 refuses an existing inventory."""
import hashlib, json, os, pathlib, sys, subprocess
root, version, revision, arch = sys.argv[1:]
root = pathlib.Path(root)
target = root / f"release-{version}.json"
if os.environ.get("RELEASE") == "1" and target.exists():
    raise SystemExit(f"Release already recorded: {target}")
names = [f"FnCPN-{version}-{arch}-unsigned.pkg", f"fncpn-{version}-arm.fpk", f"fncpn-{version}-x86.fpk"]
artifacts = [{"file": name, "sha256": hashlib.sha256((root / name).read_bytes()).hexdigest()} for name in names]
def tool(*command):
    result = subprocess.run(command, capture_output=True, text=True)
    return result.stdout.strip() if result.returncode == 0 else "unavailable"
metadata = {"version": version, "commit": revision,
            "channel": "rc" if "-rc." in version else "stable",
            "artifacts": artifacts,
            "build": {"go": tool("go", "version"), "xcode": tool("xcodebuild", "-version"),
                      "sdk": tool("xcrun", "--show-sdk-version"), "arch": arch,
                      "deploymentTarget": os.environ.get("MACOSX_DEPLOYMENT_TARGET", "13.0"),
                      "fnpackVersion": "1.2.3"}}
with target.open("x" if os.environ.get("RELEASE") == "1" else "w") as output:
    output.write(json.dumps(metadata, indent=2) + "\n")
(root / f"SHA256SUMS-{version}").write_text("".join(f"{a['sha256']}  {a['file']}\n" for a in artifacts))
print(target)
