import SwiftUI

enum TitaniumStyle {
    struct Design: Decodable {
        let typography: [String: Double]
        let navigation: [String: Double]
        let radii: [String: Double]
        let light: [String: String]
        let dark: [String: String]
        let deviceColors: [String: DevicePalette]
    }

    struct DevicePalette: Decodable {
        let light: String
        let dark: String
    }

    static let design: Design = {
        guard let url = Bundle.main.url(forResource: "titanium", withExtension: "json"),
              let data = try? Data(contentsOf: url),
              let design = try? JSONDecoder().decode(Design.self, from: data) else {
            preconditionFailure("Missing shared Titanium design resource")
        }
        return design
    }()

    static func radius(_ name: String) -> CGFloat { CGFloat(design.radii[name]!) }
    static func metric(_ name: String) -> CGFloat { CGFloat(design.navigation[name]!) }
    static func font(_ name: String, weight: Font.Weight = .regular) -> Font {
        .system(size: design.typography[name]!, weight: weight)
    }
    static var selection: Color { color("accent").opacity(0.11) }

    static func color(_ name: String) -> Color {
        Color(uiColor: UIColor { traits in
            let palette = traits.userInterfaceStyle == .dark ? design.dark : design.light
            let value = UInt(palette[name]!.dropFirst(), radix: 16)!
            return UIColor(red: CGFloat((value >> 16) & 255) / 255,
                           green: CGFloat((value >> 8) & 255) / 255,
                           blue: CGFloat(value & 255) / 255, alpha: 1)
        })
    }

    static func deviceColor(_ name: String?) -> Color {
        let pair = design.deviceColors[name ?? "steel"] ?? design.deviceColors["steel"]!
        return Color(uiColor: UIColor { traits in
            let value = UInt((traits.userInterfaceStyle == .dark ? pair.dark : pair.light).dropFirst(), radix: 16)!
            return UIColor(red: CGFloat((value >> 16) & 255) / 255,
                           green: CGFloat((value >> 8) & 255) / 255,
                           blue: CGFloat(value & 255) / 255, alpha: 1)
        })
    }
}

struct TitaniumIconButtonStyle: ButtonStyle {
    var size: CGFloat = 44
    var foreground = "text-1"
    var background: Color = .clear
    var radius = "control"

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 16, weight: .regular))
            .frame(width: size, height: size)
            .contentShape(Rectangle())
            .foregroundStyle(TitaniumStyle.color(foreground))
            .background(configuration.isPressed ? TitaniumStyle.color("surface-hover") : background,
                        in: RoundedRectangle(cornerRadius: TitaniumStyle.radius(radius)))
    }
}
