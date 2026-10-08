package packaging

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLocalizationResourcesAreCurrent(t *testing.T) {
	command := exec.Command("python3", "../scripts/sync-localizations.py", "--check")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("localization resources: %v\n%s", err, output)
	}
}

func TestMacOSLocalizationRuntime(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires Foundation on macOS")
	}
	source, err := os.ReadFile("../platform/macos/FnCPNApp.swift")
	if err != nil {
		t.Fatal(err)
	}
	helper, _, ok := strings.Cut(string(source), "private let ipcProtocolVersion")
	if !ok {
		t.Fatal("missing localization helper")
	}
	directory := filepath.Join(t.TempDir(), "LocalizationCheck.app", "Contents")
	resources := filepath.Join(directory, "Resources")
	if err := os.MkdirAll(filepath.Join(directory, "MacOS"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(resources, 0700); err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"en", "zh-Hans"} {
		if err := os.CopyFS(filepath.Join(resources, language+".lproj"), os.DirFS(filepath.Join("macos", language+".lproj"))); err != nil {
			t.Fatal(err)
		}
	}
	plist, err := os.ReadFile("macos/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "Info.plist"), plist, 0600); err != nil {
		t.Fatal(err)
	}
	program := helper + `
@main private enum LocalizationCheck {
 static func main() {
  let values = [L("连接"), L("IPv6 候选 {0}，已尝试 {1}", "NAS {1} 🐮", "2"),
                L("missing-key"), Bundle.main.localizedInfoDictionary?["NSLocalNetworkUsageDescription"] as? String ?? ""]
  let data = try! JSONSerialization.data(withJSONObject: values)
  print(String(data: data, encoding: .utf8)!)
 }
}
`
	path := filepath.Join(directory, "Check.swift")
	if err := os.WriteFile(path, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "MacOS", "FnCPN")
	if output, err := exec.Command("swiftc", "-parse-as-library", path, "-o", binary).CombinedOutput(); err != nil {
		t.Fatalf("compile localization check: %v\n%s", err, output)
	}
	for _, language := range []string{"en", "zh-Hans", "fr"} {
		t.Run(language, func(t *testing.T) {
			output, err := exec.Command(binary, "-AppleLanguages", "("+language+")").CombinedOutput()
			if err != nil {
				t.Fatalf("run localization check: %v\n%s", err, output)
			}
			var values []string
			if err := json.Unmarshal(output, &values); err != nil {
				t.Fatalf("decode output: %v\n%s", err, output)
			}
			want := "Connect"
			permission := "FnCPN needs local network access"
			if language == "zh-Hans" {
				want = "连接"
				permission = "FnCPN 需要访问局域网"
			}
			if len(values) != 4 || values[0] != want || !strings.Contains(values[1], "NAS {1} 🐮") ||
				!strings.Contains(values[1], "2") || values[2] != "missing-key" || !strings.HasPrefix(values[3], permission) {
				t.Fatalf("unexpected localized values: %#v", values)
			}
		})
	}
}
