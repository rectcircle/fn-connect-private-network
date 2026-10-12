#!/usr/bin/env python3
"""Inventory the exact product release; RELEASE=1 refuses an existing inventory."""
import hashlib, json, os, pathlib, sys, subprocess
root, version, revision, arch = sys.argv[1:]
root = pathlib.Path(root)
target = root / f"release-{version}.json"
if os.environ.get("RELEASE") == "1" and target.exists():
    raise SystemExit(f"Release already recorded: {target}")
names = [f"fncpn-{version}-macos-{arch}-unsigned.pkg", f"fncpn-{version}-fnos-arm64.fpk", f"fncpn-{version}-fnos-x64.fpk"]
artifacts = [{"file": name, "sha256": hashlib.sha256((root / name).read_bytes()).hexdigest()} for name in names]

# Windows packages are built on Windows so WPF, WiX, and PE validation use the
# native toolchain. Only their exact package and evidence manifest cross into
# the macOS release job; validate that both came from this same clean tag.
windows_fingerprint = None
for windows_arch in ("arm64", "x64"):
    relative = pathlib.Path("windows") / windows_arch
    manifest_path = root / relative / "build-manifest.json"
    try:
        build = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise SystemExit(f"Missing or invalid Windows {windows_arch} build manifest: {error}")
    package_name = f"fncpn-{version}-windows-{windows_arch}-unsigned.msi"
    if build.get("productVersion") != version or build.get("commit") != revision:
        raise SystemExit(f"Windows {windows_arch} build does not match release version and commit")
    if build.get("architecture") != windows_arch or build.get("workingTreeDirty") is not False or build.get("signed") is not False:
        raise SystemExit(f"Windows {windows_arch} build must be a clean, unsigned, matching-architecture candidate")
    fingerprint = build.get("sourceFingerprint")
    if not isinstance(fingerprint, str) or len(fingerprint) != 64 or any(c not in "0123456789abcdef" for c in fingerprint):
        raise SystemExit(f"Windows {windows_arch} build has an invalid source fingerprint")
    if windows_fingerprint is not None and fingerprint != windows_fingerprint:
        raise SystemExit("Windows ARM64 and x64 candidates have different source fingerprints")
    windows_fingerprint = fingerprint
    package_path = root / relative / package_name
    expected = hashlib.sha256(package_path.read_bytes()).hexdigest()
    entries = [entry for entry in build.get("artifacts", []) if entry.get("path") == package_name]
    if len(entries) != 1 or entries[0].get("sha256", "").lower() != expected:
        raise SystemExit(f"Windows {windows_arch} MSI checksum does not match its build manifest")
    artifacts.append({"file": package_name, "path": (relative / package_name).as_posix(), "sha256": expected})

def tool(*command):
    try:
        result = subprocess.run(command, capture_output=True, text=True)
    except OSError:
        return "unavailable"
    return result.stdout.strip() if result.returncode == 0 else "unavailable"
metadata = {"version": version, "commit": revision,
            "channel": "rc" if "-rc." in version else "stable",
            "artifacts": artifacts,
            "build": {"go": tool("go", "version"), "xcode": tool("xcodebuild", "-version"),
                      "sdk": tool("xcrun", "--show-sdk-version"), "arch": arch,
                      "deploymentTarget": os.environ.get("MACOSX_DEPLOYMENT_TARGET", "13.0"),
                      "fnpackVersion": "1.2.3",
                      "windowsSourceFingerprint": windows_fingerprint,
                      "windowsArchitectures": ["arm64", "x64"]}}
with target.open("x" if os.environ.get("RELEASE") == "1" else "w") as output:
    output.write(json.dumps(metadata, indent=2) + "\n")
(root / f"SHA256SUMS-{version}").write_text("".join(f"{a['sha256']}  {a['file']}\n" for a in artifacts))
print(target)
