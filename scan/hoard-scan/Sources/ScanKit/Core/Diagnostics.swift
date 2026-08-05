// Tracing. Four env-gated stderr channels and the debug-frame dump, kept
// together because they share one shape and nothing downstream parses any of
// them — except that HOARD_SCAN_LOG tees them, so the prefixes are stable.

import CoreGraphics
import Foundation
import ImageIO

/// multiDebug traces the multi-card decisions to stderr when asked. Purely
/// diagnostic: nothing downstream parses these lines.
func multiDebug(_ s: String) {
    guard ProcessInfo.processInfo.environment["HOARD_SCAN_MULTI"] != nil else { return }
    FileHandle.standardError.write(Data("multi: \(s)\n".utf8))
}

/// saveDebugImage writes an image to $HOARD_SCAN_DEBUG_DIR when that's set, so a
/// scan that reads the wrong line can be reproduced offline with --image.
func saveDebugImage(_ cg: CGImage, _ filename: String) {
    guard let dir = ProcessInfo.processInfo.environment["HOARD_SCAN_DEBUG_DIR"],
          !dir.isEmpty else { return }
    let url = URL(fileURLWithPath: dir).appendingPathComponent(filename)
    guard let dest = CGImageDestinationCreateWithURL(url as CFURL, "public.png" as CFString, 1, nil)
    else { return }
    CGImageDestinationAddImage(dest, cg, nil)
    CGImageDestinationFinalize(dest)
    FileHandle.standardError.write(Data("hoard-scan: wrote \(url.path)\n".utf8))
}

/// borderDebug traces the border decision to stderr when asked, the way
/// multiDebug does for the multi-card pass. Purely diagnostic; nothing
/// downstream parses these.
func borderDebug(_ s: String) {
    guard ProcessInfo.processInfo.environment["HOARD_SCAN_BORDER"] != nil else { return }
    FileHandle.standardError.write(Data("border: \(s)\n".utf8))
}

/// autoDebug traces the auto-trigger's decisions to stderr when asked, mirroring
/// multiDebug. Purely diagnostic: nothing downstream parses these lines.
func autoDebug(_ s: @autoclosure () -> String) {
    guard ProcessInfo.processInfo.environment["HOARD_SCAN_AUTO"] != nil else { return }
    FileHandle.standardError.write(Data("auto: \(s())\n".utf8))
}

/// timing writes an always-on per-capture cost line to stderr. HOARD_SCAN_LOG
/// timestamps and tees stderr, so every telemetry run carries its own latency
/// breakdown without a knob — the cost is a couple of lines per card. Nothing
/// downstream parses these; they exist so a "the scanner feels slow" report
/// comes with numbers attached.
func timing(_ s: @autoclosure () -> String) {
    FileHandle.standardError.write(Data("timing: \(s())\n".utf8))
}

/// msSince is the whole milliseconds elapsed from a mark, for timing lines.
func msSince(_ mark: Date) -> Int { Int(Date().timeIntervalSince(mark) * 1000) }
