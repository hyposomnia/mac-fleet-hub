import XCTest
import JavaScriptCore
import SwiftUI
@testable import FleetHub

final class FleetHubTests: XCTestCase {
    @MainActor
    func testDeviceIconTextRejectsInvalidEditsAndPastesBeforeChangingValue() {
        var value = "Ab1"
        let input = NativeDeviceIconTextField(text: Binding(get: { value }, set: { value = $0 }))
        let coordinator = input.makeCoordinator()
        let field = UITextField()
        field.text = value
        func replace(_ range: NSRange, with text: String) -> Bool {
            guard coordinator.textField(field, shouldChangeCharactersIn: range, replacementString: text),
                  let swiftRange = Range(range, in: field.text ?? "") else { return false }
            field.text = (field.text ?? "").replacingCharacters(in: swiftRange, with: text)
            coordinator.changed(field)
            return true
        }
        XCTAssertTrue(replace(NSRange(location: 3, length: 0), with: "2"))
        XCTAssertEqual(value, "Ab12")
        XCTAssertFalse(replace(NSRange(location: 4, length: 0), with: "3"))
        for invalid in ["中", "é", "Ａ", "🙂", "_", "-", " ", "\n", "A1中", "ABCDE"] {
            XCTAssertFalse(replace(NSRange(location: 0, length: 4), with: invalid), invalid)
            XCTAssertEqual(value, "Ab12")
            XCTAssertEqual(field.text, "Ab12")
        }
        XCTAssertTrue(replace(NSRange(location: 1, length: 2), with: "z9"))
        XCTAssertEqual(value, "Az92")
        XCTAssertTrue(replace(NSRange(location: 0, length: 4), with: ""))
        XCTAssertEqual(value, "")
        XCTAssertTrue(replace(NSRange(location: 0, length: 0), with: "aZ09"))
        XCTAssertEqual(value, "aZ09")
    }

    @MainActor
    func testAppearanceValidatesTokensAndClearsAtAuthenticationLoss() {
        let store = FleetStore()
        store.receive(["type": "appearance", "tokens": ["accent": "#123456", "bg": "red", "unknown": "#123456"]])
        XCTAssertEqual(store.appearanceTokens, ["accent": "#123456"])
        store.reset()
        XCTAssertTrue(store.appearanceTokens.isEmpty)
        XCTAssertTrue(FleetWebView.appearanceScript.contains("FleetNativeAppearance"))
    }

    @MainActor
    func testOfflineScopeCannotCreateOnAnotherOnlineDevice() {
        let store = FleetStore()
        var value = payload(ready: true)
        value["devices"] = [["id": "m1", "name": "Online", "online": true],
                            ["id": "m2", "name": "Offline", "online": false]]
        value["deviceScope"] = "m2"
        store.receive(["type": "snapshot", "payload": value])
        XCTAssertFalse(store.canCreateSession)
        value["deviceScope"] = "all"
        store.receive(["type": "snapshot", "payload": value])
        XCTAssertTrue(store.canCreateSession)
    }

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
        XCTAssertEqual(TitaniumStyle.design.light["bg"], "#FAFAFA")
        XCTAssertEqual(TitaniumStyle.design.light["session-list-bg"], TitaniumStyle.design.light["bg"])
        XCTAssertEqual(TitaniumStyle.design.light["session-detail-bg"], TitaniumStyle.design.light["bg"])
        XCTAssertEqual(TitaniumStyle.design.dark["accent"], "#5BD7B7")
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

