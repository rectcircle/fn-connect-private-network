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

func TestMacOSAuthorizationPolicy(t *testing.T) {
	runMacOSAuthorizationCheck(t, macOSAuthorizationPolicyHarness, false)
}

func TestMacOSAuthorizationWebKit(t *testing.T) {
	if os.Getenv("FNCPN_WEBKIT_CHECK") != "1" {
		t.Skip("requires macOS WindowServer and FNCPN_WEBKIT_CHECK=1")
	}
	runMacOSAuthorizationCheck(t, macOSAuthorizationWebKitHarness, true)
}

func runMacOSAuthorizationCheck(t *testing.T, harness string, webKit bool) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("requires macOS")
	}
	swift, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc is not installed")
	}
	source, err := os.ReadFile(filepath.Join("..", "platform", "macos", "FnCPNApp.swift"))
	if err != nil {
		t.Fatal(err)
	}
	before, rest, ok := strings.Cut(string(source), "private final class IPCClient {")
	if !ok {
		t.Fatal("IPC client boundary was not found")
	}
	_, after, ok := strings.Cut(rest, "private final class AuthorizationWindow:")
	if !ok {
		t.Fatal("authorization controller boundary was not found")
	}
	// Keep the real authorization controller, but never contact an installed
	// daemon or a real NAS. The WebKit fixture loads only in-memory HTML.
	program := before + macOSAuthorizationIPCStub + "\nprivate final class AuthorizationWindow:" + after
	program = strings.Replace(program, "@main\n", "", 1)
	if webKit {
		program = strings.ReplaceAll(program, "window.makeKeyAndOrderFront(nil)", "// Keep test windows offscreen.")
		program = strings.Replace(program, "let webView = WKWebView(", "let webView = AuthorizationTestWebView(", 1)
		program = strings.Replace(program, "webView.callAsyncJavaScript(",
			"(webView as! AuthorizationTestWebView).evaluateFixtureJavaScript(", 1)
	}
	program += macOSAuthorizationAssertions + harness
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "AuthorizationCheck.swift")
	if err := os.WriteFile(sourcePath, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "AuthorizationCheck")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	compile := exec.CommandContext(ctx, swift, "-parse-as-library",
		"-framework", "AppKit", "-framework", "WebKit", sourcePath, "-o", binary)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile authorization check: %v\n%s", err, output)
	}
	output, err := exec.CommandContext(ctx, binary).CombinedOutput()
	t.Logf("authorization check:\n%s", output)
	if err != nil {
		t.Fatalf("authorization check: %v", err)
	}
}

const macOSAuthorizationIPCStub = `
private final class IPCClient {
    private static let lock = NSLock()
    private static var recorded: [(String, [String: Any])] = []

    static func reset() {
        lock.lock()
        defer { lock.unlock() }
        recorded = []
    }

    static func calls(_ method: String) -> [[String: Any]] {
        lock.lock()
        defer { lock.unlock() }
        return recorded.filter { $0.0 == method }.map { $0.1 }
    }

    func call(method: String, params: Any? = nil) throws -> [String: Any] {
        Self.lock.lock()
        defer { Self.lock.unlock() }
        Self.recorded.append((method, params as? [String: Any] ?? [:]))
        return ["state": "LOCAL"]
    }
}
`

const macOSAuthorizationAssertions = `
@MainActor
private func expect(_ condition: @autoclosure () -> Bool, _ message: String) {
    if !condition() {
        print("FAIL: \(message)")
        Darwin.exit(1)
    }
}

@MainActor
private func waitFor(_ message: String, _ condition: () -> Bool) {
    let deadline = Date().addingTimeInterval(12)
    while !condition() && Date() < deadline {
        _ = RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.02))
    }
    expect(condition(), message)
}

@MainActor
private func pumpEvents() {
    let deadline = Date().addingTimeInterval(0.1)
    while Date() < deadline {
        _ = RunLoop.current.run(mode: .default, before: deadline)
    }
}
`

