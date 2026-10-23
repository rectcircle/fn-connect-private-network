"""Exercise setup with fake tools: never download or install host dependencies."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[1] / "setup-android-macos.sh"


class MacSetupTests(unittest.TestCase):
    def test_reuses_tools_and_environment_with_spaces_on_repeated_runs(self):
        with tempfile.TemporaryDirectory(prefix="fncpn setup ") as directory:
            root = Path(directory)
            (root / "scripts").mkdir()
            (root / "mobile/android").mkdir(parents=True)
            script = root / "scripts/setup-android-macos.sh"
            shutil.copy2(SOURCE, script)
            tools = root / "tools"
            tools.mkdir()
            calls = root / "calls"

            def executable(path, body):
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("#!/bin/bash\n" + body + "\n")
                path.chmod(0o755)

            executable(tools / "uname", 'case "$1" in -s) echo Darwin ;; -m) echo arm64 ;; esac')
            executable(tools / "go", 'echo "go $*" >> "$TEST_CALLS"; if [[ $1 == env ]]; then echo go1.27.1; else echo go1.27.1; fi')
            executable(tools / "gradle", 'echo "gradle $*" >> "$TEST_CALLS"; echo "Gradle 8.13"')
            for name in ("curl", "brew"):
                executable(tools / name, 'echo "UNEXPECTED INSTALL" >> "$TEST_CALLS"; exit 91')
            jdk = root / "jdk"
            executable(jdk / "bin/java", 'echo \'openjdk version "17.0.20"\' >&2')
            sdk = root / "sdk"
            executable(sdk / "cmdline-tools/12266719/bin/sdkmanager", 'echo "sdkmanager $*" >> "$TEST_CALLS"; echo 16.0')
            ndk = sdk / "ndk/28.2.13676358"
            ndk.mkdir(parents=True)
            (ndk / "source.properties").write_text("Pkg.Revision = 28.2.13676358\n")
            executable(ndk / "toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android26-clang", "exit 0")
            (sdk / "platforms/android-36").mkdir(parents=True)
            (sdk / "platforms/android-36/android.jar").touch()
            executable(sdk / "build-tools/35.0.0/aapt2", "exit 0")
            executable(sdk / "platform-tools/adb", "echo adb")
            home = root / "user home"
            home.mkdir()
            for name in (".zshrc", ".bashrc", ".bash_login"):
                (home / name).write_text("# existing user configuration\nexport KEEP_ME=yes\n")
            env = dict(os.environ, HOME=str(home), ZDOTDIR=str(home), ANDROID_NDK_HOME=str(ndk), PATH=str(tools) + ":" + os.environ["PATH"],
                       JAVA_HOME=str(jdk), ANDROID_HOME=str(sdk), TEST_CALLS=str(calls))
            for _ in range(2):
                result = subprocess.run(["/bin/bash", str(script), "--skip-smoke", "--accept-licenses"],
                                        env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            output = calls.read_text()
            self.assertNotIn("UNEXPECTED INSTALL", output)
            self.assertNotIn("assembleDebug", output)
            self.assertEqual(output.count("go mod download"), 2)
            for name in (".zshrc", ".bashrc", ".bash_login"):
                profile = home / name
                contents = profile.read_text()
                self.assertIn("# existing user configuration", contents)
                for dependency in ("Java", "Go", "Gradle", "Android"):
                    self.assertEqual(contents.count(f"# >>> FnCPN Android setup: {dependency} >>>"), 1)
                # Only the first run changes the file and creates a backup.
                self.assertEqual(len(list(home.glob(name + ".fncpn-backup-*"))), 1)
                result = subprocess.run(["/bin/bash", "-e", "-c",
                                         'source "$1"; source "$1"; printf "%s\\n%s\\n%s" "$ANDROID_HOME" "$PATH" "$KEEP_ME"',
                                         "_", str(profile)], env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                values = result.stdout.splitlines()
                self.assertEqual(values[0], str(sdk))
                self.assertEqual(values[1].split(":" ).count(str(jdk / "bin")), 1)
                self.assertEqual(values[2], "yes")
            self.assertFalse((home / ".bash_profile").exists())
            self.assertFalse((root / ".dev/android/env.sh").exists())

    def test_help_does_not_initialize(self):
        result = subprocess.run(["/bin/bash", str(SOURCE), "--help"], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0)
        self.assertIn("--skip-smoke", result.stdout)


if __name__ == "__main__":
    unittest.main()
