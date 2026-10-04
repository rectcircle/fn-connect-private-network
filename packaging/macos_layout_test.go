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
	// Execute the real view construction in an offscreen window. Do not launch
	// the application lifecycle, open IPC connections, or access installed state.
	program := strings.Replace(string(source), "@main\n", "", 1) + macOSLayoutHarness
	directory := t.TempDir()
	if os.Getenv("FNCPN_LAYOUT_PREVIEW") == "1" {
		directory = filepath.Join(os.TempDir(), "FnCPNUICheck.app", "Contents", "MacOS")
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.rectcircle.fncpn.uicheck</string>
<key>CFBundleExecutable</key><string>LayoutCheck</string>
<key>CFBundleName</key><string>FnCPN UI Check</string>
</dict></plist>`
		if err := os.WriteFile(filepath.Join(directory, "..", "Info.plist"), []byte(plist), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sourcePath := filepath.Join(directory, "LayoutCheck.swift")
	if err := os.WriteFile(sourcePath, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "LayoutCheck")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	compile := exec.CommandContext(ctx, swift, "-parse-as-library",
		"-framework", "AppKit", sourcePath, "-o", binary)
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
        let content = makeContent()
        let testWindow = NSWindow(
            contentRect: content.frame,
            styleMask: [.titled, .closable, .miniaturizable],
            backing: .buffered,
            defer: false
        )
        testWindow.isReleasedWhenClosed = false
        testWindow.contentView = content
        window = testWindow
        var failed = false
        func check(_ condition: Bool, _ message: String) {
            if !condition { print("FAIL: \(message)"); failed = true }
        }
        func checkVisible(_ view: NSView) {
            let rect = view.convert(view.bounds, to: content)
            check(!view.isHiddenOrHasHiddenAncestor && content.bounds.contains(rect) &&
                  rect.width > 10 && rect.height > 10, "visible \(view): \(rect)")
        }
        withExtendedLifetime(testWindow) {
            render()
            content.layoutSubtreeIfNeeded()
            checkVisible(fnIDField)
            checkVisible(usernameField)
            checkVisible(passwordField)
            checkVisible(setupButton)
            check(detailsView.isHidden, "first launch must show native login")
            check(fnIDField.nextKeyView === usernameField &&
                  usernameField.nextKeyView === passwordField, "login tab order")
            for size in [NSSize(width: 560, height: 620), NSSize(width: 800, height: 800)] {
                testWindow.setContentSize(size)
                for state in ["DIRECT", "RELAY", "AUTH_REQUIRED", "PAUSED", "RECONNECTING", "ERROR"] {
                    let failure: [String: Any] = [
                        "code": state == "AUTH_REQUIRED" ? "AUTH_REQUIRED" : "TIMEOUT",
                        "message": String(repeating: "long-diagnostic-detail ", count: 60)
                    ]
                    applyDiagnostics([
                        "fnId": String(repeating: "a", count: 63),
                        "username": "admin",
                        "status": ["state": state, "lastError": failure],
                        "direct": ["reason": "handshake_failed", "candidateCount": 2, "attemptCount": 2],
                        "privilegedAvailable": true
                    ])
                    content.layoutSubtreeIfNeeded()
                    check(setupView.isHidden, "saved NAS opens details")
                    for view in [serverValue, statusValue, directValue, primaryButton, changeButton] as [NSView] {
                        checkVisible(view)
                    }
                    checkVisible(errorValue.enclosingScrollView!)
                    check(errorValue.string.count > 500, "long diagnostics must not be truncated")
                    check(primaryButton.title == (state == "AUTH_REQUIRED" ? "重新登录" :
                        ["DIRECT", "RELAY"].contains(state) ? "断开连接" : "连接"), "contextual action")
                    check(!content.hasAmbiguousLayout, "root layout ambiguity")
                    print("PASS: \(state), \(size)")
                }
            }
            busy = true
            render()
            check(!primaryButton.isEnabled && !loginButton.isEnabled && !retryButton.isEnabled &&
                  !fnIDField.isEnabled && !usernameField.isEnabled && !passwordField.isEnabled,
                  "busy commands disabled")
            busy = false
            changeServer()
            content.layoutSubtreeIfNeeded()
            checkVisible(fnIDField)
            checkVisible(usernameField)
            checkVisible(passwordField)
            checkVisible(returnButton)
            returnToDetails()
            check(setupView.isHidden, "return restores saved details without forgetting NAS")
            let rootDenied = ConnectionPresentation(["state": "PAUSED", "lastError": [
                "code": "PERMISSION_DENIED", "operation": "privileged.apply"
            ]])
            check(!rootDenied.needsLogin, "system permissions must not ask for NAS login")
            let remoteDenied = ConnectionPresentation(["state": "PAUSED", "lastError": [
                "code": "PERMISSION_DENIED", "httpStatus": 403
            ]])
            check(remoteDenied.needsLogin, "remote permission rejection offers login")
            editingServer = false
            applyDiagnostics([
                "fnId": "home-nas",
                "username": "rectcircle",
                "status": ["state": "AUTH_REQUIRED", "lastError": [
                    "code": "AUTH_REQUIRED", "message": "fnOS session expired"
                ]],
                "privilegedAvailable": true
            ])
            relogin()
            content.layoutSubtreeIfNeeded()
            checkVisible(fnIDField)
            check(fnIDField.stringValue == "home-nas", "relogin pre-fills FN Connect ID")
            check(usernameField.stringValue == "rectcircle", "relogin pre-fills username")
            check(passwordField.stringValue.isEmpty, "relogin never pre-fills password")
            if ProcessInfo.processInfo.environment["FNCPN_LAYOUT_PREVIEW"] == "1" {
                editingServer = true
                applyDiagnostics([
                    "fnId": "home-nas",
                    "username": "rectcircle",
                    "status": ["state": "AUTH_REQUIRED", "lastError": [
                        "code": "AUTH_REQUIRED", "message": "FN Connect session is invalid or expired",
                        "operation": "configuration.watch", "httpStatus": 200
                    ]],
                    "privilegedAvailable": true
                ])
                testWindow.setContentSize(NSSize(width: 560, height: 620))
                testWindow.title = "FnCPN UI Check"
                testWindow.center()
                testWindow.makeKeyAndOrderFront(nil)
                NSApp.activate(ignoringOtherApps: true)
                let deadline = Date().addingTimeInterval(75)
                while Date() < deadline {
                    if let event = NSApp.nextEvent(matching: .any, until: Date().addingTimeInterval(0.1),
                                                   inMode: .default, dequeue: true) {
                        NSApp.sendEvent(event)
                    }
                    NSApp.updateWindows()
                }
                testWindow.orderOut(nil)
            }
        }
        if failed { Darwin.exit(1) }
    }
}

@main
private enum LayoutCheck {
    @MainActor
    static func main() {
        NSApplication.shared.setActivationPolicy(
            ProcessInfo.processInfo.environment["FNCPN_LAYOUT_PREVIEW"] == "1" ? .regular : .prohibited
        )
        NSApp.finishLaunching()
        AppDelegate().verifyAuthorizationLayout()
    }
}
`
