#!/usr/bin/env python3
"""Verify CI's Debug APK with the runner's existing SDK/NDK, then stage it.

The structural checks are independently testable without any Android tools.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[2]
MACHINES = {"arm64-v8a": 183, "x86_64": 62}
EXPORTS = {
    "ANativeActivity_onCreate",
    "JNI_OnLoad",
    "Java_com_rectcircle_fncpn_Bridge_prepare",
    "Java_com_rectcircle_fncpn_Bridge_start",
    "Java_com_rectcircle_fncpn_Bridge_stop",
    "Java_com_rectcircle_fncpn_Bridge_networkChanged",
    "Java_com_rectcircle_fncpn_Bridge_command",
}


def check_elf(data, machine):
    if len(data) < 64 or data[:6] != b"\x7fELF\x02\x01":
        raise ValueError("Expected a 64-bit little-endian ELF library")
    if struct.unpack_from("<H", data, 16)[0] != 3:
        raise ValueError("Expected a shared ELF library")
    if struct.unpack_from("<H", data, 18)[0] != machine:
        raise ValueError("ELF architecture does not match APK ABI")
    offset = struct.unpack_from("<Q", data, 32)[0]
    size, count = struct.unpack_from("<HH", data, 54)
    if size < 56 or count == 0 or offset + size * count > len(data):
        raise ValueError("Invalid ELF program headers")
    load_count = 0
    for index in range(count):
        header = offset + size * index
        if struct.unpack_from("<I", data, header)[0] != 1:
            continue
        load_count += 1
        alignment = struct.unpack_from("<Q", data, header + 48)[0]
        file_offset, virtual_address = struct.unpack_from("<QQ", data, header + 8)
        if alignment < 16384 or alignment & (alignment - 1) or file_offset % 16384 != virtual_address % 16384:
            raise ValueError("ELF load segment is not 16 KB aligned")
    if not load_count:
        raise ValueError("ELF has no loadable segments")


def check_exports(readelf_output):
    found = set()
    for line in readelf_output.splitlines():
        fields = line.split()
        if len(fields) >= 8 and fields[0].endswith(":") and fields[6] != "UND" and fields[4] in {"GLOBAL", "WEAK"} and fields[5] in {"DEFAULT", "PROTECTED"}:
            found.add(fields[7].split("@", 1)[0])
    missing = EXPORTS - found
    if missing:
        raise ValueError("Missing exported native entry points: " + ", ".join(sorted(missing)))


def check_badging(text, version, build_number):
    package = re.search(r"^package: name='([^']+)' versionCode='([^']+)' versionName='([^']+)'", text, re.M)
    if not package or package.groups() != ("com.rectcircle.fncpn.debug", str(build_number), version):
        raise ValueError("APK package name or version does not match the source build")
    if not re.search(r"^application-debuggable(?:\s|$)", text, re.M):
        raise ValueError("Expected Debug APK, not a release artifact")
    if not re.search(r"^sdkVersion:'26'", text, re.M) or not re.search(r"^targetSdkVersion:'36'", text, re.M):
        raise ValueError("APK SDK bounds do not match the configured Android target")


def inspect_apk(apk, version, build_number, commit, readelf):
    with zipfile.ZipFile(apk) as archive:
        names = archive.namelist()
        if len(names) != len(set(names)):
            raise ValueError("Duplicate APK entries")
        native = {name for name in names if name.startswith("lib/") and name.endswith(".so")}
        expected = {f"lib/{abi}/libfncpn.so" for abi in MACHINES}
        if native != expected:
            raise ValueError("Expected exactly one complete Go library for each supported ABI")
        if not any(re.fullmatch(r"classes(?:\d+)?\.dex", name) for name in names):
            raise ValueError("APK has no compiled Java classes")
        metadata = json.loads(archive.read("assets/build.json"))
        if (metadata.get("version"), metadata.get("versionCode"), metadata.get("commit"), metadata.get("dirty")) != (version, build_number, commit, False):
            raise ValueError("APK build metadata does not match clean source/version/commit")
        if set(metadata.get("abis", [])) != set(MACHINES):
            raise ValueError("APK metadata has the wrong ABI set")
        if metadata.get("go") != "1.27.1" or metadata.get("ndk") != "28.2.13676358" or metadata.get("gradle") != "8.13" or metadata.get("agp") != "8.13.2" or metadata.get("sdk") != 36:
            raise ValueError("APK metadata does not match the pinned toolchain")
        if not archive.read("assets/THIRD_PARTY_NOTICES.md").strip():
            raise ValueError("APK is missing dependency licenses")
        hashes = {}
        for abi, machine in MACHINES.items():
            data = archive.read(f"lib/{abi}/libfncpn.so")
            check_elf(data, machine)
            check_exports(readelf(data))
            hashes[abi] = hashlib.sha256(data).hexdigest()
        return metadata, hashes


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("apk", type=Path)
    parser.add_argument("--build-number", type=int, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    sdk = Path(os.environ["ANDROID_HOME"])
    ndk = Path(os.environ["ANDROID_NDK_HOME"])
    tools = sdk / "build-tools" / "35.0.0"
    readelf = ndk / "toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-readelf"
    version = (ROOT / "internal/version/VERSION").read_text().strip()
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    subprocess.run([str(tools / "apksigner"), "verify", "--verbose", str(args.apk)], check=True)
    subprocess.run([str(tools / "zipalign"), "-c", "-P", "16", "4", str(args.apk)], check=True)
    badging = subprocess.check_output([str(tools / "aapt2"), "dump", "badging", str(args.apk)], text=True)
    print(badging, flush=True)
    check_badging(badging, version, args.build_number)

    def symbols(data):
        with tempfile.TemporaryDirectory() as temporary:
            library = Path(temporary) / "libfncpn.so"
            library.write_bytes(data)
            return subprocess.check_output([str(readelf), "--dyn-syms", "--wide", str(library)], text=True)

    metadata, hashes = inspect_apk(args.apk, version, args.build_number, commit, symbols)
    args.output.mkdir(parents=True, exist_ok=True)
    filename = f"fncpn-{version}-android-debug-{args.build_number}.apk"
    destination = args.output / filename
    shutil.copy2(args.apk, destination)
    digest = hashlib.sha256(destination.read_bytes()).hexdigest()
    (args.output / "SHA256SUMS").write_text(f"{digest}  {filename}\n")
    manifest = {"build": metadata, "apk": filename, "sha256": digest, "nativeSha256": hashes,
                "checks": ["debug-signature", "package-version", "both-abis", "jni-exports", "16kb-alignment"]}
    (args.output / "verification.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(f"Verified {filename}: {digest}")


if __name__ == "__main__":
    main()
