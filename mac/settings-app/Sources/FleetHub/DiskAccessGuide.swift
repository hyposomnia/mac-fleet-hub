import AppKit
import FleetCore
import SwiftUI

struct DiskAccessApplication {
    let url: URL

    init?(url: URL) {
        guard url.isFileURL else { return nil }
        let candidate = url.standardizedFileURL
        guard candidate.lastPathComponent == "Fleet Agent.app",
              let values = try? candidate.resourceValues(forKeys: [.isDirectoryKey, .isSymbolicLinkKey]),
              values.isDirectory == true, values.isSymbolicLink != true else { return nil }
        for location in [candidate, candidate.resolvingSymlinksInPath()] {
            guard !location.deletingLastPathComponent().pathComponents.contains(where: { $0.lowercased().hasSuffix(".app") }) else { return nil }
        }
        self.url = candidate
    }

    @MainActor
    func draggingItem() -> NSDraggingItem { NSDraggingItem(pasteboardWriter: url as NSURL) }
}

@MainActor
final class ApplicationDragSourceView: NSImageView, NSDraggingSource {
    var application: DiskAccessApplication?

    override func mouseDown(with event: NSEvent) {}

    override func mouseDragged(with event: NSEvent) {
        guard isEnabled, let application else { return }
        let item = application.draggingItem()
        item.setDraggingFrame(bounds, contents: image)
        let session = beginDraggingSession(with: [item], event: event, source: self)
        session.animatesToStartingPositionsOnCancelOrFail = true
    }

    override func resetCursorRects() { addCursorRect(bounds, cursor: isEnabled ? .openHand : .arrow) }

    func draggingSession(_ session: NSDraggingSession, sourceOperationMaskFor context: NSDraggingContext) -> NSDragOperation { .copy }
}

struct DraggableApplicationIcon: NSViewRepresentable {
    let application: DiskAccessApplication
    @Environment(\.isEnabled) private var enabled

    func makeNSView(context: Context) -> ApplicationDragSourceView {
        let view = ApplicationDragSourceView()
        view.imageScaling = .scaleProportionallyUpOrDown
        view.setAccessibilityLabel("Fleet Agent.app，拖入完全磁盘访问列表")
        view.toolTip = "拖入系统设置的完全磁盘访问列表"
        return view
    }

    func updateNSView(_ view: ApplicationDragSourceView, context: Context) {
        view.application = application
        view.isEnabled = enabled
        view.image = NSWorkspace.shared.icon(forFile: application.url.path)
        view.window?.invalidateCursorRects(for: view)
    }
}

struct DiskAccessInstructions: View {
    let application: DiskAccessApplication
    @Environment(\.colorScheme) private var scheme
    private var theme: FleetTheme { FleetTheme(scheme: scheme) }

    var body: some View {
        VStack(spacing: 12) {
            DraggableApplicationIcon(application: application)
                .frame(width: 112, height: 112)
                .background(theme.surface, in: RoundedRectangle(cornerRadius: FleetTheme.cardRadius))
                .overlay(RoundedRectangle(cornerRadius: FleetTheme.cardRadius).stroke(theme.secondaryText.opacity(0.3), style: StrokeStyle(lineWidth: 1, dash: [5, 4])))
            Text("Fleet Agent.app").font(.system(size: 16, weight: .semibold))
            Text("将图标拖入「完全磁盘访问」，再打开开关。")
                .foregroundStyle(theme.secondaryText).multilineTextAlignment(.center)
            Button("在访达中显示") { NSWorkspace.shared.activateFileViewerSelecting([application.url]) }
                .buttonStyle(.link)
        }
        .font(.system(size: 13)).foregroundStyle(theme.text)
        .frame(maxWidth: .infinity)
    }
}

@MainActor
final class DiskAccessGuideController: ObservableObject {
    private let openSettings: (URL) -> Bool
    private(set) var panel: NSPanel?

    init(openSettings: @escaping (URL) -> Bool = { NSWorkspace.shared.open($0) }) { self.openSettings = openSettings }

    func show(applicationURL: URL) throws {
        guard let application = DiskAccessApplication(url: applicationURL) else {
            throw FleetError.message("请先安装后台，再开启完全磁盘访问。")
        }
        let settings = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles")!
        guard openSettings(settings) else { throw FleetError.message("无法打开系统设置，请手动进入隐私与安全性 → 完全磁盘访问。") }
        if panel == nil { panel = Self.makePanel(application: application) }
        guard let panel else { return }
        if let screen = NSScreen.main {
            let visible = screen.visibleFrame
            panel.setFrameOrigin(NSPoint(x: visible.minX + 24, y: max(visible.minY + 24, visible.midY - panel.frame.height / 2)))
        }
        panel.orderFrontRegardless()
    }

    static func makePanel(application: DiskAccessApplication) -> NSPanel {
        let panel = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 308, height: 330),
                            styleMask: [.titled, .closable, .utilityWindow, .nonactivatingPanel], backing: .buffered, defer: false)
        panel.title = "完全磁盘访问"
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.becomesKeyOnlyIfNeeded = true
        panel.isReleasedWhenClosed = false
        panel.collectionBehavior = [.moveToActiveSpace, .fullScreenAuxiliary]
        panel.contentView = NSHostingView(rootView: DiskAccessGuidePanel(application: application))
        return panel
    }

    func close() { panel?.close(); panel = nil }
}

private struct DiskAccessGuidePanel: View {
    let application: DiskAccessApplication
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        VStack(spacing: 16) {
            DiskAccessInstructions(application: application)
            Text("无需授权 Fleet Hub。完成后返回应用，重启并检查。")
                .font(.system(size: 12)).foregroundStyle(FleetTheme(scheme: scheme).secondaryText)
                .multilineTextAlignment(.center)
        }
        .padding(24).frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(FleetTheme(scheme: scheme).background)
    }
}
