import XCTest
@testable import FleetHub

final class DesignTests: XCTestCase {
    func testTemporaryLaunchCanOpenInstalledAppAndCarryOnlyAValidatedOrigin() throws {
        XCTAssertEqual(FleetSetupAction.installationTitle(installed: true), "打开已安装应用")
        XCTAssertEqual(FleetSetupAction.installationTitle(installed: false), "安装并启动")
        XCTAssertEqual(FleetSetupAction.initialOrigin(arguments: ["Fleet Hub", "--fleet-origin=https://fleet.example.test"]), "https://fleet.example.test")
        XCTAssertNil(FleetSetupAction.initialOrigin(arguments: ["Fleet Hub", "--fleet-origin=https://fleet.example.test/path"]))
    }
    func testTitaniumMetricsAndAccountNavigation() {
        XCTAssertEqual(FleetTheme.controlHeight, 44)
        XCTAssertEqual(FleetTheme.controlRadius, 12)
        XCTAssertEqual(FleetTheme.compactHeight, 36)
        XCTAssertEqual(FleetTheme.compactRadius, 8)
        XCTAssertEqual(FleetTheme.cardRadius, 16)
        XCTAssertEqual(SettingsPage.connection.rawValue, "关联账号")
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