    func testDuoUsesAvailableWindowWidthInsteadOfDeviceName() {
        for width in [382.0, 466, 585, 669] {
            XCTAssertEqual(FleetLayout.resolve(width: width), .compact)
        }
        for width in [867.0, 951] {
            XCTAssertEqual(FleetLayout.resolve(width: width), .compactDesktop)
        }
        XCTAssertGreaterThanOrEqual(867 - FleetLayout.compactDesktop.deviceWidth - FleetLayout.compactDesktop.sessionWidth, 320)
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

    func testGatewayInputAddsHTTPSAndKeepsOriginValidation() {
        for address in ["fleet.example.com", " fleet.example.com\n", "https://fleet.example.com", "//fleet.example.com"] {
            XCTAssertEqual(GatewayAddress.parseInput(address)?.absoluteString, "https://fleet.example.com/")
        }
        XCTAssertEqual(GatewayAddress.parseInput("fleet.example.com:20443")?.absoluteString, "https://fleet.example.com:20443/")
        for address in ["", " \n", "http://fleet.example.com", "ftp://fleet.example.com", "user:password@fleet.example.com",
                        "fleet.example.com/path", "fleet.example.com/?token=secret", "fleet.example.com/#secret"] {
            XCTAssertNil(GatewayAddress.parseInput(address), address)
        }
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
    func testWebOverlayPresentationClearsOnDismissalAndReset() {
        let store = FleetStore()
        store.receive(["type": "snapshot", "payload": payload(ready: true)])
        store.receive(["type": "overlay", "visible": true])
        XCTAssertTrue(store.webOverlayVisible)
        store.receive(["type": "overlay", "visible": false])
        XCTAssertFalse(store.webOverlayVisible)
        store.receive(["type": "overlay", "visible": true])
        store.reset()
        XCTAssertFalse(store.webOverlayVisible)
    }

    @MainActor
    func testWebProjectionRetainsEmptyProjectsAndCapabilities() {
        let store = FleetStore()
        var value = payload(ready: true)
        value["assistants"] = ["codex", "claude", "dsh", "deepseek"]
        value["projects"] = [["id": "cwd:/empty", "title": "Empty", "cwd": "/empty", "macId": "m1",
                              "projectless": false, "sessionIDs": [], "collapsed": true]]
        value["view"] = "recent"
        value["mode"] = "files"
        store.receive(["type": "snapshot", "payload": value])
        XCTAssertEqual(store.assistants, [.codex, .dsh, .deepseek])
        XCTAssertEqual(store.projects.count, 1)
        XCTAssertTrue(store.projects[0].sessionIDs.isEmpty)
        XCTAssertTrue(store.projects[0].collapsed)
        XCTAssertTrue(store.isFiles)
    }

    @MainActor
    func testNativeServiceAllowsSettingsAutomationAndDeviceRoutes() {
        for (path, method) in [
            ("/api/settings", "POST"), ("/api/settings", "PATCH"), ("/api/names", "POST"),
            ("/api/automation/access-keys", "GET"), ("/api/automation/access-keys", "POST"),
            ("/api/automation/access-keys/fixture-key_1", "PATCH"), ("/api/automation/access-keys/fixture-key_1", "DELETE"),
            ("/api/auth/sessions", "GET"), ("/api/auth/logout-others", "POST"),
            ("/enroll/client-release.json", "GET"), ("/m1/api/info", "GET"), ("/m12/api/proxy", "POST"),
            ("/m1/api/projects?assistant=dsh", "GET"), ("/m1/api/sessions?assistant=codex&scope=all", "GET"),
        ] {
            XCTAssertTrue(FleetService.allowed(path, method: method), "\(method) \(path)")
        }
    }

    @MainActor
    func testNativeServiceRejectsExternalTraversalAndUnsupportedMutations() {
        for (path, method) in [
            ("https://fleet.test/api/settings", "POST"), ("//fleet.test/api/settings", "GET"),
            ("/api/settings#fragment", "GET"), ("/api/../api/settings", "GET"), ("/api/%73ettings", "GET"),
            ("/api/settings", "DELETE"), ("/api/settings", "HEAD"), ("/api/settings", "post"),
            ("/api/auth/sessions", "POST"), ("/api/auth/logout-others", "GET"),
            ("/enroll/client-release.json", "POST"), ("/m0/api/info", "GET"),
            ("/m1/api/sessions", "DELETE"), ("/m1/api/info", "POST"),
            ("/api/automation/access-keys/fixture/secret", "PATCH"), ("/m1/api/file/content", "GET"),
        ] {
            XCTAssertFalse(FleetService.allowed(path, method: method), "\(method) \(path)")
        }
    }

    func testAccessKeyMaskAndLegacyRecoverability() {
        let secret = "synthetic-only-fixture-access-key-0001"
        let key = AccessKey(["id": "fixture-key", "name": "Fixture", "key": secret, "recoverable": true,
                             "rpm": 25, "binding": ["device_id": "m1", "ai_client": "codex"]])
        XCTAssertTrue(key.recoverable)
        XCTAssertEqual(key.masked, "synthetic-on••••••0001")
        XCTAssertFalse(key.masked.contains(secret))
        XCTAssertEqual(key.rpm, 25)
        XCTAssertEqual(key.binding["device_id"], "m1")
        XCTAssertFalse(AccessKey(["recoverable": true]).recoverable)
        XCTAssertFalse(AccessKey(["key": secret, "recoverable": false]).recoverable)
        XCTAssertEqual(AccessKey([:]).masked, "••••••")
        XCTAssertEqual(AccessKey.mask("short-fixture"), "••••••")
        XCTAssertNotEqual(AccessKey.dateLabel("2026-10-17T01:20:06.227973411Z"), "暂无")
        XCTAssertNotEqual(AccessKey.dateLabel("2026-10-15T01:00:00Z"), "暂无")
        XCTAssertEqual(AccessKey.dateLabel("—"), "暂无")
    }

    func testNativeDialogDecodesControlStatesAndDynamicFileMenu() throws {
        let payload: [String: Any] = ["id": "native-file-menu-7", "title": "文件操作", "items": [
            ["id": "control-1", "kind": "button", "label": "删除", "destructive": true],
            ["id": "control-2", "kind": "picker", "label": "模型", "value": "fixture-model", "options": [
                ["value": "fixture-model", "label": "Fixture model"],
                ["value": "unavailable", "label": "Unavailable", "disabled": true],
            ]],
            ["id": "control-3", "kind": "button", "label": "图标视图", "selected": true],
            ["id": "control-4", "kind": "toggle", "label": "显示隐藏文件", "value": "true", "disabled": false],
        ], "footer": [["id": "control-5", "kind": "button", "label": "创建", "disabled": true]]]
        let dialog = try JSONDecoder().decode(NativeWebDialog.self, from: JSONSerialization.data(withJSONObject: payload))
        XCTAssertEqual(dialog.id, "native-file-menu-7")
        XCTAssertEqual(dialog.items[0].destructive, true)
        XCTAssertEqual(dialog.items[1].options?[1].disabled, true)
        XCTAssertEqual(dialog.items[1].options?[0].id, "fixture-model")
        XCTAssertEqual(dialog.items[2].selected, true)
        XCTAssertEqual(dialog.items[3].value, "true")
        XCTAssertEqual(dialog.footer[0].disabled, true)
    }

    @MainActor
    func testNativeDialogDismissalAndAuthenticationLossClearDrawers() {
        let store = FleetStore()
        store.receive(["type": "snapshot", "payload": payload(ready: true)])
        let dialog: [String: Any] = ["id": "chat-subagent-drawer", "title": "Sub Agent", "items": [], "footer": []]
        store.receive(["type": "native-dialog", "payload": dialog])
        XCTAssertEqual(store.webDialog?.id, "chat-subagent-drawer")
        store.receive(["type": "native-dialog", "payload": NSNull()])
        XCTAssertNil(store.webDialog)
        store.showOverlay("settings-page")
        XCTAssertEqual(store.nativeSheet, .settings)
        store.drawerLocked = true
        store.receive(["type": "native-dialog", "payload": dialog])
        store.receive(["type": "snapshot", "payload": payload(ready: false)])
        XCTAssertNil(store.webDialog)
        XCTAssertNil(store.nativeSheet)
        XCTAssertFalse(store.drawerLocked)
    }

    @MainActor
    func testIdentityChangeClearsNativeEditorsAndDialogs() {
        let store = FleetStore()
        store.receive(["type": "snapshot", "payload": payload(ready: true)])
        store.showOverlay("host", device: "m1")
        XCTAssertEqual(store.nativeSheet, .device("m1"))
        store.receive(["type": "native-dialog", "payload": ["id": "chat-options-popover", "title": "模型与推理", "items": [], "footer": []]])
        var next = payload(ready: true)
        next["identity"] = "another-user"
        store.receive(["type": "snapshot", "payload": next])
        XCTAssertNil(store.nativeSheet)
        XCTAssertNil(store.webDialog)
        XCTAssertFalse(store.drawerLocked)
    }

    @MainActor
    func testSystemSheetClearsOnIdentityChange() {
        let store = FleetStore()
        store.receive(["type": "snapshot", "payload": payload(ready: true)])
        store.nativeSheet = .device("m1")
        var next = payload(ready: true)
        next["identity"] = "another-user"
        store.receive(["type": "snapshot", "payload": next])
        XCTAssertNil(store.nativeSheet)
    }

    func testLoginDeviceLabelsHideRawUserAgent() {
        XCTAssertEqual(LoginDeviceLabel.name(["agent": "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148"]), "iPhone · Fleet AI")
        XCTAssertEqual(LoginDeviceLabel.name(["agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X) Chrome/140.0.0 Safari/537.36 Edg/140.0.0"]), "Mac · Edge")
        XCTAssertEqual(LoginDeviceLabel.name(["agent": "Mozilla/5.0 (Linux; Android 15) Chrome/140.0.0"]), "Android · Chrome")
    }

    func testProxyDefaultsPreserveConfiguredAddresses() {
        XCTAssertEqual(NativeDeviceEditor.proxyDefault(nil), "http://127.0.0.1:7897")
        XCTAssertEqual(NativeDeviceEditor.proxyDefault("  "), "http://127.0.0.1:7897")
        XCTAssertEqual(NativeDeviceEditor.proxyDefault("http://proxy.example:8080"), "http://proxy.example:8080")
    }

    func testDialogPresentationUsesMenusAndSystemAlerts() throws {
        for (id, expected) in [("file-name-modal", NativeWebDialog.Presentation.alert), ("file-path-modal", .alert), ("confirm-file-delete", .alert), ("native-file-menu-1", .menu), ("file-settings-menu", .menu), ("chat-options-popover", .alert)] {
            let data = try JSONSerialization.data(withJSONObject: ["id": id, "title": "fixture", "items": [], "footer": []])
            XCTAssertEqual(try JSONDecoder().decode(NativeWebDialog.self, from: data).presentation, expected)
        }
    }
}
