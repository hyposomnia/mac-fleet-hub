import SwiftUI

struct NativeDeviceEditor: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore
    let identifier: String
    @State private var name = ""
    @State private var originalName = ""
    @State private var icon = "monitor"
    @State private var letters = ""
    @State private var color = "steel"
    @State private var proxyEnabled = false
    @State private var http = ""
    @State private var https = ""
    @State private var originalProxy = ""
    @State private var proxyLoaded = false
    @State private var meshIP = "—"
    @State private var loading = true
    @State private var saving = false
    @State private var error: String?
    private let icons = [("monitor", "显示器", "display"), ("laptop", "笔记本", "laptopcomputer"), ("desktop", "工作站", "macpro.gen3"), ("mini", "迷你主机", "macmini"), ("server", "服务器", "server.rack"), ("terminal", "终端", "terminal"), ("text", "文字", "textformat")]
    private let colors = ["steel", "teal", "green", "amber", "coral", "violet", "rose", "slate"]
    private var device: FleetDevice? { store.devices.first { $0.id == identifier } }
    var body: some View {
        VStack(spacing: 0) {
            NativeDrawerHeader(title: device?.name ?? "设备设置", locked: saving) { store.nativeSheet = nil }
            ScrollView {
                VStack(spacing: 16) {
                    if loading { ProgressView("正在读取设备…") }
                    if let error { Text(error).foregroundStyle(style.color("danger")) }
                    NativeCard(title: "设备信息") {
                        NativeField(title: "设备名称（留空恢复默认）", value: $name)
                        Text("状态：\(device?.online == true ? "在线" : "离线")　Mesh IP：\(meshIP)")
                            .font(.caption).foregroundStyle(style.color("text-2"))
                    }
                    NativeCard(title: "设备外观") {
                        LazyVGrid(columns: [GridItem(.adaptive(minimum: 78))], spacing: 8) {
                            ForEach(icons, id: \.0) { value, title, symbol in
                                Button { icon = value } label: {
                                    VStack(spacing: 8) { Image(systemName: symbol).font(.system(size: 22)); Text(title).font(.caption) }
                                        .frame(maxWidth: .infinity, minHeight: 74)
                                        .foregroundStyle(icon == value ? style.color("accent-text") : style.color("text-1"))
                                        .background(icon == value ? style.selection : .clear, in: RoundedRectangle(cornerRadius: 12))
                                }.buttonStyle(.plain).accessibilityAddTraits(icon == value ? .isSelected : [])
                            }
                        }
                        if icon == "text" {
                            NativeField(title: "文字图标", value: $letters)
                            Text("1–4 个英文字母或数字，保留大小写。").font(.caption).foregroundStyle(style.color("text-2"))
                        }
                        LazyVGrid(columns: [GridItem(.adaptive(minimum: 44))], spacing: 8) {
                            ForEach(colors, id: \.self) { value in
                                Button { color = value } label: {
                                    Circle().fill(TitaniumStyle.deviceColor(value)).frame(width: 30, height: 30)
                                        .overlay { if color == value { Image(systemName: "checkmark").foregroundStyle(.white) } }
                                        .frame(width: 44, height: 44)
                                }.buttonStyle(.plain).accessibilityLabel(value).accessibilityAddTraits(color == value ? .isSelected : [])
                            }
                        }
                        Button("恢复默认外观") { icon = "monitor"; letters = ""; color = "steel" }
                    }
                    NativeCard(title: "代理（仅对新开或重开的会话生效）") {
                        if proxyLoaded {
                            Toggle("启用代理", isOn: $proxyEnabled)
                            NativeField(title: "HTTP 代理", value: $http, keyboard: .URL)
                            NativeField(title: "HTTPS 代理", value: $https, keyboard: .URL)
                        } else { Text("设备暂时无法连接；仍可修改名称和外观，代理设置不会被覆盖。").font(.caption) }
                    }
                }.padding(16).frame(maxWidth: 800).frame(maxWidth: .infinity)
            }.scrollDismissesKeyboard(.interactively)
            HStack {
                Button("取消") { store.nativeSheet = nil }.disabled(saving)
                Spacer()
                Button(saving ? "保存中…" : "保存", action: save).buttonStyle(NativePrimaryButtonStyle())
                    .disabled(saving || loading || (icon == "text" && letters.range(of: "^[A-Za-z0-9]{1,4}$", options: .regularExpression) == nil))
            }.padding(16).background(style.color("bg"))
        }.font(.system(size: 15)).foregroundStyle(style.color("text"))
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("native-device-editor").task { await load() }
    }
    private func load() async {
        defer { loading = false }
        name = device?.name ?? ""; originalName = name
        icon = device?.icon ?? "monitor"; letters = device?.text ?? ""; color = device?.color ?? "steel"
        if let roster = try? await FleetService(store: store).request("/api/devices"), let devices = roster["devices"] as? [[String: Any]] {
            meshIP = devices.first(where: { $0["id"] as? String == identifier })?["ip"] as? String ?? "—"
        }
        do {
            let info = try await FleetService(store: store).request("/\(identifier)/api/info")
            let proxy = info["proxy"] as? [String: Any] ?? [:]
            proxyEnabled = proxy["enabled"] as? Bool ?? false
            http = proxy["http"] as? String ?? ""; https = proxy["https"] as? String ?? ""
            originalProxy = proxySignature; proxyLoaded = true
        } catch { proxyLoaded = false }
    }
    private var proxySignature: String { "\(proxyEnabled)|\(http)|\(https)" }
    private func save() {
        saving = true; error = nil; store.drawerBusy = true
        Task {
            defer { saving = false; store.drawerBusy = false }
            do {
                let service = FleetService(store: store)
                _ = try await service.shared("device-appearance", values: ["id": identifier, "appearance": ["icon": icon, "text": letters, "color": color]])
                if name != originalName { _ = try await service.request("/api/names", method: "POST", body: ["id": identifier, "name": name.trimmingCharacters(in: .whitespacesAndNewlines)]) }
                if proxyLoaded && proxySignature != originalProxy {
                    _ = try await service.request("/\(identifier)/api/proxy", method: "POST", body: ["enabled": proxyEnabled, "http": http, "https": https])
                }
                _ = try await service.shared("refresh")
                store.nativeSheet = nil; store.foreground()
            } catch { self.error = "部分设置可能已保存，请核对后重试：" + error.localizedDescription }
        }
    }
}

