import SwiftUI

// One native grouped-list layout for settings pages and their editors.
enum NativeSettingsLayout {
    static let rowHeight: CGFloat = 32
    static let detailSpacing: CGFloat = 4
    static let iconWidth: CGFloat = 24
    static let background = Color(uiColor: .systemGroupedBackground)
}

struct NativeSettingsSectionHeader: View {
    let title: String
    var body: some View {
        Text(title).font(.subheadline.weight(.semibold)).foregroundStyle(.secondary)
            .textCase(nil).accessibilityAddTraits(.isHeader)
    }
}

struct NativeSettingsSection<Content: View>: View {
    var title = ""
    @ViewBuilder var content: () -> Content
    var body: some View {
        Section { content() } header: {
            if !title.isEmpty { NativeSettingsSectionHeader(title: title) }
        }
    }
}

private struct NativeSettingsFormStyle: ViewModifier {
    func body(content: Content) -> some View {
        content.font(.body).listStyle(.insetGrouped)
            .environment(\.defaultMinListRowHeight, 54)
            .scrollContentBackground(.hidden)
            .background(NativeSettingsLayout.background)
            .contentMargins(.top, 12, for: .scrollContent)
            .scrollDismissesKeyboard(.interactively)
            .toolbarBackground(NativeSettingsLayout.background, for: .navigationBar)
            .toolbarBackground(.visible, for: .navigationBar)
    }
}

extension View {
    func nativeSettingsForm() -> some View { modifier(NativeSettingsFormStyle()) }
}

struct NativeSettingsRowLabel: View {
    let title: String
    let icon: String
    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: icon).frame(width: NativeSettingsLayout.iconWidth)
            Text(title)
        }.frame(minHeight: NativeSettingsLayout.rowHeight)
    }
}

enum DeviceIconText {
    static func accepts(_ value: String) -> Bool {
        value.utf8.count <= 4 && value.utf8.allSatisfy { (48...57).contains($0) || (65...90).contains($0) || (97...122).contains($0) }
    }
}

struct NativeDeviceIconTextField: UIViewRepresentable {
    @Binding var text: String
    func makeUIView(context: Context) -> UITextField {
        let field = UITextField()
        field.delegate = context.coordinator
        field.addTarget(context.coordinator, action: #selector(Coordinator.changed), for: .editingChanged)
        field.placeholder = "1-4个英文字母或数字"
        field.accessibilityLabel = "文字图标"; field.accessibilityIdentifier = "device-icon-text"
        field.font = .preferredFont(forTextStyle: .body); field.adjustsFontForContentSizeCategory = true
        field.textColor = .label; field.keyboardType = .asciiCapable
        field.autocapitalizationType = .none; field.autocorrectionType = .no
        field.spellCheckingType = .no; field.returnKeyType = .done
        field.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        return field
    }
    func updateUIView(_ field: UITextField, context: Context) {
        context.coordinator.parent = self
        if field.text != text { field.text = text }
    }
    func makeCoordinator() -> Coordinator { Coordinator(parent: self) }
    final class Coordinator: NSObject, UITextFieldDelegate {
        var parent: NativeDeviceIconTextField
        init(parent: NativeDeviceIconTextField) { self.parent = parent }
        func textField(_ field: UITextField, shouldChangeCharactersIn range: NSRange, replacementString string: String) -> Bool {
            guard let range = Range(range, in: field.text ?? "") else { return false }
            return DeviceIconText.accepts((field.text ?? "").replacingCharacters(in: range, with: string))
        }
        @objc func changed(_ field: UITextField) { parent.text = field.text ?? "" }
        func textFieldShouldReturn(_ field: UITextField) -> Bool { field.resignFirstResponder(); return true }
    }
}

struct SessionSetting: Identifiable {
    let id: String
    let title: String
    let icon: String
    let detail: String
    let minimum: Int
    static let all = [
        Self(id: "chatCacheMaxSessions", title: "会话缓存数", icon: "bubble.left.and.bubble.right", detail: "保持连接的会话数量", minimum: 1),
        Self(id: "autoCloseMinutes", title: "空闲连接回收", icon: "clock.arrow.circlepath", detail: "分钟；设为 0 时永不回收", minimum: 0),
        Self(id: "desktopMaxWindows", title: "电脑端终端连接", icon: "desktopcomputer", detail: "电脑端保留的终端连接数量", minimum: 1),
        Self(id: "mobileMaxWindows", title: "手机端终端连接", icon: "iphone", detail: "手机端保留的终端连接数量", minimum: 1)
    ]
    func label(_ value: Int) -> String { id == "autoCloseMinutes" ? (value == 0 ? "永不" : "\(value) 分钟") : String(value) }
}

struct NativeNumberEditor: View {
    @Environment(\.dismiss) private var dismiss
    let field: SessionSetting
    let save: (Int) -> Void
    @State private var draft: String
    @FocusState private var focused: Bool
    init(field: SessionSetting, value: Int, save: @escaping (Int) -> Void) {
        self.field = field; self.save = save; _draft = State(initialValue: String(value))
    }
    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField(field.title, text: $draft).keyboardType(.numberPad).focused($focused)
                        .accessibilityIdentifier("setting-number")
                } footer: { Text(field.detail) }
            }.nativeSettingsForm()
            .navigationTitle(field.title).navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("完成") { if let number = number { save(number); dismiss() } }.disabled(number == nil)
                }
            }
        }
        .onAppear { focused = true }
    }
    private var number: Int? {
        guard let number = Int(draft), (field.minimum...10000).contains(number) else { return nil }
        return number
    }
}

