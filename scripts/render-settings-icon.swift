import AppKit
import Foundation

let source = try String(contentsOfFile: CommandLine.arguments[1], encoding: .utf8)
let destination = URL(fileURLWithPath: CommandLine.arguments[2], isDirectory: true)
let pattern = try NSRegularExpression(pattern: "<path d=\"([^\"]+)\"")
let paths = try pattern.matches(in: source, range: NSRange(source.startIndex..., in: source)).map { match -> CGPath in
    let geometry = String(source[Range(match.range(at: 1), in: source)!])
    let tokenizer = try NSRegularExpression(pattern: "[MLQZ]|-?[0-9]+(?:\\.[0-9]+)?")
    let tokens = tokenizer.matches(in: geometry, range: NSRange(geometry.startIndex..., in: geometry)).map {
        String(geometry[Range($0.range, in: geometry)!])
    }
    var cursor = 0
    let path = CGMutablePath()
    func point() -> CGPoint {
        defer { cursor += 2 }
        return CGPoint(x: Double(tokens[cursor])!, y: Double(tokens[cursor + 1])!)
    }
    while cursor < tokens.count {
        let command = tokens[cursor]
        cursor += 1
        switch command {
        case "M": path.move(to: point())
        case "L": path.addLine(to: point())
        case "Q": let control = point(); path.addQuadCurve(to: point(), control: control)
        case "Z": path.closeSubpath()
        default: throw CocoaError(.fileReadCorruptFile)
        }
    }
    return path
}
guard paths.count == 3 else { throw CocoaError(.fileReadCorruptFile) }
let inkBounds = paths.reduce(CGRect.null) { $0.union($1.boundingBoxOfPath) }
try FileManager.default.createDirectory(at: destination, withIntermediateDirectories: true)
for (filename, pixels, icon) in [("AppIcon.png", 1024, true), ("BrandMark.png", 256, false)] {
    let context = CGContext(data: nil, width: pixels, height: pixels, bitsPerComponent: 8, bytesPerRow: 0,
                            space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    if icon {
        context.setFillColor(CGColor(red: 237 / 255, green: 242 / 255, blue: 246 / 255, alpha: 1))
        context.fill(CGRect(x: 0, y: 0, width: pixels, height: pixels))
    }
    context.translateBy(x: 0, y: CGFloat(pixels))
    context.scaleBy(x: 1, y: -1)
    if icon {
        let scale = CGFloat(pixels) * 0.78 / max(inkBounds.width, inkBounds.height)
        context.translateBy(x: (CGFloat(pixels) - inkBounds.width * scale) / 2,
                            y: (CGFloat(pixels) - inkBounds.height * scale) / 2)
        context.scaleBy(x: scale, y: scale)
        context.translateBy(x: -inkBounds.minX, y: -inkBounds.minY)
    } else {
        context.scaleBy(x: CGFloat(pixels) / 96, y: CGFloat(pixels) / 96)
    }
    context.setFillColor(CGColor(red: 44 / 255, green: 93 / 255, blue: 135 / 255, alpha: 1))
    for path in paths { context.addPath(path); context.fillPath() }
    let bitmap = NSBitmapImageRep(cgImage: context.makeImage()!)
    try bitmap.representation(using: .png, properties: [:])!.write(to: destination.appendingPathComponent(filename))
}
