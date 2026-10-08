import Foundation
import XCTest
@testable import FleetCore

@MainActor
final class NativeRuntimeTests: XCTestCase {
    func testBackgroundVerificationPassesInlineDeveloperIDRequirement() async throws {
        let fixture = try NativeRuntimeFixture(running: false)
        defer { fixture.remove() }
        try await fixture.management.synchronizeBackground(launch: false)
        let expected = "=identifier \"com.macfleet.fleet-agent\" and anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate leaf[subject.OU] = \"FAKETEAM01\""
        XCTAssertFalse(fixture.requirements.isEmpty)
        XCTAssertTrue(fixture.requirements.allSatisfy { $0 == expected })
    }

    func testFirstPreparationInstallsAgentOutsideHubWithoutStartingIt() async throws {
        let fixture = try NativeRuntimeFixture(running: false)
        defer { fixture.remove() }
        try await fixture.management.synchronizeBackground(launch: false)
        XCTAssertFalse(fixture.running)
        XCTAssertEqual(try fixture.identity(fixture.layout.backgroundApplication), "2")
        let definition = try fixture.definition()
        XCTAssertEqual(definition["ProgramArguments"] as? [String], [fixture.layout.agent.path])
        XCTAssertFalse(fixture.events.contains("bootstrap"))
        XCTAssertTrue(fixture.requirements.allSatisfy { $0.contains("com.macfleet.fleet-agent") && $0.contains("FAKETEAM01") })
    }

    func testMigratesRunningNestedAgentOnlyAfterIdleCheck() async throws {
        let fixture = try NativeRuntimeFixture(running: true, nested: true)
        defer { fixture.remove() }
        try await fixture.management.synchronizeBackground(launch: true)
        XCTAssertEqual(fixture.events.filter { ["prepare-stop", "bootout", "bootstrap"].contains($0) }, ["prepare-stop", "bootout", "bootstrap"])
        XCTAssertTrue(fixture.running)
        XCTAssertEqual(fixture.pid, 200)
        XCTAssertEqual(try fixture.definition()["ProgramArguments"] as? [String], [fixture.layout.agent.path])
        XCTAssertEqual(try fixture.identity(fixture.layout.backgroundApplication), "2")
    }

    func testMigrationBusyGuardKeepsOldProgramAndProcess() async throws {
        let fixture = try NativeRuntimeFixture(running: true, nested: true)
        defer { fixture.remove() }
        fixture.busy = true
        do { try await fixture.management.synchronizeBackground(launch: true); XCTFail("stopped busy agent") }
        catch {}
        XCTAssertTrue(fixture.running)
        XCTAssertEqual(fixture.pid, 100)
        XCTAssertFalse(fixture.events.contains("bootout"))
        XCTAssertEqual(try fixture.definition()["ProgramArguments"] as? [String], [fixture.layout.bundledAgent.path])
    }

    func testFailedUpgradeRestoresOldRuntimeAndLaunchDefinition() async throws {
        let fixture = try NativeRuntimeFixture(running: true)
        defer { fixture.remove() }
        fixture.rejectNew = true
        do { try await fixture.management.synchronizeBackground(launch: true); XCTFail("accepted unhealthy runtime") }
        catch {}
        XCTAssertTrue(fixture.running)
        XCTAssertEqual(fixture.events.filter { ["prepare-stop", "bootout", "bootstrap"].contains($0) },
                       ["prepare-stop", "bootout", "bootstrap", "prepare-stop", "bootout", "bootstrap"])
        XCTAssertEqual(fixture.version, "1.0.0+1")
        XCTAssertEqual(try fixture.identity(fixture.layout.backgroundApplication), "1")
        XCTAssertEqual(try fixture.definition()["ProgramArguments"] as? [String], [fixture.layout.agent.path])
    }

    func testMatchingRuntimeDoesNotRestartOnEverySettingsLaunch() async throws {
        let fixture = try NativeRuntimeFixture(running: false)
        defer { fixture.remove() }
        try await fixture.management.synchronizeBackground(launch: true)
        fixture.events.removeAll()
        try await fixture.management.synchronizeBackground(launch: true)
        XCTAssertFalse(fixture.events.contains("bootout"))
        XCTAssertFalse(fixture.events.contains("bootstrap"))
        XCTAssertEqual(fixture.pid, 200)
    }

    func testMatchingVersionSymlinkCannotRemainRunningInsideHub() async throws {
        let fixture = try NativeRuntimeFixture(running: false)
        defer { fixture.remove() }
        try await fixture.management.synchronizeBackground(launch: true)
        try FileManager.default.removeItem(at: fixture.layout.backgroundApplication)
        try FileManager.default.createSymbolicLink(at: fixture.layout.backgroundApplication, withDestinationURL: fixture.layout.bundledBackgroundApplication)
        fixture.events.removeAll()
        do { try await fixture.management.synchronizeBackground(launch: true); XCTFail("accepted nested runtime symlink") }
        catch {}
        XCTAssertFalse(fixture.events.contains("bootout"))
    }
}

