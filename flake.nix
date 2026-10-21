{
  description = "FnCPN Core, Wails UI and declarative NixOS client services";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      lib = nixpkgs.lib;
      linuxSystems = [ "x86_64-linux" "aarch64-linux" ];
      devSystems = linuxSystems ++ [ "x86_64-darwin" "aarch64-darwin" ];
      forSystems = systems: f: lib.genAttrs systems (system: f (import nixpkgs { inherit system; }));
      version = lib.removeSuffix "\n" (builtins.readFile ./internal/version/VERSION);
      revision = self.rev or (self.dirtyRev or "unknown");
      # Keep generated files and local dependency/build caches out of the source.
      source = lib.cleanSourceWith {
        src = ./.;
        filter = path: type:
          let name = builtins.baseNameOf path;
          in !(builtins.elem name [ ".git" ".dev" "dist" "node_modules" "vendor" "__pycache__" ])
            && lib.cleanSourceFilter path type;
      };
    in {
      packages = forSystems linuxSystems (pkgs:
        let packages = import ./nix/packages.nix { inherit pkgs source version revision; };
        in packages // { default = packages.core; });

      apps = forSystems linuxSystems (pkgs:
        let packages = self.packages.${pkgs.stdenv.hostPlatform.system};
        in {
          default = { type = "app"; program = "${packages.core}/bin/fncpn"; };
          core = { type = "app"; program = "${packages.core}/bin/fncpn"; };
          ui = { type = "app"; program = "${packages.ui}/bin/fncpn-ui"; };
        });

      overlays.default = final: prev: {
        fncpn-core = self.packages.${prev.stdenv.hostPlatform.system}.core;
        fncpn-ui = self.packages.${prev.stdenv.hostPlatform.system}.ui;
      };

      nixosModules.default = import ./nix/module.nix { inherit self; };
      nixosModules.fncpn = self.nixosModules.default;

      devShells = forSystems devSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [ pkgs.go_1_27 pkgs.nodejs_22 pkgs.python3 pkgs.pkg-config pkgs.nixfmt ]
            ++ lib.optionals pkgs.stdenv.isLinux [
              pkgs.gtk3 pkgs.webkitgtk_4_1 pkgs.libayatana-appindicator pkgs.systemd
            ];
          CGO_ENABLED = "1";
        };
      });
      formatter = forSystems devSystems (pkgs: pkgs.nixfmt);

      checks = forSystems linuxSystems (pkgs:
        let
          packages = self.packages.${pkgs.stdenv.hostPlatform.system};
          machine = lib.nixosSystem {
            system = pkgs.stdenv.hostPlatform.system;
            modules = [
              self.nixosModules.default
              ({ ... }: {
                system.stateVersion = "26.05";
                users.users.fncpn-test = { isNormalUser = true; uid = 1000; };
                services.fncpn = { enable = true; users = [ "fncpn-test" ]; };
                boot.loader.grub.enable = false;
                fileSystems."/" = { device = "/dev/test"; fsType = "ext4"; };
              })
            ];
          };
          failures = builtins.filter (item: !item.assertion) machine.config.assertions;
          service = machine.config.systemd.services."fncpn-client-privileged@1000";
        in {
          inherit (packages) core ui;
          module = assert failures == [ ];
            assert service.serviceConfig.ExecStart != "";
            assert machine.config.services.resolved.enable;
            assert machine.config.security.polkit.enable;
            pkgs.runCommand "fncpn-module-check" { } ''
              test -f ${packages.core}/share/polkit-1/actions/cn.rectcircle.fncpn.enable-client.policy
              test -x ${packages.core}/libexec/fncpn/enable-client
              mkdir -p "$out"
              printf '%s\n' '${version}' > "$out/version"
            '';
        });
    };
}
