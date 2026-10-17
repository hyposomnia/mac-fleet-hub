import SwiftUI
import UIKit

// Keep file operations in the Web runtime; UIKit only presents their controls.
struct NativeWebPresentation: UIViewControllerRepresentable {
    @ObservedObject var store: FleetStore
    func makeUIViewController(context: Context) -> NativeWebPresentationController { NativeWebPresentationController(store: store) }
    func updateUIViewController(_ controller: NativeWebPresentationController, context: Context) { controller.update() }
}

@MainActor
final class NativeWebPresentationController: UIViewController, @preconcurrency UIEditMenuInteractionDelegate {
    private let store: FleetStore
    private var interaction: UIEditMenuInteraction?
    private var menuButton: NativeMenuButton?
    private var current: NativeWebDialog?
    private var takingAction = false
    init(store: FleetStore) { self.store = store; super.init(nibName: nil, bundle: nil) }
    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }
    override func loadView() { view = UIView(); view.isUserInteractionEnabled = false }
    override func viewDidAppear(_ animated: Bool) { super.viewDidAppear(animated); update() }

    func update() {
        guard isViewLoaded, view.window != nil else { return }
        guard let dialog = store.webDialog, dialog.presentation == .menu, store.ready else {
            current = nil; interaction?.dismissMenu(); menuButton?.contextMenuInteraction?.dismissMenu(); menuButton?.removeFromSuperview(); menuButton = nil
            return
        }
        if current?.id == dialog.id {
            current = dialog
            interaction?.reloadVisibleMenu()
            return
        }
        interaction?.dismissMenu()
        let previousMenu = menuButton
        previousMenu?.contextMenuInteraction?.dismissMenu()
        previousMenu?.removeFromSuperview(); menuButton = nil
        current = dialog; takingAction = false
        showMenu(dialog)
    }

    private func showMenu(_ dialog: NativeWebDialog) {
        guard let web = store.webView else { return }
        if #available(iOS 17.4, *) {
            let button = NativeMenuButton(type: .custom)
            button.frame = targetRect(dialog)
            button.menu = makeMenu(dialog)
            button.showsMenuAsPrimaryAction = true
            button.accessibilityElementsHidden = true
            button.ended = { [weak self, weak button] in
                guard let self, self.menuButton === button else { return }
                if !self.takingAction, self.store.webDialog?.id == dialog.id { self.store.dialogAction("__close") }
                button?.removeFromSuperview()
                if self.menuButton === button { self.menuButton = nil }
            }
            web.addSubview(button); menuButton = button
            button.performPrimaryAction()
            return
        }
        if let interaction { web.removeInteraction(interaction) }
        let interaction = UIEditMenuInteraction(delegate: self)
        web.addInteraction(interaction); self.interaction = interaction
        let rect = targetRect(dialog)
        interaction.presentEditMenu(with: UIEditMenuConfiguration(identifier: dialog.id as NSString, sourcePoint: CGPoint(x: rect.midX, y: rect.midY)))
    }
    private func targetRect(_ dialog: NativeWebDialog) -> CGRect {
        if let anchor = dialog.anchor {
            return CGRect(x: anchor.x, y: anchor.y, width: max(1, anchor.width), height: max(1, anchor.height))
        }
        return CGRect(x: max(0, (store.webView?.bounds.width ?? 320) - 50), y: 40, width: 40, height: 40)
    }
    func editMenuInteraction(_ interaction: UIEditMenuInteraction, targetRectFor configuration: UIEditMenuConfiguration) -> CGRect {
        current.map(targetRect) ?? .zero
    }
    func editMenuInteraction(_ interaction: UIEditMenuInteraction, menuFor configuration: UIEditMenuConfiguration, suggestedActions: [UIMenuElement]) -> UIMenu? {
        guard let dialog = current, store.webDialog?.id == dialog.id else { return nil }
        return makeMenu(dialog)
    }
    private func makeMenu(_ dialog: NativeWebDialog) -> UIMenu {
        var groups: [(String, [UIMenuElement])] = []
        for item in dialog.items {
            guard ["button", "toggle"].contains(item.kind) else { continue }
            let image = item.icon.flatMap { UIImage(named: $0) } ?? UIImage(systemName: Self.symbol(item.label))
            let selected = item.kind == "toggle" ? item.value == "true" : item.selected == true
            var attributes: UIMenuElement.Attributes = []
            if item.disabled == true { attributes.insert(.disabled) }
            if item.destructive == true { attributes.insert(.destructive) }
            let action = UIAction(title: item.label, image: image, attributes: attributes, state: selected ? .on : .off) { [weak self] _ in
                guard let self, self.store.webDialog?.id == dialog.id else { return }
                self.takingAction = true
                if item.kind == "toggle" {
                    self.store.submitDialog(dialog, action: item.id, value: selected ? "false" : "true", closeAfter: true)
                } else { self.store.submitDialog(dialog, action: item.id, closeAfter: true) }
            }
            let group = item.group ?? ""
            if groups.last?.0 != group { groups.append((group, [])) }
            groups[groups.count - 1].1.append(action)
        }
        return UIMenu(children: groups.flatMap { group, actions -> [UIMenuElement] in
            group.isEmpty ? actions : [UIMenu(title: "", options: .displayInline, children: actions)]
        })
    }
    func editMenuInteraction(_ interaction: UIEditMenuInteraction, willDismissMenuFor configuration: UIEditMenuConfiguration, animator: UIEditMenuInteractionAnimating) {
        let id = configuration.identifier as? String
        animator.addCompletion { [weak self] in
            guard let self, !self.takingAction, self.store.webDialog?.id == id else { return }
            self.store.dialogAction("__close")
        }
    }
    private static func symbol(_ label: String) -> String {
        if label.contains("前往") { return "folder.badge.questionmark" }
        if label.contains("升序") { return "arrow.up" }
        if label.contains("降序") { return "arrow.down" }
        if label.contains("重命名") { return "pencil" }
        if label.contains("删除") { return "trash" }
        if label.contains("下载") { return "arrow.down.to.line" }
        if label.contains("隐藏") { return "eye.slash" }
        if label.contains("图标") { return "square.grid.2x2" }
        if label.contains("分栏") { return "rectangle.split.3x1" }
        if label.contains("列表") { return "list.bullet" }
        return "eye"
    }
}

