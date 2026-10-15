import SwiftUI

struct FleetStyle {
    let tokens: [String: String]

    init(tokens: [String: String] = [:]) {
        self.tokens = tokens.filter { name, value in
            TitaniumStyle.design.light[name]?.hasPrefix("#") == true &&
                value.range(of: "^#[0-9a-fA-F]{6}$", options: .regularExpression) != nil
        }
    }

    func color(_ name: String) -> Color {
        guard let token = tokens[name], let value = UInt(token.dropFirst(), radix: 16) else {
            return TitaniumStyle.color(name)
        }
        return Color(.sRGB, red: Double((value >> 16) & 255) / 255,
                     green: Double((value >> 8) & 255) / 255, blue: Double(value & 255) / 255, opacity: 1)
    }

    var selection: Color { color("accent").opacity(0.11) }
}

private struct FleetStyleKey: EnvironmentKey {
    static let defaultValue = FleetStyle()
}

extension EnvironmentValues {
    var fleetStyle: FleetStyle {
        get { self[FleetStyleKey.self] }
        set { self[FleetStyleKey.self] = newValue }
    }
}

struct FleetDeviceIcon: View {
    @Environment(\.fleetStyle) private var style
    let device: FleetDevice
    var size: CGFloat = 22
    var monochrome = false

    var body: some View {
        Group {
            if device.icon == "text", let text = device.text,
               text.range(of: "^[A-Za-z0-9]{1,4}$", options: .regularExpression) != nil {
                Text(text).font(.system(size: monochrome ? size : size * (text.count > 2 ? 0.5 : 0.7),
                                       weight: monochrome ? .regular : .semibold, design: monochrome ? .default : .monospaced))
                    .minimumScaleFactor(0.5).lineLimit(1).accessibilityLabel(text)
            } else {
                Image(systemName: device.symbol).font(.system(size: monochrome ? size : size - 2))
            }
        }
        .frame(width: monochrome && device.icon == "text" ? nil : size, height: size)
        .foregroundStyle(monochrome ? style.color("text-1") : TitaniumStyle.deviceColor(device.color))
        .saturation(device.online ? 1 : 0).opacity(device.online ? 1 : 0.45)
    }
}
