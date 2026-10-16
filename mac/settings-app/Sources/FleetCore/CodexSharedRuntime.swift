import Darwin
import Foundation

/// Installs the common Desktop CLI hook. Desktop owns the runtime lifecycle;
/// starting or updating Fleet never creates an app-server process here.
@MainActor
public final class CodexSharedRuntime {
    private let layout: RuntimeLayout
    private let execute: (URL, [String], Data?, TimeInterval) async throws -> Data
    private var domain: String { "gui/\(getuid())" }
    private var legacyTarget: String { domain + "/com.macfleet.codex-app-server" }
    private var legacyDefinition: URL { layout.state.appendingPathComponent("codex-app-server.plist") }
    private var legacySnapshot: URL { layout.state.appendingPathComponent("codex-desktop-environment.json") }
    private var marker: URL { layout.state.appendingPathComponent("codex-hook-install.json") }
    private var hook: URL { layout.home.appendingPathComponent(".local/lib/macfleet") }
    private var config: URL { hook.appendingPathComponent("codex-desktop.json") }

    public init(layout: RuntimeLayout, execute: @escaping (URL, [String], Data?, TimeInterval) async throws -> Data = {
        try await CommandRunner.run(executable: $0, arguments: $1, input: $2, timeout: $3)
    }) {
        self.layout = layout
        self.execute = execute
    }

    public func start() async throws {
        let node: String
        do { node = try await shell("codex-bin-resolve.sh", ["--keeper-node"]).trimmingCharacters(in: .whitespacesAndNewlines) }
        catch {
            FileHandle.standardError.write(Data("Codex Desktop hook 不可用：\(error.localizedDescription)\n".utf8))
            return
        }
        guard node.hasPrefix("/"), !node.contains("\n") else { throw FleetError.message("Codex Node 路径无效。") }
        let oldDefinition = FileManager.default.fileExists(atPath: legacyDefinition.path) ? try PrivateRuntime.read(legacyDefinition) : nil
        let loaded = try? await launchctl(["print", legacyTarget])
        if let loaded {
            guard oldDefinition != nil else { throw FleetError.message("Codex 运行时由其他安装管理，请先通过原安装器迁移。") }
            try validateOwner(loaded, file: legacyDefinition)
        }
        let owned = try ownsHook()
        let oldMarker = owned ? try PrivateRuntime.read(marker) : nil
        var previousJobs: [URL: Data] = [:]
        var previouslyLoaded = Set<String>()
        for scope in ["gui", "user"] {
            let file = jobFile(scope)
            if FileManager.default.fileExists(atPath: file.path) { previousJobs[file] = try PrivateRuntime.read(file) }
            if let existing = try? await launchctl(["print", jobTarget(scope)]) { try validateOwner(existing, file: file); previouslyLoaded.insert(scope) }
        }
        _ = try await installer(node, "check")
        if loaded != nil || oldDefinition != nil {
            _ = try await shell("check-codex-idle.sh")
            // Even an identical hook cannot retire a legacy server under Desktop.
            if FileManager.default.fileExists(atPath: config.path) { _ = try await installer(node, "check-removal") }
        }
        var retired = false, staged = false, environmentTouched = false
        do {
            if loaded != nil {
                _ = try await launchctl(["bootout", legacyTarget])
                retired = true
            }
            let result = try await installer(node, "install").trimmingCharacters(in: .whitespacesAndNewlines)
            guard ["current", "staged"].contains(result) else { throw FleetError.message("Codex hook 安装器返回无效状态。") }
            staged = result == "staged"
            // A common hook owned by the shell installer is reused without adopting it.
            if !staged && !owned && oldDefinition == nil { return }
            environmentTouched = true
            let prior = oldDefinition != nil && FileManager.default.fileExists(atPath: legacySnapshot.path) ? legacySnapshot : nil
            for scope in ["user", "gui"] { try await environmentJob("shared", scope: scope, persistent: true, legacy: prior) }
            try PrivateRuntime.write(JSONEncoder().encode(Ownership(schema: 1, directory: hook.path)), to: marker)
            if let oldDefinition {
                try PrivateRuntime.write(oldDefinition, to: layout.state.appendingPathComponent("codex-app-server.legacy-\(UUID().uuidString).plist"))
                try FileManager.default.removeItem(at: legacyDefinition)
            }
            _ = try await installer(node, "commit")
        } catch {
            let failure = error
            do {
                if environmentTouched {
                    for scope in ["gui", "user"] { try await environmentJob("rollback", scope: scope, persistent: false) }
                }
                if environmentTouched {
                    for scope in ["gui", "user"] { try await removeJob(scope) }
                }
                if staged { _ = try await installer(node, "rollback") }
                if let oldMarker { try PrivateRuntime.write(oldMarker, to: marker) }
                else if FileManager.default.fileExists(atPath: marker.path) { try FileManager.default.removeItem(at: marker) }
                for (file, data) in previousJobs where environmentTouched {
                    try PrivateRuntime.write(data, to: file)
                    let scope = file == jobFile("gui") ? "gui" : "user"
                    if previouslyLoaded.contains(scope) { _ = try await launchctl(["bootstrap", scope == "gui" ? domain : "user/\(getuid())", file.path]) }
                }
                if let oldDefinition { try PrivateRuntime.write(oldDefinition, to: legacyDefinition) }
                if retired { _ = try await launchctl(["bootstrap", domain, legacyDefinition.path]) }
            } catch {
                throw FleetError.message("Codex hook 安装失败且回滚未完成：\(failure.localizedDescription)；\(error.localizedDescription)")
            }
            throw failure
        }
    }

