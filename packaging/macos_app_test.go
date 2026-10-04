package packaging

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMacOSAppEntryPointInstallsDelegate(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the AppKit compiler is only available on macOS")
	}
	swift, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc is not installed")
	}
	// Inspect the compiler's entry point without launching a GUI or accessing
	// the development machine's installed app, daemons, or user state.
	command := exec.Command(swift,
		"-emit-silgen", "-parse-as-library", "-module-name", "FnCPNApp",
		"-framework", "AppKit",
		filepath.Join("..", "platform", "macos", "FnCPNApp.swift"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("compile AppKit entry point: %v\n%s", err, output)
	}
	entry := macOSAppSILFunction(t, string(output), "// static AppDelegate.main()")
	last := -1
	for _, operation := range []string{
		"AppDelegate.__allocating_init()",
		"#NSApplication.delegate!setter",
		"withExtendedLifetime",
	} {
		index := strings.Index(entry, operation)
		if index <= last {
			t.Fatalf("entry point must create, bind, and retain the delegate; missing or misplaced %q:\n%s", operation, entry)
		}
		last = index
	}
	loop := macOSAppSILFunction(t, string(output), "// closure #1 in static AppDelegate.main()")
	if !strings.Contains(loop, "NSApplicationMain") {
		t.Fatalf("delegate lifetime must include NSApplicationMain:\n%s", loop)
	}
}

func macOSAppSILFunction(t *testing.T, output, marker string) string {
	t.Helper()
	_, body, ok := strings.Cut(output, marker+"\n")
	if !ok {
		t.Fatalf("missing explicit AppKit entry function %q", marker)
	}
	body, _, ok = strings.Cut(body, "} // end sil function")
	if !ok {
		t.Fatalf("unterminated SIL function %q", marker)
	}
	return body
}
