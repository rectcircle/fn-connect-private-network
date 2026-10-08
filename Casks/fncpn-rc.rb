cask "fncpn-rc" do
  version "1.0.0-rc.1"
  sha256 "4fb94d3dfe34f138c2cb0e311d4e4a90c0ee639170abe960ff5f357b5002cbcc"

  url "https://github.com/rectcircle/fn-connect-private-network/releases/download/v#{version}/FnCPN-#{version}-arm64-unsigned.pkg"
  name "FnCPN"
  desc "Private networking for fnOS through FN Connect"
  homepage "https://github.com/rectcircle/fn-connect-private-network"

  depends_on arch: :arm64
  depends_on macos: :ventura
  conflicts_with cask: "fncpn"

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
