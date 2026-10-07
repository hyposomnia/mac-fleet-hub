import AppKit
import FleetCore
import SwiftUI

enum SettingsPage: String, CaseIterable, Identifiable {
    case overview = "运行状态", connection = "关联账号", privacy = "磁盘权限", about = "关于"
    var id: String { rawValue }
    var symbol: String {
        switch self {
        case .overview: return "desktopcomputer"
        case .connection: return "person.crop.circle"
        case .privacy: return "internaldrive"
        case .about: return "info.circle"
        }
    }
}

struct SettingsView: View {
    @ObservedObject var model: SettingsModel
    @ObservedObject var updater: AppUpdater
    let management: NativeManagement
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
    @FocusState private var originFocused: Bool
    private var theme: FleetTheme { FleetTheme(scheme: scheme) }
    private var setupAction: FleetSetupAction {
        FleetSetupAction(requiresInstallation: management.layout.requiresInstallation,
                         backgroundInstalled: FileManager.default.isExecutableFile(atPath: management.layout.agent.path))
    }
    private var installationTitle: String {
        FleetSetupAction.installationTitle(installed: FileManager.default.fileExists(atPath: "/Applications/Fleet Hub.app"))
    }
    private var validOrigin: Bool { (try? FleetSettings.validatedOrigin(model.origin)) != nil }

