import FleetCore

enum FleetSetupAction: Equatable {
    case installApplication, installBackground, startBackground

    init(requiresInstallation: Bool, backgroundInstalled: Bool) {
        self = requiresInstallation ? .installApplication : backgroundInstalled ? .startBackground : .installBackground
    }

    var title: String { self == .startBackground ? "启动" : "安装并启动" }
    var status: String {
        switch self {
        case .installApplication: return "尚未安装"
        case .installBackground: return "后台未安装"
        case .startBackground: return "后台未运行"
        }
    }

    static let replacementTitle = "卸载旧版并安装新版？"
    static func replacementMessage(legacy: Bool) -> String {
        "检测到旧版 Fleet。确认后将移除旧程序及后台服务，安装并启动新版。" +
        (legacy
            ? "保留本机配置、聊天会话和个人文件；安装后需在网页确认账号关联。检测到进行中的任务时会提示等待。替换期间旧网页连接会短暂断开，请勿发送新任务。"
            : "保留账号关联、本机配置、聊天会话和个人文件。进行中的任务不会被强制中断；需要等待时会提示原因。")
    }

    static func installationArguments(replacementConfirmed: Bool) -> [String] {
        ["--fleet-install-and-start"] + (replacementConfirmed ? ["--fleet-replace-legacy-confirmed"] : [])
    }
    static func replacementConfirmed(arguments: [String]) -> Bool {
        startsAfterInstallation(arguments: arguments) && arguments.contains("--fleet-replace-legacy-confirmed")
    }

    static func startsAfterInstallation(arguments: [String]) -> Bool { arguments.contains("--fleet-install-and-start") }
    static func authorizesDiskAfterInstallation(arguments: [String]) -> Bool { arguments.contains("--fleet-authorize-disk") }
    static func installationTitle(installed: Bool) -> String { installed ? "替换并安装新版" : "安装并启动" }
    static func initialOrigin(arguments: [String]) -> String? {
        guard let argument = arguments.first(where: { $0.hasPrefix("--fleet-origin=") }) else { return nil }
        return try? FleetSettings.validatedOrigin(String(argument.dropFirst("--fleet-origin=".count)))
    }
}
