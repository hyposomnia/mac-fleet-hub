import Foundation

@MainActor
public protocol TerminalSetupManagement: LocalManagement {
    func start(replaceLegacy: Bool) async throws
    func setAutoStart(_ enabled: Bool) async throws
}

extension NativeManagement: TerminalSetupManagement {}

public enum TerminalSetupResult: Equatable {
    case complete, needsDiskAccess
}

@MainActor
public final class TerminalSetup {
    private let client: any TerminalSetupManagement
    private let openBrowser: (URL) -> Bool
    private let sleep: () async throws -> Void
    private let timeout: TimeInterval

    public init(client: any TerminalSetupManagement, openBrowser: @escaping (URL) -> Bool,
                timeout: TimeInterval = 720, sleep: @escaping () async throws -> Void = { try await Task.sleep(nanoseconds: 1_000_000_000) }) {
        self.client = client
        self.openBrowser = openBrowser
        self.sleep = sleep
        self.timeout = timeout
    }

    public func run(origin: String, progress: (String) -> Void = { _ in }) async throws -> TerminalSetupResult {
        let origin = try FleetSettings.validatedOrigin(origin)
        progress("正在准备 Fleet Agent…")
        try await client.start(replaceLegacy: true)
        var current = try await client.status()
        try validate(current, origin: origin)
        if current.binding == nil, current.pairing?.needsCleanup == true {
            throw FleetError.message("存在未完成的关联，请先在 Fleet Agent Settings 中解除关联。")
        }
        if current.binding == nil, current.pairing?.isActive == true {
            throw FleetError.message("已有进行中的网页授权，请先完成或在 Fleet Agent Settings 中取消。")
        }
        var settings = current.settings
        settings.origin = origin
        _ = try await client.save(settings)
        try await client.setAutoStart(settings.autoStart)
        var attempt: String?
        var opened = false
        if current.binding == nil {
            let pairing = try await client.pairing("pair-start", state: nil)
            guard !pairing.attempt.isEmpty else { throw FleetError.message("后台未返回有效授权请求。") }
            attempt = pairing.attempt
            current.pairing = pairing
            progress("请在浏览器中登录并授权此设备。")
        }
        let deadline = Date().addingTimeInterval(timeout)
        while true {
            try Task.checkCancellation()
            try validate(current, origin: origin)
            if let attempt {
                guard let pairing = current.pairing, pairing.attempt == attempt, pairing.origin == origin else {
                    throw FleetError.message("网页授权请求已变化，请重新运行安装命令。")
                }
                if ["failed", "cancelled"].contains(pairing.phase) {
                    throw FleetError.message(pairing.error ?? "网页授权已取消，请重新运行安装命令。")
                }
                if pairing.phase == "browser", !opened {
                    guard let url = pairing.verificationURL else { throw FleetError.message("后台返回的网页授权地址无效。") }
                    guard openBrowser(url) else { throw FleetError.message("无法打开浏览器，请在 Fleet Agent Settings 中继续网页授权。") }
                    opened = true
                }
            }
            if current.binding?.complete == true, current.runtime?.phase == "running" { break }
            if current.runtime?.phase == "failed" {
                throw FleetError.message(current.runtime?.error ?? "Fleet Agent 连接失败，请在设置中重试。")
            }
            guard Date() < deadline else { throw FleetError.message("等待设备接入超时，请在 Fleet Agent Settings 中检查或重新运行安装命令。") }
            try await sleep()
            current = try await client.status()
        }
        progress("设备已连接，正在检查磁盘访问…")
        return try await client.recheckDisk().verifiedState == .verified ? .complete : .needsDiskAccess
    }

    private func validate(_ status: AgentStatus, origin: String) throws {
        guard status.schema == 1, status.settings.schema == 1, status.pid > 0 else {
            throw FleetError.message("后台协议版本不受支持。")
        }
        if let binding = status.binding {
            guard binding.origin == origin, binding.complete, !binding.locked else {
                throw FleetError.message("此设备已有其他服务或未完成的关联，请先在 Fleet Agent Settings 中解除关联。")
            }
        }
    }
}
