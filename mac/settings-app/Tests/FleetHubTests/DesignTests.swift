import XCTest
@testable import FleetHub

final class DesignTests: XCTestCase {
    func testTitaniumMetricsAndAccountNavigation() {
        XCTAssertEqual(FleetTheme.controlHeight, 44)
        XCTAssertEqual(FleetTheme.controlRadius, 12)
        XCTAssertEqual(FleetTheme.compactHeight, 36)
        XCTAssertEqual(FleetTheme.compactRadius, 8)
        XCTAssertEqual(FleetTheme.cardRadius, 16)
        XCTAssertEqual(SettingsPage.connection.rawValue, "关联账号")
    }
}
