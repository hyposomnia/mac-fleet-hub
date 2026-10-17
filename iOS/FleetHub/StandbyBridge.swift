import Foundation
import WidgetKit

@MainActor
final class StandbyBridge {
    private var identity = ""
    private var gateway: URL?
    private var lastRefresh = Date.distantPast
    private var task: Task<Void, Never>?
    private var generation = UUID()

    func reset() {
        task?.cancel(); task = nil
        generation = UUID()
        identity = ""; gateway = nil; lastRefresh = .distantPast
        StandbyStorage.clear()
        WidgetCenter.shared.reloadAllTimelines()
    }

    func refresh(store: FleetStore) {
        guard let snapshot = store.snapshot, snapshot.ready, !snapshot.identity.isEmpty,
              let gateway = store.gateway, gateway.scheme == "https" else { return }
        if identity != snapshot.identity || self.gateway != gateway {
            task?.cancel(); task = nil; generation = UUID()
            let existing = StandbyStorage.connection()
            if existing?.identity != snapshot.identity || existing?.gateway != gateway { StandbyStorage.clear() }
            identity = snapshot.identity; self.gateway = gateway; lastRefresh = .distantPast
        }
        guard task == nil, Date().timeIntervalSince(lastRefresh) > 60 else { return }
        let identity = identity
        let generation = generation
        lastRefresh = Date()
        task = Task { [weak self, weak store] in
            guard let self, let store else { return }
            defer { if self.generation == generation { self.task = nil } }
            do {
                var connection = StandbyStorage.connection()
                if connection == nil || (connection?.expiresAt ?? 0) < Date().addingTimeInterval(3 * 86400).timeIntervalSince1970 * 1000 {
                    let result = try await FleetService(store: store).request("/api/standby/token", method: "POST")
                    guard !Task.isCancelled, self.identity == identity, store.snapshot?.identity == identity,
                          let token = result["token"] as? String, let expires = result["expiresAt"] as? Double else { return }
                    connection = StandbyConnection(gateway: gateway, identity: identity, token: token, expiresAt: expires, generation: UUID())
                    try StandbyStorage.install(connection!)
                }
                guard let connection else { return }
                _ = try await StandbyStorage.fetch(connection)
                guard !Task.isCancelled else { return }
                WidgetCenter.shared.reloadAllTimelines()
            } catch {
                // Widget shows a dated recent snapshot or an explicit unavailable
                // state. A failed telemetry fetch never breaks normal app use.
                if !Task.isCancelled { WidgetCenter.shared.reloadAllTimelines() }
            }
        }
    }
}
