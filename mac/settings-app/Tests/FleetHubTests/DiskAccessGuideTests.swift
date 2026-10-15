import AppKit
import FleetCore
import XCTest
@testable import FleetHub

@MainActor
final class DiskAccessGuideTests: XCTestCase {
    func testAuthorizationPreparesStandaloneAgentBeforeOpeningSettings() async throws {
        let agent = try fixture().appendingPathComponent("Fleet Agent.app")
        var events: [String] = []
        let guide = DiskAccessGuideController(openSettings: { _ in
            events.append("settings")
            XCTAssertNotNil(DiskAccessApplication(url: agent))
            return false
        })
        do {
            try await guide.authorize(applicationURL: agent) {
                events.append("prepare")
                try FileManager.default.createDirectory(at: agent, withIntermediateDirectories: true)
            }
            XCTFail("ignored settings launch failure")
        } catch { XCTAssertTrue(error.localizedDescription.contains("无法打开系统设置")) }
        XCTAssertEqual(events, ["prepare", "settings"])
    }

    func testFailedBackgroundPreparationDoesNotOpenSystemSettings() async throws {
        let agent = try fixture().appendingPathComponent("Fleet Agent.app")
        var opened = false
        let guide = DiskAccessGuideController(openSettings: { _ in opened = true; return true })
        do {
            try await guide.authorize(applicationURL: agent) { throw FleetError.message("busy background") }
            XCTFail("ignored background preparation failure")
        } catch { XCTAssertEqual(error.localizedDescription, "busy background") }
        XCTAssertFalse(opened)
    }

    func testGuideWaitsThroughUnknownAndRestrictedUntilActualVerification() async throws {
        let agent = try fixture().appendingPathComponent("Fleet Agent.app")
        try FileManager.default.createDirectory(at: agent, withIntermediateDirectories: true)
        var opened = 0
        var checks = 0
        var sleeps = 0
        let guide = DiskAccessGuideController(openSettings: { _ in opened += 1; return true })
        try await guide.waitForAccess(applicationURL: agent, recheck: {
            checks += 1
            return checks == 1 ? .unknown : checks == 2 ? .restricted : .verified
        }, sleep: { sleeps += 1 })
        XCTAssertEqual(opened, 1)
        XCTAssertEqual(checks, 3)
        XCTAssertEqual(sleeps, 2)
    }

    func testGuideCancellationDoesNotCompleteOrReopenSystemSettings() async throws {
        let agent = try fixture().appendingPathComponent("Fleet Agent.app")
        try FileManager.default.createDirectory(at: agent, withIntermediateDirectories: true)
        var opened = 0
        let guide = DiskAccessGuideController(openSettings: { _ in opened += 1; return true })
        do {
            try await guide.waitForAccess(applicationURL: agent, recheck: { .restricted }, sleep: { throw CancellationError() })
            XCTFail("completed without disk access")
        } catch { XCTAssertTrue(error is CancellationError) }
        XCTAssertEqual(opened, 1)
    }

    private func fixture() throws -> URL {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent("fleet-drag-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: root) }
        return root
    }

    func testOnlyAnExistingStandaloneAgentCanBeDragged() throws {
        let root = try fixture()
        let agent = root.appendingPathComponent("Fleet Agent.app")
        try FileManager.default.createDirectory(at: agent, withIntermediateDirectories: true)
        XCTAssertNotNil(DiskAccessApplication(url: agent))
        XCTAssertNil(DiskAccessApplication(url: root.appendingPathComponent("missing/Fleet Agent.app")))
        XCTAssertNil(DiskAccessApplication(url: URL(string: "https://fleet.test/Fleet%20Agent.app")!))
        let embedded = root.appendingPathComponent("Fleet Hub.app/Contents/Library/Fleet Agent.app")
        try FileManager.default.createDirectory(at: embedded, withIntermediateDirectories: true)
        XCTAssertNil(DiskAccessApplication(url: embedded))
        XCTAssertNil(DiskAccessApplication(url: root.appendingPathComponent("Fleet Hub.app")))
        let link = root.appendingPathComponent("alias/Fleet Agent.app")
        try FileManager.default.createDirectory(at: link.deletingLastPathComponent(), withIntermediateDirectories: true)
        try FileManager.default.createSymbolicLink(at: link, withDestinationURL: agent)
        XCTAssertNil(DiskAccessApplication(url: link))
    }

    func testDragWritesTheAgentFileURLNotAnImageOrTheHub() throws {
        let agent = try fixture().appendingPathComponent("Fleet Agent.app")
        try FileManager.default.createDirectory(at: agent, withIntermediateDirectories: true)
        let target = try XCTUnwrap(DiskAccessApplication(url: agent))
        let writer = try XCTUnwrap(target.draggingItem().item as? NSPasteboardWriting)
        let pasteboard = NSPasteboard(name: .init("fleet-drag-test-\(UUID().uuidString)"))
        defer { pasteboard.clearContents() }
        XCTAssertTrue(pasteboard.writeObjects([writer]))
        let storedURL = try XCTUnwrap(pasteboard.string(forType: .fileURL))
        XCTAssertEqual(URL(string: storedURL)?.path, agent.standardizedFileURL.path)
        XCTAssertEqual(pasteboard.pasteboardItems?.count, 1)
        XCTAssertNil(pasteboard.data(forType: .png))
    }

    func testAuthorizationOpensSystemSettingsForTheExistingPageDragTarget() throws {
        let agent = try fixture().appendingPathComponent("Fleet Agent.app")
        try FileManager.default.createDirectory(at: agent, withIntermediateDirectories: true)
        var opened: [URL] = []
        let guide = DiskAccessGuideController(openSettings: { opened.append($0); return true })
        try guide.show(applicationURL: agent)
        XCTAssertEqual(opened.map(\.absoluteString), ["x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"])
        XCTAssertNotNil(DiskAccessApplication(url: agent))
    }

    func testFailedSettingsLaunchDoesNotShowGuidanceOrClaimPermission() throws {
        let agent = try fixture().appendingPathComponent("Fleet Agent.app")
        try FileManager.default.createDirectory(at: agent, withIntermediateDirectories: true)
        var opened: [URL] = []
        let guide = DiskAccessGuideController(openSettings: { opened.append($0); return false })
        XCTAssertThrowsError(try guide.show(applicationURL: agent))
        XCTAssertEqual(opened.map(\.absoluteString), ["x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"])
    }
}
