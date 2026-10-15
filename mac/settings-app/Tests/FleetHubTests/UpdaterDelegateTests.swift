import XCTest
import FleetCore
import Sparkle
@testable import FleetHub

@MainActor
final class UpdaterDelegateTests: XCTestCase {
    private func installationFixture(execute: @escaping (URL, [String], Data?, TimeInterval) async throws -> Data = { _, _, _, _ in
        XCTFail("Unexpected native operation")
        throw FleetError.message("Unexpected native operation")
    }) throws -> (AppUpdater, SPUStandardUpdaterController, SUAppcastItem) {
        let management = NativeManagement(layout: RuntimeLayout(application: URL(fileURLWithPath: "/tmp/Fleet Hub.app"), home: URL(fileURLWithPath: "/tmp/fixture")), execute: execute)
        let updater = AppUpdater(management: management)
        let controller = SPUStandardUpdaterController(startingUpdater: false, updaterDelegate: updater, userDriverDelegate: nil)
        let item = try XCTUnwrap(SUAppcastItem(dictionary: ["enclosure": ["url": "https://fleet.example.test/update.zip", "sparkle:version": "2"]]))
        return (updater, controller, item)
    }

    func testInstallationActionsAppearOnlyAfterDeferredPreparationFails() async throws {
        let (updater, controller, item) = try installationFixture()
        let sparkle = controller.updater
        var installed = false
        XCTAssertFalse(updater.installationNeedsAttention)
        updater.updater(sparkle, willDownloadUpdate: item, with: NSMutableURLRequest(url: try XCTUnwrap(item.fileURL)))
        XCTAssertFalse(updater.installationNeedsAttention)
        updater.updater(sparkle, willInstallUpdate: item)
        XCTAssertFalse(updater.installationNeedsAttention)
        XCTAssertTrue(updater.updater(sparkle, shouldPostponeRelaunchForUpdate: item, untilInvokingBlock: { installed = true }))
        XCTAssertFalse(updater.installationNeedsAttention)
        await updater.continueInstallation()
        // No validated manifest is available: preparation must fail before any native operation.
        XCTAssertTrue(updater.installationNeedsAttention)
        XCTAssertFalse(installed)
        XCTAssertTrue(updater.message.contains("缺少已校验的升级清单"))
        updater.updater(sparkle, didFinishUpdateCycleFor: .updates, error: nil)
        XCTAssertFalse(updater.installationNeedsAttention)
        await Task.yield()
        XCTAssertFalse(updater.installationNeedsAttention)
    }

    func testWaitingCanBeCancelledWithoutLosingPendingInstallationOrItsReason() async throws {
        let entered = expectation(description: "idle guard entered")
        var releaseCheck: CheckedContinuation<Void, Never>?
        let (updater, controller, item) = try installationFixture { _, arguments, _, _ in
            if arguments == ["desktop", "prepare-stop"] {
                entered.fulfill()
                await withCheckedContinuation { releaseCheck = $0 }
                throw FleetError.message("任务正在执行")
            }
            return Data()
        }
        let sparkle = controller.updater
        var installed = false
        _ = updater.updater(sparkle, shouldPostponeRelaunchForUpdate: item, untilInvokingBlock: { installed = true })
        await updater.continueInstallation()
        let waiting = Task { await updater.waitForIdleAndInstall() }
        await fulfillment(of: [entered], timeout: 1)
        XCTAssertTrue(updater.waitingForIdle)
        XCTAssertFalse(updater.installationNeedsAttention)
        XCTAssertTrue(updater.message.contains("等待"))
        let waitingMessage = updater.message
        await updater.continueInstallation()
        await updater.waitForIdleAndInstall()
        XCTAssertEqual(updater.message, waitingMessage)
        updater.cancelWaitingForIdle()
        releaseCheck?.resume()
        await waiting.value
        XCTAssertFalse(updater.waitingForIdle)
        XCTAssertTrue(updater.installationNeedsAttention)
        XCTAssertTrue(updater.message.contains("已取消等待"))
        XCTAssertFalse(installed)
        updater.updater(sparkle, didFinishUpdateCycleFor: .updates, error: nil)
    }

    func testEndingUpdateDuringAWaitPreservesTheUpdateError() async throws {
        let entered = expectation(description: "idle guard entered")
        var releaseCheck: CheckedContinuation<Void, Never>?
        let (updater, controller, item) = try installationFixture { _, arguments, _, _ in
            if arguments == ["desktop", "prepare-stop"] {
                entered.fulfill()
                await withCheckedContinuation { releaseCheck = $0 }
                throw FleetError.message("任务正在执行")
            }
            return Data()
        }
        let sparkle = controller.updater
        _ = updater.updater(sparkle, shouldPostponeRelaunchForUpdate: item, untilInvokingBlock: { XCTFail("Installed aborted update") })
        await updater.continueInstallation()
        let waiting = Task { await updater.waitForIdleAndInstall() }
        await fulfillment(of: [entered], timeout: 1)
        updater.updater(sparkle, didAbortWithError: FleetError.message("升级已被取消"))
        updater.updater(sparkle, didFinishUpdateCycleFor: .updates, error: nil)
        releaseCheck?.resume()
        await waiting.value
        XCTAssertFalse(updater.waitingForIdle)
        XCTAssertFalse(updater.installationNeedsAttention)
        XCTAssertEqual(updater.message, "升级已被取消")
    }

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
                         "updater:didAbortWithError:", "updater:willExtractUpdate:", "updater:didExtractUpdate:",
                         "updater:willInstallUpdate:", "updater:didFinishUpdateCycleForUpdateCheck:error:"] {
            XCTAssertTrue(updater.responds(to: NSSelectorFromString(selector)), selector)
        }
        XCTAssertEqual(updater.shouldTerminate(), .terminateNow)
    }
}
