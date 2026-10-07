import SwiftUI

struct FleetNavigationButton: View {
    let page: SettingsPage
    let selected: Bool
    let action: () -> Void
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let theme = FleetTheme(scheme: scheme)
        Button(action: action) {
            Label(page.rawValue, systemImage: page.symbol)
                .font(.system(size: 13, weight: selected ? .semibold : .regular))
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 14).frame(height: FleetTheme.controlHeight)
                .contentShape(Rectangle())
                .foregroundStyle(selected ? theme.accent : theme.secondaryText)
                .background(selected ? theme.surface : .clear, in: RoundedRectangle(cornerRadius: FleetTheme.controlRadius))
        }
        .buttonStyle(.plain).accessibilityAddTraits(selected ? .isSelected : [])
    }
}
