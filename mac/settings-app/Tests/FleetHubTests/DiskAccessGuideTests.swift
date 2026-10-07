import AppKit
import XCTest
@testable import FleetHub

@MainActor
final class DiskAccessGuideTests: XCTestCase {
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

    func testGuidanceRemainsVisibleAboveSettingsWithoutTakingActivation() throws {
        let agent = try fixture().appendingPathComponent("Fleet Agent.app")
        try FileManager.default.createDirectory(at: agent, withIntermediateDirectories: true)
        let application = try XCTUnwrap(DiskAccessApplication(url: agent))
        let panel = DiskAccessGuideController.makePanel(application: application)
        defer { panel.close() }
        XCTAssertTrue(panel.isFloatingPanel)
        XCTAssertFalse(panel.hidesOnDeactivate)
        XCTAssertTrue(panel.becomesKeyOnlyIfNeeded)
        XCTAssertTrue(panel.styleMask.contains(.nonactivatingPanel))
        XCTAssertEqual(panel.level, .floating)
        XCTAssertFalse(panel.isVisible)
    }

    func testFailedSettingsLaunchDoesNotShowGuidanceOrClaimPermission() throws {
        let agent = try fixture().appendingPathComponent("Fleet Agent.app")
        try FileManager.default.createDirectory(at: agent, withIntermediateDirectories: true)
        var opened: [URL] = []
        let guide = DiskAccessGuideController(openSettings: { opened.append($0); return false })
        XCTAssertThrowsError(try guide.show(applicationURL: agent))
        XCTAssertEqual(opened.map(\.absoluteString), ["x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"])
        XCTAssertNil(guide.panel)
    }
}
