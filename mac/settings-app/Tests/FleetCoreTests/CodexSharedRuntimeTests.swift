import Darwin
import Foundation
import XCTest
@testable import FleetCore

@MainActor
final class CodexSharedRuntimeTests: XCTestCase {
    func testNativeStartUsesOriginalKeeperAndOnlySetsDesktopEnvironmentAfterReady() async throws {
        let fixture = try SharedFixture()
        defer { fixture.remove() }
        try await fixture.runtime.start()
        let plist = try fixture.definition()
        XCTAssertEqual(plist["Label"] as? String, "com.macfleet.codex-app-server")
        XCTAssertEqual(plist["ProgramArguments"] as? [String], ["/bin/bash", fixture.resources.appendingPathComponent("codex-keeper-launch.sh").path])
        let env = try XCTUnwrap(plist["EnvironmentVariables"] as? [String: String])
        XCTAssertEqual(env["FLEET_CODEX_APPSERVER_LISTEN"], "ws://127.0.0.1:47682")
        XCTAssertEqual(env["FLEET_CODEX_APPSERVER_PROXY_SOCK"], fixture.layout.home.appendingPathComponent(".macfleet/codex-app-server.sock").path)
        XCTAssertEqual(env["FLEET_CODEX_KEEPER_NODE"], "/fixture/signed-node")
        XCTAssertLessThan(try XCTUnwrap(fixture.events.firstIndex(of: "ready")), try XCTUnwrap(fixture.events.firstIndex(of: "desktop-env")))
        XCTAssertTrue(fixture.events.contains("bootstrap"))
    }

    func testExistingSharedServiceIsReusedWithoutRestartOrEnvironmentMutation() async throws {
        let fixture = try SharedFixture()
        defer { fixture.remove() }
        fixture.loaded = true
        try await fixture.runtime.start()
        XCTAssertFalse(fixture.events.contains("bootstrap"))
        XCTAssertFalse(fixture.events.contains("bootout"))
        XCTAssertFalse(fixture.events.contains("desktop-env"))
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.definitionURL.path))
        try await fixture.runtime.remove()
        XCTAssertFalse(fixture.events.contains("bootout"))
    }

    func testStoppedOwnedKeeperCanStartAgainWithoutKillingARunningProcess() async throws {
        let fixture = try SharedFixture()
        defer { fixture.remove() }
        try await fixture.runtime.start()
        fixture.events.removeAll()
        fixture.stopped = true
        fixture.ready = false
        try await fixture.runtime.start()
        XCTAssertTrue(fixture.events.contains("kickstart"))
        XCTAssertFalse(fixture.events.contains("bootout"))
        XCTAssertTrue(fixture.events.contains("desktop-env"))
    }

    func testUnavailableOwnedListenerClearsDesktopEnvironmentWithoutRestartingIt() async throws {
        let fixture = try SharedFixture()
        defer { fixture.remove() }
        try await fixture.runtime.start()
        fixture.events.removeAll()
        fixture.ready = false
        do { try await fixture.runtime.start(); XCTFail("accepted dead listener") }
        catch {}
        XCTAssertTrue(fixture.events.contains("clear-env"))
        XCTAssertFalse(fixture.events.contains("bootout"))
        XCTAssertFalse(fixture.events.contains("kickstart"))
    }

    func testFailedReadyCheckRollsBackOnlyNewSharedServiceAndRestoresEnvironment() async throws {
        let fixture = try SharedFixture()
        defer { fixture.remove() }
        fixture.ready = false
        do { try await fixture.runtime.start(); XCTFail("accepted dead shared listener") }
        catch {}
        XCTAssertEqual(fixture.events.filter { ["bootstrap", "bootout"].contains($0) }, ["bootstrap", "bootout"])
        XCTAssertFalse(fixture.events.contains("desktop-env"))
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.definitionURL.path))
        XCTAssertTrue(fixture.events.contains("restore-env"))
    }

    func testUninstallChecksDesktopTurnsBeforeStoppingAndRestoresPreviousEnvironment() async throws {
        let fixture = try SharedFixture()
        defer { fixture.remove() }
        try await fixture.runtime.start()
        fixture.events.removeAll()
        fixture.busy = true
        do { try await fixture.runtime.remove(); XCTFail("stopped active Desktop turn") }
        catch {}
        XCTAssertFalse(fixture.events.contains("bootout"))
        XCTAssertTrue(FileManager.default.fileExists(atPath: fixture.definitionURL.path))
        fixture.busy = false
        try await fixture.runtime.remove()
        XCTAssertLessThan(try XCTUnwrap(fixture.events.firstIndex(of: "idle")), try XCTUnwrap(fixture.events.firstIndex(of: "bootout")))
        XCTAssertTrue(fixture.events.contains("restore-env"))
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.definitionURL.path))
    }

    func testMissingCodexDoesNotInstallServiceOrChangeDesktopEnvironment() async throws {
        let fixture = try SharedFixture()
        defer { fixture.remove() }
        fixture.codexInstalled = false
        try await fixture.runtime.start()
        XCTAssertFalse(fixture.events.contains("bootstrap"))
        XCTAssertFalse(fixture.events.contains("desktop-env"))
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.definitionURL.path))
    }

    func testUnhealthyAgentCannotRedirectDesktopEvenWhenSharedListenerIsReady() async throws {
        let fixture = try SharedFixture()
        defer { fixture.remove() }
        fixture.agentReady = false
        do { try await fixture.runtime.start(); XCTFail("redirected Desktop before Agent was ready") }
        catch {}
        XCTAssertFalse(fixture.events.contains("desktop-env"))
        XCTAssertTrue(fixture.events.contains("bootout"))
    }

    func testReadinessRejectsNonLoopbackListenerAndUnsafeProxy() async throws {
        for unsafe in ["listener", "proxy"] {
            let fixture = try SharedFixture()
            defer { fixture.remove() }
            fixture.loaded = true
            fixture.unsafe = unsafe
            do { try await fixture.runtime.start(); XCTFail("accepted unsafe \(unsafe)") }
            catch {}
            XCTAssertFalse(fixture.events.contains("bootout"))
            XCTAssertFalse(fixture.events.contains("desktop-env"))
        }
    }
}

