import AppKit
import Foundation

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
            seconds: ["authorize-native", "watch-client-status", "connect", "retry"].contains(method) ? 310 : 30,
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

private struct ConnectionPresentation {
    let state: String
    let needsLogin: Bool
    var connected: Bool { ["LOCAL", "DIRECT", "RELAY"].contains(state) }
    var title: String {
        if needsLogin { return "需要重新登录" }
        return [
            "UNCONFIGURED": "尚未连接", "AUTHORIZING": "正在登录",
            "PROBING": "正在检测网络", "LOCAL": "已在同一局域网",
            "DIRECT": "IPv6 直连", "RELAY": "FN Connect 中继",
            "RECONNECTING": "正在重新连接", "PAUSED": "已断开",
            "ERROR": "连接失败", "DAEMON_UNAVAILABLE": "客户端服务不可用"
        ][state] ?? state
    }
    var actionTitle: String { needsLogin ? "重新登录" : connected ? "断开连接" : "连接" }
    var actionSymbol: String { needsLogin ? "person.badge.key" : connected ? "stop.fill" : "bolt.fill" }

    init(_ status: [String: Any]) {
        state = status["state"] as? String ?? "UNCONFIGURED"
        let failure = status["lastError"] as? [String: Any] ?? [:]
        let code = failure["code"] as? String ?? ""
        let operation = failure["operation"] as? String ?? ""
        needsLogin = state == "AUTH_REQUIRED" || ["AUTH_REQUIRED", "DEVICE_REVOKED"].contains(code) ||
            (code == "PERMISSION_DENIED" && !operation.hasPrefix("credentials.") &&
                !operation.hasPrefix("privileged.") &&
                (failure["httpStatus"] as? Int == 403 || operation.hasPrefix("authorization.")))
    }
}

private func directSummary(_ direct: [String: Any]) -> String {
    switch direct["reason"] as? String ?? "" {
    case "connected": return "已建立 UDP 直连"
    case "local_network": return "同一局域网，无需隧道"
    case "no_local_ipv6": return "本机网络不支持 IPv6"
    case "no_server_ipv6": return "NAS 未提供可用的公网 IPv6"
    case "server_disabled": return "NAS 已禁用公网 IPv6 访问"
    case "handshake_failed": return "UDP 握手未通过，已回退中继"
    case "discovery_failed": return "地址发现失败，已使用中继"
    case "cooldown": return "等待下一次直连检测"
    case "probing", "checking": return "正在检测"
    default: return "尚未检测"
    }
}

private func loginFailureMessage(_ failure: [String: Any]?) -> String {
    guard let failure else { return "" }
    switch failure["code"] as? String ?? "" {
    case "AUTH_REQUIRED":
        let operation = failure["operation"] as? String ?? ""
        return operation.contains("native_session.authenticate")
            ? "用户名或密码无效，请重试"
            : "登录已失效，请重新输入密码"
    case "PERMISSION_DENIED":
        return "该账号没有注册设备所需的管理员权限"
    case "UNAVAILABLE", "TIMEOUT":
        return "暂时无法连接 fnOS，请检查网络后重试"
    case "FAILED_PRECONDITION":
        let message = failure["message"] as? String ?? ""
        return message.contains("two-factor")
            ? "该账号需要两步验证，当前版本暂不支持"
            : describeFailure(failure)
    default:
        return describeFailure(failure)
    }
}

