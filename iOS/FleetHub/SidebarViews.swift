import SwiftUI

struct DevicePanel: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore
    var collapsed = false
    let collapse: () -> Void

    var body: some View {
        VStack(spacing: 16) {
            HStack(spacing: 10) {
                Button(action: collapsed ? collapse : {}) {
                    Image("FleetMark").resizable().scaledToFit().frame(width: 28, height: 28)
                }
                .buttonStyle(TitaniumIconButtonStyle(size: 30, foreground: "accent"))
                .accessibilityLabel(collapsed ? "展开设备栏" : "Fleet Hub")
                .accessibilityIdentifier("expand-devices")
                .disabled(!collapsed)
                if !collapsed {
                    Text("FLEET HUB").font(.system(size: 13, weight: .semibold, design: .monospaced)).tracking(0.8)
                    Spacer(minLength: 0)
                    Button(action: collapse) { Image(systemName: "chevron.left") }
                        .buttonStyle(TitaniumIconButtonStyle(size: 36)).accessibilityLabel("收起设备栏")
                        .accessibilityIdentifier("toggle-devices")
                }
            }.padding(.leading, 6).frame(height: 42)
            FleetModePicker(store: store, collapsed: collapsed)
            ScrollView {
                VStack(alignment: .leading, spacing: 5) {
                if !collapsed {
                    Text(store.isFiles ? "文件设备" : "会话设备")
                        .font(TitaniumStyle.font("caption", weight: .semibold))
                        .foregroundStyle(style.color("text-2"))
                        .padding(.leading, 8).padding(.top, 8).frame(height: 35, alignment: .topLeading)
                }
                ForEach(store.devices) { device in
                    deviceRow(id: device.id, name: device.name, symbol: device.symbol, online: device.online,
                              color: device.color, count: device.count, device: device)
                        .contextMenu {
                            Button("设备设置", systemImage: "slider.horizontal.3") { store.showOverlay("host", device: device.id) }
                        }
                }
                if !store.isFiles {
                    deviceRow(id: "all", name: "全部设备", symbol: "square.grid.2x2", online: nil, color: nil, count: nil)
                }
                if !collapsed {
                    if store.snapshot?.gatewayDown == true {
                        Label("网关暂时不可达", systemImage: "wifi.exclamationmark").foregroundStyle(.secondary)
                    } else if store.devices.isEmpty {
                        Text("暂无已入网设备").foregroundStyle(.secondary)
                    }
                }
                }
            }
            .refreshable { store.send("refresh") }
            FleetSettingsMenu(store: store, expanded: !collapsed)
        }
        .padding(.horizontal, 12).padding(.vertical, 16)
        .background(style.color("surface"))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("device-panel")
    }

    private func deviceRow(id: String, name: String, symbol: String, online: Bool?, color: String?, count: Int?, device: FleetDevice? = nil) -> some View {
        let selected = store.snapshot?.deviceScope == id
        return HStack(spacing: 0) {
            Button { store.selectDevice(id) } label: {
                HStack(spacing: 10) {
                    if let device {
                        FleetDeviceIcon(device: device)
                    } else {
                        Image(systemName: symbol).font(.system(size: 20))
                            .foregroundStyle(style.color("accent-text")).frame(width: 22, height: 22)
                    }
                    if !collapsed {
                        Text(name).lineLimit(1)
                        Spacer(minLength: 0)
                        if id == "all" {
                            Text("\(store.devices.filter(\.online).count)/\(store.devices.count) 在线").font(TitaniumStyle.font("caption")).fixedSize()
                        } else if online == false {
                            Text("离线").font(TitaniumStyle.font("caption")).fixedSize()
                        } else if let count, count > 0 {
                            Text("\(count)").font(TitaniumStyle.font("caption"))
                        }
                    }
                }
                .font(TitaniumStyle.font("body", weight: .medium))
                .foregroundStyle(style.color(selected ? "accent-text" : "text"))
                .padding(.leading, 11).padding(.trailing, id == "all" ? 11 : 0)
                .frame(maxWidth: .infinity, minHeight: 42)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain).accessibilityIdentifier("device-\(id)")
            .accessibilityLabel(name).accessibilityValue(online == nil ? "" : online == true ? "在线" : "离线")
            .accessibilityAddTraits(selected ? .isSelected : [])
            if !collapsed && id != "all" {
                Button { store.showOverlay("host", device: id) } label: { Image(systemName: "info.circle") }
                    .buttonStyle(TitaniumIconButtonStyle(size: 24)).accessibilityLabel("\(name) 设备设置")
                    .padding(.trailing, 11)
            }
        }
        .frame(height: 42)
        .background(selected ? style.selection : .clear,
                    in: RoundedRectangle(cornerRadius: TitaniumStyle.radius("control")))
        .disabled(store.busy)
    }
}

