#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT HUP INT TERM
# Intentional pkg-config word splitting supplies compiler/linker flags.
cc -std=c11 -Wall -Wextra -Werror -I"$root/ui" \
  "$root/ui/activation_linux.c" "$root/scripts/tests/linux-activation.c" \
  $(pkg-config --cflags --libs gio-2.0) -o "$stage/activation-test"
"$stage/activation-test"
