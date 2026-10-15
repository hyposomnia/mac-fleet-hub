import Foundation
import XCTest
@testable import FleetCore

@MainActor
final class TerminalSetupTests: XCTestCase {
    func testFreshInstallOpensOAuthOnceAndWaitsUntilTheDeviceIsRunning() async throws {
        let client = TerminalClient()
        var urls: [URL] = []
        let setup = TerminalSetup(client: client, openBrowser: { urls.append($0); return true }, sleep: { client.frame += 1 })
        let result = try await setup.run(origin: "https://fleet.example.test:9443/")
        XCTAssertEqual(result, .needsDiskAccess)
        XCTAssertEqual(client.events.prefix(3), ["start", "save", "autostart:false"])
        XCTAssertEqual(client.actions, ["pair-start"])
        XCTAssertEqual(urls.map(\.absoluteString), [client.browser.url!])
        XCTAssertEqual(client.saved?.origin, "https://fleet.example.test:9443")
        XCTAssertEqual(client.saved?.autoStart, false)
        XCTAssertGreaterThanOrEqual(client.frame, 3)
        XCTAssertEqual(client.diskChecks, 1)
    }

    func testExistingBindingSkipsAuthorizationAndVerifiedDiskSkipsGuidance() async throws {
        let client = TerminalClient()
        client.frame = 3
        client.disk = DiskAccess(state: "verified", source: "background", checkedAt: 1, verifiedTargets: 1)
        let result = try await TerminalSetup(client: client, openBrowser: { _ in XCTFail("opened OAuth for existing binding"); return true }).run(origin: client.origin)
        XCTAssertEqual(result, .complete)
        XCTAssertTrue(client.actions.isEmpty)
        XCTAssertEqual(client.diskChecks, 1)
    }

    func testUnknownAndNonBackgroundEvidenceRequireGuidance() async throws {
        for evidence in [DiskAccess(state: "unknown", source: "background", checkedAt: 1, verifiedTargets: 0),
                         DiskAccess(state: "verified", source: "unverified-process", checkedAt: 1, verifiedTargets: 1)] {
            let client = TerminalClient()
            client.frame = 3
            client.disk = evidence
            let result = try await TerminalSetup(client: client, openBrowser: { _ in false }).run(origin: client.origin)
            XCTAssertEqual(result, .needsDiskAccess)
        }
    }

    func testIncompleteOrLockedBindingsAndPendingCleanupAreNotReplaced() async throws {
        for condition in ["incomplete", "locked", "cleanup"] {
            let client = TerminalClient()
            client.frame = condition == "cleanup" ? 0 : 3
            client.bindingComplete = condition != "incomplete"
            client.bindingLocked = condition == "locked"
            if condition == "cleanup" {
                client.initialPairing = PairingState(phase: "failed", attempt: "previous", origin: client.origin, needsCleanup: true)
            }
            do {
                _ = try await TerminalSetup(client: client, openBrowser: { _ in XCTFail("opened authorization"); return true }, timeout: 0).run(origin: client.origin)
                XCTFail("replaced unsafe binding")
            } catch { XCTAssertTrue(error.localizedDescription.contains("解除关联")) }
            XCTAssertNil(client.saved)
            XCTAssertTrue(client.actions.isEmpty)
        }
    }

    func testChangedAttemptAndRuntimeFailureNeverComplete() async throws {
        for condition in ["attempt", "runtime"] {
            let client = TerminalClient()
            let setup = TerminalSetup(client: client, openBrowser: { _ in true }, sleep: {
                client.frame += 1
                if condition == "attempt" { client.browser.attempt = "different-attempt" }
                else { client.runtimeFailure = "无法连接服务器" }
            })
            do { _ = try await setup.run(origin: client.origin); XCTFail("reported a changed request as complete") }
            catch { XCTAssertTrue(error.localizedDescription.contains(condition == "attempt" ? "请求已变化" : "无法连接")) }
            XCTAssertEqual(client.diskChecks, 0)
        }
    }

