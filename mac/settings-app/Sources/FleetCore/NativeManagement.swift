import Foundation
import ServiceManagement

@MainActor
public final class NativeManagement: LocalManagement {
    public let layout: RuntimeLayout
    private let loginService = SMAppService.agent(plistName: "com.macfleet.desktop-login.plist")
    private let configureAutoStart: ((Bool) throws -> Void)?
    private let controlsRegisteredLoginService: Bool
    private let execute: (URL, [String], Data?, TimeInterval) async throws -> Data
    public init(layout: RuntimeLayout, controlsRegisteredLoginService: Bool = false, configureAutoStart: ((Bool) throws -> Void)? = nil, execute: @escaping (URL, [String], Data?, TimeInterval) async throws -> Data = {
        try await CommandRunner.run(executable: $0, arguments: $1, input: $2, timeout: $3)
    }) {
        self.layout = layout
        self.configureAutoStart = configureAutoStart
        self.controlsRegisteredLoginService = controlsRegisteredLoginService
        self.execute = execute
    }

    public func status() async throws -> AgentStatus {
        let data = try await request("status")
        return try JSONDecoder().decode(AgentStatus.self, from: data)
    }

    public func save(_ settings: FleetSettings) async throws -> FleetSettings {
        let previous = try await status().settings
        let saved = try JSONDecoder().decode(FleetSettings.self, from: await request("settings", input: JSONEncoder().encode(settings)))
        if previous.autoStart != saved.autoStart {
            do {
                if let configureAutoStart { try configureAutoStart(saved.autoStart) }
                else { try await setAutoStart(saved.autoStart) }
            } catch {
                let failure = error
                do { _ = try await request("settings", input: JSONEncoder().encode(previous)) }
                catch { throw FleetError.message("自动启动设置失败，后台设置回滚失败：\(error.localizedDescription)") }
                throw failure
            }
        }
        return saved
    }

    public func recheckDisk() async throws -> DiskAccess {
        try JSONDecoder().decode(DiskAccess.self, from: await request("disk-recheck"))
    }

