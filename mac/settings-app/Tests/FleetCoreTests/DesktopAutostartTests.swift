import Foundation
import XCTest
@testable import FleetCore

@MainActor
final class DesktopAutostartTests: XCTestCase {
    func testLauncherOnlyBootstrapsVerifiedIndependentRuntime() async throws {
        let fixture = try fixture()
        defer { try? FileManager.default.removeItem(at: fixture.home) }
        var verified = false
        var commands: [[String]] = []
        var sharedStarted = false
        try await DesktopAutostart.start(layout: fixture, verify: { application in
            XCTAssertEqual(application, fixture.backgroundApplication)
            verified = true
        }, prepareCodex: {
            XCTAssertTrue(verified)
            XCTAssertEqual(commands.last?.first, "bootstrap")
            sharedStarted = true
        }, launch: { arguments in
            commands.append(arguments)
            if arguments[0] == "print" { throw FleetError.message("not loaded") }
            XCTAssertTrue(verified)
        })
        XCTAssertEqual(commands.last, ["bootstrap", "gui/\(getuid())", fixture.runtimePlist.path])
        XCTAssertEqual(commands.count, 3)
        XCTAssertTrue(sharedStarted)
    }

    func testLauncherPreparesSharedCodexWhenAgentIsAlreadyLoaded() async throws {
        let fixture = try fixture()
        defer { try? FileManager.default.removeItem(at: fixture.home) }
        var sharedStarted = false
        try await DesktopAutostart.start(layout: fixture, verify: { _ in }, prepareCodex: { sharedStarted = true }, launch: { arguments in
            if arguments.last?.hasSuffix("/com.macfleet.fleet-agent") == true { throw FleetError.message("not loaded") }
            XCTAssertEqual(arguments.first, "print")
        })
        XCTAssertTrue(sharedStarted)
    }

    func testLauncherRejectsLegacyNestedAndForeignProgramDefinitions() async throws {
        let fixture = try fixture()
        defer { try? FileManager.default.removeItem(at: fixture.home) }
        for program in [fixture.bundledAgent.path, "/bin/sh"] {
            var values = try PropertyListSerialization.propertyList(from: fixture.launchDefinition(), format: nil) as! [String: Any]
            values["ProgramArguments"] = [program]
            try PrivateRuntime.write(PropertyListSerialization.data(fromPropertyList: values, format: .xml, options: 0), to: fixture.runtimePlist)
            do {
                try await DesktopAutostart.start(layout: fixture, verify: { _ in XCTFail("verified unsafe definition") }, launch: { _ in XCTFail("loaded unsafe definition") })
                XCTFail("accepted nested or foreign program")
            } catch {}
        }
    }

    func testLauncherDoesNotLoadUntrustedRuntimeOrReplaceLegacyFleet() async throws {
        let fixture = try fixture()
        defer { try? FileManager.default.removeItem(at: fixture.home) }
        do {
            try await DesktopAutostart.start(layout: fixture, verify: { _ in throw FleetError.message("untrusted") }, launch: { _ in XCTFail("loaded untrusted runtime") })
            XCTFail("accepted untrusted runtime")
        } catch {}
        var bootstrapped = false
        do {
            try await DesktopAutostart.start(layout: fixture, verify: { _ in }, launch: { arguments in
                if arguments[0] == "bootstrap" { bootstrapped = true }
            })
            XCTFail("replaced legacy Fleet")
        } catch {}
        XCTAssertFalse(bootstrapped)
    }

    private func fixture() throws -> RuntimeLayout {
        let home = FileManager.default.temporaryDirectory.appendingPathComponent("fleet-login-\(UUID().uuidString)")
        let layout = RuntimeLayout(application: URL(fileURLWithPath: "/Applications/Fleet Hub.app"), home: home)
        try PrivateRuntime.ensureDirectory(home.appendingPathComponent(".macfleet"))
        try PrivateRuntime.ensureDirectory(layout.state)
        try PrivateRuntime.write(layout.launchDefinition(), to: layout.runtimePlist)
        return layout
    }
}