const macOSAuthorizationPolicyHarness = `
extension AuthorizationWindow {
    @MainActor
    fileprivate static func verifyPolicy() {
        let controller = AuthorizationWindow(fnID: "test-nas", requestID: "policy", completion: { _ in })
        let root = controller.relayURL!
        let app = controller.applicationURL!
        expect(root.absoluteString == "https://test-nas.fnos.net/", "login must use relay root")
        expect(app.absoluteString == "https://test-nas.fnos.net/app/fncpn/", "application URL")
        let mode = relayCookie(for: root)!
        expect(mode.name == "mode" && mode.value == "relay", "routing cookie")
        expect(mode.domain == "test-nas.fnos.net" && mode.path == "/", "routing cookie scope")
        expect(mode.isSecure && mode.isHTTPOnly && mode.expiresDate == nil, "routing cookie attributes")
        for text in [
            "http://test-nas.fnos.net/",
            "https://test-nas.fnos.net:8443/",
            "https://test-nas.fnos.net.attacker.invalid/",
            "https://other-nas.fnos.net/",
            "https://name:password@test-nas.fnos.net/",
        ] {
            expect(!controller.allowsNavigation(to: URL(string: text)), "reject \(text)")
        }
        expect(controller.allowsNavigation(to: root), "allow target")
        expect(controller.allowsNavigation(to: URL(string: "https://fnos.net/")), "allow FN Connect")
        expect(!controller.isTargetPage(URL(string: "https://fnos.net/")), "portal is not NAS")
        expect(controller.isApplicationPage(app), "recognize application")
        expect(!controller.isApplicationPage(URL(string: "https://test-nas.fnos.net/app/fncpn-other")), "path boundary")
        for status in [0, 401, 403] {
            expect(bootstrapDecision(["status": status]) == .waitingForLogin, "unauthenticated status \(status)")
        }
        expect(bootstrapDecision(["status": 200, "body": NSNull()]) == .waitingForLogin, "HTML login is not authorization")
        expect(bootstrapDecision(["status": 200, "body": ["protocolVersion": protocolVersion, "administrator": true]]) == .authenticated, "administrator bootstrap")
        let detailedFailure: [String: Any] = [
            "code": "PERMISSION_DENIED", "message": "request rejected",
            "httpStatus": 403, "detail": "forbidden origin",
            "operation": "relay.handshake", "requestId": "request-123"
        ]
        let description = describeFailure(detailedFailure)
        for part in ["PERMISSION_DENIED", "403", "forbidden origin", "relay.handshake", "request-123"] {
            expect(description.contains(part), "error detail \(part) must reach the UI")
        }
        expect(bootstrapDecision(["status": 403, "body": ["error": detailedFailure]]) ==
               .failed(description, code: "PERMISSION_DENIED", httpStatus: 403),
               "structured server rejection must not become a silent login wait")
        for response: [String: Any] in [
            ["status": 200, "body": ["protocolVersion": protocolVersion, "administrator": false]],
            ["status": 200, "body": ["protocolVersion": 999, "administrator": true]],
            ["status": 200, "body": ["administrator": true]],
            ["status": 404],
            ["status": 503],
        ] {
            if case .failed = bootstrapDecision(response) {} else {
                expect(false, "invalid bootstrap must fail")
            }
        }
        let parentHostOnly = HTTPCookie.cookies(
            withResponseHeaderFields: ["Set-Cookie": "session=fake; Path=/; Secure; HttpOnly"],
            for: URL(string: "https://fnos.net/")!
        ).first!
        expect(!controller.cookie(parentHostOnly, appliesTo: app), "host-only portal cookie must not leak to NAS")
        let domainCookie = HTTPCookie.cookies(
            withResponseHeaderFields: ["Set-Cookie": "session=fake; Domain=.fnos.net; Path=/app/fncpn; Secure; HttpOnly"],
            for: root
        ).first!
        expect(controller.cookie(domainCookie, appliesTo: app), "domain cookie matches application")
        expect(!controller.cookie(domainCookie, appliesTo: root), "cookie path is restricted")
        expect(!controller.cookie(domainCookie, appliesTo: URL(string: "http://test-nas.fnos.net/app/fncpn/")!), "secure cookie requires HTTPS")
        expect(!controller.cookie(domainCookie, appliesTo: URL(string: "https://test-nas.fnos.net/app/fncpn-other")!), "cookie path boundary")
        controller.completed = true
        controller.checkAuthorization()
        controller.submitAuthorization()
        expect(!controller.checking && !controller.submitting, "closed controller ignores late work")
        expect(IPCClient.calls("authorize-complete").isEmpty, "policy check must not submit")
        print("PASS: relay URL/cookie, navigation boundary, bootstrap decisions, cookie scope, closed guard")
    }
}

@main
private enum AuthorizationPolicyCheck {
    @MainActor
    static func main() {
        AuthorizationWindow.verifyPolicy()
    }
}
`

