// Turning a captured still into pixels Vision can read.

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
