import SwiftUI
import CoreImage.CIFilterBuiltins

struct NativePrimaryButtonStyle: ButtonStyle {
    @Environment(\.fleetStyle) private var style
    @Environment(\.isEnabled) private var enabled
    func makeBody(configuration: Configuration) -> some View {
        configuration.label.font(.system(size: 15, weight: .semibold))
            .foregroundStyle(style.color("accent-contrast"))
            .padding(.horizontal, 14).frame(minHeight: 44)
            .background(style.color("accent"), in: RoundedRectangle(cornerRadius: 12))
            .opacity(!enabled ? 0.4 : configuration.isPressed ? 0.8 : 1)
    }
}

struct NativeTextButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var enabled
    func makeBody(configuration: Configuration) -> some View {
        configuration.label.frame(minHeight: 44).contentShape(Rectangle())
            .opacity(!enabled ? 0.4 : configuration.isPressed ? 0.6 : 1)
    }
}

struct NativeField: View {
    @Environment(\.fleetStyle) private var style
    let title: String
    @Binding var value: String
    var secure = false
    var keyboard: UIKeyboardType = .default
    @FocusState private var focused: Bool
    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            NativeFieldLabel(title: title, focused: focused) { focused = false }
            Group {
                if secure { SecureField(title, text: $value) }
                else { TextField(title, text: $value).keyboardType(keyboard) }
            }
            .focused($focused).textInputAutocapitalization(.never).autocorrectionDisabled()
            .submitLabel(.done).onSubmit { focused = false }
            .font(.body).frame(minHeight: NativeSettingsLayout.rowHeight)
        }
    }
}

struct NativeFieldLabel: View {
    @Environment(\.fleetStyle) private var style
    let title: String
    let focused: Bool
    var done: () -> Void
    var body: some View {
        HStack {
            Text(title).font(.caption).foregroundStyle(.secondary)
            Spacer(minLength: 8)
            Button("完成输入", action: done)
                .font(.caption).foregroundStyle(style.color("accent-text"))
                .opacity(focused ? 1 : 0).allowsHitTesting(focused).accessibilityHidden(!focused)
        }.frame(minHeight: 32)
    }
}

struct AccessKey: Identifiable {
    var id: String
    var name: String
    var secret: String
    var recoverable: Bool
    var rpm: Int
    var binding: [String: String]
    var created: String
    var used: String
    init(_ object: [String: Any]) {
        id = object["id"] as? String ?? ""
        name = object["name"] as? String ?? "未命名密钥"
        secret = object["key"] as? String ?? ""
        recoverable = object["recoverable"] as? Bool == true && !secret.isEmpty
        rpm = object["rpm"] as? Int ?? 10
        binding = object["binding"] as? [String: String] ?? [:]
        created = object["created_at"] as? String ?? "—"
        used = object["last_used_at"] as? String ?? "—"
    }
    var masked: String { Self.mask(secret) }
    static func mask(_ value: String) -> String {
        value.count > 20 ? String(value.prefix(12)) + "••••••" + String(value.suffix(4)) : "••••••"
    }
    static func dateLabel(_ value: String) -> String {
        let formatter = ISO8601DateFormatter()
        var date = formatter.date(from: value)
        if date == nil {
            formatter.formatOptions.insert(.withFractionalSeconds)
            date = formatter.date(from: value)
        }
        return date?.formatted(date: .numeric, time: .shortened) ?? "暂无"
    }
}