    public func pairing(_ action: String, state: PairingState?) async throws -> PairingState {
        guard ["pair-start", "pair-confirm", "pair-cancel"].contains(action) else { throw FleetError.message("无效关联操作。") }
        if action == "pair-start" { try await start() }
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
        try await start()
        if try await status().pid != previous.pid { return }
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

    public func installedApplication() async throws -> URL {
        let destination = URL(fileURLWithPath: "/Applications/Fleet Hub.app")
        _ = try BackgroundIdentity.read(destination, identifier: "com.macfleet.fleet-hub")
        _ = try await execute(URL(fileURLWithPath: "/usr/sbin/spctl"), ["--assess", "--type", "execute", destination.path], nil, 30)
        _ = try await execute(URL(fileURLWithPath: "/usr/bin/codesign"), ["--verify", "--deep", "--strict", destination.path], nil, 30)
        return destination
    }

    public func uninstall(removeSettings: Bool) async throws {
        guard !layout.requiresInstallation else { throw FleetError.message("当前应用不在安装位置。") }
        let sharedRuntime = CodexSharedRuntime(layout: layout, execute: execute)
        try await sharedRuntime.prepareForRemoval()
        let current = try await UninstallPreparation.prepare(status: { try await self.status() },
                                                             start: { try await self.start() },
                                                             checkIdle: { try await self.prepareForStop() })
        do {
            try await sharedRuntime.remove()
            if current.binding != nil { _ = try await request("logout") }
            try await setAutoStart(false)
            _ = try await launchctl(["bootout", target])
            try layout.removeRuntime()
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
        try await validateInstallation()
        try await synchronizeBackground(launch: true)
        try await ensureCodexSharedRuntime()
    }

    public func prepareRuntime() async throws {
        try await validateInstallation()
        let running = (try? await launchctl(["print", target])) != nil
        try await synchronizeBackground(launch: running)
        if running { try await ensureCodexSharedRuntime() }
        if loginService.status == .enabled {
            let saved = try JSONDecoder().decode(FleetSettings.self, from: PrivateRuntime.read(layout.state.appendingPathComponent("settings.json")))
            try await setAutoStart(saved.autoStart)
        }
    }

    private func validateInstallation() async throws {
        guard !layout.requiresInstallation else {
            throw FleetError.message("请先安装到应用程序，不从磁盘映像启动后台。")
        }
        if (try? await launchctl(["print", "gui/\(getuid())/com.macfleet.fleet-agent"])) != nil {
            throw FleetError.message("检测到已有 Fleet 后台服务；本次安装不会覆盖或停止它。")
        }
        try PrivateRuntime.ensureDirectory(layout.home.appendingPathComponent(".macfleet"))
        try PrivateRuntime.ensureDirectory(layout.state)
    }

    private func ensureCodexSharedRuntime() async throws {
        try await BackgroundRuntimeInstaller.withLock(directory: layout.runtimeDirectory) {
            try await CodexSharedRuntime(layout: layout, execute: execute).start()
        }
    }

    func synchronizeBackground(launch: Bool) async throws {
        let expected = try BackgroundIdentity.read(layout.application, identifier: "com.macfleet.fleet-hub")
        let verify = try await BackgroundRuntimeVerifier.verifier(host: layout.application, execute: execute)
        try await verify(layout.bundledBackgroundApplication)
        try BackgroundRuntimeInstaller.validateInstalled(layout.backgroundApplication)
        let definition = try layout.backgroundLaunchDefinition()
        let previousDefinition = FileManager.default.fileExists(atPath: layout.runtimePlist.path) ? try PrivateRuntime.read(layout.runtimePlist) : nil
        let running = (try? await launchctl(["print", target])) != nil
        let previousStatus = running ? try await status() : nil
        if (try? await verify(layout.backgroundApplication)) != nil, previousDefinition == definition,
           !running || previousStatus?.version == expected.agentVersion {
            if running || !launch { return }
        }
        guard !running || previousDefinition != nil else {
            throw FleetError.message("现有后台缺少安全启动定义，未停止或覆盖它。")
        }
        var stoppedPrevious = false
        var startedNew = false
        var definitionWritten = false
        var touchedLifecycle = false
        try await BackgroundRuntimeInstaller.deploy(source: layout.bundledBackgroundApplication, destination: layout.backgroundApplication,
            verify: verify, prepare: {
                let currentDefinition = FileManager.default.fileExists(atPath: self.layout.runtimePlist.path) ? try PrivateRuntime.read(self.layout.runtimePlist) : nil
                let currentRunning = (try? await self.launchctl(["print", self.target])) != nil
                guard currentDefinition == previousDefinition, currentRunning == running else {
                    throw FleetError.message("后台状态已变化，未替换它，请重试。")
                }
                if running {
                    guard try await self.status().pid == previousStatus?.pid else { throw FleetError.message("后台进程已变化，未停止它，请重试。") }
                    touchedLifecycle = true
                    try await self.prepareForStop()
                    _ = try await self.launchctl(["bootout", self.target])
                    stoppedPrevious = true
                }
            }, activate: {
                try PrivateRuntime.write(definition, to: self.layout.runtimePlist)
                definitionWritten = true
                if launch {
                    _ = try await self.launchctl(["bootstrap", self.domain, self.layout.runtimePlist.path])
                    startedNew = true
                    try await self.waitForRuntime(expected: expected, replacingPID: previousStatus?.pid)
                }
            }, quiesce: {
                if startedNew {
                    try await self.prepareForStop()
                    _ = try await self.launchctl(["bootout", self.target])
                    startedNew = false
                }
            }, restore: {
                if definitionWritten || stoppedPrevious {
                    if let previousDefinition { try PrivateRuntime.write(previousDefinition, to: self.layout.runtimePlist) }
                    else if FileManager.default.fileExists(atPath: self.layout.runtimePlist.path) { try FileManager.default.removeItem(at: self.layout.runtimePlist) }
                }
                if stoppedPrevious {
                    _ = try await self.launchctl(["bootstrap", self.domain, self.layout.runtimePlist.path])
                    guard let previousStatus else { throw FleetError.message("缺少原后台状态。") }
                    try await self.waitForRuntime(expectedVersion: previousStatus.version, replacingPID: previousStatus.pid)
                } else if touchedLifecycle { await self.resumeAfterCancelledStop() }
            })
    }

    private func waitForRuntime(expected: BackgroundIdentity, replacingPID: Int?) async throws {
        try await waitForRuntime(expectedVersion: expected.agentVersion, replacingPID: replacingPID)
    }

    private func waitForRuntime(expectedVersion: String, replacingPID: Int?) async throws {
        for _ in 0..<150 {
            if let current = try? await status() {
                if current.pid == replacingPID {
                    try await Task.sleep(nanoseconds: 200_000_000)
                    continue
                }
                guard current.schema == 1, current.settings.schema == 1, current.version == expectedVersion else {
                    throw FleetError.message("新后台协议或版本不匹配，恢复原后台。")
                }
                if current.pid > 0, current.isReadyForManagement { return }
                if current.runtime?.phase == "failed" { throw FleetError.message(current.runtime?.error ?? "后台服务启动失败。") }
            }
            try await Task.sleep(nanoseconds: 200_000_000)
        }
        throw FleetError.message("独立后台未通过健康检查，恢复原后台。")
    }

    public func setAutoStart(_ enabled: Bool) async throws {
        guard !layout.requiresInstallation, FileManager.default.fileExists(atPath: layout.runtimePlist.path) else {
            throw FleetError.message("请先完成应用安装与后台启动，再设置登录后自动运行。")
        }
        if controlsRegisteredLoginService {
            // The debug UI cannot use SMAppService for another host bundle. Control
            // only the already registered, signed Hub helper through launchd.
            let definition = try await launchctl(["print", loginTarget])
            guard String(decoding: definition, as: UTF8.self).contains("parent bundle identifier = com.macfleet.fleet-hub") else {
                throw FleetError.message("登录启动项不属于已安装的 Fleet Hub。")
            }
            let previous = try await registeredLoginServiceDisabled()
            _ = try await launchctl([enabled ? "enable" : "disable", loginTarget])
            do {
                guard try await registeredLoginServiceDisabled() == !enabled else {
                    throw FleetError.message("登录启动设置未生效。")
                }
            } catch {
                let failure = error
                do { _ = try await launchctl([previous ? "disable" : "enable", loginTarget]) }
                catch { throw FleetError.message("登录启动设置失败，系统状态回滚失败：\(error.localizedDescription)") }
                throw failure
            }
            return
        }
        let marker = layout.state.appendingPathComponent("login-helper-version")
        if enabled {
            if loginService.status == .enabled, (try? PrivateRuntime.read(marker)) != Data("2".utf8) {
                try await loginService.unregister()
            }
            if loginService.status != .enabled { try loginService.register() }
            guard loginService.status == .enabled else { throw FleetError.message("系统尚未允许登录启动，设置未生效。") }
            try PrivateRuntime.write(Data("2".utf8), to: marker)
        } else if loginService.status != .notRegistered {
            try await loginService.unregister()
        }
        // A preview may have disabled this same registered helper. Remove that
        // override so the host's SMAppService registration stays authoritative.
        _ = try await launchctl(["enable", loginTarget])
    }

    private var loginTarget: String { domain + "/com.macfleet.desktop-login" }
    private func registeredLoginServiceDisabled() async throws -> Bool {
        let output = String(decoding: try await launchctl(["print-disabled", domain]), as: UTF8.self)
        for line in output.components(separatedBy: .newlines) where line.trimmingCharacters(in: .whitespaces).hasPrefix("\"com.macfleet.desktop-login\"") {
            switch line.components(separatedBy: "=>").last?.trimmingCharacters(in: .whitespaces) {
            case "disabled", "true": return true
            case "enabled", "false": return false
            default: throw FleetError.message("无法确认登录启动设置。")
            }
        }
        return false
    }

    public func openBackgroundSettings() { SMAppService.openSystemSettingsLoginItems() }

    private var domain: String { "gui/\(getuid())" }
    private var target: String { domain + "/com.macfleet.desktop-agent" }
    private func launchctl(_ arguments: [String]) async throws -> Data {
        try await execute(URL(fileURLWithPath: "/bin/launchctl"), arguments, nil, 8)
    }
    private func request(_ action: String, input: Data? = nil) async throws -> Data {
        try await execute(layout.bundledAgent, ["desktop", action], input, 28)
    }
}
