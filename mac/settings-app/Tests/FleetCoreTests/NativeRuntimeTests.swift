import Foundation
import XCTest
@testable import FleetCore

@MainActor
final class NativeRuntimeTests: XCTestCase {
    func testLegacyReplacementRequiresExplicitConfirmation() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        do { try await fixture.management.synchronizeBackground(launch: true); XCTFail("replaced without consent") }
        catch { XCTAssertTrue(error.localizedDescription.contains("确认卸载")) }
        XCTAssertTrue(fixture.legacyRunning)
        XCTAssertTrue(FileManager.default.fileExists(atPath: fixture.legacyPlist.path))
        XCTAssertFalse(fixture.events.contains("legacy-bootout"))
    }

    func testConfirmedMigrationRetiresLegacyExecutableAndHelpersWithRollback() async throws {
        for fail in [false, true] {
            let fixture = try NativeRuntimeFixture(running: false, legacy: true)
            defer { fixture.remove() }
            let binary = fixture.layout.home.appendingPathComponent(".local/bin/fleet-agent")
            try FileManager.default.createDirectory(at: binary.deletingLastPathComponent(), withIntermediateDirectories: true)
            let original = Data("signed legacy agent".utf8)
            try original.write(to: binary)
            let helper = fixture.legacyPlist.deletingLastPathComponent().appendingPathComponent("com.macfleet.ttyd.plist")
            let plist = try PropertyListSerialization.data(fromPropertyList: ["Label": "com.macfleet.ttyd", "ProgramArguments": ["/opt/homebrew/bin/ttyd"]], format: .xml, options: 0)
            try plist.write(to: helper)
            fixture.auxiliaryRunning = true
            fixture.rejectNew = fail
            do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTAssertFalse(fail) }
            catch { XCTAssertTrue(fail) }
            XCTAssertEqual(FileManager.default.fileExists(atPath: binary.path), fail)
            XCTAssertEqual(FileManager.default.fileExists(atPath: helper.path), fail)
            XCTAssertEqual(fixture.auxiliaryRunning, fail)
            if fail { XCTAssertEqual(try Data(contentsOf: binary), original); XCTAssertEqual(try Data(contentsOf: helper), plist) }
        }
    }

    func testUpdateOriginFallsBackToPrivateSettingsWhenBackgroundIsStopped() async throws {
        let fixture = try NativeRuntimeFixture(running: false)
        defer { fixture.remove() }
        let settings = FleetSettings(schema: 1, origin: "https://saved.example.test", autoStart: false)
        try PrivateRuntime.write(JSONEncoder().encode(settings), to: fixture.layout.state.appendingPathComponent("settings.json"))
        let origin = try await fixture.management.updateOrigin()
        XCTAssertEqual(origin, settings.origin)
        XCTAssertFalse(fixture.running)
    }

    func testLegacyMigrationRetainsQueueIdentityAndCustomAssistantConfig() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        let queue = fixture.layout.home.appendingPathComponent(".macfleet/chat-queue.json")
        let data = try JSONSerialization.data(withJSONObject: ["items": [["id": "pending-original", "status": "queued"]]])
        try PrivateRuntime.write(data, to: queue)
        var old = try PropertyListSerialization.propertyList(from: Data(contentsOf: fixture.legacyPlist), format: nil) as! [String: Any]
        old["EnvironmentVariables"] = ["FLEET_CLAUDE_HOME": "/custom/claude", "FLEET_CODEX_APPSERVER_MODE": "isolated"]
        try PropertyListSerialization.data(fromPropertyList: old, format: .xml, options: 0).write(to: fixture.legacyPlist)
        try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true)
        let environment = try fixture.definition()["EnvironmentVariables"] as! [String: String]
        XCTAssertEqual(environment["FLEET_CHAT_QUEUE_FILE"], queue.path)
        XCTAssertEqual(environment["FLEET_CLAUDE_HOME"], "/custom/claude")
        XCTAssertEqual(environment["FLEET_CODEX_APPSERVER_MODE"], "shared")
        XCTAssertEqual(try PrivateRuntime.read(queue), data)
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.layout.state.appendingPathComponent("chat-queue.json").path))
        try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true)
        XCTAssertEqual(try fixture.definition()["EnvironmentVariables"] as? [String: String], environment)
    }

    func testFailedMigrationRetainsOriginalQueueWithoutCreatingAReplayCopy() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        let queue = fixture.layout.home.appendingPathComponent(".macfleet/chat-queue.json")
        let original = Data("{\"items\":[{\"id\":\"pending-original\",\"status\":\"queued\"}]}".utf8)
        try PrivateRuntime.write(original, to: queue)
        fixture.rejectNew = true
        do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("accepted failed upgrade") } catch {}
        XCTAssertEqual(try PrivateRuntime.read(queue), original)
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.layout.state.appendingPathComponent("chat-queue.json").path))
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.layout.state.appendingPathComponent("migrated-environment.json").path))
        XCTAssertTrue(fixture.legacyRunning)
    }

    func testDualPendingQueuesAreNotSilentlyHiddenOrReplayed() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        let legacy = fixture.layout.home.appendingPathComponent(".macfleet/chat-queue.json")
        let native = fixture.layout.state.appendingPathComponent("chat-queue.json")
        let original = Data("{\"items\":[{\"id\":\"legacy-pending\",\"status\":\"queued\"}]}".utf8)
        let newer = Data("{\"items\":[{\"id\":\"native-pending\",\"status\":\"queued\"}]}".utf8)
        try PrivateRuntime.write(original, to: legacy)
        try PrivateRuntime.write(newer, to: native)
        do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("silently hid a queue") }
        catch { XCTAssertTrue(error.localizedDescription.contains("两份待处理消息队列")) }
        XCTAssertEqual(try PrivateRuntime.read(legacy), original)
        XCTAssertEqual(try PrivateRuntime.read(native), newer)
        XCTAssertTrue(fixture.legacyRunning)
    }

    func testLegacyMigrationKeepsLoginStartupEnabled() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true)
        XCTAssertEqual(fixture.autoStartApplied, [true])
        let settings = try JSONDecoder().decode(FleetSettings.self, from: PrivateRuntime.read(fixture.layout.state.appendingPathComponent("settings.json")))
        XCTAssertTrue(settings.autoStart)
    }

    func testLegacyMigrationRespectsExistingNativeStartupPreferences() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        let settings = FleetSettings(schema: 1, origin: "https://another.example.test", autoStart: false)
        let original = try JSONEncoder().encode(settings)
        let destination = fixture.layout.state.appendingPathComponent("settings.json")
        try PrivateRuntime.write(original, to: destination)
        try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true)
        XCTAssertEqual(fixture.autoStartApplied, [false])
        XCTAssertEqual(try PrivateRuntime.read(destination), original)
    }

    func testDisabledLegacyLoginStartupIsNotReenabledDuringMigration() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        fixture.legacyRunning = false
        var values = try PropertyListSerialization.propertyList(from: Data(contentsOf: fixture.legacyPlist), format: nil) as! [String: Any]
        values["RunAtLoad"] = false
        try PropertyListSerialization.data(fromPropertyList: values, format: .xml, options: 0).write(to: fixture.legacyPlist)
        try await fixture.management.synchronizeBackground(launch: false, replaceLegacy: true)
        XCTAssertEqual(fixture.autoStartApplied, [false])
        XCTAssertFalse(fixture.running)
        XCTAssertFalse(try JSONDecoder().decode(FleetSettings.self, from: PrivateRuntime.read(fixture.layout.state.appendingPathComponent("settings.json"))).autoStart)
    }

    func testMigrationIgnoresChangingLaunchStatisticsForTheSameLegacyProcess() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        fixture.changingLegacyStatistics = true
        try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true)
        XCTAssertTrue(fixture.running)
        XCTAssertFalse(fixture.legacyRunning)
    }

    func testLegacyStopTimeoutRestoresTheOldServiceWhenItWasUnloaded() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        fixture.legacyStopTimeout = true
        do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("ignored stop timeout") }
        catch {}
        XCTAssertTrue(fixture.legacyRunning)
        XCTAssertFalse(fixture.running)
        XCTAssertTrue(FileManager.default.fileExists(atPath: fixture.legacyPlist.path))
    }

    func testBootstrapTimeoutStopsTheNewRuntimeBeforeRestoringLegacy() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        fixture.bootstrapTimeout = true
        do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("ignored bootstrap timeout") }
        catch {}
        XCTAssertTrue(fixture.legacyRunning)
        XCTAssertFalse(fixture.running)
        XCTAssertTrue(FileManager.default.fileExists(atPath: fixture.legacyPlist.path))
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.layout.backgroundApplication.path))
    }

    func testOlderSettingsApplicationDoesNotDowngradeTheInstalledAgent() async throws {
        let fixture = try NativeRuntimeFixture(running: true)
        defer { fixture.remove() }
        let metadata = fixture.layout.backgroundApplication.appendingPathComponent("Contents/Info.plist")
        var values = try PropertyListSerialization.propertyList(from: Data(contentsOf: metadata), format: nil) as! [String: Any]
        values["CFBundleVersion"] = "3"
        try PropertyListSerialization.data(fromPropertyList: values, format: .xml, options: 0).write(to: metadata)
        fixture.version = "1.0.0+3"
        do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("downgraded newer runtime") }
        catch {}
        XCTAssertEqual(fixture.version, "1.0.0+3")
        XCTAssertTrue(fixture.running)
        XCTAssertFalse(fixture.events.contains("prepare-stop"))
        XCTAssertEqual(try fixture.identity(fixture.layout.backgroundApplication), "3")
    }

    func testLegacyServiceWithInFlightMessagesIsNotRetired() async throws {
        for state in ["sending", "steering", "taking_over", "recovering", "takeover_check"] {
            let fixture = try NativeRuntimeFixture(running: false, legacy: true)
            defer { fixture.remove() }
            let queue = fixture.layout.home.appendingPathComponent(".macfleet/chat-queue.json")
            try PrivateRuntime.write(JSONSerialization.data(withJSONObject: ["items": [["status": state]]]), to: queue)
            do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("retired service while \(state)") }
            catch { XCTAssertEqual(error.localizedDescription, "消息正在投递，请完成后再更新。") }
            XCTAssertTrue(fixture.legacyRunning)
            XCTAssertFalse(fixture.running)
            XCTAssertFalse(fixture.events.contains("legacy-bootout"))
        }
    }

    func testUntrustedLegacyExecutableIsNeverStoppedOrReplaced() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        fixture.rejectLegacySignature = true
        do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("stopped untrusted service") }
        catch { XCTAssertEqual(error.localizedDescription, "legacy signature mismatch") }
        XCTAssertTrue(fixture.legacyRunning)
        XCTAssertFalse(fixture.running)
        XCTAssertTrue(FileManager.default.fileExists(atPath: fixture.legacyPlist.path))
        XCTAssertFalse(fixture.events.contains("legacy-idle"))
    }

    func testLegacyServiceIsRetiredWhenInstallingTheNativeBackground() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        let original = try Data(contentsOf: fixture.legacyPlist)
        try await fixture.management.synchronizeBackground(launch: false, replaceLegacy: true)
        XCTAssertFalse(fixture.legacyRunning)
        XCTAssertTrue(fixture.running)
        XCTAssertEqual(fixture.version, "1.0.0+2")
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.legacyPlist.path))
        let backups = try FileManager.default.contentsOfDirectory(at: fixture.layout.state.appendingPathComponent("migrations"), includingPropertiesForKeys: nil)
        XCTAssertEqual(backups.count, 1)
        XCTAssertEqual(try Data(contentsOf: backups[0].appendingPathComponent("com.macfleet.fleet-agent.plist")), original)
        XCTAssertEqual(fixture.events.filter { ["legacy-idle", "legacy-bootout", "bootstrap"].contains($0) }, ["legacy-idle", "legacy-bootout", "bootstrap"])
        XCTAssertFalse(fixture.events.contains("logout"))
    }

    func testBusyLegacyServiceAndItsConfigurationRemainUntouched() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        let original = try Data(contentsOf: fixture.legacyPlist)
        fixture.busy = true
        do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("replaced busy legacy service") }
        catch { XCTAssertTrue(error.localizedDescription.contains("正在执行任务")) }
        XCTAssertTrue(fixture.legacyRunning)
        XCTAssertFalse(fixture.running)
        XCTAssertEqual(try Data(contentsOf: fixture.legacyPlist), original)
        XCTAssertFalse(fixture.events.contains("legacy-bootout"))
    }

    func testFailedLegacyMigrationRestoresTheOldLaunchDefinitionAndService() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        let original = try Data(contentsOf: fixture.legacyPlist)
        fixture.rejectNew = true
        do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("accepted unhealthy migration") }
        catch {}
        XCTAssertTrue(fixture.legacyRunning)
        XCTAssertFalse(fixture.running)
        XCTAssertEqual(try Data(contentsOf: fixture.legacyPlist), original)
        XCTAssertTrue(fixture.events.contains("legacy-bootstrap"))
    }

    func testStoppedLegacyServiceIsRetiredWithoutStartingEitherBackground() async throws {
        let fixture = try NativeRuntimeFixture(running: false, legacy: true)
        defer { fixture.remove() }
        fixture.legacyRunning = false
        try await fixture.management.synchronizeBackground(launch: false, replaceLegacy: true)
        XCTAssertFalse(fixture.running)
        XCTAssertFalse(fixture.legacyRunning)
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.legacyPlist.path))
        XCTAssertFalse(fixture.events.contains("bootstrap"))
        XCTAssertFalse(fixture.events.contains("legacy-bootout"))
    }

    func testUpdatePreparationCanStopAnAlreadyStoppedBackground() async throws {
        let fixture = try NativeRuntimeFixture(running: false)
        defer { fixture.remove() }
        try await fixture.management.prepareForStop()
        try await fixture.management.stop()
        XCTAssertTrue(fixture.bootoutTargets.isEmpty)
    }

    func testConnectionRetryDoesNotRestartAgentOrSharedServer() async throws {
        let fixture = try NativeRuntimeFixture(running: true)
        defer { fixture.remove() }
        try await fixture.management.retryConnection()
        XCTAssertEqual(fixture.events, ["retry-connection"])
        XCTAssertTrue(fixture.bootoutTargets.isEmpty)
        XCTAssertEqual(fixture.pid, 100)
    }

    func testStopUnloadsOnlyTheDesktopAgentAfterTheIdleCheck() async throws {
        let fixture = try NativeRuntimeFixture(running: true)
        defer { fixture.remove() }
        try await fixture.management.stop()
        XCTAssertEqual(fixture.events, ["prepare-stop", "bootout"])
        XCTAssertEqual(fixture.bootoutTargets, ["gui/\(getuid())/com.macfleet.desktop-agent"])
        XCTAssertFalse(fixture.running)
    }

    func testStopKeepsAnAgentWithActiveWorkRunning() async throws {
        let fixture = try NativeRuntimeFixture(running: true)
        defer { fixture.remove() }
        fixture.busy = true
        do { try await fixture.management.stop(); XCTFail("stopped active work") }
        catch { XCTAssertEqual(error.localizedDescription, "active turn") }
        XCTAssertEqual(fixture.events, ["prepare-stop"])
        XCTAssertTrue(fixture.bootoutTargets.isEmpty)
        XCTAssertTrue(fixture.running)
    }

    func testFailedStopResumesTheAgentInsteadOfLeavingItInMaintenance() async throws {
        let fixture = try NativeRuntimeFixture(running: true)
        defer { fixture.remove() }
        fixture.rejectBootout = true
        do { try await fixture.management.stop(); XCTFail("ignored failed stop") }
        catch { XCTAssertEqual(error.localizedDescription, "bootout failed") }
        XCTAssertEqual(fixture.events, ["prepare-stop", "bootout", "resume"])
        XCTAssertTrue(fixture.running)
    }

    func testPairStartDoesNotReuseAnOldBackgroundWhenHostPreparationFails() async throws {
        let fixture = try NativeRuntimeFixture(running: true, nested: true)
        defer { fixture.remove() }
        do {
            _ = try await fixture.management.pairing("pair-start", state: nil)
            XCTFail("sent authorization to a background without preparing the installed host")
        } catch {
            XCTAssertTrue(error.localizedDescription.contains("请先安装到应用程序"))
        }
        XCTAssertFalse(fixture.events.contains("pair-start"))
        XCTAssertTrue(fixture.running)
        XCTAssertEqual(fixture.pid, 100)
    }

    func testPairCancellationDoesNotPrepareOrReplaceTheBackground() async throws {
        let fixture = try NativeRuntimeFixture(running: true, nested: true)
        defer { fixture.remove() }
        _ = try await fixture.management.pairing("pair-cancel", state: nil)
        XCTAssertEqual(fixture.events, ["pair-cancel"])
        XCTAssertTrue(fixture.requirements.isEmpty)
        XCTAssertEqual(fixture.pid, 100)
    }

    func testBackgroundVerificationPassesInlineDeveloperIDRequirement() async throws {
        let fixture = try NativeRuntimeFixture(running: false)
        defer { fixture.remove() }
        try await fixture.management.synchronizeBackground(launch: false, replaceLegacy: true)
        let expected = "=identifier \"com.macfleet.fleet-agent\" and anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate leaf[subject.OU] = \"FAKETEAM01\""
        XCTAssertFalse(fixture.requirements.isEmpty)
        XCTAssertTrue(fixture.requirements.allSatisfy { $0 == expected })
    }

    func testFirstPreparationInstallsAgentOutsideHubWithoutStartingIt() async throws {
        let fixture = try NativeRuntimeFixture(running: false)
        defer { fixture.remove() }
        try await fixture.management.synchronizeBackground(launch: false, replaceLegacy: true)
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
        try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true)
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
        do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("stopped busy agent") }
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
        do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("accepted unhealthy runtime") }
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
        try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true)
        fixture.events.removeAll()
        try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true)
        XCTAssertFalse(fixture.events.contains("bootout"))
        XCTAssertFalse(fixture.events.contains("bootstrap"))
        XCTAssertEqual(fixture.pid, 200)
    }

    func testUpgradePreservesIncompleteBindingAndStartsItsManagementService() async throws {
        let fixture = try NativeRuntimeFixture(running: true)
        defer { fixture.remove() }
        fixture.incompleteBinding = true
        try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true)
        XCTAssertTrue(fixture.running)
        XCTAssertEqual(fixture.pid, 200)
        XCTAssertEqual(fixture.version, "1.0.0+2")
        let status = try await fixture.management.status()
        XCTAssertEqual(status.binding?.deviceID, "m1")
        XCTAssertEqual(status.binding?.complete, false)
        XCTAssertEqual(status.runtime?.phase, "unbound")
        XCTAssertFalse(fixture.events.contains("logout"))
    }

    func testMatchingVersionSymlinkCannotRemainRunningInsideHub() async throws {
        let fixture = try NativeRuntimeFixture(running: false)
        defer { fixture.remove() }
        try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true)
        try FileManager.default.removeItem(at: fixture.layout.backgroundApplication)
        try FileManager.default.createSymbolicLink(at: fixture.layout.backgroundApplication, withDestinationURL: fixture.layout.bundledBackgroundApplication)
        fixture.events.removeAll()
        do { try await fixture.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("accepted nested runtime symlink") }
        catch {}
        XCTAssertFalse(fixture.events.contains("bootout"))
    }
    func testCodexMigrationFailureRestoresThePreviousBackgroundPackageAndResources() async throws {
        let f = try NativeRuntimeFixture(running: true)
        defer { f.remove() }
        f.requireCodexClose = true
        let resource = f.layout.backgroundApplication.appendingPathComponent("Contents/Resources/codex/legacy-sentinel")
        try FileManager.default.createDirectory(at: resource.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data("previous keeper bytes".utf8).write(to: resource)
        do { try await f.management.synchronizeBackground(launch: true, replaceLegacy: true); XCTFail("committed a package before Codex migration passed") }
        catch {}
        XCTAssertTrue(f.events.contains("codex-check"))
        XCTAssertEqual(try f.identity(f.layout.backgroundApplication), "1")
        XCTAssertEqual(try String(contentsOf: resource), "previous keeper bytes")
        XCTAssertEqual(f.version, "1.0.0+1")
        XCTAssertTrue(f.running)
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
    var requireCodexClose = false
    var rejectNew = false
    var rejectBootout = false
    var incompleteBinding = false
    var legacyRunning: Bool
    var auxiliaryRunning = false
    var changingLegacyStatistics = false
    var legacyPrintCount = 0
    var legacyStopTimeout = false
    var bootstrapTimeout = false
    var rejectLegacySignature = false
    var autoStartApplied: [Bool] = []
    var legacyPlist: URL { layout.home.appendingPathComponent("Library/LaunchAgents/com.macfleet.fleet-agent.plist") }
    var events: [String] = []
    var bootoutTargets: [String] = []
    var requirements: [String] = []
    lazy var management = NativeManagement(layout: layout, configureAutoStart: { [unowned self] in autoStartApplied.append($0) }, execute: { [unowned self] executable, arguments, _, _ in
        try self.execute(executable, arguments)
    })

    init(running: Bool, nested: Bool = false, legacy: Bool = false) throws {
        root = FileManager.default.temporaryDirectory.appendingPathComponent("fleet-native-\(UUID().uuidString)")
        layout = RuntimeLayout(application: root.appendingPathComponent("Fleet Hub.app"), home: root.appendingPathComponent("home"))
        self.running = running
        self.legacyRunning = legacy
        version = nested ? "1.0.0+2" : "1.0.0+1"
        try bundle(layout.application, identifier: "com.macfleet.fleet-hub", build: "2")
        try bundle(layout.bundledBackgroundApplication, identifier: "com.macfleet.fleet-agent", build: "2")
        try PrivateRuntime.ensureDirectory(layout.home.appendingPathComponent(".macfleet"))
        try PrivateRuntime.ensureDirectory(layout.state)
        if legacy {
            let locks = layout.home.appendingPathComponent(".codex/thread-writer-locks")
            try FileManager.default.createDirectory(at: locks, withIntermediateDirectories: true)
            try Data().write(to: locks.appendingPathComponent("test-thread.lock"))
            try FileManager.default.createDirectory(at: legacyPlist.deletingLastPathComponent(), withIntermediateDirectories: true)
            let values: [String: Any] = ["Label": "com.macfleet.fleet-agent", "ProgramArguments": [layout.home.appendingPathComponent(".local/bin/fleet-agent").path],
                                       "EnvironmentVariables": ["HOME": layout.home.path], "RunAtLoad": true, "KeepAlive": true]
            try PropertyListSerialization.data(fromPropertyList: values, format: .xml, options: 0).write(to: legacyPlist)
        }
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
        if requireCodexClose {
            if arguments.contains("--keeper-node") { return Data("/fixture/signed-node\n".utf8) }
            if arguments.contains(where: { $0.hasSuffix("codex-desktop-install.mjs") }) { events.append("codex-check"); throw FleetError.message("Desktop must close") }
            if executable.lastPathComponent == "launchctl", arguments[0] == "print",
               arguments[1].contains("com.macfleet.codex-") { throw FleetError.message("not loaded") }
        }
        if executable.lastPathComponent == "codesign" {
            if arguments.contains("-d") { return Data("TeamIdentifier=FAKETEAM01\n".utf8) }
            if let position = arguments.firstIndex(of: "-R") { requirements.append(arguments[position + 1]) }
            if rejectLegacySignature, arguments.last?.hasSuffix("/.local/bin/fleet-agent") == true { throw FleetError.message("legacy signature mismatch") }
            return Data()
        }
        if executable.lastPathComponent == "spctl" { return Data() }
        if executable.lastPathComponent == "lsof" { return Data("123".utf8) }
        if executable.lastPathComponent == "curl" {
            if arguments.last?.hasSuffix("/api/info") == true { return Data("{\"dsh\":{\"enabled\":false}}".utf8) }
            events.append("legacy-idle")
            return Data("{\"turnPhase\":\"running\",\"turnOwner\":\"\(busy ? "fleet" : "desktop")\",\"items\":[]}".utf8)
        }
        if executable.lastPathComponent == "launchctl" {
            let action = arguments[0]
            if arguments.last?.hasSuffix("/com.macfleet.ttyd") == true || arguments.last?.hasSuffix("/com.macfleet.ttyd.plist") == true {
                if action == "print" {
                    guard auxiliaryRunning else { throw FleetError.message("not loaded") }
                    return Data("path = \(legacyPlist.deletingLastPathComponent().appendingPathComponent("com.macfleet.ttyd.plist").path)\nprogram = /opt/homebrew/bin/ttyd\n".utf8)
                }
                if action == "bootout" { auxiliaryRunning = false }
                if action == "bootstrap" { auxiliaryRunning = true }
                return Data()
            }
            let legacy = arguments.last?.hasSuffix("/com.macfleet.fleet-agent") == true || arguments.last == legacyPlist.path
            if legacy {
                if action == "print" {
                    guard legacyRunning else { throw FleetError.message("not loaded") }
                    legacyPrintCount += 1
                    return Data("path = \(legacyPlist.path)\nprogram = \(layout.home.appendingPathComponent(".local/bin/fleet-agent").path)\npid = 99\nruns = \(changingLegacyStatistics ? legacyPrintCount : 1)\n".utf8)
                }
                events.append("legacy-" + action)
                if action == "bootout" {
                    legacyRunning = false
                    if legacyStopTimeout { throw FleetError.message("legacy stop timeout") }
                }
                if action == "bootstrap" { legacyRunning = true }
                return Data()
            }
            if action == "print" {
                if !running { throw FleetError.message("not loaded") }
                return Data()
            }
            events.append(action)
            if action == "bootout" {
                bootoutTargets.append(arguments[1])
                if rejectBootout { throw FleetError.message("bootout failed") }
                running = false
            }
            if action == "bootstrap" {
                let program = (try definition()["ProgramArguments"] as! [String])[0]
                if program == layout.agent.path { version = "1.0.0+\(try identity(layout.backgroundApplication))" }
                else { version = "1.0.0+2" }
                running = true
                pid = pid == 100 ? 200 : 300
                if bootstrapTimeout { throw FleetError.message("bootstrap timeout") }
            }
            return Data()
        }
        guard arguments.first == "desktop", running else { throw FleetError.message("offline") }
        let action = arguments[1]
        events.append(action)
        if ["pair-start", "pair-confirm", "pair-cancel"].contains(action) {
            return try JSONEncoder().encode(PairingState(phase: "idle", attempt: "fixture", origin: "https://fleet.example.test"))
        }
        if action == "prepare-stop", busy { throw FleetError.message("active turn") }
        if action != "status" { return Data("{}".utf8) }
        let reported = rejectNew && version == "1.0.0+2" ? "wrong-version" : version
        let current = AgentStatus(schema: 1, pid: pid, version: reported,
            settings: FleetSettings(schema: 1, origin: "https://fleet.example.test", autoStart: true),
            binding: incompleteBinding ? BindingStatus(deviceID: "m1", ownerEmail: "owner@example.test", origin: "https://fleet.example.test", complete: false, locked: false) : nil,
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
