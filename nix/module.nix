{ self }:
{ config, lib, pkgs, ... }:
let
  cfg = config.services.fncpn;
  inherit (lib) mkOption mkEnableOption types mkIf;
  known = name: builtins.hasAttr name config.users.users;
  valid = name: known name && config.users.users.${name}.isNormalUser
    && config.users.users.${name}.uid != null && config.users.users.${name}.uid >= 1000 && config.users.users.${name}.uid <= 2147483647;
  users = builtins.filter valid cfg.users;
  uids = map (name: toString config.users.users.${name}.uid) users;
  core = cfg.package;
  rootService = uid: {
    name = "fncpn-client-privileged@${uid}";
    value = {
      description = "FnCPN privileged client network service for UID ${uid}";
      after = [ "network.target" "dbus.service" ] ++ lib.optional cfg.dns.enable "systemd-resolved.service";
      wants = lib.optional cfg.dns.enable "systemd-resolved.service";
      wantedBy = [ "multi-user.target" ];
      path = [ pkgs.systemd pkgs.coreutils pkgs.glibc.bin ];
      serviceConfig = {
        Type = "exec";
        ExecStart = "${core}/bin/fncpn client privileged-daemon --allow-uid ${uid} --socket /run/fncpn/${uid}/privileged.sock --state-dir /var/lib/fncpn/client/${uid} --log-file /var/log/fncpn/client-privileged-${uid}.log";
        ExecStartPost = "${core}/libexec/fncpn/wait-ready client-privileged /run/fncpn/${uid}/privileged.sock";
        Restart = "on-failure";
        RestartSec = 3;
        TimeoutStopSec = 20;
        RuntimeDirectory = "fncpn/${uid}";
        RuntimeDirectoryMode = "0711";
        StateDirectory = "fncpn/client/${uid}";
        StateDirectoryMode = "0700";
        LogsDirectory = "fncpn";
        UMask = "0077";
        NoNewPrivileges = true;
        PrivateTmp = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictSUIDSGID = true;
        CapabilityBoundingSet = [ "CAP_NET_ADMIN" "CAP_NET_BIND_SERVICE" ];
      };
    };
  };
in {
  options.services.fncpn = {
    enable = mkEnableOption "FnCPN client services";
    package = mkOption { type = types.package; default = self.packages.${pkgs.stdenv.hostPlatform.system}.core; description = "FnCPN Core package."; };
    users = mkOption { type = types.listOf types.str; default = []; description = "Explicit regular users with stable UIDs (at least 1000) allowed to run the client."; };
    ui.enable = mkOption { type = types.bool; default = true; description = "Install the Wails desktop client."; };
    ui.package = mkOption { type = types.package; default = self.packages.${pkgs.stdenv.hostPlatform.system}.ui; description = "Matching FnCPN UI package."; };
    dns.enable = mkOption { type = types.bool; default = true; description = "Enable systemd-resolved for split DNS. Disabling this leaves DNS degraded on unmanaged resolvers."; };
  };
  config = mkIf cfg.enable {
    assertions = [
      { assertion = cfg.users != [] && lib.all valid cfg.users; message = "services.fncpn.users must name existing regular users with explicit UIDs >= 1000."; }
      { assertion = builtins.length cfg.users == builtins.length (lib.unique cfg.users) && builtins.length uids == builtins.length (lib.unique uids); message = "FnCPN users and UIDs must be unique."; }
      { assertion = !cfg.ui.enable || cfg.ui.package.version == core.version; message = "FnCPN Core and UI must have matching product versions."; }
      { assertion = !cfg.dns.enable || config.services.resolved.enable; message = "FnCPN split DNS requires services.resolved.enable."; }
    ];
    environment.systemPackages = [ core ] ++ lib.optional cfg.ui.enable cfg.ui.package;
    security.polkit.enable = true;
    services.resolved.enable = lib.mkIf cfg.dns.enable (lib.mkDefault true);
    systemd.services = builtins.listToAttrs (map rootService uids);
    systemd.user.services.fncpn-client = {
      description = "FnCPN client coordinator";
      after = [ "network.target" ];
      wantedBy = [ "default.target" ];
      unitConfig.ConditionUser = map (name: "|${name}") users;
      serviceConfig = {
        Type = "exec";
        ExecStartPre = "${pkgs.coreutils}/bin/mkdir -p %h/.config/fncpn %h/.local/state/fncpn";
        ExecStart = "${core}/bin/fncpn client daemon";
        ExecStartPost = "${core}/libexec/fncpn/wait-ready client %t/fncpn/client.sock";
        Restart = "on-failure";
        RestartSec = 3;
        TimeoutStopSec = 20;
        RuntimeDirectory = "fncpn";
        RuntimeDirectoryMode = "0700";
        UMask = "0077";
        NoNewPrivileges = true;
        PrivateTmp = true;
        ProtectSystem = "full";
      };
    };
  };
}
