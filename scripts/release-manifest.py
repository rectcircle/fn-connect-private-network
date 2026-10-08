#!/usr/bin/env python3
"""Inventory the exact product release; RELEASE=1 refuses an existing inventory."""
import hashlib, json, os, pathlib, sys
root, version, revision, arch = sys.argv[1:]
root = pathlib.Path(root)
target = root / f"release-{version}.json"
if os.environ.get("RELEASE") == "1" and target.exists():
    raise SystemExit(f"Release already recorded: {target}")
names = [f"FnCPN-{version}-{arch}-unsigned.pkg", f"fncpn-{version}-arm.fpk", f"fncpn-{version}-x86.fpk"]
artifacts = [{"file": name, "sha256": hashlib.sha256((root / name).read_bytes()).hexdigest()} for name in names]
with target.open("x" if os.environ.get("RELEASE") == "1" else "w") as output:
    output.write(json.dumps({"version": version, "commit": revision, "artifacts": artifacts}, indent=2) + "\n")
(root / f"SHA256SUMS-{version}").write_text("".join(f"{a['sha256']}  {a['file']}\n" for a in artifacts))
print(target)
