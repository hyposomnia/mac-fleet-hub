import Darwin
import Foundation

public enum ApplicationInstaller {
    public static func install(source: URL, destination: URL, verify: (URL) async throws -> Void) async throws {
        var info = stat()
        guard lstat(destination.path, &info) != 0, errno == ENOENT else {
            throw FleetError.message("安装位置已有应用，请打开已有应用检查更新。")
        }
        try await verify(source)
        let staging = destination.deletingLastPathComponent().appendingPathComponent(".fleet-install-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: staging, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
        defer { try? FileManager.default.removeItem(at: staging) }
        let application = staging.appendingPathComponent(destination.lastPathComponent)
        try FileManager.default.copyItem(at: source, to: application)
        try await verify(application)
        guard renamex_np(application.path, destination.path, UInt32(RENAME_EXCL)) == 0 else {
            throw FleetError.message("安装未完成，原应用保持不变；请检查目录权限或已有安装。")
        }
    }
}
