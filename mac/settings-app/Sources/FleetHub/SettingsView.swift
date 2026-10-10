import AppKit
import FleetCore
import SwiftUI

enum SettingsPage: String, CaseIterable, Identifiable {
    case overview = "运行状态", connection = "关联账号", privacy = "磁盘权限", preferences = "设置", about = "关于"
    var id: String { rawValue }
    var symbol: String {
        switch self {
        case .overview: return "desktopcomputer"
        case .connection: return "person.crop.circle"
        case .privacy: return "internaldrive"
        case .preferences: return "gearshape"
        case .about: return "info.circle"
        }
    }
}

struct SettingsView: View {
    @ObservedObject var model: SettingsModel
    @ObservedObject var updater: AppUpdater
    let management: NativeManagement
    var preview = false
    @Environment(\.colorScheme) private var scheme
    @State private var page: SettingsPage = .overview
    @State private var operationError = ""
    @State private var operationBusy = false
    @State private var openedAttempt = ""
    @State private var confirmUninstall = false
    @State private var confirmLogout = false
    @State private var removeSettings = false
    @State private var saveDelay: Task<Void, Never>?
    @StateObject private var diskGuide = DiskAccessGuideController()
    private var theme: FleetTheme { FleetTheme(scheme: scheme) }
    private var setupAction: FleetSetupAction {
        FleetSetupAction(requiresInstallation: management.layout.requiresInstallation,
                         backgroundInstalled: FileManager.default.isExecutableFile(atPath: management.layout.agent.path))
    }
    private var installationTitle: String {
        FleetSetupAction.installationTitle(installed: FileManager.default.fileExists(atPath: "/Applications/Fleet Hub.app"))
    }
    private var validOrigin: Bool { (try? FleetSettings.validatedOrigin(model.origin)) != nil }
    private var webURL: URL? {
        guard let origin = try? FleetSettings.validatedOrigin(model.status?.binding?.origin ?? model.origin) else { return nil }
        return URL(string: origin)
    }
    private var overviewStatus: FleetOverviewStatus {
        FleetOverviewStatus(running: model.status != nil, runtime: model.status?.runtime,
                            locked: model.status?.binding?.locked == true,
                            operationError: operationError, fallback: setupAction.status)
    }
    private var statusColor: Color {
        switch overviewStatus.tone {
        case .inactive: return theme.secondaryText
        case .online: return theme.online
        case .connecting: return theme.accent
        case .warning: return theme.warning
        }
    }
    private var statusSymbol: String {
        switch overviewStatus.tone {
        case .inactive: return "pause.circle"
        case .online: return "checkmark.circle.fill"
        case .connecting: return "arrow.triangle.2.circlepath"
        case .warning: return "exclamationmark.circle.fill"
        }
    }

