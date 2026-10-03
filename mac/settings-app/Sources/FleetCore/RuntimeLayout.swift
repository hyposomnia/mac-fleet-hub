import Foundation

public struct RuntimeLayout: Sendable {
    public let application: URL
    public let home: URL
    public init(application: URL, home: URL) {
        self.application = application
        self.home = home
    }
    public var backgroundApplication: URL {
        application.appendingPathComponent("Contents/Library/LoginItems/Fleet Agent.app")
    }
    public var agent: URL { backgroundApplication.appendingPathComponent("Contents/MacOS/fleet-agent") }
    public var state: URL { home.appendingPathComponent(".macfleet/desktop") }
    public var runtimePlist: URL { state.appendingPathComponent("agent.plist") }
    public var requiresInstallation: Bool { application.path != "/Applications/Fleet Hub.app" }

    public func launchDefinition() throws -> Data {
        guard !requiresInstallation else {
            throw FleetError.message("请先将 Fleet Hub 安装到应用程序，再启动后台服务。")
        }
        let definition: [String: Any] = [
            "Label": "com.macfleet.desktop-agent",
            "ProgramArguments": [agent.path],
            "RunAtLoad": true,
            "KeepAlive": true,
            "ProcessType": "Background",
            "ThrottleInterval": 10,
            "Umask": 0o077,
            "WorkingDirectory": home.path,
            "StandardOutPath": state.appendingPathComponent("agent.log").path,
            "StandardErrorPath": state.appendingPathComponent("agent.log").path,
            "EnvironmentVariables": [
                "HOME": home.path,
                "PATH": backgroundApplication.appendingPathComponent("Contents/Resources/bin").path + ":/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
                "LANG": "en_US.UTF-8", "LC_ALL": "en_US.UTF-8",
                "FLEET_DESKTOP_MANAGED": "1",
                "FLEET_BINDING_FILE": state.appendingPathComponent("binding.json").path,
                "FLEET_TMUX_CONF": state.appendingPathComponent("tmux.conf").path,
                "FLEET_CODEX_DESKTOP_SHARED_DAEMON": "0",
                "FLEET_AUTO_CMDR": "0"
            ]
        ]
        return try PropertyListSerialization.data(fromPropertyList: definition, format: .xml, options: 0)
    }
}
