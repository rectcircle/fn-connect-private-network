import Foundation
import NetworkExtension
import Network
import Security
import UIKit
import Darwin

private weak var activeProvider: PacketTunnelProvider?
private let platformCall: @convention(c) (UnsafePointer<CChar>?) -> UnsafeMutablePointer<CChar>? = { request in
    guard let request, let provider = activeProvider else { return strdup("{\"ok\":false,\"error\":\"provider unavailable\"}") }
    do {
        let data = Data(String(cString: request).utf8)
        guard let message = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              message["version"] as? Int == 1, let method = message["method"] as? String else { throw PlatformError.invalid }
        let result = try provider.systemCall(method, message["params"] as? [String: Any] ?? [:])
        let reply = try JSONSerialization.data(withJSONObject: ["ok": true, "result": result])
        return strdup(String(decoding: reply, as: UTF8.self))
    } catch {
        fncpn_platform_event(6, 0)
        // Platform failures contain no credential input or network plan.
        return strdup("{\"ok\":false,\"error\":\"iOS platform operation failed\"}")
    }
}
private let packetWrite: @convention(c) (UnsafeRawPointer?, Int32) -> Int32 = { bytes, count in
    guard let bytes, count > 0, let provider = activeProvider, !provider.stopping else { return -1 }
    let data = Data(bytes: bytes, count: Int(count))
    let family: NSNumber = (data[0] >> 4 == 6) ? NSNumber(value: AF_INET6) : NSNumber(value: AF_INET)
    return provider.packetFlow.writePackets([data], withProtocols: [family]) ? 0 : -1
}
private enum PlatformError: Error { case invalid, timeout, unavailable, keychain(OSStatus) }

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let worker = DispatchQueue(label: "cn.rectcircle.fncpn.core", attributes: .concurrent)
    private var monitor: NWPathMonitor?
    private let connectionsLock = NSLock()
    private var connections: [String: NWTCPConnection] = [:]
    private let lifecycleLock = NSLock()
    private var isStopping = false
    var stopping: Bool { lifecycleLock.lock(); defer { lifecycleLock.unlock() }; return isStopping }

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        guard activeProvider == nil else { completionHandler(PlatformError.unavailable); return }
        guard let container = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first else {
            completionHandler(PlatformError.unavailable); return
        }
        activeProvider = self
        fncpn_set_callbacks(platformCall, packetWrite)
        worker.async {
            let directory = container.appendingPathComponent("Client", isDirectory: true)
            do {
                try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true,
                    attributes: [.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication])
                var values = URLResourceValues(); values.isExcludedFromBackup = true
                var excluded = directory; try excluded.setResourceValues(values)
                let result = directory.path.withCString { fncpn_start(UnsafeMutablePointer(mutating: $0)) }
                defer { free(result) }
                if let result, result.pointee != 0 { throw PlatformError.unavailable }
                let monitor = NWPathMonitor()
                monitor.pathUpdateHandler = { _ in fncpn_network_changed() }
                self.lifecycleLock.lock()
                if self.isStopping {
                    self.lifecycleLock.unlock()
                    let result = fncpn_stop(); free(result)
                    completionHandler(PlatformError.unavailable); return
                }
                self.monitor = monitor; monitor.start(queue: self.worker)
                self.lifecycleLock.unlock()
                self.readPackets()
                fncpn_platform_event(1, 0)
                completionHandler(nil)
            } catch { activeProvider = nil; completionHandler(error) }
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        fncpn_platform_event(2, Int32(reason.rawValue))
        lifecycleLock.lock(); isStopping = true; lifecycleLock.unlock()
        lifecycleLock.lock(); monitor?.cancel(); monitor = nil; lifecycleLock.unlock()
        connectionsLock.lock()
        let streams = Array(connections.values); connections.removeAll()
        connectionsLock.unlock()
        streams.forEach { $0.cancel() }
        worker.async {
            // One shutdown cancels admitted work, saves explicit pause intent,
            // then releases resources. System teardown owns route removal.
            let paused = reason == .userInitiated || reason == .configurationDisabled || reason == .configurationRemoved
            let error = paused ? fncpn_stop_paused() : fncpn_stop(); free(error)
            activeProvider = nil
            completionHandler()
        }
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        guard messageData.count <= 256 * 1024, let input = String(data: messageData, encoding: .utf8), !stopping else {
            completionHandler?(nil); return
        }
        worker.async {
            let output = input.withCString { fncpn_call(UnsafeMutablePointer(mutating: $0)) }
            defer { free(output) }
            completionHandler?(output.map { Data(String(cString: $0).utf8) })
        }
    }

    private func readPackets() {
        guard !stopping else { return }
        packetFlow.readPackets { [weak self] packets, _ in
            guard let self, !self.stopping else { return }
            for packet in packets { packet.withUnsafeBytes { bytes in fncpn_receive(UnsafeMutableRawPointer(mutating: bytes.baseAddress), Int32(bytes.count)) } }
            self.readPackets()
        }
    }

    fileprivate func systemCall(_ method: String, _ params: [String: Any]) throws -> Any {
        switch method {
        case "device.name":
            let name = UIDevice.current.name.trimmingCharacters(in: .whitespacesAndNewlines)
            if !name.isEmpty && name != "iPhone" && name != "iPad" && name != "iPod touch" { return name }
            var info = utsname(); uname(&info)
            let capacity = MemoryLayout.size(ofValue: info.machine)
            let identifier = withUnsafePointer(to: &info.machine) {
                $0.withMemoryRebound(to: CChar.self, capacity: capacity) { String(cString: $0) }
            }
            return "\(UIDevice.current.model) (\(identifier))"
        case "network.apply":
            guard let address = params["clientAddress"] as? String, let routes = params["allowedIPs"] as? [String],
                  let mtu = params["mtu"] as? Int, (576...9000).contains(mtu) else { throw PlatformError.invalid }
            let (ip, mask) = try ipv4Prefix(address)
            let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")
            let ipv4 = NEIPv4Settings(addresses: [ip], subnetMasks: [mask])
            ipv4.includedRoutes = try routes.map { value in
                let (destination, mask) = try ipv4Prefix(value)
                return NEIPv4Route(destinationAddress: destination, subnetMask: mask)
            }
            settings.ipv4Settings = ipv4; settings.mtu = NSNumber(value: mtu)
            if let dnsIP = params["dnsIP"] as? String, let suffix = params["dnsSuffix"] as? String, !suffix.isEmpty {
                let dns = NEDNSSettings(servers: [dnsIP]); dns.matchDomains = [suffix]
                dns.matchDomainsNoSearch = true; settings.dnsSettings = dns
            }
            try apply(settings); return NSNull()
        case "network.remove": try apply(nil); return NSNull()
        case "secret.get", "secret.put", "secret.delete": return try secret(method, params)
        case "tcp.open", "tcp.read", "tcp.write", "tcp.close": return try tcp(method, params)
        default: throw PlatformError.invalid
        }
    }

    private func apply(_ settings: NEPacketTunnelNetworkSettings?) throws {
        if stopping {
            if settings != nil { throw PlatformError.unavailable }
            return // The OS is already removing this provider's configuration.
        }
        let completed = DispatchSemaphore(value: 0)
        var failure: Error?
        fncpn_platform_event(3, 0)
        setTunnelNetworkSettings(settings) { error in failure = error; completed.signal() }
        guard completed.wait(timeout: .now() + 10) == .success else {
            fncpn_platform_event(5, 1)
            cancelTunnelWithError(PlatformError.timeout)
            throw PlatformError.timeout
        }
        if let failure { fncpn_platform_event(5, 2); throw failure }
        fncpn_platform_event(4, 0)
    }

    // Native private API connections explicitly traverse this provider's tunnel.
    private func tcp(_ method: String, _ params: [String: Any]) throws -> Any {
        if method == "tcp.open" {
            guard !stopping, let host = params["host"] as? String, let port = params["port"] as? String, let number = Int(port), (1...65535).contains(number) else { throw PlatformError.invalid }
            var address = in_addr()
            guard inet_pton(AF_INET, host, &address) == 1 else { throw PlatformError.invalid }
            let bytes = host.split(separator: ".").compactMap { UInt8($0) }
            guard bytes.count == 4, bytes[0] == 10 || (bytes[0] == 172 && (16...31).contains(bytes[1])) || (bytes[0] == 192 && bytes[1] == 168) else { throw PlatformError.invalid }
            connectionsLock.lock()
            guard !stopping, connections.count < 16 else { connectionsLock.unlock(); throw PlatformError.unavailable }
            let id = UUID().uuidString
            let endpoint = NWHostEndpoint(hostname: host, port: port)
            let connection = createTCPConnectionThroughTunnel(to: endpoint, enableTLS: false, tlsParameters: nil, delegate: nil)
            connections[id] = connection; connectionsLock.unlock()
            let deadline = Date().addingTimeInterval(2)
            while connection.state != .connected && Date() < deadline && !stopping { Thread.sleep(forTimeInterval: 0.01) }
            guard connection.state == .connected else {
                connectionsLock.lock(); connections.removeValue(forKey: id); connectionsLock.unlock()
                connection.cancel(); throw PlatformError.unavailable
            }
            return ["id": id]
        }
        guard let id = params["id"] as? String else { throw PlatformError.invalid }
        connectionsLock.lock(); let connection = connections[id]
        if method == "tcp.close" { connections.removeValue(forKey: id) }
        connectionsLock.unlock()
        if method == "tcp.close" { connection?.cancel(); return NSNull() }
        guard let connection, let timeout = params["timeoutMs"] as? Int, (1...3000).contains(timeout) else { throw PlatformError.invalid }
        let completed = DispatchSemaphore(value: 0)
        var failure: Error?
        var received = Data()
        if method == "tcp.read" {
            guard let limit = params["limit"] as? Int, (1...16384).contains(limit) else { throw PlatformError.invalid }
            connection.readMinimumLength(1, maximumLength: limit) { data, error in received = data ?? Data(); failure = error; completed.signal() }
        } else {
            guard let value = params["data"] as? String, let data = Data(base64Encoded: value), data.count <= 16384 else { throw PlatformError.invalid }
            connection.write(data) { error in failure = error; completed.signal() }
        }
        guard completed.wait(timeout: .now() + .milliseconds(timeout)) == .success else {
            connection.cancel(); throw PlatformError.timeout
        }
        if let failure { throw failure }
        return method == "tcp.read" ? ["data": received.base64EncodedString()] : NSNull()
    }

    private func secret(_ method: String, _ params: [String: Any]) throws -> Any {
        guard let key = params["key"] as? String, key.count == 64 else { throw PlatformError.invalid }
        var query: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "cn.rectcircle.fncpn.ios", kSecAttrAccount as String: key]
        if method == "secret.get" {
            query[kSecReturnData as String] = true
            var item: CFTypeRef?
            let status = SecItemCopyMatching(query as CFDictionary, &item)
            if status == errSecItemNotFound { return ["found": false, "value": ""] }
            guard status == errSecSuccess, let data = item as? Data else { throw PlatformError.keychain(status) }
            return ["found": true, "value": data.base64EncodedString()]
        }
        if method == "secret.delete" {
            let status = SecItemDelete(query as CFDictionary)
            guard status == errSecSuccess || status == errSecItemNotFound else { throw PlatformError.keychain(status) }
            return NSNull()
        }
        guard let value = params["value"] as? String, let data = Data(base64Encoded: value) else { throw PlatformError.invalid }
        let changes = [kSecValueData as String: data]
        var status = SecItemUpdate(query as CFDictionary, changes as CFDictionary)
        if status == errSecItemNotFound {
            query[kSecValueData as String] = data
            query[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
            status = SecItemAdd(query as CFDictionary, nil)
        }
        guard status == errSecSuccess else { throw PlatformError.keychain(status) }
        return NSNull()
    }
}

private func ipv4Prefix(_ input: String) throws -> (String, String) {
    let parts = input.split(separator: "/", omittingEmptySubsequences: false)
    guard parts.count == 2, let bits = Int(parts[1]), (1...32).contains(bits) else { throw PlatformError.invalid }
    let octets = parts[0].split(separator: ".", omittingEmptySubsequences: false)
    guard octets.count == 4, octets.allSatisfy({ UInt8($0) != nil }) else { throw PlatformError.invalid }
    let mask = UInt32.max << (32 - bits)
    return (String(parts[0]), [24, 16, 8, 0].map { String((mask >> $0) & 255) }.joined(separator: "."))
}
