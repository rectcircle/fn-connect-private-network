cask "fncpn" do
  version "1.0.6"
  arch arm: "arm64", intel: "x64"
  sha256 arm: "dd9a9733f2fd3985bf2c4874d0f2ee874829f85f0532a562b8b41938d79e4c25", intel: "15f6a702bc844098d13a6fa14db693042d8576e70ded190319f69f3f4cc379b1"

  url "https://github.com/rectcircle/fn-connect-private-network/releases/download/v#{version}/fncpn-#{version}-macos-#{arch}-unsigned.pkg"
  name "FnCPN"
  desc "Private networking for fnOS through FN Connect"
  homepage "https://github.com/rectcircle/fn-connect-private-network"

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
