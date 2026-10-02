import AppKit
import Foundation
import WebKit

private let protocolVersion = 4
private let maximumFrameSize = 256 * 1024

private func describeFailure(_ failure: [String: Any]?) -> String {
    guard let failure else { return "" }
    var parts = [failure["code"] as? String, failure["message"] as? String]
        .compactMap { $0 }
    if let operation = failure["operation"] as? String { parts.append("[\(operation)]") }
    if let status = failure["httpStatus"] as? Int { parts.append("HTTP \(status)") }
    if let code = failure["remoteCode"] as? String { parts.append("remote \(code)") }
    if let detail = failure["detail"] as? String { parts.append(detail) }
    if let id = failure["requestId"] as? String { parts.append("request \(id)") }
    return parts.joined(separator: ": ")
}

private func normalizeFNID(_ input: String) -> String? {
    let value = input.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    var candidate = value
    if let url = URL(string: value), let host = url.host {
        if host == "fnos.net" {
            candidate = url.pathComponents.first { $0 != "/" } ?? ""
        } else if host.hasSuffix(".fnos.net") {
            candidate = String(host.dropLast(".fnos.net".count))
        }
    } else if candidate.hasSuffix(".fnos.net") {
        candidate = String(candidate.dropLast(".fnos.net".count))
    }
    guard !candidate.isEmpty,
          candidate.count <= 63,
          candidate.first != "-",
          candidate.last != "-",
          candidate.allSatisfy({ $0.isLowercase || $0.isNumber || $0 == "-" }) else {
        return nil
    }
    return candidate
}

private func clientSocketPath() -> String {
    FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("Library/Application Support/FnCPN/run/client.sock")
        .path
}

private final class IPCClient {
    func call(method: String, params: Any? = nil) throws -> [String: Any] {
        let descriptor = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw POSIXError(.EIO) }
        defer { Darwin.close(descriptor) }
        try setTimeout(
            seconds: method == "authorize-complete" ||
                method == "watch-client-status" ? 310 : 30,
            on: descriptor
        )

        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let path = Array(clientSocketPath().utf8CString)
        guard path.count <= MemoryLayout.size(ofValue: address.sun_path) else {
            throw POSIXError(.ENAMETOOLONG)
        }
        withUnsafeMutablePointer(to: &address.sun_path) { pointer in
            pointer.withMemoryRebound(to: CChar.self, capacity: path.count) {
                target in
                for index in path.indices {
                    target[index] = path[index]
                }
            }
        }
        try connect(descriptor, to: &address)

        let requestID = UUID().uuidString
        var request: [String: Any] = [
            "version": protocolVersion,
            "id": requestID,
            "method": method,
        ]
        if let params {
            request["params"] = params
        }
        let payload = try JSONSerialization.data(withJSONObject: request)
        var length = UInt32(payload.count).bigEndian
        var frame = Data(bytes: &length, count: MemoryLayout<UInt32>.size)
        frame.append(payload)
        try writeAll(frame, to: descriptor)

