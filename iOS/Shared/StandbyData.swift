import Foundation
import Darwin

struct StandbyModel: Codable, Identifiable {
    let assistant: String
    let model: String
    let totalTokens: Int64
    var id: String { assistant + "/" + model }
}

struct StandbySession: Codable, Identifiable {
    let id: String
    let deviceId: String
    let deviceName: String
    let sessionId: String
    let assistant: String
    let title: String
    let project: String
    let running: Bool
    let waiting: Bool
    let unread: Bool
    let activityAt: Double
}

struct StandbySnapshot: Codable {
    let version: Int
    let date: String
    let timezone: String
    let generatedAt: Double
    let totalTokens: Int64
    let models: [StandbyModel]
    let hosts: [Host]
    let reportingHosts: Int
    let usageComplete: Bool
    let sessions: [StandbySession]
    let sessionsPartial: Bool
    struct Host: Codable { let id: String; let name: String; let usageStatus: String }

    func isToday(now: Date = Date(), timezone: TimeZone = .current) -> Bool {
        let formatter = DateFormatter()
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = timezone
        formatter.dateFormat = "yyyy-MM-dd"
        return self.timezone == timezone.identifier && date == formatter.string(from: now)
    }
    var updatedAt: Date { Date(timeIntervalSince1970: generatedAt / 1000) }
}

struct StandbyConnection: Codable, Equatable {
    let gateway: URL
    let identity: String
    let token: String
    let expiresAt: Double
    // Also serves as a generation: a late request from an old login cannot write.
    let generation: UUID
}

enum StandbyStorage {
    static let group = "group.com.macfleet.FleetHub"
    static var directory: URL? { FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group) }
    static var defaults: UserDefaults? { UserDefaults(suiteName: group) }
    static func read<T: Decodable>(_ name: String, as: T.Type) -> T? {
        guard let url = directory?.appendingPathComponent(name),
              let data = try? Data(contentsOf: url) else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }
    static func write<T: Encodable>(_ value: T, name: String) throws {
        guard let directory else { throw URLError(.cannotCreateFile) }
        let url = directory.appendingPathComponent(name)
        try JSONEncoder().encode(value).write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
        var resource = URLResourceValues(); resource.isExcludedFromBackup = true
        var mutable = url; try mutable.setResourceValues(resource)
    }
    // Serialize cross-process login/reset/fetch publication. Atomic file writes
    // alone do not prevent an old in-flight widget response resurrecting data.
    private static func locked<T>(_ operation: () throws -> T) throws -> T {
        guard let file = directory?.appendingPathComponent("standby.lock") else { throw URLError(.cannotCreateFile) }
        let fd = open(file.path, O_CREAT | O_RDWR, S_IRUSR | S_IWUSR)
        guard fd >= 0 else { throw URLError(.cannotOpenFile) }
        defer { flock(fd, LOCK_UN); close(fd) }
        try FileManager.default.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication], ofItemAtPath: file.path)
        guard flock(fd, LOCK_EX) == 0 else { throw URLError(.cannotOpenFile) }
        return try operation()
    }
    private static func removeFiles() {
        for name in ["standby-connection.json", "standby-snapshot.json"] {
            if let url = directory?.appendingPathComponent(name) { try? FileManager.default.removeItem(at: url) }
        }
    }
    static func clear() { try? locked { removeFiles() } }
    static func install(_ connection: StandbyConnection) throws {
        try locked { removeFiles(); try write(connection, name: "standby-connection.json") }
    }
    static func connection() -> StandbyConnection? { read("standby-connection.json", as: StandbyConnection.self) }
    static func snapshot(now: Date = Date()) -> StandbySnapshot? {
        try? locked {
            guard let connection = connection(), connection.expiresAt > now.timeIntervalSince1970 * 1000,
                  let cached = read("standby-snapshot.json", as: Cached.self), cached.generation == connection.generation,
                  cached.snapshot.isToday(now: now), now.timeIntervalSince(cached.snapshot.updatedAt) < 30 * 60 else { return nil }
            return cached.snapshot
        }
    }
    private struct Cached: Codable { let generation: UUID; let snapshot: StandbySnapshot }
    static func clockText(_ date: Date, timezone: TimeZone = .current) -> String {
        let format = DateFormatter(); format.locale = Locale(identifier: "en_US_POSIX")
        format.timeZone = timezone; format.dateFormat = "HH:mm"
        return format.string(from: date)
    }
    static func compact(_ tokens: Int64) -> String {
        if tokens >= 1_000_000_000 { return String(format: "%.2f B", Double(tokens) / 1_000_000_000) }
        if tokens >= 1_000_000 { return String(format: "%.2f M", Double(tokens) / 1_000_000) }
        if tokens >= 1_000 { return String(format: "%.0f K", Double(tokens) / 1_000) }
        return String(tokens)
    }

    // A dedicated session has no cookies/cache and never follows a redirect with
    // credentials. The widget can read only the scoped standby endpoint.
    static func fetch(_ connection: StandbyConnection) async throws -> StandbySnapshot {
        guard connection.gateway.scheme == "https", connection.gateway.user == nil,
              connection.gateway.password == nil else { throw URLError(.unsupportedURL) }
        var url = URLComponents(url: connection.gateway, resolvingAgainstBaseURL: false)!
        url.path = "/api/standby"
        url.queryItems = [URLQueryItem(name: "timezone", value: TimeZone.current.identifier)]
        var request = URLRequest(url: url.url!, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 28)
        request.setValue("Bearer " + connection.token, forHTTPHeaderField: "Authorization")
        let config = URLSessionConfiguration.ephemeral
        config.httpCookieStorage = nil; config.urlCache = nil
        let session = URLSession(configuration: config, delegate: NoStandbyRedirects(), delegateQueue: nil)
        defer { session.invalidateAndCancel() }
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw URLError(.badServerResponse) }
        guard connection == Self.connection() else { throw CancellationError() }
        if http.statusCode == 401 || http.statusCode == 403 {
            try? locked { if connection == Self.connection() { removeFiles() } }
            throw URLError(.userAuthenticationRequired)
        }
        guard http.statusCode == 200, data.count <= 2 << 20 else { throw URLError(.badServerResponse) }
        let value = try JSONDecoder().decode(StandbySnapshot.self, from: data)
        guard value.version == 1, value.isToday() else { throw URLError(.cannotParseResponse) }
        guard !Task.isCancelled, connection == Self.connection() else { throw CancellationError() }
        try locked {
            guard !Task.isCancelled, connection == Self.connection() else { throw CancellationError() }
            try write(Cached(generation: connection.generation, snapshot: value), name: "standby-snapshot.json")
        }
        return value
    }
}

private final class NoStandbyRedirects: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) { completionHandler(nil) }
}
