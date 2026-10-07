import Foundation
import XCTest
@testable import FleetCore

@MainActor
final class ManagementSettingsTests: XCTestCase {
    func testLoginApprovalFailureRollsBackSavedBackgroundSettings() async throws {
        let original = FleetSettings(schema: 1, origin: "https://fleet.example.test", autoStart: true)
        var effective = original
        let layout = RuntimeLayout(application: URL(fileURLWithPath: "/Applications/Fleet Hub.app"), home: URL(fileURLWithPath: "/Users/fixture"))
        let management = NativeManagement(layout: layout, configureAutoStart: { _ in throw FleetError.message("approval denied") }, execute: { _, arguments, input, _ in
            if arguments.last == "status" {
                return try JSONEncoder().encode(AgentStatus(schema: 1, pid: 123, version: "1", settings: effective, binding: nil,
                    diskAccess: DiskAccess(state: "unknown", source: "background", checkedAt: 0, verifiedTargets: 0)))
            }
            effective = try JSONDecoder().decode(FleetSettings.self, from: XCTUnwrap(input))
            return try JSONEncoder().encode(effective)
        })
        do {
            _ = try await management.save(FleetSettings(schema: 1, origin: original.origin, autoStart: false))
            XCTFail("reported system approval failure as saved")
        } catch { XCTAssertEqual(error.localizedDescription, "approval denied") }
        XCTAssertEqual(effective, original)
    }
}
