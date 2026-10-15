import SwiftUI

struct SettingsStartupScreen: View {
    let state: SettingsStartup.State
    let applicationName: String
    let retry: () -> Void
    let inspect: () -> Void
    @Environment(\.colorScheme) private var scheme
    private var theme: FleetTheme { FleetTheme(scheme: scheme) }

    var body: some View {
        VStack(spacing: 0) {
            Spacer(minLength: 32)
            VStack(spacing: 28) {
                FleetBrandMark(size: 64).foregroundStyle(theme.accent)
                VStack(spacing: 12) {
                    Text(state.error == nil ? "正在检查" : "检查未完成")
                        .font(.system(size: 28, weight: .semibold, design: .rounded))
                        .foregroundStyle(theme.text)
                    if let error = state.error {
                        ViewThatFits(in: .vertical) {
                            Text(error).textSelection(.enabled)
                                .fixedSize(horizontal: false, vertical: true)
                            ScrollView {
                                Text(error).textSelection(.enabled).frame(maxWidth: .infinity)
                            }
                        }
                        .frame(maxHeight: 120).fixedSize(horizontal: false, vertical: true)
                        .foregroundStyle(theme.secondaryText)
                    } else {
                        Text("正在确认后台与本机设置")
                            .foregroundStyle(theme.secondaryText)
                    }
                }
                .font(.system(size: 14)).multilineTextAlignment(.center)
                if case .checking(let current) = state {
                    HStack(alignment: .top, spacing: 0) {
                        ForEach(SettingsStartup.Stage.allCases, id: \.rawValue) { stage in
                            stageIndicator(stage, current: current)
                            if stage != .status {
                                Rectangle().fill(theme.accent.opacity(stage.rawValue < current.rawValue ? 0.45 : 0.12))
                                    .frame(width: 24, height: 1).padding(.top, 11)
                            }
                        }
                    }
                    .padding(.top, 8)
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel("正在检查，\(current.title)")
                } else if state.error != nil {
                    HStack(spacing: 20) {
                        Button(action: retry) {
                            Text("重新检查").fontWeight(.semibold).foregroundStyle(.white)
                                .frame(minWidth: 136, minHeight: FleetTheme.controlHeight)
                                .background(theme.accent, in: RoundedRectangle(cornerRadius: FleetTheme.controlRadius))
                        }
                        .buttonStyle(.plain).keyboardShortcut(.defaultAction)
                        Button("进入设置", action: inspect).buttonStyle(.link).foregroundStyle(theme.accent)
                            .frame(minHeight: FleetTheme.controlHeight)
                    }
                }
            }
            .frame(maxWidth: 420)
            Spacer(minLength: 32)
            Text(applicationName).font(.system(size: 12, weight: .medium))
                .foregroundStyle(theme.secondaryText)
        }
        .padding(40)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(theme.background).tint(theme.accent)
    }

    private func stageIndicator(_ stage: SettingsStartup.Stage, current: SettingsStartup.Stage) -> some View {
        VStack(spacing: 12) {
            Group {
                if stage.rawValue < current.rawValue {
                    Image(systemName: "checkmark.circle.fill").font(.system(size: 21))
                        .foregroundStyle(theme.accent)
                } else if stage == current {
                    ProgressView().controlSize(.small).tint(theme.accent)
                } else {
                    Circle().strokeBorder(theme.secondaryText.opacity(0.22), lineWidth: 1.5)
                        .frame(width: 17, height: 17)
                }
            }
            .frame(width: 24, height: 24)
            Text(stage.title).font(.system(size: 12, weight: stage == current ? .semibold : .regular))
                .foregroundStyle(stage == current ? theme.text : theme.secondaryText)
        }
        .frame(width: 100)
    }
}
