import XCTest

final class FleetHubDeviceUATTests: XCTestCase {
    override func setUp() {
        super.setUp()
        continueAfterFailure = false
        #if !targetEnvironment(simulator)
        let app = XCUIApplication()
        app.activate()
        if app.buttons["关闭设置"].exists { app.buttons["关闭设置"].tap() }
        if app.buttons["toggle-sessions"].exists { app.buttons["toggle-sessions"].tap() }
        #endif
    }

    func testPhysicalIPhoneComposerWithoutSending() throws {
        #if targetEnvironment(simulator)
        throw XCTSkip("仅在用户选定真机验证键盘，不输入或发送真实消息")
        #else
        continueAfterFailure = false
        let app = XCUIApplication()
        app.activate()
        XCTAssertTrue(app.buttons["mode-sessions"].waitForExistence(timeout: 20))
        app.buttons["mode-sessions"].tap()
        app.buttons["ChatGPT"].firstMatch.tap()
        let session = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'session-' AND identifier != 'session-view-toggle' AND identifier != 'session-archive-toggle'")).firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 15))
        let available = expectation(for: NSPredicate(format: "enabled == true"), evaluatedWith: session)
        wait(for: [available], timeout: 20)
        session.tap()
        XCTAssertTrue(app.buttons["toggle-sessions"].waitForExistence(timeout: 30))
        let input = app.webViews.textViews.firstMatch
        XCTAssertTrue(input.waitForExistence(timeout: 20))
        XCTAssertFalse(app.buttons["workspace-menu"].exists)
        let draft = input.value as? String
        let collapsedWidth = input.frame.width
        XCTAssertEqual(input.frame.height, 44, accuracy: 2)
        let collapsed = XCTAttachment(screenshot: app.screenshot())
        collapsed.name = "physical-iphone-composer-single-line"
        collapsed.lifetime = .keepAlways
        add(collapsed)
        input.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 10))
        let expanded = expectation(for: NSPredicate { _, _ in input.frame.width > collapsedWidth + 40 }, evaluatedWith: input)
        wait(for: [expanded], timeout: 10)
        let keyboard = XCTAttachment(screenshot: app.screenshot())
        keyboard.name = "physical-iphone-composer-two-rows-keyboard"
        keyboard.lifetime = .keepAlways
        add(keyboard)
        app.buttons["toggle-sessions"].tap()
        XCTAssertTrue(app.buttons["workspace-menu"].waitForExistence(timeout: 5))
        session.tap()
        XCTAssertTrue(input.waitForExistence(timeout: 20))
        XCTAssertEqual(input.value as? String, draft)
        app.buttons["toggle-sessions"].tap()
        #endif
    }

    func testPhysicalIPhoneSettingsAndLongPress() throws {
        #if targetEnvironment(simulator)
        throw XCTSkip("仅在用户选定真机只读验证设置与长按菜单")
        #else
        continueAfterFailure = false
        let app = XCUIApplication()
        app.activate()
        XCTAssertTrue(app.buttons["mode-sessions"].waitForExistence(timeout: 20))
        app.buttons["mode-sessions"].tap()
        app.buttons["workspace-menu"].firstMatch.tap()
        let close = app.buttons["关闭设置"]
        XCTAssertTrue(close.waitForExistence(timeout: 15))
        app.buttons["native-settings-appearance"].tap()
        XCTAssertTrue(app.buttons["浅色"].firstMatch.waitForExistence(timeout: 5))
        let settings = XCTAttachment(screenshot: app.screenshot())
        settings.name = "physical-iphone-native-settings-drawer"
        settings.lifetime = .keepAlways
        add(settings)
        close.tap()
        let session = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'session-' AND identifier != 'session-view-toggle' AND identifier != 'session-archive-toggle'")).firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 15))
        session.press(forDuration: 0.7)
        XCTAssertTrue(app.buttons["打开会话"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["用 ttyd 打开"].exists)
        let menu = XCTAttachment(screenshot: app.screenshot())
        menu.name = "physical-iphone-native-session-long-press"
        menu.lifetime = .keepAlways
        add(menu)
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.02, dy: 0.16)).tap()
        #endif
    }

    func testPhysicalIPhoneNativeDeviceEditorAndSettingsInsets() throws {
        #if targetEnvironment(simulator)
        throw XCTSkip("仅在用户选定真机只读验证原生设备编辑器与外观抽屉")
        #else
        continueAfterFailure = false
        let app = XCUIApplication()
        app.activate()
        if app.buttons["toggle-sessions"].exists { app.buttons["toggle-sessions"].tap() }
        XCTAssertTrue(app.buttons["mode-sessions"].waitForExistence(timeout: 20))
        app.buttons["mode-sessions"].tap()
        app.buttons["toggle-devices"].tap()
        let deviceSettings = app.buttons.matching(NSPredicate(format: "label ENDSWITH '设备设置'")).firstMatch
        XCTAssertTrue(deviceSettings.waitForExistence(timeout: 10))
        deviceSettings.tap()
        let editor = app.descendants(matching: .any).matching(identifier: "native-device-editor").firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 10))
        let drawer = app.descendants(matching: .any).matching(identifier: "device-drawer").firstMatch
        XCTAssertTrue(drawer.waitForExistence(timeout: 5))
        XCTAssertEqual(drawer.frame.minX, app.frame.minX, accuracy: 1)
        XCTAssertEqual(drawer.frame.width, app.frame.width, accuracy: 1)
        XCTAssertEqual(drawer.frame.maxY, app.frame.maxY, accuracy: 1)
        let name = editor.textFields.matching(identifier: "设备名称（留空恢复默认）").firstMatch
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        let editorScreenshot = XCTAttachment(screenshot: app.screenshot())
        editorScreenshot.name = "physical-iphone-native-device-editor-insets"
        editorScreenshot.lifetime = .keepAlways
        add(editorScreenshot)
        editor.buttons.matching(NSPredicate(format: "label BEGINSWITH '关闭'")).firstMatch.tap()
        let editorDismissed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: editor)
        wait(for: [editorDismissed], timeout: 5)
        let sessions = app.descendants(matching: .any).matching(identifier: "session-panel").firstMatch
        XCTAssertTrue(sessions.waitForExistence(timeout: 5))
        XCTAssertTrue(app.textFields["session-search"].exists)
        let settings = app.buttons["workspace-menu"].firstMatch
        XCTAssertTrue(settings.waitForExistence(timeout: 5))
        settings.tap()
        let panel = app.descendants(matching: .any).matching(identifier: "native-settings-panel").firstMatch
        XCTAssertTrue(panel.waitForExistence(timeout: 10))
        app.buttons["native-settings-appearance"].tap()
        XCTAssertTrue(panel.buttons["浅色"].firstMatch.waitForExistence(timeout: 5))
        XCTAssertTrue(drawer.waitForExistence(timeout: 5))
        XCTAssertEqual(drawer.frame.minX, app.frame.minX, accuracy: 1)
        XCTAssertEqual(drawer.frame.width, app.frame.width, accuracy: 1)
        XCTAssertEqual(drawer.frame.maxY, app.frame.maxY, accuracy: 1)
        let appearanceScreenshot = XCTAttachment(screenshot: app.screenshot())
        appearanceScreenshot.name = "physical-iphone-native-appearance-insets"
        appearanceScreenshot.lifetime = .keepAlways
        add(appearanceScreenshot)
        panel.buttons["关闭设置"].tap()
        let settingsDismissed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: panel)
        wait(for: [settingsDismissed], timeout: 5)
        XCTAssertTrue(sessions.waitForExistence(timeout: 5))
        #endif
    }

    func testPhysicalIPhoneLoadsSaaSService() throws {
        #if targetEnvironment(simulator)
        throw XCTSkip("仅在用户选定真机验证 SaaS 入口")
        #else
        continueAfterFailure = false
        let app = XCUIApplication()
        app.activate()
        XCTAssertTrue(app.webViews.firstMatch.waitForExistence(timeout: 20))
        let ready = expectation(for: NSPredicate { _, _ in
            app.buttons["mode-sessions"].exists
        }, evaluatedWith: app)
        let result = XCTWaiter.wait(for: [ready], timeout: 30)
        let screenshot = XCTAttachment(screenshot: app.screenshot())
        screenshot.name = "physical-iphone-saas-entry"
        screenshot.lifetime = .keepAlways
        add(screenshot)
        XCTAssertEqual(result, .completed)
        XCTAssertFalse(app.textFields["gateway-address"].exists)
        XCTAssertFalse(app.alerts["操作提示"].exists)
        if app.buttons["完成"].exists { app.buttons["完成"].tap() }
        app.buttons["toggle-devices"].tap()
        let drawer = app.descendants(matching: .any).matching(identifier: "device-drawer").firstMatch
        XCTAssertTrue(drawer.waitForExistence(timeout: 5))
        XCTAssertEqual(drawer.frame.minX, app.frame.minX, accuracy: 1)
        XCTAssertEqual(drawer.frame.width, app.frame.width, accuracy: 1)
        XCTAssertEqual(drawer.frame.maxY, app.frame.maxY, accuracy: 1)
        let drawerScreenshot = XCTAttachment(screenshot: app.screenshot())
        drawerScreenshot.name = "physical-iphone-full-width-device-drawer"
        drawerScreenshot.lifetime = .keepAlways
        add(drawerScreenshot)
        app.buttons["完成"].tap()
        XCTAssertTrue(app.buttons["mode-sessions"].waitForExistence(timeout: 5))
        #endif
    }
}

final class FleetHubUITests: XCTestCase {
    func testServerPaletteAndLetterIconsMatchNativeNavigation() {
        let prepared = expectation(description: "Prepare disposable account appearance")
        URLSession.shared.dataTask(with: URL(string: "http://localhost:18765/__appearance")!) { _, _, _ in prepared.fulfill() }.resume()
        wait(for: [prepared], timeout: 5)
        app.terminate()
        app.launch()
        let modes = app.buttons["mode-sessions"]
        let light = expectation(for: NSPredicate(format: "value == '#123456'"), evaluatedWith: modes)
        wait(for: [light], timeout: 20)
        app.buttons["toggle-devices"].tap()
        XCTAssertTrue(app.staticTexts["AI"].firstMatch.waitForExistence(timeout: 5))
        app.buttons["device-m1"].tap()
        setTheme("深色")
        let dark = expectation(for: NSPredicate(format: "value == '#ABCDEF'"), evaluatedWith: modes)
        wait(for: [dark], timeout: 10)
        attachScreenshot("saas-account-palette-native")
    }
    private var app: XCUIApplication!

