import XCTest

final class FleetHubUITests: XCTestCase {
    private var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = false
        let reset = expectation(description: "Reset isolated fixture")
        URLSession.shared.dataTask(with: URL(string: "http://localhost:18765/__reset")!) { _, _, _ in reset.fulfill() }.resume()
        wait(for: [reset], timeout: 5)
        app = XCUIApplication()
        XCUIDevice.shared.orientation = .portrait
        app.launchArguments = ["-fleet-test-gateway", "http://localhost:18765", "-fleet-test-reset"]
        app.launch()
    }

    func testNativeListsAndClaudeTerminalSurviveSidebarToggleAndRotation() {
        let session = app.buttons["session-codex-session"]
        XCTAssertTrue(session.waitForExistence(timeout: 20))
        attachScreenshot("session-list")
        XCTAssertTrue(app.buttons["toggle-devices"].exists)
        app.buttons["toggle-devices"].tap()
        let device = app.buttons["device-m1"]
        XCTAssertTrue(device.waitForExistence(timeout: 5))
        if device.isHittable { device.tap() }
        XCTAssertTrue(session.waitForExistence(timeout: 10))
        session.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
        if !session.isHittable { app.buttons["toggle-sessions"].tap() }
        XCTAssertTrue(session.waitForExistence(timeout: 5))
        app.buttons["Claude"].tap()
        let claude = app.buttons["session-claude-session"]
        XCTAssertTrue(claude.waitForExistence(timeout: 15))
        claude.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture ttyd connected"].waitForExistence(timeout: 15))
        let keyboardInput = app.webViews.textViews["输入命令…"]
        if keyboardInput.exists {
            keyboardInput.tap()
            keyboardInput.typeText("hello")
            app.webViews.buttons["↑"].tap()
        }
        XCUIDevice.shared.orientation = .landscapeLeft
        XCTAssertTrue(app.webViews.staticTexts["Fixture ttyd connected"].waitForExistence(timeout: 5))
        XCUIDevice.shared.orientation = .portrait
        XCTAssertTrue(app.webViews.staticTexts["Fixture ttyd connected"].waitForExistence(timeout: 5))
        let checked = expectation(description: "Cached chat retains its assistant")
        URLSession.shared.dataTask(with: URL(string: "http://localhost:18765/__requests")!) { data, _, _ in
            if let data, let requests = try? JSONSerialization.jsonObject(with: data) as? [[String: Any]] {
                for request in requests {
                    let body = request["body"] as? [String: Any]
                    if body?["sessionId"] as? String == "codex-session" { XCTAssertEqual(body?["assistant"] as? String, "codex") }
                    let query = request["query"] as? String ?? ""
                    if query.contains("sessionId=codex-session") { XCTAssertTrue(query.contains("assistant=codex")) }
                }
            } else { XCTFail("Missing fixture request evidence") }
            checked.fulfill()
        }.resume()
        wait(for: [checked], timeout: 5)
        attachScreenshot("native-workspace")
    }

    func testExpandedPanelsCollapseIndependently() throws {
        try XCTSkipIf(!UIDeviceIsPad, "宽屏交互在宽屏模拟器单独验证")
        XCUIDevice.shared.orientation = .landscapeLeft
        let landscape = expectation(for: NSPredicate { _, _ in
            self.app.windows.firstMatch.frame.width > self.app.windows.firstMatch.frame.height
        }, evaluatedWith: app)
        wait(for: [landscape], timeout: 10)
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        XCTAssertTrue(app.buttons["device-m1"].isHittable)
        XCTAssertFalse(app.buttons["toggle-sessions"].exists)
        XCTAssertEqual(app.buttons.matching(identifier: "workspace-menu").count, 1)
        let rail = app.otherElements["device-panel"].firstMatch
        XCTAssertEqual(rail.frame.width, app.windows.firstMatch.frame.width <= 1180 ? 220 : 260, accuracy: 2)
        app.buttons["device-m2"].tap()
        XCTAssertTrue(app.staticTexts["Offline Mac 暂时无法连接"].waitForExistence(timeout: 10))
        attachScreenshot("web-aligned-offline-device")
        app.buttons["device-all"].tap()
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 10))
        let expandedWidth = app.buttons["device-m1"].frame.width
        app.buttons["toggle-devices"].tap()
        XCTAssertTrue(app.buttons["device-m1"].isHittable)
        XCTAssertLessThan(app.buttons["device-m1"].frame.width, expandedWidth)
        XCTAssertTrue(app.buttons["session-codex-session"].isHittable)
        app.buttons["pin-sessions"].tap()
        XCTAssertFalse(app.buttons["session-codex-session"].isHittable)
        app.buttons["sessions-launcher"].tap()
        XCTAssertTrue(app.buttons["session-codex-session"].isHittable)
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.4)).tap()
        XCTAssertFalse(app.buttons["session-codex-session"].isHittable)
        app.buttons["expand-devices"].tap()
        attachScreenshot("wide-device-expanded")
        app.buttons["device-m1"].tap()
        let selected = expectation(for: NSPredicate(format: "selected == true"), evaluatedWith: app.buttons["device-m1"])
        wait(for: [selected], timeout: 10)
        app.buttons["sessions-launcher"].tap()
        app.buttons["device-m2"].tap()
        XCTAssertTrue(app.staticTexts["Offline Mac 暂时无法连接"].waitForExistence(timeout: 10))
        app.buttons["device-m1"].tap()
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 10))
        app.buttons["pin-sessions"].tap()
        XCTAssertTrue(app.buttons["session-codex-session"].isHittable)
        attachScreenshot("web-aligned-wide-list")
        app.buttons["session-codex-session"].tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
        attachScreenshot("expanded-workspace")
    }

    func testMobileNavigationAndListGeometryMatchH5() throws {
        try XCTSkipIf(UIDeviceIsPad, "手机布局在 iPhone 模拟器验证")
        let session = app.buttons["session-codex-session"]
        XCTAssertTrue(session.waitForExistence(timeout: 20))
        XCTAssertFalse(app.buttons["toggle-sessions"].exists)
        XCTAssertFalse(app.buttons["pin-sessions"].exists)
        let modes = app.buttons["mode-sessions"]
        XCTAssertEqual(modes.frame.width, 56, accuracy: 3)
        XCTAssertEqual(modes.frame.height, 44, accuracy: 2)
        let scope = app.buttons["toggle-devices"]
        XCTAssertGreaterThan(scope.frame.minX, modes.frame.maxX)
        let search = app.textFields["session-search"]
        let project = app.buttons["project-cwd:/Users/fixture/project"]
        let empty = app.buttons["project-cwd:/Users/fixture/empty"]
        XCTAssertLessThan(project.frame.minY - search.frame.maxY, 55)
        XCTAssertLessThan(empty.frame.minY - project.frame.minY, 130)
        XCTAssertEqual(app.buttons["new-session"].frame.height, 44, accuracy: 2)
        attachScreenshot("h5-aligned-native-list")
        session.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
        XCTAssertTrue(app.buttons["toggle-sessions"].exists)
        app.buttons["toggle-sessions"].tap()
        XCTAssertTrue(session.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["toggle-sessions"].exists)
    }

    func testNativeThemeMatchesEmbeddedChat() {
        let session = app.buttons["session-codex-session"]
        XCTAssertTrue(session.waitForExistence(timeout: 20))
        session.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
        app.buttons["更多"].tap()
        app.buttons["外观"].tap()
        app.buttons["深色"].tap()
        let title = app.descendants(matching: .any).matching(identifier: "workspace-title").firstMatch
        let dark = expectation(for: NSPredicate(format: "value == 'dark'"), evaluatedWith: title)
        wait(for: [dark], timeout: 10)
        attachScreenshot("titanium-dark-chat")
        app.buttons["更多"].tap()
        app.buttons["外观"].tap()
        app.buttons["浅色"].tap()
        let light = expectation(for: NSPredicate(format: "value == 'light'"), evaluatedWith: title)
        wait(for: [light], timeout: 10)
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].exists)
        let composer = app.webViews.textViews.firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 5))
        let compactComposer = expectation(for: NSPredicate { _, _ in
            composer.frame.height > 0 && composer.frame.height < 100
        }, evaluatedWith: composer)
        wait(for: [compactComposer], timeout: 5)
        XCTAssertLessThan(composer.frame.height, 100, "Empty composer frame: \(composer.frame)")
        attachScreenshot("titanium-light-chat")
    }

    func testProjectsRecentSearchAndCapabilities() {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        XCTAssertFalse(app.buttons["DeepSeek"].exists)
        let empty = app.buttons["project-cwd:/Users/fixture/empty"]
        XCTAssertTrue(empty.exists)
        let project = app.buttons["project-cwd:/Users/fixture/project"]
        project.tap()
        XCTAssertFalse(app.buttons["session-codex-session"].isHittable)
        project.tap()
        XCTAssertTrue(app.buttons["session-codex-session"].isHittable)
        app.buttons["session-view-toggle"].tap()
        XCTAssertFalse(empty.exists)
        XCTAssertTrue(app.buttons["session-codex-session"].isHittable)
        let search = app.textFields["session-search"]
        search.tap(); search.typeText("no-matching-session")
        XCTAssertTrue(app.staticTexts["没有匹配的会话"].waitForExistence(timeout: 10))
        app.buttons["清除搜索"].tap()
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 10))
        attachScreenshot("native-recent-list")
    }

    func testRenamePinArchiveRestoreAndDelete() {
        let session = app.buttons["session-codex-session"]
        XCTAssertTrue(session.waitForExistence(timeout: 20))
        app.buttons["actions-codex-session"].tap()
        app.buttons["重命名"].tap()
        let name = app.alerts.textFields.firstMatch
        name.tap()
        name.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 30) + "Renamed fixture")
        name.typeText("\n")
        if app.alerts["重命名会话"].exists {
            let keyboardDismissed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: app.keyboards.firstMatch)
            wait(for: [keyboardDismissed], timeout: 5)
            app.alerts.buttons["保存"].tap()
        }
        let renamed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: app.alerts["重命名会话"])
        wait(for: [renamed], timeout: 5)
        XCTAssertTrue(session.staticTexts["Renamed fixture"].waitForExistence(timeout: 10))
        app.buttons["actions-codex-session"].tap(); app.buttons["置顶"].tap()
        app.buttons["actions-codex-session"].tap()
        XCTAssertTrue(app.buttons["取消置顶"].exists)
        app.buttons["取消置顶"].tap()
        app.buttons["归档会话"].tap()
        let archived = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: session)
        wait(for: [archived], timeout: 10)
        app.buttons["workspace-menu"].firstMatch.tap()
        app.buttons["显示已归档会话"].tap()
        XCTAssertTrue(session.waitForExistence(timeout: 10))
        app.buttons["移回当前会话"].tap()
        app.buttons["workspace-menu"].firstMatch.tap()
        app.buttons["显示当前会话"].tap()
        XCTAssertTrue(session.waitForExistence(timeout: 10))
        app.buttons["actions-codex-session"].tap(); app.buttons["删除"].tap()
        app.alerts.buttons["取消"].tap()
        XCTAssertTrue(session.exists)
        app.buttons["actions-codex-session"].tap(); app.buttons["删除"].tap()
        app.alerts.buttons["删除"].tap()
        let removed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: session)
        wait(for: [removed], timeout: 10)
    }

    func testFileTabsPreserveDraftAndChat() {
        let session = app.buttons["session-codex-session"]
        XCTAssertTrue(session.waitForExistence(timeout: 20)); session.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
        let composer = app.webViews.textViews.firstMatch
        composer.tap(); composer.typeText("Unsent fixture draft")
        app.buttons["workspace-menu"].firstMatch.tap(); app.buttons["刷新"].tap()
        let link = app.webViews.links["README.md"].firstMatch
        XCTAssertTrue(link.waitForExistence(timeout: 10)); link.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture document"].waitForExistence(timeout: 15))
        XCTAssertFalse(app.webViews.buttons["全屏"].exists)
        XCTAssertFalse(app.webViews.buttons["返回（不结束进程）"].exists)
        XCTAssertTrue(composer.exists)
        XCTAssertEqual(composer.value as? String, "Unsent fixture draft")
        app.webViews.buttons["codex fixture"].firstMatch.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].exists)
        link.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture document"].exists)
        app.webViews.buttons["关闭 README.md"].tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].exists)
        XCTAssertEqual(composer.value as? String, "Unsent fixture draft")
        attachScreenshot("native-workspace-tabs")
    }

    func testProjectAndUnscopedClaudeCreation() {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["Claude"].tap()
        XCTAssertTrue(app.buttons["session-claude-session"].waitForExistence(timeout: 10))
        app.buttons["new-project-cwd:/Users/fixture/project"].tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture ttyd connected"].waitForExistence(timeout: 15))
        app.buttons["toggle-sessions"].tap()
        app.buttons["new-session"].tap()
        app.buttons["Fixture Mac"].tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture ttyd connected"].waitForExistence(timeout: 15))
    }

    func testDeviceAppearanceSettingsAndFileMode() {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["toggle-devices"].tap()
        app.buttons["Fixture Mac 设备设置"].tap()
        XCTAssertTrue(app.webViews.switches["笔记本"].waitForExistence(timeout: 10))
        app.webViews.switches["笔记本"].tap()
        app.webViews.switches["玫瑰"].tap()
        app.webViews.buttons["保存"].tap()
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 10))
        app.buttons["mode-files"].firstMatch.tap()
        let file = app.webViews.otherElements.matching(NSPredicate(format: "label BEGINSWITH %@", "README.md ")).firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 15))
        XCTAssertTrue(file.isHittable)
        XCTAssertFalse(app.buttons["sessions-launcher"].exists)
        attachScreenshot("native-file-browser")
        app.buttons["mode-sessions"].firstMatch.tap()
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 10))
    }

    func testAuxiliaryAccountUsesNativeConfirmation() {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["workspace-menu"].firstMatch.tap(); app.buttons["账户"].tap()
        let revoke = app.webViews.buttons["退出其他设备的登录"]
        XCTAssertTrue(revoke.waitForExistence(timeout: 10))
        for _ in 0..<5 {
            if revoke.isHittable { break }
            app.webViews["auxiliary-webview"].swipeUp()
        }
        revoke.tap()
        XCTAssertTrue(app.alerts.staticTexts["退出其他设备的登录？当前登录会保留。"].waitForExistence(timeout: 5))
        app.alerts.buttons["取消"].tap()
        let fleet = app.webViews["auxiliary-webview"].links["Fleet"]
        for _ in 0..<5 {
            if fleet.isHittable { break }
            app.webViews["auxiliary-webview"].swipeDown()
        }
        fleet.tap()
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 10))
    }

    func testDSHCapabilityAndNativeUIEntry() {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        let enable = expectation(description: "Enable isolated DSH fixture")
        URLSession.shared.dataTask(with: URL(string: "http://localhost:18765/__dsh")!) { _, _, _ in enable.fulfill() }.resume()
        wait(for: [enable], timeout: 5)
        app.buttons["workspace-menu"].firstMatch.tap(); app.buttons["刷新"].tap()
        XCTAssertTrue(app.buttons["DeepSeek"].waitForExistence(timeout: 15))
        app.buttons["DeepSeek"].tap()
        XCTAssertTrue(app.buttons["session-dsh-session"].waitForExistence(timeout: 10))
        app.buttons["toggle-devices"].tap(); app.buttons["device-m1"].tap()
        let nativeUI = app.buttons["打开 DeepSeek Harness"]
        XCTAssertTrue(nativeUI.waitForExistence(timeout: 10))
        XCTAssertTrue(nativeUI.isEnabled)
        nativeUI.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture DSH UI"].waitForExistence(timeout: 10))
        app.buttons["完成"].tap()
        XCTAssertTrue(app.buttons["session-dsh-session"].waitForExistence(timeout: 10))
    }

    private var UIDeviceIsPad: Bool { app.windows.firstMatch.frame.width >= 700 }

    private func attachScreenshot(_ name: String) {
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
