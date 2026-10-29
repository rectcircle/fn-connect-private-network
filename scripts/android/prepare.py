#!/usr/bin/env python3
"""Build a single Go/Fyne library using an ALREADY INSTALLED Android NDK.

No SDK manager, installer, Gradle wrapper download or toolchain bootstrap runs.
Invoked by Gradle preBuild, or explicitly on a prepared Android build host.
"""
import json
import os
from pathlib import Path
import platform
import shutil
import sys
import subprocess

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT / "scripts"))
import fyne_mobile_render
MOBILE = ROOT / "apps/shared/fyne"
OUT = ROOT / "apps/android/app/build/generated"


def fail(message):
    raise SystemExit(message)


def copy_generated(source, destination):
    # Go's module cache is read-only. Do not carry that mode into generated
    # outputs, otherwise a second local build cannot overwrite the first one.
    if destination.exists():
        destination.unlink()
    shutil.copyfile(source, destination)
    destination.chmod(0o644)


def main():
    ndk_value = os.environ.get("ANDROID_NDK_HOME")
    if not ndk_value:
        fail("Set ANDROID_NDK_HOME to an existing NDK. This script never installs one.")
    ndk = Path(ndk_value)
    properties = ndk / "source.properties"
    if not properties.is_file():
        fail("ANDROID_NDK_HOME is not an existing NDK installation")
    revision = next((line.split("=", 1)[1].strip() for line in properties.read_text().splitlines()
                     if line.startswith("Pkg.Revision")), "")
    if revision != "28.2.13676358":
        fail("Expected pinned NDK 28.2.13676358; refusing a different native toolchain")
    host = {"Darwin": "darwin-x86_64", "Linux": "linux-x86_64"}.get(platform.system())
    if not host:
        fail("Build host must be macOS or Linux")
    go = shutil.which("go")
    if not go:
        fail("Go 1.27.1 must already be installed")
    env = dict(os.environ, GOTOOLCHAIN="local", CGO_ENABLED="1")
    if subprocess.check_output([go, "env", "GOVERSION"], env=env, text=True).strip() != "go1.27.1":
        fail("Expected pinned Go 1.27.1; this script never installs Go")
    module = json.loads(subprocess.check_output(
        [go, "list", "-m", "-json", "fyne.io/fyne/v2"], cwd=MOBILE, env=env, text=True))
    fyne = Path(module["Dir"])
    local = OUT / "fyne-mobile-v2.8.2"
    if not local.exists():
        shutil.copytree(fyne,local)
    (local / "internal/driver/mobile").chmod(0o755)
    # Always refresh patched files from the pinned source, so rebuilding is idempotent.
    for name in ("canvas.go","driver.go"):
        copy_generated(fyne / "internal/driver/mobile" / name, local / "internal/driver/mobile" / name)
    (local / "internal/painter").chmod(0o755)
    copy_generated(fyne / "internal/painter/font.go",local / "internal/painter/font.go")
    fyne_mobile_render.prepare(fyne,local)
    modfile=OUT / "android.mod"
    modfile.parent.mkdir(parents=True,exist_ok=True)
    modfile.write_text((MOBILE / "go.mod").read_text()+f"\nreplace fyne.io/fyne/v2 => {local}\n")
    shutil.copyfile(MOBILE / "go.sum",modfile.with_suffix(".sum"))
    subprocess.run([go,"mod","edit","-modfile",str(modfile),"-replace=github.com/rectcircle/fn-connect-private-network="+str(ROOT)],cwd=MOBILE,env=env,check=True)
    java = OUT / "fyne/java/org/golang/app"
    java.mkdir(parents=True, exist_ok=True)
    for source in (fyne / "internal/driver/mobile/app").glob("*.java"):
        copy_generated(source, java / source.name)
    assets = OUT / "fyne/assets"
    assets.mkdir(parents=True, exist_ok=True)
    copy_generated(fyne / "LICENSE", assets / "Fyne-LICENSE.txt")
    copy_generated(MOBILE / "THIRD_PARTY_NOTICES.md", assets / "THIRD_PARTY_NOTICES.md")
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    dirty = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip())
    trace_revision = revision + ("-dirty" if dirty else "")
    abis = {"arm64-v8a": ("arm64", "aarch64-linux-android26-clang"),
            "x86_64": ("amd64", "x86_64-linux-android26-clang")}
    for abi, (arch, compiler) in abis.items():
        cc = ndk / "toolchains/llvm/prebuilt" / host / "bin" / compiler
        if not cc.is_file():
            fail(f"Existing NDK compiler is missing: {cc}")
        output = OUT / "go/jniLibs" / abi / "libfncpn.so"
        output.parent.mkdir(parents=True, exist_ok=True)
        build_env = dict(env, GOOS="android", GOARCH=arch, CC=str(cc))
        subprocess.run([go, "build", "-modfile",str(modfile), "-trimpath", "-tags", "no_emoji,migrated_fynedo", "-buildmode=c-shared",
                        "-ldflags", f"-s -w -X github.com/rectcircle/fn-connect-private-network/internal/version.Revision={trace_revision} -extldflags=-Wl,-z,max-page-size=16384",
                        "-o", str(output), "."], cwd=MOBILE, env=build_env, check=True)
    (assets / "build.json").write_text(json.dumps({
        "version": (ROOT / "internal/version/VERSION").read_text().strip(),
        "commit": revision, "dirty": dirty, "go": "1.27.1", "fyne": module["Version"],
        "ndk": "28.2.13676358", "abis": list(abis), "goTags": ["no_emoji", "migrated_fynedo"], "stripped": True
    }, indent=2) + "\n")


if __name__ == "__main__":
    main()
