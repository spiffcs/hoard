// Turning a captured still into pixels Vision can read.

// macOS only. This is the camera, window and HUD half of ScanKit; the read
// pipeline under Core/ is what compiles for iOS. See Package.swift.
#if os(macOS)

import AVFoundation
import CoreGraphics
import ImageIO

/// decodePhoto turns a captured photo into a CGImage plus the orientation Vision
/// should read it at. macOS doesn't expose `AVCapturePhoto.metadata`, so the
/// orientation is recovered from the encoded file representation; if that isn't
/// available the raw representation is assumed upright.
func decodePhoto(_ photo: AVCapturePhoto) -> (CGImage, CGImagePropertyOrientation)? {
    if let data = photo.fileDataRepresentation(),
       let src = CGImageSourceCreateWithData(data as CFData, nil),
       let cg = CGImageSourceCreateImageAtIndex(src, 0, nil) {
        let props = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any]
        let raw = props?[kCGImagePropertyOrientation] as? UInt32 ?? 1
        return (cg, CGImagePropertyOrientation(rawValue: raw) ?? .up)
    }
    if let cg = photo.cgImageRepresentation() { return (cg, .up) }
    return nil
}

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
