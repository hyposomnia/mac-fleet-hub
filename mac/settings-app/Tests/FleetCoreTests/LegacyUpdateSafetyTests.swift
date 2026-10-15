import XCTest
@testable import FleetCore

@MainActor
final class LegacyUpdateSafetyTests: XCTestCase {
    func testDesktopTurnAllowsAgentReplacementButFleetTurnWaits() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let locks = root.appendingPathComponent("thread-writer-locks")
        try FileManager.default.createDirectory(at: locks, withIntermediateDirectories: true)
        try Data().write(to: locks.appendingPathComponent("thread-1.lock"))
        for owner in ["desktop", "fleet", "unknown"] {
            do {
                try await LegacyUpdateSafety.check(codexHome: root, listen: "127.0.0.1:7682") { executable, args, _, _ in
                    if executable.lastPathComponent == "lsof" { return Data("123\n".utf8) }
                    XCTAssertTrue(args.contains("sessionId=thread-1"))
                    return Data("{\"turnPhase\":\"running\",\"turnOwner\":\"\(owner)\",\"items\":[]}".utf8)
                }
                XCTAssertEqual(owner, "desktop")
            } catch { XCTAssertNotEqual(owner, "desktop") }
        }
    }

    func testIdleSnapshotWithoutOwnerAndCredentialsOnStandardInput() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        try FileManager.default.createDirectory(at: root.appendingPathComponent("thread-writer-locks"), withIntermediateDirectories: true)
        try Data().write(to: root.appendingPathComponent("thread-writer-locks/thread.lock"))
        let credentials = Data("header = \"X-Fleet-Device-Token: test-private-token\"\n".utf8)
        try await LegacyUpdateSafety.check(codexHome: root, listen: "127.0.0.1:7682", credentialConfig: credentials) { executable, arguments, input, _ in
            if executable.lastPathComponent == "lsof" { return Data("123".utf8) }
            XCTAssertEqual(input, credentials)
            XCTAssertFalse(arguments.joined().contains("test-private-token"))
            return Data("{\"turnPhase\":\"idle\",\"items\":[]}".utf8)
        }
    }

    func testDSHActiveHostWaitsWithoutBlockingAbsentDSHOrClaudeTmux() async throws {
        for running in [false, true] {
            var calls = 0
            do {
                try await LegacyUpdateSafety.checkOtherAssistants(listen: "127.0.0.1:7682") { _, arguments, _, _ in
                    calls += 1
                    if arguments.last?.contains("/api/info") == true {
                        return Data("{\"dsh\":{\"enabled\":true,\"hostRunning\":\(running)}}".utf8)
                    }
                    XCTAssertTrue(arguments.contains("assistant=dsh"))
                    return Data("{\"sessions\":[{\"sessionId\":\"dsh-session\",\"status\":\"active\"}]}".utf8)
                }
                XCTAssertFalse(running)
            } catch { XCTAssertTrue(running); XCTAssertTrue(error.localizedDescription.contains("DeepSeek")) }
            XCTAssertEqual(calls, running ? 2 : 1)
        }
    }

    func testInFlightDeliveryAndUnavailableAgentPreventReplacement() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        try FileManager.default.createDirectory(at: root.appendingPathComponent("thread-writer-locks"), withIntermediateDirectories: true)
        try Data().write(to: root.appendingPathComponent("thread-writer-locks/thread.lock"))
        for response in ["{\"turnPhase\":\"running\",\"turnOwner\":\"desktop\",\"items\":[{\"status\":\"sending\"}]}", "invalid"] {
            do {
                try await LegacyUpdateSafety.check(codexHome: root, listen: "127.0.0.1:7682") { executable, _, _, _ in
                    Data((executable.lastPathComponent == "lsof" ? "123" : response).utf8)
                }
                XCTFail("unsafe migration accepted")
            } catch { XCTAssertFalse(error.localizedDescription.contains("active_or_unknown")) }
        }
    }
}