@MainActor
private final class NativeRuntimeFixture {
    let root: URL
    let layout: RuntimeLayout
    var running: Bool
    var pid = 100
    var version: String
    var busy = false
    var rejectNew = false
    var events: [String] = []
    var requirements: [String] = []
    lazy var management = NativeManagement(layout: layout, execute: { [unowned self] executable, arguments, _, _ in
        try self.execute(executable, arguments)
    })

    init(running: Bool, nested: Bool = false) throws {
        root = FileManager.default.temporaryDirectory.appendingPathComponent("fleet-native-\(UUID().uuidString)")
        layout = RuntimeLayout(application: root.appendingPathComponent("Fleet Hub.app"), home: root.appendingPathComponent("home"))
        self.running = running
        version = nested ? "1.0.0+2" : "1.0.0+1"
        try bundle(layout.application, identifier: "com.macfleet.fleet-hub", build: "2")
        try bundle(layout.bundledBackgroundApplication, identifier: "com.macfleet.fleet-agent", build: "2")
        try PrivateRuntime.ensureDirectory(layout.home.appendingPathComponent(".macfleet"))
        try PrivateRuntime.ensureDirectory(layout.state)
        if running {
            if !nested {
                try PrivateRuntime.ensureDirectory(layout.runtimeDirectory)
                try bundle(layout.backgroundApplication, identifier: "com.macfleet.fleet-agent", build: "1")
            }
            var definition = try PropertyListSerialization.propertyList(from: layout.launchDefinitionForTesting(), format: nil) as! [String: Any]
            if nested { definition["ProgramArguments"] = [layout.bundledAgent.path] }
            try PrivateRuntime.write(PropertyListSerialization.data(fromPropertyList: definition, format: .xml, options: 0), to: layout.runtimePlist)
        }
    }

    private func bundle(_ application: URL, identifier: String, build: String) throws {
        let contents = application.appendingPathComponent("Contents")
        try FileManager.default.createDirectory(at: contents, withIntermediateDirectories: true)
        let metadata = ["CFBundleIdentifier": identifier, "CFBundleShortVersionString": "1.0.0", "CFBundleVersion": build]
        try PropertyListSerialization.data(fromPropertyList: metadata, format: .xml, options: 0).write(to: contents.appendingPathComponent("Info.plist"))
    }

    func identity(_ application: URL) throws -> String {
        let metadata = try PropertyListSerialization.propertyList(from: Data(contentsOf: application.appendingPathComponent("Contents/Info.plist")), format: nil) as! [String: Any]
        return metadata["CFBundleVersion"] as! String
    }
    func definition() throws -> [String: Any] {
        try PropertyListSerialization.propertyList(from: PrivateRuntime.read(layout.runtimePlist), format: nil) as! [String: Any]
    }
    func remove() { try? FileManager.default.removeItem(at: root) }

    private func execute(_ executable: URL, _ arguments: [String]) throws -> Data {
        if executable.lastPathComponent == "codesign" {
            if arguments.contains("-d") { return Data("TeamIdentifier=FAKETEAM01\n".utf8) }
            if let position = arguments.firstIndex(of: "-R") { requirements.append(arguments[position + 1]) }
            return Data()
        }
        if executable.lastPathComponent == "spctl" { return Data() }
        if executable.lastPathComponent == "launchctl" {
            let action = arguments[0]
            if action == "print" {
                if arguments.last?.hasSuffix("/com.macfleet.fleet-agent") == true || !running { throw FleetError.message("not loaded") }
                return Data()
            }
            events.append(action)
            if action == "bootout" { running = false }
            if action == "bootstrap" {
                let program = (try definition()["ProgramArguments"] as! [String])[0]
                if program == layout.agent.path { version = "1.0.0+\(try identity(layout.backgroundApplication))" }
                else { version = "1.0.0+2" }
                running = true
                pid = pid == 100 ? 200 : 300
            }
            return Data()
        }
        guard arguments.first == "desktop", running else { throw FleetError.message("offline") }
        let action = arguments[1]
        events.append(action)
        if action == "prepare-stop", busy { throw FleetError.message("active turn") }
        if action != "status" { return Data("{}".utf8) }
        let reported = rejectNew && version == "1.0.0+2" ? "wrong-version" : version
        let current = AgentStatus(schema: 1, pid: pid, version: reported,
            settings: FleetSettings(schema: 1, origin: "https://fleet.example.test", autoStart: true),
            diskAccess: DiskAccess(state: "unknown", source: "background", checkedAt: 1, verifiedTargets: 0),
            runtime: RuntimeState(phase: "unbound"))
        return try JSONEncoder().encode(current)
    }
}

private extension RuntimeLayout {
    func launchDefinitionForTesting() throws -> Data {
        let installed = RuntimeLayout(application: URL(fileURLWithPath: "/Applications/Fleet Hub.app"), home: home)
        return try installed.launchDefinition()
    }
}
