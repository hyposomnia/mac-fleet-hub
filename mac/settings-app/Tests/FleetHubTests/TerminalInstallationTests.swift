import XCTest
@testable import FleetHub

@MainActor
final class TerminalInstallationTests: XCTestCase {
    func testCLIRequiresOneValidOriginAndDoesNotInterceptNormalLaunch() throws {
        XCTAssertNil(try TerminalInstallation.origin(arguments: ["app", "--fleet-disk-guide"]))
        XCTAssertEqual(try TerminalInstallation.origin(arguments: ["app", "--fleet-install-cli", "--fleet-origin=https://fleet.test:9443/"]), "https://fleet.test:9443")
        for args in [["--fleet-install-cli"], ["--fleet-install-cli", "--fleet-origin=http://fleet.test"],
                     ["--fleet-install-cli", "--fleet-origin=https://fleet.test/path"],
                     ["--fleet-install-cli", "--fleet-origin=https://one.test", "--fleet-origin=https://two.test"]] {
            XCTAssertThrowsError(try TerminalInstallation.origin(arguments: ["app"] + args))
        }
    }
}
