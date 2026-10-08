import SwiftUI

struct FleetWorkspace: View {
    @ObservedObject var store: FleetStore
    @AppStorage("fleet.devices.visible") private var devicesVisible = true
    @AppStorage("fleet.sessions.visible") private var sessionsPinned = true
    @State private var sessionsOpen = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        GeometryReader { geometry in
            let layout = FleetLayout.resolve(width: geometry.size.width)
            let railWidth = store.ready && layout != .compact ? (devicesVisible ? layout.deviceWidth : Double(TitaniumStyle.metric("collapsedDeviceWidth"))) : 0
            let showSessions = store.ready && !store.isFiles &&
                (layout == .compact ? !store.showingDetail : sessionsPinned)
            VStack(spacing: 0) {
                if store.gateway != nil && (!store.ready || (layout == .compact && (!store.showingDetail || store.isFiles))) { toolbar(layout) }
                HStack(spacing: 0) {
                    if railWidth > 0 {
                        DevicePanel(store: store, collapsed: !devicesVisible, collapse: { devicesVisible.toggle() })
                            .frame(width: railWidth).clipped()
                    }
                    if showSessions {
                        sessionPanel(layout).frame(width: layout == .compact ? geometry.size.width : layout.sessionWidth)
                            .clipped()
                    }
                    ZStack {
                        FleetWebView(store: store, layout: layout, sessionsPinned: sessionsPinned)
                        if store.loading && !store.ready {
                            ProgressView("正在连接网关…").padding(20)
                                .background(TitaniumStyle.color("surface-2"), in: RoundedRectangle(cornerRadius: TitaniumStyle.radius("card")))
                        }
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity).clipped()
                    .accessibilityHidden(showSessions && layout == .compact)
                    .overlay(alignment: .top) {
                        if store.ready && layout == .compact && store.showingDetail && !store.isFiles {
                            HStack {
                                Button { store.backToList() } label: { Image(systemName: "arrow.left") }
                                    .accessibilityLabel("返回会话列表").accessibilityIdentifier("toggle-sessions")
                                Spacer(minLength: 0)
                                FleetSettingsMenu(store: store)
                            }
                            .buttonStyle(TitaniumIconButtonStyle()).padding(.horizontal, 14).frame(height: 44)
                            .accessibilityElement(children: .contain)
                            .accessibilityValue(store.snapshot?.theme ?? "light").accessibilityIdentifier("workspace-title")
                        }
                    }
                    .overlay(alignment: .topLeading) {
                        if store.ready && layout != .compact && !store.isFiles && !sessionsPinned {
                            Button { sessionsOpen.toggle() } label: { Image(systemName: "list.bullet") }
                                .buttonStyle(TitaniumIconButtonStyle(size: 42, background: TitaniumStyle.color("surface-2")))
                                .accessibilityLabel("呼出会话列表").accessibilityIdentifier("sessions-launcher")
                                .padding(.leading, 14).padding(.top, 12)
                        }
                        if store.ready && layout != .compact && !store.isFiles && !sessionsPinned && sessionsOpen {
                            ZStack(alignment: .topLeading) {
                                Color.black.opacity(0.12).contentShape(Rectangle())
                                    .onTapGesture { sessionsOpen = false }
                                    .accessibilityLabel("关闭会话浮层").accessibilityIdentifier("sessions-backdrop")
                                sessionPanel(layout).frame(width: min(layout.sessionWidth, geometry.size.width - railWidth - 24))
                                    .background(TitaniumStyle.color("bg"))
                                    .clipShape(RoundedRectangle(cornerRadius: TitaniumStyle.radius("card")))
                                    .shadow(color: .black.opacity(0.12), radius: 16, x: 4, y: 8)
                                    .padding(.leading, 12).padding(.vertical, 12)
                            }
                            .onKeyPress(.escape) { sessionsOpen = false; return .handled }
                        }
                    }
                    .simultaneousGesture(DragGesture(minimumDistance: 24).onEnded { value in
                        if layout == .compact && store.showingDetail && !store.isFiles &&
                            value.startLocation.x < 28 && value.translation.width > 90 && abs(value.translation.height) < 60 {
                            store.backToList()
                        }
                    })
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .background(TitaniumStyle.color("bg"))
            .overlay { if store.gateway == nil { GatewaySetup(store: store) } }
            .animation(reduceMotion ? nil : .easeInOut(duration: 0.2), value: devicesVisible)
            .animation(reduceMotion ? nil : .easeInOut(duration: 0.2), value: sessionsPinned)
            .onChange(of: store.openedSequence) { _, _ in sessionsOpen = false }
            .onChange(of: layout == .compact) { _, _ in sessionsOpen = false }
            .onChange(of: store.isFiles) { _, _ in sessionsOpen = false }
            .onChange(of: store.ready) { _, ready in if !ready { sessionsOpen = false } }
            .sheet(isPresented: $store.deviceSheet) {
                DevicePicker(store: store).presentationDetents([.medium, .large])
            }
            .sheet(isPresented: $store.settingsSheet) {
                GatewaySetup(store: store).presentationDetents([.medium, .large])
            }
            .sheet(item: $store.auxiliaryPage) { page in
                NavigationStack {
                    AuxiliaryWebView(url: page.url, store: store)
                        .toolbar { Button("完成") { store.auxiliaryPage = nil; store.foreground() } }
                }
                .sheet(item: exportBinding, onDismiss: { store.discardExport() }) { file in
                    ShareFileView(url: file.url)
                }
            }
            .sheet(item: Binding(get: { store.auxiliaryPage == nil ? store.exportedFile : nil },
                                 set: { if let file = $0 { store.exportedFile = file } else { store.discardExport() } }),
                   onDismiss: { store.discardExport() }) { file in
                ShareFileView(url: file.url)
            }
            .alert("操作提示", isPresented: Binding(get: { store.error != nil }, set: { if !$0 { store.error = nil } })) {
                Button("关闭", role: .cancel) { store.error = nil }
                Button("重新加载") { store.webView?.reload() }
            } message: { Text(store.error ?? "") }
        }
    }

    private var exportBinding: Binding<SharedFile?> {
        Binding(get: { store.exportedFile },
                set: { if let file = $0 { store.exportedFile = file } else { store.discardExport() } })
    }

    private func sessionPanel(_ layout: FleetLayout) -> some View {
        SessionPanel(store: store, compact: layout == .compact, pinned: sessionsPinned, togglePin: {
            sessionsPinned.toggle()
            sessionsOpen = false
            if !sessionsPinned { store.showingDetail = true }
        })
    }

    private func toolbar(_ layout: FleetLayout) -> some View {
        HStack(spacing: 8) {
            if store.ready {
                    FleetModePicker(store: store, mobile: true)
                    Spacer(minLength: 0)
                    Button { store.deviceSheet = true } label: {
                        HStack(spacing: 6) {
                            Image(systemName: store.devices.first(where: { $0.id == store.snapshot?.deviceScope })?.symbol ?? "square.grid.2x2")
                                .font(.system(size: 18))
                            Text(store.deviceName).font(TitaniumStyle.font("secondary", weight: .semibold)).lineLimit(1)
                            Spacer(minLength: 0)
                            Image(systemName: "chevron.down").font(.system(size: 10))
                        }.padding(.horizontal, 8).frame(maxWidth: 176, minHeight: 44).contentShape(Rectangle())
                    }
                    .buttonStyle(.plain).accessibilityLabel("选择会话设备").accessibilityIdentifier("toggle-devices")
            } else {
                Image("FleetMark").resizable().scaledToFit().frame(width: 28, height: 28)
                    .foregroundStyle(TitaniumStyle.color("accent"))
                Text("FLEET HUB").font(.system(size: 13, weight: .semibold, design: .monospaced)).tracking(0.8)
            }
            Spacer(minLength: 0)
            if store.busy { ProgressView().controlSize(.small) }
            if store.ready { FleetSettingsMenu(store: store) }
            else {
                Button { store.settingsSheet = true } label: { Image(systemName: "network") }.accessibilityLabel("网关设置")
            }
        }
        .foregroundStyle(TitaniumStyle.color("text"))
        .buttonStyle(TitaniumIconButtonStyle()).padding(.horizontal, 14)
        .padding(.top, 14).frame(height: 58)
        .background(TitaniumStyle.color("bg"))
    }
}

struct GatewaySetup: View {
    @ObservedObject var store: FleetStore
    @State private var address = ""
    @State private var invalid = false

    var body: some View {
        NavigationStack {
            Form {
                Section("连接你的 Fleet 网关") {
                    TextField("https://fleet.example.com", text: $address)
                        .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                        .accessibilityIdentifier("gateway-address")
                    Text("支持自定义 HTTPS 端口。登录和两步验证会在下一页完成。")
                        .font(.footnote).foregroundStyle(.secondary)
                    Button("连接") {
                        invalid = !store.connect(address)
                        if !invalid { store.settingsSheet = false }
                    }
                    .accessibilityIdentifier("connect-gateway")
                    if invalid { Text("请输入 HTTPS 网关根地址，不包含账号、查询参数或子路径。") .foregroundStyle(.red) }
                }
            }
            .navigationTitle("Fleet Hub")
            .toolbar {
                if store.gateway != nil { Button("取消") { store.settingsSheet = false } }
            }
            .onAppear { address = store.gateway?.absoluteString ?? "" }
        }
    }
}
