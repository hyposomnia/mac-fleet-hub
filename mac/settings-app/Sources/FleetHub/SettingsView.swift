import AppKit
import FleetCore
import SwiftUI

private enum SettingsPage: String, CaseIterable, Identifiable {
    case overview = "运行状态", connection = "服务器", privacy = "磁盘权限", about = "关于"
    var id: String { rawValue }
    var symbol: String {
        switch self {
        case .overview: return "desktopcomputer"
        case .connection: return "network"
        case .privacy: return "lock.shield"
        case .about: return "info.circle"
        }
    }
}

struct SettingsView: View {
    @ObservedObject var model: SettingsModel
    @ObservedObject var updater: AppUpdater
    let management: NativeManagement
    @State private var page: SettingsPage = .overview
    @State private var operationError = ""
    @State private var operationBusy = false
    @State private var openedAttempt = ""
    @State private var confirmUninstall = false
    @State private var removeSettings = false

    var body: some View {
        HStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 28) {
                HStack(spacing: 10) {
                    Image(systemName: "square.stack.3d.up.fill").font(.title2)
                    Text("fleet hub").font(.title3.weight(.semibold))
                }
                .padding(.top, 32)
                VStack(spacing: 6) {
                    ForEach(SettingsPage.allCases) { item in
                        Button { page = item } label: {
                            Label(item.rawValue, systemImage: item.symbol)
                                .font(.body.weight(page == item ? .semibold : .regular))
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.horizontal, 14).padding(.vertical, 11)
                                .background(page == item ? Color.primary.opacity(0.07) : .clear, in: RoundedRectangle(cornerRadius: 8))
                        }
                        .buttonStyle(.plain)
                    }
                }
                Spacer()
                Text("关闭窗口不会停止后台服务")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Text("Fleet Hub")
                    .font(.caption2).foregroundStyle(.secondary)
            }
            .padding(20).frame(width: 180)
            .background(.regularMaterial)
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 28) {
                    VStack(alignment: .leading, spacing: 8) {
                        Text(page.rawValue).font(.largeTitle.weight(.semibold))
                        Text(subtitle).foregroundStyle(.secondary)
                    }
                    switch page {
                    case .overview: overview
                    case .connection: connection
                    case .privacy: privacy
                    case .about: about
                    }
                    if !operationError.isEmpty || !model.error.isEmpty {
                        Label(operationError.isEmpty ? model.error : operationError, systemImage: "exclamationmark.triangle")
                            .font(.callout).foregroundStyle(.orange)
                            .textSelection(.enabled)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading).padding(36)
            }
        }
        .disabled(model.isBusy || operationBusy || updater.busy)
        .task {
            if management.layout.requiresInstallation { page = .about; return }
            await updater.recoverAfterLaunch()
            await model.refresh()
            var refreshes = 0
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                guard !Task.isCancelled else { break }
                await model.refresh()
                refreshes += 1
                if model.status != nil && refreshes % 8 == 0 { await model.recheckDisk() }
                openPairingBrowser()
            }
        }
        .alert("卸载 Fleet Hub？", isPresented: $confirmUninstall) {
            Button("取消", role: .cancel) {}
            Button("确认卸载", role: .destructive) {
                perform { try await management.uninstall(removeSettings: removeSettings); NSApplication.shared.terminate(nil) }
            }
        } message: {
            Text("将撤销本机设备授权、停止后台，并将应用移入废纸篓。后台已停止时会临时启动以检查并撤销授权。Claude/Codex 会话和你的文件会保留。服务器无法确认撤销时会保留应用供你重试。")
        }
    }

    private var subtitle: String {
        switch page {
        case .overview: return "设置应用管理服务，后台独立运行。"
        case .connection: return "只填写网页地址，账号与密码始终留在浏览器。"
        case .privacy: return "验证的是实际后台进程，不是设置窗口的权限。"
        case .about: return "应用安装、发行与更新信息。"
        }
    }

    private var overview: some View {
        VStack(alignment: .leading, spacing: 22) {
            Label(model.status == nil ? "后台未确认运行" : "后台进程已响应", systemImage: model.status == nil ? "circle.dotted" : "checkmark.circle.fill")
                .font(.title3.weight(.medium))
            if let current = model.status {
                row("进程", "PID \(current.pid)")
                row("Agent 版本", current.version)
                if let binding = current.binding {
                    row("关联账号", binding.ownerEmail)
                    row("设备编号", binding.deviceID)
                    row("设备授权", binding.locked ? "已锁定" : binding.complete ? "已登记，网络状态另行检查" : "尚未完成入网")
                } else {
                    row("设备授权", "尚未关联账号")
                }
            }
            Divider()
            HStack {
                Button("刷新状态") { Task { await model.refresh() } }
                Button("启动后台") { perform { try await management.start(); await model.refresh() } }
                    .disabled(management.layout.requiresInstallation || model.status != nil)
                Button("重启") { perform { try await management.restart(); await model.refresh() } }.disabled(model.status == nil)
                Button("停止") { perform { try await management.stop(); await model.refresh() } }.disabled(model.status == nil)
            }
            Text(management.autoStartStatus).font(.callout).foregroundStyle(.secondary)
            Button("打开系统后台项目设置") { management.openBackgroundSettings() }
            if let runtime = model.status?.runtime {
                row("服务状态", runtime.phase == "running" ? "终端与文件服务已启动" : runtime.phase == "unbound" ? "等待设备关联" : runtime.error ?? "正在连接服务…")
            }
        }
    }

    private var connection: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("服务网页地址").font(.headline)
            TextField("https://fleet.example.com", text: $model.origin)
                .textFieldStyle(.roundedBorder)
            Text("使用该服务的完整 HTTPS 地址，包含实际端口，不带路径。")
                .font(.caption).foregroundStyle(.secondary)
            Toggle("登录 macOS 后自动启动后台", isOn: $model.autoStart)
            Text(management.autoStartStatus).font(.caption).foregroundStyle(.secondary)
            Button(model.status?.binding == nil ? "保存并连接" : "保存设置") {
                perform {
                    if model.status == nil { try await management.start(); await model.refresh() }
                    await model.save()
                    if model.error.isEmpty {
                        try management.setAutoStart(model.autoStart)
                        if model.status?.binding == nil { await model.beginPairing() }
                    }
                }
            }
            .buttonStyle(.borderedProminent).disabled(management.layout.requiresInstallation || model.status?.pairing?.isActive == true)
            if let pairing = model.status?.pairing, pairing.phase != "idle" {
                Divider()
                Text(pairing.label).font(.headline)
                if let code = pairing.code { row("配对码", code) }
                if let owner = pairing.ownerEmail { row("关联账号", owner) }
                if let device = pairing.deviceID { row("设备编号", device) }
                if let link = pairing.verificationURL, pairing.phase == "browser" {
                    Button("重新打开浏览器") { NSWorkspace.shared.open(link) }
                }
                if pairing.phase == "awaiting_confirmation" {
                    Button("确认接入此账号") { Task { await model.confirmPairing() } }.buttonStyle(.borderedProminent)
                }
                if pairing.isActive { Button("取消关联") { Task { await model.cancelPairing() } } }
            }
            if model.status?.binding != nil {
                Button("解除设备关联", role: .destructive) { perform { try await management.logout(); await model.refresh() } }
            }
        }
        .disabled(updater.sessionActive)
    }

    private var privacy: some View {
        VStack(alignment: .leading, spacing: 22) {
            Label(model.diskState.label, systemImage: model.diskState == .verified ? "checkmark.shield" : "exclamationmark.shield")
                .font(.title3.weight(.medium))
            Text("Fleet Agent 需要访问磁盘文件。完全磁盘访问必须由你在系统设置中开启，应用不能代为授权。")
            VStack(alignment: .leading, spacing: 14) {
                Text("1. 打开「隐私与安全性 → 完全磁盘访问」。")
                Text("2. 点击 +，添加实际后台应用 Fleet Agent.app，而不是仅添加 Fleet Hub 设置窗口。")
                Text("3. 开启开关，按系统提示退出并重新启动后台，然后返回这里重新检查。")
            }
            .font(.callout)
            HStack {
                Button("打开完全磁盘访问设置") {
                    if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles") { NSWorkspace.shared.open(url) }
                }
                Button("定位 Fleet Agent.app") {
                    NSWorkspace.shared.activateFileViewerSelecting([management.layout.backgroundApplication])
                }
                .disabled(!FileManager.default.fileExists(atPath: management.layout.backgroundApplication.path))
            }
            Button("重新检查后台权限") { Task { await model.recheckDisk() } }.disabled(model.status == nil)
            Text("检测仅只读打开固定受保护目标，不读取文件内容。目标不存在、后台未运行或证据不足时显示待验证。即使验证通过，文件本身的权限与 ACL 仍然生效。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private var about: some View {
        VStack(alignment: .leading, spacing: 20) {
            row("应用版本", Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "开发版本")
            row("安装位置", management.layout.application.path)
            Button("检查版本升级") { Task { await updater.check() } }
                .disabled(updater.sessionActive || management.layout.requiresInstallation)
            if !updater.message.isEmpty {
                Text(updater.message).font(.callout).foregroundStyle(.secondary).textSelection(.enabled)
            }
            if updater.sessionActive {
                Button("重新尝试安装升级") { Task { await updater.continueInstallation() } }
            }
            if management.layout.requiresInstallation {
                Text("将 Fleet Hub 安装到「应用程序」后即可设置后台服务。")
                Button("安装到应用程序") {
                    perform { let installed = try await management.install(); NSWorkspace.shared.open(installed); NSApplication.shared.terminate(nil) }
                }.buttonStyle(.borderedProminent)
            }
            if !management.layout.requiresInstallation {
                Divider()
                Toggle("卸载时移除本机设置与设备网络状态", isOn: $removeSettings)
                Button("卸载 Fleet Hub…", role: .destructive) { confirmUninstall = true }.disabled(updater.sessionActive)
            }
        }
    }

    private func row(_ title: String, _ value: String) -> some View {
        HStack(alignment: .top) {
            Text(title).foregroundStyle(.secondary).frame(width: 92, alignment: .leading)
            Text(value).textSelection(.enabled)
        }
        .font(.callout)
    }

    private func perform(_ action: @escaping @MainActor () async throws -> Void) {
        guard !updater.sessionActive else { operationError = "升级会话正在进行，请先完成或取消升级。"; return }
        guard !operationBusy else { return }
        operationBusy = true
        operationError = ""
        Task {
            defer { operationBusy = false }
            do { try await action() } catch { operationError = error.localizedDescription }
        }
    }

    private func openPairingBrowser() {
        guard let pairing = model.status?.pairing, pairing.isActive,
              pairing.attempt != openedAttempt, let url = pairing.verificationURL else { return }
        if NSWorkspace.shared.open(url) { openedAttempt = pairing.attempt }
    }
}
