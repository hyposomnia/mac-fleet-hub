import Combine
import Foundation
import WebKit

@MainActor
final class FleetStore: ObservableObject {
    @Published var snapshot: FleetSnapshot?
    @Published var error: String?
    @Published var gateway: URL?
    @Published var loading = false
    @Published var showingDetail = false
    @Published var deviceSheet = false
    @Published var settingsSheet = false
    @Published var auxiliaryPage: WebPage?
    @Published var exportedFile: SharedFile?
    @Published var search = ""
    @Published var busy = false
    @Published var openedSequence = 0
    private var detailBeforeOverlay: Bool?
    let dataStore = WKWebsiteDataStore.default()
    var webView: WKWebView?
    private var requestID = 0
    private var searchTask: Task<Void, Never>?
    private var commandTimeout: Task<Void, Never>?
    private var pendingRequest: Int?

    init() {
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("-fleet-test-reset") {
            for key in ["fleet.devices.visible", "fleet.sessions.visible"] { UserDefaults.standard.set(true, forKey: key) }
        }
        #endif
        if let saved = UserDefaults.standard.string(forKey: "fleet.gateway") {
            gateway = GatewayAddress.parse(saved)
        }
        #if DEBUG
        if let index = ProcessInfo.processInfo.arguments.firstIndex(of: "-fleet-test-gateway"),
           ProcessInfo.processInfo.arguments.indices.contains(index + 1) {
            gateway = GatewayAddress.parse(ProcessInfo.processInfo.arguments[index + 1], allowLocalHTTP: true)
        }
        #endif
    }

    var ready: Bool { snapshot?.ready == true }
    var devices: [FleetDevice] { snapshot?.devices ?? [] }
    var sessions: [FleetSession] { snapshot?.sessions ?? [] }
    var isFiles: Bool { snapshot?.mode == "files" || snapshot?.renderer == "files" }
    var assistants: [FleetAssistant] { snapshot?.assistants ?? [.codex, .claude] }
    var projects: [FleetProject] { snapshot?.projects ?? [] }
    var deviceName: String {
        devices.first(where: { $0.id == snapshot?.deviceScope })?.name ?? "全部设备"
    }

    func connect(_ address: String) -> Bool {
        guard let url = GatewayAddress.parse(address) else { return false }
        gateway = url
        UserDefaults.standard.set(url.absoluteString, forKey: "fleet.gateway")
        reset()
        webView?.load(URLRequest(url: url))
        return true
    }

    func reset() {
        snapshot = nil
        error = nil
        showingDetail = false
        busy = false
        search = ""
        pendingRequest = nil
        commandTimeout?.cancel()
        searchTask?.cancel()
        detailBeforeOverlay = nil
        auxiliaryPage = nil
        discardExport()
        deviceSheet = false
    }

    func receive(_ body: Any) {
        guard let object = body as? [String: Any], let type = object["type"] as? String else { return }
        if type == "snapshot", let payload = object["payload"],
           let data = try? JSONSerialization.data(withJSONObject: payload),
           let value = try? JSONDecoder().decode(FleetSnapshot.self, from: data), value.version == 1 {
            if !value.ready { reset(); loading = false; return }
            if let identity = snapshot?.identity, identity != value.identity { reset() }
            if snapshot?.overlay != nil && value.overlay == nil, let previous = detailBeforeOverlay {
                showingDetail = previous
                detailBeforeOverlay = nil
            }
            snapshot = value
            loading = false
        } else if type == "result", let identifier = object["id"] as? Int, identifier == pendingRequest {
            busy = false
            pendingRequest = nil
            commandTimeout?.cancel()
            if let message = object["error"] as? String {
                error = message
                if let previous = detailBeforeOverlay { showingDetail = previous; detailBeforeOverlay = nil }
            }
            else if let path = object["url"] as? String, let gateway,
                    let url = URL(string: path, relativeTo: gateway)?.absoluteURL,
                    GatewayAddress.sameOrigin(url, gateway) {
                auxiliaryPage = WebPage(url: url)
            } else if object["opened"] as? Bool == true { showingDetail = true; openedSequence += 1 }
        } else if type == "unsupported" {
            error = "网关版本不支持嵌入模式，请更新 dashboard。"
        }
    }

    func send(_ command: String, values: [String: Any] = [:]) {
        guard ready, !busy, let webView else { return }
        requestID += 1
        let identifier = requestID
        var message = values
        message["command"] = command
        message["id"] = identifier
        guard let data = try? JSONSerialization.data(withJSONObject: message),
              let json = String(data: data, encoding: .utf8) else { return }
        busy = true
        pendingRequest = identifier
        error = nil
        commandTimeout?.cancel()
        commandTimeout = Task { [weak self] in
            try? await Task.sleep(for: .seconds(35))
            guard !Task.isCancelled, self?.pendingRequest == identifier else { return }
            self?.busy = false
            self?.pendingRequest = nil
            self?.error = "操作超时，请检查连接后重试。"
        }
        webView.evaluateJavaScript("window.FleetNative?.dispatch(\(json)); void 0") { [weak self] _, failure in
            if let failure, self?.pendingRequest == identifier {
                self?.busy = false
                self?.pendingRequest = nil
                self?.commandTimeout?.cancel()
                self?.error = failure.localizedDescription
            }
        }
    }

    func selectDevice(_ id: String) {
        showingDetail = isFiles
        deviceSheet = false
        send("device", values: ["device": id])
    }

    func open(_ session: FleetSession, terminal: Bool = false) {
        send("open", values: ["sessionId": session.sessionId, "macId": session.macId,
                              "assistant": session.assistant.rawValue, "terminal": terminal])
    }

    func action(_ action: String, session: FleetSession, value: String = "") {
        send("action", values: ["sessionId": session.sessionId, "macId": session.macId,
                               "assistant": session.assistant.rawValue, "action": action, "value": value])
    }

    func newSession(project: FleetProject? = nil, device: String? = nil) {
        guard let macId = device ?? project?.macId ?? (snapshot?.deviceScope == "all" ? nil : snapshot?.deviceScope),
              devices.contains(where: { $0.id == macId && $0.online }) else { return }
        var values: [String: Any] = ["macId": macId, "cwd": project?.cwd ?? ""]
        if let project { values["project"] = project.id }
        send("new", values: values)
    }

    func showOverlay(_ command: String, device: String? = nil) {
        guard ready, !busy else { return }
        detailBeforeOverlay = showingDetail
        deviceSheet = false
        showingDetail = true
        send(command, values: device.map { ["macId": $0] } ?? [:])
    }

    func backToList() {
        webView?.endEditing(true)
        showingDetail = false
        send("back")
    }

    func setMode(files: Bool) {
        showingDetail = files
        send(files ? "files" : "sessions")
    }

    func searchChanged() {
        searchTask?.cancel()
        let value = search
        searchTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(350))
            guard !Task.isCancelled else { return }
            while self?.busy == true && !Task.isCancelled {
                try? await Task.sleep(for: .milliseconds(150))
            }
            guard !Task.isCancelled else { return }
            self?.send("search", values: ["search": value])
        }
    }

    func foreground() {
        guard ready else { return }
        webView?.evaluateJavaScript("window.FleetNative?.refresh(); void 0")
    }

    func discardExport() {
        if let file = exportedFile {
            try? FileManager.default.removeItem(at: file.url.deletingLastPathComponent())
        }
        exportedFile = nil
    }
}

struct WebPage: Identifiable {
    let id = UUID()
    let url: URL
}

struct SharedFile: Identifiable {
    let id = UUID()
    let url: URL
}