    func testDifferentServerBindingIsNeverOverwritten() async throws {
        let client = TerminalClient()
        client.frame = 3
        do {
            _ = try await TerminalSetup(client: client, openBrowser: { _ in XCTFail("opened another origin"); return true }).run(origin: "https://other.example.test")
            XCTFail("replaced another server binding")
        } catch { XCTAssertTrue(error.localizedDescription.contains("解除关联")) }
        XCTAssertNil(client.saved)
        XCTAssertTrue(client.actions.isEmpty)
    }

    func testBrowserOpenFailureDoesNotReportInstallationComplete() async throws {
        let client = TerminalClient()
        do {
            _ = try await TerminalSetup(client: client, openBrowser: { _ in false }, sleep: { client.frame += 1 }).run(origin: client.origin)
            XCTFail("accepted browser failure")
        } catch { XCTAssertTrue(error.localizedDescription.contains("浏览器")) }
        XCTAssertEqual(client.diskChecks, 0)
    }

    func testFailedOAuthAndTimeoutRemainFailures() async throws {
        for timeout in [false, true] {
            let client = TerminalClient()
            client.failure = timeout ? nil : "网页授权已取消"
            let setup = TerminalSetup(client: client, openBrowser: { _ in true }, timeout: timeout ? 0 : 720, sleep: { client.frame += 1 })
            do { _ = try await setup.run(origin: client.origin); XCTFail("reported failed authorization as complete") }
            catch { XCTAssertTrue(error.localizedDescription.contains(timeout ? "超时" : "取消")) }
            XCTAssertEqual(client.diskChecks, 0)
        }
    }

    func testCrossOriginBrowserURLIsRejected() async throws {
        let client = TerminalClient()
        client.browser.url = "https://other.example.test/oauth/authorize"
        do {
            _ = try await TerminalSetup(client: client, openBrowser: { _ in XCTFail("opened cross-origin URL"); return true }, sleep: { client.frame += 1 }).run(origin: client.origin)
            XCTFail("accepted foreign OAuth URL")
        } catch { XCTAssertTrue(error.localizedDescription.contains("授权")) }
    }
}

@MainActor
private final class TerminalClient: TerminalSetupManagement {
    let origin = "https://fleet.example.test:9443"
    var frame = 0
    var failure: String?
    var runtimeFailure: String?
    var initialPairing: PairingState?
    var bindingComplete = true
    var bindingLocked = false
    var events: [String] = []
    var actions: [String] = []
    var saved: FleetSettings?
    var diskChecks = 0
    var disk = DiskAccess(state: "restricted", source: "background", checkedAt: 1, verifiedTargets: 0)
    lazy var browser = PairingState(phase: "browser", attempt: "own-attempt", origin: origin, url: origin + "/oauth/authorize?state=one-time")

    func start(replaceLegacy: Bool) async throws { XCTAssertTrue(replaceLegacy); events.append("start") }
    func setAutoStart(_ enabled: Bool) async throws { events.append("autostart:\(enabled)") }
    func status() async throws -> AgentStatus {
        let binding = frame >= 2 ? BindingStatus(deviceID: "m4", ownerEmail: "owner@example.test", origin: origin, complete: bindingComplete, locked: bindingLocked) : nil
        var state = browser
        if let failure, frame > 0 { state.phase = "failed"; state.error = failure }
        else if frame >= 3 { state.phase = "complete" }
        else if frame == 2 { state.phase = "joining" }
        return AgentStatus(schema: 1, pid: 41, version: "1.0.0+2", settings: saved ?? FleetSettings(schema: 1, origin: origin, autoStart: false),
                           binding: binding, diskAccess: disk, pairing: actions.contains("pair-start") ? state : initialPairing,
                           runtime: RuntimeState(phase: runtimeFailure != nil ? "failed" : frame >= 3 ? "running" : frame == 2 ? "connecting" : "unbound", error: runtimeFailure))
    }
    func save(_ settings: FleetSettings) async throws -> FleetSettings { events.append("save"); saved = settings; return settings }
    func recheckDisk() async throws -> DiskAccess { diskChecks += 1; return disk }
    func pairing(_ action: String, state: PairingState?) async throws -> PairingState { actions.append(action); return browser }
}