    func testSoftwareKeyboardGatewayAddressAndDone() {
        app.terminate()
        app.launchArguments = ["-fleet-test-gateway", "not-a-valid-origin", "-fleet-test-reset"]
        app.launch()
        let address = app.textFields["gateway-address"]
        XCTAssertTrue(address.waitForExistence(timeout: 10))
        let ready = expectation(for: NSPredicate(format: "hittable == true"), evaluatedWith: address)
        wait(for: [ready], timeout: 10)
        address.tap()
        assertSoftwareKeyboard(for: address)
        address.typeText("https://fleet.test/path\n")
        XCTAssertTrue(app.staticTexts["请输入 HTTPS 网关根地址，不包含账号、查询参数或子路径。"].waitForExistence(timeout: 5))
        address.tap()
        assertSoftwareKeyboard(for: address)
        app.buttons["完成输入"].firstMatch.tap()
        waitForKeyboardDismissal()
        XCTAssertEqual(address.value as? String, "https://fleet.test/path")
        attachScreenshot("software-keyboard-gateway-validation")
    }

    func testSoftwareKeyboardSearchRenameAndChat() throws {
        let session = app.buttons["session-codex-session"]
        XCTAssertTrue(session.waitForExistence(timeout: 20))
        let search = app.textFields["session-search"]
        search.tap()
        assertSoftwareKeyboard(for: search)
        app.keyboards.keys["f"].tap()
        XCTAssertEqual(search.value as? String, "f")
        app.keyboards.keys["delete"].tap()
        search.typeText("fixture\n")
        waitForKeyboardDismissal()
        XCTAssertEqual(search.value as? String, "fixture")
        session.press(forDuration: 0.7)
        app.buttons["重命名"].firstMatch.tap()
        let rename = nativeElement("native-session-rename")
        let name = rename.textFields["会话名称"]
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        name.tap()
        assertSoftwareKeyboard(for: name)
        name.typeText(" keyboard")
        attachScreenshot("software-keyboard-native-rename")
        rename.buttons["取消"].tap()
        let afterRename = XCTAttachment(string: app.debugDescription)
        afterRename.name = "keyboard-after-cancelling-rename"
        afterRename.lifetime = .keepAlways
        add(afterRename)
        waitForKeyboardDismissal()
        XCTAssertFalse(rename.exists)
        session.tap()
        let input = app.webViews.textViews.firstMatch
        XCTAssertTrue(input.waitForExistence(timeout: 15))
        input.tap()
        assertSoftwareKeyboard(for: input)
        input.typeText("Software keyboard draft")
        XCTAssertEqual(input.value as? String, "Software keyboard draft")
        attachScreenshot("software-keyboard-chat")
        if app.buttons["toggle-sessions"].exists {
            app.buttons["toggle-sessions"].tap()
            waitForKeyboardDismissal()
            session.tap()
            XCTAssertEqual(input.value as? String, "Software keyboard draft")
        }
    }

    func testSoftwareKeyboardDeviceNameAndURL() throws {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        if app.windows.firstMatch.frame.width <= 860 { app.buttons["toggle-devices"].tap() }
        app.buttons["Fixture Mac 设备设置"].firstMatch.tap()
        let editor = nativeElement("native-device-editor")
        let name = editor.textFields.matching(identifier: "设备名称（留空恢复默认）").firstMatch
        XCTAssertTrue(name.waitForExistence(timeout: 10))
        name.tap()
        assertSoftwareKeyboard(for: name)
        name.typeText(" keyboard")
        attachScreenshot("software-keyboard-device-name")
        editor.buttons["完成输入"].firstMatch.tap()
        waitForKeyboardDismissal()
        let proxy = editor.textFields.matching(identifier: "HTTP 代理").firstMatch
        let content = editor.scrollViews.firstMatch
        for _ in 0..<5 {
            if proxy.exists && proxy.isHittable { break }
            content.swipeUp()
        }
        XCTAssertTrue(proxy.waitForExistence(timeout: 5))
        proxy.tap()
        assertSoftwareKeyboard(for: proxy)
        proxy.typeText("http://127.0.0.1:9999")
        attachScreenshot("software-keyboard-native-url")
        editor.buttons["取消"].firstMatch.tap()
        waitForKeyboardDismissal()
    }

    func testSoftwareKeyboardNumericAndSecureFields() throws {
        openSettings(page: "automation")
        app.buttons["新建密钥"].tap()
        let panel = nativeElement("native-settings-panel")
        let rpm = panel.textFields.matching(identifier: "RPM").firstMatch
        reveal(rpm)
        rpm.tap()
        assertSoftwareKeyboard(for: rpm, numeric: true)
        rpm.typeText("2")
        attachScreenshot("software-keyboard-number-pad")
        panel.buttons.matching(identifier: "完成输入").allElementsBoundByIndex.first(where: \.isHittable)!.tap()
        waitForKeyboardDismissal()
        reveal(panel.buttons["取消"])
        panel.buttons["取消"].tap()
        app.buttons["native-settings-account"].tap()
        reveal(app.buttons["更改密码"])
        app.buttons["更改密码"].tap()
        let password = panel.secureTextFields["当前密码"]
        reveal(password)
        password.tap()
        assertSoftwareKeyboard(for: password)
        password.typeText("synthetic-only")
        attachScreenshot("software-keyboard-secure-field")
        panel.buttons.matching(identifier: "完成输入").allElementsBoundByIndex.first(where: \.isHittable)!.tap()
        waitForKeyboardDismissal()
        panel.buttons["取消"].firstMatch.tap()
        app.buttons["关闭设置"].tap()
        XCTAssertFalse(try fixtureRequests().contains { ($0["path"] as? String)?.contains("/password") == true })
    }

