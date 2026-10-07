import Foundation
import XCTest
@testable import FleetCore

@MainActor
final class BackgroundRuntimeTests: XCTestCase {
    func testUnverifiedPayloadNeverStopsOrChangesInstalledAgent() async throws {
        let fixture = try RuntimeFixture()
        defer { fixture.remove() }
        var prepared = false
        do {
            try await BackgroundRuntimeInstaller.deploy(source: fixture.source, destination: fixture.destination,
                verify: { _ in throw FleetError.message("untrusted") }, prepare: { prepared = true },
                activate: {}, quiesce: {}, restore: {})
            XCTFail("accepted untrusted payload")
        } catch {}
        XCTAssertFalse(prepared)
        XCTAssertEqual(try fixture.installedVersion(), "old")
    }

    func testIndependentInstallVerifiesBothCopiesBeforeReplacing() async throws {
        let fixture = try RuntimeFixture()
        defer { fixture.remove() }
        var events: [String] = []
        try await BackgroundRuntimeInstaller.deploy(source: fixture.source, destination: fixture.destination,
            verify: { candidate in
                XCTAssertEqual(try fixture.version(candidate), "new")
                events.append("verify")
            }, prepare: {
                XCTAssertEqual(try fixture.installedVersion(), "old")
                events.append("prepare")
            }, activate: {
                XCTAssertEqual(try fixture.installedVersion(), "new")
                events.append("activate")
            }, quiesce: { XCTFail("unexpected shutdown") }, restore: { XCTFail("unexpected rollback") })
        XCTAssertEqual(events, ["verify", "verify", "prepare", "activate"])
        XCTAssertEqual(try fixture.installedVersion(), "new")
        XCTAssertFalse(fixture.destination.path.hasPrefix(fixture.source.deletingLastPathComponent().path + "/"))
    }

    func testBusyGuardLeavesOldAgentAndResumesWithoutActivation() async throws {
        let fixture = try RuntimeFixture()
        defer { fixture.remove() }
        var restored = false
        do {
            try await BackgroundRuntimeInstaller.deploy(source: fixture.source, destination: fixture.destination,
                verify: { _ in }, prepare: { throw FleetError.message("busy") },
                activate: { XCTFail("activated while busy") }, quiesce: { XCTFail("stopped before replacement") },
                restore: { restored = true })
            XCTFail("ignored busy guard")
        } catch {}
        XCTAssertTrue(restored)
        XCTAssertEqual(try fixture.installedVersion(), "old")
    }

    func testFailedHealthStopsNewAgentBeforeRestoringOldBundleAndDefinition() async throws {
        let fixture = try RuntimeFixture()
        defer { fixture.remove() }
        var events: [String] = []
        do {
            try await BackgroundRuntimeInstaller.deploy(source: fixture.source, destination: fixture.destination,
                verify: { _ in }, prepare: {}, activate: { throw FleetError.message("health failed") },
                quiesce: {
                    XCTAssertEqual(try fixture.installedVersion(), "new")
                    events.append("stop-new")
                }, restore: {
                    XCTAssertEqual(try fixture.installedVersion(), "old")
                    events.append("restore-definition")
                })
            XCTFail("ignored failed health")
        } catch {}
        XCTAssertEqual(events, ["stop-new", "restore-definition"])
        XCTAssertEqual(try fixture.installedVersion(), "old")
    }

    func testFailedFirstInstallRemovesOnlyNewRuntime() async throws {
        let fixture = try RuntimeFixture(installed: false)
        defer { fixture.remove() }
        do {
            try await BackgroundRuntimeInstaller.deploy(source: fixture.source, destination: fixture.destination,
                verify: { _ in }, prepare: {}, activate: { throw FleetError.message("health failed") },
                quiesce: {}, restore: {})
            XCTFail("ignored failed first start")
        } catch {}
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.destination.path))
        XCTAssertEqual(try fixture.version(fixture.source), "new")
    }

    func testSymlinkAndNestedAppDestinationsAreRejected() async throws {
        let fixture = try RuntimeFixture(installed: false)
        defer { fixture.remove() }
        try FileManager.default.createSymbolicLink(at: fixture.destination, withDestinationURL: fixture.source)
        for destination in [fixture.destination, fixture.source.appendingPathComponent("Contents/Fleet Agent.app")] {
            do {
                try await BackgroundRuntimeInstaller.deploy(source: fixture.source, destination: destination,
                    verify: { _ in XCTFail("verified unsafe destination") }, prepare: {}, activate: {}, quiesce: {}, restore: {})
                XCTFail("accepted unsafe destination")
            } catch {}
        }
        XCTAssertEqual(try fixture.version(fixture.source), "new")
    }

    func testConcurrentDeploymentCannotAcquireSameRuntimeLock() async throws {
        let fixture = try RuntimeFixture()
        defer { fixture.remove() }
        try await BackgroundRuntimeInstaller.deploy(source: fixture.source, destination: fixture.destination,
            verify: { _ in }, prepare: {
                do {
                    try await BackgroundRuntimeInstaller.withLock(directory: fixture.destination.deletingLastPathComponent()) {}
                    XCTFail("second deployment acquired lock")
                } catch {}
            }, activate: {}, quiesce: {}, restore: {})
        XCTAssertEqual(try fixture.installedVersion(), "new")
    }

    func testUnsafeRollbackKeepsBackupInsteadOfReplacingRunningProcess() async throws {
        let fixture = try RuntimeFixture()
        defer { fixture.remove() }
        do {
            try await BackgroundRuntimeInstaller.deploy(source: fixture.source, destination: fixture.destination,
                verify: { _ in }, prepare: {}, activate: { throw FleetError.message("health failed") },
                quiesce: { throw FleetError.message("cannot stop") }, restore: { XCTFail("restored under running process") })
            XCTFail("ignored incomplete rollback")
        } catch { XCTAssertTrue(error.localizedDescription.contains("备份")) }
        XCTAssertEqual(try fixture.installedVersion(), "new")
        let entries = try FileManager.default.contentsOfDirectory(at: fixture.destination.deletingLastPathComponent(), includingPropertiesForKeys: nil)
        let backup = try XCTUnwrap(entries.first { $0.lastPathComponent.hasPrefix(".fleet-agent-") })
        XCTAssertEqual(try fixture.version(backup.appendingPathComponent("Fleet Agent.app")), "old")
    }
}

private struct RuntimeFixture {
    let root: URL
    let source: URL
    let destination: URL
    init(installed: Bool = true) throws {
        root = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("fleet-runtime-\(UUID().uuidString)")
        source = root.appendingPathComponent("Hub.app/Contents/Library/LoginItems/Fleet Agent.app")
        destination = root.appendingPathComponent("runtime/Fleet Agent.app")
        try FileManager.default.createDirectory(at: source, withIntermediateDirectories: true)
        try PrivateRuntime.ensureDirectory(destination.deletingLastPathComponent())
        try Data("new".utf8).write(to: source.appendingPathComponent("version"))
        if installed {
            try FileManager.default.createDirectory(at: destination, withIntermediateDirectories: false)
            try Data("old".utf8).write(to: destination.appendingPathComponent("version"))
        }
    }
    func version(_ application: URL) throws -> String { try String(contentsOf: application.appendingPathComponent("version")) }
    func installedVersion() throws -> String { try version(destination) }
    func remove() { try? FileManager.default.removeItem(at: root) }
}
