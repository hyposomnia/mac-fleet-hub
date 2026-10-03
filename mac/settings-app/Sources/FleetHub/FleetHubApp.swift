import SwiftUI
import FleetCore

@main
struct FleetHubApp: App {
    @NSApplicationDelegateAdaptor(FleetApplicationDelegate.self) private var delegate
    @StateObject private var model: SettingsModel
    @StateObject private var updater: AppUpdater
    private let management: NativeManagement

    init() {
        let layout = RuntimeLayout(application: Bundle.main.bundleURL, home: FileManager.default.homeDirectoryForCurrentUser)
        let client = NativeManagement(layout: layout)
        management = client
        _model = StateObject(wrappedValue: SettingsModel(client: client))
        _updater = StateObject(wrappedValue: AppUpdater(management: client))
    }
    var body: some Scene {
        Window("Fleet Hub", id: "settings") {
            SettingsView(model: model, updater: updater, management: management)
                .frame(minWidth: 720, minHeight: 570)
                .onAppear { delegate.updater = updater }
        }
        .defaultSize(width: 780, height: 640)
        .windowResizability(.contentMinSize)
    }
}
