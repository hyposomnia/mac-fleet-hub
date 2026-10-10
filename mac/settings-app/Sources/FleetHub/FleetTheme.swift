import AppKit
import SwiftUI

struct FleetTheme {
    static let controlHeight: CGFloat = 44
    static let controlRadius: CGFloat = 8
    let scheme: ColorScheme
    var background: Color { .white }
    var navigation: Color { background }
    var text: Color { color(0x262C36, 0xF4F5F7) }
    var secondaryText: Color { color(0x6A717C, 0xAEB6C2) }
    var accent: Color { color(0x426BB9, 0xB8D9FF) }
    var online: Color { color(0x247A6A, 0xB1E1BC) }
    var warning: Color { color(0x9B6A25, 0xF6D7A3) }
    var danger: Color { color(0xB54B51, 0xFFB8B8) }
    func iconColor(for page: SettingsPage) -> Color {
        switch page {
        case .overview: return online
        case .connection, .about: return accent
        case .privacy: return warning
        case .preferences: return color(0x79639A, 0xD1BFE8)
        }
    }
    private func color(_ light: UInt32, _ dark: UInt32) -> Color {
        let value = scheme == .dark ? dark : light
        return Color(red: Double((value >> 16) & 255) / 255,
                     green: Double((value >> 8) & 255) / 255, blue: Double(value & 255) / 255)
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
