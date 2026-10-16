import SwiftUI

struct FleetWorkspace: View {
    @Environment(\.fleetStyle) private var style
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
            let showToolbar = store.gateway != nil && (!store.ready || (layout == .compact && (!store.showingDetail || store.isFiles)))
            let webOverlay = store.nativeSheet == nil && store.webDialog == nil && (store.webOverlayVisible || store.snapshot?.overlay != nil)
            ZStack {
                FleetWebView(store: store, layout: layout, sessionsPinned: sessionsPinned)
                    .padding(.leading, webOverlay ? 0 : railWidth + (showSessions && layout != .compact ? layout.sessionWidth : 0))
                    .padding(.top, webOverlay ? 0 : showToolbar ? 58 : 0)
                    .opacity(showSessions && layout == .compact && !webOverlay ? 0 : 1)
                    .accessibilityHidden(showSessions && layout == .compact && !webOverlay)
                    .ignoresSafeArea(.container, edges: webOverlay ? .all : .bottom)
                    .zIndex(webOverlay ? 2 : 0)
            VStack(spacing: 0) {
                if showToolbar { toolbar(layout) }
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
                        Color.clear.allowsHitTesting(false)
                        if store.loading && !store.ready {
                            ProgressView("正在连接网关…").padding(20)
                                .background(style.color("surface-2"), in: RoundedRectangle(cornerRadius: TitaniumStyle.radius("card")))
                        }
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity).clipped()
                    .allowsHitTesting(false)
                    .accessibilityHidden(showSessions && layout == .compact)
                    .overlay(alignment: .top) {
                        if store.ready && layout == .compact && store.showingDetail && !store.isFiles {
                            HStack {
                                Button { store.backToList() } label: { Image(systemName: "arrow.left") }
                                    .accessibilityLabel("返回会话列表").accessibilityIdentifier("toggle-sessions")
                                Spacer(minLength: 0)
                            }
                            .buttonStyle(TitaniumIconButtonStyle()).padding(.horizontal, 14).frame(height: 44)
                            .accessibilityElement(children: .contain)
                            .accessibilityValue(store.snapshot?.theme ?? "light").accessibilityIdentifier("workspace-title")
                        }
                    }
                    .overlay(alignment: .topLeading) {
                        if store.ready && layout != .compact && !store.isFiles && !sessionsPinned {
                            Button { sessionsOpen.toggle() } label: { Image(systemName: "list.bullet") }
                                .buttonStyle(TitaniumIconButtonStyle(size: 42, background: style.color("surface-2")))
                                .accessibilityLabel("呼出会话列表").accessibilityIdentifier("sessions-launcher")
                                .padding(.leading, 14).padding(.top, 12)
                        }
                        if store.ready && layout != .compact && !store.isFiles && !sessionsPinned && sessionsOpen {
                            ZStack(alignment: .topLeading) {
                                Color.black.opacity(0.12).contentShape(Rectangle())
                                    .onTapGesture { sessionsOpen = false }
                                    .accessibilityLabel("关闭会话浮层").accessibilityIdentifier("sessions-backdrop")
                                sessionPanel(layout).frame(width: min(layout.sessionWidth, geometry.size.width - railWidth - 24))
                                    .background(style.color("bg"))
                                    .clipShape(RoundedRectangle(cornerRadius: TitaniumStyle.radius("card")))
                                    .shadow(color: .black.opacity(0.12), radius: 16, x: 4, y: 8)
                                    .padding(.leading, 12).padding(.vertical, 12)
                            }
                            .onKeyPress(.escape) { sessionsOpen = false; return .handled }
                        }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .blur(radius: webOverlay ? 3 : 0)
            .allowsHitTesting(!webOverlay)
            .accessibilityHidden(webOverlay)
            }
            .simultaneousGesture(DragGesture(minimumDistance: 24).onEnded { value in
                if !webOverlay && layout == .compact && store.showingDetail && !store.isFiles &&
                    value.startLocation.x < 28 && value.translation.width > 90 && abs(value.translation.height) < 60 {
                    store.backToList()
                }
            })
            .background(style.color("bg"))
            .accessibilityHidden(store.deviceSheet || store.nativeSheet != nil || store.webDialog != nil)
            .overlay {
                if let sheet = store.nativeSheet {
                    FullWidthBottomDrawer(isPresented: Binding(get: { store.nativeSheet != nil }, set: { if !$0 && !store.drawerLocked && !store.drawerBusy { store.nativeSheet = nil } }), safeAreaInsets: geometry.safeAreaInsets, initialFraction: sheet.initialFraction, keyboardVisible: store.keyboardVisible, keyboardTop: store.keyboardTop) {
                        nativePanel(sheet, expanded: layout != .compact).id(sheet.id)
                    }.transition(.move(edge: .bottom).combined(with: .opacity))
                } else if let dialog = store.webDialog {
                    FullWidthBottomDrawer(isPresented: Binding(get: { store.webDialog != nil }, set: { if !$0 { store.dialogAction("__close") } }), safeAreaInsets: geometry.safeAreaInsets, initialFraction: 0.75, keyboardVisible: store.keyboardVisible, keyboardTop: store.keyboardTop) {
                        NativeWebDialogPanel(store: store, dialog: dialog)
                    }.transition(.move(edge: .bottom).combined(with: .opacity))
                } else if store.deviceSheet {
                    FullWidthBottomDrawer(isPresented: $store.deviceSheet, safeAreaInsets: geometry.safeAreaInsets, keyboardVisible: store.keyboardVisible, keyboardTop: store.keyboardTop) {
                        DevicePicker(store: store)
                    }
                    .transition(.opacity)
                }
            }
            .animation(reduceMotion ? nil : .easeInOut(duration: 0.2), value: store.deviceSheet)
            .animation(reduceMotion ? nil : .easeInOut(duration: 0.22), value: store.nativeSheet?.id)
            .animation(reduceMotion ? nil : .easeInOut(duration: 0.22), value: store.webDialog?.id)
            .animation(reduceMotion ? nil : .easeInOut(duration: 0.2), value: devicesVisible)
            .animation(reduceMotion ? nil : .easeInOut(duration: 0.2), value: sessionsPinned)
            .onChange(of: store.openedSequence) { _, _ in sessionsOpen = false }
            .onChange(of: layout == .compact) { _, _ in sessionsOpen = false }
            .onChange(of: store.isFiles) { _, _ in sessionsOpen = false }
            .onChange(of: store.ready) { _, ready in if !ready { sessionsOpen = false } }
            .onChange(of: store.nativeSheet?.id) { _, identifier in if identifier != nil { store.webView?.window?.endEditing(true) } }
            .onChange(of: store.webDialog?.id) { _, identifier in if identifier != nil { store.webView?.window?.endEditing(true) } }
            .onChange(of: store.deviceSheet) { _, visible in if visible { store.webView?.window?.endEditing(true) } }
            .fullScreenCover(item: $store.auxiliaryPage) { page in
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

    @ViewBuilder private func nativePanel(_ sheet: NativeSheet, expanded: Bool) -> some View {
        switch sheet {
        case .settings: NativeSettingsPanel(store: store, expanded: expanded)
        case .device(let identifier): NativeDeviceEditor(store: store, identifier: identifier)
        case .gateway: GatewaySetup(store: store)
        case .rename(let session): NativeSessionRenameEditor(store: store, session: session)
        case .newSessionDevice:
            VStack(spacing: 0) {
                NativeDrawerHeader(title: "选择设备") { store.nativeSheet = nil }
                ScrollView {
                    VStack(spacing: 8) {
                        ForEach(store.devices) { device in
                            Button {
                                store.nativeSheet = nil
                                store.newSession(device: device.id)
                            } label: {
                                HStack { FleetDeviceIcon(device: device); Text(device.name); Spacer(); Text(device.online ? "在线" : "离线").font(.caption) }
                                    .padding(16).frame(minHeight: 52)
                                    .background(style.color("surface-1"), in: RoundedRectangle(cornerRadius: 12))
                            }.buttonStyle(.plain).disabled(!device.online || store.busy)
                        }
                    }.padding(16)
                }
            }
        }
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
                            if let device = store.devices.first(where: { $0.id == store.snapshot?.deviceScope }) {
                                FleetDeviceIcon(device: device, size: 20)
                            } else {
                                Image(systemName: "square.grid.2x2").font(.system(size: 18))
                            }
                            Text(store.deviceName).font(TitaniumStyle.font("secondary", weight: .semibold)).lineLimit(1)
                            Spacer(minLength: 0)
                            Image(systemName: "chevron.down").font(.system(size: 10))
                        }.padding(.horizontal, 8).frame(maxWidth: 176, minHeight: 44).contentShape(Rectangle())
                    }
                    .buttonStyle(.plain).accessibilityLabel("选择会话设备").accessibilityIdentifier("toggle-devices")
            } else {
                Image("FleetMark").resizable().scaledToFit().frame(width: 28, height: 28)
                    .foregroundStyle(style.color("accent"))
                Text("FLEET HUB").font(.system(size: 13, weight: .semibold, design: .monospaced)).tracking(0.8)
            }
            Spacer(minLength: 0)
            if store.busy { ProgressView().controlSize(.small) }
            if store.ready { FleetSettingsMenu(store: store) }
            else {
                Button { store.nativeSheet = .gateway } label: { Image(systemName: "network") }.accessibilityLabel("网关设置")
            }
        }
        .foregroundStyle(style.color("text"))
        .buttonStyle(TitaniumIconButtonStyle()).padding(.horizontal, 14)
        .padding(.top, 14).frame(height: 58)
        .background(style.color("bg"))
    }
}

