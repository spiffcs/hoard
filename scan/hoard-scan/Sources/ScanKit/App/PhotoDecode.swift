// Turning bytes the phone sent into pixels, and keeping the ones worth keeping.

// macOS only. ScanKit is the Mac end of the link: the translator, and the
// optional mirror window it can draw. See Package.swift.
#if os(macOS)

import CoreGraphics
import Foundation
import ImageIO

/// decodeJPEG turns a preview frame's bytes into an image.
///
/// CGImageSource rather than NSImage: the frames arrive from a phone at ten a
/// second, and NSImage would defer the decode to draw time and do it on the
/// main thread while the window is trying to composite.
func decodeJPEG(_ data: Data) -> CGImage? {
    guard let src = CGImageSourceCreateWithData(data as CFData, nil) else { return nil }
    return CGImageSourceCreateImageAtIndex(src, 0, nil)
}

/// saveRemoteStill writes a full-resolution still the phone sent to
/// HOARD_SCAN_DEBUG_DIR, which is how the iOS fixture set gets built. Silently
/// does nothing when no debug directory is set, like every other diagnostic.
func saveRemoteStill(_ data: Data) {
    guard let dir = ProcessInfo.processInfo.environment["HOARD_SCAN_DEBUG_DIR"],
          !dir.isEmpty
    else { return }
    let name = "remote-still-\(Int(Date().timeIntervalSince1970 * 1000)).jpg"
    try? data.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name))
}

#endif
