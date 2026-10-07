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
            let layout = RuntimeLayout(application: URL(fileURLWithPath: "/Applications/Fleet Hub.app"), home: FileManager.default.homeDirectoryForCurrentUser)
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