struct FullWidthBottomDrawer<Content: View>: View {
    @Environment(\.fleetStyle) private var style
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Binding var isPresented: Bool
    var safeAreaInsets: EdgeInsets
    var initialFraction: CGFloat = 0.55
    var keyboardVisible = false
    var keyboardTop: CGFloat?
    @State private var expanded = false
    @ViewBuilder var content: () -> Content

    var body: some View {
        GeometryReader { geometry in
            let overlap = keyboardTop.map { max(0, geometry.frame(in: .global).maxY - $0) } ?? 0
            let usableHeight = max(0, geometry.size.height - overlap)
            let availableHeight = max(0, usableHeight - safeAreaInsets.top)
            let height = min(availableHeight, max(320, usableHeight * (expanded ? 1 : initialFraction)))
            ZStack(alignment: .bottom) {
                Button { isPresented = false } label: {
                    Color.black.opacity(0.2).contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel("关闭抽屉").accessibilityIdentifier("device-drawer-backdrop")
                VStack(spacing: 0) {
                    Button { expanded.toggle() } label: {
                        Capsule().fill(style.color("text-2").opacity(0.35))
                            .frame(width: 42, height: 4)
                            .frame(maxWidth: .infinity).frame(height: 24).contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(expanded ? "收起抽屉" : "展开抽屉")
                    .accessibilityIdentifier("device-drawer-handle")
                    .gesture(DragGesture(minimumDistance: 12).onEnded { value in
                        if value.translation.height < -40 { expanded = true }
                        else if value.translation.height > 60 {
                            if expanded { expanded = false }
                            else { isPresented = false }
                        }
                    })
                    content()
                        .frame(width: max(0, geometry.size.width - safeAreaInsets.leading - safeAreaInsets.trailing),
                               height: max(0, height - 24 - (keyboardVisible ? 0 : safeAreaInsets.bottom)))
                        .padding(.leading, safeAreaInsets.leading).padding(.trailing, safeAreaInsets.trailing)
                }
                .padding(.bottom, keyboardVisible ? 0 : safeAreaInsets.bottom)
                .frame(width: geometry.size.width, height: height)
                .background(style.color("bg"))
                .clipShape(UnevenRoundedRectangle(topLeadingRadius: 20, topTrailingRadius: 20))
                .accessibilityElement(children: .contain)
                .accessibilityAddTraits(.isModal)
                .accessibilityIdentifier("device-drawer")
            }
            .frame(width: geometry.size.width, height: usableHeight, alignment: .bottom)
            .animation(reduceMotion ? nil : .easeInOut(duration: 0.2), value: expanded)
        }
        .ignoresSafeArea()
        .onKeyPress(.escape) { isPresented = false; return .handled }
        .accessibilityAction(.escape) { isPresented = false }
    }
}

struct GatewaySetup: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore
    @State private var address = ""
    @State private var invalid = false
    @FocusState private var addressFocused: Bool

    var body: some View {
        NavigationStack {
            Form {
                Section("连接 AI 工作区") {
                    NativeFieldLabel(title: "服务地址", focused: addressFocused) { addressFocused = false }
                    TextField("https://fleet.example.com", text: $address)
                        .focused($addressFocused).submitLabel(.go).onSubmit(connect)
                        .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                        .accessibilityIdentifier("gateway-address")
                    Text("登录后访问电脑上的 AI 会话、终端和文件。手机无需安装电脑端 Agent。")
                        .font(.footnote).foregroundStyle(.secondary)
                    Button("连接", action: connect)
                    .accessibilityIdentifier("connect-gateway")
                    if invalid { Text("请输入 HTTPS 网关根地址，不包含账号、查询参数或子路径。") .foregroundStyle(.red) }
                }
            }
            .navigationTitle("Fleet Hub")
            .scrollDismissesKeyboard(.interactively)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    if store.gateway != nil { Button("取消") { addressFocused = false; store.settingsSheet = false; store.nativeSheet = nil } }
                }
            }
            .onAppear { address = store.gateway?.absoluteString ?? "" }
        }
    }

    private func connect() {
        invalid = !store.connect(address)
        if !invalid {
            addressFocused = false
            store.settingsSheet = false
            store.nativeSheet = nil
        }
    }
}