struct DevicePicker: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                Text(store.isFiles ? "选择文件设备" : "选择会话设备").font(TitaniumStyle.font("title", weight: .semibold))
                Spacer()
                Button { store.deviceSheet = false } label: { Image(systemName: "xmark") }
                    .buttonStyle(TitaniumIconButtonStyle()).accessibilityLabel("完成")
            }
            Text(store.isFiles ? "选择设备浏览文件" : "可汇总全部在线设备的未归档会话")
                .font(TitaniumStyle.font("secondary")).foregroundStyle(style.color("text-2"))
            ScrollView {
                LazyVStack(spacing: 4) {
                    if !store.isFiles { row(nil) }
                    ForEach(store.devices) { row($0) }
                }
            }
        }.padding(18).background(style.color("bg"))
    }

    private func row(_ device: FleetDevice?) -> some View {
        let identifier = device?.id ?? "all"
        let name = device?.name ?? "全部设备"
        let selected = store.snapshot?.deviceScope == identifier
        return HStack(spacing: 4) {
            Button { store.selectDevice(identifier) } label: {
                HStack(spacing: 14) {
                    if let device {
                        FleetDeviceIcon(device: device, size: 28).frame(width: 32)
                    } else {
                        Image(systemName: "square.grid.2x2").font(.system(size: 24))
                            .foregroundStyle(style.color("accent-text")).frame(width: 32)
                    }
                    VStack(alignment: .leading, spacing: 4) {
                        Text(name).font(TitaniumStyle.font("body", weight: .semibold)).lineLimit(1)
                        Text(device.map { "\($0.count ?? 0) 个近期会话" } ?? "\(store.devices.filter(\.online).count)/\(store.devices.count) 台设备在线")
                            .font(TitaniumStyle.font("caption")).foregroundStyle(style.color("text-2"))
                    }
                    Spacer(minLength: 0)
                    if selected { Image(systemName: "checkmark") }
                    else if let device {
                        Text(device.online ? "在线" : "离线").font(TitaniumStyle.font("caption"))
                    }
                }.frame(maxWidth: .infinity, minHeight: 62).contentShape(Rectangle())
            }.buttonStyle(.plain).accessibilityLabel(name).accessibilityIdentifier("device-\(identifier)")
            if let device {
                Button {
                    store.deviceSheet = false
                    store.showOverlay("host", device: device.id)
                } label: { Image(systemName: "info.circle") }
                .buttonStyle(TitaniumIconButtonStyle()).accessibilityLabel("\(device.name) 设备设置")
            }
        }
        .padding(.horizontal, 10).foregroundStyle(style.color("text"))
        .background(selected ? style.selection : .clear, in: RoundedRectangle(cornerRadius: TitaniumStyle.radius("control")))
        .disabled(store.busy)
    }
}

