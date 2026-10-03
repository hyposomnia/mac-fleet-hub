import XCTest
@testable import FleetCore

@MainActor
final class UninstallPreparationTests: XCTestCase {
    func testStoppedServiceStartsBeforeReadingBindingAndCheckingIdle() async throws {
        var running = false
        var actions: [String] = []
        let status = try await UninstallPreparation.prepare(status: {
            actions.append("status")
            guard running else { throw FleetError.message("not running") }
            return try self.fixture()
        }, start: { actions.append("start"); running = true }, checkIdle: { actions.append("idle") })
        XCTAssertEqual(actions, ["status", "start", "status", "idle"])
        XCTAssertEqual(status.binding?.deviceID, "m1")
    }

    func testRunningServiceIsNotRestartedAndBusyServiceBlocksUninstall() async {
        do {
            _ = try await UninstallPreparation.prepare(status: { try self.fixture() },
                                                      start: { XCTFail("restarted running service") },
                                                      checkIdle: { throw FleetError.message("busy") })
            XCTFail("accepted busy service")
        } catch {}
    }

    func testStartupFailureDoesNotProceedWithUninstall() async {
        do {
            _ = try await UninstallPreparation.prepare(status: { throw FleetError.message("unreachable") },
                                                      start: { throw FleetError.message("cannot start") },
                                                      checkIdle: { XCTFail("continued after failed startup") })
            XCTFail("accepted unreachable service")
        } catch {}
    }

    private func fixture() throws -> AgentStatus {
        try JSONDecoder().decode(AgentStatus.self, from: Data("""
        {"schema":1,"pid":20,"version":"1.0.0+2","settings":{"schema":1,"origin":"https://fleet.example.test","auto_start":true},"disk_access":{"state":"unknown","source":"background","checked_at":0,"verified_targets":0},"binding":{"device_id":"m1","owner_email":"owner@example.test","origin":"https://fleet.example.test","complete":true,"locked":false}}
        """.utf8))
    }
}