const macOSAuthorizationWebKitHarness = `
private final class AuthorizationTestWebView: WKWebView {
    typealias Reply = @MainActor @Sendable () -> Void
    var requested: [URL] = []
    var bootstrapCount = 0
    var response: [String: Any] = ["status": 401]
    var holdReply = false
    var heldReply: Reply?
    var beforeReply: ((@escaping Reply) -> Void)?

    override var url: URL? { requested.last }

    @discardableResult
    override func load(_ request: URLRequest) -> WKNavigation? {
        requested.append(request.url!)
        return super.loadHTMLString("<html><body>Local authorization fixture</body></html>", baseURL: request.url)
    }

    func evaluateFixtureJavaScript(
        _ functionBody: String,
        arguments: [String: Any] = [:],
        in frame: WKFrameInfo?,
        in contentWorld: WKContentWorld,
        completionHandler: (@MainActor @Sendable (Result<Any, Error>) -> Void)? = nil
    ) {
        expect(arguments["path"] as? String == "/app/fncpn/api/v1/bootstrap", "bootstrap request path")
        bootstrapCount += 1
        let reply: Reply = { [response] in completionHandler?(.success(response)) }
        if holdReply {
            heldReply = { reply() }
        } else if let beforeReply {
            self.beforeReply = nil
            beforeReply { reply() }
        } else {
            DispatchQueue.main.async { reply() }
        }
    }

    func verifyJavaScript(_ script: String) {
        let fixture = """
            const fetch = async (requestPath, options) => {
                if (requestPath !== "/app/fncpn/api/v1/bootstrap" ||
                    options.credentials !== "same-origin" || options.redirect !== "manual") {
                    throw new Error("unsafe bootstrap request");
                }
                return {
                    status: 200,
                    headers: {get: () => "application/json"},
                    json: async () => ({protocolVersion: 4, administrator: true})
                };
            };
            """
        var done = false
        super.callAsyncJavaScript(
            fixture + script,
            arguments: ["path": "/app/fncpn/api/v1/bootstrap"],
            in: nil, in: .defaultClient
        ) { result in
            switch result {
            case .failure(let error):
                expect(false, "bootstrap JavaScript: \(error)")
            case .success(let value):
                let result = value as? [String: Any]
                let body = result?["body"] as? [String: Any]
                expect(result?["status"] as? Int == 200 && body?["administrator"] as? Bool == true, "async bootstrap result")
            }
            done = true
        }
        waitFor("bootstrap JavaScript completes") { done }
    }
}

extension AuthorizationWindow {
    @MainActor
    private func setFixtureCookie(_ value: String) {
        let cookie = HTTPCookie.cookies(
            withResponseHeaderFields: ["Set-Cookie": "future-session=\(value); Path=/; Secure; HttpOnly"],
            for: relayURL!
        ).first!
        var done = false
        webView!.configuration.websiteDataStore.httpCookieStore.setCookie(cookie) { done = true }
        waitFor("fixture cookie written") { done }
    }

    @MainActor
    fileprivate static func verifyWebKit() {
        IPCClient.reset()
        var succeeded = 0
        let controller = AuthorizationWindow(fnID: "test-nas", requestID: "success") { result in
            if case .success = result { succeeded += 1 } else { expect(false, "unexpected failure \(result)") }
        }
        controller.show()
        let view = controller.webView as! AuthorizationTestWebView
        waitFor("initial relay page loaded") { view.requested.count == 1 && !view.isLoading }
        pumpEvents()
        expect(view.requested.first == controller.relayURL, "start at relay login root")
        expect(view.bootstrapCount == 0 && !controller.completed, "mode-only cookie must not authorize")
        var initialCookies: [HTTPCookie]?
        view.configuration.websiteDataStore.httpCookieStore.getAllCookies { initialCookies = $0 }
        waitFor("initial cookie store") { initialCookies != nil }
        expect(initialCookies!.contains { $0.name == "mode" && $0.value == "relay" && $0.isHTTPOnly && $0.isSecure }, "real WebKit routing cookie")
        view.verifyJavaScript(bootstrapScript)
        controller.setFixtureCookie("not-yet-authenticated")
        waitFor("unauthenticated bootstrap checked") { view.bootstrapCount == 1 && !controller.checking }
        expect(!controller.completed && IPCClient.calls("authorize-complete").isEmpty, "401 stays open")
        controller.cookiesDidChange(in: view.configuration.websiteDataStore.httpCookieStore)
        pumpEvents()
        expect(view.bootstrapCount == 1, "identical cookie notification is coalesced")
        view.response = ["status": 200, "body": NSNull()]
        controller.setFixtureCookie("html-login")
        waitFor("HTML login checked") { view.bootstrapCount == 2 && !controller.checking }
        expect(!controller.completed, "HTTP 200 login HTML stays open")
        view.response = ["status": 200, "body": ["protocolVersion": protocolVersion, "administrator": true]]
        view.beforeReply = { reply in
            let renewed = HTTPCookie.cookies(
                withResponseHeaderFields: ["Set-Cookie": "future-session=renewed; Path=/; Secure; HttpOnly"],
                for: controller.relayURL!
            ).first!
            view.configuration.websiteDataStore.httpCookieStore.setCookie(renewed, completionHandler: reply)
        }
        controller.setFixtureCookie("authenticated")
        waitFor("authorization completed") { succeeded == 1 }
        expect(controller.webView == nil && controller.lastCheckedCookies == nil, "release temporary credentials after success")
        expect(view.requested == [controller.relayURL!, controller.applicationURL!], "login leads to FnCPN")
        let submissions = IPCClient.calls("authorize-complete")
        expect(submissions.count == 1, "submit exactly once")
        let cookies = submissions[0]["cookies"] as! [[String: Any]]
        expect(cookies.contains { $0["name"] as? String == "future-session" && $0["value"] as? String == "renewed" }, "export refreshed credential")
        expect(cookies.contains { $0["name"] as? String == "mode" && $0["value"] as? String == "relay" }, "retain routing cookie")
        expect(IPCClient.calls("authorization-cancel").isEmpty, "success does not cancel")
        controller.cookiesDidChange(in: view.configuration.websiteDataStore.httpCookieStore)
        pumpEvents()
        expect(IPCClient.calls("authorize-complete").count == 1, "late cookie event does not resubmit")
        print("PASS: relay startup, mode-only/401/HTML waiting, JavaScript, navigation, refreshed credentials, single submission")

        IPCClient.reset()
        var failed = 0
        let regular = AuthorizationWindow(fnID: "test-nas", requestID: "regular") { result in
            if case .failure = result { failed += 1 } else { expect(false, "regular account authorized") }
        }
        regular.show()
        let regularView = regular.webView as! AuthorizationTestWebView
        waitFor("regular login loaded") { regularView.requested.count == 1 && !regularView.isLoading }
        regularView.response = ["status": 200, "body": ["protocolVersion": protocolVersion, "administrator": false]]
        regular.setFixtureCookie("regular-account")
        waitFor("regular account rejected") { failed == 1 && IPCClient.calls("authorization-cancel").count == 1 }
        expect(regular.webView == nil && regular.lastCheckedCookies == nil, "release temporary credentials after failure")
        expect(IPCClient.calls("authorize-complete").isEmpty, "regular account never submits")
        print("PASS: non-admin rejected before IPC submission")

        IPCClient.reset()
        let canceled = AuthorizationWindow(fnID: "test-nas", requestID: "canceled") { _ in
            expect(false, "late completion after close")
        }
        canceled.show()
        let canceledView = canceled.webView as! AuthorizationTestWebView
        waitFor("cancel fixture loaded") { canceledView.requested.count == 1 && !canceledView.isLoading }
        canceledView.holdReply = true
        canceledView.response = ["status": 200, "body": ["protocolVersion": protocolVersion, "administrator": true]]
        canceled.setFixtureCookie("canceled-account")
        waitFor("bootstrap in flight") { canceledView.heldReply != nil }
        canceled.window!.close()
        waitFor("cancel sent") { IPCClient.calls("authorization-cancel").count == 1 }
        expect(canceled.webView == nil && canceled.lastCheckedCookies == nil, "release temporary credentials after cancel")
        canceledView.heldReply?()
        pumpEvents()
        expect(IPCClient.calls("authorize-complete").isEmpty, "late bootstrap cannot authorize after close")
        print("PASS: closing cancels and ignores late bootstrap")

        IPCClient.reset()
        let navigated = AuthorizationWindow(fnID: "test-nas", requestID: "navigation") { _ in
            expect(false, "stale page completed authorization")
        }
        navigated.show()
        let navigationView = navigated.webView as! AuthorizationTestWebView
        waitFor("navigation fixture loaded") { navigationView.requested.count == 1 && !navigationView.isLoading }
        navigationView.holdReply = true
        navigationView.response = ["status": 200, "body": ["protocolVersion": protocolVersion, "administrator": true]]
        navigated.setFixtureCookie("old-page-account")
        waitFor("old page bootstrap in flight") { navigationView.heldReply != nil }
        let generation = navigated.navigationGeneration
        navigationView.load(URLRequest(url: navigated.relayURL!.appendingPathComponent("login")))
        waitFor("new page loaded") { navigated.navigationGeneration > generation && !navigationView.isLoading }
        pumpEvents()
        navigationView.holdReply = false
        navigationView.response = ["status": 401]
        navigationView.heldReply?()
        waitFor("new page checked independently") { navigationView.bootstrapCount == 2 && !navigated.checking }
        expect(navigationView.requested.count == 2 && !navigated.completed, "stale success must not navigate to application")
        expect(IPCClient.calls("authorize-complete").isEmpty, "stale page must not submit credentials")
        navigated.window!.close()
        waitFor("navigation fixture canceled") { IPCClient.calls("authorization-cancel").count == 1 }
        print("PASS: stale bootstrap ignored across navigation")

        IPCClient.reset()
        let earlyClose = AuthorizationWindow(fnID: "test-nas", requestID: "early-close") { _ in
            expect(false, "closed window reopened")
        }
        earlyClose.show()
        let earlyView = earlyClose.webView as! AuthorizationTestWebView
        earlyClose.window!.close()
        waitFor("early cancel sent") { IPCClient.calls("authorization-cancel").count == 1 }
        pumpEvents()
        expect(earlyView.requested.isEmpty, "late routing-cookie callback must not load a closed window")
        print("PASS: close during routing-cookie initialization")
    }
}

@main
private enum AuthorizationWebKitCheck {
    @MainActor
    static func main() {
        NSApplication.shared.setActivationPolicy(.prohibited)
        AuthorizationWindow.verifyWebKit()
    }
}
`
