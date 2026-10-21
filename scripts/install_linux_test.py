#!/usr/bin/env python3
"""Installer safety and selection tests; never installs packages or calls GitHub."""
import hashlib
import json
import pathlib
import shlex
import shutil
import sys
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
SCRIPT = ROOT / 'scripts/install-linux.sh'


class InstallerTest(unittest.TestCase):
    def shell(self, code, success=True):
        checksum_adapter = ''
        if sys.platform == 'darwin' and shutil.which('sha256sum') is None:
            checksum_adapter = 'sha256sum() { shasum -a 256 "$@"; };\n'
        result = subprocess.run(['bash', '-c', 'source '+shlex.quote(str(SCRIPT))+'\n'+checksum_adapter+code], text=True, capture_output=True)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout)
        return result

    def test_platform_rejection_before_any_mutation(self):
        for platform in ['Darwin', 'MINGW64_NT-10.0', 'Windows_NT']:
            result = self.shell(f'uname() {{ echo {platform}; }}; main --type all', False)
            self.assertIn('仅支持 Linux', result.stderr)

    def test_options_and_invalid_inputs(self):
        self.assertEqual(self.shell('parse_args --type core --version v1.1.2; echo "$INSTALL_TYPE $REQUESTED_VERSION"').stdout.strip(), 'core 1.1.2')
        self.assertEqual(self.shell('parse_args; echo "$INSTALL_TYPE $REQUESTED_VERSION"').stdout.strip(), 'auto latest')
        for args in ['--type', '--type auto', '--type ui', '--version', '--version ../x', '--version 1.2.3-rc.1', '--what']:
            self.shell('parse_args '+args, False)

    def test_catalog_and_unsupported_distributions(self):
        catalog = json.loads((ROOT/'packaging/linux/targets.json').read_text())
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary)/'os-release'
            for profile in catalog['targets']:
                path.write_text(f'ID={profile["osID"]}\nVERSION_ID={shlex.quote(profile["osVersion"])}\n')
                result = self.shell(f'detect_distribution {shlex.quote(str(path))}; echo "$FORMAT"')
                self.assertEqual(result.stdout.strip(), profile['format'])
            for name, version in [('alpine','3.22'), ('nixos','26.05'), ('ubuntu','20.04'), ('fedora','42'), ('opensuse-leap','15.6'), ('linuxmint','22')]:
                path.write_text(f'ID={name}\nVERSION_ID={version}\n')
                self.shell(f'detect_distribution {shlex.quote(str(path))}', False)
        self.shell('FORMAT=archlinux; uname() { echo aarch64; }; detect_architecture', False)
        self.assertEqual(self.shell('FORMAT=deb; uname() { echo aarch64; }; detect_architecture; echo "$ARCH"').stdout.strip(), 'arm64')

    def test_desktop_detection_including_ssh(self):
        self.shell('DISPLAY=:0; has_desktop')
        self.shell('DISPLAY=; WAYLAND_DISPLAY=; compgen() { return 0; }; has_desktop')
        self.shell('DISPLAY=; WAYLAND_DISPLAY=; compgen() { return 1; }; [ ! -L /etc/systemd/system/display-manager.service ] || exit 0; ! has_desktop')

    def test_automatic_component_selection_preserves_existing_ui(self):
        for desktop, installed, expected in [(True, False, 'all'), (False, False, 'core'), (False, True, 'all')]:
            code = f'has_desktop() {{ return {0 if desktop else 1}; }}; installed_version() {{ {"echo 1.1.1" if installed else ":"}; }}; select_type; echo "$INSTALL_TYPE"'
            self.assertEqual(self.shell(code).stdout.strip(), expected)
        self.assertEqual(self.shell('INSTALL_TYPE=core; has_desktop() { return 0; }; select_type; echo "$INSTALL_TYPE"').stdout.strip(), 'core')
        self.assertEqual(self.shell('INSTALL_TYPE=all; has_desktop() { return 1; }; select_type; echo "$INSTALL_TYPE"').stdout.strip(), 'all')

    def test_absent_rpm_is_not_treated_as_installed(self):
        result = self.shell('FORMAT=rpm; rpm() { echo "package is not installed"; return 1; }; installed_version ui')
        self.assertEqual(result.stdout, '')

    def test_paired_upgrade_and_downgrade_guards(self):
        self.shell('VERSION=1.1.2; INSTALL_TYPE=core; installed_version() { [ "$1" != ui ] || echo 1.1.1; }; check_upgrade', False)
        self.shell('VERSION=1.1.2; INSTALL_TYPE=all; installed_version() { echo 1.1.3; }; check_upgrade', False)
        self.shell('VERSION=1.1.2; INSTALL_TYPE=all; installed_version() { echo 1.1.1; }; check_upgrade')
        self.shell('VERSION=1.1.2; INSTALL_TYPE=core; installed_version() { echo 1.1.2; }; check_upgrade')
        self.shell('version_is_older 1.2.0 1.10.0')
        self.shell('! version_is_older 2.0.0 1.10.0')

    def test_release_metadata_and_missing_assets(self):
        with tempfile.TemporaryDirectory() as temporary:
            metadata = pathlib.Path(temporary)/'release.json'
            def check(data, code, success=True):
                metadata.write_text(json.dumps(data))
                return self.shell(f'WORK_DIR={shlex.quote(temporary)}; api_get() {{ :; }}; '+code, success)
            valid = dict(tag_name='v1.1.2',draft=False,prerelease=False,assets=[dict(id=42,name='package')])
            self.assertEqual(check(valid, 'resolve_release; echo "$VERSION"; asset_id package').stdout.strip(),'1.1.2\n42')
            check(valid, 'asset_id missing', False)
            check(dict(valid,assets=[dict(id=1,name='package'),dict(id=2,name='package')]), 'asset_id package', False)
            check(dict(valid,draft=True), 'resolve_release', False)
            check(dict(valid,prerelease=True), 'resolve_release', False)
            check(dict(valid,draft=True), 'REQUESTED_VERSION=1.1.2; resolve_release')
            check(valid, 'REQUESTED_VERSION=1.1.3; resolve_release', False)
            check(dict(valid,tag_name='../../bad'), 'resolve_release', False)

    def test_checksums_reject_corrupt_missing_and_duplicate_entries(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = pathlib.Path(temporary)
            name = 'fncpn-core-1.1.2-linux-amd64.deb'
            (directory/name).write_bytes(b'archive')
            digest = hashlib.sha256(b'archive').hexdigest()
            sums = directory/'SHA256SUMS-1.1.2'
            command = f'WORK_DIR={shlex.quote(temporary)}; VERSION=1.1.2; PACKAGE_NAMES=({name}); verify_packages'
            sums.write_text(f'{digest}  {name}\n'+'0'*64+'  ../../outside\n')
            self.shell(command)  # unselected names are never passed to sha256sum
            for text in [f'{"0"*64}  {name}\n', '', f'{digest}  {name}\n{digest}  {name}\n']:
                sums.write_text(text)
                self.shell(command, False)

    def test_rpm_key_pin_and_signatures_before_system_import(self):
        with tempfile.TemporaryDirectory() as directory:
            public=ROOT/'packaging/linux/keys/RPM-GPG-KEY-fncpn'
            common=f'WORK_DIR={shlex.quote(directory)}; FORMAT=rpm; VERSION=1.1.2; PACKAGE_NAMES=(core.rpm ui.rpm); api_get() {{ cp {shlex.quote(str(public))} "$2"; }}; as_root() {{ echo SYSTEM_IMPORT; }}; '
            good='rpmkeys() { case "$*" in *--checksig*) echo "Header V4 RSA/SHA256 Signature: OK";; esac; }; '
            result=self.shell(common+good+'verify_rpm_signatures')
            self.assertIn('SYSTEM_IMPORT',result.stdout)
            for output in ['Header SHA256 digest: OK','RSA Signature: NOKEY','RSA Signature: NOT OK']:
                # Each shell gets a fresh isolated RPM database directory.
                shutil.rmtree(pathlib.Path(directory)/'rpm-keyring')
                mock='rpmkeys() { case "$*" in *--checksig*) echo '+shlex.quote(output)+';; esac; }; '
                result=self.shell(common+mock+'verify_rpm_signatures',False)
                self.assertNotIn('SYSTEM_IMPORT',result.stdout)
            shutil.rmtree(pathlib.Path(directory)/'rpm-keyring')
            result=self.shell(common+good+'RPM_KEY_SHA256='+('0'*64)+'; verify_rpm_signatures',False)
            self.assertNotIn('SYSTEM_IMPORT',result.stdout)

    def test_package_transactions_and_stop_on_failed_update(self):
        common = 'WORK_DIR=/tmp/private; PACKAGE_NAMES=(core ui); as_root() { printf "%s\\n" "$*"; }; '
        for manager, expected in [('apt-get', 'apt-get install -y /tmp/private/core /tmp/private/ui'), ('dnf','dnf install -y --setopt=localpkg_gpgcheck=True /tmp/private/core /tmp/private/ui'), ('zypper','zypper --non-interactive install /tmp/private/core /tmp/private/ui'), ('pacman','pacman -U --noconfirm /tmp/private/core /tmp/private/ui')]:
            result = self.shell(common+f'MANAGER={manager}; install_packages')
            self.assertIn(expected, result.stdout)
            self.assertNotIn('nogpgcheck', result.stdout)
        for manager in ['apt-get','pacman']:
            result = self.shell(f'MANAGER={manager}; WORK_DIR=/tmp/private; PACKAGE_NAMES=(core); as_root() {{ echo "$*"; return 9; }}; install_packages || exit 8', False)
            self.assertNotIn('install -y',result.stdout)
            self.assertNotIn('pacman -U',result.stdout)

    def test_missing_runtime_tool_bootstrap_and_repo_failures(self):
        # Pretend curl/coreutils already exist (e.g. Fedora curl-minimal); only
        # jq should be requested, without conflicting with the installed curl.
        code = 'MANAGER=dnf; command() { [ "$2" != jq ]; }; as_root() { printf "%s\\n" "$*"; }; ensure_tools'
        result = self.shell(code)
        self.assertIn('dnf install -y jq', result.stdout)
        self.assertNotIn('jq curl', result.stdout)
        self.assertNotIn('coreutils', result.stdout)
        result = self.shell('api_get() { return 22; }; WORK_DIR=/tmp; resolve_release', False)
        self.assertIn('无法读取 GitHub Release', result.stderr)
        result = self.shell('asset_id() { echo 123; }; api_get() { return 22; }; WORK_DIR=/tmp; VERSION=1.1.2; download_asset package', False)
        self.assertIn('下载失败', result.stderr)

    def test_authentication_is_not_put_in_curl_arguments(self):
        result = self.shell('GH_TOKEN=test-secret; curl() { cat >/dev/null; printf "%s\\n" "$@"; }; api_get releases/latest /tmp/out application/json')
        self.assertNotIn('test-secret', result.stdout+result.stderr)
        self.assertIn('--config\n-', result.stdout)


if __name__ == '__main__': unittest.main()
