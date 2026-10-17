import AppKit
import FleetCore
import Network
import SwiftUI

enum SettingsPage: String, CaseIterable, Identifiable {
    case overview = "运行状态", connection = "关联账号", privacy = "磁盘权限", preferences = "设置", about = "关于"
    var id: String { rawValue }
    var symbol: String {
        switch self {
        case .overview: return "display"
        case .connection: return "person"
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
    @State private var page: SettingsPage = CommandLine.arguments.contains("--fleet-disk-guide") ? .privacy : .overview
    @State private var startupAttempt = 0
    @StateObject private var startup = SettingsStartup()
    @State private var operationError = ""
    @State private var operationBusy = false
    @State private var openedAttempt = ""
    @State private var confirmUninstall = false
    @State private var confirmReplacement = false
    @State private var replacementIncludesLegacy = false
    @State private var replacementAction: (@MainActor () async throws -> Void)?
    @State private var confirmLogout = false
    @State private var previewRestriction = false
    @State private var removeSettings = false
    @State private var saveDelay: Task<Void, Never>?
    @StateObject private var diskGuide = DiskAccessGuideController()
    private var theme: FleetTheme { FleetTheme(scheme: scheme) }
    private var setupAction: FleetSetupAction {
        FleetSetupAction(requiresInstallation: management.layout.requiresInstallation,
                         backgroundInstalled: FileManager.default.isExecutableFile(atPath: management.layout.agent.path))
    }
    private var installationTitle: String {
        FleetSetupAction.installationTitle(installed: FileManager.default.fileExists(atPath: RuntimeLayout.installedApplication().path))
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

    private var diskGuideLaunch: Bool { CommandLine.arguments.contains("--fleet-disk-guide") }

    var body: some View {
        Group {
            if preview || management.layout.requiresInstallation || diskGuideLaunch || startup.state.isReady {
                settingsContents
                    .disabled(model.isBusy || operationBusy || updater.busy)
                    .task { await observeSettings() }
                    .task {
                        guard !preview, !management.layout.requiresInstallation, startup.state.isReady else { return }
                        await updater.checkAvailability()
                    }
            } else {
                SettingsStartupScreen(state: startup.state, applicationName: RuntimeLayout.applicationName, retry: {
                    if startup.requestRetry() { startupAttempt += 1 }
                }, inspect: {
                    operationError = startup.state.error ?? ""
                    startup.showSettingsAfterFailure()
                })
            }
        }
        .font(.system(size: 13)).foregroundStyle(theme.text).background(theme.background).tint(theme.accent)
        .buttonStyle(.bordered).controlSize(.regular)
        .onChange(of: model.origin) { _ in
            if preview || startup.state.isReady { queueSave() }
        }
        .onReceive(NSWorkspace.shared.notificationCenter.publisher(for: NSWorkspace.didWakeNotification)) { _ in
            Task { await retryConnectionIfNeeded() }
        }
        .task(id: startupAttempt) { await prepareSettings() }
        .alert(FleetSetupAction.replacementTitle, isPresented: $confirmReplacement) {
            Button("取消", role: .cancel) { replacementAction = nil }
            Button("卸载旧版并安装新版") {
                let action = replacementAction
                replacementAction = nil
                if let action { perform { try await action() } }
            }
        } message: { Text(FleetSetupAction.replacementMessage(legacy: replacementIncludesLegacy)) }
        .alert("解除此设备关联？", isPresented: $confirmLogout) {
            Button("取消", role: .cancel) {}
            Button("解除关联", role: .destructive) { perform { try await management.logout(); await model.refresh() } }
        } message: { Text("撤销当前设备授权后，才能关联其他账号。") }
        .alert("卸载 Fleet Agent Settings 和 Fleet Agent？", isPresented: $confirmUninstall) {
            Button("取消", role: .cancel) {}
            Button("卸载", role: .destructive) {
                perform { try await management.uninstall(removeSettings: removeSettings); NSApplication.shared.terminate(nil) }
            }
        } message: {
            Text("撤销设备授权、停止后台并卸载两个程序。" +
                 (removeSettings ? "本机 Fleet 配置也会删除。" : "本机 Fleet 配置保留。") +
                 "\n\n" + CodexSharedRuntime.removalInstructions +
                 "\n\n聊天会话由 Codex / Claude 保存，独立于 Fleet；卸载不会删除这些会话或你的个人文件。")
        }
        .alert("界面预览", isPresented: $previewRestriction) {
            Button("知道了", role: .cancel) {}
        } message: { Text("请在已安装的设置程序中执行此操作。") }
    }

    private var settingsContents: some View {
        HStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 28) {
                HStack(spacing: 10) {
                    FleetBrandMark().foregroundStyle(theme.accent)
                    Text(RuntimeLayout.applicationName)
                        .font(.system(size: 15, weight: .semibold, design: .rounded)).fixedSize(horizontal: false, vertical: true)
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
                    if let version = updater.availableVersion, page != .about {
                        Button { page = .about } label: {
                            Label("可升级到 \(version)", systemImage: "arrow.down.circle.fill")
                                .font(.system(size: 12, weight: .semibold))
                        }
                        .buttonStyle(.plain).foregroundStyle(theme.accent)
                        .accessibilityHint("前往关于页安装升级")
                    }
                    if page == .connection, model.status?.binding != nil {
                        Button("解除关联") { confirmLogout = true }
                            .buttonStyle(.link).foregroundStyle(theme.danger).disabled(updater.sessionActive)
                    }
                }
                if page == .privacy {
                    ScrollView {
                        VStack(alignment: .leading, spacing: 16) {
                            pageContents
                            pageError
                        }
                        .padding(.horizontal, 8).padding(.top, 8)
                    }
                } else {
                    List {
                        pageContents
                        pageError
                    }
                    .listStyle(.plain).scrollContentBackground(.hidden)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading).padding(28)
        }
    }

    @MainActor
    private func prepareSettings() async {
        guard !preview else { return }
        if diskGuideLaunch {
            do {
                try await diskGuide.waitForAccess(applicationURL: management.layout.backgroundApplication) {
                    await model.refresh()
                    await model.recheckDisk()
                    return model.diskState
                }
                NSApplication.shared.terminate(nil)
            } catch is CancellationError { return }
            catch { operationError = error.localizedDescription }
            return
        }
        if let origin = FleetSetupAction.initialOrigin(arguments: CommandLine.arguments) {
            model.origin = origin
            page = .connection
        }
        guard !management.layout.requiresInstallation else { return }
        var needsLegacyConfirmation = false
        let ready = await startup.run(recover: {
            await updater.recoverAfterLaunch(force: startupAttempt > 0)
            if updater.recoveryPending { page = .about }
        }, prepare: {
            guard !updater.recoveryPending else { return }
            if FleetSetupAction.replacementConfirmed(arguments: CommandLine.arguments) {
                try await management.start(replaceLegacy: true)
            } else if try await management.hasLegacyInstallation() {
                replacementAction = {
                    try await management.start(replaceLegacy: true)
                    await model.refresh()
                    try await management.setAutoStart(model.autoStart)
                    if FleetSetupAction.authorizesDiskAfterInstallation(arguments: CommandLine.arguments) {
                        page = .privacy
                        try await diskGuide.authorize(applicationURL: management.layout.backgroundApplication) {
                            try await management.start()
                            await model.refresh()
                        }
                    }
                }
                replacementIncludesLegacy = true
                needsLegacyConfirmation = true
            } else if FleetSetupAction.startsAfterInstallation(arguments: CommandLine.arguments) {
                try await management.start()
            } else {
                try await management.prepareRuntime()
            }
        }, load: {
            await model.refresh()
            if FleetSetupAction.startsAfterInstallation(arguments: CommandLine.arguments), model.status != nil, !updater.recoveryPending {
                try await management.setAutoStart(model.autoStart)
            }
            if FleetSetupAction.authorizesDiskAfterInstallation(arguments: CommandLine.arguments), !updater.recoveryPending, !needsLegacyConfirmation {
                page = .privacy
                try await diskGuide.authorize(applicationURL: management.layout.backgroundApplication) {
                    try await management.start()
                    await model.refresh()
                }
            }
        })
        if ready { confirmReplacement = needsLegacyConfirmation }
    }

    @MainActor
    private func observeSettings() async {
        guard !management.layout.requiresInstallation, !diskGuideLaunch else { return }
        let connectivity = NWPathMonitor()
        if !preview {
            connectivity.pathUpdateHandler = { path in
                if path.status == .satisfied { Task { @MainActor in await retryConnectionIfNeeded() } }
            }
            connectivity.start(queue: .main)
        }
        defer { connectivity.cancel() }
        while !Task.isCancelled {
            await model.refresh()
            if !preview { openAuthorizationBrowser() }
            do { try await Task.sleep(nanoseconds: 2_000_000_000) } catch { return }
        }
    }

    @ViewBuilder
    private var pageContents: some View {
        switch page {
        case .overview: overview
        case .connection: connection
        case .privacy: privacy
        case .preferences: preferences
        case .about: about
        }
    }

    @ViewBuilder
    private var pageError: some View {
        if page != .overview, !operationError.isEmpty || !model.error.isEmpty {
            Label(operationError.isEmpty ? model.error : operationError, systemImage: "exclamationmark.circle")
                .foregroundStyle(theme.warning).textSelection(.enabled)
        }
    }

    @MainActor
    private func retryConnectionIfNeeded() async {
        guard !preview, startup.state.isReady, !management.layout.requiresInstallation,
              ["connecting", "reconnecting"].contains(model.status?.runtime?.phase ?? "") else { return }
        try? await management.retryConnection()
    }

    private var overview: some View {
        Group {
            Section {
                HStack(spacing: 16) {
                    Image(systemName: statusSymbol)
                        .font(.system(size: 30, weight: .regular))
                        .symbolRenderingMode(.hierarchical).foregroundStyle(statusColor)
                        .accessibilityHidden(true)
                    Text(overviewStatus.title)
                        .font(.system(size: 22, weight: .semibold, design: .rounded))
                        .foregroundStyle(overviewStatus.tone == .warning ? theme.warning : theme.text)
                        .textSelection(.enabled)
                    Spacer()
                    if model.status == nil {
                        Button(setupAction == .installApplication ? installationTitle : setupAction.title) {
                            if setupAction == .installApplication { installApplication() }
                            else { startWithConfirmation { try await management.setAutoStart(model.autoStart) } }
                        }
                        .buttonStyle(.borderedProminent)
                    } else {
                        Button { perform(allowPreview: true) { try await management.restart(); await model.refresh() } } label: {
                            Label("重启", systemImage: "arrow.clockwise").foregroundStyle(theme.secondaryText)
                        }
                        Button { perform(allowPreview: true) { try await management.stop(); await model.refresh() } } label: {
                            Label("停止", systemImage: "stop.fill").foregroundStyle(theme.secondaryText)
                        }
                    }
                }
                .padding(.vertical, 20)
                if management.layout.requiresInstallation {
                    Text("为保证后台与升级路径稳定，应用需位于 /Applications/Fleet Agent Settings.app。当前从其他位置打开；点击“\(installationTitle)”即可继续，无需手动搬移。")
                        .font(.callout).foregroundStyle(theme.secondaryText).textSelection(.enabled)
                }
            }
            Section {
                if let current = model.status {
                    row("版本", current.version, symbol: "shippingbox")
                    row("进程", String(current.pid), symbol: "number")
                    if ["connecting", "reconnecting"].contains(current.runtime?.phase ?? "") {
                        if let reason = current.runtime?.error, !reason.isEmpty {
                            Text(reason).foregroundStyle(theme.secondaryText).textSelection(.enabled)
                        }
                        Button("立即重试连接") { perform(allowPreview: true) { try await management.retryConnection(); await model.refresh() } }
                    }
                    ForEach(["files", "terminal"], id: \.self) { name in
                        if let component = current.runtime?.components?[name], component.phase != "running" {
                            Label("\(name == "files" ? "文件服务" : "终端服务")正在重新连接\(component.error.map { "：" + $0 } ?? "")", systemImage: "arrow.triangle.2.circlepath")
                                .foregroundStyle(theme.warning).textSelection(.enabled)
                        }
                    }
                    if current.binding == nil {
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
            Section {
                Button {
                    if let webURL, !NSWorkspace.shared.open(webURL) { operationError = "无法打开网页端" }
                } label: {
                    Label("打开网页端", systemImage: "arrow.up.right.square")
                        .font(.system(size: 17, weight: .semibold))
                        .foregroundStyle(.white)
                        .frame(minWidth: 240, minHeight: 56)
                        .background(theme.accent, in: RoundedRectangle(cornerRadius: FleetTheme.controlRadius))
                }
                .buttonStyle(.plain).disabled(webURL == nil).opacity(webURL == nil ? 0.5 : 1)
                .frame(maxWidth: .infinity).padding(.vertical, 80)
                .listRowSeparator(.hidden)
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
                        .fontWeight(.medium).foregroundStyle(theme.accent)
                    Spacer()
                    Label(model.status?.diskAccess.label ?? model.diskState.label, systemImage: model.diskState == .verified ? "checkmark.circle.fill" : "exclamationmark.circle")
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
                            startWithConfirmation {
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
                if model.diskState == .unknown {
                    Text(model.status == nil
                         ? "后台尚未连接，暂时无法检查磁盘访问权限。"
                         : "后台尚无法确认完全磁盘访问权限。若系统开关已开启，无需反复授权；可重启并检查。检测结果不影响已有文件访问。")
                        .font(.callout).foregroundStyle(theme.secondaryText)
                }
                if let evidence = model.status?.diskAccess, !(evidence.deniedTargets ?? []).isEmpty {
                    DisclosureGroup("检测详情") {
                        Text((evidence.deniedTargets ?? []).joined(separator: "\n"))
                            .font(.system(size: 11, design: .monospaced)).textSelection(.enabled)
                    }
                    .foregroundStyle(theme.secondaryText)
                }
            }
        }
    }

    private var preferences: some View {
        Section {
            Toggle(isOn: autoStartBinding) {
                Label {
                    Text("登录后启动 Fleet Agent").foregroundStyle(theme.text)
                } icon: {
                    Image(systemName: "power.circle").foregroundStyle(theme.accent)
                }
            }
                .toggleStyle(.switch).disabled(management.layout.requiresInstallation || (preview && model.status == nil))
            if management.loginRequiresApproval {
                Button("前往系统登录项设置") { management.openBackgroundSettings() }.buttonStyle(.link)
            }
        }
        .listRowBackground(Color.clear)
    }

    private var about: some View {
        Group {
            Section {
                VStack(spacing: 12) {
                    Image(nsImage: NSWorkspace.shared.icon(forFile: management.layout.application.path))
                        .resizable().scaledToFit().frame(width: 96, height: 96).accessibilityHidden(true)
                    Text(RuntimeLayout.applicationName).font(.system(size: 22, weight: .semibold))
                    Text(Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "开发版本")
                        .foregroundStyle(theme.secondaryText)
                    if !management.layout.requiresInstallation {
                        if let version = updater.availableVersion {
                            Button("升级到 \(version)") { Task { await updater.check() } }
                                .buttonStyle(.borderedProminent).disabled(updater.sessionActive)
                        } else {
                            Button("检查更新") { Task { await updater.check() } }.disabled(updater.sessionActive)
                        }
                    }
                }
                .frame(maxWidth: .infinity).padding(.vertical, 28)
                if !updater.message.isEmpty { Text(updater.message).foregroundStyle(theme.secondaryText).textSelection(.enabled) }
                if updater.waitingForIdle {
                    Button("取消等待") { updater.cancelWaitingForIdle() }
                } else if updater.installationNeedsAttention {
                    HStack {
                        Button("重试安装") { Task { await updater.continueInstallation() } }
                        Button("任务结束后自动安装") { Task { await updater.waitForIdleAndInstall() } }
                    }
                }
            }
            Section {
                if management.layout.requiresInstallation {
                    Button(installationTitle) { installApplication() }.buttonStyle(.borderedProminent)
                } else {
                    DisclosureGroup("卸载") {
                        Toggle("同时删除本机 Fleet 配置", isOn: $removeSettings)
                        Button("卸载 Fleet Agent Settings 和 Fleet Agent") { confirmUninstall = true }
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
            let existingApplication = FileManager.default.fileExists(atPath: RuntimeLayout.installedApplication().path)
            let legacy = try await management.hasLegacyInstallation()
            let needsConfirmation = existingApplication || legacy
            let action: @MainActor () async throws -> Void = {
                let installed = try await management.install(replaceExisting: needsConfirmation)
                let configuration = NSWorkspace.OpenConfiguration()
                configuration.arguments = FleetSetupAction.installationArguments(replacementConfirmed: needsConfirmation)
                if authorizeDisk { configuration.arguments.append("--fleet-authorize-disk") }
                if let origin = try? FleetSettings.validatedOrigin(model.origin) { configuration.arguments.append("--fleet-origin=\(origin)") }
                configuration.createsNewApplicationInstance = true
                _ = try await NSWorkspace.shared.openApplication(at: installed, configuration: configuration)
                NSApplication.shared.terminate(nil)
            }
            if needsConfirmation {
                replacementAction = action
                replacementIncludesLegacy = legacy
                confirmReplacement = true
            } else { try await action() }
        }
    }
    private func startWithConfirmation(_ afterStart: @escaping @MainActor () async throws -> Void = {}) {
        perform(allowPreview: true) {
            let legacy = try await management.hasLegacyInstallation()
            let action: @MainActor () async throws -> Void = {
                try await management.start(replaceLegacy: legacy)
                await model.refresh()
                try await afterStart()
            }
            if legacy {
                replacementAction = action
                replacementIncludesLegacy = legacy
                confirmReplacement = true
            } else { try await action() }
        }
    }
    private func saveSettings(authorize: Bool) {
        saveDelay?.cancel()
        let save: @MainActor () async throws -> Void = {
            guard await model.save() else { return }
            if authorize { await model.beginPairing() }
        }
        if model.status == nil { startWithConfirmation(save) }
        else { perform { try await save() } }
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
    private func perform(allowPreview: Bool = false, _ action: @escaping @MainActor () async throws -> Void) {
        guard !preview || allowPreview else {
            previewRestriction = true
            return
        }
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
