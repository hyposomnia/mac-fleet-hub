import SwiftUI

struct FleetNavigationButton: View {
    let page: SettingsPage
    let selected: Bool
    let action: () -> Void
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let theme = FleetTheme(scheme: scheme)
        Button(action: action) {
            HStack(spacing: 11) {
                Image(systemName: page.symbol)
                    .font(.system(size: 16, weight: .medium))
                    .symbolRenderingMode(.hierarchical)
                    .foregroundStyle(theme.iconColor(for: page))
                    .frame(width: 22)
                Text(page.rawValue)
                    .font(.system(size: 13, weight: selected ? .semibold : .medium))
            }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 14).frame(height: FleetTheme.controlHeight)
                .contentShape(Rectangle())
                .foregroundStyle(selected ? theme.accent : theme.text)
                .background(selected ? theme.accent.opacity(0.07) : .clear, in: RoundedRectangle(cornerRadius: FleetTheme.controlRadius))
        }
        .buttonStyle(.plain).accessibilityAddTraits(selected ? .isSelected : [])
    }
}
