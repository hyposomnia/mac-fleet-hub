import Darwin
import Foundation
import XCTest
@testable import FleetCore

@MainActor
final class CodexSharedRuntimeTests: XCTestCase {
    func testDesktopHelperIsLoadedIntoAquaRatherThanTheCallersBootstrapDomain() async throws {
        let fixture = try SharedFixture()
        defer { fixture.remove() }
        try await fixture.runtime.start()
        let url = fixture.layout.state.appendingPathComponent("codex-desktop-env.plist")
        let plist = try PropertyListSerialization.propertyList(from: PrivateRuntime.read(url), format: nil) as! [String: Any]
        XCTAssertEqual(plist["Label"] as? String, "com.macfleet.codex-desktop-env")
        XCTAssertEqual(plist["LimitLoadToSessionType"] as? String, "Aqua")
        XCTAssertEqual(plist["ProgramArguments"] as? [String], ["/bin/bash", fixture.resources.appendingPathComponent("codex-desktop-env.sh").path, "shared", "ws://127.0.0.1:47682/rpc"])
        XCTAssertTrue(fixture.events.contains("aqua-bootstrap"))
    }

    func testSnapshotKeepsAquaAndUserEnvironmentsSeparate() async throws {
        let fixture = try SharedFixture()
        defer { fixture.remove() }
        try await fixture.runtime.start()
        let snapshot = try JSONSerialization.jsonObject(with: PrivateRuntime.read(fixture.layout.state.appendingPathComponent("codex-desktop-environment.json"))) as! [String: Any]
        let aqua = try XCTUnwrap(snapshot["aqua"] as? [String: String])
        let user = try XCTUnwrap(snapshot["user"] as? [String: String])
        XCTAssertEqual(aqua["CODEX_APP_SERVER_USE_LOCAL_DAEMON"], "1")
        XCTAssertEqual(user["CODEX_APP_SERVER_USE_LOCAL_DAEMON"], "")
        try await fixture.runtime.remove()
        XCTAssertEqual(fixture.aquaEnvironment["CODEX_APP_SERVER_USE_LOCAL_DAEMON"], "1")
        XCTAssertEqual(fixture.aquaEnvironment["CODEX_APP_SERVER_WS_URL"], "")
        XCTAssertEqual(fixture.userEnvironment["CODEX_APP_SERVER_USE_LOCAL_DAEMON"], "")
    }

    func testLegacySnapshotCanBeRestoredAfterAquaUpgrade() async throws {
        let fixture = try SharedFixture()
        defer { fixture.remove() }
        let snapshot = fixture.layout.state.appendingPathComponent("codex-desktop-environment.json")
        try PrivateRuntime.write(Data("{\"CODEX_APP_SERVER_WS_URL\":\"\",\"CODEX_APP_SERVER_USE_LOCAL_DAEMON\":\"\"}".utf8), to: snapshot)
        try await fixture.runtime.start()
        try await fixture.runtime.remove()
        XCTAssertEqual(fixture.aquaEnvironment["CODEX_APP_SERVER_USE_LOCAL_DAEMON"], "")
        XCTAssertFalse(FileManager.default.fileExists(atPath: snapshot.path))
    }

