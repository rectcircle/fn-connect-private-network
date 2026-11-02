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

private let ipcProtocolVersion = 5
private let maximumFrameSize = 256 * 1024

private func additionalCredentialMessage(_ failure: [String: Any]) -> String? {
    let factorMessages = [
        "ACCESS_CODE_REQUIRED": "该 NAS 开启了访问码，请输入访问码",
        "ACCESS_CODE_INVALID": "访问码不正确，请重试",
        "ACCESS_CODE_RATE_LIMITED": "访问码尝试次数过多，请稍后重试",
        "TWO_FACTOR_REQUIRED": "该账号需要两步验证，请输入 OTP",
        "TWO_FACTOR_CODE_INVALID": "OTP 不正确，请重试",
        "TWO_FACTOR_SETUP_REQUIRED": "该账号要求两步验证，请先在 fnOS 网页端完成绑定"
    ]
    guard let remoteCode = failure["remoteCode"] as? String,
          let message = factorMessages[remoteCode] else { return nil }
    return L(message)

}

private func failureSummary(_ failure: [String: Any]) -> String {
    if ["UNAVAILABLE", "TIMEOUT"].contains(failure["code"] as? String ?? ""),
       failure["operation"] as? String == "native_session.connect" {
        return L("服务不可用。请检查网络连接、NAS 上的 FN Connect 是否已启用，以及 FN Connect ID 是否正确。")
    }

    if failure["code"] as? String == "VERSION_INCOMPATIBLE" {
        let target = failure["upgradeTarget"] as? String
        let instruction = target == "client" ? L("版本不兼容，请升级客户端。") : L("版本不兼容，请升级服务端。")
        return instruction + " " + L("客户端 {0}，服务端 {1}", failure["clientVersion"] as? String ?? "?", failure["serverVersion"] as? String ?? "?")
    }

    if let message = additionalCredentialMessage(failure) { return message }
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
        "SERVER_UNAVAILABLE": "未找到 FnCPN 服务，请在 fnOS 安装或启动 FnCPN 后重新连接。",
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
    var result = failureSummary(failure) + "\n" + L("详细信息") + ": " + parts.joined(separator: ": ")
    if failure["code"] as? String == "VERSION_INCOMPATIBLE",
       let url = failure["releaseURL"] as? String, !url.isEmpty {
        result += "\n" + L("版本下载：{0}", url)
    }
    return result
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
    func call(method: String, params: Any? = nil, cancellation: IPCCancellation? = nil, receive: (([String: Any]) -> Bool)? = nil) throws -> [String: Any] {
        let descriptor = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw POSIXError(.EIO) }
        defer { cancellation?.detach(); Darwin.close(descriptor) }
        try cancellation?.attach(descriptor)
        try setTimeout(
            seconds: ["authorize-native", "watch-client-status", "speedtest"].contains(method) ? 310 :
                ["connect", "retry"].contains(method) ? 45 : 30,
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
        if cancellation?.isCanceled == true { throw POSIXError(.ECANCELED) }

        let requestID = UUID().uuidString
        var request: [String: Any] = [
            "version": ipcProtocolVersion,
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

        while true {
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
        if let appVersion = Bundle.main.object(forInfoDictionaryKey: "FnCPNProductVersion") as? String,
           response["productVersion"] as? String != appVersion {
            throw NSError(domain: "FnCPN", code: 4, userInfo: [NSLocalizedDescriptionKey: L("本地组件版本不一致，请完成更新并重新启动 FnCPN。")])
        }
        guard response["version"] as? Int == ipcProtocolVersion,
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
        let value = result as? [String: Any] ?? [:]
        if let receive { if receive(value) { return value } } else { return value }
        }
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
    case "ERROR", "DAEMON_UNAVAILABLE", "AUTH_REQUIRED", "SERVER_UNAVAILABLE": variant = "attention"
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
            "SERVER_UNAVAILABLE": L("未找到 FnCPN 服务"), "ERROR": L("连接失败"), "DAEMON_UNAVAILABLE": L("客户端服务不可用")
        ][state] ?? state
    }
    var actionTitle: String { needsLogin ? L("重新登录") : connected ? L("断开连接") : L("连接") }
    var actionSymbol: String { needsLogin ? "person.badge.key" : connected ? "stop.fill" : "bolt.fill" }

    init(_ status: [String: Any]) {
        state = status["state"] as? String ?? "UNCONFIGURED"
        let failure = status["lastError"] as? [String: Any] ?? [:]
        let code = failure["code"] as? String ?? ""
        let operation = failure["operation"] as? String ?? ""
        needsLogin = state == "AUTH_REQUIRED" || additionalCredentialMessage(failure) != nil ||
            ["AUTH_REQUIRED", "DEVICE_REVOKED"].contains(code) ||
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

    if let message = additionalCredentialMessage(failure) { return message }

    if ["UNAVAILABLE", "TIMEOUT"].contains(failure["code"] as? String ?? ""),
       failure["operation"] as? String == "native_session.connect" {
        return L("服务不可用。请检查网络连接、NAS 上的 FN Connect 是否已启用，以及 FN Connect ID 是否正确。")
    }

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
        return describeFailure(failure)
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
private final class AppDelegate: NSObject, NSApplicationDelegate, NSTextFieldDelegate {
    private let ipc = IPCClient()
 private var linkSettingsWindow: LinkSettingsController?
    private let launchAgent = LaunchAgentManager()
    private let statusValue = NSTextField(labelWithString: L("尚未连接"))
    private let serverValue = NSTextField(labelWithString: "")
    private let pathValue = NSTextField(labelWithString: "-")
    private let addressValue = NSTextField(labelWithString: "-")
    private let dnsValue = NSTextField(labelWithString: "-")
    private var dnsSuffix = ""
    private var copyDNSButton: NSButton!
    private let nasValue = NSTextField(labelWithString: "-")
    private let interfaceValue = NSTextField(labelWithString: "-")
    private let handshakeValue = NSTextField(labelWithString: "-")
    private let rootValue = NSTextField(labelWithString: "-")
    private let directValue = NSTextField(wrappingLabelWithString: L("尚未检测"))
    private let errorValue = NSTextView()
    private let setupError = NSTextField(wrappingLabelWithString: "")
    private let fnIDField = NSTextField()
    private let accessCodeField = NSSecureTextField()
    private let usernameField = NSTextField()
    private let passwordField = NSSecureTextField()
    private let twofaField = NSSecureTextField()
    private let trustDeviceButton = NSButton(checkboxWithTitle: "", target: nil, action: nil)
    private let setupView = NSStackView()
    private let detailsView = NSStackView()
    private let progress = NSProgressIndicator()
    private var primaryButton = NSButton()
    private var setupButton = NSButton()
    private var loginButton = NSButton()
    private var retryButton = NSButton()
    private var changeButton = NSButton()
    private var returnButton = NSButton()
    private var loginBackButton = NSButton()
    private var adminButton = NSButton()
    private var browserButton = NSButton()
    private var quitButton = NSButton()
    private var loginFooterButtons: [NSButton] = []
    private var menuAction: NSMenuItem?
    private var autoStartItem: NSMenuItem?
    private var statusItem: NSStatusItem?
    private var reloginMenuItem: NSMenuItem?
    private var statusWatch: DispatchWorkItem?
    private var refreshRetry: DispatchWorkItem?
    private var refreshRetryDelay: TimeInterval = 1
    private var window: NSWindow!
    private var adminWindow: NSWindow?
    private var refreshInFlight = false
    private var refreshPending = false
    private var waitingForLocalNetwork = false
    private var busy = false
    private var loginInFlight = false
    private var editingServer = false
    private var savedFNID = ""
    private var savedUsername = ""
    private var pendingFNID = ""
    private var externalAuthorizationRequest = ""
    // The daemon keeps an authorization request open when the gateway or the
    // account asks for one more factor, so a retry reuses this request ID.
    private var loginMessage: String?
    private var accessCodeRequired = false
    private var accessCodeProbeInFlight = false
    private var accessCodeProbedFNID = ""
    private var twofaChallenge = false
    private var loginForm: NSGridView!
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
        // Start in the menu bar; user windows promote the app to a regular Dock app.
        application.setActivationPolicy(.accessory)
        // There is no nib/storyboard to create or retain the delegate.
        withExtendedLifetime(delegate) {
            _ = NSApplicationMain(CommandLine.argc, CommandLine.unsafeArgv)
        }
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        let menu = NSMenu()
        let appMenu = NSMenu(title: "FnCPN")
        appMenu.addItem(withTitle: L("官网 · 捐赠"), action: #selector(openHomepage), keyEquivalent: "").target = self
        appMenu.addItem(withTitle: L("设置…"), action: #selector(openSettings), keyEquivalent: ",").target = self
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: L("退出"), action: #selector(quit), keyEquivalent: "q").target = self
        let appItem = NSMenuItem(title: "FnCPN", action: nil, keyEquivalent: "")
        appItem.submenu = appMenu
        menu.addItem(appItem)
        let edit = NSMenu(title: L("编辑"))
        edit.addItem(withTitle: L("剪切"), action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.addItem(withTitle: L("粘贴"), action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.addItem(withTitle: L("复制"), action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.addItem(withTitle: L("全选"), action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        let item = NSMenuItem(title: L("编辑"), action: nil, keyEquivalent: "")
        item.submenu = edit
        menu.addItem(item)
        NSApp.mainMenu = menu
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
        WindowPresence.shared.register(window)
        window.center()
        // Do not show the window yet: we show it once the first diagnostics
        // round trip returns so an already-logged-in user goes straight to the
        // details page (no flash of the login page first). A fallback timer
        // guarantees the window appears even if the daemon never responds.
        window.makeFirstResponder(fnIDField)
        DispatchQueue.main.asyncAfter(deadline: .now() + 2.0) { [weak self] in
            self?.showInitialWindowIfNeeded()
        }
        configureStatusItem()
        setupLaunchAgent()
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
        if let version = Bundle.main.object(forInfoDictionaryKey: "FnCPNProductVersion") as? String,
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
        configureLoginField(accessCodeField, placeholder: L("fnOS 访问码"), label: L("访问码"))
        configureLoginField(usernameField, placeholder: L("fnOS 用户名"), label: L("用户名"))
        configureLoginField(passwordField, placeholder: L("fnOS 密码"), label: L("密码"))
        configureLoginField(twofaField, placeholder: L("6 位 OTP"), label: L("OTP"))
        twofaField.widthAnchor.constraint(equalToConstant: 120).isActive = true
        twofaField.setContentHuggingPriority(.defaultHigh, for: .horizontal)
        twofaField.setContentCompressionResistancePriority(.defaultHigh, for: .horizontal)
        trustDeviceButton.title = L("信任此设备")
        passwordField.target = self
        passwordField.action = #selector(submitLogin)
        twofaField.target = self
        twofaField.action = #selector(submitLogin)
        // Typing a FN Connect ID ends with either Enter or focus leaving the
        // field; both schedule the credential-free access-code probe.
        fnIDField.target = self
        fnIDField.action = #selector(probeAccessCode)
        fnIDField.delegate = self
        loginForm = NSGridView(views: [
            loginRow("FN Connect ID", fnIDField),
            loginRow(L("访问码"), accessCodeField),
            loginRow(L("用户名"), usernameField),
            loginRow(L("密码"), passwordField),
            loginRow(L("OTP"), horizontal([twofaField, trustDeviceButton]))
        ])
        loginForm.column(at: 0).width = 104
        loginForm.column(at: 1).width = 320
        loginForm.columnSpacing = 14
        loginForm.rowSpacing = 14
        loginForm.xPlacement = .fill
        loginForm.row(at: 1).isHidden = true
        loginForm.row(at: 4).isHidden = true
        loginForm.row(at: 4).yPlacement = .center
        loginForm.cell(atColumnIndex: 1, rowIndex: 4).xPlacement = .leading
        fill(loginForm, in: setupView)
        setupButton = button(L("登录并连接"), symbol: "arrow.right", action: #selector(submitLogin))
        setupButton.widthAnchor.constraint(greaterThanOrEqualToConstant: 132).isActive = true
        setupView.addArrangedSubview(setupButton)
        loginBackButton = button(L("上一步"), symbol: "arrow.left", action: #selector(backToCredentials))
        setupView.addArrangedSubview(loginBackButton)
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

        copyDNSButton = iconButton("doc.on.doc", action: #selector(copyDNSSuffix), help: L("复制 DNS 后缀"))
        copyDNSButton.isEnabled = false
        let dnsContainer = NSStackView(views: [dnsValue, copyDNSButton])
        dnsContainer.orientation = .horizontal
        dnsContainer.spacing = 8
        dnsContainer.alignment = .centerY
        let grid = NSGridView(views: [
            row(L("网络链路"), pathValue), row(L("隧道地址"), addressValue),
            row(L("网络接口"), interfaceValue), row(L("最近握手"), handshakeValue),
            [nasLabel("DNS"), dnsContainer], row(L("特权服务"), rootValue), row(L("IPv6 检测"), directValue),
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
        detailsView.addArrangedSubview(horizontal([primaryButton, loginButton, retryButton, button(L("设置"), symbol: "gearshape", action: #selector(openSettings))]))
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
        loginFooterButtons = [
            button("", symbol: "doc.on.doc", action: #selector(copyDiagnostics), help: L("复制诊断信息")),
            button("", symbol: "folder", action: #selector(openLogs), help: L("打开日志")),
            button("", symbol: "arrow.clockwise", action: #selector(refresh), help: L("刷新状态"))
        ]
        let tools = horizontal([changeButton, adminButton, progress] + loginFooterButtons)
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

    private func loginRow(_ name: String, _ field: NSView) -> [NSView] {
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
        if !flag { showWindow() }
        WindowPresence.shared.update()
        return true
    }

    func applicationDidBecomeActive(_ notification: Notification) {
        WindowPresence.shared.update()
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
        menu.addItem(withTitle: L("官网 · 捐赠"), action: #selector(openHomepage), keyEquivalent: "")
        // Avoid AppKit's automatic Settings action image in the otherwise text-only status menu.
        let settingsItem = menu.addItem(withTitle: L("设置…"), action: #selector(showPreferencesFromStatusMenu), keyEquivalent: "")
        settingsItem.image = nil
        if #available(macOS 27.0, *) { settingsItem.preferredImageVisibility = .hidden }
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

    @objc private func showPreferencesFromStatusMenu() { openSettings() }

    @objc private func openSettings() {
        if linkSettingsWindow == nil {
            linkSettingsWindow = LinkSettingsController()
            linkSettingsWindow?.toggleLoginItem = { [weak self] in self?.toggleAutoStart() }
        }
        linkSettingsWindow?.open()
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
        if let window { WindowPresence.shared.show(window) }
    }

    @objc private func openHomepage() {
        if let url = URL(string: "https://fncpn.rectcircle.cn/#donate") { NSWorkspace.shared.open(url) }
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
                requiresLogin = failure?["code"] as? String == "AUTH_REQUIRED" ||
                    additionalCredentialMessage(failure ?? [:]) != nil
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
            WindowPresence.shared.register(window)
        }
        let webView = WKWebView()
        webView.frame = window.contentView?.bounds ?? NSRect(x: 0, y: 0, width: 1000, height: 720)
        webView.autoresizingMask = [.width, .height]
        webView.load(URLRequest(url: proxyURL))
        window.contentView = webView
        WindowPresence.shared.show(window)
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
        accessCodeField.stringValue = ""
        usernameField.stringValue = ""
        passwordField.stringValue = ""
        twofaField.stringValue = ""
        accessCodeRequired = false
        accessCodeProbedFNID = ""
        twofaChallenge = false
        loginMessage = ""
        render()
        window.makeFirstResponder(fnIDField)
    }

    @objc private func returnToDetails() {
        cancelExternalAuthorization()
        editingServer = false
        loginMessage = nil
        passwordField.stringValue = ""
        accessCodeField.stringValue = ""
        twofaField.stringValue = ""
        accessCodeRequired = false
        accessCodeProbedFNID = ""
        twofaChallenge = false
        pendingFNID = ""
        render()
    }

    @objc private func backToCredentials() {
        guard !busy else { return }
        twofaChallenge = false
        twofaField.stringValue = ""
        trustDeviceButton.state = .off
        loginMessage = ""
        render()
        window?.makeFirstResponder(passwordField)
    }

    @objc private func submitLogin() {
        if waitingForLocalNetwork { continueWithoutLocalAccess(); return }
        guard !busy else { return }
        guard let fnID = normalizeFNID(fnIDField.stringValue) else {
            loginMessage = L("FN Connect ID 格式不正确")
            render()
            window?.makeFirstResponder(fnIDField)
            return
        }
        let username = usernameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !username.isEmpty else {
            loginMessage = L("请输入 fnOS 用户名")
            render()
            window?.makeFirstResponder(usernameField)
            return
        }
        let password = passwordField.stringValue
        guard !password.isEmpty else {
            loginMessage = L("请输入 fnOS 密码")
            render()
            window?.makeFirstResponder(passwordField)
            return
        }
        let accessCode = accessCodeField.stringValue
        if accessCodeRequired && accessCode.isEmpty {
            loginMessage = L("请输入 fnOS 访问码")
            render()
            window?.makeFirstResponder(accessCodeField)
            return
        }
        let twofaCode = twofaField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if twofaChallenge && twofaCode.isEmpty {
            loginMessage = L("请输入 OTP")
            render()
            window?.makeFirstResponder(twofaField)
            return
        }
        let trustDevice = trustDeviceButton.state == .on
        fnIDField.stringValue = fnID
        pendingFNID = fnID
        loginInFlight = true
        busy = true
        operationError = nil
        loginMessage = ""
        let existingRequest = externalAuthorizationRequest
        externalAuthorizationRequest = ""
        render()
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            var requestID = existingRequest
            do {
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
                var params: [String: Any] = [
                    "requestId": requestID,
                    "fnId": fnID,
                    "username": username,
                    "password": password,
                ]
                if !accessCode.isEmpty { params["accessCode"] = accessCode }
                if !twofaCode.isEmpty { params["twofaCode"] = twofaCode }
                if trustDevice { params["trustDevice"] = true }
                _ = try self?.ipc.call(method: "authorize-native", params: params)
                DispatchQueue.main.async {
                    self?.twofaChallenge = false
                    self?.operationFinished(.success(()))
                }
            } catch {
                DispatchQueue.main.async {
                    if self?.showLoginChallenge(error, requestID: requestID) == true { return }
                    self?.operationFinished(.failure(error))
                }
            }
        }
    }

    /// Keeps the sign-in form open when fnOS asked for one more factor. The
    /// daemon leaves that authorization request pending, so the retry reuses the
    /// same request ID instead of starting a new sign-in.
    private func showLoginChallenge(_ error: Error, requestID: String) -> Bool {
        let failure = (error as NSError).userInfo["failure"] as? [String: Any] ?? [:]
        guard let remoteCode = failure["remoteCode"] as? String else { return false }
        switch remoteCode {
        case "ACCESS_CODE_REQUIRED", "ACCESS_CODE_INVALID", "ACCESS_CODE_RATE_LIMITED":
            accessCodeRequired = true
            accessCodeProbedFNID = fnIDField.stringValue
        case "TWO_FACTOR_REQUIRED", "TWO_FACTOR_CODE_INVALID":
            accessCodeRequired = false
            accessCodeField.stringValue = ""
            twofaChallenge = true
            if remoteCode == "TWO_FACTOR_CODE_INVALID" { twofaField.stringValue = "" }
        case "TWO_FACTOR_SETUP_REQUIRED":
            accessCodeRequired = false
            accessCodeField.stringValue = ""
            twofaChallenge = false
        default:
            return false
        }
        externalAuthorizationRequest = requestID
        loginInFlight = false
        busy = false
        editingServer = true
        loginMessage = loginFailureMessage(failure)
        render()
        if twofaChallenge { window?.makeFirstResponder(twofaField) }
        else if accessCodeRequired { window?.makeFirstResponder(accessCodeField) }
        return true
    }

    /// Asks the daemon whether this FN Connect ID sits behind an fnOS access
    /// code, so the form can show the field before the user tries to sign in.
    @objc private func probeAccessCode() { scheduleAccessCodeProbe() }

    @objc func controlTextDidEndEditing(_ notification: Notification) {
        guard (notification.object as? NSTextField) === fnIDField else { return }
        scheduleAccessCodeProbe()
    }

    private func scheduleAccessCodeProbe() {
        guard !twofaChallenge && !loginInFlight else { return }
        guard let fnID = normalizeFNID(fnIDField.stringValue) else {
            if accessCodeProbedFNID.isEmpty { return }
            cancelExternalAuthorization()
            accessCodeProbedFNID = ""
            accessCodeField.stringValue = ""
            twofaField.stringValue = ""
            accessCodeRequired = false
            twofaChallenge = false
            loginMessage = ""
            render()
            return
        }
        if fnID == accessCodeProbedFNID { return }
        if !accessCodeProbedFNID.isEmpty {
            cancelExternalAuthorization()
            accessCodeProbedFNID = ""
            accessCodeField.stringValue = ""
            twofaField.stringValue = ""
            accessCodeRequired = false
            twofaChallenge = false
            loginMessage = ""
            render()
        }
        guard !accessCodeProbeInFlight else { return }
        accessCodeProbeInFlight = true
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            var required = false
            var failed = false
            do {
                let result = try self?.ipc.call(
                    method: "detect-access-code",
                    params: ["fnId": fnID]
                ) ?? [:]
                required = result["required"] as? Bool ?? false
            } catch {
                failed = true
            }
            DispatchQueue.main.async {
                guard let self else { return }
                self.accessCodeProbeInFlight = false
                guard !self.twofaChallenge && !self.loginInFlight else { return }
                guard self.editingServer || self.savedFNID.isEmpty else { return }
                // A probe for a FN ID the user has already edited away must not
                // drive the form.
                guard normalizeFNID(self.fnIDField.stringValue) == fnID else {
                    if self.editingServer { self.scheduleAccessCodeProbe() }
                    return
                }
                // A sign-in challenge may already have confirmed the requirement
                // while this credential-free probe was still in flight.
                guard self.accessCodeProbedFNID != fnID else { return }
                if !failed {
                    // The entry point may already have focused the empty password
                    // while the probe was running. Move to the newly revealed first
                    // credential only if the user has not begun entering a password.
                    let idEditor = self.fnIDField.currentEditor()
                    let passwordEditor = self.passwordField.currentEditor()
                    let focusedID = idEditor != nil && self.window?.firstResponder === idEditor
                    let focusedEmptyPassword = passwordEditor != nil &&
                        self.window?.firstResponder === passwordEditor &&
                        passwordEditor?.string.isEmpty == true && self.passwordField.stringValue.isEmpty
                    self.accessCodeProbedFNID = fnID
                    self.accessCodeRequired = required
                    self.render()
                    if required && self.accessCodeField.stringValue.isEmpty && (focusedID || focusedEmptyPassword) {
                        self.window?.makeFirstResponder(self.accessCodeField)
                    }
                }
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
        accessCodeField.stringValue = ""
        twofaField.stringValue = ""
        accessCodeRequired = false
        accessCodeProbedFNID = ""
        twofaChallenge = false
        let status = diagnostics["status"] as? [String: Any] ?? [:]
        let failure = (operationError ?? status["lastError"] as? [String: Any]) ?? [:]
        if fnID == savedFNID,
           (failure["remoteCode"] as? String ?? "").hasPrefix("ACCESS_CODE_") {
            accessCodeRequired = true
            accessCodeProbedFNID = fnID
        }
        operationError = nil
        loginMessage = ""
        render()
        scheduleAccessCodeProbe()
        window.makeFirstResponder(fnID.isEmpty ? fnIDField : accessCodeRequired ? accessCodeField : username.isEmpty ? usernameField : passwordField)
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
        if waitingForLocalNetwork { continueWithoutLocalAccess(); return }
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

    private func continueWithoutLocalAccess() {
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            _ = try? self?.ipc.call(method: "continue-remote")
        }
    }

    private func perform(_ method: String, showsWindow: Bool = true) {
        guard !busy else { return }
        if waitingForLocalNetwork && method != "disconnect" { return }
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
                loginMessage = loginFailureMessage(operationError)
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
                passwordField.stringValue = ""
                accessCodeField.stringValue = ""
                twofaField.stringValue = ""
                twofaChallenge = false
            }
        }
        render()
        refresh()
        if showsWindow { showWindow() }
    }

    private func applyDiagnostics(_ value: [String: Any]) {
        diagnostics = value
        waitingForLocalNetwork = (value["status"] as? [String: Any])?["localNetworkWaiting"] as? Bool == true
        savedFNID = value["fnId"] as? String ?? ""
        savedUsername = value["username"] as? String ?? ""
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
            WindowPresence.shared.show(window)
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
        returnButton.isHidden = savedFNID.isEmpty || twofaChallenge
        loginBackButton.isHidden = !twofaChallenge
        loginBackButton.isEnabled = !busy
        changeButton.isHidden = setup
        changeButton.isEnabled = !busy
        setupButton.isEnabled = !busy || waitingForLocalNetwork
        setupButton.title = waitingForLocalNetwork ? L("继续远程连接") : L("登录并连接")
        fnIDField.isEnabled = !busy
        accessCodeField.isEnabled = !busy
        usernameField.isEnabled = !busy
        passwordField.isEnabled = !busy
        twofaField.isEnabled = !busy
        trustDeviceButton.isEnabled = !busy
        for row in [0, 2, 3] { loginForm.row(at: row).isHidden = twofaChallenge }
        loginForm.row(at: 1).isHidden = !accessCodeRequired || twofaChallenge
        loginForm.row(at: 4).isHidden = !twofaChallenge
        primaryButton.isEnabled = !busy || waitingForLocalNetwork
        primaryButton.title = waitingForLocalNetwork ? L("继续远程连接") : presentation.actionTitle
        primaryButton.toolTip = primaryButton.title
        primaryButton.image = NSImage(systemSymbolName: presentation.actionSymbol, accessibilityDescription: primaryButton.title)
        primaryButton.setAccessibilityLabel(primaryButton.title)
        loginButton.isHidden = presentation.needsLogin
        loginButton.isEnabled = !busy
        retryButton.isEnabled = !busy && !waitingForLocalNetwork
        retryButton.isHidden = presentation.needsLogin || savedFNID.isEmpty
        menuAction?.title = presentation.actionTitle
        menuAction?.isEnabled = !busy
        reloginMenuItem?.isHidden = presentation.needsLogin
        reloginMenuItem?.isEnabled = !busy
        let username = savedUsername.trimmingCharacters(in: .whitespacesAndNewlines)
        let identity = username.isEmpty ? fnID : "\(fnID) · \(username)"
        serverValue.stringValue = identity
        serverValue.toolTip = identity
        statusValue.stringValue = waitingForLocalNetwork ? L("正在等待局域网访问权限") :
            busy && loginInFlight ? L("正在登录") : presentation.title
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
        let dns = status["dns"] as? [String: Any] ?? [:]
        let dnsState = dns["state"] as? String ?? "inactive"
        dnsSuffix = dns["suffix"] as? String ?? ""
        copyDNSButton.isEnabled = !dnsSuffix.isEmpty
        let dnsLabel = ["ready": L("就绪"), "preparing": L("正在读取"), "degraded": L("降级"), "inactive": L("未连接")][dnsState] ?? L("未连接")
        dnsValue.stringValue = "\(dnsLabel) · \(dnsSuffix)"
        dnsValue.isSelectable = true
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
        var messages = [describeFailure(status["lastError"] as? [String: Any]), describeFailure(dns["lastError"] as? [String: Any])]
        let directError = describeFailure(direct["lastError"] as? [String: Any])
        if !directError.isEmpty { messages.append("IPv6: \(directError)") }
        if let endpoint = direct["endpoint"] as? String { messages.append("UDP \(endpoint)") }
        if let count = direct["candidateCount"] as? Int, count > 0 {
            messages.append(L("IPv6 候选 {0}，已尝试 {1}", String(count), String(direct["attemptCount"] as? Int ?? 0)))
        }
        if displayed["lanOverlap"] as? Bool == true { messages.append(L("本地与 NAS 网段重叠，仅保留隧道内 NAS 路由")) }
        errorValue.string = messages.filter { !$0.isEmpty }.joined(separator: "\n")
        if errorValue.string.isEmpty { errorValue.string = L("无异常") }
        setupError.stringValue = setup ? (waitingForLocalNetwork ? L("正在等待局域网访问权限") : loginMessage ?? loginFailureMessage(status["lastError"] as? [String: Any])) : ""
        setupError.maximumNumberOfLines = 3
        setupError.toolTip = setupError.stringValue
        if busy || waitingForLocalNetwork { progress.startAnimation(nil) } else { progress.stopAnimation(nil) }
        let iconState = presentation.needsLogin ? "AUTH_REQUIRED" :
            (busy ? "PROBING" : presentation.state)
        statusItem?.button?.image = connectionStatusIcon(iconState)
        let statusDescription = L("FnCPN：") + statusValue.stringValue
        statusItem?.button?.toolTip = statusDescription
        statusItem?.button?.setAccessibilityLabel(statusDescription)
        adjustWindowHeightForPage(setup)
        updateLoginKeyViewLoop(setup: setup)
    }

    private func updateLoginKeyViewLoop(setup: Bool) {
        guard let window else { return }
        // AppKit's automatic geometry ordering can change when grid rows are
        // hidden or inserted. Own the complete login loop, including its tail.
        window.autorecalculatesKeyViewLoop = !setup
        let fields: [NSControl] = [fnIDField, accessCodeField, usernameField, passwordField,
                                   twofaField, trustDeviceButton, setupButton, loginBackButton, returnButton]
        for control in fields + loginFooterButtons + [quitButton] { control.nextKeyView = nil }
        guard setup else { window.recalculateKeyViewLoop(); return }
        var order: [NSControl]
        if twofaChallenge {
            order = [twofaField, trustDeviceButton, setupButton, loginBackButton]
        } else {
            order = [fnIDField]
            if accessCodeRequired { order.append(accessCodeField) }
            order += [usernameField, passwordField, setupButton]
            if !returnButton.isHidden { order.append(returnButton) }
        }
        order += loginFooterButtons + [quitButton]
        order = order.filter { $0.isEnabled && !$0.isHiddenOrHasHiddenAncestor }
        for (index, control) in order.enumerated() {
            control.nextKeyView = order[(index + 1) % order.count]
        }
    }

    /// Sizes the window to the current page (login vs details) and locks its
    /// height (min == max) so the window is implicitly sized — the user cannot
    /// resize it vertically, while the app still adapts it per page. Only the
    /// width remains user-resizable.
    private func adjustWindowHeightForPage(_ loginPage: Bool) {
        guard let window else { return }
        let targetHeight: CGFloat = loginPage ? (twofaChallenge ? 390 : 520 + (accessCodeRequired ? 46 : 0)) : 640
        // Resize to the target content height first, then lock min == max so
        // the user cannot drag the height.
        window.setContentSize(NSSize(width: window.contentLayoutRect.width, height: targetHeight))
        window.contentMinSize = NSSize(width: 680, height: targetHeight)
        window.contentMaxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: targetHeight)
    }

    @objc private func copyDNSSuffix() {
        guard !dnsSuffix.isEmpty else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(dnsSuffix, forType: .string)
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

// Fixed sidebar and independently constrained document pages. Only the selected
// page participates in layout; test events never resize the window horizontally.
private final class LinkSettingsController: NSWindowController, NSWindowDelegate, NSTableViewDataSource, NSTableViewDelegate {
    private let rpc = IPCClient()
    private let work = DispatchQueue(label: "cn.rectcircle.fncpn.settings")
    private let testing = DispatchQueue(label: "cn.rectcircle.fncpn.measurement")
    private let sidebar = SettingsTableView()
    private let table = SettingsTableView()
    private let scroll = NSScrollView()
    private let pageDocument = SettingsDocumentView()
    private let general = NSStackView()
    private let page = NSStackView()
    private let nas = NSTextField(labelWithString: "")
    private let current = NSTextField(labelWithString: "")
    private let notice = NSTextField(wrappingLabelWithString: "")
    private let errorDetails = NSTextField(wrappingLabelWithString: "")
    private let detailsButton = SettingsButton(title: L("详细信息"), target: nil, action: nil)
    private let results = NSGridView()
    private let resultArea = NSStackView()
    private let liveResults = NSTextField(wrappingLabelWithString: "")
    private let phase = NSTextField(labelWithString: "")
    private let canvas = SpeedTestCanvas()
    private let selector = SettingsButton(title: L("选择测试链路"), target: nil, action: nil)
    private let startButton = SettingsButton(title: L("开始测试"), target: nil, action: nil)
    private let cancelButton = SettingsButton(title: L("取消"), target: nil, action: nil)
    private let autoStart = SettingsSwitch()
    private var values: [[NSTextField]] = []
    private var links: [[String: Any]] = []
    private var selected = Set(["ipv6", "fn-connect"])
    private var fnID = ""
    private var currentPath = ""
    private var polling: Timer?
    private var inFlight = false
    private var saving = false
    private var active = false
    private var cancellation: IPCCancellation?
    private var pageBottom: NSLayoutConstraint?
    private var errorMessage = ""
    private var selectorPopover: NSPopover?
    private var confirming = false
    private var lastState: [String: Any] = [:]
    var toggleLoginItem: (() -> Void)?

    init() {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 840, height: 720), styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        window.title = L("设置")
        window.contentMinSize = NSSize(width: 780, height: 620)
        window.isReleasedWhenClosed = false
        super.init(window: window)
        window.delegate = self
        WindowPresence.shared.register(window)
        window.contentView = SettingsBackgroundView(frame: window.contentView!.frame)
        let root = window.contentView!
        let rail = NSVisualEffectView()
        rail.material = .sidebar; rail.blendingMode = .behindWindow
        rail.translatesAutoresizingMaskIntoConstraints = false
        root.addSubview(rail)
        let column = NSTableColumn(identifier: .init("section")); column.resizingMask = .autoresizingMask; sidebar.addTableColumn(column)
        sidebar.allRowsClickable = true
        sidebar.backgroundColor = .clear
        sidebar.headerView = nil; sidebar.rowHeight = 36; sidebar.style = .plain
        sidebar.delegate = self; sidebar.dataSource = self
        sidebar.setAccessibilityLabel(L("设置分类"))
        let navigation = NSScrollView(); navigation.documentView = sidebar; navigation.drawsBackground = false
        navigation.translatesAutoresizingMaskIntoConstraints = false
        rail.addSubview(navigation)
        scroll.translatesAutoresizingMaskIntoConstraints = false
        scroll.drawsBackground = false; scroll.hasVerticalScroller = true; scroll.autohidesScrollers = true
        pageDocument.translatesAutoresizingMaskIntoConstraints = false; scroll.documentView = pageDocument
        root.addSubview(scroll)
        NSLayoutConstraint.activate([
            rail.leadingAnchor.constraint(equalTo: root.leadingAnchor), rail.topAnchor.constraint(equalTo: root.topAnchor), rail.bottomAnchor.constraint(equalTo: root.bottomAnchor), rail.widthAnchor.constraint(equalToConstant: 150),
            navigation.leadingAnchor.constraint(equalTo: rail.leadingAnchor, constant: 10), navigation.trailingAnchor.constraint(equalTo: rail.trailingAnchor, constant: -10), navigation.topAnchor.constraint(equalTo: rail.topAnchor, constant: 22), navigation.heightAnchor.constraint(equalToConstant: 84),
            scroll.leadingAnchor.constraint(equalTo: rail.trailingAnchor), scroll.trailingAnchor.constraint(equalTo: root.trailingAnchor), scroll.topAnchor.constraint(equalTo: root.topAnchor), scroll.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            pageDocument.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor)
        ])
        for stack in [general, page] {
            stack.orientation = .vertical; stack.alignment = .leading; stack.spacing = 20
            stack.translatesAutoresizingMaskIntoConstraints = false
        }
        buildGeneral(); buildLinks()
        sidebar.selectRowIndexes(IndexSet(integer: 0), byExtendingSelection: false)
        displayPage(0)
        window.center()
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) is unavailable") }
    private func label(_ text: String, size: CGFloat = 13, secondary: Bool = false) -> NSTextField {
        let view = NSTextField(wrappingLabelWithString: text)
        view.font = .systemFont(ofSize: size)
        view.textColor = secondary ? .secondaryLabelColor : .labelColor
        view.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        return view
    }
    private func fill(_ view: NSView, in stack: NSStackView) {
        stack.addArrangedSubview(view); view.translatesAutoresizingMaskIntoConstraints = false
        view.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }
    private func heading(_ text: String, in stack: NSStackView) {
        let view = label(text, size: 24); view.font = .systemFont(ofSize: 24, weight: .semibold); fill(view, in: stack)
    }
    private func group(_ title: String, help: String? = nil, in stack: NSStackView) -> NSStackView {
        let row = NSStackView(); row.orientation = .horizontal
        let name = label(title, size: 14); name.font = .systemFont(ofSize: 14, weight: .semibold)
        row.addArrangedSubview(name)
        if let help {
            let button = HoverHelpButton(title: "", target: nil, action: nil)
            button.bezelStyle = .helpButton; button.helpText = help
            button.setAccessibilityLabel(title + L("说明")); row.addArrangedSubview(button)
        }
        fill(row, in: stack)
        let card = SettingsCard(); fill(card, in: stack); return card.body
    }
    private func buildGeneral() {
        heading(L("通用"), in: general)
        let startup = group(L("启动"), in: general)
        let row = NSStackView(); row.orientation = .horizontal; row.distribution = .fill
        let title = label(L("开机自启")); row.addArrangedSubview(title)
        let spacer = NSView(); spacer.setContentHuggingPriority(.defaultLow, for: .horizontal); row.addArrangedSubview(spacer)
        autoStart.target = self; autoStart.action = #selector(toggleAutoStartSetting); row.addArrangedSubview(autoStart)
        fill(row, in: startup)
        let about = group(L("关于 FnCPN"), in: general)
        let brand = NSStackView(); brand.orientation = .horizontal; brand.spacing = 14
        let icon = NSImageView(image: Bundle.main.image(forResource: "AppIcon") ?? NSImage(systemSymbolName: "network", accessibilityDescription: nil)!)
        icon.widthAnchor.constraint(equalToConstant: 48).isActive = true; icon.heightAnchor.constraint(equalToConstant: 48).isActive = true
        brand.addArrangedSubview(icon)
        let brandName = label("FnCPN", size: 20); brandName.font = .systemFont(ofSize: 20, weight: .semibold); brand.addArrangedSubview(brandName)
        fill(brand, in: about)
        let version = Bundle.main.object(forInfoDictionaryKey: "FnCPNProductVersion") as? String ?? "—"
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "—"
        fill(label(L("版本 {0} · 构建 {1}", version, build), secondary: true), in: about)
        let revision = Bundle.main.object(forInfoDictionaryKey: "FnCPNRevision") as? String ?? "unknown"
        let commit = label("Commit  " + revision, secondary: true); commit.isSelectable = true
        fill(commit, in: about)
        for (tag, title) in [L("GitHub 项目"), L("官方网站"), L("下载服务端与其他客户端")].enumerated() {
            let button = SettingsButton(title: title + " ↗", target: self, action: #selector(openLink(_:)))
            button.tag = tag; button.isBordered = false; button.contentTintColor = .linkColor
            about.addArrangedSubview(button)
        }
    }
    private func buildLinks() {
        heading(L("网络链路"), in: page)
        let summary = NSStackView(views: [nas, current]); summary.orientation = .vertical; summary.alignment = .leading; summary.spacing = 6
        current.font = .systemFont(ofSize: 12); current.textColor = .secondaryLabelColor
        nas.lineBreakMode = .byTruncatingMiddle; current.lineBreakMode = .byTruncatingTail
        fill(summary, in: page)
        for text in [nas, current] { text.translatesAutoresizingMaskIntoConstraints = false; text.widthAnchor.constraint(equalTo: summary.widthAnchor).isActive = true; text.setContentCompressionResistancePriority(.defaultLow, for: .horizontal) }
        let policy = group(L("开关与优先级"), help: L("从上到下尝试链路，拖动可调整远程链路优先级。局域网始终第一且不能禁用。禁用两条远程链路后，仅能在局域网连接。"), in: page)
        let column = NSTableColumn(identifier: .init("link")); table.addTableColumn(column)
        column.resizingMask = .autoresizingMask; table.columnAutoresizingStyle = .lastColumnOnlyAutoresizingStyle
        table.headerView = nil; table.rowHeight = 42; table.intercellSpacing = NSSize(width: 0, height: 2)
        table.backgroundColor = .clear; table.delegate = self; table.dataSource = self
        table.registerForDraggedTypes([.string]); table.setDraggingSourceOperationMask(.move, forLocal: true)
        table.setAccessibilityLabel(L("链路优先级"))
        let rows = NSScrollView(); rows.documentView = table; rows.drawsBackground = false; rows.hasVerticalScroller = false
        rows.heightAnchor.constraint(equalToConstant: 132).isActive = true; fill(rows, in: policy)
        let controls = NSStackView(); controls.orientation = .horizontal
        for (tag, title) in [L("上移"), L("下移"), L("恢复默认设置")].enumerated() {
            let button = SettingsButton(title: title, target: self, action: #selector(reorder(_:))); button.tag = tag; controls.addArrangedSubview(button)
        }
        fill(controls, in: policy)
        let network = group(L("网络链路测试"), in: page)
        let controlsRow = NSStackView(); controlsRow.orientation = .horizontal; controlsRow.spacing = 10
        selector.bezelStyle = .rounded; selector.target = self; selector.action = #selector(showLinkSelector)
        selector.image = NSImage(systemSymbolName: "chevron.down", accessibilityDescription: nil); selector.imagePosition = .imageTrailing
        selector.widthAnchor.constraint(equalToConstant: 210).isActive = true
        selector.setAccessibilityLabel(L("测试链路"))
        controlsRow.addArrangedSubview(label(L("测试链路")))
        updateSelector(); controlsRow.addArrangedSubview(selector)
        startButton.target = self; startButton.action = #selector(startTest); startButton.bezelStyle = .rounded; startButton.keyEquivalent = ""
        controlsRow.addArrangedSubview(startButton)
        cancelButton.target = self; cancelButton.action = #selector(cancelTest); cancelButton.isHidden = true; controlsRow.addArrangedSubview(cancelButton)
        fill(controlsRow, in: network)
        fill(phase, in: network); phase.isHidden = true
        canvas.heightAnchor.constraint(equalToConstant: 210).isActive = true; fill(canvas, in: network); canvas.isHidden = true
        liveResults.font = .monospacedDigitSystemFont(ofSize: 12, weight: .regular); fill(liveResults, in: network); liveResults.isHidden = true
        resultArea.orientation = .vertical; resultArea.alignment = .leading; resultArea.spacing = 10
        let resultTitle = label(L("测试结果")); resultTitle.font = .systemFont(ofSize: 13, weight: .semibold); resultArea.addArrangedSubview(resultTitle)
        results.columnSpacing = 12; results.rowSpacing = 10
        results.addRow(with: [L("链路"), L("延迟"), L("下载"), L("上传")].map { label($0, size: 12, secondary: true) })
        for title in [L("IPv6 直连"), L("FN Connect 中继")] {
            let cells = (0..<3).map { _ in label("—", size: 12) }; values.append(cells)
            cells.forEach { $0.font = .monospacedDigitSystemFont(ofSize: 12, weight: .regular) }
            results.addRow(with: [label(title, size: 12)] + cells)
        }
        results.column(at: 0).width = 130
        for index in 1...3 { results.column(at: index).width = 105 }
        resultArea.addArrangedSubview(results); fill(resultArea, in: network); resultArea.isHidden = true
        let errorRow = NSStackView(); errorRow.orientation = .horizontal; errorRow.alignment = .top
        notice.font = .systemFont(ofSize: 12); notice.textColor = .systemRed
        notice.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        errorRow.addArrangedSubview(notice)
        let dismiss = NSButton(image: NSImage(systemSymbolName: "xmark.circle", accessibilityDescription: L("关闭提示"))!, target: self, action: #selector(dismissNotice))
        dismiss.isBordered = false; errorRow.addArrangedSubview(dismiss)
        fill(errorRow, in: network); errorRow.isHidden = true
        detailsButton.target = self; detailsButton.action = #selector(toggleErrorDetails); detailsButton.isBordered = false; detailsButton.contentTintColor = .secondaryLabelColor
        network.addArrangedSubview(detailsButton); detailsButton.isHidden = true
        errorDetails.font = .monospacedSystemFont(ofSize: 11, weight: .regular); errorDetails.textColor = .secondaryLabelColor
        errorDetails.isSelectable = true; errorDetails.lineBreakMode = .byCharWrapping
        fill(errorDetails, in: network); errorDetails.isHidden = true
    }
    func open(linkPage: Bool = false) {
        sidebar.selectRowIndexes(IndexSet(integer: linkPage ? 1 : 0), byExtendingSelection: false); displayPage(linkPage ? 1 : 0)
        autoStart.state = SMAppService.mainApp.status == .enabled ? .on : .off
        if let window { WindowPresence.shared.show(window) }
        polling?.invalidate(); polling = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in self?.poll() }; poll()
        if !lastState.isEmpty { canvas.update(lastState) }
    }
    private func displayPage(_ index: Int) {
        selectorPopover?.close()
        pageBottom?.isActive = false
        for old in pageDocument.subviews { old.removeFromSuperview() }
        let view = index == 0 ? general : page
        pageDocument.addSubview(view)
        NSLayoutConstraint.activate([view.leadingAnchor.constraint(equalTo: pageDocument.leadingAnchor, constant: 24), view.trailingAnchor.constraint(equalTo: pageDocument.trailingAnchor, constant: -28), view.topAnchor.constraint(equalTo: pageDocument.topAnchor, constant: 24)])
        pageBottom = pageDocument.bottomAnchor.constraint(equalTo: view.bottomAnchor, constant: 24); pageBottom?.isActive = true
        pageDocument.layoutSubtreeIfNeeded(); pageDocument.scroll(.zero)
    }
    func windowWillClose(_ notification: Notification) { selectorPopover?.close(); cancellation?.cancel(); polling?.invalidate(); polling = nil; canvas.stopAnimation() }
    func numberOfRows(in tableView: NSTableView) -> Int { tableView === sidebar ? 2 : 1 + links.count }
    func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? { tableView === sidebar ? SettingsSidebarRow() : nil }
    func tableViewSelectionDidChange(_ notification: Notification) { if notification.object as? NSTableView === sidebar { displayPage(max(0, sidebar.selectedRow)) } }
    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        if tableView === sidebar {
            let cell = NSTableCellView(); let name = NSTextField(labelWithString: row == 0 ? L("通用") : L("网络链路")); name.translatesAutoresizingMaskIntoConstraints = false
            cell.addSubview(name); cell.textField = name
            NSLayoutConstraint.activate([name.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 12), name.centerYAnchor.constraint(equalTo: cell.centerYAnchor)])
            return cell
        }
        let cell = NSTableCellView()
        let name = row == 0 ? "local" : links[row - 1]["link"] as? String ?? ""
        let enabled = row == 0 || links[row - 1]["enabled"] as? Bool == true
        let suffix = row == 0 ? L(" · 始终优先") : currentPath == name ? L(" · 当前使用") : !enabled ? L(" · 已禁用") : ""
        let text = label((row == 0 ? "" : "⠿  ") + linkName(name) + suffix)
        text.textColor = row == 0 ? .tertiaryLabelColor : enabled ? .labelColor : .secondaryLabelColor
        text.translatesAutoresizingMaskIntoConstraints = false; cell.addSubview(text)
        NSLayoutConstraint.activate([text.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 6), text.centerYAnchor.constraint(equalTo: cell.centerYAnchor)])
        if row > 0 {
            let toggle = SettingsSwitch(); toggle.state = enabled ? .on : .off; toggle.target = self; toggle.action = #selector(toggleLink(_:)); toggle.tag = row - 1; toggle.isEnabled = !saving && !fnID.isEmpty
            toggle.setAccessibilityLabel(L("启用 {0}", linkName(name))); toggle.translatesAutoresizingMaskIntoConstraints = false; cell.addSubview(toggle)
            NSLayoutConstraint.activate([toggle.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -6), toggle.centerYAnchor.constraint(equalTo: cell.centerYAnchor), text.trailingAnchor.constraint(lessThanOrEqualTo: toggle.leadingAnchor, constant: -8)])
        } else { text.trailingAnchor.constraint(lessThanOrEqualTo: cell.trailingAnchor).isActive = true }
        return cell
    }
    private func linkName(_ key: String) -> String { key == "local" ? L("局域网") : key == "ipv6" ? L("IPv6 直连") : key == "fn-connect" ? L("FN Connect 中继") : L("未连接") }
    private func poll() {
        guard !inFlight, !saving else { return }; inFlight = true
        work.async { [weak self] in
            guard let self else { return }
            do {
                let settings = try self.rpc.call(method: "link-settings"), connection = try self.rpc.call(method: "status")
                DispatchQueue.main.async {
                    self.inFlight = false
                    let next = settings["links"] as? [[String: Any]] ?? [], fn = settings["fnId"] as? String ?? "", path = connection["path"] as? String ?? ""
                    let changed = !NSArray(array: next).isEqual(to: self.links) || fn != self.fnID || path != self.currentPath
                    self.links = next; self.fnID = fn; self.currentPath = path
                    self.nas.stringValue = fn.isEmpty ? L("请先登录 NAS") : L("当前 NAS：{0}", fn)
                    self.current.stringValue = L("当前链路：{0}", self.linkName(path))
                    self.startButton.isEnabled = !fn.isEmpty && !self.selected.isEmpty && !self.confirming
                    if changed { self.table.reloadData() }
                }
            } catch { DispatchQueue.main.async { self.inFlight = false } }
        }
    }
    @objc private func toggleAutoStartSetting() { toggleLoginItem?(); autoStart.state = SMAppService.mainApp.status == .enabled ? .on : .off }
    @objc private func openLink(_ sender: NSButton) {
        let links = ["https://github.com/rectcircle/fn-connect-private-network", "https://fncpn.rectcircle.cn", "https://github.com/rectcircle/fn-connect-private-network/releases"]
        if let url = URL(string: links[sender.tag]) { NSWorkspace.shared.open(url) }
    }
    private func showNotice(_ message: String) {
        notice.stringValue = message.count > 180 ? String(message.prefix(140)) + "…" : message
        notice.superview?.isHidden = message.isEmpty
        errorDetails.stringValue = message; errorDetails.isHidden = true
        detailsButton.isHidden = message.count <= 180
    }
    @objc private func toggleErrorDetails() { errorDetails.isHidden.toggle() }
    @objc private func dismissNotice() { errorMessage = ""; showNotice("") }
    private func updateSelector() {
        selector.title = selected.count == 2 ? L("IPv6 与 FN Connect") : selected.contains("ipv6") ? L("IPv6 直连") : selected.contains("fn-connect") ? L("FN Connect 中继") : L("选择测试链路")
        startButton.isEnabled = !selected.isEmpty && !fnID.isEmpty && !confirming
    }
    @objc private func showLinkSelector() {
        if selectorPopover?.isShown == true { selectorPopover?.close(); return }
        let controller = NSViewController()
        let stack = NSStackView(); stack.orientation = .vertical; stack.alignment = .leading; stack.spacing = 12
        for (index, key) in ["ipv6", "fn-connect"].enumerated() {
            let check = SettingsButton(checkboxWithTitle: linkName(key), target: self, action: #selector(selectLink(_:)))
            check.tag = index; check.state = selected.contains(key) ? .on : .off; stack.addArrangedSubview(check)
        }
        let done = SettingsButton(title: L("完成"), target: self, action: #selector(closeLinkSelector)); done.bezelStyle = .rounded; stack.addArrangedSubview(done)
        let view = NSView(frame: NSRect(x: 0, y: 0, width: 240, height: 116)); stack.translatesAutoresizingMaskIntoConstraints = false; view.addSubview(stack)
        NSLayoutConstraint.activate([stack.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 18), stack.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -18), stack.topAnchor.constraint(equalTo: view.topAnchor, constant: 18), stack.bottomAnchor.constraint(equalTo: view.bottomAnchor, constant: -16)])
        controller.view = view
        let popover = NSPopover(); popover.behavior = .transient; popover.animates = false; popover.contentViewController = controller; selectorPopover = popover
        popover.show(relativeTo: selector.bounds, of: selector, preferredEdge: .maxY)
    }
    @objc private func closeLinkSelector() { selectorPopover?.close() }
    @objc private func selectLink(_ sender: NSButton) {
        let key = sender.tag == 0 ? "ipv6" : "fn-connect"
        if sender.state == .on { selected.insert(key) } else { selected.remove(key) }
        updateSelector()
    }
    @objc private func startTest() {
        guard !selected.isEmpty, !confirming, let window else { return }
        selectorPopover?.close()
        let selection = selected
        let alert = NSAlert()
        alert.messageText = L("开始网络链路测试？")
        alert.informativeText = L("测试期间会重新建立连接，现有连接可能短暂中断。") + "\n\n" + L("测试结果仅供参考，会受到 Mac 与 NAS 当时的网络状况及可用带宽影响；使用 FN Connect 时，还受中转服务器带宽限制。")
        alert.alertStyle = .informational
        alert.addButton(withTitle: L("开始测试")); alert.addButton(withTitle: L("取消"))
        confirming = true; updateSelector()
        alert.beginSheetModal(for: window) { [weak self] result in
            guard let self else { return }
            self.confirming = false; self.updateSelector()
            if result == .alertFirstButtonReturn { self.beginTest(selection) }
        }
    }
    private func beginTest(_ selection: Set<String>) {
        cancellation?.cancel()
        let token = IPCCancellation(); cancellation = token
        errorMessage = ""; showNotice(""); resultArea.isHidden = true; canvas.reset()
        active = true; startButton.title = L("重新测试"); cancelButton.isHidden = false
        render(["active": true, "stage": "preparing", "results": []])
        window?.contentView?.layoutSubtreeIfNeeded(); canvas.scrollToVisible(canvas.bounds)
        let target = selection.count == 2 ? "all" : selection.first!
        testing.async { [weak self] in
            guard let self else { return }
            do {
                _ = try self.rpc.call(method: "speedtest", params: ["link": target], cancellation: token, receive: { state in
                    DispatchQueue.main.async { guard self.cancellation === token else { return }; self.render(state) }
                    return state["active"] as? Bool != true
                })
            } catch {
                let message = error.localizedDescription
                DispatchQueue.main.async {
                    guard self.cancellation === token else { return }
                    var state = self.lastState; state["active"] = false; state["stage"] = token.isCanceled ? "canceled" : "failed"
                    self.render(state)
                    if !token.isCanceled { self.showNotice(message) }
                }
            }
        }
    }
    @objc private func cancelTest() { cancellation?.cancel(); phase.stringValue = L("正在取消并恢复连接…"); cancelButton.isEnabled = false }
    private func render(_ state: [String: Any]) {
        let wasActive = active
        lastState = state; active = state["active"] as? Bool == true
        let stage = state["stage"] as? String ?? "idle"
        canvas.isHidden = !active; canvas.nasName = fnID; canvas.update(state)
        phase.isHidden = !active; phase.stringValue = active ? L("正在测试：{0}", linkName(state["link"] as? String ?? "")) : ""
        cancelButton.isHidden = !active; cancelButton.isEnabled = active && stage != "restoring"
        startButton.title = active ? L("重新测试") : L("开始测试")
        let rows = state["results"] as? [[String: Any]] ?? []
        resultArea.isHidden = active || rows.isEmpty
        var summaries: [String] = [], failures: [String] = []
        for (i, key) in ["ipv6", "fn-connect"].enumerated() {
            let result = rows.first { $0["link"] as? String == key }
            results.row(at: i + 1).isHidden = result == nil
            guard let result else { continue }
            var completed: [String] = []
            for (j, field) in ["latencyMs", "downloadMbps", "uploadMbps"].enumerated() {
                if let number = result[field] as? Double { let text = String(format: j == 0 ? "%.1f ms" : "%.1f Mbps", number); values[i][j].stringValue = text; completed.append([L("延迟"), L("下载"), L("上传")][j] + " " + text) }
                else { values[i][j].stringValue = result["error"] != nil ? L("未完成") : stage == "canceled" ? L("已取消") : "—" }
            }
            if !completed.isEmpty { summaries.append(linkName(key) + " · " + completed.joined(separator: "  ")) }
            if let error = result["error"] as? String { let message = linkName(key) + "：" + L(error); failures.append(message); if active { summaries.append(message) } }
            values[i].forEach { $0.toolTip = result["completedAt"] as? String }
        }
        liveResults.isHidden = !active || summaries.isEmpty; liveResults.stringValue = summaries.joined(separator: "\n")
        if !active {
            if let error = state["restoreError"] as? String, !error.isEmpty { failures.append(error) }
            showNotice(failures.joined(separator: "\n"))
            if wasActive { window?.contentView?.layoutSubtreeIfNeeded(); resultArea.scrollToVisible(resultArea.bounds) }
        }
    }
    func tableView(_ tableView: NSTableView, pasteboardWriterForRow row: Int) -> NSPasteboardWriting? {
        guard tableView === table, row > 0, !saving else { return nil }; let item = NSPasteboardItem(); item.setString(String(row), forType: .string); return item
    }
    func tableView(_ tableView: NSTableView, validateDrop info: NSDraggingInfo, proposedRow row: Int, proposedDropOperation operation: NSTableView.DropOperation) -> NSDragOperation {
        guard tableView === table, info.draggingSource as? NSTableView === table, row >= 1, !saving else { return [] }; table.setDropRow(row, dropOperation: .above); return .move
    }
    func tableView(_ tableView: NSTableView, acceptDrop info: NSDraggingInfo, row: Int, dropOperation: NSTableView.DropOperation) -> Bool {
        guard tableView === table, let value = info.draggingPasteboard.string(forType: .string), let source = Int(value), source > 0, source <= links.count, row >= 1 else { return false }
        let target = min(links.count - 1, max(0, row - 1 - (row > source ? 1 : 0)))
        if target != source - 1 { var next = links; let entry = next.remove(at: source - 1); next.insert(entry, at: target); save(next) }; return true
    }
    @objc private func toggleLink(_ sender: NSSwitch) { guard links.indices.contains(sender.tag) else { return }; var next = links; next[sender.tag]["enabled"] = sender.state == .on; save(next) }
    @objc private func reorder(_ sender: NSButton) {
        guard !saving, !fnID.isEmpty else { return }
        if sender.tag == 2 { save([["link": "ipv6", "enabled": true], ["link": "fn-connect", "enabled": true]]); return }
        let source = table.selectedRow - 1, target = table.selectedRow - 1 + (sender.tag == 0 ? -1 : 1)
        guard links.indices.contains(source), links.indices.contains(target) else { return }; var next = links; next.swapAt(source, target); save(next); table.selectRowIndexes(IndexSet(integer: target + 1), byExtendingSelection: false)
    }
    private func save(_ next: [[String: Any]]) {
        guard !saving else { return }; saving = true; let target = fnID
        work.async { [weak self] in
            guard let self else { return }
            do { _ = try self.rpc.call(method: "set-link-settings", params: ["fnId": target, "links": next]); DispatchQueue.main.async { self.saving = false; self.poll() } }
            catch { let message = error.localizedDescription; DispatchQueue.main.async { self.saving = false; self.showNotice(message); self.table.reloadData() } }
        }
    }
}

private final class SettingsCard: NSView {
    let body = NSStackView()
    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true; layer?.cornerRadius = 10; layer?.borderWidth = 0.5
        body.orientation = .vertical; body.alignment = .leading; body.spacing = 12; body.translatesAutoresizingMaskIntoConstraints = false; addSubview(body)
        NSLayoutConstraint.activate([body.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 16), body.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -16), body.topAnchor.constraint(equalTo: topAnchor, constant: 16), body.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -16)])
    }
    convenience init() { self.init(frame: .zero) }
    required init?(coder: NSCoder) { fatalError("init(coder:) is unavailable") }
    override var wantsUpdateLayer: Bool { true }
    override func updateLayer() { layer?.backgroundColor = NSColor.controlBackgroundColor.cgColor; layer?.borderColor = NSColor.separatorColor.withAlphaComponent(0.35).cgColor }
}

private final class SpeedTestCanvas: NSView {
    private var state: [String: Any] = [:]
    private var timer: Timer?
    private var history: [Double] = []
    private var sequence: UInt64 = 0
    private var sent: UInt64 = 0
    private var received: UInt64 = 0
    private var pingAt: TimeInterval = 0
    private var pongAt: TimeInterval = 0
    var nasName = ""
    override init(frame frameRect: NSRect) { super.init(frame: frameRect); wantsLayer = true; layer?.cornerRadius = 12; setAccessibilityLabel(L("链路测速动画")) }
    convenience init() { self.init(frame: .zero) }
    required init?(coder: NSCoder) { fatalError("init(coder:) is unavailable") }
    func reset() { history = []; sent = 0; received = 0; sequence = 0 }
    func stopAnimation() { timer?.invalidate(); timer = nil }
    func update(_ next: [String: Any]) {
        let oldStage = state["stage"] as? String ?? ""
        let newStage = next["stage"] as? String ?? ""
        let sameUpload = oldStage.hasPrefix("upload") && newStage.hasPrefix("upload")
        if (!sameUpload && oldStage != newStage) || state["link"] as? String != next["link"] as? String { history = []; sent = 0; received = 0; pingAt = 0; pongAt = 0 }
        state = next
        let now = ProcessInfo.processInfo.systemUptime
        let nextSent = (state["echoSent"] as? NSNumber)?.uint64Value ?? 0
        let nextReceived = (state["echoReceived"] as? NSNumber)?.uint64Value ?? 0
        if nextSent != sent { sent = nextSent; if now >= pongAt + 0.6 { pingAt = now } }
        if nextReceived != received { received = nextReceived; pongAt = max(now, pingAt + 0.25) }
        let nextSequence = (state["sequence"] as? NSNumber)?.uint64Value ?? 0
        if nextSequence != sequence { sequence = nextSequence; if ["download", "upload"].contains(state["stage"] as? String ?? "") { history.append(state["mbps"] as? Double ?? 0); if history.count > 80 { history.removeFirst() } } }
        if state["active"] as? Bool == true && !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion {
            if timer == nil { timer = Timer.scheduledTimer(withTimeInterval: 1.0 / 30, repeats: true) { [weak self] _ in self?.needsDisplay = true } }
        } else { stopAnimation() }
        needsDisplay = true
    }
    private func text(_ value: String, rect: NSRect, size: CGFloat = 12, color: NSColor = .labelColor, weight: NSFont.Weight = .regular) {
        let paragraph = NSMutableParagraphStyle(); paragraph.alignment = .center; paragraph.lineBreakMode = .byTruncatingMiddle
        (value as NSString).draw(in: rect, withAttributes: [.font: NSFont.systemFont(ofSize: size, weight: weight), .foregroundColor: color, .paragraphStyle: paragraph])
    }
    override func draw(_ dirtyRect: NSRect) {
        NSColor.controlBackgroundColor.setFill(); NSBezierPath(roundedRect: bounds, xRadius: 12, yRadius: 12).fill()
        let stage = state["stage"] as? String ?? "idle"
        let link = state["link"] as? String ?? ""
        let names = ["idle": L("选择链路开始测速"), "preparing": L("准备测试"), "checking": L("检查链路条件"), "failed": L("测试失败"), "queued": L("等待连接任务"), "connecting": L("正在建立真实链路"), "verifying": L("正在验证 NAS"), "latency": L("正在测试延迟"), "download": L("正在测试下载"), "upload": L("正在测试上传"), "upload-warmup": L("正在预热上传"), "upload-waiting": L("等待 NAS 接收数据"), "upload-confirming": L("等待 NAS 完成确认"), "upload-complete": L("上传完成"), "restoring": L("正在恢复正常选路"), "completed": L("测速完成"), "canceled": L("测速已取消"), "canceling": L("正在取消")]
        text(names[stage] ?? stage, rect: NSRect(x: 12, y: bounds.height - 30, width: bounds.width - 24, height: 20), size: 13, weight: .medium)
        let y: CGFloat = 121
        let xs: [CGFloat] = link == "fn-connect" ? [58, bounds.midX, bounds.width - 58] : [58, bounds.width - 58]
        let active = state["active"] as? Bool == true
        let moving = active && ["latency", "download", "upload"].contains(stage)
        let reduced = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
        let now = ProcessInfo.processInfo.systemUptime
        let left = xs.first! + 31; let right = xs.last! - 31
        for index in 0..<(xs.count - 1) {
            let a = xs[index] + 31; let b = xs[index + 1] - 31
            for direction in 0..<2 {
                let laneY = y + (direction == 0 ? 8 : -8)
                let highlight = moving && (stage == "latency" || (stage == "upload" && direction == 0) || (stage == "download" && direction == 1))
                (highlight ? NSColor.controlAccentColor : NSColor.separatorColor).setStroke()
                let path = NSBezierPath(); path.lineWidth = highlight ? 2.5 : 1.5; path.move(to: NSPoint(x: a, y: laneY)); path.line(to: NSPoint(x: b, y: laneY)); path.stroke()
                let arrow = NSBezierPath(); let tip = direction == 0 ? b : a; let offset: CGFloat = direction == 0 ? -5 : 5
                arrow.move(to: NSPoint(x: tip + offset, y: laneY + 4)); arrow.line(to: NSPoint(x: tip, y: laneY)); arrow.line(to: NSPoint(x: tip + offset, y: laneY - 4)); arrow.stroke()
                if highlight && !reduced {
                    var positions: [Double] = []
                    if stage == "latency" { let age = now - (direction == 0 ? pingAt : pongAt); if age >= 0 && age < 0.25 && (direction == 0 ? sent > 0 : received > 0) { let t = age / 0.25; positions = [t * t * (3 - 2 * t)] } }
                    else { positions = (0..<3).map { (now * 0.65 + Double($0) / 3).truncatingRemainder(dividingBy: 1) } }
                    for progress in positions { let x = direction == 0 ? left + (right - left) * progress : right - (right - left) * progress; if x >= a && x <= b { NSColor.controlAccentColor.setFill(); NSBezierPath(ovalIn: NSRect(x: x - 3, y: laneY - 3, width: 6, height: 6)).fill() } }
                }
            }
        }
        for (index, x) in xs.enumerated() {
            let frame = NSRect(x: x - 27, y: y - 24, width: 54, height: 48)
            NSColor.windowBackgroundColor.setFill(); NSBezierPath(roundedRect: frame, xRadius: 10, yRadius: 10).fill()
            (moving ? NSColor.controlAccentColor : NSColor.separatorColor).setStroke(); NSBezierPath(roundedRect: frame, xRadius: 10, yRadius: 10).stroke()
            let symbol = index == 0 ? "laptopcomputer" : index == xs.count - 1 ? "externaldrive.connected.to.line.below" : "server.rack"
            if let image = NSImage(systemSymbolName: symbol, accessibilityDescription: nil)?.withSymbolConfiguration(NSImage.SymbolConfiguration(paletteColors: [.labelColor])) { image.draw(in: frame.insetBy(dx: 13, dy: 10)) }
            text(index == 0 ? L("这台 Mac") : index == xs.count - 1 ? (nasName.isEmpty ? "NAS" : nasName) : L("FN Connect 中转"), rect: NSRect(x: x - 60, y: y - 47, width: 120, height: 18), size: 10, color: .secondaryLabelColor)
        }
        let rate = state["mbps"] as? Double ?? 0
        let metric: String
        switch stage {
        case "latency": metric = String(format: "%.1f ms", state["rttMs"] as? Double ?? 0)
        case "upload", "upload-complete", "download": metric = String(format: "%.1f Mbps", rate)
        case "upload-warmup": metric = L("预热中…")
        case "upload-waiting": metric = L("等待接收…")
        case "upload-confirming": metric = L("等待确认…")
        default: metric = link == "ipv6" ? L("IPv6 直连") : link == "fn-connect" ? L("FN Connect 中继") : ""
        }
        text(metric, rect: NSRect(x: 12, y: 43, width: bounds.width - 24, height: 28), size: 23, weight: .semibold)
        if history.count > 1 {
            let maximum = max(history.max() ?? 1, 1)
            let graph = NSBezierPath(); graph.lineWidth = 1.5
            for (i, sample) in history.enumerated() { let point = NSPoint(x: 24 + (bounds.width - 48) * CGFloat(i) / CGFloat(max(79, history.count - 1)), y: 9 + CGFloat(sample / maximum) * 22); if i == 0 { graph.move(to: point) } else { graph.line(to: point) } }
            NSColor.controlAccentColor.setStroke(); graph.stroke()
        }
        if moving { let progress = min(1, max(0, state["progress"] as? Double ?? 0)); NSColor.controlAccentColor.withAlphaComponent(0.3).setFill(); NSBezierPath(rect: NSRect(x: 0, y: 0, width: bounds.width * progress, height: 3)).fill() }
    }
}

private final class SettingsDocumentView: NSView { override var isFlipped: Bool { true } }

private final class IPCCancellation {
 private let lock = NSLock()
 private var descriptor: Int32?
 private var canceled = false
 var isCanceled: Bool { lock.lock(); defer { lock.unlock() }; return canceled }
 func attach(_ fd: Int32) throws { lock.lock(); defer { lock.unlock() }; if canceled { throw POSIXError(.ECANCELED) }; descriptor = fd }
 func detach() { lock.lock(); descriptor = nil; lock.unlock() }
 func cancel() { lock.lock(); canceled = true; if let fd = descriptor { _ = Darwin.shutdown(fd, SHUT_RDWR) }; lock.unlock() }
}

private final class SettingsBackgroundView: NSView {
 override func draw(_ dirtyRect: NSRect) { NSColor.windowBackgroundColor.setFill(); bounds.fill() }
}
private final class SettingsSidebarRow: NSTableRowView {
 override var interiorBackgroundStyle: NSView.BackgroundStyle { .normal }
 override func drawSelection(in dirtyRect: NSRect) {
  NSColor.controlAccentColor.withAlphaComponent(0.16).setFill()
  NSBezierPath(roundedRect: bounds.insetBy(dx: 2, dy: 2), xRadius: 7, yRadius: 7).fill()
 }
}

// Only user-facing windows participate. Popovers and sheets must not keep an
// otherwise headless menu-bar app in the Dock; minimized windows still count.
private final class WindowPresence {
    static let shared = WindowPresence()
    private let windows = NSHashTable<NSWindow>.weakObjects()
    private var observers: [NSObjectProtocol] = []
    private init() {
        for name in [NSWindow.willCloseNotification, NSWindow.didMiniaturizeNotification, NSWindow.didDeminiaturizeNotification, NSWindow.didBecomeKeyNotification] {
            observers.append(NotificationCenter.default.addObserver(forName: name, object: nil, queue: .main) { [weak self] note in
                guard let self, let window = note.object as? NSWindow, self.windows.contains(window) else { return }
                self.update(excluding: note.name == NSWindow.willCloseNotification ? window : nil)
            })
        }
    }
    func register(_ window: NSWindow) { windows.add(window) }
    func show(_ window: NSWindow) {
        register(window)
        if NSApp.activationPolicy() != .regular { NSApp.setActivationPolicy(.regular) }
        window.deminiaturize(nil); window.makeKeyAndOrderFront(nil)
        update(); NSApp.activate(ignoringOtherApps: true)
    }
    func update(excluding closing: NSWindow? = nil) {
        let visible = windows.allObjects.contains { $0 !== closing && ($0.isVisible || $0.isMiniaturized) }
        let policy: NSApplication.ActivationPolicy = visible ? .regular : .accessory
        if NSApp.activationPolicy() != policy { NSApp.setActivationPolicy(policy) }
    }
}

private final class HoverHelpButton: SettingsButton {
    var helpText = "" { didSet { setAccessibilityHelp(helpText) } }
    private var tracking: NSTrackingArea?
    private var popover: NSPopover?
    override func updateTrackingAreas() {
        if let tracking { removeTrackingArea(tracking) }
        let area = NSTrackingArea(rect: bounds, options: [.mouseEnteredAndExited, .activeAlways, .inVisibleRect], owner: self, userInfo: nil)
        addTrackingArea(area); tracking = area; super.updateTrackingAreas()
    }
    override func mouseEntered(with event: NSEvent) { showExplanation() }
    override func mouseExited(with event: NSEvent) { popover?.close() }
    override func viewDidMoveToWindow() { super.viewDidMoveToWindow(); if window == nil { popover?.close() } }
    override func accessibilityPerformPress() -> Bool { showExplanation(); return true }
    private func showExplanation() {
        guard window != nil, popover?.isShown != true else { return }
        let text = NSTextField(wrappingLabelWithString: helpText); text.font = .systemFont(ofSize: 12); text.preferredMaxLayoutWidth = 288
        let height = min(220, max(60, (text.cell?.cellSize(forBounds: NSRect(x: 0, y: 0, width: 288, height: 1000)).height ?? 80) + 28))
        let view = NSView(frame: NSRect(x: 0, y: 0, width: 320, height: height)); text.translatesAutoresizingMaskIntoConstraints = false; view.addSubview(text)
        NSLayoutConstraint.activate([text.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 16), text.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -16), text.topAnchor.constraint(equalTo: view.topAnchor, constant: 14), text.bottomAnchor.constraint(lessThanOrEqualTo: view.bottomAnchor, constant: -14)])
        let controller = NSViewController(); controller.view = view
        let help = NSPopover(); help.behavior = .transient; help.animates = false; help.contentViewController = controller; popover = help
        help.show(relativeTo: bounds, of: self, preferredEdge: .maxY)
    }
}

// Native controls use an arrow by default; settings exposes clickable regions
// consistently, including popover choices and the navigation rows.
private class SettingsButton: NSButton {
    override func resetCursorRects() {
        super.resetCursorRects()
        if isEnabled { addCursorRect(bounds, cursor: .pointingHand) }
    }
}
private final class SettingsSwitch: NSSwitch {
    override func resetCursorRects() {
        super.resetCursorRects()
        if isEnabled { addCursorRect(bounds, cursor: .pointingHand) }
    }
}
private final class SettingsTableView: NSTableView {
    var allRowsClickable = false
    override func resetCursorRects() {
        super.resetCursorRects()
        for row in 0..<numberOfRows where allRowsClickable || row > 0 {
            addCursorRect(rect(ofRow: row), cursor: .pointingHand)
        }
    }
}
