import Combine
import Foundation

@MainActor
final class SettingsStartup: ObservableObject {
    enum Stage: Int, CaseIterable {
        case settings, background, status
        var title: String {
            switch self {
            case .settings: return "检查设置"
            case .background: return "准备后台"
            case .status: return "读取运行状态"
            }
        }
    }
    enum State: Equatable {
        case checking(Stage), ready, failed(String)
        var isReady: Bool { self == .ready }
        var error: String? {
            if case .failed(let message) = self { return message }
            return nil
        }
    }
    @Published private(set) var state: State = .checking(.settings)
    private var running = false

    @discardableResult
    func run(recover: () async throws -> Void, prepare: () async throws -> Void, load: () async throws -> Void) async -> Bool {
        guard !running else { return false }
        running = true
        defer { running = false }
        do {
            state = .checking(.settings)
            try Task.checkCancellation()
            try await recover()
            try Task.checkCancellation()
            state = .checking(.background)
            try await prepare()
            try Task.checkCancellation()
            state = .checking(.status)
            try await load()
            try Task.checkCancellation()
            state = .ready
            return true
        } catch is CancellationError {
            state = .failed("检查已取消，可以重新检查。")
            return false
        } catch {
            state = .failed(error.localizedDescription)
            return false
        }
    }

    func requestRetry() -> Bool {
        guard !running, case .failed = state else { return false }
        state = .checking(.settings)
        return true
    }

    func showSettingsAfterFailure() {
        guard !running, case .failed = state else { return }
        state = .ready
    }
}
