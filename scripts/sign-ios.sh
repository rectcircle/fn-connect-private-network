#!/bin/bash
# Personal-device re-signing, matching the single-profile flow verified on device.
# This is not the standard App Store export/signing workflow.
set +x
set -Eeuo pipefail
umask 077
usage() {
    echo 'Usage: bash scripts/sign-ios.sh PASSWORD_FILE P12_FILE PROFILE_FILE INPUT.ipa [OUTPUT.ipa]'
}
[[ ${1:-} != -h && ${1:-} != --help ]] || { usage; exit 0; }
[[ $# -ge 4 && $# -le 5 ]] || { usage >&2; exit 2; }
[[ $(uname -s) == Darwin ]] || { echo 'Requires macOS.' >&2; exit 1; }
PASSWORD_FILE=$1
P12_FILE=$2
PROFILE_FILE=$3
INPUT=$4
OUTPUT=${5:-${INPUT%.ipa}-signed.ipa}
for file in "$PASSWORD_FILE" "$P12_FILE" "$PROFILE_FILE" "$INPUT"; do
    [[ -f $file ]] || { echo "Missing input: $file" >&2; exit 1; }
done
[[ ! -e $OUTPUT ]] || { echo "Refusing to overwrite: $OUTPUT" >&2; exit 1; }
for tool in python3 openssl security codesign ditto; do
    command -v "$tool" >/dev/null || { echo "Missing tool: $tool" >&2; exit 1; }
done
WORK=$(mktemp -d "${TMPDIR:-/tmp}/fncpn-sign.XXXXXX")
KEYCHAIN="$WORK/signing.keychain-db"
cleanup() {
    security delete-keychain "$KEYCHAIN" >/dev/null 2>&1 || true
    rm -rf "$WORK"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# Read one line; accept the usual LF and CRLF password files. Never log secrets.
PASSWORD=$(cat "$PASSWORD_FILE")
PASSWORD=${PASSWORD%$'\r'}
[[ $PASSWORD != *$'\n'* ]] || { echo 'Password file must contain one line.' >&2; exit 1; }
security cms -D -i "$PROFILE_FILE" > "$WORK/profile.plist"
if ! printf '%s\n' "$PASSWORD" | openssl pkcs12 -in "$P12_FILE" -passin stdin \
    -clcerts -nokeys -out "$WORK/cert.pem" 2> "$WORK/openssl-error"; then
    # OpenSSL 3 requires legacy mode for some older PKCS#12 files.
    printf '%s\n' "$PASSWORD" | openssl pkcs12 -legacy -in "$P12_FILE" -passin stdin \
        -clcerts -nokeys -out "$WORK/cert.pem" 2> "$WORK/openssl-error" || {
        echo 'Cannot read P12: check its password and PKCS#12 format.' >&2; exit 1;
    }
fi
openssl x509 -in "$WORK/cert.pem" -outform DER -out "$WORK/cert.der"
python3 - "$WORK" <<'PY'
import datetime, hashlib, pathlib, plistlib, sys
w = pathlib.Path(sys.argv[1])
p = plistlib.loads((w / 'profile.plist').read_bytes())
e = p['Entitlements']
cert = (w / 'cert.der').read_bytes()
if cert not in p.get('DeveloperCertificates', []):
    sys.exit('P12 certificate does not match this provisioning profile.')
if p['ExpirationDate'] <= datetime.datetime.now(datetime.timezone.utc).replace(tzinfo=None):
    sys.exit('Provisioning profile has expired.')
identity = e.get('application-identifier', '')
if '.' not in identity or '*' in identity:
    sys.exit('This script requires an explicit application-identifier.')
if 'packet-tunnel-provider' not in e.get('com.apple.developer.networking.networkextension', []):
    sys.exit('Profile does not authorize packet-tunnel-provider.')
(w / 'bundle-id').write_text(identity.split('.', 1)[1])
(w / 'identity').write_text(hashlib.sha1(cert).hexdigest().upper())
# Deliberately reproduce the tested signer: same authorized entitlements on both targets.
(w / 'entitlements.plist').write_bytes(plistlib.dumps(e))
PY
BUNDLE_ID=$(cat "$WORK/bundle-id")
IDENTITY=$(cat "$WORK/identity")
ditto -x -k "$INPUT" "$WORK/unpacked"
python3 - "$WORK" "$BUNDLE_ID" "$PROFILE_FILE" <<'PY'
import pathlib, plistlib, shutil, sys
w, bid, profile = pathlib.Path(sys.argv[1]), sys.argv[2], pathlib.Path(sys.argv[3])
apps = [p for p in (w / 'unpacked' / 'Payload').glob('*.app') if p.is_dir()]
if len(apps) != 1:
    sys.exit('Expected exactly one app in Payload.')
app = apps[0]
ext = app / 'PlugIns' / 'PacketTunnel.appex'
if not ext.is_dir():
    sys.exit('FnCPN PacketTunnel.appex is missing.')
for b, new_id in [(app, bid), (ext, bid + '.PacketTunnel')]:
    f = b / 'Info.plist'
    info = plistlib.loads(f.read_bytes())
    if info.get('FnCPNAppGroup'):
        sys.exit('App Groups build detected; use the no-App-Groups build.')
    info['CFBundleIdentifier'] = new_id
    f.write_bytes(plistlib.dumps(info, fmt=plistlib.FMT_BINARY))
    (b / 'embedded.mobileprovision').unlink(missing_ok=True)
shutil.copyfile(profile, app / 'embedded.mobileprovision')
(w / 'app-path').write_text(str(app))
PY
APP=$(cat "$WORK/app-path")
KEYCHAIN_PASSWORD=$(openssl rand -hex 24)
security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN" >/dev/null
security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
# security import accepts the password as an argument; never enable shell tracing.
security import "$P12_FILE" -k "$KEYCHAIN" -P "$PASSWORD" -T /usr/bin/codesign >/dev/null
unset PASSWORD
security set-key-partition-list -S 'apple-tool:,apple:,codesign:' -s \
    -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN" >/dev/null
# Frameworks and dylibs need their own signatures, without app entitlements.
while IFS= read -r -d '' nested; do
    codesign --force --sign "$IDENTITY" --keychain "$KEYCHAIN" --timestamp=none "$nested"
done < <(find "$APP" -depth \( -type d -name '*.framework' -o -type f -name '*.dylib' \) -print0)
for target in "$APP/PlugIns/PacketTunnel.appex" "$APP"; do
    codesign --force --sign "$IDENTITY" --keychain "$KEYCHAIN" --timestamp=none \
        --entitlements "$WORK/entitlements.plist" --generate-entitlement-der "$target"
    codesign --verify --strict "$target"
done
ditto -c -k --norsrc --keepParent "$WORK/unpacked/Payload" "$WORK/signed.ipa"
mkdir -p "$(dirname "$OUTPUT")"
# Exclusive creation protects existing output even if another process creates it meanwhile.
python3 - "$WORK/signed.ipa" "$OUTPUT" <<'PY'
import shutil, sys
with open(sys.argv[1], 'rb') as source, open(sys.argv[2], 'xb') as dest:
    shutil.copyfileobj(source, dest)
PY
printf 'Signed IPA: %s\nMain Bundle ID: %s\nExtension Bundle ID: %s.PacketTunnel\n' "$OUTPUT" "$BUNDLE_ID" "$BUNDLE_ID"
echo 'Signatures verified. Device installation and tunnel operation require separate validation.'
