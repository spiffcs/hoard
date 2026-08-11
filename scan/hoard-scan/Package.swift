// swift-tools-version: 6.0
//
// The card scanner's Swift half — which is now the *phone's* half, and only
// that. Its own package rooted here rather than at the repo root, so SwiftPM
// never sees scan/fixtures, scan/corpus or the Go module.
//
// The camera is an iPhone running the app in scan/hoard-scan-ios. It captures,
// reads the card with CardKit, and sends finished events over the link.
//
// **There is no Mac end here any more.** A `ScanKit` target and a `hoard-scan`
// executable used to sit beside these, assembled into a `bin/hoard-scan.app`
// that browsed for the phone, held the TLS session, and translated its frames
// into NDJSON on stdout for the Go parent to read off a pipe. `hoard` now does
// all of that itself in `internal/scan/link`, so the helper, its bundle and its
// build script are gone.
//
// Language mode is deliberately v5. The design is main-thread confinement plus
// one serial analysis queue, expressed with global `let` services that Swift 6's
// strict concurrency checking rejects outright. Moving to v6 is a real project
// with its own testing burden, not a side effect of reorganizing files.
//
// Every target here is linked by the phone (CardKit, BorderKit, ScanLink,
// ScanWire) or is the macOS harness that runs CardKit over an image file
// (cardkit-probe), which is why iOS and macOS are both declared.

import PackageDescription

let package = Package(
    name: "hoard-scan",
    platforms: [.macOS(.v14), .iOS(.v18)],
    products: [
        .library(name: "BorderKit", targets: ["BorderKit"]),
        // The iPhone app links these three: the read pipeline, the link to
        // hoard, and the protocol they both speak.
        .library(name: "CardKit", targets: ["CardKit"]),
        .library(name: "ScanLink", targets: ["ScanLink"]),
        .library(name: "ScanWire", targets: ["ScanWire"]),
    ],
    targets: [
        // The NDJSON contract with the Go side, and nothing else. Its Go
        // counterpart is internal/scan/scan.go, and the two must not fork —
        // internal/scan/link/testdata pins the framing and pairing values
        // against vectors generated from this target. Foundation only: no
        // camera, no window, no read pipeline.
        .target(name: "ScanWire"),
        // The border reader. Split out from CardKit because it anchors on text
        // rather than on the perspective crop, which is what let it survive a
        // white border on a light desk when a crop-relative reader could not
        // tell the card's edge from its inner frame.
        .target(name: "BorderKit"),
        .testTarget(name: "BorderKitTests", dependencies: ["BorderKit"]),
        .testTarget(name: "ScanWireTests", dependencies: ["ScanWire"]),
        // Network.framework plumbing for the phone's end of the link: it
        // advertises _hoardscan._tcp, accepts sessions and checks the pairing
        // proof. Separate from ScanWire because that target promises Foundation
        // and the shape of a JSON line, and a socket is neither. hoard's end is
        // Go, in internal/scan/link.
        .target(name: "ScanLink", dependencies: ["ScanWire"]),
        // Loopback tests: a real listener, a real browser and a real TLS-PSK
        // handshake in one process. Slower than the rest of the suite and worth
        // it — a handshake that silently never completes is the failure mode
        // here, and a mock reproduces none of them.
        .testTarget(name: "ScanLinkTests", dependencies: ["ScanLink"]),
        // CardKit is the iPhone head's read pipeline. Same package as the link
        // purely so one `swift test` covers both and the corpus harness can
        // build a macOS binary for it.
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
