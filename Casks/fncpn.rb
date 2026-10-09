cask "fncpn" do
  version "1.0.0"
  sha256 "b6cf2b05be21e5975600a2c435763410063d10e1904db9585538fde198dc0331"

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
