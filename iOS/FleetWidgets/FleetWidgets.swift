import AppIntents
import SwiftUI
import WidgetKit

struct StandbyEntry: TimelineEntry {
    let date: Date
    let snapshot: StandbySnapshot?
    var page = 0
    var stale = false
}

struct StandbyProvider: TimelineProvider {
    func placeholder(in context: Context) -> StandbyEntry { StandbyEntry(date: Date(), snapshot: nil) }
    func getSnapshot(in context: Context, completion: @escaping (StandbyEntry) -> Void) {
        completion(StandbyEntry(date: Date(), snapshot: StandbyStorage.snapshot()))
    }
    func getTimeline(in context: Context, completion: @escaping (Timeline<StandbyEntry>) -> Void) {
        Task {
            var snapshot = StandbyStorage.snapshot()
            var stale = true
            if let connection = StandbyStorage.connection() {
                do { snapshot = try await StandbyStorage.fetch(connection); stale = false }
                catch { snapshot = StandbyStorage.snapshot() }
            }
            let now = Date()
            // No eight-second polling: WidgetKit controls the timeline cadence.
            // Locally rotate whole rows using entries, without network requests.
            let expiry = snapshot?.updatedAt.addingTimeInterval(30 * 60) ?? now
            let nextDay = Calendar.current.date(byAdding: .day, value: 1, to: Calendar.current.startOfDay(for: now))!
            let end = min(expiry, nextDay)
            let entries = (0..<61).map { minute -> StandbyEntry in
                let date = now.addingTimeInterval(Double(minute) * 60)
                // Continue the clock even when cached private data has expired.
                return StandbyEntry(date: date, snapshot: date < end ? snapshot : nil, page: minute, stale: stale)
            }
            // Never leave yesterday's totals or revoked/old session titles on an
            // indefinite timeline when iOS defers the next network refresh.
            completion(Timeline(entries: entries, policy: .after(now.addingTimeInterval(15 * 60))))
        }
    }
}

struct ToggleStandbyClock: AppIntent {
    static var title: LocalizedStringResource = "切换时钟"
    static var openAppWhenRun = false
    func perform() async throws -> some IntentResult {
        let defaults = StandbyStorage.defaults
        defaults?.set(!(defaults?.bool(forKey: "standby.analog") ?? false), forKey: "standby.analog")
        return .result()
    }
}

private let standbyAccent = Color(red: 0.55, green: 0.75, blue: 0.65)

