package packaging

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMacOSAuthorizationLayout(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("FNCPN_LAYOUT_CHECK") != "1" {
		t.Skip("requires macOS WindowServer and FNCPN_LAYOUT_CHECK=1")
	}
	swift, err := exec.LookPath("swiftc")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("..", "platform", "macos", "FnCPNApp.swift"))
	if err != nil {
		t.Fatal(err)
	}
	_, construction, ok := strings.Cut(string(source),
		"func applicationDidFinishLaunching(_ notification: Notification) {\n")
	if !ok {
		t.Fatal("main view construction was not found")
	}
	construction, _, ok = strings.Cut(construction, "\n        window = NSWindow(")
	if !ok {
		t.Fatal("main view construction boundary was not found")
	}
	// Execute the real view construction in an offscreen window. Do not launch
	// the application lifecycle, open IPC connections, or access installed state.
	program := strings.Replace(string(source), "@main\n", "", 1) +
		strings.Replace(macOSLayoutHarness, "// BUILD_CONTENT", construction, 1)
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "LayoutCheck.swift")
	if err := os.WriteFile(sourcePath, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "LayoutCheck")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	compile := exec.CommandContext(ctx, swift, "-parse-as-library",
		"-framework", "AppKit", "-framework", "WebKit", sourcePath, "-o", binary)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile layout check: %v\n%s", err, output)
	}
	output, err := exec.CommandContext(ctx, binary).CombinedOutput()
	t.Logf("AppKit layout:\n%s", output)
	if err != nil {
		t.Fatalf("layout check: %v", err)
	}
}

const macOSLayoutHarness = `
extension AppDelegate {
    @MainActor
    fileprivate func verifyAuthorizationLayout() {
        // BUILD_CONTENT
        let testWindow = NSWindow(
            contentRect: content.frame,
            styleMask: [.titled, .closable, .miniaturizable],
            backing: .buffered,
            defer: false
        )
        testWindow.isReleasedWhenClosed = false
        testWindow.contentView = content
        var failed = false
        withExtendedLifetime(testWindow) {
            for (name, value) in [
                ("empty", ""),
                ("entered", "example-fn-id"),
                ("long", String(repeating: "a", count: 63)),
            ] {
                fnIDField.stringValue = value
                content.layoutSubtreeIfNeeded()
                let field = fnIDField.convert(fnIDField.bounds, to: content)
                let button = authorizeButton.convert(authorizeButton.bounds, to: content)
                print("\(name): field=\(field), button=\(button), content=\(content.bounds)")
                let visible = fnIDField.superview != nil && authorizeButton.superview != nil &&
                    !fnIDField.isHiddenOrHasHiddenAncestor &&
                    !authorizeButton.isHiddenOrHasHiddenAncestor &&
                    content.bounds.contains(field) && content.bounds.contains(button)
                let sized = abs(field.width - 270) < 0.5 &&
                    abs(button.width - 96) < 0.5 &&
                    field.height > 12 && button.height > 12 &&
                    abs(field.height - fnIDField.intrinsicContentSize.height) < 0.5
                let aligned = abs(field.minX - 24) < 0.5 &&
                    abs(button.minX - 306) < 0.5 &&
                    abs(field.midY - 108) < 0.5 &&
                    abs(field.midY - button.midY) < 0.5
                if !visible || !sized || !aligned {
                    print("FAIL: visible=\(visible), sized=\(sized), aligned=\(aligned)")
                    failed = true
                }
            }
        }
        if failed { Darwin.exit(1) }
    }
}

@main
private enum LayoutCheck {
    @MainActor
    static func main() {
        NSApplication.shared.setActivationPolicy(.prohibited)
        AppDelegate().verifyAuthorizationLayout()
    }
}
`