    func testSoftwareKeyboardFileNameAndDone() throws {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["mode-files"].tap()
        let create = webElement("新建文件夹")
        XCTAssertTrue(create.waitForExistence(timeout: 15))
        create.tap()
        let dialog = nativeElement("native-web-dialog-file-name-modal")
        let name = dialog.textFields["名称"]
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        name.tap()
        assertSoftwareKeyboard(for: name)
        name.typeText("Keyboard fixture folder")
        attachScreenshot("software-keyboard-file-name")
        dialog.buttons["完成输入"].firstMatch.tap()
        waitForKeyboardDismissal()
        XCTAssertEqual(name.value as? String, "Keyboard fixture folder")
        dialog.buttons["创建"].tap()
        let dismissed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: dialog)
        wait(for: [dismissed], timeout: 10)
        let request = try XCTUnwrap(try fixtureRequests().last { $0["path"] as? String == "/m1/api/file/mkdir" })
        XCTAssertEqual((request["body"] as? [String: Any])?["name"] as? String, "Keyboard fixture folder")
    }

    private func assertSoftwareKeyboard(for field: XCUIElement, numeric: Bool = false) {
        let keyboard = app.keyboards.firstMatch
        let visible = expectation(for: NSPredicate { _, _ in
            keyboard.exists && keyboard.keys.count >= (numeric ? 10 : 20) &&
                keyboard.frame.intersection(self.app.windows.firstMatch.frame).height > 150
        }, evaluatedWith: keyboard)
        wait(for: [visible], timeout: 10)
        print("Software keyboard: window=\(app.windows.firstMatch.frame) field=\(field.frame) keyboard=\(keyboard.frame) keys=\(keyboard.keys.count)")
        if !field.isHittable {
            let hierarchy = XCTAttachment(string: app.debugDescription)
            hierarchy.name = "keyboard-field-not-hittable"
            hierarchy.lifetime = .keepAlways
            add(hierarchy)
        }
        XCTAssertTrue(field.isHittable)
        XCTAssertLessThanOrEqual(field.frame.maxY, keyboard.frame.minY + 1)
    }

    private func waitForKeyboardDismissal() {
        let keyboard = app.keyboards.firstMatch
        let dismissed = expectation(for: NSPredicate { _, _ in
            !keyboard.exists || keyboard.keys.count == 0
        }, evaluatedWith: keyboard)
        wait(for: [dismissed], timeout: 10)
    }

    func testMobileComposerFocusExpandsAndRetainsDraft() {
        let session = app.buttons["session-codex-session"]
        XCTAssertTrue(session.waitForExistence(timeout: 20))
        session.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
        let input = app.webViews.textViews.firstMatch
        let attach = webElement("添加附件")
        XCTAssertTrue(input.waitForExistence(timeout: 5))
        XCTAssertEqual(input.frame.height, 44, accuracy: 2)
        let collapsedWidth = input.frame.width
        attachScreenshot("mobile-composer-single-line")
        input.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        let expanded = expectation(for: NSPredicate { _, _ in input.frame.width > collapsedWidth + 50 }, evaluatedWith: input)
        wait(for: [expanded], timeout: 5)
        input.typeText("Unsent composer draft")
        if attach.exists { XCTAssertLessThanOrEqual(input.frame.maxY, attach.frame.minY + 2) }
        attachScreenshot("mobile-composer-keyboard-two-rows")
        app.buttons["toggle-sessions"].tap()
        XCTAssertTrue(session.waitForExistence(timeout: 5))
        session.tap()
        let collapsed = expectation(for: NSPredicate { _, _ in input.frame.width < collapsedWidth + 5 && !self.app.keyboards.firstMatch.exists }, evaluatedWith: input)
        wait(for: [collapsed], timeout: 10)
        XCTAssertEqual(input.value as? String, "Unsent composer draft")
        attachScreenshot("mobile-composer-draft-collapsed")
    }

    private func setTheme(_ name: String) {
        let returnToChat = app.buttons["toggle-sessions"].exists
        if returnToChat { app.buttons["toggle-sessions"].tap() }
        let available = expectation(for: NSPredicate(format: "enabled == true"), evaluatedWith: app.buttons["workspace-menu"].firstMatch)
        wait(for: [available], timeout: 10)
        app.buttons["workspace-menu"].firstMatch.tap()
        let appearance = app.buttons["native-settings-appearance"]
        XCTAssertTrue(appearance.waitForExistence(timeout: 10))
        appearance.tap()
        let choice = app.buttons[name].firstMatch
        XCTAssertTrue(choice.waitForExistence(timeout: 5))
        choice.tap()
        app.buttons["关闭设置"].tap()
        if returnToChat {
            // SwiftUI context-menu rows can report a stale AX hit target after
            // transitions. Exercise the visible row with an actual touch.
            app.buttons["session-codex-session"].coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        }
    }

    func testSettingsDrawerUsesNativePagesAndDismissal() {
        let session = app.buttons["session-codex-session"]
        XCTAssertTrue(session.waitForExistence(timeout: 20))
        app.buttons["workspace-menu"].firstMatch.tap()
        let close = app.buttons["关闭设置"]
        XCTAssertTrue(close.waitForExistence(timeout: 10))
        let panel = nativeElement("native-settings-panel")
        XCTAssertTrue(panel.exists)
        let tabs = nativeElement("native-settings-tabs")
        XCTAssertTrue(tabs.waitForExistence(timeout: 5))
        XCTAssertEqual(tabs.frame.midY, close.frame.midY, accuracy: 3)
        XCTAssertLessThan(tabs.frame.maxX, close.frame.minX)
        XCTAssertFalse(app.tabBars.firstMatch.exists)
        XCTAssertFalse(webElement("关闭设置").exists)
        XCTAssertTrue(app.staticTexts["登录设备"].firstMatch.waitForExistence(timeout: 10))
        XCTAssertLessThan(tabs.frame.maxY, app.staticTexts["登录设备"].firstMatch.frame.minY)
        for identifier in ["add-device", "appearance", "automation", "account", "sessions"] {
            let tab = app.buttons["native-settings-" + identifier]
            XCTAssertTrue(tab.isHittable)
            tab.tap()
            XCTAssertTrue(tab.isSelected)
            XCTAssertTrue(app.navigationBars["设置"].exists)
            XCTAssertTrue(close.isHittable)
        }
        XCTAssertFalse(app.buttons["保存会话设置"].exists)
        XCTAssertFalse(app.staticTexts["回收不活跃连接不会丢失草稿或结束正在执行的任务。"].exists)
        attachScreenshot("native-settings-top-tabs")
        app.buttons["native-settings-add-device"].tap()
        XCTAssertFalse(app.staticTexts["拖动右侧手柄调整设备顺序。"].exists)
        attachScreenshot("native-settings-top-devices-tab")
        close.tap()
        XCTAssertTrue(session.waitForExistence(timeout: 10))
        app.buttons["workspace-menu"].firstMatch.tap()
        XCTAssertTrue(close.waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["native-settings-account"].isSelected)
        close.tap()
        XCTAssertTrue(session.waitForExistence(timeout: 10))
    }

    func testDeviceDrawerFillsMobileWidthInBothModes() throws {
        guard app.buttons["session-codex-session"].waitForExistence(timeout: 20) else {
            return XCTFail("Native workspace unavailable")
        }
        guard !app.descendants(matching: .any).matching(identifier: "device-panel").firstMatch.exists else {
            throw XCTSkip("仅验证紧凑布局底部抽屉")
        }
        for files in [false, true] {
            app.buttons[files ? "mode-files" : "mode-sessions"].tap()
            app.buttons["toggle-devices"].tap()
            let drawer = app.descendants(matching: .any).matching(identifier: "device-drawer").firstMatch
            XCTAssertTrue(drawer.waitForExistence(timeout: 5))
            XCTAssertEqual(drawer.frame.minX, app.frame.minX, accuracy: 1)
            XCTAssertEqual(drawer.frame.width, app.frame.width, accuracy: 1)
            XCTAssertEqual(drawer.frame.maxY, app.frame.maxY, accuracy: 1)
            XCTAssertEqual(app.buttons["device-all"].exists, !files)
            let originalHeight = drawer.frame.height
            let handle = app.descendants(matching: .any).matching(identifier: "device-drawer-handle").firstMatch
            handle.tap()
            let expanded = expectation(for: NSPredicate { _, _ in drawer.frame.height > originalHeight + 40 }, evaluatedWith: drawer)
            wait(for: [expanded], timeout: 5)
            XCTAssertEqual(drawer.frame.width, app.frame.width, accuracy: 1)
            attachScreenshot(files ? "files-full-width-device-drawer" : "sessions-full-width-device-drawer")
            app.buttons["完成"].tap()
            XCTAssertTrue(app.buttons["toggle-devices"].waitForExistence(timeout: 5))
            app.buttons["toggle-devices"].tap()
            app.buttons["device-drawer-backdrop"].coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.1)).tap()
            XCTAssertTrue(app.buttons["toggle-devices"].waitForExistence(timeout: 5))
            app.buttons["toggle-devices"].tap()
            XCTAssertTrue(handle.waitForExistence(timeout: 5))
            let start = handle.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            start.press(forDuration: 0.05, thenDragTo: start.withOffset(CGVector(dx: 0, dy: 160)))
            XCTAssertTrue(app.buttons["toggle-devices"].waitForExistence(timeout: 5))
        }
    }

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

    private func launchNativeAuthentication() {
        app.terminate()
        let prepared = expectation(description: "Enable isolated authentication")
        URLSession.shared.dataTask(with: URL(string: "http://localhost:18765/__auth")!) { _, _, _ in prepared.fulfill() }.resume()
        wait(for: [prepared], timeout: 5)
        app.launch()
        XCTAssertTrue(app.textFields["native-auth-email"].waitForExistence(timeout: 20))
        let available = expectation(for: NSPredicate(format: "enabled == true"), evaluatedWith: app.buttons["native-auth-submit"])
        wait(for: [available], timeout: 30)
        XCTAssertFalse(app.webViews.textFields.firstMatch.exists)
    }

    func testNativeLoginCompletesTOTPWithWebKitSession() {
        launchNativeAuthentication()
        app.textFields["native-auth-email"].tap()
        app.textFields["native-auth-email"].typeText("fixture@example.test")
        app.secureTextFields["native-auth-password"].tap()
        app.secureTextFields["native-auth-password"].typeText("synthetic-password")
        app.buttons["native-auth-submit"].tap()
        let code = app.textFields["native-auth-code"]
        XCTAssertTrue(code.waitForExistence(timeout: 10))
        code.tap(); code.typeText("123456")
        app.buttons["native-auth-submit"].tap()
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        XCTAssertFalse(app.textFields["native-auth-email"].exists)
        attachScreenshot("native-login-completed")
    }

    func testNativeRegistrationBindsAuthenticatorAndRequiresSavedCodes() {
        launchNativeAuthentication()
        app.buttons["native-auth-register"].tap()
        app.textFields["native-auth-email"].tap()
        app.textFields["native-auth-email"].typeText("fixture@example.test")
        app.secureTextFields["native-auth-password"].tap()
        app.secureTextFields["native-auth-password"].typeText("synthetic-password")
        app.secureTextFields["native-auth-confirmation"].tap()
        app.secureTextFields["native-auth-confirmation"].typeText("synthetic-password")
        attachScreenshot("native-registration-form")
        app.buttons["native-auth-submit"].tap()
        let code = app.textFields["native-auth-code"]
        XCTAssertTrue(code.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["native-auth-secret"].exists)
        attachScreenshot("native-authenticator-setup")
        code.tap(); code.typeText("123456")
        app.buttons["native-auth-submit"].tap()
        let finish = app.buttons["native-auth-finish"]
        XCTAssertTrue(finish.waitForExistence(timeout: 10))
        XCTAssertFalse(finish.isEnabled)
        attachScreenshot("native-recovery-codes")
        app.switches["native-auth-saved-codes"].tap()
        XCTAssertTrue(finish.isEnabled)
        finish.tap()
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
    }

    func testNativeRecoveryRebindsAuthenticator() {
        launchNativeAuthentication()
        app.buttons["native-auth-recovery"].tap()
        app.textFields["native-auth-email"].tap()
        app.textFields["native-auth-email"].typeText("fixture@example.test")
        app.secureTextFields["native-auth-password"].tap()
        app.secureTextFields["native-auth-password"].typeText("synthetic-password")
        app.textFields["native-auth-recovery"].tap()
        app.textFields["native-auth-recovery"].typeText("synthetic-recovery-code")
        app.buttons["native-auth-submit"].tap()
        XCTAssertTrue(app.staticTexts["native-auth-secret"].waitForExistence(timeout: 10))
        let code = app.textFields["native-auth-code"]
        code.tap(); code.typeText("123456")
        app.buttons["native-auth-submit"].tap()
        XCTAssertTrue(app.buttons["native-auth-finish"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["native-auth-finish"].isEnabled)
        app.switches["native-auth-saved-codes"].tap()
        app.buttons["native-auth-finish"].tap()
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
    }

    func testNativeListsAndChatSurviveSidebarToggleAndRotation() throws {
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
        XCTAssertFalse(app.buttons["Claude"].exists)
        XCTAssertFalse(app.buttons["session-claude-session"].exists)
        session.tap()
        XCUIDevice.shared.orientation = .landscapeLeft
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 5))
        XCUIDevice.shared.orientation = .portrait
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 5))
        let requests = try fixtureRequests()
        XCTAssertTrue(requests.contains { $0["path"] as? String == "/m1/api/chat/resume" })
        for request in requests {
            let body = request["body"] as? [String: Any]
            if body?["sessionId"] as? String == "codex-session" { XCTAssertEqual(body?["assistant"] as? String, "codex") }
            let query = request["query"] as? String ?? ""
            if query.contains("sessionId=codex-session") { XCTAssertTrue(query.contains("assistant=codex")) }
        }
        attachScreenshot("native-workspace")
    }

    func testExpandedPanelsCollapseIndependently() throws {
        try XCTSkipIf(!isExpandedWindow, "宽屏交互在展开窗口单独验证")
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
        app.otherElements["sessions-backdrop"].coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap()
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
        try XCTSkipIf(isExpandedWindow, "单栏布局在窄窗口验证")
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
        XCTAssertFalse(app.buttons["workspace-menu"].exists)
        attachScreenshot("mobile-detail-without-settings")
        app.buttons["toggle-sessions"].tap()
        XCTAssertTrue(session.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["toggle-sessions"].exists)
        XCTAssertTrue(app.buttons["workspace-menu"].exists)
    }

    func testInteractiveEdgeBackCancelsAndCompletesWithoutReloadingChat() throws {
        try XCTSkipIf(isExpandedWindow, "边缘返回在单栏窗口验证")
        let enabled = expectation(description: "Enable disposable DeepSeek fixture")
        URLSession.shared.dataTask(with: URL(string: "http://localhost:18765/__dsh")!) { _, _, _ in enabled.fulfill() }.resume()
        wait(for: [enabled], timeout: 5)
        app.terminate(); app.launch()
        XCTAssertTrue(app.buttons["assistant-picker"].waitForExistence(timeout: 20))
        let initialPageLoads = try fixtureRequests().filter { $0["path"] as? String == "/" }.count
        for (assistant, title) in [("codex", "ChatGPT"), ("dsh", "DSH Desktop")] {
            app.buttons["assistant-picker"].tap()
            app.buttons[title].firstMatch.tap()
            let session = app.buttons["session-\(assistant)-session"]
            XCTAssertTrue(session.waitForExistence(timeout: 15))
            session.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
            XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
            let composer = app.webViews.textViews.firstMatch
            let draft = "Keep \(assistant) draft"
            composer.tap(); composer.typeText(draft)
            let expectedDraft = try XCTUnwrap(composer.value as? String)
            XCTAssertTrue(expectedDraft.contains(draft))
            let window = app.windows.firstMatch
            let start = window.coordinate(withNormalizedOffset: CGVector(dx: 0.015, dy: 0.45))
            start.press(forDuration: 0.05,
                        thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: 0.18, dy: 0.45)),
                        withVelocity: .slow, thenHoldForDuration: 0.3)
            XCTAssertTrue(app.buttons["toggle-sessions"].waitForExistence(timeout: 5))
            XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].exists)
            XCTAssertFalse(session.isHittable, "短距离滑动应回弹到详情")
            XCTAssertEqual(composer.value as? String, expectedDraft)
            attachScreenshot("\(assistant)-edge-back-cancelled")

            // A horizontal swipe in the content and an edge vertical scroll are
            // ordinary chat gestures, not navigation.
            window.coordinate(withNormalizedOffset: CGVector(dx: 0.35, dy: 0.45))
                .press(forDuration: 0.05,
                       thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: 0.85, dy: 0.45)),
                       withVelocity: .slow, thenHoldForDuration: 0.3)
            start.press(forDuration: 0.05,
                        thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: 0.02, dy: 0.7)),
                        withVelocity: .slow, thenHoldForDuration: 0.3)
            XCTAssertTrue(app.buttons["toggle-sessions"].exists)

            start.press(forDuration: 0.05,
                        thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: 0.75, dy: 0.45)),
                        withVelocity: .slow, thenHoldForDuration: 0.3)
            XCTAssertTrue(session.waitForExistence(timeout: 5))
            XCTAssertFalse(app.buttons["toggle-sessions"].exists)
            attachScreenshot("\(assistant)-edge-back-completed")
            session.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
            XCTAssertTrue(app.buttons["toggle-sessions"].waitForExistence(timeout: 5))
            XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 10))
            XCTAssertEqual(composer.value as? String, expectedDraft)
            app.buttons["toggle-sessions"].tap()
            XCTAssertTrue(session.waitForExistence(timeout: 5))
        }
        let requests = try fixtureRequests()
        XCTAssertEqual(requests.filter { $0["path"] as? String == "/" }.count, initialPageLoads)
        for assistant in ["codex", "dsh"] {
            XCTAssertEqual(requests.filter { request in
                let body = request["body"] as? [String: Any]
                return request["path"] as? String == "/m1/api/chat/resume" && body?["assistant"] as? String == assistant
            }.count, 1, "返回后再次打开应复用会话连接")
        }
        XCTAssertFalse(requests.contains { $0["path"] as? String == "/m1/api/chat/send" })
    }

    func testNativeThemeMatchesEmbeddedChat() {
        let session = app.buttons["session-codex-session"]
        XCTAssertTrue(session.waitForExistence(timeout: 20))
        session.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
        setTheme("深色")
        let title = app.descendants(matching: .any).matching(identifier: "workspace-title").firstMatch
        let dark = expectation(for: NSPredicate(format: "value == 'dark'"), evaluatedWith: title)
        wait(for: [dark], timeout: 10)
        attachScreenshot("titanium-dark-chat")
        setTheme("浅色")
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
        // 不可用的助手不进下拉框：打开菜单只应看到 ChatGPT。
        app.buttons["assistant-picker"].tap()
        XCTAssertFalse(app.buttons["DSH Desktop"].exists)
        XCTAssertFalse(app.buttons["DeepSeek"].exists)
        XCTAssertFalse(app.buttons["Claude"].exists)
        app.buttons["ChatGPT"].firstMatch.tap()
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
        XCTAssertFalse(app.buttons["actions-codex-session"].exists)
        XCTAssertFalse(app.buttons["归档会话"].exists)
        session.press(forDuration: 0.7)
        XCTAssertFalse(app.buttons["用 ttyd 打开"].exists)
        app.buttons["重命名"].tap()
        let editor = app.alerts["重命名会话"]
        let name = editor.textFields["会话名称"]
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        name.tap()
        name.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 30) + "Renamed fixture")
        editor.buttons["保存"].tap()
        XCTAssertFalse(nativeElement("native-session-rename").exists)
        let title = expectation(for: NSPredicate(format: "label == 'Renamed fixture'"), evaluatedWith: session)
        wait(for: [title], timeout: 10)
        session.press(forDuration: 0.7); app.buttons["置顶"].tap()
        session.press(forDuration: 0.7)
        XCTAssertTrue(app.buttons["取消置顶"].exists)
        app.buttons["取消置顶"].tap()
        session.press(forDuration: 0.7); app.buttons["归档会话"].tap()
        let archived = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: session)
        wait(for: [archived], timeout: 10)
        app.buttons["session-archive-toggle"].tap()
        XCTAssertTrue(session.waitForExistence(timeout: 10))
        session.press(forDuration: 0.7); app.buttons["移回当前会话"].tap()
        app.buttons["session-archive-toggle"].tap()
        XCTAssertTrue(session.waitForExistence(timeout: 10))
        session.press(forDuration: 0.7); app.buttons["删除"].tap()
        app.alerts.buttons["取消"].tap()
        XCTAssertTrue(session.exists)
        session.press(forDuration: 0.7); app.buttons["删除"].tap()
        app.alerts.buttons["删除"].tap()
        let removed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: session)
        wait(for: [removed], timeout: 10)
    }

    func testMarkUnreadPersistsAfterRelaunchAndClearsWhenOpenedForBothAssistants() throws {
        let enabled = expectation(description: "Enable disposable DeepSeek fixture")
        URLSession.shared.dataTask(with: URL(string: "http://localhost:18765/__dsh")!) { _, _, _ in enabled.fulfill() }.resume()
        wait(for: [enabled], timeout: 5)
        app.terminate(); app.launch()
        // 刚 launch 时 SwiftUI 外壳先出现、WebView 还没 ready；此时点助手会被 store 丢弃。
        // 用「目标会话行出现」作为选中成功的判据，必要时重试。
        // 冷启动/重载时 WebView 还没把 capability 表推上来，此时切助手会被丢弃。
        // 先等任意会话行出现（列表已 ready），再重试选择并确认标签稳定。
        func waitForSessionList() {
            let anySession = app.buttons.matching(NSPredicate(format: "identifier ENDSWITH '-session'")).firstMatch
            XCTAssertTrue(anySession.waitForExistence(timeout: 20))
        }
        func selectAssistant(_ title: String, sessionID: String) {
            let picker = app.buttons["assistant-picker"]
            XCTAssertTrue(picker.waitForExistence(timeout: 20))
            var lastLabel = picker.label
            for _ in 0..<6 {
                picker.tap()
                let item = app.buttons[title].firstMatch
                if item.waitForExistence(timeout: 3) { item.tap() }
                lastLabel = picker.label
                if lastLabel == title, app.buttons[sessionID].waitForExistence(timeout: 2) {
                    Thread.sleep(forTimeInterval: 1)
                    if picker.label == title { return }
                }
                _ = app.buttons[sessionID].waitForExistence(timeout: 1)
            }
            XCTFail("未能切换到 \(title)，picker 仍是 \(lastLabel)")
        }
        for (assistant, title) in [("codex", "ChatGPT"), ("dsh", "DSH Desktop")] {
            // 每个助手都从一次干净启动开始，避免上一轮返回列表的转场状态残留。
            app.terminate(); app.launch()
            waitForSessionList()
            selectAssistant(title, sessionID: "session-\(assistant)-session")
            let session = app.buttons["session-\(assistant)-session"]
            XCTAssertTrue(session.waitForExistence(timeout: 15))
            let hittableDeadline = Date().addingTimeInterval(10)
            while Date() < hittableDeadline && !session.isHittable { usleep(200_000) }
            XCTAssertEqual(app.buttons["assistant-picker"].label, title, "选中后被打回其它助手")
            XCTAssertTrue(session.isEnabled, "会话行被 disabled（store.busy 或设备离线）")
            XCTAssertTrue(session.isHittable, "会话行存在但不可点击")
            session.press(forDuration: 0.7)
            let mark = app.buttons["标为未读"]
            XCTAssertTrue(mark.waitForExistence(timeout: 5))
            attachScreenshot("native-\(assistant)-mark-unread-menu")
            mark.tap()
            let unread = expectation(for: NSPredicate(format: "value == '未读'"), evaluatedWith: session)
            wait(for: [unread], timeout: 10)
            attachScreenshot("native-\(assistant)-unread-indicator")
            // Relaunch loses the WebView state. The fixture's server state must win.
            app.terminate(); app.launch()
            waitForSessionList()
            selectAssistant(title, sessionID: "session-\(assistant)-session")
            XCTAssertTrue(session.waitForExistence(timeout: 15))
            let persisted = expectation(for: NSPredicate(format: "value == '未读'"), evaluatedWith: session)
            wait(for: [persisted], timeout: 10)
            session.tap()
            XCTAssertTrue(app.buttons["toggle-sessions"].waitForExistence(timeout: 15))
            app.buttons["toggle-sessions"].tap()
            XCTAssertTrue(session.waitForExistence(timeout: 10))
            let read = expectation(for: NSPredicate(format: "value == '已读'"), evaluatedWith: session)
            wait(for: [read], timeout: 10)
        }
        let requests = try fixtureRequests().filter { $0["path"] as? String == "/m1/api/sessions/read" }
        for assistant in ["codex", "dsh"] {
            XCTAssertTrue(requests.contains { request in
                let body = request["body"] as? [String: Any]
                return body?["assistant"] as? String == assistant && body?["unread"] as? Bool == true
            })
            XCTAssertTrue(requests.contains { request in
                let body = request["body"] as? [String: Any]
                return body?["assistant"] as? String == assistant && body?["explicit"] as? Bool == true && (body?["expectedVersion"] as? Int ?? 0) > 0
            })
        }
    }

    func testFileTabsPreserveDraftAndChat() {
        let session = app.buttons["session-codex-session"]
        XCTAssertTrue(session.waitForExistence(timeout: 20)); session.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
        let composer = app.webViews.textViews.firstMatch
        composer.tap(); composer.typeText("Unsent fixture draft")
        let link = app.webViews.links["README.md"].firstMatch
        XCTAssertTrue(link.waitForExistence(timeout: 10)); link.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture document"].waitForExistence(timeout: 15))
        XCTAssertFalse(webElement("全屏").exists)
        XCTAssertFalse(webElement("返回（不结束进程）").exists)
        XCTAssertTrue(composer.exists)
        XCTAssertEqual(composer.value as? String, "Unsent fixture draft")
        webElement("codex fixture").tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].exists)
        link.tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture document"].exists)
        webElement("关闭 README.md").tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].exists)
        XCTAssertEqual(composer.value as? String, "Unsent fixture draft")
        attachScreenshot("native-workspace-tabs")
    }

    func testProjectAndUnscopedChatGPTDraftCreation() throws {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        XCTAssertFalse(app.buttons["Claude"].exists)
        app.buttons["new-project-cwd:/Users/fixture/project"].tap()
        XCTAssertTrue(app.webViews.textViews.firstMatch.waitForExistence(timeout: 15))
        XCTAssertTrue(webElement("选择会话项目").waitForExistence(timeout: 10))
        app.buttons["toggle-sessions"].tap()
        app.buttons["new-session"].tap()
        XCTAssertTrue(app.buttons["关闭选择设备"].waitForExistence(timeout: 5))
        let offline = app.buttons.matching(NSPredicate(format: "label CONTAINS 'Offline Mac'")).firstMatch
        XCTAssertFalse(offline.isEnabled)
        app.buttons.matching(NSPredicate(format: "label CONTAINS 'Fixture Mac'")).firstMatch.tap()
        XCTAssertTrue(webElement("选择会话项目").waitForExistence(timeout: 10))
        webElement("选择会话项目").tap()
        XCTAssertTrue(nativeElement("native-web-dialog-new-project-modal").waitForExistence(timeout: 5))
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH '无项目'")).firstMatch.tap()
        XCTAssertTrue(app.webViews.textViews.firstMatch.exists)
        XCTAssertFalse(try fixtureRequests().contains { ["/m1/api/chat/send", "/m1/api/new"].contains($0["path"] as? String ?? "") })
    }

    func testDevicePickerUsesNativeRowsAndSharedStatusTags() {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["mode-files"].firstMatch.tap()
        app.buttons["toggle-devices"].tap()
        let list = nativeElement("native-device-picker-list")
        XCTAssertTrue(list.waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts["选择设备浏览文件"].exists)
        XCTAssertEqual(list.staticTexts["device-status-m1"].label, "设备状态：在线")
        XCTAssertEqual(list.staticTexts["device-status-m2"].label, "设备状态：离线")
        XCTAssertTrue(list.buttons["device-m1"].isSelected)
        attachScreenshot("native-file-device-picker")
        list.buttons["Fixture Mac 设备设置"].tap()
        let editor = nativeElement("native-device-editor")
        XCTAssertTrue(editor.waitForExistence(timeout: 10))
        XCTAssertEqual(editor.staticTexts["device-status-tag"].label, "设备状态：在线")
        app.navigationBars.buttons["取消"].tap()
        XCTAssertTrue(list.waitForExistence(timeout: 5))
        list.buttons["device-m1"].tap()
        XCTAssertFalse(list.exists)
        app.buttons["mode-sessions"].firstMatch.tap()
        app.buttons["toggle-devices"].tap()
        XCTAssertTrue(list.waitForExistence(timeout: 5))
        XCTAssertTrue(list.buttons["device-all"].exists)
        XCTAssertTrue(list.staticTexts["device-status-m1"].exists)
        XCTAssertTrue(list.staticTexts["device-status-m2"].exists)
        attachScreenshot("native-session-device-picker")
    }

    func testDeviceAppearanceSettingsAndFileMode() {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["toggle-devices"].tap()
        app.buttons["Fixture Mac 设备设置"].tap()
        XCTAssertTrue(nativeElement("native-device-editor").waitForExistence(timeout: 10))
        app.buttons["笔记本"].tap()
        app.buttons["rose"].tap()
        app.buttons["保存"].tap()
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
        app.buttons["workspace-menu"].firstMatch.tap()
        XCTAssertTrue(nativeElement("native-settings-panel").waitForExistence(timeout: 10))
        let revoke = app.buttons["退出其他设备的登录"]
        XCTAssertTrue(revoke.waitForExistence(timeout: 10))
        for _ in 0..<5 {
            if revoke.isHittable { break }
            nativeElement("native-settings-content").swipeUp()
        }
        revoke.tap()
        XCTAssertTrue(app.alerts.staticTexts["退出其他设备的登录？当前登录会保留。"].waitForExistence(timeout: 5))
        app.alerts.buttons["取消"].tap()
        app.buttons["关闭设置"].tap()
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 10))
    }

    func testDSHCapabilityAndExternalBrowserEntry() throws {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        let enable = expectation(description: "Enable isolated DSH fixture")
        URLSession.shared.dataTask(with: URL(string: "http://localhost:18765/__dsh")!) { _, _, _ in enable.fulfill() }.resume()
        wait(for: [enable], timeout: 5)
        app.terminate(); app.launch()
        XCTAssertTrue(app.buttons["assistant-picker"].waitForExistence(timeout: 15))
        app.buttons["assistant-picker"].tap()
        app.buttons["DSH Desktop"].tap()
        XCTAssertTrue(app.buttons["session-dsh-session"].waitForExistence(timeout: 10))
        app.buttons["toggle-devices"].tap(); app.buttons["device-m1"].tap()
        let nativeUI = app.buttons["打开 DSH Desktop 原生界面"]
        XCTAssertTrue(nativeUI.waitForExistence(timeout: 10))
        XCTAssertTrue(nativeUI.isEnabled)
        attachScreenshot("deepseek-tab-whale-spacing")
        nativeUI.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        let leftApp = expectation(for: NSPredicate { _, _ in self.app.state != .runningForeground }, evaluatedWith: app)
        wait(for: [leftApp], timeout: 10)
        app.activate()
        XCTAssertTrue(app.buttons["session-dsh-session"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.webViews.staticTexts["Fixture DSH UI"].exists)
        XCTAssertTrue(try fixtureRequests().contains { $0["path"] as? String == "/m1/dsh/" })
        app.buttons["session-dsh-session"].tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
        let approval = webElement("如何批准 ChatGPT 操作？")
        XCTAssertFalse(approval.exists && approval.isEnabled)
    }

    func testAutomationListOpensDetailsAndDisablesLegacyCopy() {
        openSettings(page: "automation")
        let row = app.buttons["access-key-fixture-key"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["synthetic-on••••••0001"].exists)
        XCTAssertFalse(app.staticTexts["synthetic-only-fixture-access-key-0001"].exists)
        XCTAssertFalse(app.buttons["复制密钥"].exists)
        XCTAssertFalse(app.buttons["复制请求示例"].exists)
        XCTAssertFalse(app.staticTexts["未限定范围的密钥可向你的全部设备会话发送消息，请妥善保管。"].exists)
        XCTAssertTrue(row.label.contains("最近使用"))
        attachScreenshot("native-automation-compact-list")
        row.tap()
        XCTAssertTrue(app.navigationBars["密钥详情"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["复制密钥"].isEnabled)
        app.buttons["复制密钥"].tap()
        let copied = app.staticTexts["密钥已复制（两分钟后从剪贴板清除）"]
        reveal(copied)
        XCTAssertTrue(copied.waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["未限定范围的密钥可向你的全部设备会话发送消息，请妥善保管。"].exists)
        attachScreenshot("native-automation-detail")
        app.navigationBars["密钥详情"].buttons.element(boundBy: 0).tap()
        app.buttons["access-key-fixture-legacy"].tap()
        XCTAssertTrue(app.staticTexts["旧版密钥不可恢复"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["复制密钥"].isEnabled)
        XCTAssertFalse(app.buttons["复制请求示例"].isEnabled)
    }

    func testAutomationKeyCreateEditDeleteAndValidation() throws {
        openSettings(page: "automation")
        XCTAssertTrue(app.buttons["新建密钥"].waitForExistence(timeout: 10))
        app.buttons["新建密钥"].tap()
        let panel = nativeElement("native-key-editor")
        let name = panel.textFields.matching(identifier: "名称（绑定到会话后即为别名）").firstMatch
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["保存"].isEnabled)
        replaceText(name, with: "Created fixture key")
        replaceText(panel.textFields.matching(identifier: "RPM").firstMatch, with: "0")
        XCTAssertTrue(panel.buttons["保存"].exists)
        XCTAssertFalse(panel.buttons["保存"].isEnabled)
        replaceText(panel.textFields.matching(identifier: "RPM").firstMatch, with: "25")
        if app.keyboards.firstMatch.exists {
            attachScreenshot("native-key-editor-number-keyboard-before-layout-assertion")
            XCTAssertTrue(panel.textFields.matching(identifier: "RPM").firstMatch.isHittable)
            panel.buttons.matching(identifier: "完成输入").allElementsBoundByIndex.first(where: \.isHittable)!.tap()
            let keyboardDismissed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: app.keyboards.firstMatch)
            wait(for: [keyboardDismissed], timeout: 5)
        }
        reveal(app.buttons["保存"])
        app.buttons["保存"].tap()
        XCTAssertTrue(app.buttons["新建密钥"].waitForExistence(timeout: 10))
        reveal(app.staticTexts["Created fixture key"])
        XCTAssertTrue(app.staticTexts["Created fixture key"].exists)
        app.buttons["access-key-fixture-created-1"].tap()
        app.navigationBars["密钥详情"].buttons["编辑"].tap()
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        replaceText(name, with: "Edited fixture key")
        if app.keyboards.firstMatch.exists { panel.buttons.matching(identifier: "完成输入").allElementsBoundByIndex.first(where: \.isHittable)!.tap() }
        reveal(app.buttons["保存"])
        app.buttons["保存"].tap()
        let editClosed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: panel)
        wait(for: [editClosed], timeout: 10)
        XCTAssertTrue(app.navigationBars["密钥详情"].waitForExistence(timeout: 10))
        attachScreenshot("native-key-detail-after-edit")
        app.navigationBars["密钥详情"].buttons["编辑"].tap()
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        XCTAssertEqual(name.value as? String, "Edited fixture key")
        reveal(app.buttons["删除"])
        app.buttons["删除"].tap()
        XCTAssertTrue(app.alerts.staticTexts["删除此密钥？"].waitForExistence(timeout: 5))
        let confirmation = app.alerts.containing(.staticText, identifier: "删除此密钥？").firstMatch
        confirmation.buttons["取消"].firstMatch.tap()
        XCTAssertTrue(name.exists)
        app.buttons["删除"].tap()
        confirmation.buttons["删除"].firstMatch.tap()
        XCTAssertTrue(app.buttons["新建密钥"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["Edited fixture key"].exists)
        let writes = try fixtureRequests().filter {
            ($0["path"] as? String ?? "").hasPrefix("/api/automation/access-keys") && $0["method"] as? String != "GET"
        }
        XCTAssertEqual(writes.compactMap { $0["method"] as? String }, ["POST", "PATCH", "DELETE"])
        XCTAssertEqual((writes.first { $0["method"] as? String == "PATCH" }?["body"] as? [String: Any])?["name"] as? String, "Edited fixture key")
        let created = try XCTUnwrap(writes.first?["body"] as? [String: Any])
        XCTAssertEqual(created["rpm"] as? Int, 25)
        XCTAssertTrue(created["binding"] is NSNull)
    }

    func testNativeSessionSettingsPersistAndDeviceManifestLoads() throws {
        openSettings(page: "sessions")
        let field = app.buttons["setting-chatCacheMaxSessions"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap()
        let number = app.textFields["setting-number"]
        XCTAssertTrue(number.waitForExistence(timeout: 5))
        number.tap(); number.typeText(XCUIKeyboardKey.delete.rawValue + "7")
        app.navigationBars.buttons["完成"].tap()
        XCTAssertFalse(app.buttons["保存会话设置"].exists)
        app.buttons["native-settings-account"].tap()
        app.buttons["native-settings-sessions"].tap()
        XCTAssertTrue(field.label.contains("7"))
        // A failed Done keeps the draft and the sheet available for retry.
        let failure = expectation(description: "Fail one isolated settings save")
        URLSession.shared.dataTask(with: URL(string: "http://localhost:18765/__fail-settings-once")!) { _, _, _ in failure.fulfill() }.resume()
        wait(for: [failure], timeout: 5)
        app.buttons["关闭设置"].tap()
        XCTAssertTrue(app.staticTexts["合成设置保存失败"].waitForExistence(timeout: 10))
        XCTAssertTrue(field.label.contains("7"))
        app.buttons["关闭设置"].tap()
        XCTAssertTrue(app.buttons["workspace-menu"].firstMatch.waitForExistence(timeout: 10))
        openSettings(page: "add-device")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'App 不支持新增设备' AND label CONTAINS 'localhost:18765'")).firstMatch.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["在浏览器下载安装包"].exists)
        let requests = try fixtureRequests()
        let saved = try XCTUnwrap(requests.last { $0["path"] as? String == "/api/settings" && $0["method"] as? String == "POST" }?["body"] as? [String: Any])
        XCTAssertEqual(saved["chatCacheMaxSessions"] as? Int, 7)
        XCTAssertEqual(saved["mobileMaxWindows"] as? Int, 4)
        attachScreenshot("native-session-number-sheet")
    }

    func testNativeFileSettingsAndCreateFolderDrawer() throws {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["mode-files"].tap()
        let settings = webElement("文件菜单")
        XCTAssertTrue(settings.waitForExistence(timeout: 15))
        settings.tap()
        let icons = app.buttons["图标视图"]
        if !icons.waitForExistence(timeout: 5) {
            attachScreenshot("file-menu-missing")
            add(XCTAttachment(string: app.debugDescription))
        }
        XCTAssertTrue(icons.exists)
        XCTAssertFalse(nativeElement("native-web-dialog-file-settings-menu").exists)
        attachScreenshot("native-file-settings-menu")
        icons.tap()
        webElement("新建文件夹").tap()
        let alert = app.alerts["新建文件夹"]
        XCTAssertTrue(alert.waitForExistence(timeout: 5))
        XCTAssertFalse(alert.buttons["创建"].isEnabled)
        alert.textFields["名称"].tap(); alert.textFields["名称"].typeText("Fixture folder")
        alert.buttons["创建"].tap()
        let dismissed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: alert)
        wait(for: [dismissed], timeout: 10)
        let request = try XCTUnwrap(try fixtureRequests().last { $0["path"] as? String == "/m1/api/file/mkdir" })
        XCTAssertEqual((request["body"] as? [String: Any])?["name"] as? String, "Fixture folder")
        XCTAssertFalse(webElement("1 项").exists)
        attachScreenshot("native-folder-input-alert-finished")
    }

    func testNativeFileMenuSortsAndGoToAlert() throws {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["mode-files"].tap()
        let menu = webElement("文件菜单")
        XCTAssertTrue(menu.waitForExistence(timeout: 15))
        XCTAssertFalse(webElement("文件夹路径").exists)
        menu.tap()
        XCTAssertTrue(app.buttons["文件名升序"].waitForExistence(timeout: 5))
        for title in ["文件名降序", "创建时间升序", "创建时间降序", "修改时间升序", "修改时间降序"] {
            XCTAssertTrue(app.buttons[title].exists)
        }
        attachScreenshot("native-file-menu-sorting")
        app.buttons["创建时间降序"].tap()
        menu.tap()
        XCTAssertTrue(app.buttons["前往文件夹…"].waitForExistence(timeout: 5))
        app.buttons["前往文件夹…"].tap()
        let alert = app.alerts["前往文件夹"]
        XCTAssertTrue(alert.waitForExistence(timeout: 5))
        let field = alert.textFields["文件夹路径"]
        field.tap()
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap()
        let initial = field.value as? String ?? ""
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: initial.count * 2) + "relative")
        XCTAssertEqual(field.value as? String, "relative")
        XCTAssertFalse(alert.buttons["前往"].isEnabled)
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 8) + "/Volumes/Fixture Folder")
        XCTAssertEqual(field.value as? String, "/Volumes/Fixture Folder")
        XCTAssertTrue(alert.buttons["前往"].isEnabled)
        attachScreenshot("native-go-to-folder-alert")
        alert.buttons["前往"].coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        if alert.exists {
            attachScreenshot("native-go-to-folder-after-submit")
            print("Native folder submit state: " + app.debugDescription)
        }
        let dismissed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: alert)
        wait(for: [dismissed], timeout: 10)
        let request = try XCTUnwrap(try fixtureRequests().last { $0["path"] as? String == "/m1/api/file/list" })
        XCTAssertTrue((request["query"] as? String ?? "").contains("path=%2FVolumes%2FFixture+Folder"))
    }

    func testNativeSettingsStackedDeviceEditorReturnsToParentAndDefaults() throws {
        openSettings(page: "add-device")
        app.buttons["编辑Fixture Mac"].tap()
        let editor = nativeElement("native-device-editor")
        XCTAssertTrue(editor.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["100.64.0.10"].firstMatch.waitForExistence(timeout: 10))
        let status = editor.staticTexts["device-status-tag"]
        XCTAssertTrue(status.waitForExistence(timeout: 5))
        XCTAssertEqual(status.label, "设备状态：在线")
        XCTAssertFalse(editor.staticTexts["状态"].exists)
        XCTAssertFalse(editor.textFields["状态"].exists)
        XCTAssertLessThan(status.frame.maxY, editor.textFields["设备名称"].frame.minY)
        attachScreenshot("native-stacked-device-editor")
        let name = editor.textFields["设备名称"]
        replaceText(name, with: "")
        XCTAssertEqual(name.placeholderValue, "Mac 1")
        XCTAssertEqual(name.value as? String, "Mac 1")
        XCTAssertFalse(editor.staticTexts["设备名称留空时恢复默认名称。"].exists)
        attachScreenshot("native-device-empty-name-default")
        replaceText(name, with: "Fixture Mac\n")
        editor.swipeUp()
        let http = app.textFields["HTTP 代理"]
        XCTAssertTrue(http.waitForExistence(timeout: 5))
        XCTAssertEqual(http.value as? String, "http://127.0.0.1:7897")
        XCTAssertEqual(app.textFields["HTTPS 代理"].value as? String, "http://127.0.0.1:7897")
        app.navigationBars.buttons["取消"].tap()
        XCTAssertTrue(app.buttons["编辑Fixture Mac"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["关闭设置"].exists)
        app.buttons["关闭设置"].tap()
        app.buttons["toggle-devices"].tap()
        app.buttons["Fixture Mac 设备设置"].tap()
        XCTAssertTrue(editor.waitForExistence(timeout: 10))
        app.navigationBars.buttons["取消"].tap()
        XCTAssertTrue(app.buttons["device-m1"].waitForExistence(timeout: 5))
        app.navigationBars.buttons["完成"].tap()
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 5))
    }

    func testNativeDeviceDragOrderPersists() throws {
        openSettings(page: "add-device")
        let first = app.buttons["编辑Fixture Mac"]
        let second = app.buttons["编辑Offline Mac"]
        XCTAssertTrue(first.waitForExistence(timeout: 10))
        let handle = app.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0)).withOffset(CGVector(dx: app.frame.width - 37, dy: first.frame.midY))
        let target = app.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0)).withOffset(CGVector(dx: app.frame.width - 37, dy: second.frame.maxY + 10))
        handle.press(forDuration: 0.8, thenDragTo: target)
        let saved = expectation(for: NSPredicate { _, _ in first.frame.midY > second.frame.midY }, evaluatedWith: first)
        wait(for: [saved], timeout: 10)
        let writes = try fixtureRequests()
        let update = try XCTUnwrap(writes.last { $0["method"] as? String == "PATCH" && ($0["body"] as? [String: Any])?["order"] != nil }?["body"] as? [String: Any])
        XCTAssertEqual(update["order"] as? [String], ["m2", "m1"])
        app.buttons["关闭设置"].tap()
        app.buttons["toggle-devices"].tap()
        XCTAssertLessThan(app.buttons["device-m2"].frame.midY, app.buttons["device-m1"].frame.midY)
        attachScreenshot("native-device-order-persisted")
    }

    func testSettingsShareNativeGroupsAndDeviceIconInputLimits() {
        openSettings(page: "appearance")
        for theme in ["浅色", "深色"] {
            let mode = app.buttons[theme]
            XCTAssertTrue(mode.waitForExistence(timeout: 10))
            mode.tap()
            for (page, header) in [("appearance", "显示模式"), ("automation", "访问密钥"), ("account", "登录设备"), ("sessions", "连接与缓存"), ("add-device", "设备列表")] {
                let tab = app.buttons["native-settings-" + page]
                tab.tap()
                XCTAssertTrue(tab.isSelected)
                XCTAssertTrue(app.staticTexts[header].firstMatch.waitForExistence(timeout: 10))
                XCTAssertEqual(nativeElement("native-settings-tabs").frame.midY, app.buttons["关闭设置"].frame.midY, accuracy: 3)
                attachScreenshot("unified-settings-" + page + "-" + theme)
            }
            app.buttons["native-settings-appearance"].tap()
        }
        app.buttons["native-settings-add-device"].tap()
        app.buttons["编辑Fixture Mac"].tap()
        let editor = nativeElement("native-device-editor")
        XCTAssertTrue(editor.waitForExistence(timeout: 10))
        app.buttons["文字"].tap()
        let letters = app.textFields["device-icon-text"]
        XCTAssertTrue(letters.waitForExistence(timeout: 5))
        XCTAssertEqual(letters.placeholderValue, "1-4个英文字母或数字")
        XCTAssertFalse(app.staticTexts["1–4 个英文字母或数字，保留大小写。"].exists)
        replaceText(letters, with: "aB12Z")
        XCTAssertEqual(letters.value as? String, "aB12")
        letters.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 4))
        XCTAssertEqual(letters.value as? String, "1-4个英文字母或数字")
        letters.typeText("a-9!")
        XCTAssertEqual(letters.value as? String, "a9")
        letters.typeText("\n")
        attachScreenshot("unified-device-icon-input")
        app.navigationBars.buttons["取消"].tap()
        app.buttons["关闭设置"].tap()
        app.buttons["toggle-devices"].tap()
        XCTAssertTrue(nativeElement("native-device-picker-list").waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts["可汇总全部在线设备的未归档会话"].exists)
    }

    func testNativeAccountSummaryAndPaletteInputs() {
        openSettings(page: "account")
        XCTAssertTrue(app.staticTexts["iPhone · Fleet AI"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["当前设备"].exists)
        XCTAssertTrue(app.staticTexts["Mac · Chrome"].exists)
        XCTAssertFalse(app.staticTexts["其他设备"].exists)
        XCTAssertFalse(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Mozilla/5.0'")).firstMatch.exists)
        XCTAssertLessThan(app.staticTexts["登录设备"].firstMatch.frame.maxY, app.staticTexts["iPhone · Fleet AI"].firstMatch.frame.minY)
        reveal(app.buttons["更改密码"])
        XCTAssertLessThanOrEqual(app.staticTexts["账号安全"].firstMatch.frame.maxY, app.buttons["更改密码"].frame.minY)
        attachScreenshot("native-account-headers-outside-cards")
        app.buttons["更改密码"].tap()
        XCTAssertTrue(app.secureTextFields["当前密码"].waitForExistence(timeout: 5))
        app.navigationBars.buttons["取消"].tap()
        XCTAssertTrue(app.buttons["更改密码"].waitForExistence(timeout: 5))
        app.buttons["native-settings-appearance"].tap()
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "palette-页面背景").firstMatch.waitForExistence(timeout: 10))
        XCTAssertTrue(app.textFields["color-页面背景"].firstMatch.exists)
        app.descendants(matching: .any).matching(identifier: "palette-页面背景").firstMatch.tap()
        attachScreenshot("native-color-palette")
    }

    func testPaletteAutosavesAndThemeRoundTripKeepsNativeForms() throws {
        openSettings(page: "appearance")
        XCTAssertTrue(app.textFields["color-页面背景"].firstMatch.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["保存配色"].exists)
        replaceText(app.textFields["color-页面背景"].firstMatch, with: "#F1F2F3")
        let saved = expectation(for: NSPredicate { _, _ in
            (try? self.fixtureRequests().contains { request in
                guard let body = request["body"] as? [String: Any], let palette = body["palette"] as? [String: [String: String]] else { return false }
                return palette["light"]?["canvas"] == "#F1F2F3"
            }) == true
        }, evaluatedWith: app)
        wait(for: [saved], timeout: 10)
        if app.keyboards.firstMatch.exists { app.textFields["color-页面背景"].firstMatch.typeText("\n") }
        for mode in ["深色", "浅色", "深色", "浅色"] {
            reveal(app.buttons[mode]); app.buttons[mode].tap()
            app.buttons["native-settings-sessions"].tap()
            XCTAssertTrue(app.buttons["setting-chatCacheMaxSessions"].waitForExistence(timeout: 5))
            attachScreenshot("native-theme-roundtrip-" + mode)
            app.buttons["native-settings-appearance"].tap()
        }
        app.buttons["native-settings-automation"].tap()
        XCTAssertTrue(app.buttons["access-key-fixture-key"].waitForExistence(timeout: 10))
        app.buttons["access-key-fixture-key"].tap()
        app.navigationBars["密钥详情"].buttons["编辑"].tap()
        XCTAssertTrue(app.textFields["RPM"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.textFields["项目绝对路径（留空为全部）"].exists)
        XCTAssertFalse(app.textFields["会话 ID（留空为全部）"].exists)
        let project = app.buttons["key-project-picker"]
        XCTAssertTrue(project.waitForExistence(timeout: 10))
        project.tap()
        XCTAssertTrue(app.buttons["Fixture project"].waitForExistence(timeout: 5))
        app.buttons["Fixture project"].tap()
        let session = app.buttons["key-session-picker"]
        session.tap()
        XCTAssertTrue(app.buttons["codex fixture"].waitForExistence(timeout: 5))
        app.buttons["codex fixture"].tap()
        attachScreenshot("native-key-named-selectors-light")
        app.buttons["保存"].tap()
        let bound = expectation(for: NSPredicate { _, _ in
            (try? self.fixtureRequests().contains { request in
                guard request["method"] as? String == "PATCH", let body = request["body"] as? [String: Any], let binding = body["binding"] as? [String: String] else { return false }
                return binding["project_path"] == "/Users/fixture/project" && binding["session_id"] == "codex-session"
            }) == true
        }, evaluatedWith: app)
        wait(for: [bound], timeout: 10)
    }

    func testNativePermissionAndModelDrawersPreserveDraft() throws {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["session-codex-session"].tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
        let composer = app.webViews.textViews.firstMatch
        composer.tap(); composer.typeText(" Popup draft")
        let draftBeforePopup = composer.value as? String
        webElement("如何批准 ChatGPT 操作？").tap()
        let permissions = app.alerts["会话权限"]
        XCTAssertTrue(permissions.waitForExistence(timeout: 5))
        permissions.buttons.matching(NSPredicate(format: "label BEGINSWITH '请求批准'")).firstMatch.tap()
        let permissionsClosed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: app.alerts["会话权限"])
        wait(for: [permissionsClosed], timeout: 10)
        let options = webElement("模型选项")
        XCTAssertTrue(options.waitForExistence(timeout: 5))
        options.tap()
        let models = app.alerts["模型与推理"]
        XCTAssertTrue(models.waitForExistence(timeout: 5))
        models.buttons.matching(NSPredicate(format: "label BEGINSWITH '模型 '")).firstMatch.tap()
        XCTAssertTrue(models.buttons["Fixture alternate model"].waitForExistence(timeout: 5))
        models.buttons["Fixture alternate model"].tap()
        let modelsClosed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: app.alerts["模型与推理"])
        wait(for: [modelsClosed], timeout: 10)
        XCTAssertEqual(composer.value as? String, draftBeforePopup)
        options.tap()
        XCTAssertTrue(models.waitForExistence(timeout: 5))
        models.buttons.matching(NSPredicate(format: "label BEGINSWITH '模型 '")).firstMatch.tap()
        XCTAssertTrue(models.buttons["✓ Fixture alternate model"].waitForExistence(timeout: 5))
        XCTAssertTrue(models.buttons["✓ Fixture alternate model"].exists)
        models.buttons["取消"].tap()
        let requests = try fixtureRequests()
        let update = try XCTUnwrap(requests.last { $0["path"] as? String == "/m1/api/chat/settings" }?["body"] as? [String: Any])
        XCTAssertEqual(update["approvalMode"] as? String, "untrusted")
        XCTAssertEqual(update["assistant"] as? String, "codex")
        XCTAssertEqual(update["sessionId"] as? String, "codex-session")
        XCTAssertFalse(requests.contains { $0["path"] as? String == "/m1/api/chat/send" })
        attachScreenshot("native-permission-model-drawers")
    }

    func testFixtureModelSettingsPersistAcrossResumeAndQueue() async throws {
        let base = try XCTUnwrap(URL(string: "http://localhost:18765"))
        var update = URLRequest(url: base.appendingPathComponent("m1/api/chat/settings"))
        update.httpMethod = "POST"
        update.setValue("application/json", forHTTPHeaderField: "Content-Type")
        update.httpBody = try JSONSerialization.data(withJSONObject: ["assistant": "codex", "sessionId": "codex-session", "model": "fixture-model-alternate"])
        let (savedData, savedResponse) = try await URLSession.shared.data(for: update)
        XCTAssertEqual((savedResponse as? HTTPURLResponse)?.statusCode, 200)
        let saved = try XCTUnwrap(JSONSerialization.jsonObject(with: savedData) as? [String: Any])
        XCTAssertEqual(saved["model"] as? String, "fixture-model-alternate")
        XCTAssertEqual(saved["approvalMode"] as? String, "on-request")
        update.httpBody = try JSONSerialization.data(withJSONObject: ["assistant": "codex", "sessionId": "codex-session", "approvalMode": "untrusted"])
        let (_, approvalResponse) = try await URLSession.shared.data(for: update)
        XCTAssertEqual((approvalResponse as? HTTPURLResponse)?.statusCode, 200)
        for endpoint in ["chat/resume", "chat/queue"] {
            let url = try XCTUnwrap(URL(string: "/m1/api/\(endpoint)?assistant=codex&sessionId=codex-session", relativeTo: base))
            var request = URLRequest(url: url)
            if endpoint == "chat/resume" {
                request.httpMethod = "POST"
                request.setValue("application/json", forHTTPHeaderField: "Content-Type")
                request.httpBody = try JSONSerialization.data(withJSONObject: ["assistant": "codex", "sessionId": "codex-session"])
            }
            let (data, response) = try await URLSession.shared.data(for: request)
            XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 200)
            let state = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
            XCTAssertEqual(state["model"] as? String, "fixture-model-alternate", endpoint)
            XCTAssertEqual(state["approvalMode"] as? String, "untrusted", endpoint)
        }
        let (evidenceData, _) = try await URLSession.shared.data(from: base.appendingPathComponent("__requests"))
        let evidence = try XCTUnwrap(JSONSerialization.jsonObject(with: evidenceData) as? [[String: Any]])
        let modelUpdate = try XCTUnwrap(evidence.last {
            $0["path"] as? String == "/m1/api/chat/settings" && ($0["body"] as? [String: Any])?["model"] as? String == "fixture-model-alternate"
        }?["body"] as? [String: Any])
        XCTAssertEqual(modelUpdate["model"] as? String, "fixture-model-alternate")
    }

    func testNativeDynamicFileMenuOpensRenameAndDismisses() {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["mode-files"].tap()
        let row = app.webViews.firstMatch.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH 'README.md '")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 15))
        let trigger = row.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5))
        trigger.tap()
        XCTAssertTrue(app.buttons["重命名"].waitForExistence(timeout: 5))
        attachScreenshot("native-file-row-menu")
        app.buttons["重命名"].tap()
        let nameDialog = app.alerts["重命名"]
        XCTAssertTrue(nameDialog.waitForExistence(timeout: 5))
        XCTAssertEqual(nameDialog.textFields["名称"].value as? String, "README.md")
        nameDialog.buttons["取消"].tap()
        XCTAssertTrue(row.waitForExistence(timeout: 5))
        trigger.tap(); app.buttons["删除"].tap()
        XCTAssertTrue(app.alerts["删除文件"].waitForExistence(timeout: 5))
        app.alerts["删除文件"].buttons["取消"].tap()
        XCTAssertTrue(row.waitForExistence(timeout: 5))
    }

    func testNativeSubAgentDrawerShowsReadOnlyHistory() throws {
        let enabled = expectation(description: "Enable synthetic subagent")
        URLSession.shared.dataTask(with: URL(string: "http://localhost:18765/__subagents")!) { _, response, error in
            XCTAssertNil(error)
            XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 200)
            enabled.fulfill()
        }.resume()
        wait(for: [enabled], timeout: 5)
        app.terminate(); app.launch()
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["session-codex-session"].tap()
        let toggle = webElement("展开 Sub Agent 列表")
        XCTAssertTrue(toggle.waitForExistence(timeout: 15))
        toggle.tap()
        XCTAssertTrue(nativeElement("native-web-dialog-chat-subagent-drawer").waitForExistence(timeout: 5))
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Fixture reviewer'")).firstMatch.tap()
        XCTAssertTrue(app.staticTexts["Synthetic subagent review evidence"].waitForExistence(timeout: 10))
        app.buttons["关闭Sub Agent"].tap()
        XCTAssertTrue(toggle.waitForExistence(timeout: 5))
        XCTAssertTrue(try fixtureRequests().contains {
            $0["path"] as? String == "/m1/api/chat/history" && ($0["query"] as? String ?? "").contains("sessionId=fixture-subagent")
        })
    }

    func testExpandedNativeSettingsAndDeviceDrawerGeometry() throws {
        XCUIDevice.shared.orientation = .landscapeLeft
        guard isExpandedWindow else { throw XCTSkip("仅验证展开宽度的原生抽屉") }
        XCTAssertTrue(app.buttons["workspace-menu"].firstMatch.waitForExistence(timeout: 20))
        app.buttons["workspace-menu"].firstMatch.tap()
        let panel = nativeElement("native-settings-panel")
        XCTAssertTrue(panel.waitForExistence(timeout: 10))
        let drawer = nativeElement("device-drawer")
        let window = app.windows.firstMatch.frame
        XCTAssertEqual(drawer.frame.minX, window.minX, accuracy: 1)
        XCTAssertEqual(drawer.frame.width, window.width, accuracy: 1)
        XCTAssertEqual(drawer.frame.maxY, window.maxY, accuracy: 1)
        let devices = app.buttons["native-settings-add-device"]
        let appearance = app.buttons["native-settings-appearance"]
        XCTAssertEqual(devices.frame.minX, appearance.frame.minX, accuracy: 1)
        XCTAssertGreaterThan(appearance.frame.minY, devices.frame.minY)
        devices.tap()
        XCTAssertTrue(panel.buttons["编辑"].firstMatch.waitForExistence(timeout: 10))
        attachScreenshot("expanded-native-settings-drawer")
        panel.buttons["编辑"].firstMatch.tap()
        XCTAssertTrue(nativeElement("native-device-editor").waitForExistence(timeout: 10))
        XCTAssertEqual(drawer.frame.width, window.width, accuracy: 1)
        XCTAssertEqual(drawer.frame.maxY, window.maxY, accuracy: 1)
        attachScreenshot("expanded-native-device-editor")
        nativeElement("native-device-editor").buttons.matching(identifier: "xmark").firstMatch.tap()
        XCUIDevice.shared.orientation = .portrait
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 10))
    }

    private func webElement(_ label: String) -> XCUIElement {
        app.webViews.firstMatch.descendants(matching: .any).matching(NSPredicate(format: "label == %@", label)).firstMatch
    }

    private func nativeElement(_ identifier: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: identifier).firstMatch
    }

    private func openSettings(page: String) {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        let settings = app.buttons["workspace-menu"].firstMatch
        let ready = expectation(for: NSPredicate(format: "enabled == true"), evaluatedWith: settings)
        wait(for: [ready], timeout: 10)
        settings.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        XCTAssertTrue(nativeElement("native-settings-panel").waitForExistence(timeout: 10))
        let tab = app.buttons["native-settings-" + page]
        XCTAssertTrue(tab.waitForExistence(timeout: 5))
        XCTAssertTrue(tab.isHittable)
        tab.tap()
    }

    private func reveal(_ element: XCUIElement) {
        if element.exists && element.isHittable { return }
        let content = nativeScrollContent()
        for _ in 0..<6 {
            if element.exists && element.isHittable { return }
            if element.exists && element.frame.midY < content.frame.minY {
                scrollNativeContent(content, upwards: false)
            } else {
                scrollNativeContent(content, upwards: true)
            }
        }
        if !element.exists || !element.isHittable {
            attachScreenshot("native-field-scroll-failure")
            let hierarchy = XCTAttachment(string: app.debugDescription)
            hierarchy.name = "native-field-scroll-failure-hierarchy"
            hierarchy.lifetime = .keepAlways
            add(hierarchy)
        }
        XCTAssertTrue(element.waitForExistence(timeout: 5))
        XCTAssertTrue(element.isHittable)
    }

    private func scrollNativeContent(_ content: XCUIElement, upwards: Bool) {
        let keyboard = app.keyboards.firstMatch
        let visibleBottom = min(content.frame.maxY, app.tabBars.firstMatch.exists ? app.tabBars.firstMatch.frame.minY - 8 : app.windows.firstMatch.frame.maxY - 48)
        let bottom = keyboard.exists ? min(visibleBottom, keyboard.frame.minY - 8) : visibleBottom
        let top = content.frame.minY + 12
        XCTAssertGreaterThan(bottom - top, 40)
        let origin = content.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0))
        let upper = origin.withOffset(CGVector(dx: 0, dy: top + 12 - content.frame.minY))
        let lower = origin.withOffset(CGVector(dx: 0, dy: bottom - 12 - content.frame.minY))
        (upwards ? lower : upper).press(forDuration: 0.05, thenDragTo: upwards ? upper : lower)
    }

    private func replaceText(_ field: XCUIElement, with value: String) {
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        reveal(field)
        field.tap()
        let previous = field.value as? String ?? ""
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: previous.count) + value)
    }

    private func nativeScrollContent() -> XCUIElement {
        let editors = app.descendants(matching: .any).matching(identifier: "native-editor-content")
        for index in 0..<editors.count {
            let content = editors.element(boundBy: index)
            if content.isHittable { return content }
        }
        let keyDetail = nativeElement("native-key-detail")
        if keyDetail.exists { return keyDetail }
        let dialog = app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH 'native-web-dialog-'")).firstMatch
        let content = dialog.exists
            ? dialog.descendants(matching: .any).matching(identifier: "native-dialog-content").firstMatch
            : nativeElement("native-settings-panel").descendants(matching: .any).matching(identifier: "native-settings-content").firstMatch
        XCTAssertTrue(content.waitForExistence(timeout: 5))
        return content
    }

    private func fixtureRequests() throws -> [[String: Any]] {
        let fetched = expectation(description: "Read isolated fixture request evidence")
        var result: [[String: Any]]?
        URLSession.shared.dataTask(with: URL(string: "http://localhost:18765/__requests")!) { data, response, error in
            XCTAssertNil(error)
            XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 200)
            if let data { result = try? JSONSerialization.jsonObject(with: data) as? [[String: Any]] }
            fetched.fulfill()
        }.resume()
        wait(for: [fetched], timeout: 5)
        return try XCTUnwrap(result)
    }

    private var isExpandedWindow: Bool { app.windows.firstMatch.frame.width > 860 }

    private func attachScreenshot(_ name: String) {
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
