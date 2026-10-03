import XCTest
@testable import FleetCore

final class ReleaseTests: XCTestCase {
    func testReleaseRejectsUnsignedDowngradedOrCrossOriginAssets() throws {
        var release = try JSONDecoder().decode(NativeRelease.self, from: Data("""
        {"schema":1,"bundle_id":"com.macfleet.fleet-hub","version":"1.0.0","build":2,"minimum_macos":"13.0","architectures":["arm64","x86_64"],"notarization":"Accepted","assets":{"dmg":{"path":"/enroll/clients/2/Fleet-Hub.dmg","sha256":"\(String(repeating:"a",count:64))","size":123},"update":{"path":"/enroll/clients/2/Fleet-Hub-update.zip","sha256":"\(String(repeating:"b",count:64))","size":123,"ed_signature":"\(Data(repeating: 1, count: 64).base64EncodedString())"}}}
        """.utf8))
        XCTAssertNoThrow(try release.validate(currentBuild: 1, architecture: "arm64", system: "13.0"))
        XCTAssertThrowsError(try release.validate(currentBuild: 3, architecture: "arm64", system: "13.0"))
        XCTAssertThrowsError(try release.validate(currentBuild: 1, architecture: "arm64", system: "12.0"))
        let originalSignature = release.assets.update.edSignature
        release.assets.update.edSignature = "invalid-signature"
        XCTAssertThrowsError(try release.validate(currentBuild: 1, architecture: "arm64", system: "13.0"))
        release.assets.update.edSignature = originalSignature
        release.version = "invalid-version"
        XCTAssertThrowsError(try release.validate(currentBuild: 1, architecture: "arm64", system: "13.0"))
        release.version = "1.0.0"
        release.assets.update.path = "https://other.example.test/update.zip"
        XCTAssertThrowsError(try release.validate(currentBuild: 1, architecture: "arm64", system: "13.0"))
        release.assets.update.path = "/enroll/clients/2/Fleet-Hub-update.zip"
        release.notarization = "In Progress"
        XCTAssertThrowsError(try release.validate(currentBuild: 1, architecture: "arm64", system: "13.0"))
    }
}
