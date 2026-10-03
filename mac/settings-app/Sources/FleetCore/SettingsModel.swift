import Combine
import Foundation

public enum FleetError: LocalizedError {
    case message(String)
    public var errorDescription: String? {
        switch self { case .message(let message): return message }
    }
}

public struct FleetSettings: Codable, Equatable, Sendable {
    public var schema: Int
    public var origin: String
    public var autoStart: Bool
    enum CodingKeys: String, CodingKey { case schema, origin; case autoStart = "auto_start" }

    public static func validatedOrigin(_ draft: String) throws -> String {
        let value = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard var components = URLComponents(string: value),
              components.scheme == "https", let host = components.host, !host.isEmpty,
              components.user == nil, components.password == nil,
              components.query == nil, components.fragment == nil,
              components.path.isEmpty || components.path == "/",
              components.port == nil || (1...65535).contains(components.port!) else {
            throw FleetError.message("请输入 HTTPS 服务网页地址，不含路径、账号或查询参数。")
        }
        components.path = ""
        guard let normalized = components.url?.absoluteString else {
            throw FleetError.message("服务网页地址无效。")
        }
        return normalized
    }
}

public enum DiskState: String, Sendable {
    case unknown, restricted, verified
    public var label: String {
        switch self {
        case .unknown: return "待验证"
        case .restricted: return "访问受限"
        case .verified: return "受保护路径访问已验证"
        }
    }
}

public struct DiskAccess: Codable, Equatable, Sendable {
    public var state: String
    public var source: String
    public var checkedAt: Int64
    public var verifiedTargets: Int
    enum CodingKeys: String, CodingKey {
        case state, source
        case checkedAt = "checked_at"
        case verifiedTargets = "verified_targets"
    }
    public var verifiedState: DiskState {
        guard source == "background", checkedAt > 0 else { return .unknown }
        if state == "restricted" { return .restricted }
        if state == "verified", verifiedTargets > 0 { return .verified }
        return .unknown
    }
}

public struct BindingStatus: Codable, Equatable, Sendable {
    public var deviceID: String
    public var ownerEmail: String
    public var origin: String
    public var complete: Bool
    public var locked: Bool
    enum CodingKeys: String, CodingKey {
        case origin, complete, locked
        case deviceID = "device_id"
        case ownerEmail = "owner_email"
    }
}

public struct AgentStatus: Codable, Equatable, Sendable {
    public var schema: Int
    public var pid: Int
    public var version: String
    public var settings: FleetSettings
    public var binding: BindingStatus?
    public var diskAccess: DiskAccess
    public var pairing: PairingState? = nil
    public var runtime: RuntimeState? = nil
    enum CodingKeys: String, CodingKey { case schema, pid, version, settings, binding, pairing, runtime; case diskAccess = "disk_access" }
}

public struct RuntimeState: Codable, Equatable, Sendable {
    public var phase: String
    public var error: String?
}

public struct PairingState: Codable, Equatable, Sendable {
    public var phase: String
    public var attempt: String
    public var origin: String
    public var url: String?
    public var code: String?
    public var deviceID: String?
    public var ownerEmail: String?
    public var error: String?
    enum CodingKeys: String, CodingKey {
        case phase, attempt, origin, url, code, error
        case deviceID = "device_id"
        case ownerEmail = "owner_email"
    }
    public var verificationURL: URL? {
        guard let value = url, let link = URLComponents(string: value), let server = URLComponents(string: origin),
              link.scheme == server.scheme, link.host == server.host, link.port == server.port,
              link.user == nil, link.password == nil, link.path == "/enroll/confirm",
              link.fragment == nil else { return nil }
        return link.url
    }
    public var isActive: Bool { ["starting", "browser", "awaiting_confirmation", "joining"].contains(phase) }
    public var label: String {
        switch phase {
        case "starting": return "正在发起设备关联…"
        case "browser": return "请在浏览器登录并确认关联"
        case "awaiting_confirmation": return "请核对账号与设备，确认接入"
        case "joining": return "正在接入设备网络…"
        case "complete": return "设备关联已完成"
        case "cancelled": return "关联已取消"
        case "failed": return error ?? "关联失败，请重试"
        default: return "尚未发起设备关联"
        }
    }
}

@MainActor
public protocol LocalManagement {
    func status() async throws -> AgentStatus
    func save(_ settings: FleetSettings) async throws -> FleetSettings
    func recheckDisk() async throws -> DiskAccess
    func pairing(_ action: String, state: PairingState?) async throws -> PairingState
}

@MainActor
public final class SettingsModel: ObservableObject {
    @Published public var origin = ""
    @Published public var autoStart = true
    @Published public private(set) var status: AgentStatus?
    @Published public private(set) var error = ""
    @Published public private(set) var isBusy = false
    private let client: any LocalManagement
    private var initialized = false

    public init(client: any LocalManagement) { self.client = client }
    public var diskState: DiskState { status?.diskAccess.verifiedState ?? .unknown }

    public func refresh() async {
        guard !isBusy else { return }
        do {
            let current = try await client.status()
            guard current.schema == 1, current.settings.schema == 1, current.pid > 0 else {
                throw FleetError.message("后台协议版本不受支持。")
            }
            status = current
            if !initialized {
                if origin.isEmpty { origin = current.settings.origin }
                autoStart = current.settings.autoStart
                initialized = true
            }
            error = ""
        } catch {
            status = nil
            self.error = error.localizedDescription
        }
    }

    public func save() async {
        guard !isBusy else { return }
        isBusy = true
        defer { isBusy = false }
        do {
            let settings = FleetSettings(schema: 1, origin: try FleetSettings.validatedOrigin(origin), autoStart: autoStart)
            let saved = try await client.save(settings)
            status?.settings = saved
            origin = saved.origin
            autoStart = saved.autoStart
            error = ""
        } catch {
            self.error = error.localizedDescription
        }
    }

    public func recheckDisk() async {
        guard !isBusy else { return }
        isBusy = true
        defer { isBusy = false }
        do {
            let evidence = try await client.recheckDisk()
            guard status != nil else { throw FleetError.message("后台尚未运行，无法验证权限。") }
            status?.diskAccess = evidence
            error = ""
        } catch {
            status?.diskAccess = DiskAccess(state: "unknown", source: "background", checkedAt: 0, verifiedTargets: 0)
            self.error = error.localizedDescription
        }
    }

    public func beginPairing() async { await changePairing("pair-start", state: nil) }
    public func confirmPairing() async {
        guard let state = status?.pairing, state.phase == "awaiting_confirmation" else { return }
        await changePairing("pair-confirm", state: state)
    }
    public func cancelPairing() async { await changePairing("pair-cancel", state: nil) }
    private func changePairing(_ action: String, state: PairingState?) async {
        guard !isBusy else { return }
        isBusy = true
        defer { isBusy = false }
        do { status?.pairing = try await client.pairing(action, state: state); error = "" }
        catch { self.error = error.localizedDescription }
    }
}
