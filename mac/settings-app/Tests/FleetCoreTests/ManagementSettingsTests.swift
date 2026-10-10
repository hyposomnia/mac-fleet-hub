import Foundation
import XCTest
@testable import FleetCore

@MainActor
final class ManagementSettingsTests: XCTestCase {
    func testPreviewSwitchControlsTheExistingSignedLoginHelperWithoutRestartingTheAgent() async throws {
        let fixture = try RegisteredLoginFixture()
        defer { try? FileManager.default.removeItem(at: fixture.home) }
        let management = fixture.management()
        for enabled in [false, true] {
            let saved = try await management.save(FleetSettings(schema: 1, origin: fixture.settings.origin, autoStart: enabled))
            XCTAssertEqual(saved.autoStart, enabled)
            XCTAssertEqual(fixture.settings.autoStart, enabled)
            XCTAssertEqual(fixture.disabled, !enabled)
        }
        XCTAssertTrue(fixture.commands.contains { $0.first == "disable" })
        XCTAssertTrue(fixture.commands.contains { $0.first == "enable" })
        XCTAssertFalse(fixture.commands.contains { ["bootstrap", "bootout", "kickstart"].contains($0.first ?? "") })
    }

    func testPreviewSwitchRejectsALoginHelperFromAnotherApplicationAndRestoresSettings() async throws {
        let fixture = try RegisteredLoginFixture()
        defer { try? FileManager.default.removeItem(at: fixture.home) }
        fixture.owner = "another.application"
        do {
            _ = try await fixture.management().save(FleetSettings(schema: 1, origin: fixture.settings.origin, autoStart: false))
            XCTFail("accepted another application's login helper")
        } catch { XCTAssertTrue(error.localizedDescription.contains("不属于")) }
        XCTAssertTrue(fixture.settings.autoStart)
        XCTAssertFalse(fixture.disabled)
        XCTAssertFalse(fixture.commands.contains { $0.first == "disable" })
    }

    func testPreviewSwitchVerifiesTheSystemResultAndRollsBackAFailedChange() async throws {
        let fixture = try RegisteredLoginFixture()
        defer { try? FileManager.default.removeItem(at: fixture.home) }
        fixture.appliesChanges = false
        do {
            _ = try await fixture.management().save(FleetSettings(schema: 1, origin: fixture.settings.origin, autoStart: false))
            XCTFail("reported an ineffective login setting as saved")
        } catch { XCTAssertEqual(error.localizedDescription, "登录启动设置未生效。") }
        XCTAssertTrue(fixture.settings.autoStart)
        XCTAssertFalse(fixture.disabled)
        XCTAssertTrue(fixture.commands.contains { $0.first == "enable" })
    }

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

@MainActor
private final class RegisteredLoginFixture {
    let home: URL
    let layout: RuntimeLayout
    var settings = FleetSettings(schema: 1, origin: "https://fleet.example.test", autoStart: true)
    var disabled = false
    var owner = "com.macfleet.fleet-hub"
    var appliesChanges = true
    var commands: [[String]] = []

    init() throws {
        home = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        layout = RuntimeLayout(application: URL(fileURLWithPath: "/Applications/Fleet Hub.app"), home: home)
        try FileManager.default.createDirectory(at: layout.state, withIntermediateDirectories: true)
        try Data("fixture".utf8).write(to: layout.runtimePlist)
    }

    func management() -> NativeManagement {
        NativeManagement(layout: layout, controlsRegisteredLoginService: true, execute: { _, arguments, input, _ in
            if arguments == ["desktop", "status"] {
                return try JSONEncoder().encode(AgentStatus(schema: 1, pid: 123, version: "1", settings: self.settings, binding: nil,
                    diskAccess: DiskAccess(state: "unknown", source: "background", checkedAt: 0, verifiedTargets: 0)))
            }
            if arguments == ["desktop", "settings"] {
                self.settings = try JSONDecoder().decode(FleetSettings.self, from: XCTUnwrap(input))
                return try JSONEncoder().encode(self.settings)
            }
            self.commands.append(arguments)
            switch arguments.first {
            case "print": return Data("parent bundle identifier = \(self.owner)".utf8)
            case "print-disabled": return Data("\"com.macfleet.desktop-login\" => \(self.disabled ? "disabled" : "enabled")".utf8)
            case "disable", "enable":
                if self.appliesChanges { self.disabled = arguments.first == "disable" }
                return Data()
            default: throw FleetError.message("unexpected operation: \(arguments)")
            }
        })
    }
}
