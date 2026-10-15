import SwiftUI
import FleetCore

struct FleetHubApp: App {
    @NSApplicationDelegateAdaptor(FleetApplicationDelegate.self) private var delegate
    @StateObject private var model: SettingsModel
    @StateObject private var updater: AppUpdater
    private let management: NativeManagement
    private let preview: Bool

    init() {
        #if DEBUG
        preview = CommandLine.arguments.contains("--fleet-ui-preview") || Bundle.main.object(forInfoDictionaryKey: "FleetUIPreview") as? Bool == true
        #else
        preview = false
        #endif
        let application = preview ? RuntimeLayout.installedApplication() : Bundle.main.bundleURL
        let resources = preview ? Bundle.main.bundleURL.appendingPathComponent("Contents/Resources/codex") : nil
        let layout = RuntimeLayout(application: application, home: FileManager.default.homeDirectoryForCurrentUser, codexResources: resources)
        let client = NativeManagement(layout: layout, controlsRegisteredLoginService: preview)
        management = client
        _model = StateObject(wrappedValue: SettingsModel(client: client))
        _updater = StateObject(wrappedValue: AppUpdater(management: client))
    }
    var body: some Scene {
        Window(preview ? RuntimeLayout.applicationName + " — 界面预览" : RuntimeLayout.applicationName, id: "settings") {
            SettingsView(model: model, updater: updater, management: management, preview: preview)
                .preferredColorScheme(.light)
                .frame(minWidth: 720, minHeight: 570)
                .onAppear { delegate.updater = updater }
        }
        .defaultSize(width: 780, height: 640)
        .windowResizability(.contentMinSize)
    }
}
