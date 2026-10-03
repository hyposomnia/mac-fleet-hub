import XCTest
@testable import FleetCore

final class InstallationTests: XCTestCase {
    func testInstallationVerifiesStagingAndNeverLeavesPartialDestination() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let source = root.appendingPathComponent("source.app")
        let destination = root.appendingPathComponent("Fleet Hub.app")
        try FileManager.default.createDirectory(at: source, withIntermediateDirectories: false)
        try Data("original".utf8).write(to: source.appendingPathComponent("payload"))
        do {
            try await ApplicationInstaller.install(source: source, destination: destination) { candidate in
                if candidate != source { throw FleetError.message("signature mismatch") }
            }
            XCTFail("installed unverified staged application")
        } catch {}
        XCTAssertFalse(FileManager.default.fileExists(atPath: destination.path))
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: root.path), ["source.app"])
        try await ApplicationInstaller.install(source: source, destination: destination) { candidate in
            XCTAssertEqual(try String(contentsOf: candidate.appendingPathComponent("payload")), "original")
        }
        XCTAssertEqual(try String(contentsOf: destination.appendingPathComponent("payload")), "original")
        try Data("new".utf8).write(to: source.appendingPathComponent("payload"))
        do {
            try await ApplicationInstaller.install(source: source, destination: destination) { _ in }
            XCTFail("overwrote an existing installation")
        } catch {}
        XCTAssertEqual(try String(contentsOf: destination.appendingPathComponent("payload")), "original")
    }

    func testConcurrentInstallationIsNeverOverwritten() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let source = root.appendingPathComponent("source.app")
        let destination = root.appendingPathComponent("Fleet Hub.app")
        try FileManager.default.createDirectory(at: source, withIntermediateDirectories: false)
        do {
            try await ApplicationInstaller.install(source: source, destination: destination) { candidate in
                if candidate != source {
                    try FileManager.default.createDirectory(at: destination, withIntermediateDirectories: false)
                    try Data("other installation".utf8).write(to: destination.appendingPathComponent("payload"))
                }
            }
            XCTFail("overwrote concurrent installation")
        } catch {}
        XCTAssertEqual(try String(contentsOf: destination.appendingPathComponent("payload")), "other installation")
    }
}
