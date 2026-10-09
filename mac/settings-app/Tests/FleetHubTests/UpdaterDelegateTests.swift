import XCTest
import FleetCore
import Sparkle
@testable import FleetHub

@MainActor
final class UpdaterDelegateTests: XCTestCase {
    func testNewerSignedReplacementCanRecoverAnInterruptedOlderUpdate() {
        let record = AppUpdater.Recovery(directory: UUID(), previousBuild: 5, expectedBuild: 6, previousPID: 10)
        XCTAssertTrue(record.accepts(installedBuild: 5))
        XCTAssertTrue(record.accepts(installedBuild: 6))
        XCTAssertTrue(record.accepts(installedBuild: 7))
        XCTAssertFalse(record.accepts(installedBuild: 4))
        let invalid = AppUpdater.Recovery(directory: UUID(), previousBuild: 6, expectedBuild: 5, previousPID: 10)
        XCTAssertFalse(invalid.accepts(installedBuild: 7))
    }

    func testSparklePreservesManifestFieldsUsedBySafetyCallback() throws {
        let signature = Data(repeating: 1, count: 64).base64EncodedString()
        let url = "https://fleet.example.test/enroll/clients/2/Fleet-Hub-update.zip"
        let item = try XCTUnwrap(SUAppcastItem(dictionary: [
            "title": "Fleet Hub 1.0.0",
            "enclosure": ["url": url, "length": "123", "type": "application/octet-stream",
                          "sparkle:version": "2", "sparkle:edSignature": signature],
        ]))
        XCTAssertEqual(item.versionString, "2")
        XCTAssertEqual(item.fileURL?.absoluteString, url)
        XCTAssertEqual(item.contentLength, 123)
        XCTAssertFalse(item.isDeltaUpdate)
        let enclosure = item.propertiesDictionary["enclosure"] as? [String: Any]
        XCTAssertEqual(enclosure?["sparkle:edSignature"] as? String, signature)
    }

    func testSafetyCallbacksAreActuallyRegisteredWithSparkle() {
        let management = NativeManagement(layout: RuntimeLayout(application: URL(fileURLWithPath: "/tmp/Fleet Hub.app"), home: URL(fileURLWithPath: "/tmp/fixture")))
        let updater = AppUpdater(management: management)
        for selector in ["feedURLStringForUpdater:", "updater:shouldProceedWithUpdate:updateCheck:error:",
                         "updater:willDownloadUpdate:withRequest:", "updater:shouldPostponeRelaunchForUpdate:untilInvokingBlock:",
                         "updater:didAbortWithError:", "updater:willInstallUpdate:", "updater:didFinishUpdateCycleForUpdateCheck:error:"] {
            XCTAssertTrue(updater.responds(to: NSSelectorFromString(selector)), selector)
        }
        XCTAssertEqual(updater.shouldTerminate(), .terminateNow)
    }
}
