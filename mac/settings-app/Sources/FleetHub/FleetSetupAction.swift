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

    static func startsAfterInstallation(arguments: [String]) -> Bool { arguments.contains("--fleet-install-and-start") }
}
