import Darwin
import Foundation

public enum PrivateRuntime {
    public static func read(_ source: URL) throws -> Data {
        try check(source, directory: false)
        let descriptor = open(source.path, O_RDONLY | O_NOFOLLOW)
        guard descriptor >= 0 else { throw FleetError.message("无法打开私有配置文件。") }
        let handle = FileHandle(fileDescriptor: descriptor, closeOnDealloc: true)
        let data = try handle.read(upToCount: 65537) ?? Data()
        guard data.count <= 65536 else { throw FleetError.message("本机配置文件过大。") }
        return data
    }
    public static func ensureDirectory(_ directory: URL) throws {
        if !FileManager.default.fileExists(atPath: directory.path) {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        }
        try check(directory, directory: true)
    }

    public static func write(_ data: Data, to destination: URL) throws {
        try ensureDirectory(destination.deletingLastPathComponent())
        var info = stat()
        if lstat(destination.path, &info) == 0 {
            try check(destination, directory: false)
        } else if errno != ENOENT {
            throw FleetError.message("无法检查本机配置文件。")
        }
        let temporary = destination.deletingLastPathComponent().appendingPathComponent(".runtime-\(UUID().uuidString)")
        let descriptor = open(temporary.path, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW, 0o600)
        guard descriptor >= 0 else { throw FleetError.message("无法创建私有配置文件。") }
        defer { close(descriptor); unlink(temporary.path) }
        try data.withUnsafeBytes { bytes in
            var offset = 0
            while offset < bytes.count {
                let written = Darwin.write(descriptor, bytes.baseAddress!.advanced(by: offset), bytes.count - offset)
                guard written > 0 else { throw FleetError.message("配置写入失败。") }
                offset += written
            }
        }
        guard fsync(descriptor) == 0, rename(temporary.path, destination.path) == 0 else {
            throw FleetError.message("配置保存失败，原文件保持不变。")
        }
    }

    private static func check(_ url: URL, directory: Bool) throws {
        var info = stat()
        guard lstat(url.path, &info) == 0,
              info.st_uid == getuid(), info.st_mode & 0o077 == 0,
              info.st_mode & S_IFMT == (directory ? S_IFDIR : S_IFREG) else {
            throw FleetError.message("本机配置路径的归属或权限不安全。")
        }
    }
}