@MainActor
final class NativeSettingsModel: ObservableObject {
    let service: FleetService
    @Published var settings: [String: Any] = [:]
    @Published var palette: [String: [String: String]] = [:]
    @Published var defaults: [String: [String: String]] = [:]
    @Published var keys: [AccessKey] = []
    @Published var logins: [[String: Any]] = []
    @Published var cache: [String: Any] = [:]
    @Published var release: [String: Any] = [:]
    @Published var loading = false
    @Published var saving = false
    @Published var error: String?
    @Published var status = ""
    init(store: FleetStore) { service = FleetService(store: store) }
    private var paletteSaveTask: Task<Void, Never>?
    private var paletteRevision = 0
    private var persistedPalette: [String: [String: String]] = [:]
    private var persistedSessionSettings: [String: Int] = [:]
    private var sessionSettingsLoaded = false
    private var sessionValues: [String: Int] {
        Dictionary(uniqueKeysWithValues: SessionSetting.all.compactMap { field in
            (settings[field.id] as? Int).map { (field.id, $0) }
        })
    }
    var hasSessionChanges: Bool { sessionSettingsLoaded && sessionValues != persistedSessionSettings }
    func saveSessionSettings() async throws {
        guard hasSessionChanges else { return }
        let values = sessionValues
        _ = try await service.request("/api/settings", method: "POST", body: values)
        _ = try await service.shared("refresh")
        persistedSessionSettings = values
    }
    func savePaletteAutomatically() {
        paletteRevision += 1
        guard paletteSaveTask == nil else { return }
        paletteSaveTask = Task {
            defer { paletteSaveTask = nil }
            while !Task.isCancelled {
                let revision = paletteRevision
                try? await Task.sleep(for: .milliseconds(350))
                if revision != paletteRevision { continue }
                let draft = palette
                guard ["light", "dark"].allSatisfy({ mode in
                    ["canvas", "accent", "highlight", "text"].allSatisfy { NativeColorField.color(draft[mode]?[$0] ?? "") != nil }
                }), draft != persistedPalette else { return }
                do {
                    _ = try await service.shared("save-palette", values: ["palette": draft])
                    persistedPalette = draft; error = nil; status = "配色已保存"
                } catch {
                    if revision != paletteRevision { continue }
                    self.error = "配色保存失败：" + error.localizedDescription; return
                }
                if revision == paletteRevision { return }
            }
        }
    }
    func perform(_ operation: @escaping () async throws -> Void) {
        guard !saving else { return }
        saving = true; error = nil; status = ""
        service.store.drawerBusy = true
        Task {
            defer { saving = false; service.store.drawerBusy = false }
            do { try await operation() } catch { self.error = error.localizedDescription }
        }
    }
    func load(_ page: String) async {
        loading = true; error = nil; status = ""
        defer { loading = false }
        do {
            switch page {
            case "automation": keys = try await service.request("/api/automation/access-keys")["keys"].flatMap { $0 as? [[String: Any]] }?.map(AccessKey.init) ?? []
            case "account": logins = try await service.request("/api/auth/sessions")["sessions"] as? [[String: Any]] ?? []
            case "appearance":
                let result = try await service.shared("palette") as? [String: Any] ?? [:]
                if palette.isEmpty { palette = result["palette"] as? [String: [String: String]] ?? [:]; persistedPalette = palette }
                defaults = result["defaults"] as? [String: [String: String]] ?? [:]
            case "sessions":
                if !sessionSettingsLoaded {
                    settings = try await service.request("/api/settings")
                    persistedSessionSettings = sessionValues; sessionSettingsLoaded = true
                }
                cache = try await service.shared("cache") as? [String: Any] ?? [:]
            case "add-device": release = (try? await service.request("/enroll/client-release.json")) ?? [:]
            default: break
            }
        } catch { self.error = error.localizedDescription }
    }
}