    var body: some View {
        HStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 28) {
                HStack(spacing: 10) {
                    FleetBrandMark().foregroundStyle(theme.accent)
                    Text("Fleet Hub").font(.system(size: 16, weight: .semibold, design: .rounded))
                }
                VStack(spacing: 8) {
                    ForEach(SettingsPage.allCases) { item in
                        FleetNavigationButton(page: item, selected: page == item) { page = item }
                    }
                }
                Spacer()
            }
            .padding(20).frame(width: 188).background(theme.navigation)
            .overlay(alignment: .trailing) { theme.secondaryText.opacity(0.12).frame(width: 0.5) }
            VStack(alignment: .leading, spacing: 16) {
                HStack {
                    Text(page.rawValue).font(.system(size: 24, weight: .semibold))
                    Spacer()
                    if page == .overview {
                        Button {
                            if let webURL, !NSWorkspace.shared.open(webURL) { operationError = "无法打开网页端" }
                        } label: { Label("打开网页端", systemImage: "arrow.up.right.square") }
                        .buttonStyle(.borderedProminent).controlSize(.large).disabled(webURL == nil)
                    }
                }
                List {
                    switch page {
                    case .overview: overview
                    case .connection: connection
                    case .privacy: privacy
                    case .preferences: preferences
                    case .about: about
                    }
                    if page != .overview, !operationError.isEmpty || !model.error.isEmpty {
                        Label(operationError.isEmpty ? model.error : operationError, systemImage: "exclamationmark.circle")
                            .foregroundStyle(theme.warning).textSelection(.enabled)
                    }
                }
                .listStyle(.plain).scrollContentBackground(.hidden)
            }
            .frame(maxWidth: .infinity, alignment: .leading).padding(28)
        }
        .font(.system(size: 13)).foregroundStyle(theme.text).background(theme.background).tint(theme.accent)
        .buttonStyle(.bordered).controlSize(.regular)
        .disabled(model.isBusy || operationBusy || updater.busy)
        .onChange(of: model.origin) { _ in queueSave() }
        .task {
            if preview {
                while !Task.isCancelled {
                    await model.refresh()
                    try? await Task.sleep(nanoseconds: 2_000_000_000)
                }
                return
            }
            if let origin = FleetSetupAction.initialOrigin(arguments: CommandLine.arguments) {
                model.origin = origin
                page = .connection
            }
            if management.layout.requiresInstallation { return }
            operationBusy = true
            await updater.recoverAfterLaunch()
            if !updater.recoveryPending {
                do {
                    if FleetSetupAction.startsAfterInstallation(arguments: CommandLine.arguments) { try await management.start() }
                    else { try await management.prepareRuntime() }
                }
                catch { operationError = error.localizedDescription }
            }
            await model.refresh()
            if FleetSetupAction.startsAfterInstallation(arguments: CommandLine.arguments), model.status != nil, !updater.recoveryPending {
                do { try await management.setAutoStart(model.autoStart) }
                catch { operationError = error.localizedDescription }
            }
            if FleetSetupAction.authorizesDiskAfterInstallation(arguments: CommandLine.arguments), !updater.recoveryPending {
                page = .privacy
                do {
                    try await diskGuide.authorize(applicationURL: management.layout.backgroundApplication) {
                        try await management.start()
                        await model.refresh()
                    }
                } catch { operationError = error.localizedDescription }
            }
            operationBusy = false
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                guard !Task.isCancelled else { break }
                await model.refresh()
                openAuthorizationBrowser()
            }
        }
        .alert("解除此设备关联？", isPresented: $confirmLogout) {
            Button("取消", role: .cancel) {}
            Button("解除关联", role: .destructive) { perform { try await management.logout(); await model.refresh() } }
        } message: { Text("撤销当前设备授权后，才能关联其他账号。") }
        .alert("卸载 Fleet Hub 和 Fleet Agent？", isPresented: $confirmUninstall) {
            Button("取消", role: .cancel) {}
            Button("卸载", role: .destructive) {
                perform { try await management.uninstall(removeSettings: removeSettings); NSApplication.shared.terminate(nil) }
            }
        } message: { Text("撤销设备授权、停止后台并卸载两个程序。你的文件与聊天会话保留。") }
    }

    private var overview: some View {
        Group {
            Section {
                HStack(spacing: 16) {
                    Image(systemName: statusSymbol)
                        .font(.system(size: 30, weight: .regular))
                        .symbolRenderingMode(.hierarchical).foregroundStyle(statusColor)
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 12) {
                        Text(overviewStatus.title)
                            .font(.system(size: 22, weight: .semibold, design: .rounded))
                            .foregroundStyle(overviewStatus.tone == .warning ? theme.warning : theme.text)
                            .textSelection(.enabled)
                        if let current = model.status {
                            HStack(spacing: 12) {
                                Text("版本 \(current.version)")
                                Text("·")
                                Text("进程 \(String(current.pid))").monospacedDigit()
                            }
                            .font(.system(size: 12)).foregroundStyle(theme.secondaryText).textSelection(.enabled)
                        }
                    }
                    Spacer()
                    if model.status == nil {
                        Button(setupAction == .installApplication ? installationTitle : setupAction.title) {
                            if setupAction == .installApplication { installApplication() }
                            else { perform { try await management.start(); await model.refresh(); try await management.setAutoStart(model.autoStart) } }
                        }
                        .buttonStyle(.borderedProminent)
                    } else {
                        Button { perform { try await management.restart(); await model.refresh() } } label: {
                            Label("重启", systemImage: "arrow.clockwise").foregroundStyle(theme.secondaryText)
                        }
                        Button { perform { try await management.stop(); await model.refresh() } } label: {
                            Label("停止", systemImage: "stop.fill").foregroundStyle(theme.secondaryText)
                        }
                    }
                }
                .padding(.vertical, 20)
            }
            Section {
                if let binding = model.status?.binding {
                    row("账号", binding.ownerEmail, symbol: "person.crop.circle")
                    row("设备名称", binding.displayName, symbol: "desktopcomputer")
                } else {
                    Button { page = .connection } label: {
                        HStack {
                            Text("尚未关联账号")
                            Spacer()
                            Text("关联账号")
                            Image(systemName: "chevron.right")
                        }
                        .frame(maxWidth: .infinity).padding(.vertical, 8).contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .listRowBackground(Color.clear)
    }

    private var connection: some View {
        Group {
            Section {
                if let binding = model.status?.binding {
                    row("服务器地址", binding.origin, symbol: "network")
                } else {
                    LabeledContent("服务器地址") {
                        HStack(spacing: 12) {
                            TextField("https://fleet.example.com", text: $model.origin)
                                .textFieldStyle(.roundedBorder)
                            Button("打开网页授权") {
                                if management.layout.requiresInstallation { installApplication() }
                                else { saveSettings(authorize: true) }
                            }
                            .buttonStyle(.borderedProminent).fixedSize()
                            .disabled(!validOrigin || model.isSaving || model.status?.pairing?.phase == "joining")
                            if model.isSaving { ProgressView().controlSize(.small) }
                        }
                    }
                }
                if management.layout.requiresInstallation {
                    Text("先\(installationTitle)，再继续网页授权。").foregroundStyle(theme.secondaryText)
                }
            }
            if let pairing = model.status?.pairing, pairing.phase != "idle" {
                Section {
                    HStack {
                        if pairing.isActive { ProgressView().controlSize(.small) }
                        Text(pairing.label)
                        Spacer()
                        if pairing.isActive && pairing.phase != "joining" {
                            Button { Task { await model.cancelPairing() } } label: { Image(systemName: "xmark") }
                                .accessibilityLabel("取消授权")
                        }
                    }
                    if let owner = pairing.ownerEmail { row("账号", owner) }
                    if pairing.phase == "awaiting_confirmation" {
                        Button("确认接入") { Task { await model.confirmPairing() } }.buttonStyle(.borderedProminent)
                    }
                    if pairing.needsCleanup == true && model.status?.binding == nil {
                        Button("解除未完成关联") { confirmLogout = true }.foregroundStyle(theme.danger)
                    }
                }
            }
            if let binding = model.status?.binding {
                Section {
                    row("账号", binding.ownerEmail, symbol: "person.crop.circle")
                    row("设备名称", binding.displayName, symbol: "desktopcomputer")
                    Button("解除关联") { confirmLogout = true }.buttonStyle(.link).foregroundStyle(theme.danger)
                }
            }
        }
        .listRowBackground(Color.clear)
        .disabled(updater.sessionActive)
    }

    private var privacy: some View {
        Group {
            Section {
                HStack {
                    Label("完全磁盘访问", systemImage: "internaldrive")
                        .fontWeight(.medium).foregroundStyle(theme.iconColor(for: .privacy))
                    Spacer()
                    Label(model.diskState.label, systemImage: model.diskState == .verified ? "checkmark.circle.fill" : "exclamationmark.circle")
                        .foregroundStyle(model.diskState == .verified ? theme.online : theme.warning)
                    Button { Task { await model.recheckDisk() } } label: { Image(systemName: "arrow.clockwise") }
                        .accessibilityLabel("重新检查后台权限")
                        .help("由实际后台只读检查；文件权限与 ACL 仍然生效。").disabled(model.status == nil)
                }
            }
            if let application = DiskAccessApplication(url: management.layout.backgroundApplication), !management.layout.requiresInstallation {
                Section { DiskAccessInstructions(application: application) }
            }
            Section {
                HStack {
                    Button("授权磁盘访问") {
                        if management.layout.requiresInstallation { installApplication(authorizeDisk: true) }
                        else {
                            perform {
                                try await diskGuide.authorize(applicationURL: management.layout.backgroundApplication) {
                                    try await management.start()
                                    await model.refresh()
                                }
                            }
                        }
                    }
                    .buttonStyle(.borderedProminent).controlSize(.large)
                    if model.diskState != .verified && model.status != nil {
                        Button("重启并检查") { perform { try await management.restart(); await model.refresh(); await model.recheckDisk() } }
                    }
                }
                .frame(maxWidth: .infinity)
                if let evidence = model.status?.diskAccess, !(evidence.deniedTargets ?? []).isEmpty {
                    DisclosureGroup("检测详情") {
                        Text((evidence.deniedTargets ?? []).joined(separator: "\n"))
                            .font(.system(size: 11, design: .monospaced)).textSelection(.enabled)
                    }
                    .foregroundStyle(theme.secondaryText)
                }
            }
        }
        .listRowBackground(Color.clear)
    }

    private var preferences: some View {
        Section {
            Toggle(isOn: autoStartBinding) {
                Label {
                    Text("登录后启动后台").foregroundStyle(theme.text)
                } icon: {
                    Image(systemName: "power.circle").foregroundStyle(theme.iconColor(for: .preferences))
                }
            }
                .toggleStyle(.switch).disabled(management.layout.requiresInstallation || (preview && model.status == nil))
        }
        .listRowBackground(Color.clear)
    }

    private var about: some View {
        Group {
            Section {
                VStack(spacing: 12) {
                    Image(nsImage: NSWorkspace.shared.icon(forFile: management.layout.application.path))
                        .resizable().scaledToFit().frame(width: 96, height: 96).accessibilityHidden(true)
                    Text("Fleet Hub").font(.system(size: 22, weight: .semibold))
                    Text(Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "开发版本")
                        .foregroundStyle(theme.secondaryText)
                    if !management.layout.requiresInstallation {
                        Button("检查更新") { Task { await updater.check() } }.disabled(updater.sessionActive)
                    }
                }
                .frame(maxWidth: .infinity).padding(.vertical, 28)
                if !updater.message.isEmpty { Text(updater.message).foregroundStyle(theme.secondaryText).textSelection(.enabled) }
                if updater.sessionActive { Button("继续安装升级") { Task { await updater.continueInstallation() } } }
            }
            Section {
                if management.layout.requiresInstallation {
                    Button(installationTitle) { installApplication() }.buttonStyle(.borderedProminent)
                } else {
                    DisclosureGroup("卸载") {
                        Toggle("同时移除本机设置", isOn: $removeSettings)
                        Button("卸载 Fleet Hub 和 Fleet Agent") { confirmUninstall = true }
                            .buttonStyle(.link).foregroundStyle(theme.warning).disabled(updater.sessionActive)
                    }
                    .foregroundStyle(theme.secondaryText)
                }
            }
        }
        .listRowBackground(Color.clear)
    }

    private func row(_ title: String, _ value: String, symbol: String? = nil) -> some View {
        LabeledContent {
            Text(value).foregroundStyle(theme.text).textSelection(.enabled)
        } label: {
            if let symbol {
                Label {
                    Text(title)
                } icon: {
                    Image(systemName: symbol).symbolRenderingMode(.hierarchical).foregroundStyle(theme.accent)
                }
            }
            else { Text(title) }
        }
        .foregroundStyle(theme.secondaryText).padding(.vertical, 10)
        .listRowSeparator(.visible)
    }
    private func installApplication(authorizeDisk: Bool = false) {
        perform {
            let installed: URL
            if FileManager.default.fileExists(atPath: "/Applications/Fleet Hub.app") { installed = try await management.installedApplication() }
            else { installed = try await management.install() }
            let configuration = NSWorkspace.OpenConfiguration()
            configuration.arguments = ["--fleet-install-and-start"]
            if authorizeDisk { configuration.arguments.append("--fleet-authorize-disk") }
            if let origin = try? FleetSettings.validatedOrigin(model.origin) { configuration.arguments.append("--fleet-origin=\(origin)") }
            configuration.createsNewApplicationInstance = true
            _ = try await NSWorkspace.shared.openApplication(at: installed, configuration: configuration)
            NSApplication.shared.terminate(nil)
        }
    }
    private func saveSettings(authorize: Bool) {
        saveDelay?.cancel()
        perform {
            if model.status == nil { try await management.start(); await model.refresh() }
            guard await model.save() else { return }
            if authorize { await model.beginPairing() }
        }
    }
    private var autoStartBinding: Binding<Bool> {
        Binding(get: { model.autoStart }, set: { value in
            model.autoStart = value
            queueSave(allowPreview: true)
        })
    }
    private func queueSave(allowPreview: Bool = false) {
        saveDelay?.cancel()
        guard (!preview || allowPreview), !management.layout.requiresInstallation, !updater.sessionActive,
              model.origin.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || validOrigin else { return }
        saveDelay = Task {
            do { try await Task.sleep(nanoseconds: 600_000_000) } catch { return }
            guard !Task.isCancelled else { return }
            if model.status == nil { saveSettings(authorize: false) }
            else { await model.save() }
        }
    }
    private func perform(_ action: @escaping @MainActor () async throws -> Void) {
        guard !preview else { return }
        guard !updater.sessionActive else { operationError = "请先完成或取消升级"; return }
        guard !operationBusy else { return }
        operationBusy = true
        operationError = ""
        Task {
            defer { operationBusy = false }
            do { try await action() } catch { operationError = error.localizedDescription }
        }
    }
    private func openAuthorizationBrowser() {
        guard let pairing = model.status?.pairing, pairing.phase == "browser", pairing.attempt != openedAttempt, let link = pairing.verificationURL else { return }
        if NSWorkspace.shared.open(link) { openedAttempt = pairing.attempt }
    }
}
