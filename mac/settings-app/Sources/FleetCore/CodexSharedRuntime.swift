import Darwin
import Foundation

/// Native installation of the same keeper, launch definition and Desktop helper
/// used by setup-mac.sh. This owns no Codex transport or recovery implementation.
@MainActor
public final class CodexSharedRuntime {
    private let layout: RuntimeLayout
    private let execute: (URL, [String], Data?, TimeInterval) async throws -> Data
    private let readyAttempts: Int
    private let endpoint = "ws://127.0.0.1:47682/rpc"
    private var domain: String { "gui/\(getuid())" }
    private var target: String { domain + "/com.macfleet.codex-app-server" }
    private var resources: URL { layout.backgroundApplication.appendingPathComponent("Contents/Resources/codex") }
    private var definition: URL { layout.state.appendingPathComponent("codex-app-server.plist") }
    private var snapshot: URL { layout.state.appendingPathComponent("codex-desktop-environment.json") }
    private var proxy: URL { layout.home.appendingPathComponent(".macfleet/codex-app-server.sock") }
    private let environmentKeys = ["CODEX_APP_SERVER_WS_URL", "CODEX_APP_SERVER_USE_LOCAL_DAEMON"]

    public init(layout: RuntimeLayout, execute: @escaping (URL, [String], Data?, TimeInterval) async throws -> Data = {
        try await CommandRunner.run(executable: $0, arguments: $1, input: $2, timeout: $3)
    }, readyAttempts: Int = 30) {
        self.layout = layout
        self.execute = execute
        self.readyAttempts = readyAttempts
    }

    public func start() async throws {
        let owned = FileManager.default.fileExists(atPath: definition.path)
        if let loaded = try? await launchctl(["print", target]) {
            if owned {
                try validateOwnership(loaded)
                _ = try readSnapshot()
                let state = (String(data: loaded, encoding: .utf8) ?? "").split(separator: "\n")
                    .map { $0.trimmingCharacters(in: .whitespaces) }
                if state.contains("state = not running") || state.contains("state = exited") {
                    // No -k: a concurrently recovered keeper must never be killed.
                    _ = try await launchctl(["kickstart", target])
                }
            }
            do { try await waitForReady() }
            catch {
                let failure = error
                if owned { _ = try await shell("codex-desktop-env.sh", ["clear"]) }
                throw failure
            }
            // An existing installation keeps its own lifecycle and GUI settings.
            if owned { try await applyDesktopEnvironment() }
            return
        }
        let node: String
        do {
            node = try await shell("codex-bin-resolve.sh", ["--keeper-node"])
                .trimmingCharacters(in: .whitespacesAndNewlines)
        } catch {
            // Codex is optional. Its absence must not disable enrollment/files/terminal.
            FileHandle.standardError.write(Data("Codex shared keeper 不可用：\(error.localizedDescription)\n".utf8))
            return
        }
        guard node.hasPrefix("/"), !node.contains("\n") else { throw FleetError.message("Codex Node 路径无效。") }
        let data = try launchDefinition(node: node)
        let previousDefinition = owned ? try PrivateRuntime.read(definition) : nil
        let previousEnvironment = try await readEnvironment()
        let hadSnapshot = FileManager.default.fileExists(atPath: snapshot.path)
        if hadSnapshot { _ = try readSnapshot() }
        else { try PrivateRuntime.write(JSONEncoder().encode(previousEnvironment), to: snapshot) }
        var started = false
        do {
            try PrivateRuntime.write(data, to: definition)
            _ = try await launchctl(["bootstrap", domain, definition.path])
            started = true
            try await waitForReady()
            try await applyDesktopEnvironment()
        } catch {
            let failure = error
            do {
                if !started, let loaded = try? await launchctl(["print", target]),
                   String(data: loaded, encoding: .utf8)?.contains("path = " + definition.path) == true {
                    // Account for a bootstrap that completed despite a command timeout.
                    started = true
                }
                if started {
                    // A Desktop may have attached while startup was being checked.
                    _ = try await shell("check-codex-idle.sh")
                }
                try await restoreEnvironment(previousEnvironment)
                if started { _ = try await launchctl(["bootout", target]) }
                if let previousDefinition { try PrivateRuntime.write(previousDefinition, to: definition) }
                else { try removePrivateFile(definition) }
                if !hadSnapshot { try removePrivateFile(snapshot) }
            } catch {
                throw FleetError.message("Codex shared 启动失败，回滚未完成，保留启动定义：\(failure.localizedDescription)；\(error.localizedDescription)")
            }
            throw failure
        }
    }

    public func prepareForRemoval() async throws {
        guard FileManager.default.fileExists(atPath: definition.path) else { return }
        _ = try PrivateRuntime.read(definition)
        _ = try readSnapshot()
        if let loaded = try? await launchctl(["print", target]) {
            try validateOwnership(loaded)
            _ = try await shell("check-codex-idle.sh")
        }
    }

    public func remove() async throws {
        guard FileManager.default.fileExists(atPath: definition.path) else { return }
        try await prepareForRemoval()
        // Restore GUI intent before taking away the listener.
        try await restoreEnvironment(readSnapshot())
        if let loaded = try? await launchctl(["print", target]) {
            try validateOwnership(loaded)
            _ = try await launchctl(["bootout", target])
        }
        try removePrivateFile(definition)
        try removePrivateFile(snapshot)
    }

    private func validateOwnership(_ loaded: Data) throws {
        _ = try PrivateRuntime.read(definition)
        guard String(data: loaded, encoding: .utf8)?.contains("path = " + definition.path) == true else {
            throw FleetError.message("Codex shared 服务已由其他安装管理，未修改或停止它。")
        }
    }

