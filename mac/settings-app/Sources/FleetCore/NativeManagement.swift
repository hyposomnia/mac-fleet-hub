import Foundation
import ServiceManagement

@MainActor
public final class NativeManagement: LocalManagement {
    public let layout: RuntimeLayout
    private let loginService = SMAppService.agent(plistName: "com.macfleet.desktop-login.plist")
    public init(layout: RuntimeLayout) { self.layout = layout }

    public var autoStartStatus: String {
        switch loginService.status {
        case .enabled: return "已获系统许可，登录后启动"
        case .requiresApproval: return "等待系统批准"
        case .notRegistered: return "未启用登录后启动"
        case .notFound: return "登录启动组件尚未安装"
        @unknown default: return "系统状态未确认"
        }
    }

    public func status() async throws -> AgentStatus {
        let data = try await request("status")
        return try JSONDecoder().decode(AgentStatus.self, from: data)
    }

    public func save(_ settings: FleetSettings) async throws -> FleetSettings {
        let saved = try JSONDecoder().decode(FleetSettings.self, from: await request("settings", input: JSONEncoder().encode(settings)))
        return saved
    }

    public func recheckDisk() async throws -> DiskAccess {
        try JSONDecoder().decode(DiskAccess.self, from: await request("disk-recheck"))
    }

    public func pairing(_ action: String, state: PairingState?) async throws -> PairingState {
        guard ["pair-start", "pair-confirm", "pair-cancel"].contains(action) else { throw FleetError.message("无效关联操作。") }
        let input = try state.map { try JSONEncoder().encode($0) } ?? Data("{}".utf8)
        return try JSONDecoder().decode(PairingState.self, from: await request(action, input: input))
    }

    public func prepareForStop() async throws { _ = try await request("prepare-stop") }
    public func resumeAfterCancelledStop() async { _ = try? await request("resume") }

    public func stop() async throws {
        try await prepareForStop()
        do { _ = try await launchctl(["bootout", target]) }
        catch { await resumeAfterCancelledStop(); throw error }
    }

    public func restart() async throws {
        let previous = try await status()
        try await prepareForStop()
        do {
            _ = try await launchctl(["kickstart", "-k", target])
            for _ in 0..<50 {
                if let current = try? await status(), current.pid != previous.pid { return }
                try await Task.sleep(nanoseconds: 200_000_000)
            }
            throw FleetError.message("已请求重启，但尚未确认新的后台进程。")
        } catch { await resumeAfterCancelledStop(); throw error }
    }

    public func logout() async throws {
        try await prepareForStop()
        do {
            _ = try await request("logout")
            _ = try await launchctl(["kickstart", "-k", target])
        } catch { await resumeAfterCancelledStop(); throw error }
    }

    public func install() async throws -> URL {
        let destination = URL(fileURLWithPath: "/Applications/Fleet Hub.app")
        guard layout.requiresInstallation else { return destination }
        try await ApplicationInstaller.install(source: layout.application, destination: destination) { candidate in
            _ = try await CommandRunner.run(executable: URL(fileURLWithPath: "/usr/sbin/spctl"), arguments: ["--assess", "--type", "execute", candidate.path], timeout: 30)
            _ = try await CommandRunner.run(executable: URL(fileURLWithPath: "/usr/bin/codesign"), arguments: ["--verify", "--deep", "--strict", candidate.path], timeout: 30)
        }
        return destination
    }

    public func uninstall(removeSettings: Bool) async throws {
        guard !layout.requiresInstallation else { throw FleetError.message("当前应用不在安装位置。") }
        let current = try await UninstallPreparation.prepare(status: { try await self.status() },
                                                             start: { try await self.start() },
                                                             checkIdle: { try await self.prepareForStop() })
        do {
            if current.binding != nil { _ = try await request("logout") }
            try setAutoStart(false)
            _ = try await launchctl(["bootout", target])
            try FileManager.default.trashItem(at: layout.application, resultingItemURL: nil)
            if removeSettings {
                let expected = layout.home.appendingPathComponent(".macfleet/desktop")
                guard layout.state.standardizedFileURL == expected.standardizedFileURL else { throw FleetError.message("卸载路径无效。") }
                try PrivateRuntime.ensureDirectory(expected)
                try FileManager.default.removeItem(at: expected)
            }
        } catch { await resumeAfterCancelledStop(); throw error }
    }

    public func start() async throws {
        guard !layout.requiresInstallation else {
            throw FleetError.message("请先安装到应用程序，不从磁盘映像启动后台。")
        }
        _ = try await CommandRunner.run(executable: URL(fileURLWithPath: "/usr/sbin/spctl"), arguments: ["--assess", "--type", "execute", layout.application.path])
        _ = try await CommandRunner.run(executable: URL(fileURLWithPath: "/usr/bin/codesign"), arguments: ["--verify", "--deep", "--strict", layout.application.path])
        if (try? await launchctl(["print", "gui/\(getuid())/com.macfleet.fleet-agent"])) != nil {
            throw FleetError.message("检测到已有 Fleet 后台服务；本次安装不会覆盖或停止它。")
        }
        try PrivateRuntime.ensureDirectory(layout.home.appendingPathComponent(".macfleet"))
        try PrivateRuntime.ensureDirectory(layout.state)
        try PrivateRuntime.write(layout.launchDefinition(), to: layout.runtimePlist)
        if (try? await launchctl(["print", target])) == nil {
            _ = try await launchctl(["bootstrap", domain, layout.runtimePlist.path])
        }
        for _ in 0..<30 {
            if (try? await status()) != nil { return }
            try await Task.sleep(nanoseconds: 200_000_000)
        }
        throw FleetError.message("后台已请求启动，但健康检查尚未通过。")
    }

    public func setAutoStart(_ enabled: Bool) throws {
        guard !layout.requiresInstallation, FileManager.default.fileExists(atPath: layout.runtimePlist.path) else {
            throw FleetError.message("请先完成应用安装与后台启动，再设置登录后自动运行。")
        }
        if enabled {
            if loginService.status != .enabled { try loginService.register() }
        } else if loginService.status != .notRegistered {
            try loginService.unregister()
        }
    }

    public func openBackgroundSettings() { SMAppService.openSystemSettingsLoginItems() }

    private var domain: String { "gui/\(getuid())" }
    private var target: String { domain + "/com.macfleet.desktop-agent" }
    private func launchctl(_ arguments: [String]) async throws -> Data {
        try await CommandRunner.run(executable: URL(fileURLWithPath: "/bin/launchctl"), arguments: arguments)
    }
    private func request(_ action: String, input: Data? = nil) async throws -> Data {
        try await CommandRunner.run(executable: layout.agent, arguments: ["desktop", action], input: input, timeout: 28)
    }
}
