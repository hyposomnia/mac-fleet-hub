import Foundation

enum FleetAssistant: String, CaseIterable, Identifiable, Codable {
    case codex, claude, dsh

    var id: String { rawValue }
    var title: String {
        switch self {
        case .codex: return "ChatGPT"
        case .claude: return "Claude"
        case .dsh: return "DeepSeek"
        }
    }
}

struct FleetDevice: Codable, Identifiable {
    let id: String
    let name: String
    let online: Bool
    var icon: String? = nil
    var color: String? = nil
    var count: Int? = nil

    var symbol: String {
        ["monitor": "display", "laptop": "laptopcomputer", "desktop": "macpro.gen3",
         "mini": "macmini", "server": "server.rack", "terminal": "terminal"][icon ?? ""] ?? "display"
    }
}

struct FleetSession: Codable, Identifiable {
    let sessionId: String
    let macId: String
    let assistant: FleetAssistant
    let title: String
    let cwd: String
    let project: String
    let projectKey: String
    let mtime: Double
    let pinned: Bool
    let unread: Bool
    let running: Bool
    let waiting: Bool
    var live: Bool? = nil
    var status: String? = nil
    var actions: [String]? = nil

    var id: String { "\(macId)/\(assistant.rawValue)/\(sessionId)" }
}

struct FleetProject: Codable, Identifiable {
    let id: String
    let title: String
    let cwd: String
    let projectless: Bool
    let macId: String?
    let sessionIDs: [String]
    let collapsed: Bool
}

struct FleetSnapshot: Codable {
    let version: Int
    let theme: String?
    let themePreference: String?
    let ready: Bool
    let identity: String
    let devices: [FleetDevice]
    let sessions: [FleetSession]
    let deviceScope: String
    let assistant: FleetAssistant
    let archived: Bool
    let loading: Bool
    let hasMore: Bool
    let errors: [String]
    let gatewayDown: Bool
    let selectedID: String?
    let title: String
    let renderer: String
    var projects: [FleetProject]? = nil
    var assistants: [FleetAssistant]? = nil
    var mode: String? = nil
    var view: String? = nil
    var search: String? = nil
    var overlay: String? = nil
    var email: String? = nil
    var admin: Bool? = nil
    var dshNativeURL: String? = nil
    var dshNativeHint: String? = nil
    var degraded: Bool? = nil
}

enum FleetLayout: Equatable {
    case compact, compactDesktop, expanded

    static func resolve(width: Double) -> Self {
        if width <= TitaniumStyle.design.navigation["mobileBreakpoint"]! { return .compact }
        if width <= TitaniumStyle.design.navigation["compactDesktopBreakpoint"]! { return .compactDesktop }
        return .expanded
    }

    var deviceWidth: Double {
        self == .compact ? 0 : TitaniumStyle.design.navigation[self == .compactDesktop ? "compactDeviceWidth" : "deviceWidth"]!
    }
    var sessionWidth: Double {
        TitaniumStyle.design.navigation[self == .compactDesktop ? "compactSessionWidth" : "sessionWidth"]!
    }
}

enum GatewayAddress {
    static func parse(_ value: String, allowLocalHTTP: Bool = false) -> URL? {
        guard var components = URLComponents(string: value.trimmingCharacters(in: .whitespacesAndNewlines)),
              let host = components.host, !host.isEmpty,
              components.user == nil, components.password == nil,
              components.query == nil, components.fragment == nil,
              components.path.isEmpty || components.path == "/" else { return nil }
        let local = ["localhost", "127.0.0.1", "::1"].contains(host)
        guard components.scheme == "https" || (allowLocalHTTP && local && components.scheme == "http") else { return nil }
        components.path = "/"
        return components.url
    }

    static func sameOrigin(_ first: URL, _ second: URL) -> Bool {
        first.scheme?.lowercased() == second.scheme?.lowercased() &&
        first.host?.lowercased() == second.host?.lowercased() &&
        (first.port ?? (first.scheme == "https" ? 443 : 80)) ==
        (second.port ?? (second.scheme == "https" ? 443 : 80))
    }
}
