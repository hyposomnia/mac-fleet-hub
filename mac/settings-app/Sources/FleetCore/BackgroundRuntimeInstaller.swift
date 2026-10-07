import Darwin
import Foundation

@MainActor
public enum BackgroundRuntimeInstaller {
    static func validateInstalled(_ application: URL) throws {
        try PrivateRuntime.ensureDirectory(application.deletingLastPathComponent())
        _ = try applicationInfo(application)
    }

    public static func withLock(directory: URL, operation: () async throws -> Void) async throws {
        guard !directory.standardizedFileURL.pathComponents.contains(where: { $0.lowercased().hasSuffix(".app") }) else {
            throw FleetError.message("后台必须独立安装，不能运行在设置应用包内。")
        }
        try PrivateRuntime.ensureDirectory(directory)
        let descriptor = open(directory.appendingPathComponent("install.lock").path, O_RDWR | O_CREAT | O_NOFOLLOW, 0o600)
        guard descriptor >= 0 else { throw FleetError.message("无法锁定后台安装目录。") }
        defer { close(descriptor) }
        var info = stat()
        guard fstat(descriptor, &info) == 0, info.st_uid == getuid(), info.st_mode & 0o077 == 0,
              info.st_mode & S_IFMT == S_IFREG, flock(descriptor, LOCK_EX | LOCK_NB) == 0 else {
            throw FleetError.message("后台正在安装或运行目录不安全，请稍后重试。")
        }
        defer { flock(descriptor, LOCK_UN) }
        try await operation()
    }

    public static func deploy(source: URL, destination: URL, verify: (URL) async throws -> Void,
                              prepare: () async throws -> Void, activate: () async throws -> Void,
                              quiesce: () async throws -> Void, restore: () async throws -> Void) async throws {
        try await withLock(directory: destination.deletingLastPathComponent()) {
            let existing = try applicationInfo(destination)
            try await verify(source)
            let staging = destination.deletingLastPathComponent().appendingPathComponent(".fleet-agent-\(UUID().uuidString)")
            try PrivateRuntime.ensureDirectory(staging)
            var cleanup = true
            defer { if cleanup { try? FileManager.default.removeItem(at: staging) } }
            let candidate = staging.appendingPathComponent(destination.lastPathComponent)
            try FileManager.default.copyItem(at: source, to: candidate)
            try await verify(candidate)
            var prepared = false
            var replaced = false
            do {
                prepared = true
                try await prepare()
                let current = try applicationInfo(destination)
                guard existing?.st_ino == current?.st_ino, existing?.st_dev == current?.st_dev else {
                    throw FleetError.message("后台安装位置已发生变化，未覆盖它。")
                }
                let flags = existing == nil ? RENAME_EXCL : RENAME_SWAP
                guard renamex_np(candidate.path, destination.path, UInt32(flags)) == 0 else {
                    throw FleetError.message("后台替换失败，原应用保留。")
                }
                replaced = true
                try await activate()
            } catch {
                let failure = error
                do {
                    if replaced {
                        try await quiesce()
                        if existing != nil {
                            guard renamex_np(candidate.path, destination.path, UInt32(RENAME_SWAP)) == 0 else {
                                throw FleetError.message("无法恢复原后台。")
                            }
                        } else {
                            try FileManager.default.removeItem(at: destination)
                        }
                    }
                    if prepared { try await restore() }
                } catch {
                    cleanup = false
                    throw FleetError.message("后台安装未完成且恢复失败：\(error.localizedDescription) 备份保留在 \(staging.path)")
                }
                throw failure
            }
        }
    }

    private static func applicationInfo(_ application: URL) throws -> stat? {
        var info = stat()
        if lstat(application.path, &info) != 0 {
            guard errno == ENOENT else { throw FleetError.message("无法检查后台安装位置。") }
            return nil
        }
        guard info.st_uid == getuid(), info.st_mode & S_IFMT == S_IFDIR, info.st_mode & 0o022 == 0 else {
            throw FleetError.message("后台安装位置不是安全的应用目录。")
        }
        return info
    }
}
