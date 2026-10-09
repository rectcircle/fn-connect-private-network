#!/usr/bin/env python3
"""Generate the stable fncpn cask only from a public stable release manifest."""
import json
import pathlib
import re
import subprocess
import sys

version = sys.argv[1]
if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', version):
    raise SystemExit('Homebrew accepts stable X.Y.Z versions only; download RC packages manually')
repository = 'rectcircle/fn-connect-private-network'
release = json.loads(subprocess.check_output(['gh', 'release', 'view', 'v' + version, '--repo', repository, '--json', 'isDraft,isPrerelease'], text=True))
if release['isDraft'] or release['isPrerelease']:
    raise SystemExit('Cask requires a public stable release')
manifest = json.loads(subprocess.check_output(['gh', 'release', 'download', 'v' + version, '--repo', repository, '--pattern', f'release-{version}.json', '--output', '-'], text=True))
if manifest['version'] != version:
    raise SystemExit('Manifest version mismatch')
artifact = next(a for a in manifest['artifacts'] if a['file'] == f'FnCPN-{version}-arm64-unsigned.pkg')
sha = artifact['sha256']
if not re.fullmatch('[0-9a-f]{64}', sha):
    raise SystemExit('Invalid checksum')
token = 'fncpn'
body = '''cask "TOKEN" do
  version "VERSION"
  sha256 "SHA256"

  url "https://github.com/rectcircle/fn-connect-private-network/releases/download/v#{version}/FnCPN-#{version}-arm64-unsigned.pkg"
  name "FnCPN"
  desc "Private networking for fnOS through FN Connect"
  homepage "https://github.com/rectcircle/fn-connect-private-network"

  depends_on arch: :arm64
  depends_on macos: :ventura

  pkg "FnCPN-#{version}-arm64-unsigned.pkg"

  uninstall script: {
    executable: "/Library/PrivilegedHelperTools/cn.rectcircle.fncpn/uninstall.sh",
    sudo: true,
  },
  pkgutil: "cn.rectcircle.fncpn"

  caveats <<~EOS
    The app and helper are ad-hoc signed. The PKG is unsigned and is not notarized.
    The maintainer does not have an Apple Developer account.
    Installation requires administrator privileges. See the README for downloading
    the PKG and removing its quarantine attribute before installation, if you trust
    this release. This affects only that package, not system-wide protection.
    If you do not trust the prebuilt artifacts, review the source and build locally.
    Uninstall retains user configuration and credentials. Purge is a separate manual operation.
  EOS
end
'''
for key, value in [('TOKEN', token), ('VERSION', version), ('SHA256', sha)]:
    body = body.replace(key, value)
path = pathlib.Path('Casks') / (token + '.rb')
path.parent.mkdir(exist_ok=True)
path.write_text(body)
print(path)
