import SwiftUI

struct FleetWorkspace: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore
    @AppStorage("fleet.devices.visible") private var devicesVisible = true
    @AppStorage("fleet.sessions.visible") private var sessionsPinned = true
    @State private var sessionsOpen = false
    @State private var backProgress: CGFloat = 0
    @State private var backDragging = false
    @State private var backSettling = false
    @State private var backGestureRejected = false
    @State private var backTransitionID = UUID()
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var backTransitionActive: Bool { backDragging || backSettling }

    var body: some View {
        GeometryReader { geometry in
            let layout = FleetLayout.resolve(width: geometry.size.width)
            let railWidth = store.ready && layout != .compact ? (devicesVisible ? layout.deviceWidth : Double(TitaniumStyle.metric("collapsedDeviceWidth"))) : 0
            let showSessions = store.ready && !store.isFiles &&
                (layout == .compact ? (!store.showingDetail || backTransitionActive) : sessionsPinned)
            let showToolbar = store.authentication == nil && store.gateway != nil && (!store.ready || (layout == .compact && (!store.showingDetail || store.isFiles)))
            let webOverlay = store.nativeSheet == nil && store.webDialog == nil && (store.webOverlayVisible || store.snapshot?.overlay != nil)
            let compactDetail = layout == .compact && store.showingDetail && !store.isFiles
            let canSwipeBack = compactDetail && store.ready && store.authentication == nil && !webOverlay &&
                store.nativeSheet == nil && store.webDialog == nil && !store.deviceSheet && store.auxiliaryPage == nil
            let detailOffset = compactDetail && !webOverlay ? geometry.size.width * backProgress : 0
            ZStack {
                FleetWebView(store: store, layout: layout, sessionsPinned: sessionsPinned)
                    .allowsHitTesting(store.authentication == nil && !backTransitionActive &&
                        !(showSessions && layout == .compact && !store.showingDetail && !webOverlay))
                    .padding(.leading, webOverlay ? 0 : railWidth + (showSessions && layout != .compact ? layout.sessionWidth : 0))
                    .padding(.top, webOverlay ? 0 : showToolbar ? 58 : 0)
                    .opacity(store.authentication != nil || (showSessions && layout == .compact && !store.showingDetail && !webOverlay) ? 0 : 1)
                    .accessibilityHidden(store.authentication != nil || (showSessions && layout == .compact && !store.showingDetail && !webOverlay))
                    .ignoresSafeArea(.container, edges: webOverlay ? .all : .bottom)
                    .background(webOverlay ? .clear : style.color("bg"))
                    .shadow(color: .black.opacity(backTransitionActive ? 0.16 : 0), radius: 14, x: -5, y: 0)
                    .overlay(alignment: .top) {
                        if store.ready && compactDetail && !webOverlay && store.authentication == nil {
                            HStack {
                                Button { settleBackTransition(complete: true) } label: { Image(systemName: "arrow.left") }
                                    .accessibilityLabel("返回会话列表").accessibilityIdentifier("toggle-sessions")
                                    .disabled(backSettling)
                                Spacer(minLength: 0)
                            }
                            .buttonStyle(TitaniumIconButtonStyle()).padding(.horizontal, 14).frame(height: 44)
                            .accessibilityElement(children: .contain)
                            .accessibilityValue(store.snapshot?.theme ?? "light").accessibilityIdentifier("workspace-title")
                        }
                    }
                    .offset(x: detailOffset)
                    .zIndex(webOverlay ? 3 : compactDetail ? 1 : 0)
            VStack(spacing: 0) {
                if showToolbar || (compactDetail && backTransitionActive) { toolbar(layout) }
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
            .background(layout == .compact && showSessions ? style.color("bg") : .clear)
            .overlay {
                if compactDetail && backTransitionActive {
                    Color.black.opacity(0.08 * (1 - backProgress)).allowsHitTesting(false)
                }
            }
            .offset(x: compactDetail && backTransitionActive ? -geometry.size.width * 0.25 * (1 - backProgress) : 0)
            .blur(radius: webOverlay ? 3 : 0)
            .allowsHitTesting(!webOverlay && store.authentication == nil && !backTransitionActive)
            .accessibilityHidden(webOverlay || store.authentication != nil || compactDetail)
            if let authentication = store.authentication {
                NativeAuthenticationView(store: store, model: authentication).id(authentication.id)
                    .zIndex(4)
            }
            }
            .simultaneousGesture(DragGesture(minimumDistance: 12)
                .onChanged { value in
                    guard canSwipeBack, !backSettling, !backGestureRejected else { return }
                    if !backDragging {
                        guard value.startLocation.x < 28, value.translation.width > abs(value.translation.height) else {
                            backGestureRejected = true
                            return
                        }
                        backDragging = true
                        store.webView?.endEditing(true)
                    }
                    backProgress = min(1, max(0, value.translation.width / max(geometry.size.width, 1)))
                }
                .onEnded { value in
                    backGestureRejected = false
                    guard backDragging else { return }
                    let width = max(geometry.size.width, 1)
                    let complete = canSwipeBack && (value.translation.width > width * 0.35 ||
                        (value.translation.width > 36 && value.predictedEndTranslation.width > width * 0.55))
                    settleBackTransition(complete: complete)
                })
            .background(style.color("bg"))
            .accessibilityHidden(store.deviceSheet || store.nativeSheet != nil || store.webDialog != nil)
            .sheet(item: $store.nativeSheet) { sheet in
                nativePanel(sheet, expanded: layout != .compact)
                    .nativeSheetSurface(locked: store.drawerLocked || store.drawerBusy)
            }
            .sheet(isPresented: $store.deviceSheet) {
                DevicePicker(store: store)
                    .nativeSheetSurface(detents: [.medium, .large])
            }
            .sheet(item: webSheetBinding, onDismiss: closeWebSheet) { dialog in
                NativeWebDialogPanel(store: store, dialog: dialog)
                    .nativeSheetSurface()
            }
            .background(NativeWebPresentation(store: store))
            .background(NativeWebAlertPresenter(store: store))
            .animation(reduceMotion ? nil : .easeInOut(duration: 0.2), value: devicesVisible)
            .animation(reduceMotion ? nil : .easeInOut(duration: 0.2), value: sessionsPinned)
            .onChange(of: store.openedSequence) { _, _ in sessionsOpen = false; resetBackTransition() }
            .onChange(of: layout == .compact) { _, _ in sessionsOpen = false; resetBackTransition() }
            .onChange(of: store.isFiles) { _, _ in sessionsOpen = false; resetBackTransition() }
            .onChange(of: store.ready) { _, ready in if !ready { sessionsOpen = false; resetBackTransition() } }
            .onChange(of: canSwipeBack) { _, allowed in if !allowed { resetBackTransition() } }
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

        }
    }

    private func settleBackTransition(complete: Bool) {
        guard !backSettling else { return }
        backDragging = false
        backSettling = true
        let identifier = backTransitionID
        let animation: Animation? = reduceMotion ? nil : .interactiveSpring(response: 0.3, dampingFraction: 1)
        withAnimation(animation, completionCriteria: .removed) {
            backProgress = complete ? 1 : 0
        } completion: {
            guard backTransitionID == identifier else { return }
            // Keep the chat runtime visible until the native slide finishes.
            var transaction = Transaction()
            transaction.disablesAnimations = true
            withTransaction(transaction) {
                if complete { store.backToList() }
                resetBackTransition()
            }
        }
    }

    private func resetBackTransition() {
        backTransitionID = UUID()
        backProgress = 0
        backDragging = false
        backSettling = false
        backGestureRejected = false
    }

    private var webSheetBinding: Binding<NativeWebDialog?> {
        Binding(get: { store.webDialog?.presentation == .sheet ? store.webDialog : nil },
                set: { if $0 == nil, store.webDialog?.presentation == .sheet { store.dialogAction("__close") } })
    }
    private func closeWebSheet() {
        if store.webDialog?.presentation == .sheet { store.dialogAction("__close") }
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
                case .newSessionDevice:
            NavigationStack {
                List(store.devices) { device in
                    Button {
                        store.nativeSheet = nil
                        store.newSession(device: device.id)
                    } label: {
                        HStack(spacing: 12) { FleetDeviceIcon(device: device); Text(device.name); Spacer(); Text(device.online ? "在线" : "离线").font(.caption).foregroundStyle(.secondary) }
                            .frame(minHeight: 44)
                    }.disabled(!device.online || store.busy)
                }
                .navigationTitle("选择设备").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .cancellationAction) { Button("取消") { store.nativeSheet = nil } } }
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

// System presentations own the dimming, safe areas, keyboard avoidance and sheet stacking.
struct NativeSheetSurface: ViewModifier {
    @Environment(\.fleetStyle) private var style
    var detents: Set<PresentationDetent> = [.large]
    var locked = false
    func body(content: Content) -> some View {
        content.environment(\.colorScheme, style.colorScheme ?? .light)
            .preferredColorScheme(style.followsSystem ? nil : style.colorScheme)
            .presentationDetents(detents)
            .presentationDragIndicator(.visible)
            .presentationBackground(NativeSettingsLayout.background)
            .interactiveDismissDisabled(locked)
            .tint(style.color("accent-text"))
    }
}

extension View {
    func nativeSheetSurface(detents: Set<PresentationDetent> = [.large], locked: Bool = false) -> some View {
        modifier(NativeSheetSurface(detents: detents, locked: locked))
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
                    TextField("服务地址", text: $address, prompt: Text("https://fleet.example.com").foregroundStyle(.secondary))
                        .foregroundStyle(style.color("text"))
                        .focused($addressFocused).submitLabel(.go).onSubmit(connect)
                        .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                        .accessibilityIdentifier("gateway-address")
                    Button(action: connect) {
                        Text("连接").frame(maxWidth: .infinity)
                    }
                    .buttonStyle(NativePrimaryButtonStyle())
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
