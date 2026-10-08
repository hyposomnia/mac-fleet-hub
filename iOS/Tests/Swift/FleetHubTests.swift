import XCTest
import JavaScriptCore
@testable import FleetHub

final class FleetHubTests: XCTestCase {
    @MainActor
    func testNativeBootstrapCanAppendDebugResetWithoutBreakingBridge() {
        let view = FleetWebView(store: FleetStore(), layout: .compactDesktop, sessionsPinned: false)
        let context = JSContext()!
        context.evaluateScript("var window = {}; var document = {documentElement: {dataset: {}}};" + view.bootstrapScript + "if (true) { window.reset = true; }")
        XCTAssertNil(context.exception)
        XCTAssertEqual(context.evaluateScript("window.__fleetNativeVersion")?.toInt32(), 1)
        XCTAssertEqual(context.evaluateScript("document.documentElement.dataset.nativeLayout")?.toString(), "expanded")
        XCTAssertEqual(context.evaluateScript("document.documentElement.dataset.nativeSessionsPinned")?.toString(), "false")
        XCTAssertEqual(context.evaluateScript("window.reset")?.toBool(), true)
    }

    func testSharedTitaniumDesignResource() {
        XCTAssertEqual(TitaniumStyle.design.light["bg"], "#FFFFFF")
        XCTAssertEqual(TitaniumStyle.design.dark["accent"], "#B8D9FF")
        XCTAssertEqual(TitaniumStyle.radius("compact"), 8)
        XCTAssertEqual(TitaniumStyle.radius("control"), 12)
        XCTAssertEqual(TitaniumStyle.radius("card"), 16)
        XCTAssertEqual(TitaniumStyle.radius("panel"), 20)
    }

    func testWidthsMatchWebMobileBreakpoint() {
        XCTAssertEqual(FleetLayout.resolve(width: 393), .compact)
        XCTAssertEqual(FleetLayout.resolve(width: 699), .compact)
        XCTAssertEqual(FleetLayout.resolve(width: 700), .compact)
        XCTAssertEqual(FleetLayout.resolve(width: 860), .compact)
        XCTAssertEqual(FleetLayout.resolve(width: 861), .compactDesktop)
        XCTAssertEqual(FleetLayout.resolve(width: 899), .compactDesktop)
        XCTAssertEqual(FleetLayout.resolve(width: 900), .compactDesktop)
        XCTAssertEqual(FleetLayout.resolve(width: 1180), .compactDesktop)
        XCTAssertEqual(FleetLayout.resolve(width: 1181), .expanded)
        XCTAssertEqual(FleetLayout.resolve(width: 1200), .expanded)
        XCTAssertEqual(FleetLayout.expanded.deviceWidth, 260)
        XCTAssertEqual(FleetLayout.expanded.sessionWidth, 330)
        XCTAssertEqual(FleetLayout.compactDesktop.deviceWidth, 220)
        XCTAssertEqual(FleetLayout.compactDesktop.sessionWidth, 310)
        XCTAssertEqual(TitaniumStyle.metric("collapsedDeviceWidth"), 72)
    }

    func testGatewayValidationAndNATPorts() {
        XCTAssertEqual(GatewayAddress.parse("https://fleet.example.com:20443")?.absoluteString, "https://fleet.example.com:20443/")
        for value in ["http://fleet.test", "https://user:password@fleet.test", "https://fleet.test/path", "https://fleet.test/?token=secret", "https://fleet.test/#secret"] {
            XCTAssertNil(GatewayAddress.parse(value))
        }
        XCTAssertNotNil(GatewayAddress.parse("http://localhost:8765", allowLocalHTTP: true))
        XCTAssertNil(GatewayAddress.parse("http://fleet.test", allowLocalHTTP: true))
    }

    func testOriginIncludesSchemeAndEffectivePort() {
        let gateway = URL(string: "https://fleet.test:20443/")!
        XCTAssertTrue(GatewayAddress.sameOrigin(gateway, URL(string: "https://fleet.test:20443/auth/")!))
        XCTAssertFalse(GatewayAddress.sameOrigin(gateway, URL(string: "https://fleet.test/")!))
        XCTAssertFalse(GatewayAddress.sameOrigin(gateway, URL(string: "http://fleet.test:20443/")!))
        XCTAssertTrue(GatewayAddress.sameOrigin(URL(string: "https://fleet.test:443")!, URL(string: "https://fleet.test")!))
    }

    @MainActor
    func testAuthenticationLossClearsNativePrivateState() throws {
        let store = FleetStore()
        store.receive(["type": "snapshot", "payload": payload(ready: true)])
        XCTAssertTrue(store.ready)
        store.showingDetail = true
        store.receive(["type": "snapshot", "payload": payload(ready: false)])
        XCTAssertFalse(store.ready)
        XCTAssertFalse(store.showingDetail)
        store.reset()
        XCTAssertNil(store.snapshot)
    }

    private func payload(ready: Bool) -> [String: Any] {
        ["version": 1, "ready": ready, "identity": "test-user", "devices": [], "sessions": [],
         "deviceScope": "all", "assistant": "codex", "archived": false, "loading": false, "hasMore": false,
         "errors": [], "gatewayDown": false, "selectedID": NSNull(), "title": "Fleet Hub", "renderer": "chat"]
    }

    @MainActor
    func testIdentityChangeClosesPrivatePagesAndSearch() {
        let store = FleetStore()
        store.receive(["type": "snapshot", "payload": payload(ready: true)])
        store.auxiliaryPage = WebPage(url: URL(string: "https://fleet.test/account")!)
        store.search = "Private search"
        store.showingDetail = true
        var next = payload(ready: true)
        next["identity"] = "another-user"
        store.receive(["type": "snapshot", "payload": next])
        XCTAssertNil(store.auxiliaryPage)
        XCTAssertFalse(store.showingDetail)
        XCTAssertTrue(store.search.isEmpty)
    }

    @MainActor
    func testResetDeletesPrivateExport() throws {
        let store = FleetStore()
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: folder) }
        let file = folder.appendingPathComponent("fixture.txt")
        try Data("Fixture export".utf8).write(to: file)
        store.exportedFile = SharedFile(url: file)
        store.reset()
        XCTAssertNil(store.exportedFile)
        XCTAssertFalse(FileManager.default.fileExists(atPath: folder.path))
    }

    @MainActor
    func testWebProjectionRetainsEmptyProjectsAndCapabilities() {
        let store = FleetStore()
        var value = payload(ready: true)
        value["assistants"] = ["codex", "claude"]
        value["projects"] = [["id": "cwd:/empty", "title": "Empty", "cwd": "/empty", "macId": "m1",
                              "projectless": false, "sessionIDs": [], "collapsed": true]]
        value["view"] = "recent"
        value["mode"] = "files"
        store.receive(["type": "snapshot", "payload": value])
        XCTAssertEqual(store.assistants, [.codex, .claude])
        XCTAssertEqual(store.projects.count, 1)
        XCTAssertTrue(store.projects[0].sessionIDs.isEmpty)
        XCTAssertTrue(store.projects[0].collapsed)
        XCTAssertTrue(store.isFiles)
    }
}
