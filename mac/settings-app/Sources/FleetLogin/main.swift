import Darwin
import FleetCore
import Foundation

@main
struct FleetLogin {
    @MainActor
    static func main() async {
        do {
            guard Array(CommandLine.arguments.dropFirst()) == ["autostart-start"] else {
                throw FleetError.message("无效登录启动操作。")
            }
            // launchd passes a bare argv[0]; locate the actual BundleProgram at
            // Contents/Library/Helpers/fleet-login-launcher within its host app.
            var size: UInt32 = 0
            _NSGetExecutablePath(nil, &size)
            var path = [CChar](repeating: 0, count: Int(size))
            guard _NSGetExecutablePath(&path, &size) == 0 else { throw FleetError.message("无法定位登录启动程序。") }
            var application = URL(fileURLWithPath: String(cString: path)).resolvingSymlinksInPath()
            for _ in 0..<4 { application.deleteLastPathComponent() }
            let layout = RuntimeLayout(application: application, home: FileManager.default.homeDirectoryForCurrentUser)
            let verify = try await BackgroundRuntimeVerifier.verifier(host: layout.application)
            try await DesktopAutostart.start(layout: layout, verify: verify) { arguments in
                _ = try await CommandRunner.run(executable: URL(fileURLWithPath: "/bin/launchctl"), arguments: arguments)
            }
        } catch {
            FileHandle.standardError.write(Data((error.localizedDescription + "\n").utf8))
            exit(1)
        }
    }
}
