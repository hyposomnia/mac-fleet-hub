import Foundation

/// Only used after the user explicitly chooses to wait. Checking readiness has
/// no destructive effects; the action still performs its own final idle check.
@MainActor
public final class MaintenanceWait {
    private var cancelled = false
    public init() {}
    public func cancel() { cancelled = true }

    public func run(check: () async throws -> Void,
                    pause: () async throws -> Void = { try await Task.sleep(nanoseconds: 2_000_000_000) },
                    action: () async throws -> Void) async throws {
        while true {
            try Task.checkCancellation()
            if cancelled { throw CancellationError() }
            do { try await check(); break }
            catch is CancellationError { throw CancellationError() }
            catch { try await pause() }
        }
        try Task.checkCancellation()
        if cancelled { throw CancellationError() }
        try await action()
    }
}