struct StandbyClock: View {
    let date: Date
    var body: some View {
        Button(intent: ToggleStandbyClock()) {
            Group {
                if StandbyStorage.defaults?.bool(forKey: "standby.analog") == true {
                    Canvas { context, size in
                        let center = CGPoint(x: size.width / 2, y: size.height / 2)
                        let radius = min(size.width, size.height) / 2 - 3
                        for tick in 0..<60 {
                            let angle = Double(tick) * .pi / 30
                            let length: CGFloat = tick % 5 == 0 ? 5 : 2
                            var p = Path()
                            p.move(to: CGPoint(x: center.x + sin(angle) * (radius - length), y: center.y - cos(angle) * (radius - length)))
                            p.addLine(to: CGPoint(x: center.x + sin(angle) * radius, y: center.y - cos(angle) * radius))
                            context.stroke(p, with: .color(.primary.opacity(tick % 5 == 0 ? 0.8 : 0.3)), lineWidth: tick % 5 == 0 ? 1.5 : 1)
                        }
                        let parts = Calendar.current.dateComponents([.hour, .minute], from: date)
                        for (value, length, width, color) in [(Double((parts.hour ?? 0) % 12) / 12 + Double(parts.minute ?? 0) / 720, 0.5, 3.5, Color.primary), (Double(parts.minute ?? 0) / 60, 0.76, 2.0, standbyAccent)] {
                            let angle = value * 2 * .pi
                            var p = Path(); p.move(to: center)
                            p.addLine(to: CGPoint(x: center.x + sin(angle) * radius * length, y: center.y - cos(angle) * radius * length))
                            context.stroke(p, with: .color(color), style: StrokeStyle(lineWidth: width, lineCap: .round))
                        }
                        context.fill(Path(ellipseIn: CGRect(x: center.x - 2, y: center.y - 2, width: 4, height: 4)), with: .color(standbyAccent))
                    }
                    .aspectRatio(1, contentMode: .fit)
                } else {
                    Text(StandbyStorage.clockText(date))
                        .font(.system(size: 56, weight: .semibold, design: .rounded)).monospacedDigit()
                        .minimumScaleFactor(0.5).lineLimit(1)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel("时钟，点击切换数字或指针")
    }
}

struct UsageWidgetView: View {
    let entry: StandbyEntry
    @Environment(\.widgetFamily) private var family
    var body: some View {
        Group {
            if family == .systemMedium {
                HStack(spacing: 18) {
                    VStack(spacing: 0) { StandbyClock(date: entry.date); Text(entry.date, format: .dateTime.month().day().weekday()).font(.caption2).foregroundStyle(.secondary) }.frame(maxWidth: .infinity)
                    VStack(alignment: .leading, spacing: 10) { usage }.frame(maxWidth: .infinity)
                }
            } else {
                VStack(spacing: 5) { StandbyClock(date: entry.date); usage }
            }
        }
        .containerBackground(.background, for: .widget)
        .privacySensitive()
    }
    @ViewBuilder private var usage: some View {
        if let value = entry.snapshot {
            HStack(alignment: .firstTextBaseline) {
                Text(family == .systemMedium ? "今日模型用量" : "今日用量").font(.caption.weight(.semibold))
                Spacer(minLength: 3)
                Text(value.reportingHosts == 0 && !value.hosts.isEmpty ? "—" : StandbyStorage.compact(value.totalTokens))
                    .font(.system(size: 19, weight: .semibold, design: .rounded)).minimumScaleFactor(0.6).lineLimit(1)
            }
            if !value.models.isEmpty {
                let count = family == .systemMedium ? 3 : 1
                ForEach(0..<min(count, value.models.count), id: \.self) { offset in
                    let model = value.models[(entry.page + offset) % value.models.count]
                    HStack { Text(model.model).lineLimit(1); Spacer(minLength: 3); Text(StandbyStorage.compact(model.totalTokens)).monospacedDigit() }.font(.caption2)
                }
            }
            HStack(spacing: 4) {
                if !value.usageComplete { Image(systemName: "exclamationmark.circle"); Text("\(value.reportingHosts)/\(value.hosts.count) 台完整") }
                else { Text("\(value.hosts.count) 台电脑") }
                Spacer(minLength: 0)
                Text(value.updatedAt, style: .time)
            }.font(.system(size: 9)).foregroundStyle(.secondary)
        } else { Text("打开 Fleet AI 登录并同步").font(.caption).foregroundStyle(.secondary) }
    }
}

struct SessionsWidgetView: View {
    let entry: StandbyEntry
    @Environment(\.widgetFamily) private var family
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack { Circle().fill(standbyAccent).frame(width: 5, height: 5); Text("活动与未读").font(.caption.weight(.semibold)); Spacer(); Text("\(entry.snapshot?.sessions.count ?? 0)").font(.caption).foregroundStyle(.secondary) }
            if let value = entry.snapshot, !value.sessions.isEmpty {
                ForEach(0..<min(3, value.sessions.count), id: \.self) { offset in
                    let session = value.sessions[(entry.page + offset) % value.sessions.count]
                    VStack(alignment: .leading, spacing: 3) {
                        HStack(spacing: 4) {
                            Text(session.title.isEmpty ? "新会话" : session.title).font(.system(size: 14, weight: .semibold)).lineLimit(1)
                            Spacer(minLength: 0)
                            Image(systemName: session.waiting ? "circle" : session.running ? "rays" : "circle.fill")
                                .font(.system(size: 9, weight: .semibold)).foregroundStyle(session.waiting ? .orange : standbyAccent)
                                .accessibilityLabel(session.waiting ? "待回应" : session.running ? "进行中" : "未读")
                        }
                        HStack(spacing: 3) {
                            Image(systemName: "display"); Text(session.deviceName).lineLimit(1)
                            if family == .systemMedium { Image(systemName: "folder"); Text(session.project).lineLimit(1) }
                            Spacer(minLength: 0)
                            Text(Date(timeIntervalSince1970: session.activityAt / 1000), style: .relative).monospacedDigit()
                        }.font(.system(size: 9)).foregroundStyle(.secondary)
                    }
                    if offset < min(3, value.sessions.count) - 1 { Divider().opacity(0.3) }
                }
                if value.sessionsPartial { Text("部分电脑暂不可达").font(.system(size: 9)).foregroundStyle(.secondary) }
            } else {
                Spacer()
                Text(entry.snapshot == nil ? "打开 Fleet AI 登录并同步" : "暂无活动或未读会话").font(.caption).foregroundStyle(.secondary)
                Spacer()
            }
        }
        .containerBackground(.background, for: .widget)
        .privacySensitive()
    }
}

struct FleetUsageWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "FleetUsageWidget", provider: StandbyProvider()) { UsageWidgetView(entry: $0) }
            .configurationDisplayName("时钟与模型用量")
            .description("汇总当前账号所有 Mac 今天的模型用量；点击时钟切换数字与指针。")
            .supportedFamilies([.systemSmall, .systemMedium])
    }
}
struct FleetSessionsWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "FleetSessionsWidget", provider: StandbyProvider()) { SessionsWidgetView(entry: $0) }
            .configurationDisplayName("活动与未读会话")
            .description("查看所有 Mac 的活动和未读会话。")
            .supportedFamilies([.systemSmall, .systemMedium])
    }
}
@main
struct FleetWidgets: WidgetBundle {
    var body: some Widget { FleetSessionsWidget(); FleetUsageWidget() }
}
