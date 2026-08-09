// swift-tools-version: 6.0
//
// The card scanner's Swift half. Its own package rooted here rather than at the
// repo root, so SwiftPM never sees scan/fixtures, scan/corpus or the Go module,
// and so Info.plist and hoard-scan.icns keep the paths build-scan.sh expects.
//
// The camera is an iPhone running the app in scan/hoard-scan-ios. It captures,
// reads the card with CardKit, and sends finished events to the Mac; ScanKit is
// the Mac end of that link and owns no camera at all. There used to be a second
// backend here — a local Continuity Camera, with its own read pipeline under
// ScanKit/Core/ — and it is gone. One camera, one reader.
//
// Language mode is deliberately v5. The design is main-thread confinement plus
// one serial analysis queue, expressed with global `let` services that Swift 6's
// strict concurrency checking rejects outright. Moving to v6 is a real project
// with its own testing burden, not a side effect of reorganizing files.
//
// ScanKit is a library and the executable is a shell that calls runCLI(). That
// split exists for one reason: a target with top-level code cannot be linked
// into a test bundle. Both are macOS-only in effect — runCLI() and main.swift
// sit behind the same `#if os(macOS)`, so an iOS build of the executable links
// an empty main.
//
// iOS is declared for the four targets the phone actually links: CardKit,
// BorderKit, ScanLink and ScanWire.

import PackageDescription

let package = Package(
    name: "hoard-scan",
    platforms: [.macOS(.v14), .iOS(.v18)],
    products: [
        .library(name: "BorderKit", targets: ["BorderKit"]),
        // The iPhone app links these three: the read pipeline, the link to the
        // Mac, and the protocol they both speak. ScanKit is deliberately not
        // among them — it is the Mac's end of the wire and nothing outside this
        // package consumes it.
        .library(name: "CardKit", targets: ["CardKit"]),
        .library(name: "ScanLink", targets: ["ScanLink"]),
        .library(name: "ScanWire", targets: ["ScanWire"]),
    ],
    targets: [
        // The NDJSON contract with the Go side, and nothing else. Both ends
        // speak it — CardKit's read pipeline on the phone and ScanKit's
        // translator on the Mac — so it gets one definition rather than one per
        // end. Foundation only: no camera, no window, no read pipeline.
        .target(name: "ScanWire"),
        // The border reader. Split out from CardKit because it anchors on text
        // rather than on the perspective crop, which is what let it survive a
        // white border on a light desk when a crop-relative reader could not
        // tell the card's edge from its inner frame.
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
        // ScanKit is the Mac end of the link and only that: it translates the
        // phone's frames into NDJSON on stdout and the parent's stdin verbs back
        // into frames, plus the optional `--mirror` window. No camera, no read
        // pipeline — which is why it needs neither AVFoundation nor BorderKit.
        .target(name: "ScanKit", dependencies: ["ScanWire", "ScanLink"]),
        // ScanKit's sources are all behind `#if os(macOS)`, so on an iOS
        // destination this bundle compiles to nothing and passes trivially —
        // which is correct, and is why `task scan-ios-test` can keep running
        // the whole package against the simulator. The tests themselves carry
        // the same fence for the same reason.
        .testTarget(name: "ScanKitTests", dependencies: ["ScanKit"]),
        .executableTarget(name: "hoard-scan", dependencies: ["ScanKit"]),
        // CardKit is the iPhone head's read pipeline. It shares no code with the
        // Mac side beyond ScanWire, which is the Go side's protocol and must not
        // fork. Same package purely so one `swift test` covers both and the
        // corpus harness can build a macOS binary for it.
        .target(name: "CardKit", dependencies: ["ScanWire", "BorderKit"]),
        .testTarget(name: "CardKitTests", dependencies: ["CardKit"]),
        // A macOS shim so CardKit can be run over an image file from the command
        // line: it scores scan/corpus's labelled images and replays
        // scan/fixtures' frames against their goldens, which is what
        // `make cardkit-score` and `make scan-check` drive.
        // BorderKit is named explicitly even though CardKit re-exports it: the
        // foil-sparkle harness drives BorderKit's reader directly, and a
        // dependency that only works because something else happens to
        // re-export it is one refactor away from breaking.
        .executableTarget(name: "cardkit-probe",
                          dependencies: ["CardKit", "ScanWire", "BorderKit"]),
    ],
    swiftLanguageModes: [.v5]
)
