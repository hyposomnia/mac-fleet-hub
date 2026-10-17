import XCTest
@testable import FleetHub

final class StandbyTests: XCTestCase {
    func testTodayUsesPhoneTimezoneAndNeverLabelsYesterdayToday() throws {
        let data = Data("""
        {"version":1,"date":"2026-10-17","timezone":"Asia/Shanghai","generatedAt":1792195200000,
        "totalTokens":120,"models":[{"assistant":"codex","model":"gpt-5","totalTokens":120}],
        "hosts":[{"id":"m1","name":"Mac","usageStatus":"ok"}],"reportingHosts":1,
        "usageComplete":true,"sessions":[],"sessionsPartial":false}
        """.utf8)
        let value = try JSONDecoder().decode(StandbySnapshot.self, from: data)
        let now = try XCTUnwrap(ISO8601DateFormatter().date(from: "2026-10-16T17:00:00Z"))
        XCTAssertTrue(value.isToday(now: now, timezone: TimeZone(identifier: "Asia/Shanghai")!))
        XCTAssertFalse(value.isToday(now: now.addingTimeInterval(86400), timezone: TimeZone(identifier: "Asia/Shanghai")!))
        XCTAssertFalse(value.isToday(now: now, timezone: TimeZone(identifier: "UTC")!))
        XCTAssertEqual(value.models.first?.id, "codex/gpt-5")
    }
    func testTokenFormattingDoesNotOverflowAtBillionScale() {
        XCTAssertEqual(StandbyStorage.compact(999), "999")
        XCTAssertEqual(StandbyStorage.compact(640_000), "640 K")
        XCTAssertEqual(StandbyStorage.compact(1_280_000), "1.28 M")
        XCTAssertEqual(StandbyStorage.compact(2_300_000_000), "2.30 B")
    }
    func testDigitalClockIsAlwaysHorizontal24Hour() throws {
        let date = try XCTUnwrap(ISO8601DateFormatter().date(from: "2026-10-17T13:41:00Z"))
        XCTAssertEqual(StandbyStorage.clockText(date, timezone: TimeZone(identifier: "Asia/Shanghai")!), "21:41")
    }
    @MainActor func testNativeBridgeAllowsOnlyTokenMintRoute() {
        XCTAssertTrue(FleetService.allowed("/api/standby/token", method: "POST"))
        XCTAssertFalse(FleetService.allowed("/api/standby/token", method: "GET"))
        XCTAssertFalse(FleetService.allowed("/api/standby/token/other", method: "POST"))
    }
}
