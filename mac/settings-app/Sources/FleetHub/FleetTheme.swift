import AppKit
import SwiftUI

struct FleetTheme {
    static let controlHeight: CGFloat = 44
    static let controlRadius: CGFloat = 12
    static let compactHeight: CGFloat = 36
    static let compactRadius: CGFloat = 8
    static let cardRadius: CGFloat = 16
    let scheme: ColorScheme
    var background: Color { color(0xEDF1F4, 0x10141B) }
    var navigation: Color { color(0xF7F9FB, 0x181E28) }
    var surface: Color { color(0xFFFFFF, 0x202836) }
    var hover: Color { color(0xD8E2EA, 0x2D394B) }
    var secondarySurface: Color { color(0xE3E9EF, 0x273143) }
    var text: Color { color(0x253446, 0xF2F5F9) }
    var secondaryText: Color { color(0x516476, 0xA5B6CA) }
    var accent: Color { color(0x2C5D87, 0xB8D9FF) }
    var accentContrast: Color { color(0xFFFFFF, 0x111826) }
    var online: Color { color(0x386046, 0xB1E1BC) }
    var warning: Color { color(0x785319, 0xF6D7A3) }
    var danger: Color { color(0xA23B40, 0xFFB8B8) }
    private func color(_ light: UInt32, _ dark: UInt32) -> Color {
        let value = scheme == .dark ? dark : light
        return Color(red: Double((value >> 16) & 255) / 255,
                     green: Double((value >> 8) & 255) / 255, blue: Double(value & 255) / 255)
    }
}

struct FleetButtonStyle: ButtonStyle {
    enum Kind { case primary, secondary, compact, danger }
    let kind: Kind
    @Environment(\.colorScheme) private var scheme
    @Environment(\.isEnabled) private var enabled
    init(_ kind: Kind = .secondary) { self.kind = kind }
    func makeBody(configuration: Configuration) -> some View {
        let theme = FleetTheme(scheme: scheme)
        return configuration.label
            .font(.system(size: 13, weight: .medium))
            .padding(.horizontal, kind == .compact ? 10 : 16)
            .frame(minHeight: kind == .compact ? FleetTheme.compactHeight : FleetTheme.controlHeight)
            .foregroundStyle(kind == .primary ? theme.accentContrast : kind == .danger ? theme.danger : theme.text)
            .background(kind == .primary ? theme.accent : configuration.isPressed ? theme.hover : theme.secondarySurface,
                        in: RoundedRectangle(cornerRadius: kind == .compact ? FleetTheme.compactRadius : FleetTheme.controlRadius))
            .opacity(enabled ? 1 : 0.4)
    }
}

struct FleetBrandMark: View {
    var size: CGFloat = 32
    var body: some View {
        Group {
            if let file = Bundle.main.url(forResource: "BrandMark", withExtension: "png"), let image = NSImage(contentsOf: file) {
                Image(nsImage: image).resizable().renderingMode(.template).scaledToFit()
            } else {
                Image(systemName: "cube").resizable().scaledToFit()
            }
        }
        .frame(width: size, height: size).accessibilityHidden(true)
    }
}
