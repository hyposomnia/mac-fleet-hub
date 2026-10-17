import Darwin
import Foundation
import XCTest
@testable import FleetCore

@MainActor
final class CodexSharedRuntimeTests: XCTestCase {
    func testStartInstallsDesktopHookWithoutStartingOrWaitingForAListener() async throws {
        let f = try HookFixture(); defer { f.remove() }
        try await f.runtime.start()
        XCTAssertTrue(f.events.contains("install"))
        XCTAssertTrue(f.events.contains("gui-shared"))
        XCTAssertTrue(f.events.contains("user-shared"))
        XCTAssertFalse(f.events.contains("listener-bootstrap"))
        XCTAssertFalse(f.events.contains("ready"))
        XCTAssertTrue(FileManager.default.fileExists(atPath: f.marker.path))
        let definition = try f.job("codex-desktop-env.plist")
        let args = try XCTUnwrap(definition["ProgramArguments"] as? [String])
        XCTAssertEqual(args[0], "/bin/bash")
        XCTAssertEqual(args[1], f.hook.appendingPathComponent("codex-desktop-env.sh").path)
        XCTAssertEqual(args[3], f.hook.appendingPathComponent("codex-desktop.json").path)
        XCTAssertFalse(args.joined().contains("Preview.app"))
    }

    func testMigratedHomeAndPackageArePassedToTheCommonInstaller() async throws {
        let f = try HookFixture(preview: true); defer { f.remove() }
        try PrivateRuntime.write(JSONEncoder().encode(["FLEET_CODEX_HOME": "/custom/codex"]), to: f.layout.state.appendingPathComponent("migrated-environment.json"))
        try await f.runtime.start()
        let call = try XCTUnwrap(f.installArguments)
        XCTAssertEqual(call[0], f.layout.codexResources.appendingPathComponent("codex-desktop-install.mjs").path)
        XCTAssertTrue(call.contains("/custom/codex"))
        XCTAssertTrue(call.contains(f.hook.path))
        XCTAssertTrue(call.contains(f.layout.home.appendingPathComponent(".macfleet/codex-app-server.sock").path))
    }

    func testActiveDesktopRejectsMigrationBeforeAnyServiceOrEnvironmentMutation() async throws {
        let f = try HookFixture(); defer { f.remove() }
        f.desktopOpen = true
        await expectFailure { try await f.runtime.start() }
        XCTAssertFalse(f.events.contains("install"))
        XCTAssertFalse(f.events.contains("legacy-bootout"))
        XCTAssertFalse(f.events.contains("gui-shared"))
    }

    func testOwnedLegacyKeeperIsRetiredOnlyAfterIdleGuardAndRestoredOnFailure() async throws {
        let f = try HookFixture(); defer { f.remove() }
        try f.legacy()
        let bytes = try PrivateRuntime.read(f.legacyDefinition)
        f.helperFails = true
        await expectFailure { try await f.runtime.start() }
        XCTAssertLessThan(try XCTUnwrap(f.events.firstIndex(of: "idle")), try XCTUnwrap(f.events.firstIndex(of: "legacy-bootout")))
        XCTAssertTrue(f.events.contains("rollback"))
        XCTAssertTrue(f.events.contains("legacy-bootstrap"))
        XCTAssertTrue(f.events.contains("gui-rollback"))
        XCTAssertEqual(try PrivateRuntime.read(f.legacyDefinition), bytes)
        XCTAssertFalse(FileManager.default.fileExists(atPath: f.marker.path))
    }

    func testBusyLegacyKeeperAndForeignDefinitionAreNeverStopped() async throws {
        for foreign in [false, true] {
            let f = try HookFixture(); defer { f.remove() }
            try f.legacy(); f.busy = !foreign; f.foreign = foreign
            await expectFailure { try await f.runtime.start() }
            XCTAssertFalse(f.events.contains("legacy-bootout"))
            XCTAssertFalse(f.events.contains("install"))
        }
    }

    func testRepeatedStartDoesNotRestartDesktopRuntime() async throws {
        let f = try HookFixture(); defer { f.remove() }
        try await f.runtime.start(); f.current = true; f.events.removeAll(); f.desktopOpen = true
        try await f.runtime.start()
        XCTAssertFalse(f.events.contains("listener-bootstrap"))
        XCTAssertFalse(f.events.contains("legacy-bootout"))
        XCTAssertFalse(f.events.contains("ready"))
    }