struct NativeColorField: View {
    let title: String
    @Binding var value: String
    var body: some View {
        HStack(spacing: 12) {
            ColorPicker(title, selection: Binding(get: { Self.color(value) ?? .clear }, set: { value = Self.hex($0) }), supportsOpacity: false)
                .labelsHidden().frame(width: 44, height: 44).accessibilityLabel("\(title)调色盘").accessibilityIdentifier("palette-\(title)")
            VStack(alignment: .leading, spacing: NativeSettingsLayout.detailSpacing) {
                Text(title).font(.body)
                TextField("#RRGGBB", text: $value).font(.system(.body, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .textInputAutocapitalization(.characters).autocorrectionDisabled()
                    .accessibilityIdentifier("color-\(title)")
            }
        }.frame(minHeight: NativeSettingsLayout.rowHeight)
    }
    static func color(_ value: String) -> Color? {
        guard value.range(of: "^#[0-9a-fA-F]{6}$", options: .regularExpression) != nil,
              let rgb = UInt(value.dropFirst(), radix: 16) else { return nil }
        return Color(.sRGB, red: Double((rgb >> 16) & 255) / 255, green: Double((rgb >> 8) & 255) / 255, blue: Double(rgb & 255) / 255, opacity: 1)
    }
    static func hex(_ color: Color) -> String {
        var r: CGFloat = 0, g: CGFloat = 0, b: CGFloat = 0, a: CGFloat = 0
        UIColor(color).getRed(&r, green: &g, blue: &b, alpha: &a)
        return String(format: "#%02X%02X%02X", Int((r * 255).rounded()), Int((g * 255).rounded()), Int((b * 255).rounded()))
    }
}

enum LoginDeviceLabel {
    static func name(_ login: [String: Any]) -> String {
        let ua = (login["user_agent"] as? String ?? login["agent"] as? String ?? "").lowercased()
        let device: String
        if ua.contains("ipad") { device = "iPad" }
        else if ua.contains("iphone") { device = "iPhone" }
        else if ua.contains("android") { device = "Android" }
        else if ua.contains("macintosh") || ua.contains("mac os") { device = "Mac" }
        else if ua.contains("windows") { device = "Windows" }
        else if ua.contains("linux") { device = "Linux" }
        else { device = "登录设备" }
        if ua.contains("fleethub") || ua.contains("fleet ai") || (ua.contains("mobile/") && !ua.contains("safari")) { return device + " · Fleet AI" }
        let browser = ua.contains("edg") ? "Edge" : ua.contains("firefox") || ua.contains("fxios") ? "Firefox" : ua.contains("chrome") || ua.contains("crios") ? "Chrome" : ua.contains("safari") ? "Safari" : ""
        return browser.isEmpty ? device : device + " · " + browser
    }
}