struct FleetModePicker: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore
    var collapsed = false
    var mobile = false

    var body: some View {
        Group {
            if mobile { HStack(spacing: 20) { buttons } }
            else if collapsed { VStack(spacing: 4) { buttons } }
            else { HStack(spacing: 4) { buttons } }
        }
        .padding(mobile ? 0 : 4)
        .background(mobile ? .clear : style.color("surface-2"), in: RoundedRectangle(cornerRadius: TitaniumStyle.radius("control")))
        .fixedSize(horizontal: mobile, vertical: false)
    }

    private var buttons: some View {
        ForEach([false, true], id: \.self) { files in
            Button { store.setMode(files: files) } label: {
                HStack(spacing: 7) {
                    if !mobile { Image(systemName: files ? "doc.text" : "terminal").font(.system(size: 15)) }
                    if !collapsed { Text(files ? "文件" : "会话") }
                }
                .font(TitaniumStyle.font(mobile ? "page" : "body", weight: mobile && store.isFiles == files ? .bold : .medium))
                .padding(.horizontal, mobile ? 0 : 10)
                .frame(maxWidth: mobile ? nil : .infinity, minHeight: mobile ? 44 : 36, alignment: .leading)
                .contentShape(Rectangle())
                .foregroundStyle(style.color(mobile ? (store.isFiles == files ? "accent" : "text") : (store.isFiles == files ? "accent-text" : "text-1")))
                .background(!mobile && store.isFiles == files ? style.selection : .clear,
                            in: RoundedRectangle(cornerRadius: TitaniumStyle.radius(mobile ? "control" : "compact")))
            }
            .buttonStyle(.plain).disabled(store.busy)
            .accessibilityLabel(files ? "文件" : "会话")
            .accessibilityIdentifier(files ? "mode-files" : "mode-sessions")
            #if DEBUG
            .accessibilityValue(style.tokens["accent"] ?? "")
            #endif
            .accessibilityAddTraits(store.isFiles == files ? .isSelected : [])
        }
    }
}

struct FleetSettingsMenu: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore
    var expanded = false

    var body: some View {
        Button { store.showOverlay("settings-page") } label: {
            HStack(spacing: 8) {
                Image(systemName: expanded ? "person.crop.circle" : "gearshape")
                    .font(.system(size: expanded ? 15 : 17))
                    .frame(width: expanded ? 28 : nil, height: expanded ? 28 : nil)
                    .background(.clear, in: Circle())
                if expanded {
                    Text(store.snapshot?.email ?? "设置").font(TitaniumStyle.font("body", weight: .medium)).lineLimit(1)
                    Spacer(minLength: 0)
                    Image(systemName: "chevron.down").font(.caption2)
                }
            }
            .padding(.horizontal, expanded ? 10 : 0)
            .font(.system(size: 17, weight: .medium))
            .foregroundStyle(style.color("text-1"))
            .frame(width: expanded ? nil : 44, height: 44).contentShape(Rectangle())
        }
        .buttonStyle(.plain).disabled(store.busy || !store.ready)
        .accessibilityLabel("设置").accessibilityIdentifier("workspace-menu")
    }
}

