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

struct NativeDrawerHeader: View {
    @Environment(\.fleetStyle) private var style
    let title: String
    var locked = false
    let close: () -> Void
    var body: some View {
        HStack {
            Text(title).font(.system(size: 18, weight: .semibold)).lineLimit(2)
            Spacer(minLength: 8)
            Button(action: close) { Image(systemName: "xmark") }
                .buttonStyle(TitaniumIconButtonStyle()).disabled(locked)
                .accessibilityLabel("关闭\(title)")
        }.padding(.horizontal, 18).frame(minHeight: 56).foregroundStyle(style.color("text"))
    }
}

struct NativeCard<Content: View>: View {
    @Environment(\.fleetStyle) private var style
    var title: String = ""
    @ViewBuilder var content: () -> Content
    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            if !title.isEmpty { Text(title).font(.system(size: 17, weight: .semibold)) }
            content()
        }.frame(maxWidth: .infinity, alignment: .leading).padding(16)
            .background(style.color("surface-1"), in: RoundedRectangle(cornerRadius: 16))
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
            .font(.system(size: 16)).padding(.horizontal, 12).frame(minHeight: 44)
            .background(style.color("surface-2"), in: RoundedRectangle(cornerRadius: 12))
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
            Text(title).font(.system(size: 13)).foregroundStyle(style.color("text-1"))
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
                palette = result["palette"] as? [String: [String: String]] ?? [:]
                defaults = result["defaults"] as? [String: [String: String]] ?? [:]
            case "sessions":
                settings = try await service.request("/api/settings")
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
        VStack(spacing: 0) {
                NativeDrawerHeader(title: creatingKey || keyEditor != nil ? "访问密钥" : "设置", locked: store.drawerLocked || model.saving) { store.nativeSheet = nil }
                if creatingKey || keyEditor != nil { settingsContent }
                else if expanded {
                    HStack(alignment: .top, spacing: 0) {
                        VStack(spacing: 6) { pageButtons; Spacer() }.padding(12).frame(width: 172)
                            .background(style.color("surface-1"))
                        settingsContent
                    }
                } else {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 4) { pageButtons }.padding(.horizontal, 16)
                    }.fixedSize(horizontal: false, vertical: true)
                    settingsContent
                }
        }
        .font(.system(size: 15)).foregroundStyle(style.color("text"))
        .buttonStyle(NativeTextButtonStyle())
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("native-settings-panel")
        .task(id: page) { await model.load(page) }
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
        .alert("重新生成恢复码并使旧恢复码失效？", isPresented: $regenerateCodes) {
            Button("取消", role: .cancel) {}
            Button("重新生成", role: .destructive) { submitSecurity() }
        }
        .onDisappear { credentials = [:]; recoveryCodes = []; totpSecret = ""; totpURI = "" }
    }

    private var pageButtons: some View {
        ForEach(pages, id: \.0) { identifier, title in
            Button { security = ""; page = identifier } label: {
                Text(title).font(.system(size: 15, weight: page == identifier ? .semibold : .regular))
                    .padding(.horizontal, 12).frame(minHeight: 44)
                    .frame(maxWidth: .infinity)
                    .foregroundStyle(style.color(page == identifier ? "accent-text" : "text-1"))
                    .background(page == identifier ? style.selection : .clear, in: RoundedRectangle(cornerRadius: 12))
            }.buttonStyle(.plain).disabled(store.drawerLocked || model.saving)
                .accessibilityIdentifier("native-settings-\(identifier)")
                .accessibilityAddTraits(page == identifier ? .isSelected : [])
        }
    }

    private var settingsContent: some View {
        ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    if model.loading { ProgressView("正在加载…").frame(maxWidth: .infinity) }
                    if let error = model.error { Text(error).foregroundStyle(style.color("danger")).accessibilityIdentifier("native-settings-error") }
                    if !model.status.isEmpty { Text(model.status).foregroundStyle(style.color("accent-text")) }
                    if creatingKey || keyEditor != nil {
                        NativeAccessKeyEditor(store: store, model: model, key: keyEditor) { creatingKey = false; keyEditor = nil }
                    } else {
                        switch page {
                        case "add-device": devices
                        case "appearance": appearance
                        case "automation": automation
                        case "sessions": sessions
                        default: account
                        }
                    }
                }.padding(16).padding(.bottom, keyboardInset).frame(maxWidth: 800).frame(maxWidth: .infinity)
        }.scrollDismissesKeyboard(.interactively).accessibilityIdentifier("native-settings-content")
    }

    private var keyboardInset: CGFloat {
        guard let top = store.keyboardTop, let window = store.webView?.window else { return 0 }
        return max(0, window.bounds.maxY - top)
    }

    private var devices: some View {
        VStack(spacing: 16) {
            NativeCard(title: "设备列表") {
                ForEach(store.devices) { device in
                    HStack {
                        FleetDeviceIcon(device: device)
                        VStack(alignment: .leading, spacing: 4) {
                            Text(device.name).fontWeight(.medium)
                            Text(device.online ? "在线" : "离线").font(.caption).foregroundStyle(style.color("text-2"))
                        }
                        Spacer()
                        Button("编辑") { store.nativeSheet = .device(device.id) }
                    }.frame(minHeight: 44)
                }
                if store.devices.isEmpty { Text("暂无设备") }
            }
            NativeCard(title: "新增设备") {
                Text("在需要访问的 Mac 上安装 Fleet Agent Settings；iPhone 无需安装电脑端 Agent。")
                if let gateway = store.gateway, model.release["installation_protocol"] as? Int == 1 {
                    let command = "curl -fsSL '\(gateway.appendingPathComponent("install.sh").absoluteString)' | bash"
                    Text(command).font(.system(size: 12, design: .monospaced)).textSelection(.enabled)
                    Button("复制安装命令") { FleetService.copy(command); model.status = "安装命令已复制" }
                } else {
                    Text("当前版本暂不支持命令安装；如已发布安装包，可使用下方下载入口。").foregroundStyle(style.color("text-2"))
                }
                if let assets = model.release["assets"] as? [String: Any], let dmg = assets["dmg"] as? [String: Any], let path = dmg["path"] as? String {
                    Button("在浏览器下载安装包") { store.openExternal(path) }
                }
                if let gateway = store.gateway {
                    Button("复制服务器地址") { FleetService.copy(gateway.absoluteString); model.status = "地址已复制" }
                }
                Text("打开 Mac 应用后关联账号，在浏览器登录并授权连接。磁盘权限需在该 Mac 的系统设置中授予。")
                    .foregroundStyle(style.color("text-2"))
            }
        }
    }

    private var appearance: some View {
        VStack(spacing: 16) {
            NativeCard(title: "显示模式") {
                HStack(spacing: 8) {
                    ForEach([("light", "浅色", "sun.max"), ("dark", "深色", "moon"), ("system", "跟随系统", "circle.lefthalf.filled")], id: \.0) { value, title, symbol in
                        Button { store.send("theme", values: ["preference": value]) } label: {
                            VStack(spacing: 8) { Image(systemName: symbol); Text(title).font(.caption) }
                                .frame(maxWidth: .infinity, minHeight: 66)
                                .background(store.snapshot?.themePreference == value ? style.selection : .clear, in: RoundedRectangle(cornerRadius: 12))
                        }.buttonStyle(.plain).accessibilityLabel(title).disabled(store.busy)
                    }
                }
            }
            ForEach([("light", "浅色配色"), ("dark", "深色配色")], id: \.0) { mode, title in
                NativeCard(title: title) {
                    ForEach([("canvas", "页面背景"), ("accent", "主题色"), ("highlight", "反差色"), ("text", "字色")], id: \.0) { field, label in
                        NativeField(title: label, value: Binding(get: { model.palette[mode]?[field] ?? model.defaults[mode]?[field] ?? "" }, set: { model.palette[mode, default: [:]][field] = $0 }))
                    }
                }
            }
            HStack {
                Button("恢复默认") { model.palette = model.defaults }
                Spacer()
                Button("保存配色") {
                    model.perform {
                        _ = try await model.service.shared("save-palette", values: ["palette": model.palette])
                        model.status = "配色已保存"
                    }
                }.buttonStyle(NativePrimaryButtonStyle()).disabled(model.saving || !validPalette)
            }
        }
    }

    private var validPalette: Bool {
        ["light", "dark"].allSatisfy { mode in ["canvas", "accent", "highlight", "text"].allSatisfy { field in
            model.palette[mode]?[field]?.range(of: "^#[0-9a-fA-F]{6}$", options: .regularExpression) != nil
        } }
    }

    private var sessions: some View {
        NativeCard(title: "会话设置") {
            ForEach([("chatCacheMaxSessions", "保持连接的会话缓存数", 1), ("autoCloseMinutes", "空闲连接回收（分钟，0 为永不）", 0), ("desktopMaxWindows", "电脑端保留的终端连接", 1), ("mobileMaxWindows", "手机端保留的终端连接", 1)], id: \.0) { key, label, minimum in
                Stepper(value: Binding(get: { model.settings[key] as? Int ?? minimum }, set: { model.settings[key] = $0 }), in: minimum...10000) {
                    Text("\(label)：\(model.settings[key] as? Int ?? minimum)")
                }
            }
            Text("当前缓存：\(model.cache["count"] as? Int ?? 0) / \(model.cache["max"] as? Int ?? 0)")
            Text("回收不活跃连接不会丢失草稿或结束正在执行的任务。")
                .font(.caption).foregroundStyle(style.color("text-2"))
            Button("保存会话设置") {
                model.perform {
                    let names = ["chatCacheMaxSessions", "autoCloseMinutes", "desktopMaxWindows", "mobileMaxWindows"]
                    _ = try await model.service.request("/api/settings", method: "POST", body: model.settings.filter { names.contains($0.key) })
                    _ = try await model.service.shared("refresh"); model.status = "会话设置已保存"
                }
            }.buttonStyle(NativePrimaryButtonStyle()).disabled(model.saving || model.loading)
        }
    }

    private var automation: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                Text("访问密钥").font(.system(size: 18, weight: .semibold))
                Spacer()
                Button("新建密钥") { creatingKey = true }.buttonStyle(NativePrimaryButtonStyle())
            }
            if model.keys.isEmpty && !model.loading { Text("尚未创建访问密钥").foregroundStyle(style.color("text-2")) }
            ForEach(model.keys) { key in
                NativeCard {
                    HStack(alignment: .firstTextBaseline) {
                        Text(key.name).fontWeight(.semibold).lineLimit(2)
                        Spacer(minLength: 8)
                        Button("编辑") { keyEditor = key }.fixedSize()
                    }
                    ViewThatFits(in: .horizontal) {
                        HStack(spacing: 10) { keyValue(key); keyCopyActions(key) }
                        VStack(alignment: .leading, spacing: 8) { keyValue(key); HStack { keyCopyActions(key) } }
                    }
                    let binding = key.binding
                    Text("范围：" + [binding["device_id"].map { id in store.devices.first(where: { $0.id == id })?.name ?? id } ?? "全部设备", binding["ai_client"] ?? "全部客户端", binding["project_path"] ?? "全部项目", binding["session_id"] ?? "全部会话"].joined(separator: " · "))
                        .font(.caption).foregroundStyle(style.color("text-2"))
                    Text("RPM：\(key.rpm)　创建：\(key.created)").font(.caption).foregroundStyle(style.color("text-2"))
                    Text("最近使用：\(key.used)").font(.caption).foregroundStyle(style.color("text-2"))
                }
            }
            Text("未限定范围的密钥可向你的全部设备会话发送消息，请妥善保管。")
                .font(.caption).foregroundStyle(style.color("wait"))
            HStack {
                Button("消息记录") { store.openExternal("/automation-records.html") }
                Button("使用文档") { store.openExternal("/automation-guide.html") }
            }
        }
    }

    private func keyValue(_ key: AccessKey) -> some View {
        Text(key.recoverable ? key.masked : "旧版密钥不可恢复").font(.system(size: 12, design: .monospaced)).lineLimit(1)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
    private func keyCopyActions(_ key: AccessKey) -> some View {
        Group {
            Button("复制") { FleetService.copy(key.secret, sensitive: true); model.status = "密钥已复制（两分钟后从剪贴板清除）" }.fixedSize()
            Button("复制请求示例") {
                model.perform {
                    let result = try await model.service.shared("access-key-request", values: ["key": ["key": key.secret, "name": key.name, "binding": key.binding]])
                    if let value = result as? String { FleetService.copy(value, sensitive: true); model.status = "请求示例已复制" }
                }
            }.fixedSize()
        }.font(.caption).disabled(!key.recoverable || model.saving)
    }

    private var account: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(store.snapshot?.email ?? "账号设置").font(.system(size: 17, weight: .semibold))
            if !recoveryCodes.isEmpty {
                NativeCard(title: "保存恢复码") {
                    Text("请离线保存。每个恢复码只能使用一次，离开后无法再次查看。")
                    Text(recoveryCodes.joined(separator: "\n")).font(.system(size: 15, design: .monospaced)).textSelection(.enabled)
                    Button("复制恢复码") { FleetService.copy(recoveryCodes.joined(separator: "\n"), sensitive: true) }
                    Toggle("我已安全保存恢复码", isOn: $savedCodes)
                    Button("继续") { recoveryCodes = []; savedCodes = false; store.drawerLocked = false; security = "" }
                        .buttonStyle(NativePrimaryButtonStyle()).disabled(!savedCodes)
                }
            } else if !totpSecret.isEmpty {
                NativeCard(title: "更换 Authenticator") {
                    if let image = qrImage { Image(uiImage: image).interpolation(.none).resizable().scaledToFit().frame(maxWidth: 220).accessibilityLabel("Authenticator 设置二维码") }
                    Text("无法扫码时手动输入：\(totpSecret)").font(.system(size: 12, design: .monospaced)).textSelection(.enabled)
                    NativeField(title: "新的 Authenticator 验证码", value: credential("code"), keyboard: .numberPad)
                    Button("验证并完成设置") { submitSecurity(confirmTOTP: true) }.buttonStyle(NativePrimaryButtonStyle()).disabled(model.saving)
                    Text("请完成验证并保存恢复码后再关闭。")
                }
            } else if !security.isEmpty {
                NativeCard(title: security == "password" ? "更改密码" : security == "totp" ? "更换 Authenticator" : "重新生成恢复码") {
                    NativeField(title: "当前密码", value: credential("current_password"), secure: true)
                    if security == "password" {
                        NativeField(title: "新密码", value: credential("password"), secure: true)
                        NativeField(title: "确认新密码", value: credential("confirm_password"), secure: true)
                    }
                    NativeField(title: "Authenticator 验证码", value: credential("code"), keyboard: .numberPad)
                    HStack {
                        Button("取消") { security = ""; credentials = [:] }
                        Spacer()
                        Button("继续") { if security == "recovery-codes" { regenerateCodes = true } else { submitSecurity() } }
                            .buttonStyle(NativePrimaryButtonStyle()).disabled(model.saving)
                    }
                }
            } else {
                NativeCard(title: "登录设备") {
                    ForEach(Array(model.logins.enumerated()), id: \.offset) { _, login in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(login["current"] as? Bool == true ? "此设备" : "其他设备").fontWeight(.medium)
                            if let agent = login["user_agent"] as? String ?? login["agent"] as? String, !agent.isEmpty { Text(agent).font(.caption) }
                            if let address = login["ip"] as? String, !address.isEmpty { Text(address).font(.caption).foregroundStyle(style.color("text-2")) }
                            ForEach([("created_at", "创建时间"), ("last_seen", "最近使用"), ("expires_at", "到期时间")], id: \.0) { key, label in
                                if login[key] != nil { Text("\(label)：\(dateLabel(login[key]))").font(.caption).foregroundStyle(style.color("text-2")) }
                            }
                        }
                    }
                    Button("退出其他设备的登录", role: .destructive) { revokeOthers = true }
                }
                NativeCard(title: "账号安全") {
                    Button("更改密码") { security = "password"; credentials = [:] }
                    Button("更换 Authenticator") { security = "totp"; credentials = [:] }
                    Button("重新生成恢复码") { security = "recovery-codes"; credentials = [:] }
                    if store.snapshot?.admin == true { Button("在浏览器打开管理") { store.openExternal("/admin") } }
                    Button("退出登录", role: .destructive) { loggingOut = true }
                }
            }
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
