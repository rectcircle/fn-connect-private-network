import copy
import io
import json
import struct
import unittest
import zipfile

import verify_apk


def library(machine=183, alignment=16384):
    data = bytearray(120)
    data[:6] = b"\x7fELF\x02\x01"
    struct.pack_into("<HH", data, 16, 3, machine)
    struct.pack_into("<Q", data, 32, 64)
    struct.pack_into("<HH", data, 54, 56, 1)
    struct.pack_into("<I", data, 64, 1)
    struct.pack_into("<Q", data, 112, alignment)
    return bytes(data)


def symbols():
    return "\n".join(f" {i}: 00001000 10 FUNC GLOBAL DEFAULT 1 {name}"
                     for i, name in enumerate(sorted(verify_apk.EXPORTS)))


def metadata():
    return {"version": "1.2.0", "versionCode": 42, "commit": "abc123", "dirty": False,
            "go": "1.27.1", "ndk": "28.2.13676358", "gradle": "8.13",
            "agp": "8.13.2", "sdk": 36, "abis": ["arm64-v8a", "x86_64"]}


class APKVerificationTests(unittest.TestCase):
    def apk(self, build=None, omit=None, bad_arch=False, padding=0):
        output = io.BytesIO()
        output.write(b"x" * padding)
        with zipfile.ZipFile(output, "w") as archive:
            for abi, machine in verify_apk.MACHINES.items():
                if abi != omit:
                    archive.writestr(f"lib/{abi}/libfncpn.so", library(183 if bad_arch else machine))
            archive.writestr("classes.dex", b"test fixture; not signed")
            archive.writestr("assets/build.json", json.dumps(build or metadata()))
            archive.writestr("assets/THIRD_PARTY_NOTICES.md", "upstream licenses")
        output.seek(0)
        return output

    def test_both_abis_and_clean_build_metadata(self):
        build, hashes = verify_apk.inspect_apk(self.apk(), "1.2.0", 42, "abc123", lambda _: symbols())
        self.assertEqual(build["versionCode"], 42)
        self.assertEqual(set(hashes), {"arm64-v8a", "x86_64"})

    def test_single_architecture_distribution(self):
        for abi, other in (("arm64-v8a", "x86_64"), ("x86_64", "arm64-v8a")):
            with self.subTest(abi=abi):
                _, hashes = verify_apk.inspect_apk(self.apk(omit=other), "1.2.0", 42, "abc123", lambda _: symbols(), abi)
                self.assertEqual(set(hashes), {abi})
                with self.assertRaises(ValueError):
                    verify_apk.inspect_apk(self.apk(), "1.2.0", 42, "abc123", lambda _: symbols(), abi)

    def test_obsolete_zip_storage_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "unused ZIP space"):
            verify_apk.inspect_apk(self.apk(padding=2 << 20), "1.2.0", 42, "abc123", lambda _: symbols())

    def test_missing_or_mislabeled_library_rejected(self):
        for apk in (self.apk(omit="x86_64"), self.apk(bad_arch=True)):
            with self.subTest(), self.assertRaises(ValueError):
                verify_apk.inspect_apk(apk, "1.2.0", 42, "abc123", lambda _: symbols())

    def test_stale_or_dirty_artifacts_rejected(self):
        for name, value in (("commit", "old-commit"), ("dirty", True), ("versionCode", 41), ("version", "1.1.0")):
            build = copy.deepcopy(metadata())
            build[name] = value
            with self.subTest(name=name), self.assertRaises(ValueError):
                verify_apk.inspect_apk(self.apk(build), "1.2.0", 42, "abc123", lambda _: symbols())

    def test_missing_or_undefined_jni_export_rejected(self):
        for output in (symbols().replace("Java_com_rectcircle_fncpn_Bridge_start", "other_symbol"),
                       symbols().replace("DEFAULT 1", "DEFAULT UND")):
            with self.subTest(), self.assertRaises(ValueError):
                verify_apk.inspect_apk(self.apk(), "1.2.0", 42, "abc123", lambda _: output)

    def test_wrong_alignment_or_truncated_elf_rejected(self):
        for data in (library(alignment=4096), library()[:80], b"not-elf"):
            with self.subTest(), self.assertRaises(ValueError):
                verify_apk.check_elf(data, 183)

    def test_debug_package_and_sdk_bounds(self):
        text = "package: name='com.rectcircle.fncpn.debug' versionCode='42' versionName='1.2.0'\nsdkVersion:'26'\ntargetSdkVersion:'36'\napplication-debuggable\n"
        for field in ("sdkVersion", "minSdkVersion"):
            with self.subTest(field=field):
                verify_apk.check_badging(text.replace("sdkVersion:", field + ":"), "1.2.0", 42)
        for bad in (text.replace(".debug", ""), text.replace("application-debuggable", ""),
                    text.replace("sdkVersion:'26'", "sdkVersion:'23'"),
                    text.replace("sdkVersion:'26'", "minSdkVersion:'23'"),
                    text.replace("targetSdkVersion:'36'", "targetSdkVersion:'35'"),
                    text.replace("sdkVersion:'26'", "minSdkVersion:'260'")):
            with self.subTest(), self.assertRaises(ValueError):
                verify_apk.check_badging(bad, "1.2.0", 42)


if __name__ == "__main__":
    unittest.main()

class SigningIdentityTests(unittest.TestCase):
    def test_only_pinned_single_signer_is_accepted(self):
        expected = 'ab' * 32
        output = 'Signer #1 certificate SHA-256 digest: ' + expected.upper() + '\n'
        self.assertEqual(verify_apk.check_signer(output, expected), expected)
        for invalid in ('', output.replace(expected.upper(), 'cd' * 32), output + output.replace('#1', '#2')):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                verify_apk.check_signer(invalid, expected)

class ReleaseBadgingTests(unittest.TestCase):
    def test_production_package_must_not_be_debuggable(self):
        text = "package: name='com.rectcircle.fncpn' versionCode='100' versionName='1.1.4'\nsdkVersion:'26'\ntargetSdkVersion:'36'\n"
        verify_apk.check_badging(text, '1.1.4', 100, 'com.rectcircle.fncpn', release=True)
        for invalid in (text+'application-debuggable\n', text.replace("fncpn'", "fncpn.debug'")):
            with self.assertRaises(ValueError):
                verify_apk.check_badging(invalid, '1.1.4', 100, 'com.rectcircle.fncpn', release=True)