        let header = try readExactly(4, from: descriptor)
        let responseLength = header.withUnsafeBytes {
            $0.loadUnaligned(as: UInt32.self).bigEndian
        }
        guard responseLength > 0 && responseLength <= maximumFrameSize else {
            throw NSError(
                domain: "FnCPN",
                code: 1,
                userInfo: [NSLocalizedDescriptionKey: "Invalid daemon response"]
            )
        }
        let responseData = try readExactly(Int(responseLength), from: descriptor)
        guard let response = try JSONSerialization.jsonObject(
            with: responseData
        ) as? [String: Any] else {
            throw NSError(
                domain: "FnCPN",
                code: 2,
                userInfo: [NSLocalizedDescriptionKey: "Invalid daemon response"]
            )
        }
        guard response["version"] as? Int == protocolVersion,
              response["id"] as? String == requestID,
              let ok = response["ok"] as? Bool else {
            throw invalidResponse()
        }
        let result = response["result"]
        let responseError = response["error"] as? [String: Any]
        guard (ok && responseError == nil) || (!ok && result == nil && responseError != nil) else {
            throw invalidResponse()
        }
        if !ok {
            let message = describeFailure(responseError)
            throw NSError(
                domain: "FnCPN",
                code: 3,
                userInfo: [NSLocalizedDescriptionKey: message, "failure": responseError ?? [:]]
            )
        }
        return result as? [String: Any] ?? [:]
    }

    private func connect(
        _ descriptor: Int32,
        to address: inout sockaddr_un
    ) throws {
        let flags = Darwin.fcntl(descriptor, F_GETFL)
        guard flags >= 0,
              Darwin.fcntl(descriptor, F_SETFL, flags | O_NONBLOCK) == 0 else {
            throw POSIXError(.EIO)
        }
        defer { _ = Darwin.fcntl(descriptor, F_SETFL, flags) }
        let result = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(
                    descriptor,
                    $0,
                    socklen_t(MemoryLayout<sockaddr_un>.size)
                )
            }
        }
        if result == 0 { return }
        guard errno == EINPROGRESS else {
            throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO)
        }
        var descriptorState = pollfd(fd: descriptor, events: Int16(POLLOUT), revents: 0)
        guard Darwin.poll(&descriptorState, 1, 5_000) > 0 else {
            throw POSIXError(.ETIMEDOUT)
        }
        var socketError: Int32 = 0
        var length = socklen_t(MemoryLayout<Int32>.size)
        guard Darwin.getsockopt(
            descriptor,
            SOL_SOCKET,
            SO_ERROR,
            &socketError,
            &length
        ) == 0, socketError == 0 else {
            throw POSIXError(POSIXErrorCode(rawValue: socketError) ?? .EIO)
        }
    }

    private func setTimeout(seconds: Int, on descriptor: Int32) throws {
        var timeout = timeval(tv_sec: seconds, tv_usec: 0)
        let size = socklen_t(MemoryLayout<timeval>.size)
        guard withUnsafePointer(to: &timeout, {
            Darwin.setsockopt(descriptor, SOL_SOCKET, SO_RCVTIMEO, $0, size)
        }) == 0,
        withUnsafePointer(to: &timeout, {
            Darwin.setsockopt(descriptor, SOL_SOCKET, SO_SNDTIMEO, $0, size)
        }) == 0 else {
            throw POSIXError(.EIO)
        }
    }

    private func invalidResponse() -> Error {
        NSError(
            domain: "FnCPN",
            code: 2,
            userInfo: [NSLocalizedDescriptionKey: "Invalid daemon response"]
        )
    }

    private func writeAll(_ data: Data, to descriptor: Int32) throws {
        try data.withUnsafeBytes { bytes in
            guard var pointer = bytes.baseAddress else { return }
            var remaining = bytes.count
            while remaining > 0 {
                let written = Darwin.write(descriptor, pointer, remaining)
                guard written > 0 else { throw POSIXError(.EIO) }
                pointer = pointer.advanced(by: written)
                remaining -= written
            }
        }
    }

    private func readExactly(_ count: Int, from descriptor: Int32) throws -> Data {
        var data = Data(count: count)
        try data.withUnsafeMutableBytes { bytes in
            guard var pointer = bytes.baseAddress else { return }
            var remaining = count
            while remaining > 0 {
                let received = Darwin.read(descriptor, pointer, remaining)
                guard received > 0 else { throw POSIXError(.EIO) }
                pointer = pointer.advanced(by: received)
                remaining -= received
            }
        }
        return data
    }
}

private final class AuthorizationWindow: NSObject, WKNavigationDelegate, NSWindowDelegate, WKHTTPCookieStoreObserver {
    private struct CookieIdentity: Hashable {
        let name: String
        let value: String
        let domain: String
        let path: String
    }

    private enum BootstrapDecision: Equatable {
        case waitingForLogin
        case authenticated
        case failed(String, code: String = "PROTOCOL_ERROR", httpStatus: Int = 0, retryable: Bool = false)
    }

    private static let bootstrapScript = """
        const controller = new AbortController();
        const timeout = setTimeout(() => controller.abort(), 10000);
        try {
            const response = await fetch(path, {
                credentials: "same-origin",
                cache: "no-store",
                redirect: "manual",
                signal: controller.signal
            });
            let body = null;
            if ((response.headers.get("content-type") || "").includes("application/json")) {
                try { body = await response.json(); } catch (_) {}
            }
            return { status: response.status, body };
        } finally {
            clearTimeout(timeout);
        }
        """

