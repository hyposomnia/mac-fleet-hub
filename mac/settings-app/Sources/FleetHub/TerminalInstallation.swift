import AppKit
import FleetCore

@MainActor
enum TerminalInstallation {
    static func origin(arguments: [String]) throws -> String? {
        guard arguments.contains("--fleet-install-cli") else { return nil }
        let origins = arguments.filter { $0.hasPrefix("--fleet-origin=") }
        guard origins.count == 1, arguments.dropFirst().allSatisfy({ $0 == "--fleet-install-cli" || $0.hasPrefix("--fleet-origin=") }) else {
            throw FleetError.message("用法：Fleet Agent Settings --fleet-install-cli --fleet-origin=https://服务地址")
        }
        return try FleetSettings.validatedOrigin(String(origins[0].dropFirst("--fleet-origin=".count)))
    }

    static func run(origin: String) async throws -> Int32 {
        guard getuid() != 0 else { throw FleetError.message("请使用当前登录用户运行安装命令，不要使用 sudo。") }
        let layout = RuntimeLayout(application: Bundle.main.bundleURL, home: FileManager.default.homeDirectoryForCurrentUser)
        let management = NativeManagement(layout: layout)
        if layout.requiresInstallation {
            progress("正在安装或更新 Fleet Agent Settings…")
            let installed = try await management.install(replaceExisting: true)
            guard let executable = Bundle(url: installed)?.executableURL else { throw FleetError.message("已安装的应用缺少可执行文件。") }
            // Inherit the terminal handles so browser authorization progress is visible immediately.
            let process = Process()
            process.executableURL = executable
            process.arguments = ["--fleet-install-cli", "--fleet-origin=\(origin)"]
            return try await withCheckedThrowingContinuation { continuation in
                process.terminationHandler = { process in continuation.resume(returning: process.terminationStatus) }
                do { try process.run() } catch { continuation.resume(throwing: error) }
            }
        }
        let result = try await TerminalSetup(client: management, openBrowser: { NSWorkspace.shared.open($0) }).run(origin: origin, progress: progress)
        switch result {
        case .complete: progress("安装完成，Fleet Agent 已连接。")
        case .needsDiskAccess:
            let configuration = NSWorkspace.OpenConfiguration()
            configuration.arguments = ["--fleet-disk-guide"]
            configuration.createsNewApplicationInstance = true
            _ = try await NSWorkspace.shared.openApplication(at: layout.application, configuration: configuration)
            progress("设备已连接。请在打开的磁盘权限引导中开启 Fleet Agent，生效后引导会自动关闭。")
        }
        return 0
    }

    static func progress(_ message: String) {
        FileHandle.standardOutput.write(Data((message + "\n").utf8))
    }
}

@main
struct FleetProgram {
    @MainActor static func main() async {
        do {
            if let origin = try TerminalInstallation.origin(arguments: CommandLine.arguments) {
                exit(try await TerminalInstallation.run(origin: origin))
            }
            FleetHubApp.main()
        } catch {
            FileHandle.standardError.write(Data(("安装未完成：\(error.localizedDescription)\n").utf8))
            exit(1)
        }
    }
}