@main
private final class AppDelegate: NSObject, NSApplicationDelegate {
    private let ipc = IPCClient()
    private let statusValue = NSTextField(labelWithString: "尚未连接")
    private let serverValue = NSTextField(labelWithString: "")
    private let pathValue = NSTextField(labelWithString: "-")
    private let addressValue = NSTextField(labelWithString: "-")
    private let interfaceValue = NSTextField(labelWithString: "-")
    private let handshakeValue = NSTextField(labelWithString: "-")
    private let rootValue = NSTextField(labelWithString: "-")
    private let directValue = NSTextField(wrappingLabelWithString: "尚未检测")
    private let errorValue = NSTextView()
    private let setupError = NSTextField(wrappingLabelWithString: "")
    private let fnIDField = NSTextField()
    private let usernameField = NSTextField()
    private let passwordField = NSSecureTextField()
    private let setupView = NSStackView()
    private let detailsView = NSStackView()
    private let progress = NSProgressIndicator()
    private var primaryButton = NSButton()
    private var setupButton = NSButton()
    private var loginButton = NSButton()
    private var retryButton = NSButton()
    private var changeButton = NSButton()
    private var returnButton = NSButton()
    private var menuAction: NSMenuItem?
    private var statusItem: NSStatusItem?
    private var statusWatch: DispatchWorkItem?
    private var refreshRetry: DispatchWorkItem?
    private var refreshRetryDelay: TimeInterval = 1
    private var window: NSWindow!
    private var refreshInFlight = false
    private var refreshPending = false
    private var busy = false
    private var loginInFlight = false
    private var editingServer = false
    private var savedFNID = ""
    private var savedUsername = ""
    private var pendingFNID = ""
    private var externalAuthorizationRequest = ""
    private var diagnostics: [String: Any] = [:]
    private var operationError: [String: Any]?
    private var presentation = ConnectionPresentation([:])

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
        let content = makeContent()
        window = NSWindow(
            contentRect: content.frame,
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered, defer: false
        )
        window.title = "FnCPN"
        window.contentMinSize = NSSize(width: 560, height: 620)
        window.isReleasedWhenClosed = false
        window.contentView = content
        fnIDField.nextKeyView = usernameField
        usernameField.nextKeyView = passwordField
        passwordField.nextKeyView = setupButton
        window.center()
        window.makeKeyAndOrderFront(nil)
        window.makeFirstResponder(fnIDField)
        NSApp.activate(ignoringOtherApps: true)
        configureStatusItem()
        render()
        refresh()
        startStatusWatch()
    }

    private func makeContent() -> NSView {
        let content = NSView(frame: NSRect(x: 0, y: 0, width: 560, height: 620))
        let title = NSTextField(labelWithString: "FnCPN")
        title.font = .systemFont(ofSize: 24, weight: .semibold)
        let icon = NSImageView()
        icon.image = Bundle.main.image(forResource: "AppIcon") ??
            NSImage(systemSymbolName: "network", accessibilityDescription: "FnCPN")
        icon.imageScaling = .scaleProportionallyUpOrDown
        icon.translatesAutoresizingMaskIntoConstraints = false
        icon.widthAnchor.constraint(equalToConstant: 40).isActive = true
        icon.heightAnchor.constraint(equalToConstant: 40).isActive = true
        let header = horizontal([icon, title])
        header.spacing = 14

        setupView.orientation = .vertical
        setupView.alignment = .leading
        setupView.spacing = 18
        let setupTitle = NSTextField(labelWithString: "登录 fnOS")
        setupTitle.font = .systemFont(ofSize: 18, weight: .medium)
        setupView.addArrangedSubview(setupTitle)
        configureLoginField(fnIDField, placeholder: "例如 home-nas", label: "FN Connect ID")
        configureLoginField(usernameField, placeholder: "fnOS 用户名", label: "用户名")
        configureLoginField(passwordField, placeholder: "fnOS 密码", label: "密码")
        passwordField.target = self
        passwordField.action = #selector(submitLogin)
        let loginForm = NSGridView(views: [
            loginRow("FN Connect ID", fnIDField),
            loginRow("用户名", usernameField),
            loginRow("密码", passwordField)
        ])
        loginForm.column(at: 0).width = 104
        loginForm.column(at: 1).width = 320
        loginForm.columnSpacing = 14
        loginForm.rowSpacing = 14
        loginForm.xPlacement = .fill
        fnIDField.nextKeyView = usernameField
        usernameField.nextKeyView = passwordField
        fill(loginForm, in: setupView)
        setupButton = button("登录并连接", symbol: "arrow.right", action: #selector(submitLogin))
        setupButton.widthAnchor.constraint(equalToConstant: 132).isActive = true
        setupView.addArrangedSubview(setupButton)
        setupError.textColor = .systemRed
        fill(setupError, in: setupView)
        returnButton = button("返回连接详情", symbol: "arrow.left", action: #selector(returnToDetails))
        setupView.addArrangedSubview(returnButton)

        detailsView.orientation = .vertical
        detailsView.alignment = .leading
        detailsView.spacing = 16
        serverValue.font = .systemFont(ofSize: 17, weight: .semibold)
        serverValue.lineBreakMode = .byTruncatingMiddle
        serverValue.isSelectable = true
        fill(serverValue, in: detailsView)
        statusValue.font = .systemFont(ofSize: 20, weight: .medium)
        fill(statusValue, in: detailsView)
        let grid = NSGridView(views: [
            row("连接路径", pathValue), row("隧道地址", addressValue),
            row("网络接口", interfaceValue), row("最近握手", handshakeValue),
            row("特权服务", rootValue), row("IPv6 检测", directValue)
        ])
        grid.column(at: 0).width = 84
        grid.columnSpacing = 16
        grid.rowSpacing = 12
        grid.xPlacement = .fill
        grid.yPlacement = .top
        fill(grid, in: detailsView)

        primaryButton = button("连接", symbol: "bolt.fill", action: #selector(primaryAction))
        primaryButton.widthAnchor.constraint(equalToConstant: 126).isActive = true
        loginButton = button("重新登录", symbol: "person.badge.key", action: #selector(relogin))
        retryButton = button("重新检测", symbol: "arrow.clockwise", action: #selector(retryConnection))
        detailsView.addArrangedSubview(horizontal([primaryButton, loginButton, retryButton]))
        let errorLabel = NSTextField(labelWithString: "诊断详情")
        errorLabel.textColor = .secondaryLabelColor
        detailsView.addArrangedSubview(errorLabel)
        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.borderType = .bezelBorder
        errorValue.isEditable = false
        errorValue.isSelectable = true
        errorValue.font = .monospacedSystemFont(ofSize: 11, weight: .regular)
        errorValue.textContainerInset = NSSize(width: 8, height: 8)
        errorValue.isVerticallyResizable = true
        errorValue.isHorizontallyResizable = false
        errorValue.autoresizingMask = [.width]
        errorValue.textContainer?.widthTracksTextView = true
        errorValue.setAccessibilityLabel("诊断详情")
        scroll.documentView = errorValue
        scroll.heightAnchor.constraint(equalToConstant: 100).isActive = true
        fill(scroll, in: detailsView)

        let body = NSStackView(views: [header, setupView, detailsView])
        body.orientation = .vertical
        body.alignment = .leading
        body.spacing = 28
        body.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(body)
        for pane in [setupView, detailsView] {
            pane.translatesAutoresizingMaskIntoConstraints = false
            pane.widthAnchor.constraint(equalTo: body.widthAnchor).isActive = true
        }
        changeButton = button("更换 NAS", symbol: "arrow.left.arrow.right", action: #selector(changeServer))
        progress.style = .spinning
        progress.controlSize = .small
        progress.isDisplayedWhenStopped = false
        let tools = horizontal([
            changeButton, progress,
            button("", symbol: "doc.on.doc", action: #selector(copyDiagnostics), help: "复制诊断信息"),
            button("", symbol: "folder", action: #selector(openLogs), help: "打开日志"),
            button("", symbol: "arrow.clockwise", action: #selector(refresh), help: "刷新状态")
        ])
        tools.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(tools)
        NSLayoutConstraint.activate([
            body.topAnchor.constraint(equalTo: content.topAnchor, constant: 28),
            body.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 28),
            body.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -28),
            body.bottomAnchor.constraint(lessThanOrEqualTo: tools.topAnchor, constant: -16),
            tools.leadingAnchor.constraint(equalTo: body.leadingAnchor),
            tools.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -20)
        ])
        return content
    }

    private func configureLoginField(_ field: NSTextField, placeholder: String, label: String) {
        field.placeholderString = placeholder
        field.setAccessibilityLabel(label)
        field.usesSingleLineMode = true
        field.controlSize = .large
        field.font = .systemFont(ofSize: 15)
        field.setContentHuggingPriority(.defaultLow, for: .horizontal)
        field.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    }

    private func loginRow(_ name: String, _ field: NSTextField) -> [NSView] {
        let label = NSTextField(labelWithString: name)
        label.alignment = .right
        label.textColor = .secondaryLabelColor
        return [label, field]
    }

    private func horizontal(_ views: [NSView]) -> NSStackView {
        let stack = NSStackView(views: views)
        stack.orientation = .horizontal
        stack.alignment = .centerY
        stack.spacing = 10
        return stack
    }

    private func fill(_ view: NSView, in stack: NSStackView) {
        view.translatesAutoresizingMaskIntoConstraints = false
        stack.addArrangedSubview(view)
        view.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }

    private func row(_ name: String, _ value: NSTextField) -> [NSView] {
        let label = NSTextField(labelWithString: name)
        label.textColor = .secondaryLabelColor
        value.isSelectable = true
        value.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        if value !== directValue { value.lineBreakMode = .byTruncatingMiddle }
        return [label, value]
    }

    private func button(_ title: String, symbol: String, action: Selector, help: String? = nil) -> NSButton {
        let result = NSButton(title: title, target: self, action: action)
        result.bezelStyle = .rounded
        result.image = NSImage(systemSymbolName: symbol, accessibilityDescription: help ?? title)
        result.imagePosition = title.isEmpty ? .imageOnly : .imageLeading
        result.toolTip = help ?? title
        result.setAccessibilityLabel(help ?? title)
        result.translatesAutoresizingMaskIntoConstraints = false
        result.heightAnchor.constraint(equalToConstant: 32).isActive = true
        if title.isEmpty { result.widthAnchor.constraint(equalToConstant: 36).isActive = true }
        return result
    }

    func applicationWillTerminate(_ notification: Notification) {
        statusWatch?.cancel()
        refreshRetry?.cancel()
        cancelExternalAuthorization()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if !flag { showWindow() }
        return true
    }

    func application(_ application: NSApplication, open urls: [URL]) {
        for url in urls where url.scheme == "fncpn" && url.host == "authorize" {
            let fnID = url.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            let requestID = URLComponents(url: url, resolvingAgainstBaseURL: false)?
                .queryItems?.first { $0.name == "request" }?.value
            if let normalized = normalizeFNID(fnID), let requestID, !requestID.isEmpty {
                presentLogin(fnID: normalized, username: savedUsername, requestID: requestID)
            }
        }
    }

    private func configureStatusItem() {
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        item.button?.title = "FnCPN"
        let menu = NSMenu()
        menu.addItem(withTitle: "打开 FnCPN", action: #selector(showWindow), keyEquivalent: "")
        menu.addItem(.separator())
        menuAction = menu.addItem(withTitle: "连接", action: #selector(primaryAction), keyEquivalent: "")
        menu.addItem(withTitle: "重新登录", action: #selector(relogin), keyEquivalent: "")
        menu.addItem(.separator())
        menu.addItem(withTitle: "退出", action: #selector(quit), keyEquivalent: "q")
        menu.autoenablesItems = false
        for entry in menu.items { entry.target = self }
        item.menu = menu
        statusItem = item
    }

    @objc private func showWindow() {
        window?.deminiaturize(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    @objc private func quit() { NSApp.terminate(nil) }

    @objc private func changeServer() {
        guard !busy else { return }
        cancelExternalAuthorization()
        editingServer = true
        pendingFNID = ""
        fnIDField.stringValue = ""
        usernameField.stringValue = ""
        passwordField.stringValue = ""
        setupError.stringValue = ""
        render()
        window.makeFirstResponder(fnIDField)
    }

    @objc private func returnToDetails() {
        cancelExternalAuthorization()
        editingServer = false
        passwordField.stringValue = ""
        pendingFNID = ""
        render()
    }

    @objc private func submitLogin() {
        guard !busy else { return }
        guard let fnID = normalizeFNID(fnIDField.stringValue) else {
            setupError.stringValue = "FN Connect ID 格式不正确"
            window?.makeFirstResponder(fnIDField)
            return
        }
        let username = usernameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !username.isEmpty else {
            setupError.stringValue = "请输入 fnOS 用户名"
            window?.makeFirstResponder(usernameField)
            return
        }
        let password = passwordField.stringValue
        guard !password.isEmpty else {
            setupError.stringValue = "请输入 fnOS 密码"
            window?.makeFirstResponder(passwordField)
            return
        }
        fnIDField.stringValue = fnID
        pendingFNID = fnID
        passwordField.stringValue = ""
        loginInFlight = true
        busy = true
        operationError = nil
        setupError.stringValue = ""
        let existingRequest = externalAuthorizationRequest
        externalAuthorizationRequest = ""
        render()
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            do {
                var requestID = existingRequest
                if requestID.isEmpty {
                    let result = try self?.ipc.call(
                        method: "authorization-begin",
                        params: ["fnId": fnID]
                    ) ?? [:]
                    guard let value = result["requestId"] as? String, !value.isEmpty else {
                        throw NSError(
                            domain: "FnCPN",
                            code: 4,
                            userInfo: [NSLocalizedDescriptionKey: "Invalid authorization request"]
                        )
                    }
                    requestID = value
                }
                _ = try self?.ipc.call(method: "authorize-native", params: [
                    "requestId": requestID,
                    "fnId": fnID,
                    "username": username,
                    "password": password,
                ])
                DispatchQueue.main.async { self?.operationFinished(.success(())) }
            } catch {
                DispatchQueue.main.async { self?.operationFinished(.failure(error)) }
            }
        }
    }

    @objc private func relogin() {
        showWindow()
        guard !busy else { return }
        let fnID = pendingFNID.isEmpty ? savedFNID : pendingFNID
        presentLogin(fnID: fnID, username: savedUsername)
    }

    private func presentLogin(fnID: String, username: String, requestID: String = "") {
        showWindow()
        if !externalAuthorizationRequest.isEmpty && externalAuthorizationRequest != requestID {
            cancelExternalAuthorization()
        }
        editingServer = true
        pendingFNID = fnID
        externalAuthorizationRequest = requestID
        fnIDField.stringValue = fnID
        usernameField.stringValue = username
        passwordField.stringValue = ""
        operationError = nil
        setupError.stringValue = ""
        render()
        window.makeFirstResponder(fnID.isEmpty ? fnIDField : username.isEmpty ? usernameField : passwordField)
    }

    private func cancelExternalAuthorization() {
        let requestID = externalAuthorizationRequest
        externalAuthorizationRequest = ""
        guard !requestID.isEmpty else { return }
        DispatchQueue.global(qos: .utility).async {
            _ = try? IPCClient().call(
                method: "authorization-cancel",
                params: ["requestId": requestID]
            )
        }
    }

    @objc private func primaryAction() {
        guard !busy else { return }
        if savedFNID.isEmpty && pendingFNID.isEmpty { showWindow(); return }
        if presentation.needsLogin || savedFNID.isEmpty { relogin() }
        else { perform(presentation.connected ? "disconnect" : "connect") }
    }

    @objc private func retryConnection() {
        if presentation.needsLogin { relogin() } else { perform("retry") }
    }

    private func perform(_ method: String) {
        guard !busy else { return }
        busy = true
        operationError = nil
        render()
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            do {
                _ = try self?.ipc.call(method: method)
                DispatchQueue.main.async { self?.operationFinished(.success(())) }
            } catch {
                DispatchQueue.main.async { self?.operationFinished(.failure(error)) }
            }
        }
    }

    private func operationFinished(_ result: Result<Void, Error>) {
        let completedLogin = loginInFlight
        loginInFlight = false
        busy = false
        if case .failure(let error) = result {
            let failure = (error as NSError).userInfo["failure"] as? [String: Any]
            operationError = failure ?? ["code": "UNAVAILABLE", "message": error.localizedDescription]
            if completedLogin {
                editingServer = true
            }
            if failure?["code"] as? String == "CANCELED" {
                operationError = nil
                pendingFNID = ""
            }
        } else {
            operationError = nil
            pendingFNID = ""
            if completedLogin {
                editingServer = false
            }
        }
        render()
        refresh()
        showWindow()
    }

    private func applyDiagnostics(_ value: [String: Any]) {
        diagnostics = value
        savedFNID = value["fnId"] as? String ?? ""
        savedUsername = value["username"] as? String ?? savedUsername
        let status = value["status"] as? [String: Any] ?? [:]
        if pendingFNID.isEmpty && ConnectionPresentation(status).connected && status["lastError"] == nil {
            operationError = nil
        }
        render()
    }

    private func render() {
        let pendingTarget = !pendingFNID.isEmpty && pendingFNID != savedFNID
        let displayed = pendingTarget ? [:] : diagnostics
        var status = displayed["status"] as? [String: Any] ?? [:]
        if pendingTarget { status["state"] = busy ? "AUTHORIZING" : "AUTH_REQUIRED" }
        if let operationError { status["lastError"] = operationError }
        presentation = ConnectionPresentation(status)
        let fnID = pendingFNID.isEmpty ? savedFNID : pendingFNID
        let setup = editingServer || fnID.isEmpty
        setupView.isHidden = !setup
        detailsView.isHidden = setup
        returnButton.isHidden = savedFNID.isEmpty
        changeButton.isHidden = setup
        changeButton.isEnabled = !busy
        setupButton.isEnabled = !busy
        fnIDField.isEnabled = !busy
        usernameField.isEnabled = !busy
        passwordField.isEnabled = !busy
        primaryButton.isEnabled = !busy
        primaryButton.title = presentation.actionTitle
        primaryButton.toolTip = primaryButton.title
        primaryButton.image = NSImage(systemSymbolName: presentation.actionSymbol, accessibilityDescription: primaryButton.title)
        primaryButton.setAccessibilityLabel(primaryButton.title)
        loginButton.isHidden = presentation.needsLogin
        loginButton.isEnabled = !busy
        retryButton.isEnabled = !busy
        retryButton.isHidden = presentation.needsLogin || savedFNID.isEmpty
        menuAction?.title = presentation.actionTitle
        menuAction?.isEnabled = !busy
        serverValue.stringValue = fnID
        serverValue.toolTip = fnID
        statusValue.stringValue = busy && loginInFlight ? "正在登录" : presentation.title
        statusValue.textColor = presentation.needsLogin ? .systemOrange :
            presentation.connected ? .systemGreen : .labelColor
        pathValue.stringValue = [
            "local": "局域网", "ipv6": "IPv6 / UDP", "fn-connect": "FN Connect / WSS"
        ][status["path"] as? String ?? ""] ?? "-"
        addressValue.stringValue = displayed["clientAddress"] as? String ?? "-"
        let interfaceName = displayed["networkInterface"] as? String ?? "-"
        let mtu = displayed["mtu"] as? Int ?? 0
        interfaceValue.stringValue = mtu > 0 ? "\(interfaceName) · MTU \(mtu)" : interfaceName
        handshakeValue.stringValue = displayed["handshakeAge"] as? String ?? "-"
        rootValue.stringValue = diagnostics["privilegedAvailable"] as? Bool != true ? "不可用" :
            diagnostics["privilegedDegraded"] as? Bool == true ? "降级" :
            diagnostics["privilegedActive"] as? Bool == true ? "活动" : "就绪"
        let direct = displayed["direct"] as? [String: Any] ?? [:]
        directValue.stringValue = directSummary(direct)
        var messages = [describeFailure(status["lastError"] as? [String: Any])]
        let directError = describeFailure(direct["lastError"] as? [String: Any])
        if !directError.isEmpty { messages.append("IPv6: \(directError)") }
        if let endpoint = direct["endpoint"] as? String { messages.append("UDP \(endpoint)") }
        if let count = direct["candidateCount"] as? Int, count > 0 {
            messages.append("IPv6 候选 \(count)，已尝试 \(direct["attemptCount"] as? Int ?? 0)")
        }
        if displayed["lanOverlap"] as? Bool == true { messages.append("本地与 NAS 网段重叠，仅保留隧道内 NAS 路由") }
        errorValue.string = messages.filter { !$0.isEmpty }.joined(separator: "\n")
        if errorValue.string.isEmpty { errorValue.string = "无异常" }
        setupError.stringValue = setup ? loginFailureMessage(status["lastError"] as? [String: Any]) : ""
        setupError.maximumNumberOfLines = 3
        setupError.toolTip = setupError.stringValue
        if busy { progress.startAnimation(nil) } else { progress.stopAnimation(nil) }
        statusItem?.button?.title = presentation.connected ? "FnCPN ●" : "FnCPN"
    }

    @objc private func copyDiagnostics() {
        var value = diagnostics
        if let operationError { value["operationError"] = operationError }
        guard let data = try? JSONSerialization.data(withJSONObject: value, options: [.prettyPrinted, .sortedKeys]),
              let text = String(data: data, encoding: .utf8) else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }

    @objc private func openLogs() {
        let directory = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Logs/FnCPN")
        NSWorkspace.shared.open(directory)
    }

    private func startStatusWatch() {
        let work = DispatchWorkItem { [weak self] in
            var generation = 0
            while let self, self.statusWatch?.isCancelled == false {
                do {
                    let result = try self.ipc.call(method: "watch-client-status", params: ["after": generation])
                    generation = result["generation"] as? Int ?? generation
                    if result["changed"] as? Bool == true {
                        DispatchQueue.main.async { self.refresh() }
                    }
                } catch {
                    if self.statusWatch?.isCancelled == true { return }
                    DispatchQueue.main.async { self.showDaemonUnavailable(error) }
                    Thread.sleep(forTimeInterval: 1)
                    generation = 0
                }
            }
        }
        statusWatch = work
        DispatchQueue.global(qos: .utility).async(execute: work)
    }

    @objc private func refresh() {
        guard !refreshInFlight else { refreshPending = true; return }
        refreshRetry?.cancel()
        refreshRetry = nil
        refreshInFlight = true
        DispatchQueue.global(qos: .utility).async { [weak self] in
            do {
                let diagnostics = try self?.ipc.call(method: "diagnose") ?? [:]
                DispatchQueue.main.async {
                    guard let self else { return }
                    self.refreshInFlight = false
                    self.refreshRetryDelay = 1
                    self.applyDiagnostics(diagnostics)
                    if self.refreshPending {
                        self.refreshPending = false
                        self.refresh()
                    }
                }
            } catch {
                DispatchQueue.main.async {
                    self?.refreshInFlight = false
                    self?.refreshPending = false
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
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: retry)
    }

    private func showDaemonUnavailable(_ error: Error) {
        diagnostics["status"] = [
            "state": "DAEMON_UNAVAILABLE",
            "lastError": ["code": "UNAVAILABLE", "message": error.localizedDescription]
        ]
        diagnostics["privilegedAvailable"] = false
        render()
    }
}
