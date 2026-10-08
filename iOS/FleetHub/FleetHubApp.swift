import SwiftUI

@main
struct FleetHubApp: App {
    @StateObject private var store = FleetStore()
    @Environment(\.scenePhase) private var phase

    var body: some Scene {
        WindowGroup {
            FleetWorkspace(store: store)
                .tint(TitaniumStyle.color("accent"))
                .foregroundStyle(TitaniumStyle.color("text"))
                .preferredColorScheme(store.snapshot?.themePreference == "system" ? nil : store.snapshot?.theme == "dark" ? .dark : .light)
                .onChange(of: phase) { _, value in
                    if value == .active { store.foreground() }
                }
        }
    }
}
