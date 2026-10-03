import Darwin
import Foundation

public enum CommandRunner {
    public static func run(executable: URL, arguments: [String], input: Data? = nil, timeout: TimeInterval = 8) async throws -> Data {
        try await Task.detached {
            try runSynchronously(executable: executable, arguments: arguments, input: input, timeout: timeout)
        }.value
    }

    private static func runSynchronously(executable: URL, arguments: [String], input: Data?, timeout: TimeInterval) throws -> Data {
            let process = Process()
            process.executableURL = executable
            process.arguments = arguments
            let allowed = ["HOME", "USER", "LOGNAME", "TMPDIR", "PATH", "LANG", "LC_ALL"]
            process.environment = ProcessInfo.processInfo.environment.filter { allowed.contains($0.key) }
            let output = Pipe()
            let standardInput = Pipe()
            process.standardOutput = output
            process.standardError = output
            process.standardInput = standardInput
            let captured = CapturedOutput()
            let completed = DispatchGroup()
            try process.run()
            defer {
                if process.isRunning {
                    Darwin.kill(process.processIdentifier, SIGKILL)
                    process.waitUntilExit()
                }
            }
            completed.enter()
            DispatchQueue.global(qos: .utility).async {
                defer { completed.leave() }
                while true {
                    let chunk = output.fileHandleForReading.availableData
                    if chunk.isEmpty { break }
                    captured.append(chunk)
                }
            }
            if let input { try standardInput.fileHandleForWriting.write(contentsOf: input) }
            try standardInput.fileHandleForWriting.close()
            let deadline = Date().addingTimeInterval(timeout)
            while process.isRunning && Date() < deadline { Thread.sleep(forTimeInterval: 0.02) }
            if process.isRunning {
                process.terminate()
                let grace = Date().addingTimeInterval(0.3)
                while process.isRunning && Date() < grace { Thread.sleep(forTimeInterval: 0.02) }
                if process.isRunning { Darwin.kill(process.processIdentifier, SIGKILL) }
                process.waitUntilExit()
                throw FleetError.message("操作超时，未确认完成。")
            }
            process.waitUntilExit()
            guard completed.wait(timeout: .now() + 1) == .success else {
                throw FleetError.message("后台响应未完成。")
            }
            let result = captured.data
            guard process.terminationReason == .exit, process.terminationStatus == 0 else {
                let message = String(data: result, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "操作失败。"
                throw FleetError.message(message.isEmpty ? "操作失败。" : message)
            }
            guard !captured.overflow else { throw FleetError.message("后台响应过大。") }
            return result
    }
}

private final class CapturedOutput: @unchecked Sendable {
    private let lock = NSLock()
    private var storage = Data()
    private var exceeded = false
    func append(_ data: Data) {
        lock.lock()
        defer { lock.unlock() }
        if storage.count + data.count > 32768 { exceeded = true }
        storage.append(data.prefix(max(0, 32768 - storage.count)))
    }
    var data: Data {
        lock.lock()
        defer { lock.unlock() }
        return storage
    }
    var overflow: Bool {
        lock.lock()
        defer { lock.unlock() }
        return exceeded
    }
}