    private let fnID: String
    private let requestID: String
    private let completion: (Result<Void, Error>) -> Void
    private var completed = false
    private var checking = false
    private var recheck = false
    private var submitting = false
    private var navigationGeneration = 0
    private var lastCheckedCookies: Set<CookieIdentity>?
    private var webView: WKWebView?
    private var window: NSWindow?

    private var relayURL: URL? {
        URL(string: "https://\(fnID).fnos.net/")
    }

    private var applicationURL: URL? {
        relayURL?.appendingPathComponent("app/fncpn", isDirectory: true)
    }

    init(
        fnID: String,
        requestID: String,
        completion: @escaping (Result<Void, Error>) -> Void
    ) {
        self.fnID = fnID
        self.requestID = requestID
        self.completion = completion
    }

    func show() {
        guard let relayURL,
              let relayCookie = Self.relayCookie(for: relayURL) else {
            finish(.failure(authorizationError("Invalid FN Connect address", code: "INVALID_ARGUMENT")))
            return
        }
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        let webView = WKWebView(
            frame: NSRect(x: 0, y: 0, width: 900, height: 680),
            configuration: configuration
        )
        self.webView = webView
        webView.navigationDelegate = self
        configuration.websiteDataStore.httpCookieStore.add(self)
        let window = NSWindow(
            contentRect: webView.frame,
            styleMask: [.titled, .closable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "FnCPN Authorization"
        window.isReleasedWhenClosed = false
        window.delegate = self
        window.contentView = webView
        window.center()
        window.makeKeyAndOrderFront(nil)
        self.window = window
        configuration.websiteDataStore.httpCookieStore.setCookie(relayCookie) {
            [weak self, weak webView] in
            DispatchQueue.main.async {
                guard let self, !self.completed else { return }
                webView?.load(URLRequest(url: relayURL))
            }
        }
    }

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void
    ) {
        guard allowsNavigation(to: navigationAction.request.url) else {
            decisionHandler(.cancel)
            return
        }
        if navigationAction.targetFrame == nil {
            webView.load(navigationAction.request)
            decisionHandler(.cancel)
            return
        }
        decisionHandler(.allow)
    }

    func webView(_ webView: WKWebView, didStartProvisionalNavigation navigation: WKNavigation!) {
        navigationGeneration += 1
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        lastCheckedCookies = nil
        checkAuthorization()
    }

    func cookiesDidChange(in cookieStore: WKHTTPCookieStore) {
        DispatchQueue.main.async { [weak self] in
            self?.checkAuthorization()
        }
    }

    func webView(
        _ webView: WKWebView,
        didFailProvisionalNavigation navigation: WKNavigation!,
        withError error: Error
    ) {
        navigationFailed(error)
    }

    func webView(
        _ webView: WKWebView,
        didFail navigation: WKNavigation!,
        withError error: Error
    ) {
        navigationFailed(error)
    }

    private func navigationFailed(_ error: Error) {
        let error = error as NSError
        if error.domain == NSURLErrorDomain && error.code == NSURLErrorCancelled { return }
        finish(.failure(error))
    }

    private func checkAuthorization() {
        guard !completed, !submitting else { return }
        if checking {
            recheck = true
            return
        }
        guard let webView, !webView.isLoading, isTargetPage(webView.url),
              let applicationURL else { return }
        checking = true
        let generation = navigationGeneration
        webView.configuration.websiteDataStore.httpCookieStore.getAllCookies {
            [weak self, weak webView] cookies in
            DispatchQueue.main.async {
                guard let self, let webView, !self.completed else { return }
                guard generation == self.navigationGeneration,
                      !webView.isLoading, self.isTargetPage(webView.url) else {
                    self.finishCheck()
                    return
                }
                let matching = cookies.filter { self.cookie($0, appliesTo: applicationURL) }
                let identity = Set(matching.filter { $0.name != "mode" }.map {
                    CookieIdentity(name: $0.name, value: $0.value, domain: $0.domain, path: $0.path)
                })
                // The routing cookie is not a credential. Ignore duplicate cookie notifications.
                if identity.isEmpty { self.lastCheckedCookies = nil }
                guard !identity.isEmpty, identity != self.lastCheckedCookies else {
                    self.finishCheck()
                    return
                }
                self.lastCheckedCookies = identity
                webView.callAsyncJavaScript(
                    Self.bootstrapScript,
                    arguments: ["path": "/app/fncpn/api/v1/bootstrap"],
                    in: nil,
                    in: .defaultClient
                ) { [weak self, weak webView] result in
                    DispatchQueue.main.async {
                        guard let self, let webView, !self.completed else { return }
                        guard generation == self.navigationGeneration,
                              !webView.isLoading, self.isTargetPage(webView.url) else {
                            self.finishCheck()
                            return
                        }
                        switch result {
                        case .failure(let error):
                            self.finish(.failure(error))
                        case .success(let response):
                            switch Self.bootstrapDecision(response) {
                            case .waitingForLogin:
                                self.finishCheck()
                            case .failed(let message, let code, let status, let retryable):
                                self.finish(.failure(self.authorizationError(
                                    message, code: code, httpStatus: status, retryable: retryable
                                )))
                            case .authenticated:
                                if self.isApplicationPage(webView.url) {
                                    self.submitAuthorization()
                                } else {
                                    self.checking = false
                                    self.recheck = false
                                    webView.load(URLRequest(url: applicationURL))
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    private func finishCheck() {
        checking = false
        if recheck {
            recheck = false
            checkAuthorization()
        }
    }

    private func submitAuthorization() {
        guard !completed, !submitting, let webView, let applicationURL else { return }
        checking = false
        submitting = true
        let generation = navigationGeneration
        // Bootstrap may refresh credentials; export the store only after its response.
        webView.configuration.websiteDataStore.httpCookieStore.getAllCookies {
            [weak self] cookies in
            DispatchQueue.main.async {
                guard let self, !self.completed else { return }
                guard generation == self.navigationGeneration,
                      let currentView = self.webView, !currentView.isLoading,
                      self.isApplicationPage(currentView.url) else {
                    self.submitting = false
                    self.lastCheckedCookies = nil
                    self.checkAuthorization()
                    return
                }
                let matching = cookies.filter { self.cookie($0, appliesTo: applicationURL) }
                guard matching.contains(where: { $0.name != "mode" }) else {
                    self.submitting = false
                    self.lastCheckedCookies = nil
                    self.checkAuthorization()
                    return
                }
                let values = matching.map { cookie -> [String: Any] in
                    var value: [String: Any] = [
                        "name": cookie.name,
                        "value": cookie.value,
                        "domain": cookie.domain,
                        "path": cookie.path,
                        "secure": cookie.isSecure,
                        "httpOnly": cookie.isHTTPOnly,
                        "hostOnly": !cookie.domain.hasPrefix("."),
                    ]
                    if let expires = cookie.expiresDate {
                        value["expires"] = ISO8601DateFormatter().string(from: expires)
                    }
                    return value
                }
                DispatchQueue.global(qos: .userInitiated).async {
                    let result: Result<Void, Error>
                    do {
                        _ = try IPCClient().call(
                            method: "authorize-complete",
                            params: [
                                "requestId": self.requestID,
                                "fnId": self.fnID,
                                "cookies": values,
                            ]
                        )
                        result = .success(())
                    } catch {
                        result = .failure(error)
                    }
                    DispatchQueue.main.async {
                        self.finish(result)
                    }
                }
            }
        }
    }

    func windowWillClose(_ notification: Notification) {
        guard !completed else { return }
        completed = true
        stopObservingCookies()
        cancelRequest()
        releaseWebView()
    }

    private func finish(_ result: Result<Void, Error>) {
        guard !completed else { return }
        completed = true
        stopObservingCookies()
        if case .failure(let error) = result { cancelRequest(failure: error) }
        window?.close()
        releaseWebView()
        completion(result)
    }

    private func stopObservingCookies() {
        webView?.configuration.websiteDataStore.httpCookieStore.remove(self)
    }

    private func releaseWebView() {
        webView?.stopLoading()
        window?.contentView = nil
        webView = nil
        window = nil
        lastCheckedCookies = nil
    }

    private func cancelRequest(failure: Error? = nil) {
        let requestID = requestID
        var params: [String: Any] = ["requestId": requestID]
        if let failure {
            let error = failure as NSError
            let timedOut = error.domain == NSURLErrorDomain && error.code == NSURLErrorTimedOut
            params["failure"] = error.userInfo["failure"] as? [String: Any] ?? [
                "code": timedOut ? "TIMEOUT" : "UNAVAILABLE",
                "message": "Authorization browser failed",
                "retryable": true,
                "operation": "authorization.browser",
                "detail": "\(error.domain) (\(error.code)): \(error.localizedDescription)",
            ]
        }
        let cancellation = params
        DispatchQueue.global(qos: .utility).async {
            _ = try? IPCClient().call(
                method: "authorization-cancel",
                params: cancellation
            )
        }
    }

    private func authorizationError(
        _ message: String, code: String = "PROTOCOL_ERROR", httpStatus: Int = 0, retryable: Bool = false
    ) -> Error {
        var failure: [String: Any] = [
            "code": code, "message": message, "retryable": retryable,
            "operation": "authorization.bootstrap"
        ]
        if httpStatus != 0 { failure["httpStatus"] = httpStatus }
        return NSError(domain: "FnCPN", code: 5, userInfo: [
            NSLocalizedDescriptionKey: describeFailure(failure), "failure": failure
        ])
    }

    private func allowsNavigation(to url: URL?) -> Bool {
        guard let url, url.scheme?.lowercased() == "https",
              url.port == nil || url.port == 443,
              url.user == nil, url.password == nil,
              let host = url.host?.lowercased() else { return false }
        return host == "fnos.net" || host == "\(fnID).fnos.net"
    }

    private func isTargetPage(_ url: URL?) -> Bool {
        allowsNavigation(to: url) && url?.host?.lowercased() == "\(fnID).fnos.net"
    }

    private func isApplicationPage(_ url: URL?) -> Bool {
        guard isTargetPage(url), let path = url?.path else { return false }
        return path == "/app/fncpn" || path.hasPrefix("/app/fncpn/")
    }

    private static func relayCookie(for url: URL) -> HTTPCookie? {
        HTTPCookie.cookies(
            withResponseHeaderFields: ["Set-Cookie": "mode=relay; Path=/; Secure; HttpOnly"],
            for: url
        ).first
    }

    private static func bootstrapDecision(_ response: Any?) -> BootstrapDecision {
        guard let response = response as? [String: Any],
              let status = response["status"] as? Int else {
            return .failed("Invalid FnCPN bootstrap response")
        }
        if status != 401,
           let body = response["body"] as? [String: Any],
           let failure = body["error"] as? [String: Any] {
            return .failed(describeFailure(failure), code: failure["code"] as? String ?? "PROTOCOL_ERROR",
                           httpStatus: status, retryable: failure["retryable"] as? Bool == true)
        }
        if status == 0 || status == 401 || status == 403 {
            return .waitingForLogin
        }
        guard status == 200 else {
            let code = status == 429 ? "RESOURCE_EXHAUSTED" : status >= 500 ? "UNAVAILABLE" : "PROTOCOL_ERROR"
            return .failed("FnCPN bootstrap returned HTTP \(status)", code: code,
                           httpStatus: status, retryable: status == 429 || status >= 500)
        }
        // Some fnOS gateways return an HTML login/invalid-token page with HTTP 200.
        if response["body"] == nil || response["body"] is NSNull {
            return .waitingForLogin
        }
        guard let body = response["body"] as? [String: Any],
              let version = body["protocolVersion"] as? Int,
              let administrator = body["administrator"] as? Bool else {
            return .failed("Invalid FnCPN bootstrap response")
        }
        guard version == protocolVersion else {
            return .failed("FnCPN client and server protocol versions do not match")
        }
        return administrator
            ? .authenticated
            : .failed("Administrator access is required to register this device", code: "PERMISSION_DENIED", httpStatus: status)
    }

    private func cookie(_ cookie: HTTPCookie, appliesTo url: URL) -> Bool {
        guard let host = url.host?.lowercased() else { return false }
        let domain = cookie.domain
            .trimmingCharacters(in: CharacterSet(charactersIn: "."))
            .lowercased()
        let domainMatches = host == domain ||
            (cookie.domain.hasPrefix(".") && host.hasSuffix("." + domain))
        guard domainMatches, !cookie.isSecure || url.scheme == "https",
              cookie.expiresDate.map({ $0 > Date() }) ?? true else {
            return false
        }
        let cookiePath = cookie.path.isEmpty ? "/" : cookie.path
        let requestPath = url.path.isEmpty ? "/" : url.path
        guard requestPath.hasPrefix(cookiePath) else { return false }
        if requestPath.count == cookiePath.count || cookiePath.hasSuffix("/") {
            return true
        }
        let boundary = requestPath.index(requestPath.startIndex, offsetBy: cookiePath.count)
        return requestPath[boundary] == "/"
    }
}

@main
private final class AppDelegate: NSObject, NSApplicationDelegate {
    private let ipc = IPCClient()
    private let statusValue = NSTextField(labelWithString: "UNCONFIGURED")
    private let pathValue = NSTextField(labelWithString: "-")
    private let interfaceValue = NSTextField(labelWithString: "-")
    private let handshakeValue = NSTextField(labelWithString: "-")
    private let rootValue = NSTextField(labelWithString: "-")
    private let errorValue = NSTextField(labelWithString: "-")
    private let fnIDField = NSTextField()
    private var authorizationWindow: AuthorizationWindow?
    private var statusItem: NSStatusItem?
    private var statusWatch: DispatchWorkItem?
    private var refreshRetry: DispatchWorkItem?
    private var refreshRetryDelay: TimeInterval = 1
    private var window: NSWindow!
    private var refreshInFlight = false

    @MainActor
    static func main() {
        let application = NSApplication.shared
        let delegate = AppDelegate()
        application.delegate = delegate
        application.setActivationPolicy(.regular)
        // There is no nib/storyboard to create or retain the delegate.
        withExtendedLifetime(delegate) {
            _ = NSApplicationMain(CommandLine.argc, CommandLine.unsafeArgv)
        }
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        let content = NSView(frame: NSRect(x: 0, y: 0, width: 520, height: 444))
        let title = NSTextField(labelWithString: "FnCPN")
        title.font = .systemFont(ofSize: 20, weight: .semibold)
        title.frame = NSRect(x: 24, y: 392, width: 472, height: 28)
        content.addSubview(title)

        addRow("状态", value: statusValue, y: 352, to: content)
        addRow("路径", value: pathValue, y: 318, to: content)
        addRow("接口", value: interfaceValue, y: 284, to: content)
        addRow("最后握手", value: handshakeValue, y: 250, to: content)
        addRow("特权服务", value: rootValue, y: 216, to: content)
        addRow("最近错误", value: errorValue, y: 182, to: content)

        fnIDField.placeholderString = "FN ID"
        fnIDField.usesSingleLineMode = true
        fnIDField.font = .systemFont(ofSize: NSFont.systemFontSize)
        fnIDField.translatesAutoresizingMaskIntoConstraints = false
        let authorizeButton = button("授权", x: 0, y: 0, action: #selector(authorize))
        authorizeButton.translatesAutoresizingMaskIntoConstraints = false
        let authorizationRow = NSStackView(views: [fnIDField, authorizeButton])
        authorizationRow.orientation = .horizontal
        authorizationRow.alignment = .centerY
        authorizationRow.spacing = 12
        authorizationRow.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(authorizationRow)
        NSLayoutConstraint.activate([
            authorizationRow.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 24),
            authorizationRow.centerYAnchor.constraint(equalTo: content.bottomAnchor, constant: -108),
            fnIDField.widthAnchor.constraint(equalToConstant: 270),
            authorizeButton.widthAnchor.constraint(equalToConstant: 96),
        ])
        content.addSubview(button("连接", x: 24, y: 38, action: #selector(connect)))
        content.addSubview(button("断开", x: 132, y: 38, action: #selector(disconnect)))
        content.addSubview(button("重试", x: 240, y: 38, action: #selector(retryConnection)))

        window = NSWindow(
            contentRect: content.frame,
            styleMask: [.titled, .closable, .miniaturizable],
            backing: .buffered,
            defer: false
        )
        window.title = "FnCPN"
        window.isReleasedWhenClosed = false
        window.contentView = content
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        configureStatusItem()

        refresh()
        startStatusWatch()
    }

    func applicationWillTerminate(_ notification: Notification) {
        statusWatch?.cancel()
        refreshRetry?.cancel()
    }

    func applicationShouldTerminateAfterLastWindowClosed(
        _ sender: NSApplication
    ) -> Bool {
        false
    }

    func applicationShouldHandleReopen(
        _ sender: NSApplication,
        hasVisibleWindows flag: Bool
    ) -> Bool {
        if !flag { showWindow() }
        return true
    }

    func application(_ application: NSApplication, open urls: [URL]) {
        for url in urls where url.scheme == "fncpn" && url.host == "authorize" {
            let fnID = url.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            let components = URLComponents(url: url, resolvingAgainstBaseURL: false)
            let requestID = components?.queryItems?.first {
                $0.name == "request"
            }?.value
            if let normalized = normalizeFNID(fnID),
               let requestID,
               !requestID.isEmpty {
                showAuthorization(fnID: normalized, requestID: requestID)
            }
        }
    }

    private func configureStatusItem() {
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        item.button?.title = "FnCPN"
        let menu = NSMenu()
        menu.addItem(withTitle: "打开 FnCPN", action: #selector(showWindow), keyEquivalent: "")
        menu.addItem(.separator())
        menu.addItem(withTitle: "连接", action: #selector(connect), keyEquivalent: "")
        menu.addItem(withTitle: "断开", action: #selector(disconnect), keyEquivalent: "")
        menu.addItem(.separator())
        menu.addItem(withTitle: "退出", action: #selector(quit), keyEquivalent: "q")
        for item in menu.items {
            item.target = self
        }
        item.menu = menu
        statusItem = item
    }

    @objc private func showWindow() {
        window.deminiaturize(nil)
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    @objc private func quit() {
        NSApp.terminate(nil)
    }

    private func addRow(
        _ label: String,
        value: NSTextField,
        y: CGFloat,
        to view: NSView
    ) {
        let name = NSTextField(labelWithString: label)
        name.textColor = .secondaryLabelColor
        name.frame = NSRect(x: 24, y: y, width: 80, height: 24)
        value.frame = NSRect(x: 112, y: y, width: 384, height: 24)
        value.lineBreakMode = .byTruncatingMiddle
        view.addSubview(name)
        view.addSubview(value)
    }

    private func button(
        _ title: String,
        x: CGFloat,
        y: CGFloat,
        action: Selector
    ) -> NSButton {
        let button = NSButton(frame: NSRect(x: x, y: y, width: 96, height: 32))
        button.title = title
        button.bezelStyle = .rounded
        button.target = self
        button.action = action
        return button
    }

    @objc private func authorize() {
        guard let fnID = normalizeFNID(fnIDField.stringValue) else {
            statusValue.stringValue = "INVALID_FN_ID"
            return
        }
        fnIDField.stringValue = fnID
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            do {
                let result = try self?.ipc.call(
                    method: "authorization-begin",
                    params: ["fnId": fnID]
                ) ?? [:]
                guard let requestID = result["requestId"] as? String else {
                    throw NSError(
                        domain: "FnCPN",
                        code: 4,
                        userInfo: [NSLocalizedDescriptionKey: "Invalid authorization request"]
                    )
                }
                DispatchQueue.main.async {
                    self?.showAuthorization(fnID: fnID, requestID: requestID)
                }
            } catch {
                DispatchQueue.main.async {
                    self?.statusValue.stringValue = error.localizedDescription
                }
            }
        }
    }

    private func showAuthorization(fnID: String, requestID: String) {
        fnIDField.stringValue = fnID
        let authorization = AuthorizationWindow(
            fnID: fnID,
            requestID: requestID
        ) {
            [weak self] result in
            if case .failure(let error) = result {
                self?.statusValue.stringValue = error.localizedDescription
            } else {
                self?.refresh()
            }
        }
        authorizationWindow = authorization
        authorization.show()
    }

    @objc private func connect() { perform("connect") }
    @objc private func disconnect() { perform("disconnect") }
    @objc private func retryConnection() { perform("retry") }

    private func perform(_ method: String) {
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            do {
                _ = try self?.ipc.call(method: method)
                DispatchQueue.main.async { self?.refresh() }
            } catch {
                DispatchQueue.main.async {
                    self?.statusValue.stringValue = error.localizedDescription
                }
            }
        }
    }

    private func startStatusWatch() {
        let work = DispatchWorkItem { [weak self] in
            var generation = 0
            while let self, self.statusWatch?.isCancelled == false {
                do {
                    let result = try self.ipc.call(
                        method: "watch-client-status",
                        params: ["after": generation]
                    )
                    generation = result["generation"] as? Int ?? generation
                    if result["changed"] as? Bool == true {
                        DispatchQueue.main.async {
                            self.refresh()
                        }
                    }
                } catch {
                    if self.statusWatch?.isCancelled == true { return }
                    DispatchQueue.main.async {
                        self.showDaemonUnavailable(error)
                    }
                    Thread.sleep(forTimeInterval: 1)
                    generation = 0
                }
            }
        }
        statusWatch = work
        DispatchQueue.global(qos: .utility).async(execute: work)
    }

    private func refresh() {
        guard !refreshInFlight else { return }
        refreshRetry?.cancel()
        refreshRetry = nil
        refreshInFlight = true
        DispatchQueue.global(qos: .utility).async { [weak self] in
            do {
                let diagnostics = try self?.ipc.call(method: "diagnose") ?? [:]
                DispatchQueue.main.async {
                    self?.refreshInFlight = false
                    self?.refreshRetryDelay = 1
                    let status = diagnostics["status"] as? [String: Any] ?? [:]
                    let state = status["state"] as? String ?? "-"
                    self?.statusValue.stringValue = state
                    self?.pathValue.stringValue = status["path"] as? String ?? "-"
                    let interfaceName =
                        diagnostics["networkInterface"] as? String ?? "-"
                    let mtu = diagnostics["mtu"] as? Int ?? 0
                    self?.interfaceValue.stringValue = mtu > 0
                        ? "\(interfaceName) · MTU \(mtu)"
                        : interfaceName
                    if let value = diagnostics["handshakeAge"] as? String {
                        self?.handshakeValue.stringValue = value
                    } else {
                        self?.handshakeValue.stringValue = "-"
                    }
                    let lastError = status["lastError"] as? [String: Any]
                    let errorText = describeFailure(lastError)
                    self?.errorValue.toolTip = errorText
                    let overlap = diagnostics["lanOverlap"] as? Bool == true
                    let displayedError = overlap && errorText.isEmpty
                        ? "LAN_OVERLAP"
                        : errorText
                    self?.errorValue.stringValue =
                        displayedError.isEmpty ? "-" : displayedError
                    let rootAvailable =
                        diagnostics["privilegedAvailable"] as? Bool == true
                    let rootActive =
                        diagnostics["privilegedActive"] as? Bool == true
                    let rootDegraded =
                        diagnostics["privilegedDegraded"] as? Bool == true
                    self?.rootValue.stringValue = !rootAvailable
                        ? "不可用"
                        : rootDegraded
                            ? "降级"
                            : rootActive ? "活动" : "空闲"
                    self?.statusItem?.button?.title = state == "LOCAL" ||
                        state == "DIRECT" ||
                        state == "RELAY" ? "FnCPN ●" : "FnCPN"
                }
            } catch {
                DispatchQueue.main.async {
                    self?.refreshInFlight = false
                    self?.showDaemonUnavailable(error)
                    self?.scheduleRefreshRetry()
                }
            }
        }
    }

    private func scheduleRefreshRetry() {
        guard refreshRetry == nil else { return }
        let delay = refreshRetryDelay
        refreshRetryDelay = min(refreshRetryDelay * 2, 30)
        let retry = DispatchWorkItem { [weak self] in
            guard let self else { return }
            self.refreshRetry = nil
            self.refresh()
        }
        refreshRetry = retry
        DispatchQueue.main.asyncAfter(
            deadline: .now() + delay,
            execute: retry
        )
    }

    private func showDaemonUnavailable(_ error: Error) {
        statusValue.stringValue = "DAEMON_UNAVAILABLE"
        pathValue.stringValue = "-"
        interfaceValue.stringValue = "-"
        handshakeValue.stringValue = "-"
        rootValue.stringValue = "不可用"
        errorValue.stringValue = error.localizedDescription
    }
}
