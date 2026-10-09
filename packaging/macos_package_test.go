package packaging

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestMacOSPackageArchiveOwnership(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires macOS packaging tools")
	}
	script, err := os.ReadFile(filepath.Join("..", "scripts", "build-macos-pkg.sh"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(script), "OUTPUT=\"")
	if start < 0 {
		t.Fatal("packaging stage was not found")
	}
	fragment := "set -euo pipefail\n" + string(script[start:])
	for _, test := range []struct {
		name, from, to, failure string
	}{
		{name: "root_archives"},
		{"reject_relocatable_app", `"BundleIsRelocatable": False`, `"BundleIsRelocatable": True`, "relocatable bundle found in built package"},
		{"reject_non_root_bom", `$3 = "0/0"`, `$3 = "12345/12345"`, "non-root owner found in built package BOM"},
		{"reject_non_root_payload", "--uid 0", "--uid 12345", "non-root owner found in built package payload"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			payload := filepath.Join(directory, "root")
			scripts := filepath.Join(directory, "scripts")
			dist := filepath.Join(directory, "dist")
			for _, path := range []string{
				filepath.Join(payload, "Library", "PrivilegedHelperTools"),
				filepath.Join(payload, "Applications", "FnCPN.app", "Contents", "MacOS"),
				filepath.Join(payload, "usr", "local", "bin"), scripts, dist,
			} {
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			appContents := filepath.Join(payload, "Applications", "FnCPN.app", "Contents")
			if err := os.WriteFile(filepath.Join(appContents, "Info.plist"), []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>cn.rectcircle.fncpn</string><key>CFBundleExecutable</key><string>FnCPN</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleShortVersionString</key><string>1.0.0</string><key>CFBundleVersion</key><string>1</string></dict></plist>`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(appContents, "MacOS", "FnCPN"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			relativeTool := "Library/PrivilegedHelperTools/test helper"
			tool := filepath.Join(payload, relativeTool)
			content := []byte("package ownership fixture\n")
			if err := os.WriteFile(tool, content, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(scripts, "preinstall"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/"+relativeTool, filepath.Join(payload, "usr", "local", "bin", "fncpn")); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(tool)
			if err != nil {
				t.Fatal(err)
			}
			run := fragment
			if test.from != "" {
				if !strings.Contains(run, test.from) {
					t.Fatalf("missing ownership operation %q", test.from)
				}
				run = strings.Replace(run, test.from, test.to, 1)
			}
			command := exec.Command("bash", "-c", run)
			command.Env = append(os.Environ(),
				"COPYFILE_DISABLE=1", "COPY_EXTENDED_ATTRIBUTES_DISABLE=1",
				"BUILD_DIR="+directory, "PAYLOAD="+payload, "SCRIPTS="+scripts,
				"DIST_DIR="+dist, "VERSION=1.0.0-rc.1", "PACKAGE_VERSION=1.0.0", "ARCH=fixture",
			)
			output, err := command.CombinedOutput()
			pkg := filepath.Join(dist, "FnCPN-1.0.0-rc.1-fixture-unsigned.pkg")
			if test.failure != "" {
				if err == nil || !bytes.Contains(output, []byte(test.failure)) {
					t.Fatalf("bad ownership was not rejected: %v\n%s", err, output)
				}
				if _, err := os.Stat(pkg); !os.IsNotExist(err) {
					t.Fatalf("invalid package was published: %v", err)
				}
				return
			}
			if err != nil || bytes.Contains(output, []byte("Permission denied")) {
				t.Fatalf("packaging failed or emitted a permission warning: %v\n%s", err, output)
			}
			after, err := os.Stat(tool)
			if err != nil {
				t.Fatal(err)
			}
			a, b := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
			if a.Uid != b.Uid || a.Gid != b.Gid || before.Mode() != after.Mode() {
				t.Fatal("packaging changed source ownership or mode")
			}
			expanded := filepath.Join(directory, "verify")
			packageCommand(t, "pkgutil", "--expand", pkg, expanded)
			info, err := os.ReadFile(filepath.Join(expanded, "PackageInfo"))
			if err != nil || !bytes.Contains(info, []byte(`version="1.0.0"`)) {
				t.Fatalf("PKG must use numeric core version: %s, %v", info, err)
			}
			bom := packageCommand(t, "lsbom", filepath.Join(expanded, "Bom"))
			for _, line := range strings.Split(strings.TrimSpace(string(bom)), "\n") {
				fields := strings.Split(line, "\t")
				if len(fields) < 3 || fields[2] != "0/0" {
					t.Fatalf("non-root BOM entry: %q", line)
				}
			}
			archive := filepath.Join(expanded, "Payload")
			list := packageCommand(t, "bsdtar", "-tv", "--numeric-owner", "-f", archive)
			for _, line := range strings.Split(strings.TrimSpace(string(list)), "\n") {
				fields := strings.Fields(line)
				if len(fields) < 4 || fields[2] != "0" || fields[3] != "0" {
					t.Fatalf("non-root payload entry: %q", line)
				}
			}
			extracted := packageCommand(t, "bsdtar", "-xOf", archive, "./"+relativeTool)
			if !bytes.Equal(extracted, content) {
				t.Fatal("packaging changed file contents")
			}
			if !bytes.Contains(bom, []byte("test helper")) || !bytes.Contains(list, []byte("-> /"+relativeTool)) {
				t.Fatal("filename with spaces or symbolic link was lost")
			}
		})
	}
}

func packageCommand(t *testing.T, name string, arguments ...string) []byte {
	t.Helper()
	output, err := exec.Command(name, arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, arguments, err, output)
	}
	return output
}