    func testCommonHookOwnedByAnotherInstallerIsReusedAndNotRemoved() async throws {
        let f = try HookFixture(); defer { f.remove() }
        f.current = true
        try await f.runtime.start(); f.events.removeAll()
        try await f.runtime.remove()
        XCTAssertFalse(f.events.contains("remove"))
        XCTAssertFalse(f.events.contains("gui-clear"))
    }

    func testRemovalRequiresDesktopClosedAndRestoresBothDomainsBeforeRemovingFiles() async throws {
        let f = try HookFixture(); defer { f.remove() }
        try await f.runtime.start(); f.events.removeAll(); f.desktopOpen = true
        do {
            try await f.runtime.remove()
            XCTFail("removed the hook while Desktop was running")
        } catch {
            let message = error.localizedDescription
            XCTAssertTrue(message.contains("ChatGPT 桌面应用（Codex Desktop）仍在运行"))
            XCTAssertTrue(message.contains("当前任务结束"))
            XCTAssertTrue(message.contains("⌘Q"))
            XCTAssertTrue(message.contains("仅关闭窗口不够"))
            XCTAssertTrue(message.contains("再次点击“卸载”"))
            XCTAssertFalse(message.contains("CLI hook"))
        }
        XCTAssertFalse(f.events.contains("remove"))
        XCTAssertFalse(f.events.contains("gui-clear"))
        f.desktopOpen = false
        try await f.runtime.remove()
        XCTAssertLessThan(try XCTUnwrap(f.events.firstIndex(of: "gui-clear")), try XCTUnwrap(f.events.firstIndex(of: "remove")))
        XCTAssertLessThan(try XCTUnwrap(f.events.firstIndex(of: "user-clear")), try XCTUnwrap(f.events.firstIndex(of: "remove")))
        XCTAssertFalse(FileManager.default.fileExists(atPath: f.marker.path))
    }

    func testMissingCodexLeavesOptionalChatUnconfigured() async throws {
        let f = try HookFixture(); defer { f.remove() }; f.installed = false
        try await f.runtime.start()
        XCTAssertFalse(f.events.contains("install"))
        XCTAssertFalse(f.events.contains("gui-shared"))
    }

    func testInstallerFailureBeforeEnvironmentChangesDoesNotReloadExistingJobs() async throws {
        let f = try HookFixture(); defer { f.remove() }
        try await f.runtime.start()
        f.current = true; f.installFails = true; f.events.removeAll()
        await expectFailure { try await f.runtime.start() }
        XCTAssertFalse(f.events.contains("gui-shared"))
        XCTAssertFalse(f.events.contains("user-shared"))
        XCTAssertFalse(f.events.contains("gui-rollback"))
        XCTAssertEqual(f.jobs.count, 2)
    }

    private func expectFailure(_ body: () async throws -> Void) async {
        do { try await body(); XCTFail("accepted unsafe runtime change") } catch {}
    }
}

