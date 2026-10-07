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
    @FocusState private var originFocused: Bool
    private var theme: FleetTheme { FleetTheme(scheme: scheme) }

    var body: some View {
        HStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 28) {
                HStack(spacing: 10) {
                    FleetBrandMark().foregroundStyle(theme.accent)
                    Text("FLEET HUB").font(.system(size: 13, weight: .semibold, design: .monospaced)).tracking(0.8)
                }
                VStack(spacing: 8) {
                    ForEach(SettingsPage.allCases) { item in
                        Button { page = item } label: {
                            Label(item.rawValue, systemImage: item.symbol)
                                .font(.system(size: 13, weight: page == item ? .semibold : .regular))
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.horizontal, 14).frame(height: FleetTheme.controlHeight)
                                .foregroundStyle(page == item ? theme.accent : theme.secondaryText)
                                .background(page == item ? theme.surface : .clear, in: RoundedRectangle(cornerRadius: FleetTheme.controlRadius))
                        }
                        .buttonStyle(.plain).accessibilityAddTraits(page == item ? .isSelected : [])
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
        .task {
            if management.layout.requiresInstallation { page = .about; return }
            await updater.recoverAfterLaunch()
            if !updater.recoveryPending {
                do { try await management.prepareRuntime() }
                catch { operationError = error.localizedDescription }
            }
            await model.refresh()
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
                    Text(model.status == nil ? "后台未运行" : "后台运行中").font(.system(size: 16, weight: .semibold))
                    Spacer()
                    if model.status == nil {
                        Button("启动") { perform { try await management.start(); await model.refresh() } }
                            .buttonStyle(FleetButtonStyle(.primary)).disabled(management.layout.requiresInstallation)
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
                    .padding(20).background(theme.surface, in: RoundedRectangle(cornerRadius: FleetTheme.cardRadius))
                }
                .buttonStyle(.plain)
            }
            card {
                row("登录后启动", management.autoStartStatus)
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
                    Button("打开网页授权") { saveSettings(authorize: true) }
                        .buttonStyle(FleetButtonStyle(.primary))
                        .disabled(management.layout.requiresInstallation || model.status?.binding != nil || model.status?.pairing?.phase == "joining")
                    Button("保存设置") { saveSettings(authorize: false) }.disabled(management.layout.requiresInstallation)
                }
            }
            card {
                Toggle("登录后启动后台", isOn: $model.autoStart).toggleStyle(.switch)
                Text(management.autoStartStatus).foregroundStyle(theme.secondaryText)
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
            HStack {
                Button("打开系统设置") {
                    if let link = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles") { NSWorkspace.shared.open(link) }
                }
                .buttonStyle(FleetButtonStyle(.primary))
                Button("选择后台应用") { NSWorkspace.shared.activateFileViewerSelecting([management.layout.backgroundApplication]) }
                    .help("选择独立运行的 Fleet Agent.app；旧版 fleet-agent 的授权不会自动继承。")
                    .disabled(management.layout.requiresInstallation || !FileManager.default.fileExists(atPath: management.layout.backgroundApplication.path))
            }
            Text("仅授权 Fleet Agent，设置应用无需磁盘权限。").foregroundStyle(theme.secondaryText)
            if model.diskState != .verified && model.status != nil {
                Button("重启并检查") { perform { try await management.restart(); await model.refresh(); await model.recheckDisk() } }
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
                Button("安装到应用程序") {
                    perform { let installed = try await management.install(); NSWorkspace.shared.open(installed); NSApplication.shared.terminate(nil) }
                }
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
    private func saveSettings(authorize: Bool) {
        perform {
            if model.status == nil { try await management.start(); await model.refresh() }
            await model.save()
            guard model.error.isEmpty else { return }
            try management.setAutoStart(model.autoStart)
            if authorize { await model.beginPairing() }
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