// SwiftUI owns the alert lifecycle and its native text-field values.
struct NativeWebAlertPresenter: View {
    @ObservedObject var store: FleetStore
    @State private var dialog: NativeWebDialog?
    @State private var fields: [String: String] = [:]
    @State private var visible = false

    var body: some View {
        Color.clear.frame(width: 0, height: 0)
            .onChange(of: store.webDialog?.id, initial: true) { _, _ in
                guard let next = store.webDialog, next.presentation == .alert else {
                    if store.error == nil { visible = false }
                    return
                }
                dialog = next
                fields = Dictionary(uniqueKeysWithValues: next.items.filter {
                    ["field", "secure", "number"].contains($0.kind)
                }.map { ($0.id, $0.value ?? "") })
                // Let the native menu finish dismissing before presenting its alert.
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.35) {
                    if store.webDialog?.id == next.id { visible = true }
                }
            }
            .onChange(of: store.error) { _, error in
                if error != nil { visible = true }
            }
            .alert(store.error == nil ? dialog?.title ?? "" : "操作提示", isPresented: $visible) {
                if store.error != nil {
                    Button("关闭", role: .cancel) { store.error = nil; store.dialogAction("__close") }
                    Button("重新加载") { store.error = nil; store.webView?.reload() }
                } else if let dialog {
                    ForEach(dialog.items.filter { ["field", "secure", "number"].contains($0.kind) }) { item in
                        let value = Binding(get: { fields[item.id] ?? "" }, set: { fields[item.id] = $0 })
                        Group {
                            if item.kind == "secure" { SecureField(item.placeholder ?? item.label, text: value) }
                            else { TextField(item.placeholder ?? item.label, text: value) }
                        }
                        .accessibilityIdentifier(item.label)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                        .keyboardType(item.kind == "number" ? .numberPad : .default)
                    }
                    Button("取消", role: .cancel) { store.dialogAction("__close") }
                    ForEach(dialog.footer + dialog.items.filter { $0.kind == "button" }) { item in
                        Button(item.label, role: item.destructive == true ? .destructive : nil) {
                            store.submitDialog(dialog, action: item.id, fields: fields)
                        }.disabled(item.disabled == true || !validInput(dialog))
                    }
                }
            } message: {
                if let error = store.error { Text(error) }
                else if let dialog {
                    Text(dialog.items.filter { $0.kind == "text" }.map(\.label).joined(separator: "\n"))
                }
            }
    }

    private func validInput(_ dialog: NativeWebDialog) -> Bool {
        guard ["file-name-modal", "file-path-modal"].contains(dialog.id) else { return true }
        return fields.values.allSatisfy { value in
            let text = value.trimmingCharacters(in: .whitespacesAndNewlines)
            return dialog.id == "file-path-modal" ? text.hasPrefix("/") && !text.contains("\0") : !text.isEmpty
        }
    }
}

private final class NativeMenuButton: UIButton {
    var ended: (() -> Void)?
    override func contextMenuInteraction(_ interaction: UIContextMenuInteraction, willEndFor configuration: UIContextMenuConfiguration, animator: UIContextMenuInteractionAnimating?) {
        super.contextMenuInteraction(interaction, willEndFor: configuration, animator: animator)
        if let animator { animator.addCompletion { [weak self] in self?.ended?() } }
        else { ended?() }
    }
}
