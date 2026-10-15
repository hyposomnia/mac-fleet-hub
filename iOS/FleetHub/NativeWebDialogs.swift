import SwiftUI

struct NativeWebDialog: Decodable, Identifiable {
    let id: String
    let title: String
    let items: [NativeDialogItem]
    let footer: [NativeDialogItem]
}

struct NativeDialogItem: Decodable, Identifiable {
    let id: String
    let kind: String
    let label: String
    var value: String?
    var placeholder: String?
    var disabled: Bool?
    var selected: Bool?
    var destructive: Bool?
    var options: [NativeDialogOption]?
}

struct NativeDialogOption: Decodable, Identifiable {
    let value: String
    let label: String
    var disabled: Bool?
    var id: String { value }
}

struct NativeWebDialogPanel: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore
    let dialog: NativeWebDialog
    var body: some View {
        VStack(spacing: 0) {
            NativeDrawerHeader(title: dialog.title) { store.dialogAction("__close") }
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    ForEach(dialog.items) { item in NativeDialogControl(store: store, item: item) }
                }.padding(18).frame(maxWidth: 800).frame(maxWidth: .infinity)
            }.scrollDismissesKeyboard(.interactively).accessibilityIdentifier("native-dialog-content")
            if !dialog.footer.isEmpty {
                HStack(spacing: 14) {
                    Spacer()
                    ForEach(dialog.footer) { item in NativeDialogControl(store: store, item: item) }
                }.padding(16).background(style.color("bg"))
            }
        }.foregroundStyle(style.color("text")).accessibilityElement(children: .contain)
            .accessibilityIdentifier("native-web-dialog-\(dialog.id)")
    }
}

private struct NativeDialogControl: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore
    let item: NativeDialogItem
    var body: some View {
        Group {
            switch item.kind {
            case "heading": Text(item.label).font(.system(size: 17, weight: .semibold))
            case "text": Text(item.label).font(.system(size: 14)).foregroundStyle(style.color("text-1"))
            case "field", "number", "secure":
                NativeDialogField(store: store, item: item)
            case "toggle": Toggle(item.label, isOn: Binding(get: { item.value == "true" }, set: { store.dialogAction(item.id, value: String($0)) }))
            case "picker":
                Picker(item.label, selection: value) {
                    ForEach(item.options ?? []) { option in Text(option.label).tag(option.value).disabled(option.disabled == true) }
                }.pickerStyle(.menu)
            default:
                Button(role: item.destructive == true ? .destructive : nil) { store.dialogAction(item.id) } label: {
                    HStack {
                        Text(item.label).multilineTextAlignment(.leading)
                        Spacer(minLength: 8)
                        if item.selected == true { Image(systemName: "checkmark") }
                    }.padding(.horizontal, 12).frame(minHeight: 44)
                        .background(item.selected == true ? style.selection : style.color("surface-1"), in: RoundedRectangle(cornerRadius: 12))
                }.buttonStyle(.plain).foregroundStyle(style.color(item.destructive == true ? "danger" : "text"))
            }
        }.disabled(item.disabled == true).accessibilityIdentifier(item.id)
            .accessibilityAddTraits(item.selected == true ? .isSelected : [])
    }
    private var value: Binding<String> {
        Binding(get: { item.value ?? "" }, set: { store.dialogAction(item.id, value: $0) })
    }
}

private struct NativeDialogField: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore
    let item: NativeDialogItem
    @State private var draft: String
    @FocusState private var focused: Bool
    init(store: FleetStore, item: NativeDialogItem) {
        self.store = store
        self.item = item
        _draft = State(initialValue: item.value ?? "")
    }
    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            Text(item.label).font(.system(size: 13)).foregroundStyle(style.color("text-1"))
            Group {
                if item.kind == "secure" { SecureField(item.label, text: input) }
                else { TextField(item.label, text: input).keyboardType(item.kind == "number" ? .numberPad : .default) }
            }.focused($focused).textInputAutocapitalization(.never).autocorrectionDisabled()
                .font(.system(size: 16)).padding(.horizontal, 12).frame(minHeight: 44)
                .background(style.color("surface-2"), in: RoundedRectangle(cornerRadius: 12))
        }
        .onChange(of: item.value) { _, value in if !focused { draft = value ?? "" } }
        .onChange(of: focused) { _, value in if !value && item.kind != "secure" { draft = item.value ?? "" } }
    }
    private var input: Binding<String> {
        Binding(get: { draft }, set: { draft = $0; store.dialogAction(item.id, value: $0) })
    }
}