struct NativeSettingsPanel: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore
    let expanded: Bool
    @StateObject private var model: NativeSettingsModel
    @State private var page = "account"
    @State private var keyEditor: AccessKey?
    @State private var deviceEditor: NativeSheet?
    @State private var order: [String] = []
    @State private var numberEditor: SessionSetting?
    @State private var creatingKey = false
    @State private var revokeOthers = false
    @State private var loggingOut = false
    @State private var security = ""
    @State private var credentials: [String: String] = [:]
    @State private var totpSecret = ""
    @State private var totpURI = ""
    @State private var recoveryCodes: [String] = []
    @State private var savedCodes = false
    @State private var regenerateCodes = false
    private let pages = [("add-device", "设备"), ("appearance", "外观"), ("automation", "自动化"), ("account", "账号设置"), ("sessions", "会话设置")]
    init(store: FleetStore, expanded: Bool = false) {
        self.store = store
        self.expanded = expanded
        _model = StateObject(wrappedValue: NativeSettingsModel(store: store))
        _page = State(initialValue: store.nativeSettingsPage)
    }
    var body: some View {
        GeometryReader { geometry in
        NavigationStack {
            Form {
                if model.loading { ProgressView().frame(maxWidth: .infinity) }
                if let error = model.error { Text(error).foregroundStyle(.red).accessibilityIdentifier("native-settings-error") }
                if !model.status.isEmpty { Text(model.status).foregroundStyle(style.color("accent-text")) }
                switch page {
                case "add-device": devices
                case "sessions": sessions
                case "appearance": appearance
                case "automation": automation
                default: account
                }
            }
            .nativeSettingsForm().accessibilityIdentifier("native-settings-content")
            .navigationTitle("设置").navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .principal) {
                    pageTabs.frame(width: max(120, min(geometry.size.width, 800) - 104))
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("完成", action: finish)
                        .disabled(store.drawerLocked || model.saving || (page == "sessions" && model.loading))
                        .accessibilityLabel("关闭设置")
                }
            }
        }
        }
        .tint(style.color("accent-text"))
        .accessibilityElement(children: .contain).accessibilityIdentifier("native-settings-panel")
        .interactiveDismissDisabled(model.hasSessionChanges)
        .task(id: page) { await model.load(page) }
        .onAppear { order = store.devices.map(\.id) }
        .onChange(of: store.devices.map(\.id)) { _, ids in order = ids }
        .sheet(item: $deviceEditor) { sheet in
            if case .device(let id) = sheet {
                NativeDeviceEditor(store: store, identifier: id).nativeSheetSurface(locked: store.drawerBusy)
            }
        }
        .sheet(isPresented: Binding(get: { creatingKey || keyEditor != nil }, set: { if !$0 { creatingKey = false; keyEditor = nil } })) {
            NativeAccessKeyEditor(store: store, model: model, key: keyEditor) { creatingKey = false; keyEditor = nil }
                .nativeSheetSurface(locked: model.saving)
        }
        .sheet(item: $numberEditor) { field in
            NativeNumberEditor(field: field, value: model.settings[field.id] as? Int ?? field.minimum) { value in
                model.settings[field.id] = value
            }.nativeSheetSurface(detents: [.medium])
        }
        .sheet(isPresented: Binding(get: { !security.isEmpty }, set: { if !$0 && !store.drawerLocked && !model.saving { clearSecurity() } })) {
            NavigationStack {
                Form {
                    securityContent
                    if let error = model.error { Text(error).foregroundStyle(.red) }
                }.nativeSettingsForm().accessibilityIdentifier("native-editor-content")
                .navigationTitle(securityTitle).navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("取消") { clearSecurity() }.disabled(store.drawerLocked || model.saving)
                    }
                }
            }.accessibilityIdentifier("native-security-editor").nativeSheetSurface(locked: store.drawerLocked || model.saving)
        }
        .alert("退出其他设备的登录？当前登录会保留。", isPresented: $revokeOthers) {
            Button("取消", role: .cancel) {}
            Button("退出其他设备", role: .destructive) {
                model.perform { _ = try await model.service.request("/api/auth/logout-others", method: "POST"); await model.load("account") }
            }
        }
        .alert("退出登录？", isPresented: $loggingOut) {
            Button("取消", role: .cancel) {}
            Button("退出登录", role: .destructive) { model.perform { _ = try await model.service.shared("logout") } }
        }
        .onDisappear { if !store.ready { clearSecurity() } }
    }

    private var pageTabs: some View {
        ScrollViewReader { proxy in
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 12) {
                    ForEach(pages, id: \.0) { identifier, title in
                        Button {
                            guard !store.drawerLocked && !model.saving else { return }
                            page = identifier
                        } label: {
                            Text(title.replacingOccurrences(of: "设置", with: "")).font(.subheadline.weight(page == identifier ? .semibold : .regular))
                                .fixedSize().frame(minHeight: 44)
                                .foregroundStyle(page == identifier ? style.color("accent-text") : .secondary)
                                .overlay(alignment: .bottom) {
                                    if page == identifier { Capsule().fill(style.color("accent-text")).frame(height: 2) }
                                }
                        }.buttonStyle(.plain).id(identifier).accessibilityLabel(title)
                            .accessibilityIdentifier("native-settings-\(identifier)")
                            .accessibilityAddTraits(page == identifier ? .isSelected : [])
                            .disabled(store.drawerLocked || model.saving)
                    }
                }
            }
            .accessibilityIdentifier("native-settings-tabs")
            .onChange(of: page) { _, value in withAnimation { proxy.scrollTo(value, anchor: .center) } }
        }
    }

    private func finish() {
        if model.hasSessionChanges {
            model.perform {
                try await model.saveSessionSettings()
                store.nativeSheet = nil
            }
        } else { store.nativeSheet = nil }
    }

    private var securityTitle: String {
        security == "password" ? "更改密码" : security == "totp" ? "更换 Authenticator" : "恢复码"
    }
    private func clearSecurity() {
        credentials = [:]; recoveryCodes = []; totpSecret = ""; totpURI = ""; savedCodes = false
        store.drawerLocked = false; security = ""
    }

    private var orderedDevices: [FleetDevice] {
        order.compactMap { id in store.devices.first { $0.id == id } }
    }
    private var devices: some View {
        Group {
            NativeSettingsSection(title: "设备列表") {
                ForEach(orderedDevices) { device in
                    HStack(spacing: 12) {
                        FleetDeviceIcon(device: device)
                        VStack(alignment: .leading, spacing: NativeSettingsLayout.detailSpacing) {
                            Text(device.name)
                            DeviceStatusTag(online: device.online)
                        }
                        Spacer()
                        Button("编辑") { deviceEditor = .device(device.id) }
                            .buttonStyle(.borderless).accessibilityLabel("编辑\(device.name)")
                    }.frame(minHeight: NativeSettingsLayout.rowHeight)
                }.onMove(perform: moveDevices)
                if store.devices.isEmpty { Text("暂无设备").foregroundStyle(.secondary) }
            }
            NativeSettingsSection(title: "新增设备") {
                Text("在 Mac 电脑上打开 \(store.gateway?.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/")) ?? "当前服务地址") 即可新增设备。App 不支持新增设备。")
                    .foregroundStyle(.secondary)
            }
        }
        .environment(\.editMode, .constant(.active))
    }
    private func moveDevices(from source: IndexSet, to destination: Int) {
        guard !model.saving else { return }
        let previous = order
        order.move(fromOffsets: source, toOffset: destination)
        let updated = order
        model.perform {
            do {
                _ = try await model.service.request("/api/settings", method: "PATCH", body: ["order": updated])
                _ = try await model.service.shared("refresh")
            } catch { order = previous; throw error }
        }
    }

    private var appearance: some View {
        Group {
            NativeSettingsSection(title: "显示模式") {
                HStack(spacing: 8) {
                    ForEach([("light", "浅色", "sun.max", "sun.max.fill"), ("dark", "深色", "moon", "moon.fill"), ("system", "跟随系统", "circle.lefthalf.filled", "circle.lefthalf.filled")], id: \.0) { value, title, symbol, selectedSymbol in
                        let selected = store.snapshot?.themePreference == value
                        Button { store.send("theme", values: ["preference": value]) } label: {
                            VStack(spacing: 8) {
                                Image(systemName: selected ? selectedSymbol : symbol)
                                    .frame(height: 24)
                                    .foregroundStyle(selected ? style.color("accent-text") : .primary)
                                Text(title).font(.caption)
                            }
                                .frame(maxWidth: .infinity, minHeight: 66)
                                .contentShape(Rectangle())
                        }.buttonStyle(.plain).accessibilityLabel(title)
                            .accessibilityAddTraits(selected ? .isSelected : [])
                            .disabled(store.busy)
                    }
                }
            }
            ForEach([("light", "浅色配色"), ("dark", "深色配色")], id: \.0) { mode, title in
                NativeSettingsSection(title: title) {
                    ForEach([("canvas", "页面背景"), ("accent", "主题色"), ("highlight", "反差色"), ("text", "字色")], id: \.0) { field, label in
                        NativeColorField(title: label, value: Binding(get: { model.palette[mode]?[field] ?? model.defaults[mode]?[field] ?? "" }, set: { model.palette[mode, default: [:]][field] = $0; model.savePaletteAutomatically() }))
                    }
                }
            }
            NativeSettingsSection {
                Button { model.palette = model.defaults; model.savePaletteAutomatically() } label: {
                    NativeSettingsRowLabel(title: "恢复默认", icon: "arrow.counterclockwise")
                }
            }
        }
    }

    private var sessions: some View {
        Group {
            NativeSettingsSection(title: "连接与缓存") {
                ForEach(SessionSetting.all) { field in
                    Button { numberEditor = field } label: {
                        HStack(spacing: 12) {
                            NativeSettingsRowLabel(title: field.title, icon: field.icon).foregroundStyle(.primary)
                            Spacer(minLength: 4)
                            Text(field.label(model.settings[field.id] as? Int ?? field.minimum)).foregroundStyle(.secondary)
                            Image(systemName: "chevron.right").font(.caption.weight(.semibold)).foregroundStyle(.tertiary)
                        }.frame(minHeight: 32)
                    }.accessibilityIdentifier("setting-\(field.id)")
                }
            }
            NativeSettingsSection {
                LabeledContent {
                    Text("\(model.cache["count"] as? Int ?? 0) / \(model.cache["max"] as? Int ?? 0)")
                } label: { NativeSettingsRowLabel(title: "当前缓存", icon: "tray.full") }
            }
        }
    }

    private var automation: some View {
        Group {
            NativeSettingsSection(title: "访问密钥") {
                if model.keys.isEmpty && !model.loading { Text("尚未创建访问密钥").foregroundStyle(.secondary) }
                ForEach(model.keys) { key in
                    NavigationLink {
                        NativeAccessKeyDetail(store: store, model: model, original: key) {
                            model.error = nil; keyEditor = $0
                        }
                    } label: {
                        VStack(alignment: .leading, spacing: NativeSettingsLayout.detailSpacing) {
                                Text(key.name).lineLimit(2)
                                Text("最近使用：" + AccessKey.dateLabel(key.used))
                                    .font(.caption).foregroundStyle(.secondary)
                        }.frame(minHeight: NativeSettingsLayout.rowHeight).contentShape(Rectangle())
                    }.buttonStyle(.plain).accessibilityIdentifier("access-key-\(key.id)")
                }
                Button { model.error = nil; creatingKey = true } label: {
                    NativeSettingsRowLabel(title: "新建密钥", icon: "plus")
                }
            }
            NativeSettingsSection {
                Button { store.openExternal("/automation-records.html") } label: { NativeSettingsRowLabel(title: "消息记录", icon: "clock.arrow.circlepath") }
                Button { store.openExternal("/automation-guide.html") } label: { NativeSettingsRowLabel(title: "使用文档", icon: "book") }
            }
        }
    }

    private var account: some View {
        Group {
            NativeSettingsSection {
                Text(store.snapshot?.email ?? "账号设置").frame(minHeight: NativeSettingsLayout.rowHeight)
            }
            NativeSettingsSection(title: "登录设备") {
                ForEach(Array(model.logins.enumerated()), id: \.offset) { _, login in
                    VStack(alignment: .leading, spacing: NativeSettingsLayout.detailSpacing) {
                        HStack {
                            Text(LoginDeviceLabel.name(login))
                            if login["current"] as? Bool == true {
                                Text("当前设备").font(.caption2.weight(.medium)).padding(.horizontal, 7).padding(.vertical, 3)
                                    .foregroundStyle(style.color("accent-text"))
                                    .background(style.selection, in: Capsule())
                            }
                        }
                        Text("最后登录时间：\(dateLabel(login["last_seen"] ?? login["created_at"]))")
                            .font(.caption).foregroundStyle(.secondary)
                    }.frame(minHeight: NativeSettingsLayout.rowHeight)
                }
                Button("退出其他设备的登录", role: .destructive) { revokeOthers = true }.foregroundStyle(.red)
            }.accessibilityIdentifier("account-logins-card")
            NativeSettingsSection(title: "账号安全") {
                Button { model.error = nil; credentials = [:]; security = "password" } label: {
                    securityRow("更改密码", icon: "key")
                }
                Button { model.error = nil; credentials = [:]; security = "totp" } label: {
                    securityRow("更换 Authenticator", icon: "lock.shield")
                }
                Button { model.error = nil; credentials = [:]; security = "recovery-codes" } label: {
                    securityRow("重新生成恢复码", icon: "arrow.clockwise")
                }
            }.accessibilityIdentifier("account-security-card")
            NativeSettingsSection {
                Button(role: .destructive) { loggingOut = true } label: { NativeSettingsRowLabel(title: "退出登录", icon: "rectangle.portrait.and.arrow.right").foregroundStyle(.red) }
            }
        }
    }

    private func securityRow(_ title: String, icon: String) -> some View {
        HStack {
            NativeSettingsRowLabel(title: title, icon: icon).foregroundStyle(.primary)
            Spacer(minLength: 8)
            Image(systemName: "chevron.right").font(.caption.weight(.semibold)).foregroundStyle(.tertiary)
        }.contentShape(Rectangle())
    }

    @ViewBuilder private var securityContent: some View {
        Group {
            if !recoveryCodes.isEmpty {
                NativeSettingsSection(title: "保存恢复码") {
                    Text("请离线保存。每个恢复码只能使用一次，离开后无法再次查看。")
                    Text(recoveryCodes.joined(separator: "\n")).font(.system(size: 15, design: .monospaced)).textSelection(.enabled)
                    Button("复制恢复码") { FleetService.copy(recoveryCodes.joined(separator: "\n"), sensitive: true) }
                    Toggle("我已安全保存恢复码", isOn: $savedCodes)
                    Button("完成") { clearSecurity() }
                        .buttonStyle(.automatic).disabled(!savedCodes)
                }
            } else if !totpSecret.isEmpty {
                NativeSettingsSection(title: "更换 Authenticator") {
                    if let image = qrImage { Image(uiImage: image).interpolation(.none).resizable().scaledToFit().frame(maxWidth: 220).accessibilityLabel("Authenticator 设置二维码") }
                    Text("无法扫码时手动输入：\(totpSecret)").font(.system(size: 12, design: .monospaced)).textSelection(.enabled)
                    NativeField(title: "新的 Authenticator 验证码", value: credential("code"), keyboard: .numberPad)
                    Button("验证并完成设置") { submitSecurity(confirmTOTP: true) }.buttonStyle(NativePrimaryButtonStyle()).disabled(model.saving)
                    Text("请完成验证并保存恢复码后再关闭。")
                }
            } else if !security.isEmpty {
                NativeSettingsSection(title: security == "password" ? "更改密码" : security == "totp" ? "更换 Authenticator" : "重新生成恢复码") {
                    NativeField(title: "当前密码", value: credential("current_password"), secure: true)
                    if security == "password" {
                        NativeField(title: "新密码", value: credential("password"), secure: true)
                        NativeField(title: "确认新密码", value: credential("confirm_password"), secure: true)
                    }
                    NativeField(title: "Authenticator 验证码", value: credential("code"), keyboard: .numberPad)
                    HStack {
                        Button("取消") { clearSecurity() }
                        Spacer()
                        Button("继续") { if security == "recovery-codes" { regenerateCodes = true } else { submitSecurity() } }
                            .buttonStyle(.automatic).disabled(model.saving)
                    }
                }
            }
        }
        .alert("重新生成恢复码并使旧恢复码失效？", isPresented: $regenerateCodes) {
            Button("取消", role: .cancel) {}
            Button("重新生成", role: .destructive) { submitSecurity() }
        }
    }

    private func credential(_ name: String) -> Binding<String> {
        Binding(get: { credentials[name] ?? "" }, set: { credentials[name] = $0 })
    }
    private func dateLabel(_ value: Any?) -> String {
        let date: Date?
        if let seconds = value as? NSNumber { date = seconds.doubleValue > 0 ? Date(timeIntervalSince1970: seconds.doubleValue) : nil }
        else if let text = value as? String { date = ISO8601DateFormatter().date(from: text) }
        else { date = nil }
        return date?.formatted(date: .numeric, time: .standard) ?? "—"
    }
    private var qrImage: UIImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(totpURI.utf8)
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 8, y: 8)),
              let image = CIContext().createCGImage(output, from: output.extent) else { return nil }
        return UIImage(cgImage: image)
    }
    private func submitSecurity(confirmTOTP: Bool = false) {
        let entered = credentials
        if security == "password", entered["password"] != entered["confirm_password"] { model.error = "两次输入的密码不一致。"; return }
        guard entered["code"]?.range(of: "^[0-9]{6}$", options: .regularExpression) != nil else { model.error = "请输入六位验证码。"; return }
        model.perform {
            let path = confirmTOTP ? "totp/confirm" : security == "totp" ? "totp/start" : security
            var body: [String: Any] = entered
            if security != "password" { body["password"] = entered["current_password"]; body.removeValue(forKey: "current_password") }
            if confirmTOTP { body = ["code": entered["code"] ?? ""] }
            let result = try await model.service.request("/api/auth/" + path, method: "POST", body: body)
            credentials = [:]
            if path == "totp/start", let secret = result["secret"] as? String {
                totpSecret = secret
                var components = URLComponents(); components.scheme = "otpauth"; components.host = "totp"; components.path = "/Fleet:\(store.snapshot?.email ?? "Fleet")"
                components.queryItems = [URLQueryItem(name: "secret", value: secret), URLQueryItem(name: "issuer", value: "Fleet")]
                totpURI = result["uri"] as? String ?? components.string ?? ""
                store.drawerLocked = true
            } else if let codes = result["recovery_codes"] as? [String], !codes.isEmpty {
                totpSecret = ""; totpURI = ""; recoveryCodes = codes; store.drawerLocked = true
            } else if result["login_required"] as? Bool == true {
                _ = try await model.service.shared("reauthenticate")
            } else { security = ""; model.status = "账号安全设置已更新" }
        }
    }
}
