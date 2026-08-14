// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "hoard-scan",
    platforms: [.macOS(.v14), .iOS(.v18)],
    products: [
        .library(name: "BorderKit", targets: ["BorderKit"]),
        .library(name: "CardKit", targets: ["CardKit"]),
        .library(name: "ScanLink", targets: ["ScanLink"]),
        .library(name: "ScanWire", targets: ["ScanWire"]),
    ],
    targets: [
        .target(name: "ScanWire"),
        .target(name: "BorderKit"),
        .testTarget(name: "BorderKitTests", dependencies: ["BorderKit"]),
        .testTarget(name: "ScanWireTests", dependencies: ["ScanWire"]),
        .target(name: "ScanLink", dependencies: ["ScanWire"]),
        .testTarget(name: "ScanLinkTests", dependencies: ["ScanLink"]),
        .target(name: "CardKit", dependencies: ["ScanWire", "BorderKit"]),
        .testTarget(name: "CardKitTests", dependencies: ["CardKit"]),
        .executableTarget(name: "cardkit-probe",
                          dependencies: ["CardKit", "ScanWire", "BorderKit"]),
    ],
    swiftLanguageModes: [.v5]
)
