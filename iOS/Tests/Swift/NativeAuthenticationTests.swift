import XCTest
@testable import FleetHub

@MainActor
final class NativeAuthenticationTests: XCTestCase {
    func testAuthenticationTransportOnlyAllowsKnownMethodsAndRoutes() {
        XCTAssertTrue(NativeAuthentication.allowed("/api/auth/state", method: "GET"))
        XCTAssertTrue(NativeAuthentication.allowed("/api/auth/login", method: "POST"))
        for path in ["https://other.test/api/auth/login", "//other.test/api/auth/login", "/api/auth/login?next=/", "/api/auth/logout", "/api/auth/../login"] {
            XCTAssertFalse(NativeAuthentication.allowed(path, method: "POST"))
        }
        XCTAssertFalse(NativeAuthentication.allowed("/api/auth/login", method: "GET"))
        XCTAssertFalse(NativeAuthentication.allowed("/api/auth/state", method: "POST"))
        XCTAssertTrue(NativeAuthentication.isAuthRoute(URL(string: "https://fleet.test/auth/setup")!))
        XCTAssertFalse(NativeAuthentication.isAuthRoute(URL(string: "https://fleet.test/authentication")!))
    }

    func testLoginChallengePreservesStageOnBadCodeAndClearsSecretsOnCompletion() async {
        var writes = 0
        var completed = false
        let model = NativeAuthentication(transport: { path, method, body in
            if method == "GET" { return ["state": "anonymous"] }
            writes += 1
            if path == "/api/auth/login" {
                XCTAssertEqual(body?["email"], "fixture@example.test")
                return ["state": "challenge"]
            }
            guard body?["code"] == "123456" else { throw NativeAuthFailure(message: "验证码错误或已使用") }
            return ["user": ["id": 1], "recovery_codes": []]
        }, completed: { completed = true })
        await model.resume()
        model.email = " fixture@example.test "; model.password = "synthetic-password"
        await model.submit()
        XCTAssertEqual(model.stage, .challenge)
        XCTAssertTrue(model.password.isEmpty)
        model.code = "１２３４５６"
        await model.submit()
        XCTAssertEqual(writes, 1)
        model.code = "000000"
        await model.submit()
        XCTAssertEqual(writes, 2) // Failed writes are never automatically replayed.
        XCTAssertEqual(model.stage, .challenge)
        XCTAssertEqual(model.error, "验证码错误或已使用")
        XCTAssertFalse(completed)
        model.code = "123456"
        await model.submit()
        XCTAssertTrue(completed)
        XCTAssertTrue(model.code.isEmpty)
        XCTAssertTrue(model.email.isEmpty)
    }

    func testRegistrationRequiresConfirmationAndRecoveryCodeAcknowledgement() async {
        var writes = 0
        var completed = false
        let model = NativeAuthentication(transport: { path, method, body in
            if method == "GET" { return ["state": "anonymous"] }
            writes += 1
            if path == "/api/auth/register" {
                XCTAssertEqual(body?["password"], body?["confirm_password"])
                return ["state": "setup", "totp": ["secret": "SYNTHETIC", "uri": "otpauth://totp/Fixture?secret=SYNTHETIC"]]
            }
            return ["user": ["id": 1], "recovery_codes": ["fixture-one", "fixture-two"]]
        }, completed: { completed = true })
        await model.resume()
        model.select(.register)
        model.email = "fixture@example.test"; model.password = "too-short"; model.confirmation = "too-short"
        await model.submit()
        XCTAssertEqual(writes, 0)
        model.password = "synthetic-password"; model.confirmation = "different-password"
        await model.submit()
        XCTAssertEqual(writes, 0)
        model.confirmation = model.password
        await model.submit()
        XCTAssertEqual(model.stage, .setup)
        XCTAssertFalse(model.secret.isEmpty)
        model.code = "123456"
        await model.submit()
        XCTAssertEqual(model.stage, .codes)
        XCTAssertTrue(model.secret.isEmpty)
        XCTAssertFalse(completed)
        await model.resume() // Foreground/reload must not bypass saving recovery codes.
        model.select(.login)
        XCTAssertEqual(model.stage, .codes)
        model.finish()
        XCTAssertTrue(completed)
        XCTAssertTrue(model.codes.isEmpty)
    }

    func testInvalidatedAuthenticationDiscardsLateReply() async {
        var model: NativeAuthentication!
        var completed = false
        model = NativeAuthentication(transport: { _, _, _ in
            model.invalidate()
            return ["state": "full"]
        }, completed: { completed = true })
        await model.resume()
        XCTAssertFalse(completed)
        XCTAssertFalse(model.transportReady)
    }
}
