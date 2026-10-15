import Foundation
import XCTest
@testable import FleetHub

@MainActor
final class SettingsStartupTests: XCTestCase {
    func testLocalChecksFinishBeforeSettingsAppear() async {
        let startup = SettingsStartup()
        XCTAssertEqual(startup.state, .checking(.settings))
        var events: [String] = []
        let success = await startup.run(recover: {
            events.append("settings")
            XCTAssertEqual(startup.state, .checking(.settings))
        }, prepare: {
            events.append("background")
            XCTAssertEqual(startup.state, .checking(.background))
        }, load: {
            await Task.yield()
            events.append("status")
            XCTAssertEqual(startup.state, .checking(.status))
            XCTAssertFalse(startup.state.isReady)
        })
        XCTAssertTrue(success)
        XCTAssertEqual(events, ["settings", "background", "status"])
        XCTAssertEqual(startup.state, .ready)
    }

    func testFailureShowsTheReasonAndRetryRunsFreshLocalChecks() async {
        let startup = SettingsStartup()
        var loaded = false
        let failure = NSError(domain: "startup", code: 1, userInfo: [NSLocalizedDescriptionKey: "后台验证失败"])
        let failed = await startup.run(recover: {}, prepare: { throw failure }, load: { loaded = true })
        XCTAssertFalse(failed)
        XCTAssertFalse(loaded)
        XCTAssertEqual(startup.state, .failed("后台验证失败"))
        XCTAssertTrue(startup.requestRetry())
        XCTAssertFalse(startup.requestRetry())
        XCTAssertEqual(startup.state, .checking(.settings))
        let retried = await startup.run(recover: {}, prepare: {}, load: { loaded = true })
        XCTAssertTrue(retried)
        XCTAssertTrue(loaded)
        XCTAssertEqual(startup.state, .ready)
    }

    func testAnotherCheckCannotStartDuringPreparation() async {
        let startup = SettingsStartup()
        let success = await startup.run(recover: {}, prepare: {
            let duplicate = await startup.run(recover: { XCTFail("duplicated recovery") }, prepare: { XCTFail("duplicated preparation") }, load: { XCTFail("duplicated status load") })
            XCTAssertFalse(duplicate)
            XCTAssertFalse(startup.requestRetry())
            startup.showSettingsAfterFailure()
            XCTAssertEqual(startup.state, .checking(.background))
        }, load: {})
        XCTAssertTrue(success)
    }

    func testCancelledCheckCanBeRetriedWithoutReportingReady() async {
        let startup = SettingsStartup()
        let launch = Task {
            await startup.run(recover: {}, prepare: { throw CancellationError() }, load: { XCTFail("continued cancelled startup") })
        }
        let success = await launch.value
        XCTAssertFalse(success)
        XCTAssertFalse(startup.state.isReady)
        XCTAssertTrue(startup.requestRetry())
    }

    func testFailureCanBeInspectedWithoutRunningAnotherCheck() async {
        let startup = SettingsStartup()
        _ = await startup.run(recover: { throw NSError(domain: "startup", code: 1) }, prepare: { XCTFail("continued failed startup") }, load: {})
        startup.showSettingsAfterFailure()
        XCTAssertEqual(startup.state, .ready)
    }
}
