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
    root / 'apps/macos/packaging/Info.plist': (r'(<key>CFBundleShortVersionString</key>\s*<string>)[^<]+(</string>)', rf'\g<1>{core_version}\g<2>'),
}
# Wails UI is a local component of the same product, not a separately versioned app.
wails_config = root / 'apps/shared/wails/wails.json'
if wails_config.exists():
    updates[wails_config] = (r'("productVersion"\s*:\s*")[^"]+(")', rf'\g<1>{version}\g<2>')
harmony_app = root / 'apps/harmonyos/AppScope/app.json5'
if harmony_app.exists():
    number = (root / 'apps/harmonyos/BUILD_NUMBER').read_text().strip()
    if not re.fullmatch(r'[1-9][0-9]*', number) or int(number) > 2147483647:
        raise SystemExit('HarmonyOS BUILD_NUMBER must be a positive 32-bit integer')
    # Fold both fields into a single replacement to preserve --check semantics.
    updates[harmony_app] = (r'("version(?:Name|Code)"\s*:\s*)("[^"\n]*"|[0-9]+)',
        lambda match: match[1] + (str(number) if 'versionCode' in match[1] else '"' + version + '"'))
    for qualifier in ['base', 'en_US']:
        resource = root / f'apps/harmonyos/entry/src/main/resources/{qualifier}/element/string.json'
        updates[resource] = (r'("name"\s*:\s*"product_version",\s*"value"\s*:\s*")[^"]+("\s*)', rf'\g<1>{version}\g<2>')
# All package metadata uses the same numeric product version.
plist_path = root / 'apps/macos/packaging/Info.plist'

failed = False
for path, (pattern, replacement) in updates.items():
    before = path.read_text(encoding='utf-8')
    after, count = re.subn(pattern, replacement, before)
    if path.name == 'manifest':
        after, desc_count = re.subn(r'(?m)^desc=.*$',
            f'desc=<p>让任意设备一键连接到 NAS 所在的私有网络。</p><p>Connect any device to the private network of your NAS with one click.</p><p><a href="https://github.com/rectcircle/fn-connect-private-network/releases/tag/v{version}" target="_blank" rel="noopener noreferrer">下载当前版本客户端 / Download current version</a></p><p><a href="https://fncpn.rectcircle.cn" target="_blank" rel="noopener noreferrer">官方网站 / Official website</a></p><p><a href="https://fncpn.rectcircle.cn/?donate=1" target="_blank" rel="noopener noreferrer">打赏作者 / Tip the author</a></p>', after)
        if desc_count != 1:
            raise SystemExit('Missing description in fnOS manifest')
        description = re.search(r'(?m)^desc=(.*)$', after)
        if not description or '#' in description.group(1) or description.group(1).count('<a ') != 3:
            raise SystemExit('fnOS description must use three links and avoid raw # characters')
    if path == plist_path:
        after, product_count = re.subn(r'(<key>FnCPNProductVersion</key>\s*<string>)[^<]+(</string>)', rf'\g<1>{version}\g<2>', after)
        if product_count != 1:
            raise SystemExit('Missing product version in Info.plist')
    if count != (2 if path == harmony_app else 1):
        raise SystemExit(f'Missing or duplicate version field: {path}')
    if before != after:
        if args.check:
            print(f'Outdated version template: {path}', file=sys.stderr)
            failed = True
        else:
            path.write_text(after, encoding='utf-8')
# The generated iOS project is committed for direct Xcode use. Keep both
# project configurations synchronized even when no Xcode build has run yet.
ios_project = root / 'apps/ios/FnCPN.xcodeproj/project.pbxproj'
if ios_project.exists():
    number = (root / 'apps/ios/BUILD_NUMBER').read_text().strip()
    if not re.fullmatch(r'[1-9][0-9]{0,3}', number):
        raise SystemExit('iOS BUILD_NUMBER must be a positive integer of at most four digits')
    before = ios_project.read_text()
    after = before
    for key, value in [('MARKETING_VERSION', version), ('CURRENT_PROJECT_VERSION', number)]:
        after, count = re.subn(r'("' + key + r'" = ")[^"]+(";)', lambda match: match[1] + value + match[2], after)
        if count != 2:
            raise SystemExit('Expected two iOS project configurations for ' + key)
    if before != after:
        if args.check:
            print('Outdated version template: ' + str(ios_project), file=sys.stderr)
            failed = True
        else:
            ios_project.write_text(after)
sys.exit(1 if failed else 0)
