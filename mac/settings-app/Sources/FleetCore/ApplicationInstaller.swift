import Darwin
import Foundation

public enum ApplicationInstaller {
    public static func install(source: URL, destination: URL, replaceExisting: Bool = false, verify: (URL) async throws -> Void) async throws {
        var original = stat()
        let exists = lstat(destination.path, &original) == 0
        guard exists || errno == ENOENT else { throw FleetError.message("无法读取应用安装位置，请检查目录权限。") }
        if exists {
            guard replaceExisting else { throw FleetError.message("安装位置已有应用，请确认替换旧版本。") }
            guard (original.st_mode & S_IFMT) == S_IFDIR else { throw FleetError.message("安装位置不是应用目录，未替换。") }
            let incoming = try BackgroundIdentity.read(source, identifier: "com.macfleet.fleet-hub")
            let installed = try BackgroundIdentity.read(destination, identifier: "com.macfleet.fleet-hub")
            guard incoming.build >= installed.build,
                  incoming.version.compare(installed.version, options: .numeric) != .orderedAscending else {
                throw FleetError.message("已安装的版本较新，请下载最新版安装包。")
            }
        }
        try await verify(source)
        let staging = destination.deletingLastPathComponent().appendingPathComponent(".fleet-install-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: staging, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
        var preserveBackup = false
        defer { if !preserveBackup { try? FileManager.default.removeItem(at: staging) } }
        let application = staging.appendingPathComponent(destination.lastPathComponent)
        try FileManager.default.copyItem(at: source, to: application)
        try await verify(application)
        if exists {
            var current = stat()
            guard lstat(destination.path, &current) == 0,
                  current.st_dev == original.st_dev, current.st_ino == original.st_ino else {
                throw FleetError.message("安装期间旧应用发生变化，请重新确认后重试。")
            }
            // Atomic exchange keeps the complete old bundle as a rollback copy until verification succeeds.
            guard renamex_np(application.path, destination.path, UInt32(RENAME_SWAP)) == 0 else {
                throw FleetError.message("无法替换应用，旧版本保持不变；请检查安装目录权限。")
            }
            do { try await verify(destination) }
            catch {
                let failure = error
                guard renamex_np(application.path, destination.path, UInt32(RENAME_SWAP)) == 0 else {
                    preserveBackup = true
                    throw FleetError.message("新版验证失败，自动恢复旧版失败。旧应用备份位于：\(application.path)")
                }
                throw failure
            }
        } else {
            guard renamex_np(application.path, destination.path, UInt32(RENAME_EXCL)) == 0 else {
                throw FleetError.message("安装未完成，原应用保持不变；请检查目录权限或已有安装。")
            }
        }
    }
}