struct SessionPanel: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore
    var compact = false
    let pinned: Bool
    let togglePin: () -> Void
    @State private var renameTarget: FleetSession?
    @State private var renameValue = ""
    @State private var deleteTarget: FleetSession?

    private var recent: Bool { store.snapshot?.view == "recent" }
    private var ordered: [FleetSession] {
        store.sessions.sorted { $0.pinned != $1.pinned ? $0.pinned : $0.mtime > $1.mtime }
    }

    var body: some View {
        VStack(spacing: 0) {
            VStack(spacing: compact ? 10 : 9) {
            HStack(spacing: 10) {
                assistantPicker
                if !compact {
                Button(action: togglePin) { Image(systemName: pinned ? "pin.fill" : "pin.slash") }
                    .buttonStyle(TitaniumIconButtonStyle(size: 36, foreground: pinned ? "accent-text" : "text-1", background: pinned ? style.selection : .clear, radius: "compact"))
                    .accessibilityLabel(pinned ? "取消固定会话列表" : "固定会话列表")
                    .accessibilityIdentifier("pin-sessions")
                    .accessibilityValue(pinned ? "固定" : "未固定")
                }
            }
            HStack(spacing: 7) {
                Button { store.send("archive") } label: { Image(systemName: "archivebox") }
                    .buttonStyle(TitaniumIconButtonStyle(size: compact ? 44 : 36, foreground: store.snapshot?.archived == true ? "accent-text" : "text-1", background: store.snapshot?.archived == true ? style.selection : .clear, radius: compact ? "control" : "compact"))
                    .accessibilityLabel(store.snapshot?.archived == true ? "显示当前会话" : "显示已归档会话")
                    .accessibilityIdentifier("session-archive-toggle")
                Button { store.send("view", values: ["view": recent ? "project" : "recent"]) } label: {
                    Image(systemName: recent ? "folder" : "clock")
                }
                .buttonStyle(TitaniumIconButtonStyle(size: compact ? 44 : 36, foreground: recent ? "accent-text" : "text-1", background: recent ? style.selection : .clear, radius: compact ? "control" : "compact")).accessibilityLabel(recent ? "按项目分组" : "按最近更新排序")
                .accessibilityIdentifier("session-view-toggle")
                HStack(spacing: 7) {
                    Image(systemName: "magnifyingglass").font(.system(size: 15)).foregroundStyle(style.color("text-2"))
                    TextField(store.snapshot?.archived == true ? "搜索已归档会话" : "搜索全部未归档会话", text: $store.search)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                        .accessibilityIdentifier("session-search")
                        .font(TitaniumStyle.font(compact ? "control" : "secondary"))
                    if !store.search.isEmpty {
                        Button { store.search = "" } label: { Image(systemName: "xmark.circle.fill") }.accessibilityLabel("清除搜索")
                    }
                }
                .padding(.horizontal, 9).frame(height: compact ? 44 : 36)
                .background(style.color("surface-2"), in: RoundedRectangle(cornerRadius: TitaniumStyle.radius(compact ? "control" : "compact")))
                Button(action: newSession) { Image(systemName: "plus") }
                    .buttonStyle(TitaniumIconButtonStyle(size: compact ? 44 : 36, foreground: "accent-contrast", background: style.color("accent"), radius: compact ? "control" : "compact")).accessibilityLabel("新建无项目会话")
                    .accessibilityIdentifier("new-session").disabled(!store.canCreateSession)
            }
            }.padding(.horizontal, compact ? 14 : 10).padding(.top, compact ? 10 : 14).padding(.bottom, 10).disabled(store.busy)
            if store.snapshot?.assistant == .dsh {
                if store.snapshot?.degraded == true {
                    Text("DSH Desktop 未运行，当前仅显示磁盘会话（只读）").font(.footnote).padding(8)
                }
            }
            ScrollView {
                LazyVStack(alignment: .leading, spacing: compact ? 5 : 8) {
                if store.snapshot?.loading == true && store.sessions.isEmpty { ProgressView("正在读取会话…") }
                if recent {
                    ForEach(ordered) { sessionRow($0) }
                } else {
                    ForEach(store.projects) { project in
                        VStack(alignment: .leading, spacing: 2) {
                            projectHeader(project)
                            if !project.collapsed {
                                VStack(alignment: .leading, spacing: 0) {
                                if project.sessionIDs.isEmpty {
                                    Text("暂无会话").font(TitaniumStyle.font("caption"))
                                        .foregroundStyle(style.color("text-2"))
                                        .padding(.horizontal, 10).padding(.top, 8).padding(.bottom, 12)
                                }
                                ForEach(project.sessionIDs.compactMap { identifier in store.sessions.first(where: { $0.id == identifier }) }) {
                                    sessionRow($0)
                                }
                                }.padding(.leading, 12).padding(.trailing, 4)
                            }
                        }
                    }
                }
                if store.sessions.isEmpty && (recent || store.projects.isEmpty) && store.snapshot?.loading != true {
                    Text(emptyMessage).font(TitaniumStyle.font("secondary"))
                        .foregroundStyle(style.color("text-2"))
                        .frame(maxWidth: .infinity).padding(.vertical, 24)
                }
                ForEach(store.snapshot?.errors ?? [], id: \.self) {
                    Text($0).font(TitaniumStyle.font("secondary")).foregroundStyle(style.color("text-2")).padding(10)
                }
                if store.snapshot?.hasMore == true {
                    Button("加载更多会话") { store.send("more") }
                        .disabled(store.busy || store.snapshot?.loading == true)
                        .onAppear { if !store.busy && store.snapshot?.loading != true { store.send("more") } }
                }
                }.padding(compact ? 8 : 10)
            }
            .refreshable { store.send("refresh") }
        }
        .onChange(of: store.search) { _, _ in store.searchChanged() }
        .background(style.color("bg")).accessibilityElement(children: .contain)
        .accessibilityIdentifier("session-panel")
        .alert("重命名会话", isPresented: Binding(get: { renameTarget != nil }, set: { if !$0 { renameTarget = nil } }), presenting: renameTarget) { session in
            TextField("会话名称", text: $renameValue)
            Button("取消", role: .cancel) { renameTarget = nil }
            Button("保存") {
                store.action("rename", session: session, value: renameValue)
                renameTarget = nil
            }.keyboardShortcut(.defaultAction)
                .disabled(renameValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }
        .alert("永久删除会话？", isPresented: Binding(get: { deleteTarget != nil }, set: { if !$0 { deleteTarget = nil } }), presenting: deleteTarget) { session in
            Button("取消", role: .cancel) { deleteTarget = nil }
            Button("删除", role: .destructive) {
                store.action("delete", session: session)
                deleteTarget = nil
            }
        } message: { session in Text("“\(session.title)”将被永久删除，此操作无法撤销。") }
    }

    private var emptyMessage: String {
        if !store.search.isEmpty { return "没有匹配的会话" }
        if !store.devices.isEmpty && !store.devices.contains(where: \.online) { return "设备均处于离线状态" }
        return store.snapshot?.archived == true ? "没有已归档会话" : "没有未归档会话"
    }

    private var assistantPicker: some View {
        HStack(spacing: 4) {
            ForEach(store.assistants) { assistant in
                HStack(spacing: 0) {
                    Button {
                        store.showingDetail = false
                        store.search = ""
                        store.send("assistant", values: ["assistant": assistant.rawValue])
                    } label: {
                        Text(assistant.title).font(TitaniumStyle.font("body", weight: .medium))
                            .frame(maxWidth: .infinity, minHeight: compact ? 44 : 31)
                    }
                    .buttonStyle(.plain).accessibilityAddTraits(store.snapshot?.assistant == assistant ? .isSelected : [])
                    if assistant == .dsh {
                        Button { if let path = store.snapshot?.dshNativeURL { store.openExternal(path) } } label: {
                            Image("DeepSeek").resizable().renderingMode(.template).scaledToFit().frame(width: 22, height: 18)
                        }
                            .buttonStyle(TitaniumIconButtonStyle(size: compact ? 44 : 26))
                            .accessibilityLabel("打开 DeepSeek Harness")
                            .disabled(store.snapshot?.dshNativeURL == nil || store.busy)
                            .help(store.snapshot?.dshNativeHint ?? "")
                    }
                }
                .background(store.snapshot?.assistant == assistant ? style.color("surface-1") : .clear,
                            in: RoundedRectangle(cornerRadius: TitaniumStyle.radius(compact ? "control" : "compact")))
            }
        }
        .padding(4).foregroundStyle(style.color("text"))
        .background(style.color("surface-2"), in: RoundedRectangle(cornerRadius: TitaniumStyle.radius(compact ? "card" : "control")))
    }

    private func newSession() {
        if store.snapshot?.deviceScope == "all" { store.nativeSheet = .newSessionDevice }
        else { store.newSession() }
    }

    private func projectHeader(_ project: FleetProject) -> some View {
        HStack(spacing: 2) {
            Button { store.send("collapse-project", values: ["project": project.id]) } label: {
                HStack(spacing: 9) {
                    Image(systemName: project.collapsed ? "chevron.right" : "chevron.down")
                        .font(.system(size: 12)).frame(width: 16).foregroundStyle(style.color("text-1"))
                    Text(project.title).fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 0)
                }.padding(.leading, compact ? 8 : 10).padding(.trailing, 4)
                    .padding(.vertical, 9).frame(minHeight: compact ? 42 : 39).contentShape(Rectangle())
            }
            .buttonStyle(.plain).accessibilityIdentifier("project-\(project.id)")
            .accessibilityValue(project.collapsed ? "收起" : "展开")
            if !project.projectless && !project.cwd.isEmpty {
                Button { store.newSession(project: project) } label: { Image(systemName: "plus") }
                    .buttonStyle(TitaniumIconButtonStyle(size: compact ? 34 : 30)).accessibilityLabel("在 \(project.title) 中新建会话")
                    .accessibilityIdentifier("new-project-\(project.id)")
                    .disabled(store.busy || !store.devices.contains(where: { $0.id == project.macId && $0.online }))
                    .help(project.cwd)
            }
        }
        .padding(.trailing, compact ? 4 : 6)
        .font(TitaniumStyle.font("body", weight: .semibold)).foregroundStyle(style.color("text"))
        .disabled(store.busy)
    }

    private func sessionRow(_ session: FleetSession) -> some View {
        return VStack(alignment: .leading, spacing: compact ? 1 : 2) {
            HStack(spacing: 2) {
            Button { store.open(session) } label: {
                HStack(spacing: 9) {
                    Text(session.title.isEmpty ? "(无标题)" : session.title)
                        .font(TitaniumStyle.font(compact ? "title" : "body", weight: .medium)).lineLimit(1)
                    Spacer(minLength: 0)
                    if session.pinned { Image(systemName: "pin.fill").font(.caption) }
                }
                .frame(maxWidth: .infinity, minHeight: compact ? 44 : 25).contentShape(Rectangle())
            }.buttonStyle(.plain).accessibilityIdentifier("session-\(session.sessionId)")
                .accessibilityValue(session.status ?? "已读")
            if session.running {
                ProgressView().controlSize(.mini).tint(style.color("accent")).frame(width: 14, height: 14)
                    .accessibilityLabel(session.status ?? "进行中")
            } else if session.waiting || session.unread {
                Circle().fill(style.color(session.waiting ? "wait" : "accent")).frame(width: 8, height: 8)
                    .padding(.leading, 2).accessibilityLabel(session.status ?? "未读")
            }
            }
            if store.snapshot?.deviceScope == "all" || recent {
                HStack(spacing: 5) {
                    if store.snapshot?.deviceScope == "all" {
                        if let device = store.devices.first(where: { $0.id == session.macId }) {
                            FleetDeviceIcon(device: device, size: compact ? 12 : 11, monochrome: true)
                        }
                        Text(store.devices.first(where: { $0.id == session.macId })?.name ?? session.macId)
                    }
                    if recent { Text(session.project) }
                }
                .font(TitaniumStyle.font(compact ? "secondary" : "caption"))
                .foregroundStyle(style.color("text-2")).lineLimit(1)
                .frame(height: compact ? 17 : 16, alignment: .leading)
            }
        }
        .padding(.vertical, compact ? 2 : 4).padding(.leading, compact ? 8 : 11).padding(.trailing, compact ? 8 : 7)
        .foregroundStyle(style.color("text"))
        .background(store.snapshot?.selectedID == session.id ? style.selection : .clear,
                    in: RoundedRectangle(cornerRadius: TitaniumStyle.radius("control")))
        .disabled(store.busy || !store.devices.contains(where: { $0.id == session.macId && $0.online }))
        .contextMenu { sessionActions(session) }
    }

    @ViewBuilder private func sessionActions(_ session: FleetSession) -> some View {
        Button("打开会话") { store.open(session) }
        let actions = session.actions ?? []
        let archive = store.snapshot?.archived == true ? "unarchive" : "archive"
        if actions.contains(archive) {
            Button(archive == "archive" ? "归档会话" : "移回当前会话", systemImage: archive == "archive" ? "archivebox" : "tray.and.arrow.up") {
                store.action(archive, session: session)
            }
        }
        let pin = session.pinned ? "unpin" : "pin"
        if actions.contains(pin) { Button(session.pinned ? "取消置顶" : "置顶") { store.action(pin, session: session) } }
        if actions.contains("rename") { Button("重命名") { renameValue = session.title; renameTarget = session } }
        if actions.contains("delete") { Button("删除", role: .destructive) { deleteTarget = session } }
    }
}
