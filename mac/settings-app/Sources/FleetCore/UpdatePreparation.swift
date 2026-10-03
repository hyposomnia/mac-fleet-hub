import Foundation

@MainActor
public final class UpdatePreparation {
    public private(set) var isPrepared = false
    public private(set) var isCancelled = false
    private var isPreparing = false
    public init() {}
    public func cancel() { isCancelled = true }

    private func checkCancellation() throws {
        guard !isCancelled else { throw FleetError.message("升级已取消。") }
        try Task.checkCancellation()
    }

    public func prepare(check: () async throws -> Void, backup: () async throws -> Void, stop: () async throws -> Void, resume: () async -> Void) async throws {
        try checkCancellation()
        if isPrepared { return }
        guard !isPreparing else { throw FleetError.message("升级准备正在进行。") }
        isPreparing = true
        defer { isPreparing = false }
        try await check()
        do {
            try checkCancellation()
            try await backup()
            try checkCancellation()
            try await stop()
            try checkCancellation()
            isPrepared = true
        } catch {
            await resume()
            throw error
        }
    }
}
