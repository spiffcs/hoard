// swift-tools-version: 6.0
//
// The macOS camera-scan helper. Its own package rooted here rather than at the
// repo root, so SwiftPM never sees scan/fixtures, scan/corpus or the Go module,
// and so Info.plist and hoard-scan.icns keep the paths build-scan.sh expects.
//
// Language mode is deliberately v5. The helper's design is main-thread
// confinement plus one serial analysis queue, expressed with global `let`
// services (a shared CIContext, seven compiled NSRegularExpressions) that Swift
// 6's strict concurrency checking rejects outright. Moving to v6 is a real
// project with its own testing burden, not a side effect of reorganizing files.
//
// Everything lives in the ScanKit library and the executable is a shell that
// calls runCLI(). That split exists for one reason: a target with top-level
// code cannot be linked into a test bundle, so the tests would otherwise be
// locked out of the whole helper. ScanKit's symbols stay `internal` and the
// tests reach them with @testable — only runCLI() is public.
//
// Inside ScanKit, Core/ must not depend on App/: Core is the read pipeline
// (parser, border reader, trigger) and App is camera, window and HUD. That
// direction is a convention here rather than a compiler rule, checked by
// ScanKitTests.testCoreDoesNotImportUIFrameworks.

import PackageDescription

let package = Package(
    name: "hoard-scan",
    platforms: [.macOS(.v14)],
    targets: [
        .target(name: "ScanKit"),
        .executableTarget(name: "hoard-scan", dependencies: ["ScanKit"]),
        .testTarget(name: "ScanKitTests", dependencies: ["ScanKit"]),
    ],
    swiftLanguageModes: [.v5]
)
