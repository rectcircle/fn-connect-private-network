{ pkgs, source, version, revision }:
let
  inherit (pkgs) lib;
  hashes = builtins.fromJSON (builtins.readFile ./go-hashes.json);
  goBuilder = pkgs.buildGo127Module;
  commonMeta = {
    homepage = "https://fncpn.rectcircle.cn";
    license = lib.licenses.mit;
    platforms = [ "x86_64-linux" "aarch64-linux" ];
  };
  runtimePath = lib.makeBinPath [ pkgs.systemd pkgs.coreutils pkgs.glibc.bin ];
  frontend = pkgs.buildNpmPackage {
    pname = "fncpn-frontend";
    inherit version;
    src = source + "/apps/shared/wails/frontend";
    nodejs = pkgs.nodejs_22;
    npmDeps = pkgs.importNpmLock { npmRoot = source + "/apps/shared/wails/frontend"; };
    npmConfigHook = pkgs.importNpmLock.npmConfigHook;
    installPhase = ''
      runHook preInstall
      mkdir -p "$out"
      cp -r dist/. "$out/"
      runHook postInstall
    '';
  };
  core = goBuilder {
    pname = "fncpn-core";
    inherit version;
    src = source;
    vendorHash = hashes.core;
    subPackages = [ "cmd/fncpn" ];
    env.CGO_ENABLED = "0";
    ldflags = [ "-s" "-w" "-X github.com/rectcircle/fn-connect-private-network/internal/version.Revision=${revision}" ];
    nativeBuildInputs = [ pkgs.makeWrapper ];
    postInstall = ''
      mkdir -p "$out/libexec/fncpn" "$out/share/polkit-1/actions" "$out/lib/udev/rules.d"
      cp apps/linux/packaging/udev/85-fncpn.rules "$out/lib/udev/rules.d/"
      install -m755 apps/linux/packaging/scripts/enable-client apps/linux/packaging/scripts/wait-ready "$out/libexec/fncpn/"
      install -m755 apps/linux/packaging/scripts/fncpn-linux-setup "$out/bin/"
      # NixOS modules enable units declaratively. The helper only starts an
      # already configured instance and never edits /etc/systemd/system.
      substituteInPlace "$out/libexec/fncpn/enable-client" \
        --replace-fail 'exec systemctl enable --now' 'exec systemctl start'
      substituteInPlace "$out/libexec/fncpn/wait-ready" \
        --replace-fail /usr/bin/fncpn "$out/bin/fncpn"
      substituteInPlace "$out/bin/fncpn-linux-setup" \
        --replace-fail /usr/libexec/fncpn/enable-client "$out/libexec/fncpn/enable-client" \
        --replace-fail 'enable --now fncpn-client.service' 'start fncpn-client.service'
      cp apps/linux/packaging/polkit/cn.rectcircle.fncpn.enable-client.policy "$out/share/polkit-1/actions/"
      substituteInPlace "$out/share/polkit-1/actions/cn.rectcircle.fncpn.enable-client.policy" \
        --replace-fail /usr/libexec/fncpn/enable-client "$out/libexec/fncpn/enable-client"
      patchShebangs "$out/bin" "$out/libexec"
      wrapProgram "$out/bin/fncpn" --prefix PATH : ${lib.escapeShellArg runtimePath}
      wrapProgram "$out/libexec/fncpn/enable-client" --prefix PATH : ${lib.escapeShellArg runtimePath}
      wrapProgram "$out/libexec/fncpn/wait-ready" --prefix PATH : ${lib.escapeShellArg runtimePath}
      # Keep the host's setuid sudo wrapper ahead of ordinary store binaries.
      wrapProgram "$out/bin/fncpn-linux-setup" --suffix PATH : ${lib.escapeShellArg runtimePath}
    '';
    doInstallCheck = true;
    installCheckPhase = ''
      "$out/bin/fncpn" version | grep -F '${version}'
    '';
    meta = commonMeta // { description = "FnCPN headless client and privileged service"; mainProgram = "fncpn"; };
  };
  ui = goBuilder {
    pname = "fncpn-ui";
    inherit version;
    src = source;
    modRoot = "apps/shared/wails";
    # Cache external module downloads, not a vendor tree containing replace => ../../..
    # project sources. The local module is compiled from the candidate checkout.
    proxyVendor = true;
    vendorHash = hashes.ui;
    subPackages = [ "." ];
    tags = [ "desktop" "production" "webkit2_41" ];
    env.CGO_ENABLED = "1";
    nativeBuildInputs = [ pkgs.pkg-config pkgs.wrapGAppsHook3 pkgs.makeWrapper ];
    buildInputs = [ pkgs.gtk3 pkgs.webkitgtk_4_1 pkgs.libayatana-appindicator ];
    ldflags = [
      "-s" "-w"
      "-X github.com/rectcircle/fn-connect-private-network/internal/version.Revision=${revision}"
      "-X main.linuxEnableClientPath=${core}/libexec/fncpn/enable-client"
      # PATH lookup survives system/profile upgrades and garbage collection.
      "-X main.linuxUIExecutable=fncpn-ui"
    ];
    preBuild = ''
      mkdir -p frontend/dist
      cp -r ${frontend}/. frontend/dist/
      chmod -R u+w frontend/dist
      ldflags+=( "-X=main.linuxStatusIconPath=$out/share/fncpn/icons" )
    '';
    postInstall = ''
      # The Go module's basename is ui; expose the stable product command.
      mv "$out/bin/ui" "$out/bin/fncpn-ui"
      mkdir -p "$out/share/applications" "$out/share/icons/hicolor/256x256/apps" "$out/share/fncpn/icons"
      cp ../../../apps/linux/packaging/desktop/fncpn.desktop "$out/share/applications/"
      substituteInPlace "$out/share/applications/fncpn.desktop" \
        --replace-fail /usr/bin/fncpn-ui "$out/bin/fncpn-ui"
      cp ../../../assets/branding/AppIcon.png "$out/share/icons/hicolor/256x256/apps/fncpn.png"
      cp ../../../apps/linux/packaging/icons/*-symbolic.svg "$out/share/fncpn/icons/"
      # pkexec must come from the host's privileged wrapper, not a non-setuid
      # polkit executable in the Nix store.
      gappsWrapperArgs+=( --suffix PATH : ${lib.escapeShellArg (lib.makeBinPath [ pkgs.systemd pkgs.xdg-utils pkgs.libnotify ])} )
    '';
    meta = commonMeta // { description = "FnCPN Wails v2 desktop client"; mainProgram = "fncpn-ui"; };
  };
in { inherit core ui frontend; }
