#!/usr/bin/env python3
"""Inventory the exact product release; RELEASE=1 refuses an existing inventory."""
import hashlib, json, os, pathlib, sys, subprocess
from linux_support import TARGETS, BUNDLES, package_name as linux_package_name
from rpm_signing import signing_identity
from android_release import candidates as android_candidates
from ios_release import candidates as ios_candidates
from harmonyos_release import candidates as harmonyos_candidates
root, version, revision, arch = sys.argv[1:]
root = pathlib.Path(root)
target = root / f"release-{version}.json"
if os.environ.get("RELEASE") == "1" and target.exists():
    raise SystemExit(f"Release already recorded: {target}")
names = [f"fncpn-{version}-macos-arm64-unsigned.pkg", f"fncpn-{version}-macos-x64-unsigned.pkg", f"fncpn-{version}-fnos-arm64.fpk", f"fncpn-{version}-fnos-x64.fpk"]
artifacts = [{"file": name, "sha256": hashlib.sha256((root / name).read_bytes()).hexdigest()} for name in names]

# Windows packages are built on Windows so Win32, WiX, and PE validation use the
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

if tuple(map(int, version.split('.'))) >= (1, 1, 2):
    for linux_target, profile in BUNDLES.items():
        for linux_arch in profile['architectures']:
            relative = pathlib.Path('linux') / linux_target / linux_arch
            try:
                build = json.loads((root / relative / 'build-manifest.json').read_text())
            except (OSError, json.JSONDecodeError) as error:
                raise SystemExit(f'Missing Linux {linux_target} {linux_arch} build manifest: {error}')
            if (build.get('productVersion') != version or build.get('commit') != revision or
                build.get('architecture') != linux_arch or build.get('distribution') != linux_target or
                build.get('packageFormat') != profile.get('format', linux_target) or build.get('workingTreeDirty') is not False):
                raise SystemExit('Linux candidate must match the clean release version, commit, format and architecture')
            if linux_target == 'rpm' and build.get('rpmSigning') != signing_identity():
                raise SystemExit('RPM signing evidence does not match the project key')
            expected_names = {linux_package_name(component,version,linux_target,linux_arch) for component in ('core', 'ui')}
            entries = build.get('artifacts', [])
            if len(entries) != 2 or {entry.get('path') for entry in entries} != expected_names:
                raise SystemExit('Linux manifest must contain exactly matching Core/UI packages')
            for entry in entries:
                name = entry['path']
                digest = hashlib.sha256((root / relative / name).read_bytes()).hexdigest()
                if digest != entry.get('sha256'):
                    raise SystemExit('Linux package checksum does not match its build manifest')
                artifacts.append({'file': name, 'path': (relative / name).as_posix(), 'sha256': digest})

    # Every supported target must have checked these exact shared archives.
    by_name = {entry['file']: entry['sha256'] for entry in artifacts}
    for distro, profile in TARGETS.items():
        for architecture in profile['architectures']:
            try:
                evidence = json.loads((root/'linux-compat'/f'{distro}-{architecture}.json').read_text())
            except (OSError, json.JSONDecodeError) as error:
                raise SystemExit(f'Missing Linux compatibility evidence for {distro} {architecture}: {error}')
            expected = {linux_package_name(component,version,distro,architecture): by_name[linux_package_name(component,version,distro,architecture)] for component in ('core','ui')}
            expected_checks = {'package-metadata','dependency-resolution','core-version','ui-dynamic-linking','core-only-install','elf-abi','ui-frontend-handshake','package-reinstall','package-remove'}
            if architecture == 'loong64':
                expected_checks.update({'systemd-services', 'package-upgrade', 'upgrade-state-preserved', 'kernel-network', 'real-resolved'})
            if profile['format'] == 'rpm':
                expected_checks.add('rpm-signature')
                if evidence.get('rpmSigning') != signing_identity():
                    raise SystemExit('RPM compatibility signing evidence does not match the project key')
            if (evidence.get('target') != distro or evidence.get('architecture') != architecture or
                evidence.get('bundle') != profile['format'] or evidence.get('productVersion') != version or
                evidence.get('commit') != revision or evidence.get('artifacts') != expected or
                evidence.get('passed') is not True or
                set(evidence.get('checks', [])) != expected_checks):
                raise SystemExit('Linux compatibility evidence does not match the release packages and commit')

android_build = None
if tuple(map(int, version.split('.'))) >= (1, 1, 4):
    try:
        android_artifacts, android_build = android_candidates(root, version, revision)
    except (OSError, ValueError, KeyError) as error:
        raise SystemExit(f'Invalid Android release candidates: {error}')
    artifacts.extend(android_artifacts)

ios_artifacts, ios_build = ios_candidates(root, version, revision)
artifacts.extend(ios_artifacts)
harmonyos_artifacts, harmonyos_build = harmonyos_candidates(root, version, revision)
artifacts.extend(harmonyos_artifacts)

def tool(*command):
    try:
        result = subprocess.run(command, capture_output=True, text=True)
    except OSError:
        return "unavailable"
    return result.stdout.strip() if result.returncode == 0 else "unavailable"
metadata = {"version": version, "commit": revision,
            "channel": "stable",
            "artifacts": artifacts,
            "build": {"go": tool("go", "version"), "xcode": tool("xcodebuild", "-version"),
                      "sdk": tool("xcrun", "--show-sdk-version"), "arch": arch,
                      "deploymentTarget": os.environ.get("MACOSX_DEPLOYMENT_TARGET", "13.0"),
                      "fnpackVersion": "1.2.3",
                      "windowsSourceFingerprint": windows_fingerprint,
                      "macosArchitectures": ["arm64", "x64"],
                      "windowsArchitectures": ["arm64", "x64"]}}
if tuple(map(int, version.split('.'))) >= (1, 1, 2):
    metadata['build']['rpmSigning'] = signing_identity()
    metadata['build']['linuxArchitectures'] = sorted({arch for p in BUNDLES.values() for arch in p['architectures']})
    metadata['build']['linuxTargets'] = list(TARGETS)
    metadata['build']['linuxBundles'] = list(BUNDLES)
    metadata['build']['nixSystems'] = ['x86_64-linux', 'aarch64-linux']
if harmonyos_build:
    metadata['build']['harmonyos'] = harmonyos_build
if ios_build:
    metadata['build']['ios'] = ios_build
if android_build is not None:
    metadata['build']['android'] = android_build
with target.open("x" if os.environ.get("RELEASE") == "1" else "w") as output:
    output.write(json.dumps(metadata, indent=2) + "\n")
(root / f"SHA256SUMS-{version}").write_text("".join(f"{a['sha256']}  {a['file']}\n" for a in artifacts))
print(target)
