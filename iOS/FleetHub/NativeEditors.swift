import SwiftUI

struct NativeDeviceEditor: View {
    @Environment(\.fleetStyle) private var style
    @Environment(\.dismiss) private var dismiss
    @ObservedObject var store: FleetStore
    let identifier: String
    @State private var name = ""
    @State private var originalName = ""
    @State private var defaultName = ""
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
        NavigationStack {
            Form {
                Section {
                    TextField("设备名称", text: $name, prompt: Text(defaultName))
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                        .accessibilityIdentifier("设备名称")
                } header: { NativeSettingsSectionHeader(title: "设备信息") }
                NativeSettingsSection(title: "设备外观") {
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 70))], spacing: 8) {
                        ForEach(icons, id: \.0) { value, title, symbol in
                            Button { icon = value } label: {
                                VStack(spacing: 8) { FleetDeviceGlyph(icon: value, symbol: symbol).frame(width: 24, height: 24); Text(title).font(.caption) }
                                    .frame(maxWidth: .infinity, minHeight: 70)
                                    .foregroundStyle(icon == value ? style.color("accent-text") : style.color("text-1"))
                                    .background(icon == value ? style.selection : .clear, in: RoundedRectangle(cornerRadius: 12))
                            }.buttonStyle(.plain).accessibilityLabel(title).accessibilityAddTraits(icon == value ? .isSelected : [])
                        }
                    }
                    if icon == "text" {
                        NativeDeviceIconTextField(text: $letters).frame(minHeight: NativeSettingsLayout.rowHeight)
                    }
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 44))], spacing: 8) {
                        ForEach(colors, id: \.self) { value in
                            Button { color = value } label: {
                                Circle().fill(TitaniumStyle.deviceColor(value)).frame(width: 28, height: 28)
                                    .overlay { if color == value { Image(systemName: "checkmark").foregroundStyle(.white) } }
                                    .frame(width: 44, height: 44)
                            }.buttonStyle(.plain).accessibilityLabel(value).accessibilityAddTraits(color == value ? .isSelected : [])
                        }
                    }
                    Button("恢复默认外观") { icon = "monitor"; letters = ""; color = "steel" }
                }
                Section {
                    if proxyLoaded {
                        Toggle("启用代理", isOn: $proxyEnabled)
                        VStack(alignment: .leading, spacing: 6) {
                            Text("HTTP 代理").font(.caption).foregroundStyle(.secondary)
                            TextField("HTTP 代理", text: $http).keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                        }
                        VStack(alignment: .leading, spacing: 6) {
                            Text("HTTPS 代理").font(.caption).foregroundStyle(.secondary)
                            TextField("HTTPS 代理", text: $https).keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                        }
                    } else if !loading {
                        Text("设备暂时无法连接；仍可修改名称和外观，代理设置不会被覆盖。").font(.caption)
                    }
                } header: { NativeSettingsSectionHeader(title: "代理") } footer: { Text("仅对新开或重新打开的会话生效。") }
                if let error { Section { Text(error).foregroundStyle(.red) } }
            }
            .nativeSettingsForm().accessibilityIdentifier("native-editor-content")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .principal) {
                    VStack(spacing: 2) {
                        HStack(spacing: 6) {
                            Text(device?.name ?? "设备设置").font(.headline).lineLimit(1)
                            DeviceStatusTag(online: device?.online == true)
                        }
                        Text(meshIP).font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
                    }.accessibilityElement(children: .contain).accessibilityIdentifier("device-title")
                }
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() }.disabled(saving) }
                ToolbarItem(placement: .confirmationAction) {
                    Button("保存", action: save).disabled(saving || loading || (icon == "text" && letters.range(of: "^[A-Za-z0-9]{1,4}$", options: .regularExpression) == nil))
                }
            }
            .overlay { if loading { ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity) } }
        }.accessibilityElement(children: .contain).accessibilityIdentifier("native-device-editor")
            .interactiveDismissDisabled(saving).task { await load() }
    }
    private func load() async {
        defer { loading = false }
        name = device?.name ?? ""; originalName = name
        defaultName = "Mac " + identifier.dropFirst()
        icon = device?.icon ?? "monitor"; letters = device?.text ?? ""; color = device?.color ?? "steel"
        if let roster = try? await FleetService(store: store).request("/api/devices"), let devices = roster["devices"] as? [[String: Any]] {
            let current = devices.first(where: { $0["id"] as? String == identifier })
            meshIP = current?["ip"] as? String ?? "—"
            if let index = current?["index"] as? Int { defaultName = "Mac \(index)" }
        }
        do {
            let info = try await FleetService(store: store).request("/\(identifier)/api/info")
            if meshIP == "—" { meshIP = info["meshIP"] as? String ?? "—" }
            let proxy = info["proxy"] as? [String: Any] ?? [:]
            proxyEnabled = proxy["enabled"] as? Bool ?? false
            http = Self.proxyDefault(proxy["http"] as? String); https = Self.proxyDefault(proxy["https"] as? String)
            originalProxy = proxySignature; proxyLoaded = true
        } catch { proxyLoaded = false }
    }
    static func proxyDefault(_ value: String?) -> String {
        guard let value, !value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return "http://127.0.0.1:7897" }
        return value
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
                dismiss(); store.foreground()
            } catch { self.error = "部分设置可能已保存，请核对后重试：" + error.localizedDescription }
        }
    }
}

