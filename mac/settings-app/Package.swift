// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "FleetSettings",
    platforms: [.macOS(.v13)],
    products: [.executable(name: "FleetHub", targets: ["FleetHub"]), .executable(name: "FleetLogin", targets: ["FleetLogin"])],
    targets: [
        .target(name: "FleetCore"),
        .executableTarget(name: "FleetLogin", dependencies: ["FleetCore"]),
        .binaryTarget(name: "Sparkle", path: "Vendor/Sparkle.xcframework"),
        .executableTarget(name: "FleetHub", dependencies: ["FleetCore", "Sparkle"], linkerSettings: [.unsafeFlags(["-Xlinker", "-rpath", "-Xlinker", "@executable_path/../Frameworks"])]),
        .testTarget(name: "FleetCoreTests", dependencies: ["FleetCore"]),
        .testTarget(name: "FleetHubTests", dependencies: ["FleetHub", "FleetCore"])
    ]
)
