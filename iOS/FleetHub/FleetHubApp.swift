import SwiftUI

@main
struct FleetHubApp: App {
    @StateObject private var store = FleetStore()
    @Environment(\.scenePhase) private var phase

    var body: some Scene {
        WindowGroup {
            let style = FleetStyle(tokens: store.appearanceTokens, theme: store.snapshot?.theme, followsSystem: store.snapshot?.themePreference == "system")
            Group {
                if store.gateway == nil { GatewaySetup(store: store) }
                else { FleetWorkspace(store: store) }
            }
                .environment(\.fleetStyle, style)
                .environment(\.colorScheme, style.colorScheme ?? .light)
                .tint(style.color("accent"))
                .foregroundStyle(style.color("text"))
                .preferredColorScheme(store.snapshot?.themePreference == "system" ? nil : store.snapshot?.theme == "dark" ? .dark : .light)
                .onChange(of: phase) { _, value in
                    if value == .active { store.foreground() }
                }
        }
    }
}
