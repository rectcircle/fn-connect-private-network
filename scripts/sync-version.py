#!/usr/bin/env python3
"""Generate package version templates from the canonical embedded version."""
import argparse, pathlib, re, sys
root = pathlib.Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser()
parser.add_argument('--check', action='store_true')
args = parser.parse_args()
version = (root / 'internal/version/VERSION').read_text().strip()
if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', version):
    raise SystemExit('Invalid product version')
core_version = version
updates = {
    root / 'packaging/fnos/manifest': (r'(?m)^version=.*$', f'version={version}'),
    root / 'packaging/macos/Info.plist': (r'(<key>CFBundleShortVersionString</key>\s*<string>)[^<]+(</string>)', rf'\g<1>{core_version}\g<2>'),
}
# All package metadata uses the same numeric product version.
plist_path = root / 'packaging/macos/Info.plist'

failed = False
for path, (pattern, replacement) in updates.items():
    before = path.read_text(encoding='utf-8')
    after, count = re.subn(pattern, replacement, before)
    if path.name == 'manifest':
        after, desc_count = re.subn(r'(?m)^desc=.*$',
            f'desc=<p>通过 FN Connect 自动建立 WireGuard 异地私有网络。</p><p>Automatically connect private networks with WireGuard via FN Connect.</p><p><a href="https://github.com/rectcircle/fn-connect-private-network/releases/tag/v{version}" target="_blank" rel="noopener noreferrer">下载当前版本客户端 / Download current version</a></p><p><a href="https://fncpn.rectcircle.cn" target="_blank" rel="noopener noreferrer">官方网站 / Official website</a></p><p><a href="https://fncpn.rectcircle.cn/?donate=1" target="_blank" rel="noopener noreferrer">打赏作者 / Tip the author</a></p>', after)
        if desc_count != 1:
            raise SystemExit('Missing description in fnOS manifest')
        description = re.search(r'(?m)^desc=(.*)$', after)
        if not description or '#' in description.group(1) or description.group(1).count('<a ') != 3:
            raise SystemExit('fnOS description must use three links and avoid raw # characters')
    if path == plist_path:
        after, product_count = re.subn(r'(<key>FnCPNProductVersion</key>\s*<string>)[^<]+(</string>)', rf'\g<1>{version}\g<2>', after)
        if product_count != 1:
            raise SystemExit('Missing product version in Info.plist')
    if count != 1:
        raise SystemExit(f'Missing or duplicate version field: {path}')
    if before != after:
        if args.check:
            print(f'Outdated version template: {path}', file=sys.stderr)
            failed = True
        else:
            path.write_text(after, encoding='utf-8')
sys.exit(1 if failed else 0)
