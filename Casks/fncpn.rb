cask "fncpn" do
  version "1.0.8"
  arch arm: "arm64", intel: "x64"
  sha256 arm: "c6cc9e66d07287b3fac69b33bf24481e4657b85f03b457e707243e0301fc426d", intel: "94460b255dcede1200e7db9b59e7536c3bcbe7461ea49108c8cd0b84856e3500"

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
