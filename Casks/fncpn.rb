cask "fncpn" do
  version "1.1.2"
  arch arm: "arm64", intel: "x64"
  sha256 arm: "0f650dc709f1341f80b01166053f82cd417c5a7d3c6c2fe9cdc8ab011fd56a72", intel: "a85f589eb48a7fb1edb5acf6cfe333738d29698822224e84b9e33959101c72ea"

  url "https://github.com/rectcircle/fn-connect-private-network/releases/download/v#{version}/fncpn-#{version}-macos-#{arch}-unsigned.pkg"
  name "FnCPN"
  desc "Private networking for fnOS through FN Connect"
  homepage "https://fncpn.rectcircle.cn"

  depends_on macos: :ventura

  pkg "fncpn-#{version}-macos-#{arch}-unsigned.pkg"

  uninstall script: {
    executable: "/Library/PrivilegedHelperTools/cn.rectcircle.fncpn/uninstall.sh",
    sudo: true,
  },
  pkgutil: "cn.rectcircle.fncpn"

  caveats <<~EOS
    The app and helper are ad-hoc signed. The PKG is unsigned and is not notarized.
    The maintainer does not have an Apple Developer account.
    Homebrew uses the system command-line installer with administrator privileges.
    No manual PKG opening or quarantine removal is required as a preparation step.
    If macOS blocks installation or launch, follow its security approval prompts.
    If you do not trust the prebuilt artifacts, review the source and build locally.
    Uninstall retains user configuration and credentials. Purge is a separate manual operation.
  EOS
end
