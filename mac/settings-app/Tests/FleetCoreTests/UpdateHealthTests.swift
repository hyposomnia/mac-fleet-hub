import XCTest
@testable import FleetCore

final class UpdateHealthTests: XCTestCase {
    func testRecoveryRequiresMatchingAgentVersionAndNewPID() throws {
        var status = try fixture()
        XCTAssertTrue(status.isHealthyAfterUpdate(version: "1.0.0", build: 2, replacingPID: 10))
        XCTAssertFalse(status.isHealthyAfterUpdate(version: "1.0.0", build: 3, replacingPID: 10))
        XCTAssertFalse(status.isHealthyAfterUpdate(version: "1.0.0", build: 2, replacingPID: 20))
        status.version = "dev"
        XCTAssertFalse(status.isHealthyAfterUpdate(version: "1.0.0", build: 2, replacingPID: 10))
    }

    func testRecoveryDoesNotAcceptInitialUnboundStatusForRegisteredDevice() throws {
        var status = try fixture()
        status.binding = BindingStatus(deviceID: "m1", ownerEmail: "owner@example.test", origin: "https://fleet.example.test", complete: true, locked: false)
        XCTAssertFalse(status.isHealthyAfterUpdate(version: "1.0.0", build: 2, replacingPID: 10))
        status.runtime = RuntimeState(phase: "running")
        XCTAssertTrue(status.isHealthyAfterUpdate(version: "1.0.0", build: 2, replacingPID: 10))
        status.binding?.locked = true
        XCTAssertFalse(status.isHealthyAfterUpdate(version: "1.0.0", build: 2, replacingPID: 10))
    }

    private func fixture() throws -> AgentStatus {
        try JSONDecoder().decode(AgentStatus.self, from: Data("""
        {"schema":1,"pid":20,"version":"1.0.0+2","settings":{"schema":1,"origin":"https://fleet.example.test","auto_start":true},"disk_access":{"state":"unknown","source":"background","checked_at":0,"verified_targets":0},"runtime":{"phase":"unbound"}}
        """.utf8))
    }
}
