#!/bin/bash
# Same signed distribution/version/architecture; ordered HTTPS mirror fallbacks.
set -euo pipefail
primary=https://mirrors.huaweicloud.com/openeuler
secondary=https://mirrors.ustc.edu.cn/openeuler
origin=https://repo.openeuler.org
if [ "${1:-}" = --probe ]; then
 for root in "$primary" "$secondary" "$origin"; do
  url="$root/openEuler-24.03-LTS-SP4/everything/loongarch64/repodata/repomd.xml"
  echo "Checking openEuler repository: $url"
  if curl -4 --fail --silent --show-error --location --connect-timeout 6 --max-time 15 "$url" -o "$2"; then
   if python3 -c 'import sys,xml.etree.ElementTree as E; assert E.parse(sys.argv[1]).getroot().tag == "{http://linux.duke.edu/metadata/repo}repomd"' "$2"; then exit 0; fi
  fi
 done
 echo 'No valid openEuler repository metadata available' >&2
 exit 1
fi
config="${1:-/etc/yum.repos.d/openEuler.repo}"
output=$(mktemp)
trap 'rm -f "$output"' EXIT
awk -v primary="$primary" -v secondary="$secondary" -v origin="$origin" '
 /^[ \t]*(metalink|mirrorlist)[ \t]*=/ { next }
 /^[ \t]*baseurl[ \t]*=/ {
  suffix=$0
  sub(/^[ \t]*baseurl[ \t]*=[ \t]*/, "", suffix)
  if (suffix ~ /^https?:\/\/(repo[.]openeuler[.]org|repo[.]openeuler[.]openatom[.]cn)\//) {
   sub(/^https?:\/\/[^\/]+\//, "", suffix)
  } else if (index(suffix, primary "/") == 1) {
   suffix=substr(suffix, length(primary)+2)
  } else if (index(suffix, secondary "/") == 1) {
   suffix=substr(suffix, length(secondary)+2)
  } else { print; continuation=0; next }
  sub(/[ \t].*$/, "", suffix)
  printf "baseurl=%s/%s\n        %s/%s\n        %s/%s\n", primary,suffix,secondary,suffix,origin,suffix
  continuation=1
  next
 }
 /^[ \t]+https?:\/\// { if (continuation) next }
 { continuation=0; if ($0 ~ /^[ \t]*gpgkey[ \t]*=/) gsub(/http:\/\//, "https://"); print }
' "$config" > "$output"
# Keep the existing file owner/mode and every signature-checking setting.
cat "$output" > "$config"
