#!/usr/bin/env bash
# Shared by both package builders. VERSION is never independently overridden.
PRODUCT_VERSION="$(tr -d '\r\n' < "${ROOT_DIR}/internal/version/VERSION")"
if [ -n "${VERSION:-}" ] && [ "$VERSION" != "$PRODUCT_VERSION" ]; then
  echo "VERSION must match internal/version/VERSION ($PRODUCT_VERSION)" >&2
  exit 1
fi
VERSION="$PRODUCT_VERSION"
if ! [[ "$VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "Invalid product version: $VERSION" >&2
  exit 1
fi
PACKAGE_VERSION="${VERSION}"
python3 "${ROOT_DIR}/scripts/sync-version.py" --check
REVISION="$(git -C "$ROOT_DIR" rev-parse HEAD)"
BUILD_NUMBER="${BUILD_NUMBER:-$(git -C "$ROOT_DIR" rev-list --count HEAD)}"
if [ "${RELEASE:-0}" = 1 ]; then
  if [ -n "$(git -C "$ROOT_DIR" status --porcelain)" ]; then
    echo "Release requires a clean checkout" >&2; exit 1
  fi
  (cd "$ROOT_DIR" && python3 scripts/check-release-candidate.py) || exit 1
fi
