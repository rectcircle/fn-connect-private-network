#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
git diff --check
python3 scripts/ci_changes_test.py
python3 scripts/loongarch/validate_test.py
python3 scripts/loongarch/guest_commands_test.py
python3 scripts/nix_go_hashes_test.py
python3 scripts/fnos_systemd_test.py
python3 scripts/sync-version.py --check
python3 -X warn_default_encoding -W error::EncodingWarning scripts/sync-localizations.py --check
python3 scripts/sync-native-localizations.py --check
python3 scripts/rpm_signing_test.py
python3 scripts/install_linux_test.py
python3 scripts/android_release_test.py
python3 scripts/ios_release_test.py
python3 scripts/harmonyos_release_test.py
python3 -m unittest discover -s scripts/ios -p '*_test.py'
python3 -m unittest discover -s scripts/android -p '*_test.py'
python3 scripts/release_manifest_test.py
python3 scripts/release_candidate_test.py
python3 scripts/release_assets_test.py
python3 scripts/linux_acceptance_test.py
python3 scripts/publish_release_test.py
python3 scripts/quark_release_test.py
python3 scripts/update_cask_test.py
go test ./...
go test -race ./...
go vet ./...
(cd apps/shared/fyne && go test -tags ci ./... && go test -race -tags ci ./... && go vet -tags ci ./...)
node --test internal/server/web/index.test.cjs website/download-platform.test.mjs
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...
swiftc -target arm64-apple-macos13.0 -typecheck -parse-as-library -framework AppKit -framework ServiceManagement -framework UserNotifications -framework WebKit apps/macos/FnCPNApp.swift