    public func prepareForRemoval() async throws {
        guard try ownsHook() else {
            if let loaded = try? await launchctl(["print", legacyTarget]) {
                try validateOwner(loaded, file: legacyDefinition)
                let node = try await shell("codex-bin-resolve.sh", ["--keeper-node"]).trimmingCharacters(in: .whitespacesAndNewlines)
                _ = try await installer(node, "check")
                _ = try await shell("check-codex-idle.sh")
            }
            return
        }
        let node = try await shell("codex-bin-resolve.sh", ["--keeper-node"]).trimmingCharacters(in: .whitespacesAndNewlines)
        _ = try await installer(node, "check-removal")
        for scope in ["gui", "user"] {
            if let loaded = try? await launchctl(["print", jobTarget(scope)]) { try validateOwner(loaded, file: jobFile(scope)) }
        }
    }

    public func remove() async throws {
        if try !ownsHook() {
            guard FileManager.default.fileExists(atPath: legacyDefinition.path) else { return }
            try await start()
            guard try ownsHook() else { throw FleetError.message("无法恢复旧 Codex GUI 配置，保留安装以便重试。") }
        }
        try await prepareForRemoval()
        let node = try await shell("codex-bin-resolve.sh", ["--keeper-node"]).trimmingCharacters(in: .whitespacesAndNewlines)
        for scope in ["gui", "user"] {
            try await environmentJob("clear", scope: scope, persistent: false)
            try await removeJob(scope)
        }
        _ = try await installer(node, "remove")
        try FileManager.default.removeItem(at: marker)
    }

