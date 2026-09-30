import AppKit
import Foundation
import WebKit

private let protocolVersion = 4
private let maximumFrameSize = 256 * 1024

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
            let message = responseError?["message"] as? String ?? "Operation failed"
            throw NSError(
                domain: "FnCPN",
                code: 3,
                userInfo: [NSLocalizedDescriptionKey: message]
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

private final class AuthorizationWindow: NSObject, WKNavigationDelegate, NSWindowDelegate {
    private let fnID: String
    private let requestID: String
    private let completion: (Result<Void, Error>) -> Void
    private var completed = false
    private var window: NSWindow?

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
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        let webView = WKWebView(
            frame: NSRect(x: 0, y: 0, width: 900, height: 680),
            configuration: configuration
        )
        webView.navigationDelegate = self
        let window = NSWindow(
            contentRect: webView.frame,
            styleMask: [.titled, .closable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "FnCPN Authorization"
        window.delegate = self
        window.contentView = webView
        window.center()
        window.makeKeyAndOrderFront(nil)
        self.window = window
        guard let url = URL(
            string: "https://\(fnID).fnos.net/app/fncpn/"
        ) else { return }
        webView.load(URLRequest(url: url))
    }

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void
    ) {
        guard let host = navigationAction.request.url?.host?.lowercased(),
              host == "fnos.net" || host == "\(fnID).fnos.net" else {
            decisionHandler(.cancel)
            return
        }
        decisionHandler(.allow)
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        guard !completed,
              let url = webView.url,
              url.host?.lowercased() == "\(fnID).fnos.net",
              url.path == "/app/fncpn" || url.path.hasPrefix("/app/fncpn/") else {
            return
        }
        webView.configuration.websiteDataStore.httpCookieStore.getAllCookies {
            [weak self] cookies in
            guard let self else { return }
            let matching = cookies.filter {
                self.cookie($0, appliesTo: url)
            }
            guard !matching.isEmpty else { return }
            self.completed = true
            DispatchQueue.global(qos: .userInitiated).async {
                do {
                    let values = matching.map { cookie -> [String: Any] in
                        var value: [String: Any] = [
                            "name": cookie.name,
                            "value": cookie.value,
                            "domain": cookie.domain,
                            "path": cookie.path,
                            "secure": cookie.isSecure,
                            "httpOnly": cookie.isHTTPOnly,
                            "hostOnly": cookie.domain.caseInsensitiveCompare(
                                url.host ?? ""
                            ) == .orderedSame,
                        ]
                        if let expires = cookie.expiresDate {
                            value["expires"] = ISO8601DateFormatter().string(from: expires)
                        }
                        return value
                    }
                    _ = try IPCClient().call(
                        method: "authorize-complete",
                        params: [
                            "requestId": self.requestID,
                            "fnId": self.fnID,
                            "cookies": values,
                        ]
                    )
                    DispatchQueue.main.async {
                        self.window?.close()
                        self.completion(.success(()))
                    }
                } catch {
                    DispatchQueue.main.async {
                        self.completed = true
                        self.window?.close()
                        self.completion(.failure(error))
                    }
                }
            }
        }
    }

    func windowWillClose(_ notification: Notification) {
        guard !completed else { return }
        completed = true
        DispatchQueue.global(qos: .utility).async {
            _ = try? IPCClient().call(
                method: "authorization-cancel",
                params: ["requestId": self.requestID]
            )
        }
    }

    private func cookie(_ cookie: HTTPCookie, appliesTo url: URL) -> Bool {
        guard let host = url.host?.lowercased() else { return false }
        let domain = cookie.domain
            .trimmingCharacters(in: CharacterSet(charactersIn: "."))
            .lowercased()
        let domainMatches = host == domain || host.hasSuffix("." + domain)
        guard domainMatches, !cookie.isSecure || url.scheme == "https" else {
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
        fnIDField.frame = NSRect(x: 24, y: 92, width: 270, height: 30)
        content.addSubview(fnIDField)
        content.addSubview(button("授权", x: 306, y: 92, action: #selector(authorize)))
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
                    let errorText =
                        [lastError?["code"] as? String, lastError?["message"] as? String]
                            .compactMap { $0 }
                            .joined(separator: ": ")
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
