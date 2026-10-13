cask "fncpn" do
  version "1.0.7"
  arch arm: "arm64", intel: "x64"
  sha256 arm: "ad0f1394cddaff58e501e2411d56ddafe3d151773ed3fe567effeb0eda442c9e", intel: "7bc22483787b73b6f6113fcbb658bac68328d575068c54b93e3c6c908669ff74"

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
