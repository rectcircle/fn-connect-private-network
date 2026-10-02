#!/bin/sh

set -eu

TOOL_DIR="/Library/PrivilegedHelperTools/com.rectcircle.fncpn"
TOOL="${TOOL_DIR}/fncpn"
AGENT_LABEL="com.rectcircle.fncpn.client"
DAEMON_LABEL="com.rectcircle.fncpn.privileged"
STATE_DIR="/var/db/fncpn"
PURGE_UID=""

if [ "$(/usr/bin/id -u)" -ne 0 ]; then
  echo "Run as root: sudo $0 [--purge-user-data UID]" >&2
  exit 1
fi
if [ "${1:-}" = "--purge-user-data" ]; then
  PURGE_UID="${2:-}"
  case "$PURGE_UID" in
    ''|*[!0-9]*|0) echo "A numeric non-root UID is required" >&2; exit 2 ;;
  esac
elif [ "$#" -ne 0 ]; then
  echo "Usage: sudo $0 [--purge-user-data UID]" >&2
  exit 2
fi

# Stop every loaded user agent whose executable is the installed FnCPN binary.
/bin/ps -axo uid=,pid=,command= | while read -r uid pid command; do
  case "$command" in
    "$TOOL client daemon"*)
      /bin/launchctl bootout "gui/${uid}/${AGENT_LABEL}" 2>/dev/null || true
      /bin/kill -TERM "$pid" 2>/dev/null || true
      ;;
  esac
done

/bin/launchctl bootout "system/${DAEMON_LABEL}" 2>/dev/null || true
if ! "$TOOL" internal-cleanup --role client --state-dir "$STATE_DIR"; then
  echo "FnCPN network cleanup failed; system files and journal were retained" >&2
  exit 1
fi

if [ -n "$PURGE_UID" ]; then
  USER_NAME="$(/usr/bin/dscl . -search /Users UniqueID "$PURGE_UID" |
    /usr/bin/awk 'NR == 1 {print $1}')"
  if [ -z "$USER_NAME" ]; then
    echo "No local user found for UID ${PURGE_UID}" >&2
    exit 1
  fi
  HOME_DIR="$(/usr/bin/dscl . -read "/Users/${USER_NAME}" NFSHomeDirectory |
    /usr/bin/awk '{print $2}')"
  if ! /bin/launchctl asuser "$PURGE_UID" /usr/bin/sudo -u "$USER_NAME" \
    /usr/bin/env HOME="$HOME_DIR" "$TOOL" internal-purge-user; then
    echo "User data purge failed; user data was retained" >&2
    exit 1
  fi
  /bin/rm -rf "${STATE_DIR}/credentials/${PURGE_UID}"
else
  echo "FnCPN user configuration, logs, and root-owned credentials were retained"
fi

/bin/rm -f /var/run/fncpn-client-privileged.sock
/bin/rm -f /usr/local/bin/fncpn
/bin/rm -f /Library/LaunchAgents/com.rectcircle.fncpn.client.plist
/bin/rm -f /Library/LaunchDaemons/com.rectcircle.fncpn.privileged.plist
/bin/rm -rf /Applications/FnCPN.app
/bin/rm -f "${STATE_DIR}/owner.lock"
# Network cleanup removed its journal; preserve credentials for every other user.
/bin/rmdir "${STATE_DIR}/credentials" "$STATE_DIR" 2>/dev/null || true
/bin/rm -rf /var/log/fncpn
/bin/rm -rf "$TOOL_DIR"

exit 0