struct NativeAccessKeyEditor: View {
    @ObservedObject var store: FleetStore
    @ObservedObject var model: NativeSettingsModel
    let key: AccessKey?
    let done: () -> Void
    @State private var name = ""
    @State private var rpm = "10"
    @State private var device = ""
    @State private var client = ""
    @State private var project = ""
    @State private var session = ""
    @State private var projects: [String] = []
    @State private var sessions: [[String: String]] = []
    @State private var deleting = false
    @State private var initialized = false
    var body: some View {
        NativeCard(title: key == nil ? "新建密钥" : "编辑密钥") {
            NativeField(title: "名称（绑定到会话后即为别名）", value: $name)
            NativeField(title: "RPM", value: $rpm, keyboard: .numberPad)
            Picker("设备", selection: $device) {
                Text("全部设备").tag("")
                ForEach(store.devices) { Text($0.name).tag($0.id) }
            }.pickerStyle(.menu)
            Picker("AI 客户端", selection: $client) {
                Text("全部客户端").tag(""); Text("ChatGPT").tag("codex"); Text("DeepSeek").tag("deepseek")
            }.pickerStyle(.menu).disabled(device.isEmpty)
            if !projects.isEmpty {
                Menu("选择项目") {
                    Button("全部项目") { project = ""; session = "" }
                    ForEach(projects, id: \.self) { path in Button(path) { project = path; session = "" } }
                }.disabled(client.isEmpty || device.isEmpty)
            }
            NativeField(title: "项目绝对路径（留空为全部）", value: $project).disabled(device.isEmpty || client.isEmpty)
            if !sessions.isEmpty {
                Menu("选择会话") {
                    Button("全部会话") { session = "" }
                    ForEach(Array(sessions.enumerated()), id: \.offset) { _, value in
                        if project.isEmpty || value["cwd"] == project {
                            Button(value["title"] ?? value["sessionId"] ?? "会话") { session = value["sessionId"] ?? "" }
                        }
                    }
                }.disabled(project.isEmpty)
            }
            NativeField(title: "会话 ID（留空为全部）", value: $session).disabled(project.isEmpty || device.isEmpty || client.isEmpty)
            HStack {
                if key != nil { Button("删除", role: .destructive) { deleting = true } }
                Spacer()
                Button("取消", action: done).disabled(model.saving)
                Button("保存", action: save).buttonStyle(NativePrimaryButtonStyle())
                    .disabled(model.saving || name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !(1...10000).contains(Int(rpm) ?? 0) || (!project.isEmpty && !project.hasPrefix("/")) || (!session.isEmpty && project.isEmpty))
            }
        }
        .task {
            guard !initialized else { return }; initialized = true
            name = key?.name ?? ""; rpm = String(key?.rpm ?? 10)
            device = key?.binding["device_id"] ?? ""; client = key?.binding["ai_client"] ?? ""
            project = key?.binding["project_path"] ?? ""; session = key?.binding["session_id"] ?? ""
        }
        .task(id: device + ":" + client) { await loadOptions() }
        .onChange(of: device) { old, _ in if initialized && !old.isEmpty { client = ""; project = ""; session = "" } }
        .onChange(of: client) { old, _ in if initialized && !old.isEmpty { project = ""; session = "" } }
        .onChange(of: project) { old, _ in if initialized && !old.isEmpty { session = "" } }
        .alert("删除此密钥？", isPresented: $deleting) {
            Button("取消", role: .cancel) {}
            Button("删除", role: .destructive) {
                guard let key else { return }
                model.perform { _ = try await model.service.request("/api/automation/access-keys/" + key.id, method: "DELETE"); await model.load("automation"); done() }
            }
        } message: { Text("删除后无法再发起新请求；已接收的任务仍会执行。") }
    }
    private func loadOptions() async {
        projects = []; sessions = []
        guard !device.isEmpty, !client.isEmpty else { return }
        let selected = device + ":" + client
        do {
            let catalog = try await model.service.shared("binding-sessions", values: ["device": device, "client": client]) as? [[String: Any]] ?? []
            guard selected == device + ":" + client else { return }
            sessions = catalog.map { value in
                ["title": value["title"] as? String ?? "", "sessionId": value["sessionId"] as? String ?? "", "cwd": value["projectCwd"] as? String ?? value["cwd"] as? String ?? ""]
            }
            projects = Array(Set(sessions.compactMap { $0["cwd"] }.filter { $0.hasPrefix("/") })).sorted()
        } catch { model.error = "候选加载失败，可手动填写绝对路径及会话 ID：" + error.localizedDescription }
    }
    private func save() {
        var binding: [String: String] = [:]
        if !device.isEmpty {
            binding["device_id"] = device
            if !client.isEmpty { binding["ai_client"] = client }
            if !client.isEmpty && !project.isEmpty { binding["project_path"] = project }
            if !client.isEmpty && !project.isEmpty && !session.isEmpty { binding["session_id"] = session }
        }
        let body: [String: Any] = ["name": name.trimmingCharacters(in: .whitespacesAndNewlines), "rpm": Int(rpm) ?? 10, "binding": binding.isEmpty ? NSNull() : binding as Any]
        model.perform {
            _ = try await model.service.request("/api/automation/access-keys" + (key.map { "/" + $0.id } ?? ""), method: key == nil ? "POST" : "PATCH", body: body)
            await model.load("automation"); done()
        }
    }
}
