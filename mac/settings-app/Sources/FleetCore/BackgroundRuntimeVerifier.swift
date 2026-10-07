import Foundation

struct BackgroundIdentity {
    let version: String
    let build: Int64
    var agentVersion: String { "\(version)+\(build)" }
    static func read(_ application: URL, identifier: String) throws -> BackgroundIdentity {
        let data = try Data(contentsOf: application.appendingPathComponent("Contents/Info.plist"))
        guard let values = try PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any],
              values["CFBundleIdentifier"] as? String == identifier,
              let version = values["CFBundleShortVersionString"] as? String,
              version.range(of: "^[0-9]+\\.[0-9]+\\.[0-9]+$", options: .regularExpression) != nil,
              let rawBuild = values["CFBundleVersion"] as? String, let build = Int64(rawBuild), build > 0 else {
            throw FleetError.message("应用或后台载荷身份无效。")
        }
        return BackgroundIdentity(version: version, build: build)
    }
}

@MainActor
public enum BackgroundRuntimeVerifier {
    public static func verifier(host: URL, execute: @escaping (URL, [String], Data?, TimeInterval) async throws -> Data = {
        try await CommandRunner.run(executable: $0, arguments: $1, input: $2, timeout: $3)
    }) async throws -> (URL) async throws -> Void {
        let expected = try BackgroundIdentity.read(host, identifier: "com.macfleet.fleet-hub")
        _ = try await execute(URL(fileURLWithPath: "/usr/bin/codesign"), ["--verify", "--deep", "--strict", host.path], nil, 30)
        _ = try await execute(URL(fileURLWithPath: "/usr/sbin/spctl"), ["--assess", "--type", "execute", host.path], nil, 30)
        let details = try await execute(URL(fileURLWithPath: "/usr/bin/codesign"), ["-d", "--verbose=4", host.path], nil, 30)
        guard let team = String(data: details, encoding: .utf8)?.split(separator: "\n").first(where: { $0.hasPrefix("TeamIdentifier=") })?.dropFirst(15),
              team.range(of: "^[A-Z0-9]{10}$", options: .regularExpression) != nil else {
            throw FleetError.message("无法确认应用的 Developer ID 团队，不安装后台。")
        }
        let requirement = "identifier \"com.macfleet.fleet-agent\" and anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate leaf[subject.OU] = \"\(team)\""
        return { candidate in
            let identity = try BackgroundIdentity.read(candidate, identifier: "com.macfleet.fleet-agent")
            guard identity.version == expected.version, identity.build == expected.build else {
                throw FleetError.message("后台与设置应用的版本不一致，未安装。")
            }
            _ = try await execute(URL(fileURLWithPath: "/usr/bin/codesign"), ["--verify", "--deep", "--strict", "-R", requirement, candidate.path], nil, 30)
            _ = try await execute(URL(fileURLWithPath: "/usr/sbin/spctl"), ["--assess", "--type", "execute", candidate.path], nil, 30)
        }
    }
}
