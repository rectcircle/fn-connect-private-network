"""macOS iOS bootstrap. Apple manages runtime downloads and verification."""
import argparse
import json
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
FYNE_VERSION = "v1.7.3"


def run(*args, capture=False, cwd=None, env=None):
    return subprocess.run(args, check=True, cwd=cwd, env=env,
                          stdout=subprocess.PIPE if capture else None,
                          text=True).stdout


def select_runtime(items, version):
    return next((r for r in items if r.get("isAvailable") and
                 r.get("version") == version and
                 r["identifier"].startswith("com.apple.CoreSimulator.SimRuntime.iOS-")), None)


def main():
    parser = argparse.ArgumentParser(description="准备 iOS 开发环境；需要已有完整 Xcode、Go 1.27.1、Python 3。")
    parser.add_argument("--skip-runtime", action="store_true", help="跳过 Runtime 下载和设备创建，仅准备编译环境")
    parser.add_argument("--skip-smoke", action="store_true", help="跳过 Swift/Go iOS 编译检查")
    parser.add_argument("--runtime-version", help="指定 iOS Runtime 版本，默认匹配当前 Simulator SDK")
    args = parser.parse_args()
    if platform.system() != "Darwin":
        parser.error("仅支持 macOS")
    start = time.monotonic()

    def stage(message):
        print(f"[setup-ios-macos] [{time.monotonic() - start:.0f}s] {message}", flush=True)

    # Do not inherit Android/cross-build settings into host tool installation.
    env = dict(os.environ)
    for key in ("GOOS", "GOARCH", "CGO_ENABLED", "CC", "CXX", "SDKROOT"):
        env.pop(key, None)
    env["GOTOOLCHAIN"] = "local"
    stage("检查 Xcode、SDK、Go")
    run("xcodebuild", "-version")
    run("xcodebuild", "-checkFirstLaunchStatus")
    sdk = run("xcrun", "--sdk", "iphonesimulator", "--show-sdk-path", capture=True).strip()
    run("xcrun", "--sdk", "iphoneos", "--show-sdk-path")
    sdk_version = run("xcrun", "--sdk", "iphonesimulator", "--show-sdk-version", capture=True).strip()
    go = None
    for candidate in (shutil.which("go"), str(Path.home() / ".local/opt/go-1.27.1/bin/go")):
        if candidate and Path(candidate).is_file() and run(candidate, "env", "GOVERSION", capture=True, env=env).strip() == "go1.27.1":
            go = candidate
            break
    if go is None:
        raise SystemExit("需要 Go 1.27.1；请安装后重跑（也复用 Android 初始化脚本的 ~/.local/opt/go-1.27.1）。")
    run(go, "version", env=env)
    tools = Path.home() / ".local/opt" / f"fncpn-ios-tools-{FYNE_VERSION}" / "bin"
    tools.mkdir(parents=True, exist_ok=True)
    tool_env = dict(env, GOBIN=str(tools))
    fyne = tools / "fyne"
    metadata = run(go, "version", "-m", str(fyne), capture=True, env=env) if fyne.exists() else ""
    if f"mod\tfyne.io/tools\t{FYNE_VERSION}\t" not in metadata:
        stage(f"安装 Fyne tools {FYNE_VERSION}")
        run(go, "install", f"fyne.io/tools/cmd/fyne@{FYNE_VERSION}", env=tool_env, cwd=ROOT)
    run(str(fyne), "version", env=env)
    stage("下载已有 mobile module 依赖（保留代理与校验配置）")
    run(go, "mod", "download", cwd=ROOT / "apps/shared/fyne", env=env)
    # No shell startup changes needed: print the exact path and use it explicitly.
    stage(f"Fyne 工具：{fyne}；无需修改 shell 配置")

    if not args.skip_runtime:
        version = args.runtime_version or sdk_version
        stage(f"准备 iOS {version} Runtime（预留约 30 GB）")
        runtimes = json.loads(run("xcrun", "simctl", "list", "runtimes", "--json", capture=True))["runtimes"]
        runtime = select_runtime(runtimes, version)
        if runtime is None:
            command = ["xcodebuild", "-downloadPlatform", "iOS", "-buildVersion", version]
            if platform.machine() == "arm64":
                command += ["-architectureVariant", "arm64"]
            run(*command)
            runtimes = json.loads(run("xcrun", "simctl", "list", "runtimes", "--json", capture=True))["runtimes"]
            runtime = select_runtime(runtimes, version)
        if runtime is None:
            raise SystemExit(f"下载后仍找不到可用 iOS {version} Runtime；检查 Xcode Components 后重跑。")
        devices = json.loads(run("xcrun", "simctl", "list", "devices", "--json", capture=True))["devices"]
        name = "FnCPN iOS Development"
        device = next((d for d in devices.get(runtime["identifier"], [])
                       if d["name"] == name and d.get("isAvailable")), None)
        if device is None:
            types = runtime.get("supportedDeviceTypes", [])
            iphones = [t for t in types if t.get("productFamily") == "iPhone" or t.get("name", "").startswith("iPhone")]
            if not iphones:
                # Some Xcode versions omit supportedDeviceTypes from runtime JSON.
                types = json.loads(run("xcrun", "simctl", "list", "devicetypes", "--json", capture=True))["devicetypes"]
                parts = [int(p) for p in runtime["version"].split(".")]
                parts += [0] * (3 - len(parts))
                encoded = (parts[0] << 16) | (parts[1] << 8) | parts[2]
                iphones = [t for t in types if t.get("productFamily") == "iPhone" and
                           t.get("minRuntimeVersion", 0) <= encoded <= t.get("maxRuntimeVersion", 0)]
            if not iphones:
                raise SystemExit("未找到兼容的 iPhone 类型；请在 Xcode 手动创建设备。")
            udid = run("xcrun", "simctl", "create", name, iphones[0]["identifier"], runtime["identifier"], capture=True).strip()
        else:
            udid = device["udid"]
        stage(f"模拟设备已就绪（不自动启动）：{udid}")
        print(f"启动：xcrun simctl boot {shlex.quote(udid)} && open -a Simulator", flush=True)

    if not args.skip_smoke:
        stage("编译 Swift 模拟器程序及 Go iOS C archive（非产品构建）")
        arch = "arm64" if platform.machine() == "arm64" else "x86_64"
        go_arch = "arm64" if arch == "arm64" else "amd64"
        with tempfile.TemporaryDirectory(prefix="fncpn-ios-smoke-") as temporary:
            folder = Path(temporary)
            (folder / "main.swift").write_text('import Foundation\nprint("FnCPN iOS SDK smoke")\n')
            target = f"{arch}-apple-ios15.0-simulator"
            run("xcrun", "--sdk", "iphonesimulator", "swiftc", "-sdk", sdk,
                "-target", target, str(folder / "main.swift"), "-o", str(folder / "swift-smoke"))
            cc = folder / "cc"
            cc.write_text('#!/bin/sh\nexec xcrun --sdk iphonesimulator clang -isysroot ' +
                          shlex.quote(sdk) + ' -target ' + shlex.quote(target) + ' "$@"\n')
            cc.chmod(0o755)
            (folder / "main.go").write_text('package main\nimport "C"\n//export FnCPNSmoke\nfunc FnCPNSmoke() C.int { return 1 }\nfunc main() {}\n')
            build_env = dict(env, GOOS="ios", GOARCH=go_arch, CGO_ENABLED="1", CC=str(cc))
            run(go, "build", "-buildmode=c-archive", "-o", str(folder / "smoke.a"),
                str(folder / "main.go"), env=build_env)
            # Verify that Swift can link the exported Go C ABI, not just compile separately.
            (folder / "bridge.swift").write_text(
                '@_silgen_name("FnCPNSmoke") func smoke() -> Int32\nprint(smoke())\n')
            run("xcrun", "--sdk", "iphonesimulator", "swiftc", "-sdk", sdk,
                "-target", target, str(folder / "bridge.swift"), str(folder / "smoke.a"),
                "-framework", "CoreFoundation", "-lresolv", "-o", str(folder / "bridge-smoke"))
    stage("环境准备完成；仓库尚无 iOS 产品工程，模拟器不能验证 Packet Tunnel，真机和签名另行准备。")


if __name__ == "__main__":
    main()