    private func launchDefinition(node: String) throws -> Data {
        let template = try Data(contentsOf: resources.appendingPathComponent("com.macfleet.codex-shared-app-server.plist"))
        let replacements = [
            "__CODEX_HOME__": layout.home.appendingPathComponent(".codex").path,
            "__CODEX_RESOLVER__": resources.appendingPathComponent("codex-bin-resolve.sh").path,
            "__CODEX_KEEPER_LAUNCHER__": resources.appendingPathComponent("codex-keeper-launch.sh").path,
            "__CODEX_KEEPER_NODE__": node,
            "__CODEX_KEEPER_SCRIPT__": resources.appendingPathComponent("codex-shared-app-server.mjs").path,
            "__CODEX_BIN__": "", // The existing supervisor resolves it on every start.
            "__CODEX_APPSERVER_LISTEN__": "ws://127.0.0.1:47682",
            "__CODEX_APPSERVER_SOCK__": proxy.path,
            "__FLEET_LOG_DIR__": layout.state.appendingPathComponent("codex-logs").path,
            "__FLEET_STATE_DIR__": layout.state.appendingPathComponent("codex-state").path,
            "__BREW_PREFIX__": "/opt/homebrew"
        ]
        func render(_ value: Any) throws -> Any {
            if var string = value as? String {
                for (key, replacement) in replacements { string = string.replacingOccurrences(of: key, with: replacement) }
                guard !string.contains("__") else { throw FleetError.message("Codex 启动定义包含未解析参数。") }
                return string
            }
            if let list = value as? [Any] { return try list.map(render) }
            if let dictionary = value as? [String: Any] { return try dictionary.mapValues(render) }
            return value
        }
        guard var result = try render(PropertyListSerialization.propertyList(from: template, format: nil)) as? [String: Any],
              var environment = result["EnvironmentVariables"] as? [String: String],
              result["Label"] as? String == "com.macfleet.codex-app-server" else {
            throw FleetError.message("Codex shared 启动模板无效。")
        }
        environment["HOME"] = layout.home.path
        result["EnvironmentVariables"] = environment
        for directory in ["codex-logs", "codex-state"] { try PrivateRuntime.ensureDirectory(layout.state.appendingPathComponent(directory)) }
        return try PropertyListSerialization.data(fromPropertyList: result, format: .xml, options: 0)
    }

    private func waitForReady() async throws {
        for attempt in 0..<readyAttempts {
            if (try? await execute(URL(fileURLWithPath: "/usr/bin/curl"), ["-fsS", "--max-time", "1", "http://127.0.0.1:47682/readyz"], nil, 2)) != nil {
                let listener = try await execute(URL(fileURLWithPath: "/usr/sbin/lsof"), ["-nP", "-iTCP:47682", "-sTCP:LISTEN"], nil, 3)
                let lines = (String(data: listener, encoding: .utf8) ?? "").split(separator: "\n").filter { $0.contains("(LISTEN)") }
                let socket = try await execute(URL(fileURLWithPath: "/usr/bin/stat"), ["-f", "%u %Lp %HT", proxy.path], nil, 3)
                guard !lines.isEmpty, lines.allSatisfy({ $0.contains("127.0.0.1:47682 (LISTEN)") }),
                      String(data: socket, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) == "\(getuid()) 600 Socket" else {
                    throw FleetError.message("Codex shared listener 或私有 proxy 不安全。")
                }
                let data = try? await execute(URL(fileURLWithPath: "/usr/bin/env"), ["HOME=" + layout.home.path, layout.agent.path, "desktop", "status"], nil, 3)
                if let data, let status = try? JSONDecoder().decode(AgentStatus.self, from: data),
                   status.schema == 1, status.settings.schema == 1, status.pid > 0, status.isReadyForManagement {
                    return
                }
            }
            if attempt + 1 < readyAttempts { try await Task.sleep(nanoseconds: 1_000_000_000) }
        }
        throw FleetError.message("Codex shared app-server 或 Fleet Agent 未通过就绪检查。")
    }

    private func applyDesktopEnvironment() async throws {
        _ = try await shell("codex-desktop-env.sh", ["shared", endpoint])
    }
    private func readEnvironment() async throws -> [String: String] {
        var values: [String: String] = [:]
        for key in environmentKeys {
            let data = try await launchctl(["getenv", key])
            values[key] = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .newlines) ?? ""
        }
        return values
    }
    private func readSnapshot() throws -> [String: String] {
        let values = try JSONDecoder().decode([String: String].self, from: PrivateRuntime.read(snapshot))
        guard Set(values.keys) == Set(environmentKeys) else { throw FleetError.message("Codex Desktop 环境备份无效。") }
        return values
    }
    private func restoreEnvironment(_ values: [String: String]) async throws {
        for key in environmentKeys {
            let value = values[key] ?? ""
            _ = try await launchctl(value.isEmpty ? ["unsetenv", key] : ["setenv", key, value])
        }
    }
    private func removePrivateFile(_ file: URL) throws {
        _ = try PrivateRuntime.read(file)
        try FileManager.default.removeItem(at: file)
    }
    private func shell(_ file: String, _ arguments: [String] = []) async throws -> String {
        let output = try await execute(URL(fileURLWithPath: "/usr/bin/env"), ["HOME=" + layout.home.path, "/bin/bash", resources.appendingPathComponent(file).path] + arguments, nil, 35)
        return String(data: output, encoding: .utf8) ?? ""
    }
    private func launchctl(_ arguments: [String]) async throws -> Data {
        try await execute(URL(fileURLWithPath: "/bin/launchctl"), arguments, nil, 8)
    }
}
