#!/usr/bin/env python3
"""Generate package version templates from the canonical embedded version."""
import argparse, pathlib, re, sys
root = pathlib.Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser()
parser.add_argument('--check', action='store_true')
args = parser.parse_args()
version = (root / 'internal/version/VERSION').read_text().strip()
if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-rc\.(0|[1-9][0-9]*))?', version):
    raise SystemExit('Invalid product version')
core_version = version.split("-", 1)[0]
updates = {
    root / 'packaging/fnos/manifest': (r'(?m)^version=.*$', f'version={version}'),
    root / 'packaging/macos/Info.plist': (r'(<key>CFBundleShortVersionString</key>\s*<string>)[^<]+(</string>)', rf'\g<1>{core_version}\g<2>'),
}
# The installer version is numeric; the product version retains the RC identity.
plist_path = root / 'packaging/macos/Info.plist'

failed = False
for path, (pattern, replacement) in updates.items():
    before = path.read_text()
    after, count = re.subn(pattern, replacement, before)
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
            path.write_text(after)
sys.exit(1 if failed else 0)
