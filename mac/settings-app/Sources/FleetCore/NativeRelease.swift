import Foundation

public struct NativeRelease: Codable, Sendable {
    public struct Asset: Codable, Sendable {
        public var path: String
        public var sha256: String
        public var size: Int64
        public var edSignature: String?
        enum CodingKeys: String, CodingKey { case path, sha256, size; case edSignature = "ed_signature" }
    }
    public struct Assets: Codable, Sendable { public var dmg: Asset; public var update: Asset }
    public var schema: Int
    public var bundleID: String
    public var version: String
    public var build: Int64
    public var minimumMacOS: String
    public var architectures: [String]
    public var notarization: String
    public var assets: Assets
    enum CodingKeys: String, CodingKey {
        case schema, version, build, architectures, notarization, assets
        case bundleID = "bundle_id"
        case minimumMacOS = "minimum_macos"
    }

    public func validate(currentBuild: Int64, architecture: String, system: String) throws {
        guard schema == 1, bundleID == "com.macfleet.fleet-hub", notarization == "Accepted",
              version.range(of: "^[0-9]+\\.[0-9]+\\.[0-9]+$", options: .regularExpression) != nil,
              minimumMacOS.range(of: "^[0-9]+\\.[0-9]+(?:\\.[0-9]+)?$", options: .regularExpression) != nil,
              build > 0, build >= currentBuild, architectures.contains(architecture),
              system.compare(minimumMacOS, options: .numeric) != .orderedAscending else {
            throw FleetError.message("发行版本不受支持、尚未公证，或会造成版本降级。")
        }
        for (asset, filename) in [(assets.dmg, "Fleet-Hub.dmg"), (assets.update, "Fleet-Hub-update.zip")] {
            guard asset.path == "/enroll/clients/\(build)/\(filename)",
                  asset.sha256.range(of: "^[a-f0-9]{64}$", options: .regularExpression) != nil,
                  asset.size > 0 else { throw FleetError.message("应用发行清单中的安装包信息无效。") }
        }
        guard let signature = assets.update.edSignature, let bytes = Data(base64Encoded: signature),
              bytes.count == 64, bytes.base64EncodedString() == signature else {
            throw FleetError.message("升级包缺少发行签名。")
        }
    }

    public static func fetch(origin: String) async throws -> NativeRelease {
        let origin = try FleetSettings.validatedOrigin(origin)
        let delegate = SameOriginDownload(origin: origin)
        let configuration = URLSessionConfiguration.ephemeral
        configuration.httpShouldSetCookies = false
        configuration.httpCookieStorage = nil
        configuration.timeoutIntervalForRequest = 20
        let session = URLSession(configuration: configuration, delegate: delegate, delegateQueue: nil)
        defer { session.invalidateAndCancel() }
        let (bytes, response) = try await session.bytes(from: URL(string: origin + "/enroll/client-release.json")!)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else {
            throw FleetError.message("服务器尚未提供可用的应用发行清单。")
        }
        var data = Data()
        for try await byte in bytes {
            guard data.count < 65536 else { throw FleetError.message("应用发行清单过大。") }
            data.append(byte)
        }
        return try JSONDecoder().decode(NativeRelease.self, from: data)
    }
}

private final class SameOriginDownload: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    let origin: String
    init(origin: String) { self.origin = origin }
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        guard let target = request.url, let base = URL(string: origin), target.scheme == base.scheme,
              target.host == base.host, target.port == base.port, target.user == nil else { completionHandler(nil); return }
        completionHandler(request)
    }
}
