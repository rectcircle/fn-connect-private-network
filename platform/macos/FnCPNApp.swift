import AppKit
import Foundation
import ServiceManagement
import UserNotifications
import WebKit

// Source-language keys live in the en/zh-Hans .lproj catalogs. Unsupported
// primary languages use English; Chinese variants use Simplified Chinese.
private let uiLanguage = (Locale.preferredLanguages.first ?? "en")
    .lowercased().hasPrefix("zh") ? "zh-Hans" : "en"
private let localizationBundle: Bundle = {
    guard let path = Bundle.main.path(forResource: uiLanguage, ofType: "lproj"),
          let bundle = Bundle(path: path) else { return Bundle.main }
    return bundle
}()

private func L(_ key: String, _ arguments: String...) -> String {
    let template = localizationBundle.localizedString(forKey: key, value: key, table: nil)
    // Replace tokens in the template only, never inside user-supplied arguments.
    let tokens = try! NSRegularExpression(pattern: #"\{([0-9]+)\}"#)
    let source = template as NSString
    var result = template
    for match in tokens.matches(in: template, range: NSRange(location: 0, length: source.length)).reversed() {
        let index = Int(source.substring(with: match.range(at: 1)))!
        if arguments.indices.contains(index), let range = Range(match.range, in: result) {
            result.replaceSubrange(range, with: arguments[index])
        }
    }
    return result
}

private let protocolVersion = 4
private let maximumFrameSize = 256 * 1024

private func failureSummary(_ failure: [String: Any]) -> String {
    if failure["remoteCode"] as? String == "TWO_FACTOR_REQUIRED" {
        return L("该账号需要两步验证，当前版本暂不支持")
    }
    let keys = [
        "INVALID_ARGUMENT": "请求参数无效，请检查输入。",
        "AUTH_REQUIRED": "请重新登录后重试。",
        "PERMISSION_DENIED": "权限不足，无法完成操作。",
        "NOT_FOUND": "目标不存在，请刷新后重试。",
        "ALREADY_EXISTS": "配置冲突，请刷新后重试。",
        "CONFLICT": "配置冲突，请刷新后重试。",
        "FAILED_PRECONDITION": "操作所需条件尚未满足。",
        "DEVICE_REVOKED": "设备授权已撤销，请重新登录。",
        "UNAVAILABLE": "服务暂不可用，请稍后重试。",
        "TIMEOUT": "请求超时，请重试。",
        "RESOURCE_EXHAUSTED": "资源不足，请稍后重试。",
        "DISCOVERY_FAILED": "网络地址发现失败，请检查网络。",
        "PROTOCOL_ERROR": "响应格式无效，请重试。",
        "CANCELED": "操作已取消。",
        "INTERNAL": "操作失败，请重试或查看诊断详情。"
    ]
    return L(keys[failure["code"] as? String ?? ""] ?? "操作失败，请重试或查看诊断详情。")
}

private func describeFailure(_ failure: [String: Any]?) -> String {
    guard let failure else { return "" }
    var parts = [failure["code"] as? String, failure["message"] as? String]
        .compactMap { $0 }
    if let operation = failure["operation"] as? String { parts.append("[\(operation)]") }
    if let status = failure["httpStatus"] as? Int { parts.append("HTTP \(status)") }
    if let code = failure["remoteCode"] as? String { parts.append("remote \(code)") }
    if let detail = failure["detail"] as? String { parts.append(detail) }
    if let id = failure["requestId"] as? String { parts.append("request \(id)") }
    return failureSummary(failure) + "\n" + L("详细信息") + ": " + parts.joined(separator: ": ")
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
            seconds: ["authorize-native", "watch-client-status"].contains(method) ? 310 : 30,
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
                userInfo: [NSLocalizedDescriptionKey: L("客户端服务响应无效")]
            )
        }
        let responseData = try readExactly(Int(responseLength), from: descriptor)
        guard let response = try JSONSerialization.jsonObject(
            with: responseData
        ) as? [String: Any] else {
            throw NSError(
                domain: "FnCPN",
                code: 2,
                userInfo: [NSLocalizedDescriptionKey: L("客户端服务响应无效")]
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
            userInfo: [NSLocalizedDescriptionKey: L("客户端服务响应无效")]
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

private final class LaunchAgentManager {
    static let label = "cn.rectcircle.fncpn.client"
    static let plistFileName = "cn.rectcircle.fncpn.client.plist"

    var plistPath: String {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents")
            .appendingPathComponent(Self.plistFileName)
            .path
    }

    func installPlist() -> Bool {
        let contents = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents")
        try? FileManager.default.createDirectory(
            at: contents, withIntermediateDirectories: true
        )
        guard let template = Bundle.main.url(
            forResource: "client-agent", withExtension: "plist"
        ),
        let data = try? Data(contentsOf: template) else {
            return false
        }
        if (try? Data(contentsOf: URL(fileURLWithPath: plistPath))) == data {
            return true
        }
        do {
            try data.write(to: URL(fileURLWithPath: plistPath), options: .atomic)
            return true
        } catch {
            return false
        }
    }

    func run(_ arguments: [String]) -> Bool {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/launchctl")
        process.arguments = arguments
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        do {
            try process.run()
            process.waitUntilExit()
            return process.terminationStatus == 0
        } catch {
            return false
        }
    }

    func isLoaded() -> Bool {
        run(["print", "gui/\(getuid())/\(Self.label)"])
    }

    func bootstrap() -> Bool {
        _ = run(["bootout", "gui/\(getuid())/\(Self.label)"])
        guard run(["bootstrap", "gui/\(getuid())", plistPath]) else {
            return false
        }
        // The plist sets RunAtLoad=false (auto-start is a separate toggle), so
        // bootstrap only registers the service. Kickstart actually launches the
        // client daemon; without it the daemon would be loaded but never run.
        return run(["kickstart", "gui/\(getuid())/\(Self.label)"])
    }

    func bootout() -> Bool {
        run(["bootout", "gui/\(getuid())/\(Self.label)"])
    }

    /// Ensures the registered client daemon is actually running. A no-op when
    /// it is already up; launches it when it was registered but not running.
    func kickstart() -> Bool {
        run(["kickstart", "gui/\(getuid())/\(Self.label)"])
    }
}

// Generated from the same connectionMark() as the app icon, including Retina variants.
private func connectionStatusIcon(_ state: String) -> NSImage {
    let variant: String
    switch state {
    case "LOCAL", "DIRECT", "RELAY": variant = "connected"
    case "AUTHORIZING", "PROBING", "RECONNECTING": variant = "working"
    case "ERROR", "DAEMON_UNAVAILABLE", "AUTH_REQUIRED": variant = "attention"
    default: variant = "offline"
    }
    let image = Bundle.main.image(forResource: "Status-" + variant) ??
        NSImage(systemSymbolName: "link", accessibilityDescription: "FnCPN")!
    image.size = NSSize(width: 22, height: 18)
    image.isTemplate = true
    return image
}

private struct ConnectionPresentation {
    let state: String
    let needsLogin: Bool
    var connected: Bool { ["LOCAL", "DIRECT", "RELAY"].contains(state) }
    var title: String {
        if needsLogin { return L("需要重新登录") }
        return [
            "UNCONFIGURED": L("尚未连接"), "AUTHORIZING": L("正在登录"),
            "PROBING": L("正在检测网络"), "LOCAL": L("已在同一局域网"),
            "DIRECT": L("IPv6 直连"), "RELAY": L("FN Connect 中继"),
            "RECONNECTING": L("正在重新连接"), "PAUSED": L("已断开"),
            "ERROR": L("连接失败"), "DAEMON_UNAVAILABLE": L("客户端服务不可用")
        ][state] ?? state
    }
    var actionTitle: String { needsLogin ? L("重新登录") : connected ? L("断开连接") : L("连接") }
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
    case "connected": return L("已建立 UDP 直连")
    case "local_network": return L("同一局域网，无需隧道")
    case "no_local_ipv6": return L("本机网络不支持 IPv6")
    case "no_server_ipv6": return L("NAS 未提供可用的公网 IPv6")
    case "server_disabled": return L("NAS 已禁用公网 IPv6 访问")
    case "handshake_failed": return L("UDP 握手未通过，已回退中继")
    case "discovery_failed": return L("地址发现失败，已使用中继")
    case "cooldown": return L("等待下一次直连检测")
    case "probing", "checking": return L("正在检测")
    default: return L("尚未检测")
    }
}

private func loginFailureMessage(_ failure: [String: Any]?) -> String {
    guard let failure else { return "" }
    switch failure["code"] as? String ?? "" {
    case "AUTH_REQUIRED":
        let operation = failure["operation"] as? String ?? ""
        return operation.contains("native_session.authenticate")
            ? L("用户名或密码无效，请重试")
            : L("登录已失效，请重新输入密码")
    case "PERMISSION_DENIED":
        return L("该账号权限不足，无法完成授权")
    case "UNAVAILABLE", "TIMEOUT":
        return L("暂时无法连接 fnOS，请检查网络后重试")
    case "FAILED_PRECONDITION":
        return failure["remoteCode"] as? String == "TWO_FACTOR_REQUIRED"
            ? L("该账号需要两步验证，当前版本暂不支持")
            : describeFailure(failure)
    default:
        return describeFailure(failure)
    }
}

/// Button that shows a pointing-hand cursor on hover, for any interactive control.
private final class HandCursorButton: NSButton {
    override func resetCursorRects() {
        addCursorRect(bounds, cursor: .pointingHand)
    }
}

@main
private final class AppDelegate: NSObject, NSApplicationDelegate {
    private let ipc = IPCClient()
    private let launchAgent = LaunchAgentManager()
    private let statusValue = NSTextField(labelWithString: L("尚未连接"))
    private let serverValue = NSTextField(labelWithString: "")
    private let pathValue = NSTextField(labelWithString: "-")
    private let addressValue = NSTextField(labelWithString: "-")
    private let nasValue = NSTextField(labelWithString: "-")
    private let interfaceValue = NSTextField(labelWithString: "-")
    private let handshakeValue = NSTextField(labelWithString: "-")
    private let rootValue = NSTextField(labelWithString: "-")
    private let directValue = NSTextField(wrappingLabelWithString: L("尚未检测"))
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
    private var adminButton = NSButton()
    private var browserButton = NSButton()
    private var quitButton = NSButton()
    private var menuAction: NSMenuItem?
    private var autoStartItem: NSMenuItem?
    private var statusItem: NSStatusItem?
    private var reloginMenuItem: NSMenuItem?
    private var statusWatch: DispatchWorkItem?
    private var refreshRetry: DispatchWorkItem?
    private var refreshRetryDelay: TimeInterval = 1
    private var window: NSWindow!
    private var adminWindow: NSWindow?
    private var adminWindowOpen = false
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
    private var privilegedStartPrompted = false
    // When false, the window is hidden until the first diagnostics round trip
    // returns, so an already-logged-in user does not see a flash of the login
    // page before the details page renders.
    private var initialWindowShown = false

    @MainActor
    static func main() {
        let application = NSApplication.shared
        let delegate = AppDelegate()
        application.delegate = delegate
        // Menu-bar/accessory app: no Dock icon, lives only in the status bar.
        application.setActivationPolicy(.accessory)
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
        window.contentMinSize = NSSize(width: 680, height: 440)
        window.isReleasedWhenClosed = false
        window.contentView = content
        fnIDField.nextKeyView = usernameField
        usernameField.nextKeyView = passwordField
        passwordField.nextKeyView = setupButton
        window.center()
        // Do not show the window yet: we show it once the first diagnostics
        // round trip returns so an already-logged-in user goes straight to the
        // details page (no flash of the login page first). A fallback timer
        // guarantees the window appears even if the daemon never responds.
        window.makeFirstResponder(fnIDField)
        DispatchQueue.main.asyncAfter(deadline: .now() + 2.0) { [weak self] in
            self?.showInitialWindowIfNeeded()
        }
        setupLaunchAgent()
        configureStatusItem()
        render()
        refresh()
        startStatusWatch()
    }

    /// Installs (if needed) and boots the user-level client LaunchAgent so the
    /// client daemon is running for this app session. The daemon's resume()
    /// automatically reconnects when AutoConnect was left true on a prior quit.
    private func setupLaunchAgent() {
        guard launchAgent.installPlist() else { return }
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self else { return }
            if self.launchAgent.isLoaded() {
                // Registered but possibly not running (RunAtLoad=false): make
                // sure the daemon process is actually up before we talk to it.
                _ = self.launchAgent.kickstart()
            } else {
                _ = self.launchAgent.bootstrap()
            }
            // Interrupt any stale watcher/socket once the daemon is up.
            DispatchQueue.main.async { self.refresh() }
        }
    }

    private func makeContent() -> NSView {
        let content = NSView(frame: NSRect(x: 0, y: 0, width: 680, height: 520))
        let title = NSTextField(labelWithString: "FnCPN")
        title.font = .systemFont(ofSize: 24, weight: .semibold)
        // A gray, body-sized version string shown right after the title.
        let titleRow = NSStackView(views: [title])
        titleRow.orientation = .horizontal
        titleRow.alignment = .firstBaseline
        titleRow.spacing = 8
        if let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String,
           !version.isEmpty {
            let versionLabel = NSTextField(labelWithString: "v\(version)")
            versionLabel.font = .systemFont(ofSize: 13)
            versionLabel.textColor = .secondaryLabelColor
            titleRow.addArrangedSubview(versionLabel)
        }
        let icon = NSImageView()
        icon.image = Bundle.main.image(forResource: "AppIcon") ??
            NSImage(systemSymbolName: "network", accessibilityDescription: "FnCPN")
        icon.imageScaling = .scaleProportionallyUpOrDown
        icon.translatesAutoresizingMaskIntoConstraints = false
        icon.widthAnchor.constraint(equalToConstant: 40).isActive = true
        icon.heightAnchor.constraint(equalToConstant: 40).isActive = true
        let header = horizontal([icon, titleRow])
        header.spacing = 14

        setupView.orientation = .vertical
        setupView.alignment = .leading
        setupView.spacing = 18
        let setupTitle = NSTextField(labelWithString: L("登录 fnOS"))
        setupTitle.font = .systemFont(ofSize: 18, weight: .medium)
        setupView.addArrangedSubview(setupTitle)
        configureLoginField(fnIDField, placeholder: L("例如 home-nas"), label: "FN Connect ID")
        configureLoginField(usernameField, placeholder: L("fnOS 用户名"), label: L("用户名"))
        configureLoginField(passwordField, placeholder: L("fnOS 密码"), label: L("密码"))
        passwordField.target = self
        passwordField.action = #selector(submitLogin)
        let loginForm = NSGridView(views: [
            loginRow("FN Connect ID", fnIDField),
            loginRow(L("用户名"), usernameField),
            loginRow(L("密码"), passwordField)
        ])
        loginForm.column(at: 0).width = 104
        loginForm.column(at: 1).width = 320
        loginForm.columnSpacing = 14
        loginForm.rowSpacing = 14
        loginForm.xPlacement = .fill
        fnIDField.nextKeyView = usernameField
        usernameField.nextKeyView = passwordField
        fill(loginForm, in: setupView)
        setupButton = button(L("登录并连接"), symbol: "arrow.right", action: #selector(submitLogin))
        setupButton.widthAnchor.constraint(greaterThanOrEqualToConstant: 132).isActive = true
        setupView.addArrangedSubview(setupButton)
        setupError.textColor = .systemRed
        fill(setupError, in: setupView)
        returnButton = button(L("返回连接详情"), symbol: "arrow.left", action: #selector(returnToDetails))
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
        browserButton = iconButton("arrow.up.right.square", action: #selector(openNASInBrowser), help: L("在浏览器中打开 NAS 管理页面"))

        // Keep the value cell's height equal to the text label so the NAS IP row
        // stays exactly as tall as every other row (avoids the vertical mismatch
        // where the label renders higher than the content). The icon is layered on
        // top of the value, center-aligned, so it does not affect row height.
        let nasContainer = NSView()
        nasContainer.translatesAutoresizingMaskIntoConstraints = false
        nasValue.translatesAutoresizingMaskIntoConstraints = false
        browserButton.translatesAutoresizingMaskIntoConstraints = false
        nasContainer.addSubview(nasValue)
        nasContainer.addSubview(browserButton)
        nasContainer.setContentHuggingPriority(.required, for: .horizontal)
        nasContainer.setContentCompressionResistancePriority(.required, for: .horizontal)
        NSLayoutConstraint.activate([
            nasValue.leadingAnchor.constraint(equalTo: nasContainer.leadingAnchor),
            nasValue.topAnchor.constraint(equalTo: nasContainer.topAnchor),
            nasValue.bottomAnchor.constraint(equalTo: nasContainer.bottomAnchor),
            nasContainer.heightAnchor.constraint(equalTo: nasValue.heightAnchor),
            browserButton.leadingAnchor.constraint(equalTo: nasValue.trailingAnchor, constant: 8),
            browserButton.centerYAnchor.constraint(equalTo: nasValue.centerYAnchor),
            browserButton.trailingAnchor.constraint(equalTo: nasContainer.trailingAnchor),
        ])

        let grid = NSGridView(views: [
            row(L("连接路径"), pathValue), row(L("隧道地址"), addressValue),
            row(L("网络接口"), interfaceValue), row(L("最近握手"), handshakeValue),
            row(L("特权服务"), rootValue), row(L("IPv6 检测"), directValue),
            [nasLabel("NAS IP"), nasContainer]
        ])
        grid.column(at: 0).width = 84
        grid.columnSpacing = 16
        grid.rowSpacing = 12
        grid.xPlacement = .fill
        grid.yPlacement = .top
        // The NAS row's value cell must hug its content instead of being stretched
        // to column width; otherwise the icon gets pushed to the far right.
        grid.cell(for: nasContainer)?.xPlacement = .leading
        fill(grid, in: detailsView)

        nasValue.isSelectable = true
        nasValue.lineBreakMode = .byTruncatingMiddle
        nasValue.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        primaryButton = button(L("连接"), symbol: "bolt.fill", action: #selector(primaryAction))
        primaryButton.widthAnchor.constraint(greaterThanOrEqualToConstant: 126).isActive = true
        loginButton = button(L("重新登录"), symbol: "person.badge.key", action: #selector(relogin))
        retryButton = button(L("重新检测"), symbol: "arrow.clockwise", action: #selector(retryConnection))
        detailsView.addArrangedSubview(horizontal([primaryButton, loginButton, retryButton]))
        let errorLabel = NSTextField(labelWithString: L("诊断详情"))
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
        errorValue.setAccessibilityLabel(L("诊断详情"))
        scroll.documentView = errorValue
        scroll.translatesAutoresizingMaskIntoConstraints = false
        scroll.heightAnchor.constraint(greaterThanOrEqualToConstant: 80).isActive = true
        scroll.heightAnchor.constraint(lessThanOrEqualToConstant: 220).isActive = true
        fill(scroll, in: detailsView)

        let body = NSStackView(views: [header, setupView, detailsView])
        body.orientation = .vertical
        body.alignment = .leading
        body.spacing = 28
        body.translatesAutoresizingMaskIntoConstraints = false
        for pane in [setupView, detailsView] {
            pane.translatesAutoresizingMaskIntoConstraints = false
            pane.widthAnchor.constraint(equalTo: body.widthAnchor).isActive = true
            // Pane contents must decide their own height; never stretch panes
            // to fill spare window space (which would blow up spacing between
            // the login fields).
            pane.setContentHuggingPriority(.required, for: .vertical)
            pane.setContentCompressionResistancePriority(.required, for: .vertical)
        }
        changeButton = button(L("更换 NAS"), symbol: "arrow.left.arrow.right", action: #selector(changeServer))
        adminButton = button(L("管理后台"), symbol: "globe", action: #selector(openAdmin), help: L("在浏览器中打开管理后台"))
        progress.style = .spinning
        progress.controlSize = .small
        progress.isDisplayedWhenStopped = false
        let tools = horizontal([
            changeButton, adminButton, progress,
            button("", symbol: "doc.on.doc", action: #selector(copyDiagnostics), help: L("复制诊断信息")),
            button("", symbol: "folder", action: #selector(openLogs), help: L("打开日志")),
            button("", symbol: "arrow.clockwise", action: #selector(refresh), help: L("刷新状态"))
        ])
        tools.translatesAutoresizingMaskIntoConstraints = false
        quitButton = button(L("退出"), symbol: "power", action: #selector(quit), help: L("停止连接并退出"))
        quitButton.translatesAutoresizingMaskIntoConstraints = false

        // Stack the body and the tools row together and center the whole block
        // vertically. This keeps the login button and the quit button tightly
        // grouped regardless of window height (spare height is split evenly top
        // and bottom, instead of leaving a huge gap between tools and content).
        let footer = NSView()
        footer.translatesAutoresizingMaskIntoConstraints = false
        footer.addSubview(tools)
        footer.addSubview(quitButton)
        let container = NSStackView(views: [body, footer])
        container.orientation = .vertical
        container.alignment = .leading
        container.spacing = 16
        container.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(container)
        NSLayoutConstraint.activate([
            container.centerYAnchor.constraint(equalTo: content.centerYAnchor),
            container.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 28),
            container.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -28),
            footer.heightAnchor.constraint(equalTo: tools.heightAnchor),
            footer.widthAnchor.constraint(equalTo: container.widthAnchor),
            tools.leadingAnchor.constraint(equalTo: footer.leadingAnchor),
            tools.centerYAnchor.constraint(equalTo: footer.centerYAnchor),
            tools.trailingAnchor.constraint(lessThanOrEqualTo: quitButton.leadingAnchor, constant: -12),
            quitButton.trailingAnchor.constraint(equalTo: footer.trailingAnchor),
            quitButton.centerYAnchor.constraint(equalTo: footer.centerYAnchor)
        ])
        for pane in [body, footer] {
            pane.setContentHuggingPriority(.defaultLow, for: .horizontal)
            pane.widthAnchor.constraint(equalTo: container.widthAnchor).isActive = true
        }
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

    private func nasLabel(_ name: String) -> NSTextField {
        let label = NSTextField(labelWithString: name)
        label.textColor = .secondaryLabelColor
        return label
    }

    private func button(_ title: String, symbol: String, action: Selector, help: String? = nil) -> NSButton {
        let result = HandCursorButton(title: title, target: self, action: action)
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

    /// Icon-only button with no border/bezel, sized to match adjacent text baselines.
    private func iconButton(_ symbolName: String, action: Selector, help: String) -> NSButton {
        let result = HandCursorButton(title: "", target: self, action: action)
        result.isBordered = false
        result.image = NSImage(systemSymbolName: symbolName, accessibilityDescription: help)
        result.imagePosition = .imageOnly
        result.toolTip = help
        result.setAccessibilityLabel(help)
        result.translatesAutoresizingMaskIntoConstraints = false
        result.widthAnchor.constraint(equalToConstant: 20).isActive = true
        result.heightAnchor.constraint(equalToConstant: 20).isActive = true
        result.contentTintColor = .secondaryLabelColor
        result.setContentHuggingPriority(.required, for: .horizontal)
        result.setContentCompressionResistancePriority(.required, for: .horizontal)
        return result
    }

    func applicationWillTerminate(_ notification: Notification) {
        statusWatch?.cancel()
        refreshRetry?.cancel()
        cancelExternalAuthorization()
        // Stop tunneling on quit by halting the client daemon. The daemon's
        // AutoConnect flag is preserved on disk, so the next app launch
        // reconnects automatically.
        _ = launchAgent.bootout()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        // Reopening from Finder re-activates the app, which can transiently place
        // a regular Dock icon. Re-assert the menu-bar/accessory policy so no Dock
        // icon ever appears (the status bar remains the only entry point), unless
        // the management WebView (which needs a Dock icon) is showing.
        if !adminWindowOpen {
            NSApp.setActivationPolicy(.accessory)
            DispatchQueue.main.async {
                guard !self.adminWindowOpen else { return }
                NSApp.setActivationPolicy(.accessory)
            }
        }
        if !flag { showWindow() }
        return true
    }

    func applicationDidBecomeActive(_ notification: Notification) {
        // Finder "Open" of a running app, or any external activation, can raise a
        // regular Dock icon briefly. Force accessory so the Dock icon stays hidden.
        guard !adminWindowOpen else { return }
        NSApp.setActivationPolicy(.accessory)
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
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        item.button?.image = connectionStatusIcon("UNCONFIGURED")
        item.button?.setAccessibilityLabel(L("FnCPN：尚未连接"))
        let menu = NSMenu()
        menu.addItem(withTitle: L("打开 FnCPN"), action: #selector(showWindow), keyEquivalent: "")
        menu.addItem(.separator())
        menuAction = menu.addItem(withTitle: L("连接"), action: #selector(statusBarAction), keyEquivalent: "")
        // The item title becomes "重新登录" exactly when menuAction also shows
        // "重新登录" (i.e. the session needs re-authentication). The dedicated
        // relogin item is hidden in that case so the menu does not show two
        // duplicate "重新登录" entries; it stays visible while connected so the
        // user can switch accounts.
        reloginMenuItem = menu.addItem(withTitle: L("重新登录"), action: #selector(relogin), keyEquivalent: "")
        menu.addItem(.separator())
        autoStartItem = menu.addItem(
            withTitle: "", action: #selector(toggleAutoStart), keyEquivalent: ""
        )
        refreshAutoStartItem()
        menu.addItem(.separator())
        menu.addItem(withTitle: L("退出"), action: #selector(quit), keyEquivalent: "q")
        menu.autoenablesItems = false
        for entry in menu.items { entry.target = self }
        item.menu = menu
        statusItem = item
    }

    private func refreshAutoStartItem() {
        // SMAppService registers the whole app as a macOS login item, which is
        // what shows up in System Settings > General > Login Items and lets the
        // app launch at login (running in the background with its status bar).
        let enabled = SMAppService.mainApp.status == .enabled
        autoStartItem?.title = L("开机自启")
        autoStartItem?.state = enabled ? .on : .off
    }

    @objc private func toggleAutoStart() {
        let currentlyEnabled = SMAppService.mainApp.status == .enabled
        do {
            if currentlyEnabled {
                try SMAppService.mainApp.unregister()
            } else {
                try SMAppService.mainApp.register()
            }
        } catch {
            let alert = NSAlert()
            alert.messageText = L("无法设置开机自启")
            alert.informativeText = error.localizedDescription
            alert.alertStyle = .warning
            alert.runModal()
        }
        refreshAutoStartItem()
    }

    @objc private func showWindow() {
        window?.deminiaturize(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    @objc private func quit() {
        // applicationWillTerminate() halts the client daemon; AutoConnect is
        // preserved so the next launch reconnects automatically.
        NSApp.terminate(nil)
    }

    @objc private func openNASInBrowser() {
        guard let address = diagnostics["nasAddress"] as? String, !address.isEmpty else { return }
        var components = URLComponents()
        components.scheme = "http"
        components.host = address
        guard let url = components.url else { return }
        NSWorkspace.shared.open(url)
    }

    @objc private func openAdmin() {
        let fnID = pendingFNID.isEmpty ? savedFNID : pendingFNID
        guard !fnID.isEmpty else { return }
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let base: String
            let requiresLogin: Bool
            let failureMessage: String
            do {
                let response = try self?.ipc.call(method: "admin-proxy") ?? [:]
                base = response["url"] as? String ?? ""
                requiresLogin = false
                failureMessage = ""
            } catch {
                base = ""
                let failure = (error as NSError).userInfo["failure"] as? [String: Any]
                requiresLogin = failure?["code"] as? String == "AUTH_REQUIRED"
                failureMessage = error.localizedDescription
            }
            DispatchQueue.main.async { [weak self] in
                guard let self else { return }
                if !base.isEmpty, let proxyURL = URL(string: base) {
                    self.showAdminWebView(proxyURL)
                } else {
                    let alert = NSAlert()
                    alert.messageText = requiresLogin ?
                        L("管理后台登录已过期") : L("管理后台暂不可用")
                    alert.informativeText = requiresLogin ?
                        L("请重新登录 FnCPN。当前隧道连接不受影响。") : failureMessage
                    alert.alertStyle = .warning
                    if requiresLogin {
                        alert.addButton(withTitle: L("重新登录"))
                        alert.addButton(withTitle: L("取消"))
                        if alert.runModal() == .alertFirstButtonReturn {
                            self.relogin()
                        }
                    } else {
                        alert.runModal()
                    }
                }
            }
        }
    }

    private func showAdminWebView(_ proxyURL: URL) {
        let window: NSWindow
        if let existing = adminWindow {
            window = existing
        } else {
            window = NSWindow(
                contentRect: NSRect(x: 0, y: 0, width: 1000, height: 720),
                styleMask: [.titled, .closable, .miniaturizable, .resizable],
                backing: .buffered, defer: false
            )
            window.title = L("FnCPN 管理后台")
            window.contentMinSize = NSSize(width: 640, height: 480)
            window.isReleasedWhenClosed = false
            window.center()
            adminWindow = window
            // Restore the menu-bar/accessory policy once the admin window closes
            // so the regular Dock icon disappears again.
            NotificationCenter.default.addObserver(
                forName: NSWindow.willCloseNotification,
                object: window,
                queue: .main
            ) { [weak self] _ in
                self?.adminWindowOpen = false
                NSApp.setActivationPolicy(.accessory)
            }
        }
        let webView = WKWebView()
        webView.frame = window.contentView?.bounds ?? NSRect(x: 0, y: 0, width: 1000, height: 720)
        webView.autoresizingMask = [.width, .height]
        webView.load(URLRequest(url: proxyURL))
        window.contentView = webView
        // The management UI appears as a regular window (with a Dock icon); the
        // menu-bar/accessory policy is restored when it closes.
        adminWindowOpen = true
        NSApp.setActivationPolicy(.regular)
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    /// Detects when the root privileged daemon is unavailable (e.g. the user
    /// manually stopped it) and prompts the user. Restarting a system
    /// LaunchDaemon needs privileges, so we surface the situation and trigger a
    /// best-effort bootstrap; the configured KeepAlive will re-run a merely
    /// killed process automatically.
    private func notifyRootUnavailable() {
        privilegedStartPrompted = true
        let center = UNUserNotificationCenter.current()
        center.requestAuthorization(options: [.alert, .sound]) { granted, _ in
            guard granted else { return }
            let content = UNMutableNotificationContent()
            content.title = L("FnCPN 特权服务不可用")
            content.body = L("连接可能需要特权服务，请确认已安装或重新授权。")
            content.sound = .default
            let request = UNNotificationRequest(
                identifier: "fncpn.privileged.unavailable",
                content: content,
                trigger: nil
            )
            center.add(request)
        }
    }

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
        // Local Network access on macOS 15+ requires user permission. Trigger
        // the system prompt (and confirm it was not denied) BEFORE the daemon
        // runs its LOCAL LAN probe, otherwise that probe can be silently
        // blocked by the privacy system.
        guard ensureLocalNetworkAccess() else {
            setupError.stringValue = L("请在系统设置中允许 FnCPN 访问本地网络，然后重试。")
            window?.makeFirstResponder(fnIDField)
            return
        }
        guard let fnID = normalizeFNID(fnIDField.stringValue) else {
            setupError.stringValue = L("FN Connect ID 格式不正确")
            window?.makeFirstResponder(fnIDField)
            return
        }
        let username = usernameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !username.isEmpty else {
            setupError.stringValue = L("请输入 fnOS 用户名")
            window?.makeFirstResponder(usernameField)
            return
        }
        let password = passwordField.stringValue
        guard !password.isEmpty else {
            setupError.stringValue = L("请输入 fnOS 密码")
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
                            userInfo: [NSLocalizedDescriptionKey: L("授权请求无效")]
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
        else { perform(presentation.connected ? "disconnect" : "connect", showsWindow: true) }
    }

    /// Status-bar menu variant: performs connect/disconnect without reopening
    /// the window.
    @objc private func statusBarAction() {
        guard !busy else { return }
        if savedFNID.isEmpty && pendingFNID.isEmpty { showWindow(); return }
        if presentation.needsLogin || savedFNID.isEmpty { relogin() }
        else { perform(presentation.connected ? "disconnect" : "connect", showsWindow: false) }
    }

    @objc private func retryConnection() {
        if presentation.needsLogin { relogin() } else { perform("retry", showsWindow: true) }
    }

    private func perform(_ method: String, showsWindow: Bool = true) {
        guard !busy else { return }
        busy = true
        operationError = nil
        render()
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            do {
                _ = try self?.ipc.call(method: method)
                DispatchQueue.main.async { self?.operationFinished(.success(()), showsWindow: showsWindow) }
            } catch {
                DispatchQueue.main.async { self?.operationFinished(.failure(error), showsWindow: showsWindow) }
            }
        }
    }

    private func operationFinished(_ result: Result<Void, Error>, showsWindow: Bool = true) {
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
        if showsWindow { showWindow() }
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
        showInitialWindowIfNeeded()
    }

    /// Shows the window on the first diagnostics round trip. It stays hidden only
    /// when the app was launched as a login item at boot (the system is still at
    /// the login window, so no user app is frontmost yet) — in that case the
    /// status bar is the entry point. Any user-initiated launch (manual
    /// double-click from Finder/Launchpad, status bar "打开") opens the window.
    private func showInitialWindowIfNeeded() {
        guard !initialWindowShown, let window else { return }
        initialWindowShown = true
        if let frontmost = NSWorkspace.shared.frontmostApplication,
           frontmost.bundleIdentifier != "com.apple.loginwindow" {
            window.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
        }
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
        reloginMenuItem?.isHidden = presentation.needsLogin
        reloginMenuItem?.isEnabled = !busy
        serverValue.stringValue = fnID
        serverValue.toolTip = fnID
        statusValue.stringValue = busy && loginInFlight ? L("正在登录") : presentation.title
        statusValue.textColor = presentation.needsLogin ? .systemOrange :
            presentation.connected ? .systemGreen : .labelColor
        pathValue.stringValue = [
            "local": L("局域网"), "ipv6": "IPv6 / UDP", "fn-connect": "FN Connect / WSS"
        ][status["path"] as? String ?? ""] ?? "-"
        addressValue.stringValue = displayed["clientAddress"] as? String ?? "-"
        let interfaceName = displayed["networkInterface"] as? String ?? "-"
        let mtu = displayed["mtu"] as? Int ?? 0
        interfaceValue.stringValue = mtu > 0 ? "\(interfaceName) · MTU \(mtu)" : interfaceName
        handshakeValue.stringValue = displayed["handshakeAge"] as? String ?? "-"
        let privilegedAvailable = diagnostics["privilegedAvailable"] as? Bool == true
        rootValue.stringValue = !privilegedAvailable ? L("不可用") :
            diagnostics["privilegedDegraded"] as? Bool == true ? L("降级") :
            diagnostics["privilegedActive"] as? Bool == true ? L("活动") : L("就绪")
        let nasAddress = displayed["nasAddress"] as? String ?? ""
        nasValue.stringValue = nasAddress.isEmpty ? "-" : nasAddress
        nasValue.isHidden = setup || nasAddress.isEmpty
        browserButton.isHidden = setup || nasAddress.isEmpty
        browserButton.isEnabled = !busy && !nasAddress.isEmpty
        let administrator = displayed["administrator"] as? Bool == true
        adminButton.isHidden = setup || fnID.isEmpty || !administrator
        adminButton.isEnabled = !busy
        if !administrator {
            adminButton.toolTip = L("管理后台仅对 fnOS 管理员账号开放")
        }
        if !privilegedAvailable && !privilegedStartPrompted {
            notifyRootUnavailable()
        }
        let direct = displayed["direct"] as? [String: Any] ?? [:]
        directValue.stringValue = directSummary(direct)
        var messages = [describeFailure(status["lastError"] as? [String: Any])]
        let directError = describeFailure(direct["lastError"] as? [String: Any])
        if !directError.isEmpty { messages.append("IPv6: \(directError)") }
        if let endpoint = direct["endpoint"] as? String { messages.append("UDP \(endpoint)") }
        if let count = direct["candidateCount"] as? Int, count > 0 {
            messages.append(L("IPv6 候选 {0}，已尝试 {1}", String(count), String(direct["attemptCount"] as? Int ?? 0)))
        }
        if displayed["lanOverlap"] as? Bool == true { messages.append(L("本地与 NAS 网段重叠，仅保留隧道内 NAS 路由")) }
        errorValue.string = messages.filter { !$0.isEmpty }.joined(separator: "\n")
        if errorValue.string.isEmpty { errorValue.string = L("无异常") }
        setupError.stringValue = setup ? loginFailureMessage(status["lastError"] as? [String: Any]) : ""
        setupError.maximumNumberOfLines = 3
        setupError.toolTip = setupError.stringValue
        if busy { progress.startAnimation(nil) } else { progress.stopAnimation(nil) }
        let iconState = presentation.needsLogin ? "AUTH_REQUIRED" :
            (busy ? "PROBING" : presentation.state)
        statusItem?.button?.image = connectionStatusIcon(iconState)
        let statusDescription = L("FnCPN：") + (busy && loginInFlight ? L("正在登录") : presentation.title)
        statusItem?.button?.toolTip = statusDescription
        statusItem?.button?.setAccessibilityLabel(statusDescription)
        adjustWindowHeightForPage(setup)
    }

    /// Sizes the window to the current page (login vs details) and locks its
    /// height (min == max) so the window is implicitly sized — the user cannot
    /// resize it vertically, while the app still adapts it per page. Only the
    /// width remains user-resizable.
    private func adjustWindowHeightForPage(_ loginPage: Bool) {
        guard let window else { return }
        let targetHeight: CGFloat = loginPage ? 520 : 640
        // Resize to the target content height first, then lock min == max so
        // the user cannot drag the height.
        window.setContentSize(NSSize(width: window.contentLayoutRect.width, height: targetHeight))
        window.contentMinSize = NSSize(width: 680, height: targetHeight)
        window.contentMaxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: targetHeight)
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

    /// macOS 15+ (Sequoia) blocks access to addresses on the local network
    /// until the user grants the "Local Network" permission. The system prompt
    /// is only surfaced after a real local-network operation, so we fire a
    /// multicast probe from the app bundle (the process that owns the
    /// `NSLocalNetworkUsageDescription` key) to bring up the authorization
    /// dialog. It returns false when the user has explicitly denied access, so
    /// the connect flow stops instead of letting the daemon's LOCAL probe get
    /// blocked silently. On macOS older than 15 local network access is
    /// unrestricted, so it is always allowed.
    private func ensureLocalNetworkAccess() -> Bool {
        guard #available(macOS 15.0, *) else { return true }
        let descriptor = Darwin.socket(AF_INET, SOCK_DGRAM, 0)
        guard descriptor >= 0 else { return true }
        defer { Darwin.close(descriptor) }
        // mDNS multicast group: reaching it exercises the local-network path
        // and causes macOS to present the Local Network permission prompt.
        var destination = sockaddr_in()
        destination.sin_family = sa_family_t(AF_INET)
        destination.sin_port = in_port_t(5353).bigEndian
        destination.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        destination.sin_addr.s_addr = inet_addr("224.0.0.251")
        var loop: UInt8 = 1
        _ = withUnsafePointer(to: &loop) { pointer in
            Darwin.setsockopt(
                descriptor, IPPROTO_IP, IP_MULTICAST_LOOP,
                pointer, socklen_t(MemoryLayout<UInt8>.size)
            )
        }
        let payload: [UInt8] = [0]
        let result = payload.withUnsafeBytes { bytes -> Int in
            var target = destination
            return withUnsafePointer(to: &target) { pointer -> Int in
                pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { socketAddress in
                    Darwin.sendto(
                        descriptor, bytes.baseAddress, payload.count, 0,
                        socketAddress, socklen_t(MemoryLayout<sockaddr_in>.size)
                    )
                }
            }
        }
        if result < 0 {
            let code = Darwin.errno
            // EPERM/EACCES indicate the user denied Local Network access.
            if code == EPERM || code == EACCES { return false }
        }
        return true
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
        // On a pristine first launch there is no saved NAS yet, so a not-yet-ready
        // client daemon is normal initialization, not an error the user must see
        // on the login page. Only surface it once a NAS has been configured.
        if savedFNID.isEmpty && pendingFNID.isEmpty {
            return
        }
        diagnostics["status"] = [
            "state": "DAEMON_UNAVAILABLE",
            "lastError": ["code": "UNAVAILABLE", "message": error.localizedDescription]
        ]
        diagnostics["privilegedAvailable"] = false
        render()
    }
}
