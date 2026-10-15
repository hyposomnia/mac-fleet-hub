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
    func testConfirmedReplacementRemovesOldFilesAndRollsBackFailedVerification() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        func app(_ name: String, build: String) throws -> URL {
            let url = root.appendingPathComponent(name)
            try FileManager.default.createDirectory(at: url.appendingPathComponent("Contents"), withIntermediateDirectories: true)
            let plist = ["CFBundleIdentifier": "com.macfleet.fleet-hub", "CFBundleShortVersionString": "0.1.10", "CFBundleVersion": build]
            try PropertyListSerialization.data(fromPropertyList: plist, format: .xml, options: 0).write(to: url.appendingPathComponent("Contents/Info.plist"))
            return url
        }
        let source = try app("new.app", build: "12")
        let destination = try app("installed.app", build: "11")
        try Data("old".utf8).write(to: destination.appendingPathComponent("obsolete"))
        do {
            try await ApplicationInstaller.install(source: source, destination: destination, replaceExisting: true) { candidate in
                if candidate == destination { throw FleetError.message("post-install verification failed") }
            }
            XCTFail("accepted failed final verification")
        } catch {}
        XCTAssertEqual(try BackgroundIdentity.read(destination, identifier: "com.macfleet.fleet-hub").build, 11)
        XCTAssertTrue(FileManager.default.fileExists(atPath: destination.appendingPathComponent("obsolete").path))
        try await ApplicationInstaller.install(source: source, destination: destination, replaceExisting: true) { _ in }
        XCTAssertEqual(try BackgroundIdentity.read(destination, identifier: "com.macfleet.fleet-hub").build, 12)
        XCTAssertFalse(FileManager.default.fileExists(atPath: destination.appendingPathComponent("obsolete").path))
        let older = try app("older.app", build: "10")
        do {
            try await ApplicationInstaller.install(source: older, destination: destination, replaceExisting: true) { _ in }
            XCTFail("downgraded installed application")
        } catch {}
        XCTAssertEqual(try BackgroundIdentity.read(destination, identifier: "com.macfleet.fleet-hub").build, 12)
        try await ApplicationInstaller.install(source: source, destination: destination, replaceExisting: true) { _ in }
        XCTAssertEqual(Set(try FileManager.default.contentsOfDirectory(atPath: root.path)), ["new.app", "installed.app", "older.app"])
    }

}
