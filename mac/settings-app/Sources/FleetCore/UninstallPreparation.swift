import Foundation

@MainActor
public enum UninstallPreparation {
    public static func prepare(status: () async throws -> AgentStatus, start: () async throws -> Void,
                               checkIdle: () async throws -> Void) async throws -> AgentStatus {
        let current: AgentStatus
        do { current = try await status() }
        catch {
            try await start()
            current = try await status()
        }
        guard current.schema == 1, current.pid > 0 else {
            throw FleetError.message("后台协议无效，未执行卸载。")
        }
        try await checkIdle()
        return current
    }
}