    private struct Ownership: Codable { let schema: Int; let directory: String }
    private func ownsHook() throws -> Bool {
        guard FileManager.default.fileExists(atPath: marker.path) else { return false }
        let value = try JSONDecoder().decode(Ownership.self, from: PrivateRuntime.read(marker))
        guard value.schema == 1, value.directory == hook.path else { throw FleetError.message("Codex hook 所有权记录无效。") }
        return true
    }
    private func installer(_ node: String, _ command: String) async throws -> String {
        let home = try layout.savedEnvironment()["FLEET_CODEX_HOME"] ?? layout.home.appendingPathComponent(".codex").path
        let arguments = [layout.codexResources.appendingPathComponent("codex-desktop-install.mjs").path, command, hook.path,
                         "/Applications/ChatGPT.app", "ws://127.0.0.1:47682", layout.home.appendingPathComponent(".macfleet/codex-app-server.sock").path,
                         home, layout.home.appendingPathComponent(".macfleet/codex-runtime").path]
        let data = try await execute(URL(fileURLWithPath: node), arguments, nil, 35)
        return String(data: data, encoding: .utf8) ?? ""
    }
    private func jobFile(_ scope: String) -> URL { layout.state.appendingPathComponent("codex-desktop-env\(scope == "gui" ? "" : "-user").plist") }
    private func jobLabel(_ scope: String) -> String { "com.macfleet.codex-desktop-env\(scope == "gui" ? "" : "-user")" }
    private func jobTarget(_ scope: String) -> String { (scope == "gui" ? domain : "user/\(getuid())") + "/" + jobLabel(scope) }
    private func validateOwner(_ loaded: Data, file: URL) throws {
        _ = try PrivateRuntime.read(file)
        guard String(data: loaded, encoding: .utf8)?.contains("path = " + file.path + "\n") == true else {
            throw FleetError.message("Codex 启动定义由其他安装管理，未修改或停止它。")
        }
    }
    private func removeJob(_ scope: String) async throws {
        let file = jobFile(scope)
        if let loaded = try? await launchctl(["print", jobTarget(scope)]) {
            try validateOwner(loaded, file: file)
            _ = try await launchctl(["bootout", jobTarget(scope)])
        }
        if FileManager.default.fileExists(atPath: file.path) { _ = try PrivateRuntime.read(file); try FileManager.default.removeItem(at: file) }
    }
    private func environmentJob(_ mode: String, scope: String, persistent: Bool, legacy: URL? = nil) async throws {
        let bootstrap = scope == "gui" ? domain : "user/\(getuid())"
        let label = persistent ? jobLabel(scope) : "com.macfleet.codex-env-operation-" + UUID().uuidString
        let file = persistent ? jobFile(scope) : layout.state.appendingPathComponent(label + ".plist")
        let target = bootstrap + "/" + label
        if persistent, let loaded = try? await launchctl(["print", target]) {
            try validateOwner(loaded, file: file)
            _ = try await launchctl(["bootout", target])
        }
        let data = try Data(contentsOf: layout.codexResources.appendingPathComponent("com.macfleet.codex-desktop-env.plist"))
        guard var definition = try PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any] else { throw FleetError.message("Codex GUI 模板无效。") }
        definition["Label"] = label
        definition["LimitLoadToSessionType"] = scope == "gui" ? "Aqua" : "Background"
        definition["ProgramArguments"] = ["/bin/bash", hook.appendingPathComponent("codex-desktop-env.sh").path, mode, config.path] + (legacy.map { [$0.path] } ?? [])
        definition["EnvironmentVariables"] = ["HOME": layout.home.path]
        try PrivateRuntime.ensureDirectory(layout.state.appendingPathComponent("codex-logs"))
        definition["StandardOutPath"] = layout.state.appendingPathComponent("codex-logs/codex-desktop-env.log").path
        definition["StandardErrorPath"] = layout.state.appendingPathComponent("codex-logs/codex-desktop-env.err").path
        try PrivateRuntime.write(PropertyListSerialization.data(fromPropertyList: definition, format: .xml, options: 0), to: file)
        do {
            _ = try await launchctl(["bootstrap", bootstrap, file.path])
            let deadline = Date().addingTimeInterval(35)
            while Date() < deadline {
                let data = try await launchctl(["print", target])
                let lines = (String(data: data, encoding: .utf8) ?? "").split(separator: "\n").map { $0.trimmingCharacters(in: .whitespaces) }
                if lines.contains("state = not running") || lines.contains("state = exited") {
                    guard lines.contains("last exit code = 0") else { throw FleetError.message("Codex Desktop 环境设置失败。") }
                    if !persistent { _ = try await launchctl(["bootout", target]); try FileManager.default.removeItem(at: file) }
                    return
                }
                try await Task.sleep(nanoseconds: 100_000_000)
            }
            throw FleetError.message("Codex Desktop 环境设置超时。")
        } catch {
            if let loaded = try? await launchctl(["print", target]), String(data: loaded, encoding: .utf8)?.contains("path = " + file.path + "\n") == true { _ = try? await launchctl(["bootout", target]) }
            if !persistent { try? FileManager.default.removeItem(at: file) }
            throw error
        }
    }
    private func shell(_ name: String, _ arguments: [String] = []) async throws -> String {
        let output = try await execute(URL(fileURLWithPath: "/usr/bin/env"), ["HOME=" + layout.home.path, "/bin/bash", layout.codexResources.appendingPathComponent(name).path] + arguments, nil, 35)
        return String(data: output, encoding: .utf8) ?? ""
    }
    private func launchctl(_ arguments: [String]) async throws -> Data { try await execute(URL(fileURLWithPath: "/bin/launchctl"), arguments, nil, 8) }
}
