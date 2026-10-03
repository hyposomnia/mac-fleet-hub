import Foundation
import XCTest
@testable import FleetCore

final class RuntimeTests: XCTestCase {
    func testPrivateRuntimeRejectsSymlinksAndKeepsPermissions() throws {
        let directory = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let file = directory.appendingPathComponent("agent.plist")
        try PrivateRuntime.write(Data("private".utf8), to: file)
        let attributes = try FileManager.default.attributesOfItem(atPath: file.path)
        XCTAssertEqual(attributes[.posixPermissions] as? NSNumber, 0o600)
        let target = directory.appendingPathComponent("target")
        try Data("keep".utf8).write(to: target)
        try FileManager.default.removeItem(at: file)
        try FileManager.default.createSymbolicLink(at: file, withDestinationURL: target)
        XCTAssertThrowsError(try PrivateRuntime.write(Data("replace".utf8), to: file))
        XCTAssertEqual(try String(contentsOf: target), "keep")
    }
    func testLaunchDefinitionKeepsDesktopAndSecretsOutOfEnvironment() throws {
        let layout = RuntimeLayout(application: URL(fileURLWithPath: "/Applications/Fleet Hub.app"), home: URL(fileURLWithPath: "/Users/fixture"))
        let definition = try layout.launchDefinition()
        let plist = try XCTUnwrap(PropertyListSerialization.propertyList(from: definition, format: nil) as? [String: Any])
        XCTAssertEqual(plist["Label"] as? String, "com.macfleet.desktop-agent")
        XCTAssertEqual(plist["ProgramArguments"] as? [String], ["/Applications/Fleet Hub.app/Contents/Library/LoginItems/Fleet Agent.app/Contents/MacOS/fleet-agent"])
        let environment = try XCTUnwrap(plist["EnvironmentVariables"] as? [String: String])
        XCTAssertEqual(environment["FLEET_DESKTOP_MANAGED"], "1")
        XCTAssertEqual(environment["FLEET_BINDING_FILE"], "/Users/fixture/.macfleet/desktop/binding.json")
        XCTAssertTrue(environment["PATH"]?.hasPrefix("/Applications/Fleet Hub.app/Contents/Library/LoginItems/Fleet Agent.app/Contents/Resources/bin:") == true)
        XCTAssertEqual(environment["FLEET_CODEX_DESKTOP_SHARED_DAEMON"], "0")
        XCTAssertNil(environment["CODEX_APP_SERVER_WS_URL"])
        XCTAssertNil(environment["FLEET_DEVICE_TOKEN"])
        XCTAssertEqual(environment["LANG"], "en_US.UTF-8")
        XCTAssertFalse(layout.runtimePlist.path.contains("Library/LaunchAgents"))
    }

    func testRuntimeDoesNotRunFromReadOnlyDMGOrSourceDirectory() throws {
        for application in ["/Volumes/Fleet Hub/Fleet Hub.app", "/Users/fixture/Downloads/Fleet Hub.app", "/Applications/Other.app", "/tmp/Fleet Hub.app"] {
            let layout = RuntimeLayout(application: URL(fileURLWithPath: application), home: URL(fileURLWithPath: "/Users/fixture"))
            XCTAssertThrowsError(try layout.launchDefinition())
        }
    }

    func testProcessRunnerDoesNotTreatCommandFailureOrTimeoutAsSuccess() async throws {
        let successful = try await CommandRunner.run(executable: URL(fileURLWithPath: "/usr/bin/printf"), arguments: ["actual-output"], timeout: 2)
        XCTAssertEqual(String(data: successful, encoding: .utf8), "actual-output")
        do {
            _ = try await CommandRunner.run(executable: URL(fileURLWithPath: "/usr/bin/false"), arguments: [], timeout: 2)
            XCTFail("reported failed command as successful")
        } catch {}
        let start = Date()
        do {
            _ = try await CommandRunner.run(executable: URL(fileURLWithPath: "/bin/sleep"), arguments: ["10"], timeout: 0.1)
            XCTFail("did not time out")
        } catch {}
        XCTAssertLessThan(Date().timeIntervalSince(start), 3)
    }
}
