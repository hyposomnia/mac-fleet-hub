import XCTest
@testable import FleetCore

@MainActor
final class MaintenanceWaitTests: XCTestCase {
    func testExplicitWaitRetriesBusyGuardAndRunsOnceWhenIdle() async throws {
        let wait = MaintenanceWait()
        var attempts = 0
        var actions = 0
        try await wait.run(check: {
            attempts += 1
            if attempts < 3 { throw FleetError.message("任务正在执行") }
        }, pause: {}, action: { actions += 1 })
        XCTAssertEqual(attempts, 3)
        XCTAssertEqual(actions, 1)
    }
    func testCancelWhileWaitingNeverRunsMaintenance() async throws {
        let wait = MaintenanceWait()
        do {
            try await wait.run(check: { throw FleetError.message("任务正在执行") }, pause: { wait.cancel() }, action: { XCTFail("cancelled work executed") })
            XCTFail("cancel ignored")
        } catch is CancellationError {} catch { XCTFail("unexpected error: \(error)") }
    }
}
