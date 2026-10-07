import Foundation

@MainActor
public enum DesktopAutostart {
    public static func start(layout: RuntimeLayout, verify: (URL) async throws -> Void,
                             launch: ([String]) async throws -> Void) async throws {
        try PrivateRuntime.ensureDirectory(layout.home.appendingPathComponent(".macfleet"))
        try PrivateRuntime.ensureDirectory(layout.state)
        try await BackgroundRuntimeInstaller.withLock(directory: layout.runtimeDirectory) {
            let definition = try PropertyListSerialization.propertyList(from: PrivateRuntime.read(layout.runtimePlist), format: nil)
            let expected = try PropertyListSerialization.propertyList(from: layout.launchDefinition(), format: nil)
            guard let actual = definition as? NSDictionary, let required = expected as? NSDictionary, actual.isEqual(required) else {
                throw FleetError.message("后台启动定义不是独立 Fleet Agent，未启动。")
            }
            try await verify(layout.backgroundApplication)
            let domain = "gui/\(getuid())"
            if (try? await launch(["print", domain + "/com.macfleet.fleet-agent"])) != nil {
                throw FleetError.message("检测到旧版 Fleet 后台，未启动新服务。")
            }
            if (try? await launch(["print", domain + "/com.macfleet.desktop-agent"])) != nil { return }
            try await launch(["bootstrap", domain, layout.runtimePlist.path])
        }
    }
}
