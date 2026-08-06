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
//
// iOS is declared because Core/ is the read pipeline for the iPhone capture
// head too — it imports only Foundation, CoreGraphics, Vision, ImageIO,
// CoreVideo and CoreImage, all of which exist on both platforms, and the
// layering test is what has quietly kept that true. App/ is fenced off with
// `#if os(macOS)` rather than split into a second target: the split is the
// better design and costs ~140 `public` annotations on interfaces that are
// still moving, so it stays deferred. App/RunLoopPump.swift is the one
// deliberate exception, being Foundation and a deadline.
//
// The executable target is macOS-only in effect — runCLI() and main.swift are
// both behind the same fence, so an iOS build of it links an empty main.

import PackageDescription

let package = Package(
    name: "hoard-scan",
    platforms: [.macOS(.v14), .iOS(.v18)],
    products: [
        // The iPhone capture head is a separate Xcode target (see
        // scan/hoard-scan-ios), so ScanKit has to be consumable from outside
        // this package. Only ScanPublic.swift is public; everything else stays
        // internal and reachable from the tests with @testable.
        .library(name: "ScanKit", targets: ["ScanKit"]),
        .library(name: "BorderKit", targets: ["BorderKit"]),
        // The iPhone app links these three: the read pipeline, the link to the
        // Mac, and the protocol they both speak.
        .library(name: "CardKit", targets: ["CardKit"]),
        .library(name: "ScanLink", targets: ["ScanLink"]),
        .library(name: "ScanWire", targets: ["ScanWire"]),
    ],
    targets: [
        // The NDJSON contract with the Go side, and nothing else. Two pipelines
        // speak it now — ScanKit's Continuity path and CardKit's iPhone head,
        // which share no other code — so it gets one definition rather than one
        // per pipeline. Foundation only: no camera, no window, no read pipeline.
        .target(name: "ScanWire"),
        // The border reader, and the only thing the Mac and the phone share.
        // Both pipelines had one; the phone's read the perspective crop and
        // could not tell a white border from the card's inner frame when the
        // quad landed inside the card's cut edge — which is what a white
        // border on a light desk provokes. This is the one that anchors on
        // text instead, so neither pipeline keeps a copy.
        .target(name: "BorderKit"),
        .testTarget(name: "BorderKitTests", dependencies: ["BorderKit"]),
        .testTarget(name: "ScanWireTests", dependencies: ["ScanWire"]),
        // Network.framework plumbing for the link to the phone. Separate from
        // ScanWire because that target promises Foundation and the shape of a
        // JSON line, and a socket is neither; separate from ScanKit and CardKit
        // because both ends of the link need it.
        .target(name: "ScanLink", dependencies: ["ScanWire"]),
        // Loopback tests: a real listener, a real browser and a real TLS-PSK
        // handshake in one process. Slower than the rest of the suite and worth
        // it — a handshake that silently never completes is the failure mode
        // here, and a mock reproduces none of them.
        .testTarget(name: "ScanLinkTests", dependencies: ["ScanLink"]),
        // ScanKit depends on ScanLink for one thing: RemoteController, the
        // backend where the camera is an iPhone app rather than a Continuity
        // Camera. The read pipeline under Core/ still touches neither.
        .target(name: "ScanKit", dependencies: ["ScanWire", "ScanLink", "BorderKit"]),
        .executableTarget(name: "hoard-scan", dependencies: ["ScanKit"]),
        .testTarget(name: "ScanKitTests", dependencies: ["ScanKit"]),
        // CardKit is the iPhone head's read pipeline, written from scratch
        // rather than ported. It shares no code with ScanKit — only ScanWire,
        // which is the Go side's protocol and must not fork. Same package
        // purely so one `swift test` covers both and the corpus harness can
        // build a macOS binary for it.
        .target(name: "CardKit", dependencies: ["ScanWire", "BorderKit"]),
        .testTarget(name: "CardKitTests", dependencies: ["CardKit"]),
        // A macOS shim so CardKit can be scored against scan/corpus's 231
        // labelled images with the harness that already exists: it speaks the
        // same event shape, so `HELPER=... ./scan/corpus/sweep.sh` reports the
        // new pipeline's numbers in exactly the table the old one is measured
        // in. Rewriting from scratch is only defensible if it can be compared.
        .executableTarget(name: "cardkit-probe", dependencies: ["CardKit", "ScanWire"]),
    ],
    swiftLanguageModes: [.v5]
)
