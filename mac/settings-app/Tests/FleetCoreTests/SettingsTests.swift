import XCTest
@testable import FleetCore

@MainActor
final class SettingsTests: XCTestCase {
    func testNativeOAuthURLAndExpiryRemainRetryable() {
        var state = PairingState(phase: "browser", attempt: "new", origin: "https://fleet.example.test", url: "https://fleet.example.test/oauth/authorize?client_id=fleet-hub&response_type=code&scope=device%3Aenroll&state=new&code_challenge_method=S256&code_challenge=challenge&redirect_uri=http%3A%2F%2F127.0.0.1%3A51234%2Foauth%2Fcallback", code: nil, deviceID: nil, ownerEmail: nil, error: nil)
        XCTAssertNotNil(state.verificationURL)
        state.phase = "failed"
        XCTAssertFalse(state.isActive)
        XCTAssertNil(state.code)
        state.url = "https://other.example.test/oauth/authorize"
        XCTAssertNil(state.verificationURL)
    }
    func testBrowserURLIsSameOriginAndConfirmationStaysExplicit() async throws {
        let client = TestManagement()
        let model = SettingsModel(client: client)
        await model.refresh()
        await model.beginPairing()
        XCTAssertEqual(client.actions, ["status", "pair-start"])
        XCTAssertFalse(client.actions.contains("pair-confirm"))
        let state = PairingState(phase: "browser", attempt: "attempt", origin: "https://fleet.example.test", url: "https://fleet.example.test/enroll/confirm?code=ABCD1234", code: "ABCD1234", deviceID: nil, ownerEmail: nil, error: nil)
        XCTAssertNotNil(state.verificationURL)
        var malicious = state
        malicious.url = "https://other.example.test/enroll/confirm?code=ABCD1234"
        XCTAssertNil(malicious.verificationURL)
        malicious.url = "https://fleet.example.test/api/delete"
        XCTAssertNil(malicious.verificationURL)
    }
    func testRejectsUnsafeOriginAndNormalizesDraft() throws {
        XCTAssertEqual(try FleetSettings.validatedOrigin(" https://fleet.example.test:7443/ "), "https://fleet.example.test:7443")
        for invalid in ["http://fleet.example.test", "https://fleet.example.test/path", "https://owner:password@fleet.example.test", "https://fleet.example.test?token=x", "https://fleet.example.test#x", "https://fleet.example.test:99999"] {
            XCTAssertThrowsError(try FleetSettings.validatedOrigin(invalid), invalid)
        }
    }

    func testFailedSavePreservesEffectiveSettingsAndDraft() async {
        let client = TestManagement()
        let model = SettingsModel(client: client)
        await model.refresh()
        model.origin = "https://new.example.test"
        client.rejectSave = true
        await model.save()
        XCTAssertEqual(model.status?.settings.origin, "https://fleet.example.test")
        XCTAssertEqual(model.origin, "https://new.example.test")
        XCTAssertFalse(model.error.isEmpty)
        XCTAssertFalse(model.isBusy)
    }

    func testStoppedBackgroundNeverClaimsDiskAuthorization() async {
        let client = TestManagement()
        let model = SettingsModel(client: client)
        await model.refresh()
        XCTAssertEqual(model.diskState, .verified)
        client.rejectStatus = true
        await model.refresh()
        XCTAssertNil(model.status)
        XCTAssertEqual(model.diskState, .unknown)
    }

    func testUIEvidenceAndMissingTargetsDoNotImplyFDA() {
        XCTAssertEqual(DiskAccess(state: "verified", source: "ui", checkedAt: 1, verifiedTargets: 1).verifiedState, .unknown)
        XCTAssertEqual(DiskAccess(state: "verified", source: "background", checkedAt: 1, verifiedTargets: 0).verifiedState, .unknown)
        XCTAssertEqual(DiskAccess(state: "restricted", source: "background", checkedAt: 1, verifiedTargets: 0).verifiedState, .restricted)
    }

    func testClosingSettingsDoesNotStopBackground() async {
        let client = TestManagement()
        var model: SettingsModel? = SettingsModel(client: client)
        await model?.refresh()
        model = nil
        XCTAssertEqual(client.actions, ["status"])
    }

    func testRefreshDoesNotOverwriteAnEditedDraft() async {
        let client = TestManagement()
        let model = SettingsModel(client: client)
        await model.refresh()
        model.origin = "https://draft.example.test"
        await model.refresh()
        XCTAssertEqual(model.origin, "https://draft.example.test")
    }
}

@MainActor
final class TestManagement: LocalManagement {
    var rejectSave = false
    var rejectStatus = false
    var actions: [String] = []
    func status() async throws -> AgentStatus {
        actions.append("status")
        if rejectStatus { throw FleetError.message("后台尚未运行") }
        return AgentStatus(schema: 1, pid: 123, version: "1.0.0", settings: FleetSettings(schema: 1, origin: "https://fleet.example.test", autoStart: true), binding: nil, diskAccess: DiskAccess(state: "verified", source: "background", checkedAt: 1, verifiedTargets: 1))
    }
    func save(_ settings: FleetSettings) async throws -> FleetSettings {
        actions.append("save")
        if rejectSave { throw FleetError.message("无法保存") }
        return settings
    }
    func recheckDisk() async throws -> DiskAccess {
        actions.append("recheck")
        return DiskAccess(state: "unknown", source: "background", checkedAt: 1, verifiedTargets: 0)
    }
    func pairing(_ action: String, state: PairingState?) async throws -> PairingState {
        actions.append(action)
        return PairingState(phase: "starting", attempt: "attempt", origin: "https://fleet.example.test", url: nil, code: nil, deviceID: nil, ownerEmail: nil, error: nil)
    }
}
