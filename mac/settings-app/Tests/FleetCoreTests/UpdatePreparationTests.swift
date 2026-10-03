import XCTest
@testable import FleetCore

@MainActor
final class UpdatePreparationTests: XCTestCase {
    func testUpdateCannotStopServiceBeforeIdleCheckAndBackup() async throws {
        let preparation = UpdatePreparation()
        var actions: [String] = []
        try await preparation.prepare(check: { actions.append("idle") }, backup: { actions.append("backup") }, stop: { actions.append("stop") }, resume: { actions.append("resume") })
        XCTAssertEqual(actions, ["idle", "backup", "stop"])
        XCTAssertTrue(preparation.isPrepared)
        try await preparation.prepare(check: { XCTFail("duplicate check") }, backup: { XCTFail("duplicate backup") }, stop: { XCTFail("duplicate stop") }, resume: {})
    }

    func testBackupFailureResumesAndDoesNotAllowInstallation() async {
        let preparation = UpdatePreparation()
        var actions: [String] = []
        do {
            try await preparation.prepare(check: { actions.append("idle") }, backup: { throw FleetError.message("disk full") }, stop: { actions.append("stop") }, resume: { actions.append("resume") })
            XCTFail("accepted failed backup")
        } catch {}
        XCTAssertEqual(actions, ["idle", "resume"])
        XCTAssertFalse(preparation.isPrepared)
    }

    func testBusyServicePreventsBackupAndStop() async {
        let preparation = UpdatePreparation()
        do {
            try await preparation.prepare(check: { throw FleetError.message("active turn") }, backup: { XCTFail("backup while busy") }, stop: { XCTFail("stopped active turn") }, resume: { XCTFail("released a guard owned by another operation") })
            XCTFail("accepted busy service")
        } catch {}
        XCTAssertFalse(preparation.isPrepared)
    }

    func testCancellingDuringBackupDoesNotStopTheService() async {
        let preparation = UpdatePreparation()
        var resumed = false
        do {
            try await preparation.prepare(check: {}, backup: { preparation.cancel() },
                                          stop: { XCTFail("stopped service after cancellation") },
                                          resume: { resumed = true })
            XCTFail("accepted cancelled update")
        } catch {}
        XCTAssertTrue(resumed)
        XCTAssertFalse(preparation.isPrepared)
    }

    func testCancellingWhileStopCompletesRestoresService() async {
        let preparation = UpdatePreparation()
        var restored = false
        do {
            try await preparation.prepare(check: {}, backup: {}, stop: { preparation.cancel() },
                                          resume: { restored = true })
            XCTFail("installed cancelled update")
        } catch {}
        XCTAssertTrue(restored)
        XCTAssertFalse(preparation.isPrepared)
    }
}
