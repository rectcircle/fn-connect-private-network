# Installing and signing on iPhone / iPad

[简体中文](ios-installation.zh-CN.md)

Starting with 1.1.5, FnCPN provides an unsigned ARM64 IPA for iOS/iPadOS. The build targets
 iOS 15.0 or later. Device verification is limited to the maintainer's hardware; see the
[validation record](validation.md) for its scope.

## Why the IPA cannot be installed directly

Apple requires device code signing and authorization for system capabilities. The maintainer
currently has no paid Apple Developer Program subscription. FnCPN is therefore not distributed
through the App Store or TestFlight; releases contain only an unsigned IPA that cannot be installed directly.

FnCPN uses a Packet Tunnel extension. Its provisioning must authorize
`packet-tunnel-provider` under `com.apple.developer.networking.networkextension`.
Both the app and its embedded PacketTunnel extension need appropriate authorization.
An ordinary free-account signing workflow is not a working installation route for this project;
a service that can sign a regular IPA may still be unable to run FnCPN.
See Apple's [Network Extension entitlement](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.developer.networking.networkextension)
and [device distribution methods](https://help.apple.com/xcode/mac/current/en.lproj/dev31de635e5.html).

## Certificate services verified by the maintainer

Without your own paid developer team, one tested route is a third-party device registration
and development/testing certificate service tied to your device's UDID, sometimes marketed
as “super signing.” This purchases device registration and signing materials, not an Apple
Developer Program membership in your own name.

| Service | Price paid during maintainer testing | Verification |
|---|---|---|
| [DxSign](https://dxsign.cc/) | US$2 | Re-signing, installation and connection verified |
| [KravaSign](https://www.kravasign.com/) | US$9.90 | Re-signing, installation and connection verified |

The maintainer has no financial interest in or commercial relationship with DxSign or KravaSign.
They are listed only to document the services tested with this project.

These are historical test prices, not guaranteed current prices. Check the current plan before
buying. KravaSign advertises VPN entitlements; inspect the delivered profile for the specific
entitlement above. Other providers have not been verified with FnCPN.

Certificates are subject to expiry, device binding and revocation by Apple or the provider.
These are third-party services, not Apple services; the project neither sells certificates nor
guarantees their lifetime. Confirm Network Extension support, registration of your UDID, and
access to a P12 private key and matching provisioning profile. Never commit keys or passwords,
post them in issues, or include them in diagnostics.

## Prerequisites

Use a Mac with full Xcode (`devicectl` is used below), Python 3, OpenSSL and the system signing
tools. Unlock the device and trust the Mac; development signing also requires Developer Mode.
USB is recommended for the first connection. Wireless installation requires prior pairing
and network reachability.

1. Follow the provider's UDID registration instructions and obtain a certificate for your device.
   Device Hub's device information can help verify the UDID.
2. Download the matching `.p12`, `.mobileprovision` and P12 password. If several certificate types
   are supplied, use the matching Development files rather than mixing development/distribution materials.
3. Download `fncpn-<version>-ios-arm64-unsigned.ipa` from
   [GitHub Releases](https://github.com/rectcircle/fn-connect-private-network/releases)
   and verify it against that release's SHA256SUMS.
4. Obtain the source matching the IPA release, including `scripts/sign-ios.sh`.

Store signing materials outside the repository, for example:

```text
~/.ios/signing/kravasign/
  password
  Development.p12
  Development.mobileprovision
```

The password file contains one line. Create it with a local editor; do not put the password in
shell history. Set directory permissions to `700` and file permissions to `600`.

## Sign the IPA

Run from the repository root, substituting the actual version and paths:

```sh
bash scripts/sign-ios.sh \
  ~/.ios/signing/kravasign/password \
  ~/.ios/signing/kravasign/Development.p12 \
  ~/.ios/signing/kravasign/Development.mobileprovision \
  ~/Downloads/fncpn-1.1.5-ios-arm64-unsigned.ipa \
  ~/Downloads/fncpn-1.1.5-ios-arm64-signed.ipa
```

For DxSign, use the same command with its material paths. The script checks certificate/profile
matching, profile expiry and Packet Tunnel entitlement, uses a temporary keychain, and removes
it afterwards. It refuses to overwrite an existing output.

This script reproduces the project's verified single-profile personal-device signing flow,
setting the app ID and its `.PacketTunnel` extension ID. It is not a general App Store export
tool and does not guarantee compatibility with arbitrary provider materials. Signature verification
does not prove installation or VPN operation. The script does not install the app.

## Install and launch

```sh
xcrun devicectl list devices
```

Copy the device Identifier and substitute it for `DEVICE_ID`. Extract the signed IPA into a new
temporary directory:

```sh
install_dir=$(mktemp -d "${TMPDIR:-/tmp}/fncpn-install.XXXXXX")
ditto -x -k ~/Downloads/fncpn-1.1.5-ios-arm64-signed.ipa "$install_dir"
xcrun devicectl device install app --device DEVICE_ID "$install_dir/Payload/FnCPN.app"
/usr/libexec/PlistBuddy -c 'Print CFBundleIdentifier' "$install_dir/Payload/FnCPN.app/Info.plist"
```

Use the actual bundle ID printed above; re-signing can change the original project ID:

```sh
xcrun devicectl device process launch --device DEVICE_ID YOUR_SIGNED_BUNDLE_ID
rm -rf "$install_dir"
```

Alternatively, install the extracted app and launch it using the paired device in Device Hub.
Follow iOS prompts for developer trust, Developer Mode and VPN configuration approval as applicable
to the signing type and OS version. Sign in, check connection status, then open the NAS in Safari
to verify the tunnel, rather than only checking that the app launches.

## Updates and troubleshooting

- Keep the same team, app ID and entitlements for an in-place update. Changing team or ID can
  install a separate app; Keychain continuity is not guaranteed. Keep the old app until the new one works.
- Missing `packet-tunnel-provider`: obtain an authorized profile from the provider. Do not strip
  the extension or entitlements merely to make installation succeed.
- Signing succeeds but installation fails: check device UDID registration, profile/certificate validity,
  device unlock and trust.
- The app launches but VPN fails: inspect extension authorization, VPN approval and diagnostics.
  Signing an ordinary IPA does not prove Packet Tunnel capability.
- Expired or revoked certificate: obtain replacement materials and re-sign; recompilation is unnecessary.

See [development documentation](development.md#ios-开发环境准备) for builds, simulator use and releases.
