import Darwin
import Foundation

@MainActor
final class LegacyFleetMigration {
    let wasLoaded: Bool
    let autoStart: Bool
    private let layout: RuntimeLayout
    private let definition: URL
    private let original: Data
    private let loaded: Data?
    private let environment: [String: String]
    private let execute: (URL, [String], Data?, TimeInterval) async throws -> Data
    private var backup: URL?
    private var stopped = false
    private var retired = false
    private var target: String { "gui/\(getuid())/com.macfleet.fleet-agent" }

    private init(layout: RuntimeLayout, definition: URL, original: Data, loaded: Data?, autoStart: Bool, environment: [String: String],
                 execute: @escaping (URL, [String], Data?, TimeInterval) async throws -> Data) {
        self.layout = layout
        self.definition = definition
        self.original = original
        self.loaded = loaded
        self.wasLoaded = loaded != nil
        self.autoStart = autoStart
        self.environment = environment
        self.execute = execute
    }

    static func inspect(layout: RuntimeLayout, execute: @escaping (URL, [String], Data?, TimeInterval) async throws -> Data) async throws -> LegacyFleetMigration? {
        let definition = layout.home.appendingPathComponent("Library/LaunchAgents/com.macfleet.fleet-agent.plist")
        let loaded = try? await execute(URL(fileURLWithPath: "/bin/launchctl"), ["print", "gui/\(getuid())/com.macfleet.fleet-agent"], nil, 8)
        var info = stat()
        if lstat(definition.path, &info) != 0 {
            guard errno == ENOENT, loaded == nil else { throw FleetError.message("无法读取旧 Fleet 的启动定义，暂未更新。") }
            return nil
        }
        let original = try readOwnedFile(definition)
        guard let values = try PropertyListSerialization.propertyList(from: original, format: nil) as? [String: Any],
              values["Label"] as? String == "com.macfleet.fleet-agent",
              let arguments = values["ProgramArguments"] as? [String], arguments.count == 1,
              arguments[0].hasPrefix("/"), URL(fileURLWithPath: arguments[0]).lastPathComponent == "fleet-agent" else {
            throw FleetError.message("旧 Fleet 启动定义无效，暂未更新。")
        }
        if let loaded {
            let lines = String(decoding: loaded, as: UTF8.self).components(separatedBy: .newlines).map { $0.trimmingCharacters(in: .whitespaces) }
            guard lines.contains("path = " + definition.path), lines.contains("program = " + arguments[0]) else {
                throw FleetError.message("正在运行的旧 Fleet 与安装记录不一致，暂未更新。")
            }
        }
        let requirement = try await BackgroundRuntimeVerifier.developerIDRequirement(host: layout.application, execute: execute)
        _ = try await execute(URL(fileURLWithPath: "/usr/bin/codesign"), ["--verify", "--strict", "-R", "=" + requirement, arguments[0]], nil, 30)
        return LegacyFleetMigration(layout: layout, definition: definition, original: original, loaded: loaded, autoStart: values["RunAtLoad"] as? Bool ?? false,
                                    environment: values["EnvironmentVariables"] as? [String: String] ?? [:], execute: execute)
    }

    func prepare() async throws {
        let currentLoaded = try? await execute(URL(fileURLWithPath: "/bin/launchctl"), ["print", target], nil, 8)
        guard try Self.readOwnedFile(definition) == original, Self.processIdentity(currentLoaded) == Self.processIdentity(loaded) else {
            throw FleetError.message("旧 Fleet 状态已变化，未替换它，请重试。")
        }
        if wasLoaded {
            // Reuse the existing Codex migration guard; queued deliveries must finish first.
            try checkQueueIdle()
            let codexHome = environment["FLEET_CODEX_HOME"].flatMap { $0.isEmpty ? nil : $0 } ?? layout.home.appendingPathComponent(".codex").path
            let script = layout.bundledBackgroundApplication.appendingPathComponent("Contents/Resources/codex/check-codex-idle.sh")
            _ = try await execute(URL(fileURLWithPath: "/usr/bin/env"), ["FLEET_CODEX_HOME=" + codexHome, "/bin/bash", script.path], nil, 30)
        }
        let directory = layout.state.appendingPathComponent("migrations/\(UUID().uuidString)")
        try PrivateRuntime.ensureDirectory(directory)
        let backup = directory.appendingPathComponent(definition.lastPathComponent)
        try PrivateRuntime.write(original, to: backup)
        self.backup = backup
        if wasLoaded {
            stopped = true
            _ = try await execute(URL(fileURLWithPath: "/bin/launchctl"), ["bootout", target], nil, 8)
        }
        guard try Self.readOwnedFile(definition) == original else { throw FleetError.message("旧 Fleet 启动定义已变化，未移除它。") }
        try FileManager.default.removeItem(at: definition)
        retired = true
    }

    func restore() async throws {
        if retired, let backup {
            guard renamex_np(backup.path, definition.path, UInt32(RENAME_EXCL)) == 0 else {
                throw FleetError.message("无法恢复旧 Fleet 启动定义，备份保留在 \(backup.path)")
            }
            retired = false
        }
        if stopped {
            if (try? await execute(URL(fileURLWithPath: "/bin/launchctl"), ["print", target], nil, 8)) == nil {
                _ = try await execute(URL(fileURLWithPath: "/bin/launchctl"), ["bootstrap", "gui/\(getuid())", definition.path], nil, 8)
            }
            stopped = false
        }
    }

    private static func processIdentity(_ output: Data?) -> [String: String]? {
        guard let output else { return nil }
        var identity: [String: String] = [:]
        for line in String(decoding: output, as: UTF8.self).components(separatedBy: .newlines) {
            let pair = line.components(separatedBy: " = ")
            guard pair.count == 2 else { continue }
            let key = pair[0].trimmingCharacters(in: .whitespaces)
            if ["path", "program", "pid", "state"].contains(key) { identity[key] = pair[1].trimmingCharacters(in: .whitespaces) }
        }
        return identity
    }

    private func checkQueueIdle() throws {
        let queue = URL(fileURLWithPath: environment["FLEET_CHAT_QUEUE_FILE"].flatMap { $0.isEmpty ? nil : $0 } ?? layout.home.appendingPathComponent(".macfleet/chat-queue.json").path)
        var info = stat()
        if lstat(queue.path, &info) != 0 {
            guard errno == ENOENT else { throw FleetError.message("无法核对旧 Fleet 消息队列。") }
            return
        }
        guard let value = try JSONSerialization.jsonObject(with: Self.readOwnedFile(queue)) as? [String: Any],
              let items = value["items"] as? [[String: Any]] else { throw FleetError.message("旧 Fleet 消息队列无法读取，暂未更新。") }
        let inFlight: Set<String> = ["sending", "steering", "taking_over", "recovering", "takeover_check"]
        if items.contains(where: { inFlight.contains($0["status"] as? String ?? "") }) {
            throw FleetError.message("消息正在投递，请完成后再更新。")
        }
    }

    private static func readOwnedFile(_ url: URL) throws -> Data {
        let descriptor = open(url.path, O_RDONLY | O_NOFOLLOW)
        guard descriptor >= 0 else { throw FleetError.message("无法读取旧 Fleet 配置。") }
        let handle = FileHandle(fileDescriptor: descriptor, closeOnDealloc: true)
        var info = stat()
        guard fstat(descriptor, &info) == 0, info.st_uid == getuid(), info.st_mode & S_IFMT == S_IFREG, info.st_mode & 0o022 == 0 else {
            throw FleetError.message("旧 Fleet 配置的归属或权限无效。")
        }
        return try handle.readToEnd() ?? Data()
    }
}
