#!/usr/bin/env python3
"""Generate a stable or RC cask only from a public release manifest."""
import json
import pathlib
import re
import subprocess
import sys

version = sys.argv[1]
if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-rc\.(0|[1-9][0-9]*))?', version):
    raise SystemExit('Invalid version')
repository = 'rectcircle/fn-connect-private-network'
release = json.loads(subprocess.check_output(['gh', 'release', 'view', 'v' + version, '--repo', repository, '--json', 'isDraft,isPrerelease'], text=True))
if release['isDraft'] or release['isPrerelease'] != ('-rc.' in version):
    raise SystemExit('Cask requires a public release with matching prerelease status')
manifest = json.loads(subprocess.check_output(['gh', 'release', 'download', 'v' + version, '--repo', repository, '--pattern', f'release-{version}.json', '--output', '-'], text=True))
if manifest['version'] != version:
    raise SystemExit('Manifest version mismatch')
artifact = next(a for a in manifest['artifacts'] if a['file'] == f'FnCPN-{version}-arm64-unsigned.pkg')
sha = artifact['sha256']
if not re.fullmatch('[0-9a-f]{64}', sha):
    raise SystemExit('Invalid checksum')
token = 'fncpn-rc' if '-rc.' in version else 'fncpn'
conflict = 'fncpn' if token == 'fncpn-rc' else 'fncpn-rc'
body = '''cask "TOKEN" do
  version "VERSION"
  sha256 "SHA256"

  url "https://github.com/rectcircle/fn-connect-private-network/releases/download/v#{version}/FnCPN-#{version}-arm64-unsigned.pkg"
  name "FnCPN"
  desc "Private networking for fnOS through FN Connect"
  homepage "https://github.com/rectcircle/fn-connect-private-network"

  depends_on arch: :arm64
  depends_on macos: :ventura
  conflicts_with cask: "CONFLICT"

  pkg "FnCPN-#{version}-arm64-unsigned.pkg"

  uninstall script: {
    executable: "/Library/PrivilegedHelperTools/cn.rectcircle.fncpn/uninstall.sh",
    sudo: true,
  },
  pkgutil: "cn.rectcircle.fncpn"

  caveats <<~EOS
    The app and helper are ad-hoc signed. The PKG is unsigned and is not notarized.
    Installation requires administrator privileges. macOS may require explicit approval.
    Uninstall retains user configuration and credentials. Purge is a separate manual operation.
  EOS
end
'''
for key, value in [('TOKEN', token), ('VERSION', version), ('SHA256', sha), ('CONFLICT', conflict)]:
    body = body.replace(key, value)
path = pathlib.Path('Casks') / (token + '.rb')
path.parent.mkdir(exist_ok=True)
path.write_text(body)
print(path)