    var body: some View {
        HStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 28) {
                HStack(spacing: 10) {
                    FleetBrandMark().foregroundStyle(theme.accent)
                    Text("FLEET HUB").font(.system(size: 13, weight: .semibold, design: .monospaced)).tracking(0.8)
                }
                VStack(spacing: 8) {
                    ForEach(SettingsPage.allCases) { item in
                        FleetNavigationButton(page: item, selected: page == item) { page = item }
                    }
                }
                Spacer()
            }
            .padding(20).frame(width: 188).background(theme.navigation)
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    Text(page.rawValue).font(.system(size: 24, weight: .semibold))
                    switch page {
                    case .overview: overview
                    case .connection: connection
                    case .privacy: privacy
                    case .about: about
                    }
                    if !operationError.isEmpty || !model.error.isEmpty {
                        Label(operationError.isEmpty ? model.error : operationError, systemImage: "exclamationmark.circle")
                            .foregroundStyle(theme.warning).textSelection(.enabled)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading).padding(32)
            }
        }
        .font(.system(size: 13)).foregroundStyle(theme.text).background(theme.background).tint(theme.accent)
        .buttonStyle(FleetButtonStyle())
        .disabled(model.isBusy || operationBusy || updater.busy)
        .onDisappear { diskGuide.close() }
        .onChange(of: page) { selected in if selected != .privacy { diskGuide.close() } }
        .onChange(of: model.origin) { _ in queueSave() }
        .onChange(of: model.autoStart) { _ in queueSave() }
        .task {
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
                do { try management.setAutoStart(model.autoStart) }
                catch { operationError = error.localizedDescription }
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
        .alert("卸载 Fleet Hub？", isPresented: $confirmUninstall) {
            Button("取消", role: .cancel) {}
            Button("卸载", role: .destructive) {
                perform { try await management.uninstall(removeSettings: removeSettings); NSApplication.shared.terminate(nil) }
            }
        } message: { Text("撤销设备授权、停止后台并将应用移入废纸篓。你的文件与聊天会话保留。") }
    }

    private var overview: some View {
        VStack(alignment: .leading, spacing: 20) {
            card {
                HStack(spacing: 12) {
                    Circle().fill(model.status == nil ? theme.secondaryText : theme.online).frame(width: 8, height: 8)
                    Text(model.status == nil ? setupAction.status : "后台运行中").font(.system(size: 16, weight: .semibold))
                    Spacer()
                    if model.status == nil {
                        Button(setupAction == .installApplication ? installationTitle : setupAction.title) {
                            if setupAction == .installApplication { installApplication() }
                            else { perform { try await management.start(); await model.refresh(); try management.setAutoStart(model.autoStart) } }
                        }
                        .buttonStyle(FleetButtonStyle(.primary))
                    } else {
                        Button("重启") { perform { try await management.restart(); await model.refresh() } }
                        Button("停止") { perform { try await management.stop(); await model.refresh() } }
                    }
                }
                if let runtime = model.status?.runtime, runtime.phase != "unbound" {
                    Text(runtime.phase == "running" ? "设备服务已就绪" : runtime.error ?? "正在连接")
                        .foregroundStyle(theme.secondaryText)
                }
            }
            if let binding = model.status?.binding {
                card {
                    row("账号", binding.ownerEmail)
                    row("设备", binding.deviceID)
                    if binding.locked { Text("授权已锁定").foregroundStyle(theme.warning) }
                }
            } else {
                Button { page = .connection } label: {
                    HStack {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("尚未关联账号").font(.system(size: 16, weight: .semibold))
                            Text("关联账号").foregroundStyle(theme.accent)
                        }
                        Spacer()
                        Image(systemName: "arrow.right").foregroundStyle(theme.accent)
                    }
                    .padding(20).contentShape(Rectangle())
                    .background(theme.surface, in: RoundedRectangle(cornerRadius: FleetTheme.cardRadius))
                }
                .buttonStyle(.plain)
            }
            card {
                Toggle("登录后启动后台", isOn: $model.autoStart).toggleStyle(.switch)
                    .disabled(management.layout.requiresInstallation)
                Text(management.layout.requiresInstallation ? "安装后可设置" : management.autoStartStatus).foregroundStyle(theme.secondaryText)
                DisclosureGroup("运行详情") {
                    if let current = model.status { row("版本", current.version); row("进程", String(current.pid)).monospacedDigit() }
                }
                .foregroundStyle(theme.secondaryText)
            }
        }
    }

    private var connection: some View {
        VStack(alignment: .leading, spacing: 20) {
            VStack(alignment: .leading, spacing: 12) {
                Text("服务器地址").fontWeight(.medium)
                TextField("https://fleet.example.com", text: $model.origin)
                    .textFieldStyle(.plain).padding(.horizontal, 14).frame(height: FleetTheme.controlHeight)
                    .background(theme.surface, in: RoundedRectangle(cornerRadius: FleetTheme.controlRadius))
                    .focused($originFocused)
                    .overlay(RoundedRectangle(cornerRadius: FleetTheme.controlRadius).stroke(originFocused ? theme.accent : .clear, lineWidth: 2))
                    .disabled(model.status?.binding != nil)
                HStack {
                    Button("打开网页授权") {
                        if management.layout.requiresInstallation { installApplication() }
                        else { saveSettings(authorize: true) }
                    }
                        .buttonStyle(FleetButtonStyle(.primary))
                        .disabled(!validOrigin || model.isSaving || model.status?.binding != nil || model.status?.pairing?.phase == "joining")
                    if model.isSaving { ProgressView().controlSize(.small) }
                }
                if management.layout.requiresInstallation { Text("先\(installationTitle)，再继续网页授权。").foregroundStyle(theme.secondaryText) }
            }
            if let pairing = model.status?.pairing, pairing.phase != "idle" {
                card {
                    HStack {
                        if pairing.isActive { ProgressView().controlSize(.small) }
                        Text(pairing.label)
                        Spacer()
                        if pairing.isActive && pairing.phase != "joining" {
                            Button { Task { await model.cancelPairing() } } label: { Image(systemName: "xmark") }
                                .buttonStyle(FleetButtonStyle(.compact)).accessibilityLabel("取消授权")
                        }
                    }
                    if let owner = pairing.ownerEmail { row("账号", owner) }
                    if pairing.phase == "awaiting_confirmation" {
                        Button("确认接入") { Task { await model.confirmPairing() } }.buttonStyle(FleetButtonStyle(.primary))
                    }
                    if pairing.needsCleanup == true && model.status?.binding == nil {
                        Button("解除未完成关联") { confirmLogout = true }.buttonStyle(FleetButtonStyle(.danger))
                    }
                }
            }
            if let binding = model.status?.binding {
                card {
                    row("账号", binding.ownerEmail)
                    row("设备", binding.deviceID)
                    Button("解除关联") { confirmLogout = true }.buttonStyle(FleetButtonStyle(.danger)).disabled(updater.sessionActive)
                }
            }
        }
        .disabled(updater.sessionActive)
    }

    private var privacy: some View {
        VStack(alignment: .leading, spacing: 20) {
            card {
                HStack {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("完全磁盘访问").fontWeight(.medium)
                        Text(model.diskState.label).foregroundStyle(model.diskState == .verified ? theme.online : theme.warning)
                    }
                    Spacer()
                    Button { Task { await model.recheckDisk() } } label: { Image(systemName: "arrow.clockwise") }
                        .buttonStyle(FleetButtonStyle(.compact)).accessibilityLabel("重新检查后台权限")
                        .help("由实际后台只读检查；文件权限与 ACL 仍然生效。").disabled(model.status == nil)
                }
            }
            if let application = DiskAccessApplication(url: management.layout.backgroundApplication), !management.layout.requiresInstallation {
                card { DiskAccessInstructions(application: application) }
                HStack {
                    Button("打开系统设置") {
                        do { try diskGuide.show(applicationURL: management.layout.backgroundApplication) }
                        catch { operationError = error.localizedDescription }
                    }
                    .buttonStyle(FleetButtonStyle(.primary))
                    if model.diskState != .verified && model.status != nil {
                        Button("重启并检查") { perform { try await management.restart(); await model.refresh(); await model.recheckDisk() } }
                    }
                }
                Text("仅授权 Fleet Agent，设置应用无需磁盘权限。").foregroundStyle(theme.secondaryText)
            } else {
                Button("安装并启动") {
                    if management.layout.requiresInstallation { installApplication() }
                    else { perform { try await management.start(); await model.refresh(); try management.setAutoStart(model.autoStart) } }
                }
                .buttonStyle(FleetButtonStyle(.primary))
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

    private var about: some View {
        VStack(alignment: .leading, spacing: 20) {
            card {
                HStack(spacing: 16) {
                    FleetBrandMark(size: 48).foregroundStyle(theme.accent)
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Fleet Hub").font(.system(size: 20, weight: .semibold))
                        Text(Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "开发版本")
                            .foregroundStyle(theme.secondaryText)
                    }
                    Spacer()
                }
                if !management.layout.requiresInstallation { Button("检查更新") { Task { await updater.check() } }.disabled(updater.sessionActive) }
                if !updater.message.isEmpty { Text(updater.message).foregroundStyle(theme.secondaryText).textSelection(.enabled) }
                if updater.sessionActive { Button("继续安装升级") { Task { await updater.continueInstallation() } } }
            }
            if management.layout.requiresInstallation {
                Button(installationTitle) { installApplication() }
                .buttonStyle(FleetButtonStyle(.primary))
            } else {
                DisclosureGroup("卸载") {
                    Toggle("同时移除本机设置", isOn: $removeSettings)
                    Button("卸载 Fleet Hub") { confirmUninstall = true }.buttonStyle(FleetButtonStyle(.danger)).disabled(updater.sessionActive)
                }
                .foregroundStyle(theme.secondaryText)
            }
        }
    }

    private func card<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 16, content: content)
            .frame(maxWidth: .infinity, alignment: .leading).padding(20)
            .background(theme.surface, in: RoundedRectangle(cornerRadius: FleetTheme.cardRadius))
    }
    private func row(_ title: String, _ value: String) -> some View {
        HStack(alignment: .firstTextBaseline) {
            Text(title).foregroundStyle(theme.secondaryText).frame(width: 88, alignment: .leading)
            Text(value).textSelection(.enabled)
        }
    }
    private func installApplication() {
        perform {
            let installed: URL
            if FileManager.default.fileExists(atPath: "/Applications/Fleet Hub.app") { installed = try await management.installedApplication() }
            else { installed = try await management.install() }
            let configuration = NSWorkspace.OpenConfiguration()
            configuration.arguments = ["--fleet-install-and-start"]
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
    private func queueSave() {
        saveDelay?.cancel()
        guard !management.layout.requiresInstallation, !updater.sessionActive,
              model.origin.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || validOrigin else { return }
        saveDelay = Task {
            do { try await Task.sleep(nanoseconds: 600_000_000) } catch { return }
            guard !Task.isCancelled else { return }
            if model.status == nil { saveSettings(authorize: false) }
            else { await model.save() }
        }
    }
    private func perform(_ action: @escaping @MainActor () async throws -> Void) {
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