    func testFailedAquaHelperRollsBackNewKeeperAndBothEnvironments() async throws {
        let fixture = try SharedFixture()
        defer { fixture.remove() }
        fixture.helperExit = 1
        do { try await fixture.runtime.start(); XCTFail("accepted failed Aqua helper") }
        catch {}
        XCTAssertFalse(fixture.loaded)
        XCTAssertEqual(fixture.aquaEnvironment["CODEX_APP_SERVER_USE_LOCAL_DAEMON"], "1")
        XCTAssertEqual(fixture.userEnvironment["CODEX_APP_SERVER_WS_URL"], "")
    }

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
    var aquaJobs: [String: String] = [:]
    var helperExit = 0
    var aquaEnvironment = ["CODEX_APP_SERVER_WS_URL": "", "CODEX_APP_SERVER_USE_LOCAL_DAEMON": "1"]
    var userEnvironment = ["CODEX_APP_SERVER_WS_URL": "", "CODEX_APP_SERVER_USE_LOCAL_DAEMON": ""]
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
        for file in ["codex-keeper-launch.sh", "codex-bin-resolve.sh", "codex-shared-app-server.mjs", "codex-desktop-env.sh", "check-codex-idle.sh", "com.macfleet.codex-shared-app-server.plist", "com.macfleet.codex-desktop-env.plist"] {
            try FileManager.default.copyItem(at: mac.appendingPathComponent(file), to: resources.appendingPathComponent(file))
        }
    }
    func definition() throws -> [String: Any] {
        try PropertyListSerialization.propertyList(from: PrivateRuntime.read(definitionURL), format: nil) as! [String: Any]
    }
    func remove() { try? FileManager.default.removeItem(at: root) }
    private func execute(_ executable: URL, _ arguments: [String]) throws -> Data {
        if executable.path == "/bin/bash", arguments.first == "-o" {
            return environmentOutput(arguments.last == "gui/\(getuid())" ? aquaEnvironment : userEnvironment)
        }
        if executable.lastPathComponent == "launchctl" {
            switch arguments[0] {
            case "print":
                if arguments.count == 2, arguments[1] == "gui/\(getuid())" {
                    return environmentOutput(aquaEnvironment)
                }
                if arguments.count == 2, arguments[1] == "user/\(getuid())" {
                    return environmentOutput(userEnvironment)
                }
                if let file = aquaJobs[arguments[1]] {
                    let code = arguments[1].hasSuffix("com.macfleet.codex-desktop-env") ? helperExit : 0
                    return Data("path = \(file)\nstate = not running\nlast exit code = \(code)\n".utf8)
                }
                guard arguments[1].hasSuffix("/com.macfleet.codex-app-server") else { throw FleetError.message("not loaded") }
                guard loaded else { throw FleetError.message("not loaded") }
                return Data("path = \(definitionURL.path)\nstate = \(stopped ? "not running" : "running")\n".utf8)
            case "bootstrap":
                let definition = try PropertyListSerialization.propertyList(from: PrivateRuntime.read(URL(fileURLWithPath: arguments[2])), format: nil) as! [String: Any]
                if definition["Label"] as? String != "com.macfleet.codex-app-server" {
                    let label = definition["Label"] as! String
                    XCTAssertEqual(definition["LimitLoadToSessionType"] as? String, arguments[1].hasPrefix("gui/") ? "Aqua" : "Background")
                    aquaJobs[arguments[1] + "/" + label] = arguments[2]
                    let command = definition["ProgramArguments"] as! [String]
                    events.append("aqua-bootstrap")
                    if command[0] == "/bin/bash" {
                        let clear = command[2] == "clear"
                        events.append(clear ? "clear-env" : "desktop-env")
                        aquaEnvironment["CODEX_APP_SERVER_USE_LOCAL_DAEMON"] = ""
                        aquaEnvironment["CODEX_APP_SERVER_WS_URL"] = clear ? "" : command[3]
                    } else {
                        events.append("restore-env")
                        if arguments[1] == "gui/\(getuid())" { aquaEnvironment[command[2]] = command[1] == "setenv" ? command[3] : "" }
                        else { userEnvironment[command[2]] = command[1] == "setenv" ? command[3] : "" }
                    }
                } else { loaded = true; events.append("bootstrap") }
            case "bootout":
                if aquaJobs.removeValue(forKey: arguments[1]) != nil { events.append("aqua-bootout") }
                else { loaded = false; events.append("bootout") }
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
    private func environmentOutput(_ values: [String: String]) -> Data {
        Data(("environment = {\n" + values.filter { !$0.value.isEmpty }.map { " \($0.key) => \($0.value)\n" }.joined() + "}\n").utf8)
    }
}