@MainActor
private final class HookFixture {
    let layout: RuntimeLayout
    var root: URL { layout.home }
    var hook: URL { root.appendingPathComponent(".local/lib/macfleet") }
    var marker: URL { layout.state.appendingPathComponent("codex-hook-install.json") }
    var legacyDefinition: URL { layout.state.appendingPathComponent("codex-app-server.plist") }
    var events: [String] = []
    var installArguments: [String]?
    var desktopOpen = false, busy = false, foreign = false, helperFails = false, current = false, installed = true, legacyLoaded = false, installFails = false
    var jobs: [String: (String, Int)] = [:]
    lazy var runtime = CodexSharedRuntime(layout: layout, execute: { [unowned self] file, args, _, _ in try self.execute(file, args) })
    init(preview: Bool = false) throws {
        let home = URL(fileURLWithPath: "/private/tmp/fleet-hook-\(UUID().uuidString)")
        let resources = home.appendingPathComponent(preview ? "Fleet Preview.app/Contents/Resources/codex" : "package")
        layout = RuntimeLayout(application: URL(fileURLWithPath: "/Applications/Fleet Agent Settings.app"), home: home, codexResources: resources)
        try PrivateRuntime.ensureDirectory(layout.state)
        try FileManager.default.createDirectory(at: resources, withIntermediateDirectories: true)
        let mac = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        try FileManager.default.copyItem(at: mac.appendingPathComponent("com.macfleet.codex-desktop-env.plist"), to: resources.appendingPathComponent("com.macfleet.codex-desktop-env.plist"))
        // Legacy definition is an external adapter boundary, never an executable fixture.
        let old: [String: Any] = ["Label": "com.macfleet.codex-app-server", "ProgramArguments": ["/fixture/node", "/fixture/keeper"]]
        try Data(PropertyListSerialization.data(fromPropertyList: old, format: .xml, options: 0)).write(to: resources.appendingPathComponent("com.macfleet.codex-shared-app-server.plist"))
    }
    func legacy() throws {
        try PrivateRuntime.write(Data(contentsOf: layout.codexResources.appendingPathComponent("com.macfleet.codex-shared-app-server.plist")), to: legacyDefinition)
        legacyLoaded = true
    }
    func remove() { try? FileManager.default.removeItem(at: root) }
    func job(_ file: String) throws -> [String: Any] {
        try PropertyListSerialization.propertyList(from: PrivateRuntime.read(layout.state.appendingPathComponent(file)), format: nil) as! [String: Any]
    }
    func execute(_ file: URL, _ args: [String]) throws -> Data {
        if args.contains("--keeper-node") {
            if !installed { throw FleetError.message("Codex unavailable") }
            return Data("/fixture/signed-node\n".utf8)
        }
        if args.contains(where: { $0.hasSuffix("codex-bin-resolve.sh") }) { return Data("/fixture/codex\n".utf8) }
        if let index = args.firstIndex(where: { $0.hasSuffix("codex-desktop-install.mjs") }) {
            let command = args[index + 1]
            if command == "check" && desktopOpen && !current { throw FleetError.message("Desktop must close") }
            if command == "check-removal" && desktopOpen { throw FleetError.message("mac-fleet hook install: Desktop must close before removing its CLI hook") }
            if command == "install", installFails { throw FleetError.message("installer failed before environment") }
            if command == "install" { installArguments = Array(args[index...]); events.append("install"); return Data((current ? "current\n" : "staged\n").utf8) }
            events.append(command); return Data()
        }
        if args.contains(where: { $0.hasSuffix("check-codex-idle.sh") }) {
            events.append("idle"); if busy { throw FleetError.message("active turn") }; return Data()
        }
        if file.lastPathComponent == "curl" { events.append("ready"); throw FleetError.message("Desktop is closed") }
        if file.lastPathComponent == "launchctl" {
            let target = "gui/\(getuid())/com.macfleet.codex-app-server"
            if args[0] == "print" {
                if args[1] == target {
                    guard legacyLoaded else { throw FleetError.message("not loaded") }
                    return Data("path = \(foreign ? "/other/install.plist" : legacyDefinition.path)\nstate = running\n".utf8)
                }
                if let (path, exit) = jobs[args[1]] { return Data("path = \(path)\nstate = not running\nlast exit code = \(exit)\n".utf8) }
                throw FleetError.message("not loaded")
            }
            if args[0] == "bootout" {
                if args[1] == target { legacyLoaded = false; events.append("legacy-bootout") }
                else { jobs.removeValue(forKey: args[1]) }
                return Data()
            }
            if args[0] == "bootstrap" {
                let definition = try PropertyListSerialization.propertyList(from: PrivateRuntime.read(URL(fileURLWithPath: args[2])), format: nil) as! [String: Any]
                let label = definition["Label"] as! String
                if label == "com.macfleet.codex-app-server" { events.append("legacy-bootstrap"); legacyLoaded = true; return Data() }
                let command = definition["ProgramArguments"] as! [String]
                let mode = command.count > 2 ? command[2] : "unknown"
                let scope = args[1].hasPrefix("gui/") ? "gui" : "user"
                events.append(scope + "-" + mode)
                jobs[args[1] + "/" + label] = (args[2], helperFails && mode == "shared" ? 1 : 0)
                return Data()
            }
        }
        XCTFail("unexpected external command: \(file) \(args)"); return Data()
    }
}
