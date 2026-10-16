import XCTest

final class FleetHubDuoUITests: XCTestCase {
    private let app = XCUIApplication()

    override func setUpWithError() throws {
        continueAfterFailure = false
        #if !targetEnvironment(simulator)
        throw XCTSkip("Duo 状态切换仅使用隔离模拟器和 fixture")
        #endif
        let configuration = try fixture("/__duo")
        try XCTSkipUnless(configuration["enabled"] as? Bool == true, "须显式启用 Duo UAT 并使用 Device Hub 切换实际折叠状态")
        _ = try fixture("/__reset")
        XCUIDevice.shared.orientation = .portrait
        app.launchArguments = ["-fleet-test-gateway", "http://localhost:18765", "-fleet-test-reset"]
        app.launch()
    }

    func testLiveFoldTransitionsPreserveChatDrawersAndSidebarPreferences() throws {
        try stage("close-before-start")
        waitFor("Duo 折叠单栏") { self.app.buttons["toggle-devices"].exists && !self.app.buttons["pin-sessions"].exists }
        let session = app.buttons["session-codex-session"]
        XCTAssertTrue(session.waitForExistence(timeout: 20))
        record("duo-closed-list")
        session.tap()
        let reply = app.webViews.staticTexts["Fixture reply"]
        XCTAssertTrue(reply.waitForExistence(timeout: 15))
        let initialRequests = try fixture("/__duo")
        let input = app.webViews.textViews.firstMatch
        XCTAssertTrue(input.waitForExistence(timeout: 10))
        XCTAssertEqual(input.frame.height, 44, accuracy: 2)
        input.tap()
        record("duo-closed-input-focused")
        assertSoftwareKeyboard(input)
        input.typeText("Duo synthetic draft")
        XCTAssertEqual(input.value as? String, "Duo synthetic draft")
        record("duo-closed-keyboard")

        try stage("open-with-keyboard")
        waitFor("Duo 展开多栏") { self.app.buttons["pin-sessions"].exists }
        XCTAssertTrue(reply.waitForExistence(timeout: 10))
        XCTAssertEqual(input.value as? String, "Duo synthetic draft")
        XCTAssertTrue(input.isHittable)
        input.tap()
        assertSoftwareKeyboard(input)
        record("duo-open-keyboard")
        reply.tap()
        app.buttons["toggle-devices"].tap()
        let rail = app.otherElements["device-panel"].firstMatch
        waitFor("设备栏完成收起动画", timeout: 5) { abs(rail.frame.width - 72) < 2 }
        XCTAssertEqual(rail.frame.width, 72, accuracy: 2)
        app.buttons["pin-sessions"].tap()
        XCTAssertTrue(app.buttons["sessions-launcher"].waitForExistence(timeout: 5))
        app.buttons["workspace-menu"].tap()
        let closeSettings = app.buttons["关闭设置"]
        XCTAssertTrue(closeSettings.waitForExistence(timeout: 10))
        assertDrawer()
        record("duo-open-settings")

        try stage("close-with-settings")
        waitFor("Duo 设置抽屉收至折叠宽度") {
            self.app.windows.firstMatch.frame.width <= 860 && abs(self.drawer.frame.width - self.app.windows.firstMatch.frame.width) < 1
        }
        XCTAssertTrue(closeSettings.exists)
        assertDrawer()
        record("duo-closed-settings")
        closeSettings.tap()
        waitFor("折叠屏可关闭设置", timeout: 5) { !self.drawer.exists }
        XCTAssertEqual(input.value as? String, "Duo synthetic draft")
        XCTAssertTrue(app.buttons["toggle-sessions"].waitForExistence(timeout: 5))
        record("duo-closed-chat")

        try stage("reopen-preserving-sidebars")
        waitFor("Duo 保留会话栏收起状态") { self.app.buttons["sessions-launcher"].exists }
        XCTAssertEqual(rail.frame.width, 72, accuracy: 2)
        XCTAssertEqual(input.value as? String, "Duo synthetic draft")
        XCTAssertTrue(reply.exists)
        app.buttons["sessions-launcher"].tap()
        XCTAssertTrue(session.waitForExistence(timeout: 5))
        let backdrop = app.otherElements["sessions-backdrop"].firstMatch
        XCTAssertTrue(backdrop.exists)
        backdrop.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap()
        waitFor("关闭会话浮层", timeout: 5) { !self.app.otherElements["sessions-backdrop"].exists }
        input.tap()
        assertSoftwareKeyboard(input)
        record("duo-reopened-chat")

        XCUIDevice.shared.orientation = .landscapeLeft
        XCTAssertTrue(reply.waitForExistence(timeout: 10))
        XCTAssertEqual(input.value as? String, "Duo synthetic draft")
        input.tap()
        assertSoftwareKeyboard(input)
        record("duo-open-rotated")
        XCUIDevice.shared.orientation = .portrait
        XCTAssertEqual(input.value as? String, "Duo synthetic draft")
        input.tap()
        assertSoftwareKeyboard(input)
        let finalRequests = try fixture("/__duo")
        XCTAssertEqual(finalRequests["rootLoads"] as? Int, initialRequests["rootLoads"] as? Int)
        XCTAssertEqual(finalRequests["chatResumes"] as? Int, initialRequests["chatResumes"] as? Int)
        XCTAssertEqual(finalRequests["messagesSent"] as? Int, 0)
        try stage("complete")
    }

