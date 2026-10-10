import XCTest
import FleetCore
@testable import FleetHub

final class DesignTests: XCTestCase {
    func testMainStatusReportsTheConnectionFailureInsteadOfRunning() throws {
        let runtime = try JSONDecoder().decode(RuntimeState.self, from: Data(#"{"phase":"failed","error":"无法连接服务器"}"#.utf8))
        let status = FleetOverviewStatus(running: true, runtime: runtime, locked: false, operationError: "", fallback: "后台未运行")
        XCTAssertEqual(status.title, "无法连接服务器")
        XCTAssertEqual(status.tone, .warning)
    }

    func testMainStatusDistinguishesConnectingFromHealthyAndLocked() throws {
        func runtime(_ phase: String) throws -> RuntimeState {
            try JSONDecoder().decode(RuntimeState.self, from: Data("{\"phase\":\"\(phase)\"}".utf8))
        }
        XCTAssertEqual(FleetOverviewStatus(running: true, runtime: try runtime("running"), locked: false, operationError: "", fallback: "").title, "后台运行中")
        XCTAssertEqual(FleetOverviewStatus(running: true, runtime: try runtime("connecting"), locked: false, operationError: "", fallback: "").title, "正在连接设备")
        XCTAssertEqual(FleetOverviewStatus(running: true, runtime: try runtime("running"), locked: true, operationError: "", fallback: "").title, "设备授权已锁定")
    }

    func testMainStatusPreservesTheFailureFromAnOperation() {
        let status = FleetOverviewStatus(running: false, runtime: nil, locked: false, operationError: "后台重启失败", fallback: "后台未运行")
        XCTAssertEqual(status.title, "后台重启失败")
        XCTAssertEqual(status.tone, .warning)
    }

    func testDiskAuthorizationCanContinueAfterInstallingTheApplication() {
        XCTAssertFalse(FleetSetupAction.authorizesDiskAfterInstallation(arguments: ["Fleet Hub"]))
        XCTAssertTrue(FleetSetupAction.authorizesDiskAfterInstallation(arguments: ["Fleet Hub", "--fleet-install-and-start", "--fleet-authorize-disk"]))
    }

    func testTemporaryLaunchCanOpenInstalledAppAndCarryOnlyAValidatedOrigin() throws {
        XCTAssertEqual(FleetSetupAction.installationTitle(installed: true), "打开已安装应用")
        XCTAssertEqual(FleetSetupAction.installationTitle(installed: false), "安装并启动")
        XCTAssertEqual(FleetSetupAction.initialOrigin(arguments: ["Fleet Hub", "--fleet-origin=https://fleet.example.test"]), "https://fleet.example.test")
        XCTAssertNil(FleetSetupAction.initialOrigin(arguments: ["Fleet Hub", "--fleet-origin=https://fleet.example.test/path"]))
    }
    func testNavigationMetricsAndAccountSettingsPages() {
        XCTAssertEqual(FleetTheme.controlHeight, 44)
        XCTAssertEqual(FleetTheme.controlRadius, 8)
        XCTAssertEqual(SettingsPage.connection.rawValue, "关联账号")
        XCTAssertEqual(SettingsPage.preferences.rawValue, "设置")
    }

    func testFirstRunOffersInstallationRatherThanADisabledStartButton() {
        XCTAssertEqual(FleetSetupAction(requiresInstallation: true, backgroundInstalled: false), .installApplication)
        XCTAssertEqual(FleetSetupAction(requiresInstallation: true, backgroundInstalled: true), .installApplication)
        XCTAssertEqual(FleetSetupAction(requiresInstallation: false, backgroundInstalled: false), .installBackground)
        XCTAssertEqual(FleetSetupAction(requiresInstallation: false, backgroundInstalled: true), .startBackground)
        XCTAssertEqual(FleetSetupAction.installApplication.title, "安装并启动")
        XCTAssertEqual(FleetSetupAction.installBackground.title, "安装并启动")
        XCTAssertEqual(FleetSetupAction.startBackground.title, "启动")
    }

    func testOnlyAnExplicitInstallationLaunchStartsThePreparedBackground() {
        XCTAssertFalse(FleetSetupAction.startsAfterInstallation(arguments: ["Fleet Hub"]))
        XCTAssertTrue(FleetSetupAction.startsAfterInstallation(arguments: ["Fleet Hub", "--fleet-install-and-start"]))
    }
}