struct NativeAccessKeyDetail: View {
    @Environment(\.dismiss) private var dismiss
    @ObservedObject var store: FleetStore
    @ObservedObject var model: NativeSettingsModel
    let original: AccessKey
    let edit: (AccessKey) -> Void
    @State private var projectName = ""
    @State private var sessionName = ""
    private var key: AccessKey { model.keys.first { $0.id == original.id } ?? original }
    var body: some View {
        Form {
            Section {
                LabeledContent("名称", value: key.name)
                LabeledContent("RPM", value: String(key.rpm))
                LabeledContent("创建时间", value: AccessKey.dateLabel(key.created))
                LabeledContent("最近使用", value: AccessKey.dateLabel(key.used))
            } header: { NativeSettingsSectionHeader(title: "密钥信息") }
            Section {
                Button {
                    FleetService.copy(key.secret, sensitive: true)
                    model.status = "密钥已复制（两分钟后从剪贴板清除）"
                } label: { Label("复制密钥", systemImage: "doc.on.doc") }
                Button {
                    model.perform {
                        let result = try await model.service.shared("access-key-request", values: ["key": ["key": key.secret, "name": key.name, "binding": key.binding]])
                        if let value = result as? String { FleetService.copy(value, sensitive: true); model.status = "请求示例已复制" }
                    }
                } label: { Label("复制请求示例", systemImage: "doc.text") }
            } footer: {
                if !key.recoverable { Text("旧版密钥不可恢复") }
            }.disabled(!key.recoverable || model.saving)
            Section {
                LabeledContent("设备", value: key.binding["device_id"].map { id in store.devices.first { $0.id == id }?.name ?? id } ?? "全部设备")
                LabeledContent("AI 客户端", value: key.binding["ai_client"].map { $0 == "codex" ? "ChatGPT" : $0 == "deepseek-cli" ? "DeepSeek CLI" : "DeepSeek" } ?? "全部客户端")
                LabeledContent("项目", value: key.binding["project_path"].map { projectName.isEmpty ? URL(fileURLWithPath: $0).lastPathComponent : projectName } ?? "全部项目")
                LabeledContent("会话", value: key.binding["session_id"].map { sessionName.isEmpty ? $0 : sessionName } ?? "全部会话")
            } header: { NativeSettingsSectionHeader(title: "访问范围") } footer: {
                Text("未限定范围的密钥可向你的全部设备会话发送消息，请妥善保管。")
            }
            if !model.status.isEmpty { Section { Text(model.status).foregroundStyle(.secondary) } }
            if let error = model.error { Section { Text(error).foregroundStyle(.red) } }
        }
        .nativeSettingsForm()
        .accessibilityIdentifier("native-key-detail")
        .navigationTitle("密钥详情").navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .primaryAction) { Button("编辑") { edit(key) }.disabled(model.saving) }
        }
        .task(id: key.binding) {
            projectName = ""; sessionName = ""
            guard let device = key.binding["device_id"], let client = key.binding["ai_client"] else { return }
            let binding = key.binding
            if let catalog = try? await model.service.shared("binding-sessions", values: ["device": device, "client": client]) as? [[String: Any]], !Task.isCancelled {
                projectName = catalog.first { ($0["projectCwd"] as? String ?? $0["cwd"] as? String) == binding["project_path"] }?["projectName"] as? String ?? ""
                sessionName = catalog.first { $0["sessionId"] as? String == binding["session_id"] }?["title"] as? String ?? ""
            }
        }
        .onChange(of: model.keys.map(\.id)) { _, ids in if !ids.contains(original.id) { dismiss() } }
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
    @State private var projectNames: [String: String] = [:]
    @State private var sessions: [[String: String]] = []
    @State private var deleting = false
    @State private var initialized = false
    @State private var loadingOptions = false
    @State private var optionsError: String?
    var body: some View {
        NavigationStack {
        Form {
            NativeField(title: "名称（绑定到会话后即为别名）", value: $name)
            NativeField(title: "RPM", value: $rpm, keyboard: .numberPad)
            Picker("设备", selection: Binding(get: { device }, set: { device = $0; client = ""; project = ""; session = "" })) {
                Text("全部设备").tag("")
                ForEach(store.devices) { Text($0.name).tag($0.id) }
            }.pickerStyle(.menu)
            Picker("AI 客户端", selection: Binding(get: { client }, set: { client = $0; project = ""; session = "" })) {
                Text("全部客户端").tag(""); Text("ChatGPT").tag("codex"); Text("DeepSeek").tag("deepseek"); Text("DeepSeek CLI").tag("deepseek-cli")
            }.pickerStyle(.menu).disabled(device.isEmpty)
            Picker("选择项目", selection: Binding(get: { project }, set: { project = $0; session = "" })) {
                Text("全部项目").tag("")
                ForEach(projects, id: \.self) { path in
                    Text(projectNames[path] ?? URL(fileURLWithPath: path).lastPathComponent).tag(path)
                }
                if !project.isEmpty && !projects.contains(project) { Text(URL(fileURLWithPath: project).lastPathComponent + "（当前绑定）").tag(project) }
            }.pickerStyle(.menu).accessibilityIdentifier("key-project-picker").disabled(device.isEmpty || client.isEmpty || loadingOptions)
            Picker("选择会话", selection: $session) {
                Text("全部会话").tag("")
                ForEach(sessions.filter { $0["cwd"] == project && !($0["sessionId"] ?? "").isEmpty }, id: \.self) { item in
                    Text((item["title"] ?? "").isEmpty ? "未命名会话" : item["title"]!).tag(item["sessionId"]!)
                }
                if !session.isEmpty && !sessions.contains(where: { $0["sessionId"] == session }) { Text("当前绑定会话（暂不可用）").tag(session) }
            }.pickerStyle(.menu).accessibilityIdentifier("key-session-picker").disabled(project.isEmpty || loadingOptions)
            if loadingOptions { ProgressView("正在加载项目与会话…") }
            if let optionsError {
                Text(optionsError).foregroundStyle(.red)
                Button("重新加载选项") { Task { await loadOptions() } }
            } else if !device.isEmpty && !client.isEmpty && !loadingOptions && projects.isEmpty {
                Text("暂无可选项目与会话").foregroundStyle(.secondary)
            }
            if key != nil { Button("删除", role: .destructive) { deleting = true }.foregroundStyle(.red) }
            if let error = model.error { Text(error).foregroundStyle(.red) }
        }
        .nativeSettingsForm().accessibilityIdentifier("native-editor-content")
        .navigationTitle(key == nil ? "新建密钥" : "编辑密钥").navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) { Button("取消", action: done).disabled(model.saving) }
            ToolbarItem(placement: .confirmationAction) {
                Button("保存", action: save)
                    .disabled(model.saving || name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !(1...10000).contains(Int(rpm) ?? 0) || (!project.isEmpty && !project.hasPrefix("/")) || (!session.isEmpty && project.isEmpty))
            }
        }
        }.accessibilityIdentifier("native-key-editor")
        .task {
            guard !initialized else { return }; initialized = true
            name = key?.name ?? ""; rpm = String(key?.rpm ?? 10)
            device = key?.binding["device_id"] ?? ""; client = key?.binding["ai_client"] ?? ""
            project = key?.binding["project_path"] ?? ""; session = key?.binding["session_id"] ?? ""
        }
        .task(id: device + ":" + client) { await loadOptions() }
        .alert("删除此密钥？", isPresented: $deleting) {
            Button("取消", role: .cancel) {}
            Button("删除", role: .destructive) {
                guard let key else { return }
                model.perform { _ = try await model.service.request("/api/automation/access-keys/" + key.id, method: "DELETE"); await model.load("automation"); done() }
            }
        } message: { Text("删除后无法再发起新请求；已接收的任务仍会执行。") }
    }
    private var assistantForClient: String {
        switch client {
        case "deepseek": return "dsh"
        case "deepseek-cli": return "deepseek"
        default: return "codex"
        }
    }

    private func loadOptions() async {
        projects = []; projectNames = [:]; sessions = []; optionsError = nil; loadingOptions = false
        guard !device.isEmpty, !client.isEmpty else { return }
        loadingOptions = true
        let selected = device + ":" + client
        defer { if selected == device + ":" + client { loadingOptions = false } }
        do {
            let catalog = try await model.service.shared("binding-sessions", values: ["device": device, "client": client]) as? [[String: Any]] ?? []
            let projectCatalog = try await model.service.request("/" + device + "/api/projects?assistant=" + assistantForClient)["projects"] as? [[String: Any]] ?? []
            guard !Task.isCancelled, selected == device + ":" + client else { return }
            sessions = catalog.map { value in
                ["projectName": value["projectName"] as? String ?? "", "title": value["title"] as? String ?? "", "sessionId": value["sessionId"] as? String ?? "", "cwd": value["projectCwd"] as? String ?? value["cwd"] as? String ?? ""]
            }
            for item in sessions {
                if let path = item["cwd"], path.hasPrefix("/") { projectNames[path] = item["projectName"].flatMap { $0.isEmpty ? nil : $0 } ?? URL(fileURLWithPath: path).lastPathComponent }
            }
            for item in projectCatalog {
                if let path = item["cwd"] as? String, path.hasPrefix("/") { projectNames[path] = item["name"] as? String ?? URL(fileURLWithPath: path).lastPathComponent }
            }
            projects = projectNames.keys.sorted()
        } catch { if !Task.isCancelled && selected == device + ":" + client { optionsError = "候选加载失败：" + error.localizedDescription } }
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
