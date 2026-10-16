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
        panel.buttons["native-settings-appearance"].tap()
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
        address.typeText("invalid-address\n")
        XCTAssertTrue(app.staticTexts["请输入 HTTPS 网关根地址，不包含账号、查询参数或子路径。"].waitForExistence(timeout: 5))
        address.tap()
        assertSoftwareKeyboard(for: address)
        app.buttons["完成输入"].firstMatch.tap()
        waitForKeyboardDismissal()
        XCTAssertEqual(address.value as? String, "invalid-address")
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
        panel.buttons["完成输入"].firstMatch.tap()
        waitForKeyboardDismissal()
        reveal(panel.buttons["取消"])
        panel.buttons["取消"].tap()
        app.buttons["native-settings-account"].tap()
        app.buttons["更改密码"].tap()
        let password = panel.secureTextFields["当前密码"]
        reveal(password)
        password.tap()
        assertSoftwareKeyboard(for: password)
        password.typeText("synthetic-only")
        attachScreenshot("software-keyboard-secure-field")
        panel.buttons["完成输入"].firstMatch.tap()
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
        if returnToChat { app.buttons["session-codex-session"].tap() }
    }

    func testSettingsDrawerUsesNativePagesAndDismissal() {
        let session = app.buttons["session-codex-session"]
        XCTAssertTrue(session.waitForExistence(timeout: 20))
        app.buttons["workspace-menu"].firstMatch.tap()
        let close = app.buttons["关闭设置"]
        XCTAssertTrue(close.waitForExistence(timeout: 10))
        attachScreenshot("settings-drawer-after-native-trigger")
        XCTAssertTrue(close.isHittable)
        let panel = nativeElement("native-settings-panel")
        XCTAssertTrue(panel.exists)
        XCTAssertEqual(panel.frame.width, app.frame.width, accuracy: 1)
        XCTAssertFalse(webElement("关闭设置").exists)
        XCTAssertFalse(app.buttons["网关设置"].exists)
        XCTAssertTrue(app.staticTexts["登录设备"].firstMatch.waitForExistence(timeout: 10))
        for page in ["add-device", "appearance", "automation", "account", "sessions"] {
            let tab = app.buttons["native-settings-" + page]
            XCTAssertTrue(tab.exists)
            let tabs = panel.scrollViews.firstMatch
            for _ in 0..<3 {
                if tab.isHittable { break }
                tabs.swipeLeft()
            }
            tab.tap()
            XCTAssertTrue(close.exists)
        }
        attachScreenshot("native-settings-drawer")
        close.tap()
        XCTAssertTrue(session.waitForExistence(timeout: 10))
        app.buttons["workspace-menu"].firstMatch.tap()
        XCTAssertTrue(close.waitForExistence(timeout: 10))
        let drawer = nativeElement("device-drawer")
        XCTAssertTrue(drawer.waitForExistence(timeout: 5))
        app.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0))
            .withOffset(CGVector(dx: drawer.frame.midX - app.frame.minX, dy: drawer.frame.minY - 4 - app.frame.minY)).tap()
        let dismissed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: panel)
        wait(for: [dismissed], timeout: 5)
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
        XCTAssertFalse(app.buttons["DeepSeek"].exists)
        XCTAssertFalse(app.buttons["Claude"].exists)
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
        let editor = nativeElement("native-session-rename")
        let name = editor.textFields["会话名称"]
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        name.tap()
        name.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 30) + "Renamed fixture")
        name.typeText("\n")
        waitForKeyboardDismissal()
        editor.buttons["保存"].tap()
        let renamed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: editor)
        wait(for: [renamed], timeout: 5)
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
        XCTAssertTrue(app.buttons["DeepSeek"].waitForExistence(timeout: 15))
        app.buttons["DeepSeek"].tap()
        XCTAssertTrue(app.buttons["session-dsh-session"].waitForExistence(timeout: 10))
        app.buttons["toggle-devices"].tap(); app.buttons["device-m1"].tap()
        let nativeUI = app.buttons["打开 DeepSeek Harness"]
        XCTAssertTrue(nativeUI.waitForExistence(timeout: 10))
        XCTAssertTrue(nativeUI.isEnabled)
        app.buttons["session-dsh-session"].tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
        XCTAssertFalse(webElement("如何批准 ChatGPT 操作？").isEnabled)
        app.buttons["toggle-sessions"].tap()
        nativeUI.tap()
        let leftApp = expectation(for: NSPredicate { _, _ in self.app.state != .runningForeground }, evaluatedWith: app)
        wait(for: [leftApp], timeout: 10)
        app.activate()
        XCTAssertTrue(app.buttons["session-dsh-session"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.webViews.staticTexts["Fixture DSH UI"].exists)
        XCTAssertTrue(try fixtureRequests().contains { $0["path"] as? String == "/m1/dsh/" })
    }

    func testAutomationCardsMaskSecretsAndDisableLegacyCopy() {
        openSettings(page: "automation")
        XCTAssertTrue(app.staticTexts["Fixture automation"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["synthetic-on••••••0001"].exists)
        XCTAssertFalse(app.staticTexts["synthetic-only-fixture-access-key-0001"].exists)
        XCTAssertTrue(app.buttons.matching(identifier: "复制").firstMatch.isEnabled)
        app.buttons.matching(identifier: "复制").firstMatch.tap()
        XCTAssertTrue(app.staticTexts["密钥已复制（两分钟后从剪贴板清除）"].waitForExistence(timeout: 5))
        reveal(app.staticTexts["Fixture legacy"])
        XCTAssertTrue(app.staticTexts["旧版密钥不可恢复"].exists)
        XCTAssertFalse(app.buttons.matching(identifier: "复制").element(boundBy: 1).isEnabled)
        XCTAssertFalse(app.buttons.matching(identifier: "复制请求示例").element(boundBy: 1).isEnabled)
        attachScreenshot("native-automation-cards")
    }

    func testAutomationKeyCreateEditDeleteAndValidation() throws {
        openSettings(page: "automation")
        XCTAssertTrue(app.buttons["新建密钥"].waitForExistence(timeout: 10))
        app.buttons["新建密钥"].tap()
        let panel = nativeElement("native-settings-panel")
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
            panel.buttons["完成输入"].firstMatch.tap()
            let keyboardDismissed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: app.keyboards.firstMatch)
            wait(for: [keyboardDismissed], timeout: 5)
        }
        reveal(app.buttons["保存"])
        app.buttons["保存"].tap()
        XCTAssertTrue(app.buttons["新建密钥"].waitForExistence(timeout: 10))
        reveal(app.staticTexts["Created fixture key"])
        XCTAssertTrue(app.staticTexts["Created fixture key"].exists)
        let editButtons = app.buttons.matching(identifier: "编辑")
        reveal(editButtons.element(boundBy: editButtons.count - 1))
        editButtons.element(boundBy: editButtons.count - 1).tap()
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        replaceText(name, with: "Edited fixture key")
        if app.keyboards.firstMatch.exists { panel.buttons["完成输入"].firstMatch.tap() }
        reveal(app.buttons["保存"])
        app.buttons["保存"].tap()
        XCTAssertTrue(app.buttons["新建密钥"].waitForExistence(timeout: 10))
        reveal(app.staticTexts["Edited fixture key"])
        XCTAssertTrue(app.staticTexts["Edited fixture key"].exists)
        reveal(editButtons.element(boundBy: editButtons.count - 1))
        editButtons.element(boundBy: editButtons.count - 1).tap()
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
        let created = try XCTUnwrap(writes.first?["body"] as? [String: Any])
        XCTAssertEqual(created["rpm"] as? Int, 25)
        XCTAssertTrue(created["binding"] is NSNull)
    }

    func testNativeSessionSettingsPersistAndDeviceManifestLoads() throws {
        openSettings(page: "sessions")
        let stepper = app.steppers.matching(NSPredicate(format: "label BEGINSWITH '保持连接的会话缓存数'")).firstMatch
        XCTAssertTrue(stepper.waitForExistence(timeout: 10))
        XCTAssertTrue(stepper.label.contains("6"))
        stepper.buttons.element(boundBy: 1).tap()
        reveal(app.buttons["保存会话设置"])
        app.buttons["保存会话设置"].tap()
        XCTAssertTrue(app.staticTexts["会话设置已保存"].waitForExistence(timeout: 10))
        app.buttons["关闭设置"].tap()
        openSettings(page: "sessions")
        XCTAssertTrue(stepper.waitForExistence(timeout: 10))
        XCTAssertTrue(stepper.label.contains("7"))
        app.buttons["关闭设置"].tap()
        openSettings(page: "add-device")
        XCTAssertTrue(app.buttons["在浏览器下载安装包"].waitForExistence(timeout: 10))
        let requests = try fixtureRequests()
        XCTAssertTrue(requests.contains { $0["path"] as? String == "/enroll/client-release.json" })
        let saved = try XCTUnwrap(requests.last { $0["path"] as? String == "/api/settings" && $0["method"] as? String == "POST" }?["body"] as? [String: Any])
        XCTAssertEqual(saved["chatCacheMaxSessions"] as? Int, 7)
        XCTAssertEqual(saved["mobileMaxWindows"] as? Int, 4)
    }

    func testNativeFileSettingsAndCreateFolderDrawer() throws {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["mode-files"].tap()
        let settings = webElement("文件设置")
        XCTAssertTrue(settings.waitForExistence(timeout: 15))
        settings.tap()
        let panel = nativeElement("native-web-dialog-file-settings-menu")
        XCTAssertTrue(panel.waitForExistence(timeout: 5))
        XCTAssertTrue(panel.buttons["图标视图"].exists)
        panel.buttons["图标视图"].tap()
        panel.switches.matching(identifier: "显示隐藏文件").firstMatch.tap()
        app.buttons["关闭文件设置"].tap()
        XCTAssertTrue(settings.waitForExistence(timeout: 5))
        settings.tap()
        XCTAssertTrue(panel.waitForExistence(timeout: 5))
        XCTAssertEqual(panel.switches.matching(identifier: "显示隐藏文件").firstMatch.value as? String, "1")
        app.buttons["关闭文件设置"].tap()
        webElement("新建文件夹").tap()
        let nameDialog = nativeElement("native-web-dialog-file-name-modal")
        XCTAssertTrue(nameDialog.waitForExistence(timeout: 5))
        replaceText(nameDialog.textFields.matching(identifier: "名称").firstMatch, with: "Fixture folder")
        reveal(app.buttons["创建"])
        app.buttons["创建"].tap()
        let dismissed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: nativeElement("native-web-dialog-file-name-modal"))
        wait(for: [dismissed], timeout: 10)
        let request = try XCTUnwrap(try fixtureRequests().last { $0["path"] as? String == "/m1/api/file/mkdir" })
        XCTAssertEqual((request["body"] as? [String: Any])?["name"] as? String, "Fixture folder")
        attachScreenshot("native-file-dialogs")
    }

    func testNativePermissionAndModelDrawersPreserveDraft() throws {
        XCTAssertTrue(app.buttons["session-codex-session"].waitForExistence(timeout: 20))
        app.buttons["session-codex-session"].tap()
        XCTAssertTrue(app.webViews.staticTexts["Fixture reply"].waitForExistence(timeout: 15))
        let composer = app.webViews.textViews.firstMatch
        composer.tap(); composer.typeText("Unsent drawer fixture")
        webElement("如何批准 ChatGPT 操作？").tap()
        let permissions = nativeElement("native-web-dialog-chat-approval-popover")
        XCTAssertTrue(permissions.waitForExistence(timeout: 5))
        permissions.buttons.matching(NSPredicate(format: "label BEGINSWITH '请求批准'")).firstMatch.tap()
        let permissionsClosed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: nativeElement("native-web-dialog-chat-approval-popover"))
        wait(for: [permissionsClosed], timeout: 10)
        let options = webElement("模型选项")
        XCTAssertTrue(options.waitForExistence(timeout: 5))
        options.tap()
        let models = nativeElement("native-web-dialog-chat-options-popover")
        XCTAssertTrue(models.waitForExistence(timeout: 5))
        models.buttons.matching(NSPredicate(format: "label BEGINSWITH '模型 '")).firstMatch.tap()
        XCTAssertTrue(models.buttons["Fixture alternate model"].waitForExistence(timeout: 5))
        models.buttons["Fixture alternate model"].tap()
        let modelsClosed = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: nativeElement("native-web-dialog-chat-options-popover"))
        wait(for: [modelsClosed], timeout: 10)
        XCTAssertEqual(composer.value as? String, "Unsent drawer fixture")
        options.tap()
        XCTAssertTrue(models.waitForExistence(timeout: 5))
        models.buttons.matching(NSPredicate(format: "label BEGINSWITH '模型 '")).firstMatch.tap()
        XCTAssertTrue(models.buttons["Fixture alternate model"].waitForExistence(timeout: 5))
        XCTAssertTrue(models.buttons["Fixture alternate model"].isSelected)
        app.buttons["关闭模型与推理"].tap()
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
        row.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap()
        let panel = app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH 'native-web-dialog-native-file-menu-'")).firstMatch
        XCTAssertTrue(panel.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["预览"].exists)
        XCTAssertTrue(app.buttons["下载"].exists)
        app.buttons["重命名"].tap()
        let nameDialog = nativeElement("native-web-dialog-file-name-modal")
        XCTAssertTrue(nameDialog.waitForExistence(timeout: 5))
        XCTAssertEqual(nameDialog.textFields.matching(identifier: "名称").firstMatch.value as? String, "README.md")
        app.buttons["关闭重命名"].tap()
        XCTAssertTrue(row.waitForExistence(timeout: 5))
        row.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap()
        XCTAssertTrue(panel.waitForExistence(timeout: 5))
        app.buttons["关闭文件操作"].tap()
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
        XCTAssertTrue(app.buttons["workspace-menu"].firstMatch.waitForExistence(timeout: 20))
        app.buttons["workspace-menu"].firstMatch.tap()
        let panel = nativeElement("native-settings-panel")
        XCTAssertTrue(panel.waitForExistence(timeout: 10))
        let tab = app.buttons["native-settings-" + page]
        for _ in 0..<3 {
            if tab.isHittable { break }
            panel.scrollViews.firstMatch.swipeLeft()
        }
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
        let visibleBottom = min(content.frame.maxY, app.windows.firstMatch.frame.maxY - 48)
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
        let dialog = app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH 'native-web-dialog-'")).firstMatch
        let content = dialog.exists
            ? dialog.descendants(matching: .any).matching(identifier: "native-dialog-content").firstMatch
            : nativeElement("native-settings-panel").descendants(matching: .any).matching(identifier: "native-settings-content").firstMatch
        XCTAssertTrue(content.waitForExistence(timeout: 5))
        let laidOut = expectation(for: NSPredicate { _, _ in
            content.frame.width > 0 && content.frame.height > 0 && content.isHittable
        }, evaluatedWith: content)
        wait(for: [laidOut], timeout: 5)
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