    private var drawer: XCUIElement {
        app.descendants(matching: .any).matching(identifier: "device-drawer").firstMatch
    }

    private func assertDrawer() {
        XCTAssertTrue(drawer.waitForExistence(timeout: 5))
        let window = app.windows.firstMatch.frame
        XCTAssertEqual(drawer.frame.minX, window.minX, accuracy: 1)
        XCTAssertEqual(drawer.frame.width, window.width, accuracy: 1)
        XCTAssertEqual(drawer.frame.maxY, window.maxY, accuracy: 1)
    }

    private func assertSoftwareKeyboard(_ input: XCUIElement) {
        let keyboard = app.keyboards.firstMatch
        waitFor("屏幕软键盘而非硬件工具条", timeout: 10) {
            keyboard.exists && keyboard.keys.count >= 20 &&
                keyboard.frame.intersection(self.app.windows.firstMatch.frame).height > 150
        }
        print("Duo software keyboard: window=\(app.windows.firstMatch.frame) input=\(input.frame) keyboard=\(keyboard.frame) keys=\(keyboard.keys.count)")
        XCTAssertTrue(input.isHittable)
        XCTAssertLessThanOrEqual(input.frame.maxY, keyboard.frame.minY + 1)
    }

    private func waitFor(_ description: String, timeout: TimeInterval = 300, condition: @escaping () -> Bool) {
        let ready = expectation(for: NSPredicate { _, _ in condition() }, evaluatedWith: app)
        ready.expectationDescription = description
        wait(for: [ready], timeout: timeout)
    }

    private func stage(_ value: String) throws {
        _ = try fixture("/__duo", body: ["stage": value])
    }

    private func fixture(_ path: String, body: [String: String]? = nil) throws -> [String: Any] {
        let completed = expectation(description: path)
        var payload: [String: Any]?
        var failure: Error?
        var request = URLRequest(url: URL(string: "http://localhost:18765" + path)!)
        if let body {
            request.httpMethod = "POST"
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        URLSession.shared.dataTask(with: request) { data, _, error in
            failure = error
            if let data { payload = try? JSONSerialization.jsonObject(with: data) as? [String: Any] }
            completed.fulfill()
        }.resume()
        wait(for: [completed], timeout: 10)
        if let failure { throw failure }
        return try XCTUnwrap(payload)
    }

    private func record(_ name: String) {
        print("\(name): app=\(app.frame) windows=\(app.windows.allElementsBoundByIndex.map(\.frame))")
        let screenshot = XCTAttachment(screenshot: app.screenshot())
        screenshot.name = name
        screenshot.lifetime = .keepAlways
        add(screenshot)
        let hierarchy = XCTAttachment(string: app.debugDescription)
        hierarchy.name = name + "-hierarchy"
        hierarchy.lifetime = .keepAlways
        add(hierarchy)
    }
}