@MainActor
private final class SharedFixture {
    let root: URL
    let layout: RuntimeLayout
    let resources: URL
    let definitionURL: URL
    var loaded = false
    var stopped = false
    var ready = true
    var busy = false
    var codexInstalled = true
    var agentReady = true
    var unsafe = ""
    var events: [String] = []
    lazy var runtime = CodexSharedRuntime(layout: layout, execute: { [unowned self] executable, arguments, _, _ in
        try self.execute(executable, arguments)
    }, readyAttempts: 1)

    init() throws {
        root = URL(fileURLWithPath: "/private/tmp/fleet-shared-\(UUID().uuidString)")
        layout = RuntimeLayout(application: URL(fileURLWithPath: "/Applications/Fleet Hub.app"), home: root)
        resources = layout.backgroundApplication.appendingPathComponent("Contents/Resources/codex")
        definitionURL = layout.state.appendingPathComponent("codex-app-server.plist")
        try PrivateRuntime.ensureDirectory(layout.home.appendingPathComponent(".macfleet"))
        try PrivateRuntime.ensureDirectory(layout.state)
        try FileManager.default.createDirectory(at: resources, withIntermediateDirectories: true)
        let mac = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        for file in ["codex-keeper-launch.sh", "codex-bin-resolve.sh", "codex-shared-app-server.mjs", "codex-desktop-env.sh", "check-codex-idle.sh", "com.macfleet.codex-shared-app-server.plist"] {
            try FileManager.default.copyItem(at: mac.appendingPathComponent(file), to: resources.appendingPathComponent(file))
        }
    }
    func definition() throws -> [String: Any] {
        try PropertyListSerialization.propertyList(from: PrivateRuntime.read(definitionURL), format: nil) as! [String: Any]
    }
    func remove() { try? FileManager.default.removeItem(at: root) }
    private func execute(_ executable: URL, _ arguments: [String]) throws -> Data {
        if executable.lastPathComponent == "launchctl" {
            switch arguments[0] {
            case "print":
                guard loaded else { throw FleetError.message("not loaded") }
                return Data("path = \(definitionURL.path)\nstate = \(stopped ? "not running" : "running")\n".utf8)
            case "bootstrap": loaded = true; events.append("bootstrap")
            case "bootout": loaded = false; events.append("bootout")
            case "kickstart":
                XCTAssertFalse(arguments.contains("-k"))
                stopped = false; ready = true; events.append("kickstart")
            case "getenv": return Data("previous-value\n".utf8)
            case "setenv", "unsetenv": events.append("restore-env")
            default: XCTFail("unexpected launchctl operation")
            }
            return Data()
        }
        if executable.lastPathComponent == "curl" {
            guard ready else { throw FleetError.message("connection refused") }
            events.append("ready"); return Data()
        }
        if executable.lastPathComponent == "lsof" {
            return Data("codex 123 user TCP \(unsafe == "listener" ? "*" : "127.0.0.1"):47682 (LISTEN)\n".utf8)
        }
        if executable.lastPathComponent == "stat" { return Data("\(getuid()) \(unsafe == "proxy" ? "666" : "600") Socket\n".utf8) }
        if arguments.contains("--keeper-node") {
            guard codexInstalled else { throw FleetError.message("Codex unavailable") }
            return Data("/fixture/signed-node\n".utf8)
        }
        if arguments.suffix(2) == ["desktop", "status"] {
            if !agentReady { throw FleetError.message("Agent not ready") }
            return Data("{\"schema\":1,\"pid\":123,\"version\":\"1.0.0+2\",\"settings\":{\"schema\":1,\"origin\":\"\",\"auto_start\":true},\"disk_access\":{\"state\":\"unknown\",\"source\":\"background\",\"checked_at\":1,\"verified_targets\":0},\"runtime\":{\"phase\":\"unbound\"}}".utf8)
        }
        if arguments.contains(where: { $0.hasSuffix("check-codex-idle.sh") }) {
            events.append("idle")
            if busy { throw FleetError.message("active_or_unknown") }
            return Data("codex_turns_idle\n".utf8)
        }
        if arguments.contains(where: { $0.hasSuffix("codex-desktop-env.sh") }) {
            events.append(arguments.last == "clear" ? "clear-env" : "desktop-env"); return Data()
        }
        XCTFail("unexpected command \(executable) \(arguments)")
        return Data()
    }
}
